package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/VBCDX/keyhole/internal/auth/oidctest"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// oidcFlow drives the browser through sign-in up to (not including) the
// callback, returning the callback URL the provider redirected to.
func (h *harness) oidcFlow(c *http.Client) string {
	h.t.Helper()
	res := h.do(c, http.MethodGet, "/auth/oidc/login", nil, nil)
	if res.StatusCode != http.StatusFound {
		h.t.Fatalf("/auth/oidc/login = %d", res.StatusCode)
	}
	authz, err := url.Parse(res.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(authz.String(), h.idp.URL+"/authorize") {
		h.t.Fatalf("login redirected to %q, want the provider", res.Header.Get("Location"))
	}
	q := authz.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" || q.Get("state") == "" {
		h.t.Fatalf("authorization request lacks PKCE S256, nonce or state: %v", q)
	}
	res = h.do(c, http.MethodGet, authz.String(), nil, nil)
	if res.StatusCode != http.StatusFound {
		h.t.Fatalf("provider authorize = %d", res.StatusCode)
	}
	return res.Header.Get("Location")
}

// Acceptance criterion 2: the full code flow against a fake provider.
func TestZitadelSignInAndSignOut(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthZitadel, nil)
		c := h.client()

		callback := h.oidcFlow(c)
		if !strings.HasPrefix(callback, h.ts.URL+"/auth/oidc/callback?") {
			t.Fatalf("provider redirected to %q", callback)
		}
		res := h.do(c, http.MethodGet, callback, nil, nil)
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/" {
			t.Fatalf("callback = %d → %q, want 302 → /", res.StatusCode, res.Header.Get("Location"))
		}

		me, status := h.me(c)
		if status != http.StatusOK || me.Method != "zitadel" || me.User.Email != "zed@example.com" || me.User.Name != "Zed Example" {
			t.Fatalf("me = %d %+v; want the provider's user (profile filled from userinfo)", status, me)
		}

		// Second sign-in by the same subject is the same Keyhole user.
		c2 := h.client()
		h.do(c2, http.MethodGet, h.oidcFlow(c2), nil, nil)
		me2, _ := h.me(c2)
		if me2.User.ID != me.User.ID {
			t.Fatalf("same issuer+subject produced a second user: %s vs %s", me2.User.ID, me.User.ID)
		}

		redirect, status := h.logout(c, me.CSRFToken)
		if status != http.StatusOK {
			t.Fatalf("logout = %d", status)
		}
		end, err := url.Parse(redirect)
		if err != nil || !strings.HasPrefix(redirect, h.idp.URL+"/end_session?") {
			t.Fatalf("logout redirect = %q; want the provider's end-session endpoint", redirect)
		}
		eq := end.Query()
		if eq.Get("id_token_hint") == "" || eq.Get("post_logout_redirect_uri") != h.ts.URL+"/" || eq.Get("client_id") != h.idp.ClientID {
			t.Fatalf("end-session parameters = %v", eq)
		}
		if _, status := h.me(c); status != http.StatusUnauthorized {
			t.Fatalf("me after sign-out = %d", status)
		}
		if _, status := h.me(c2); status != http.StatusOK {
			t.Fatal("signing out one browser ended another browser's session")
		}
	})
}

func TestZitadelProfileInIDToken(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	h.idp.IncludeProfileInIDToken = true
	h.idp.SetUser(oidctest.User{Subject: "s-2", Email: "id@example.com", Name: "From Token"})
	c := h.client()
	h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil)
	if me, status := h.me(c); status != http.StatusOK || me.User.Name != "From Token" {
		t.Fatalf("me = %d %+v", status, me)
	}
}

func assertRejected(t *testing.T, h *harness, c *http.Client, res *http.Response) {
	t.Helper()
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/?error=signin_failed" {
		t.Fatalf("callback = %d → %q; want 302 → /?error=signin_failed", res.StatusCode, res.Header.Get("Location"))
	}
	for _, ck := range res.Cookies() {
		if ck.Name == h.srv.cookieName() && ck.MaxAge >= 0 {
			t.Fatal("a rejected callback set a session cookie")
		}
	}
	if _, status := h.me(c); status != http.StatusUnauthorized {
		t.Fatalf("me after a rejected callback = %d, want 401", status)
	}
	if n, _ := h.st.CountUsers(t.Context()); n != 0 {
		t.Fatalf("a rejected callback created %d user(s)", n)
	}
}

func TestZitadelRejectsBadState(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	c := h.client()
	cb, _ := url.Parse(h.oidcFlow(c))
	q := cb.Query()
	q.Set("state", "attacker-state")
	cb.RawQuery = q.Encode()
	assertRejected(t, h, c, h.do(c, http.MethodGet, cb.String(), nil, nil))
}

func TestZitadelRejectsBadNonce(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	h.idp.IDTokenClaims = func(claims map[string]any) { claims["nonce"] = "replayed-nonce" }
	c := h.client()
	assertRejected(t, h, c, h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil))
}

func TestZitadelRejectsMissingNonce(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	h.idp.IDTokenClaims = func(claims map[string]any) { delete(claims, "nonce") }
	c := h.client()
	assertRejected(t, h, c, h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil))
}

func TestZitadelRejectsTokenForAnotherClient(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	h.idp.IDTokenClaims = func(claims map[string]any) { claims["aud"] = "some-other-app" }
	c := h.client()
	assertRejected(t, h, c, h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil))
}

func TestZitadelRejectsWrongIssuer(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	h.idp.IDTokenClaims = func(claims map[string]any) { claims["iss"] = "https://evil.example" }
	c := h.client()
	assertRejected(t, h, c, h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil))
}

// A callback URL used in a browser that never started the sign-in (login
// CSRF / a leaked code) must fail: there is no flow cookie to match.
func TestZitadelRejectsCallbackWithoutFlowCookie(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	callback := h.oidcFlow(h.client())
	victim := h.client()
	assertRejected(t, h, victim, h.do(victim, http.MethodGet, callback, nil, nil))
}

func TestZitadelCallbackCannotBeReplayed(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	c := h.client()
	callback := h.oidcFlow(c)
	h.do(c, http.MethodGet, callback, nil, nil)
	me, _ := h.me(c)
	h.logout(c, me.CSRFToken)
	res := h.do(c, http.MethodGet, callback, nil, nil)
	if res.Header.Get("Location") != "/?error=signin_failed" {
		t.Fatalf("replayed callback = %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
	if _, status := h.me(c); status != http.StatusUnauthorized {
		t.Fatal("a replayed callback signed the browser in")
	}
}

func TestZitadelProviderError(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	c := h.client()
	cb, _ := url.Parse(h.oidcFlow(c))
	q := cb.Query()
	q.Del("code")
	q.Set("error", "access_denied")
	cb.RawQuery = q.Encode()
	assertRejected(t, h, c, h.do(c, http.MethodGet, cb.String(), nil, nil))
}

func TestZitadelProviderUnreachable(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	h.idp.Close()
	res := h.do(h.client(), http.MethodGet, "/auth/oidc/login", nil, nil)
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/?error=provider_unavailable" {
		t.Fatalf("login with the provider down = %d → %q", res.StatusCode, res.Header.Get("Location"))
	}
}
