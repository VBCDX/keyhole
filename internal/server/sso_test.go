package server

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

func (h *harness) authConfig(c *http.Client) authConfigResponse {
	h.t.Helper()
	return decode[authConfigResponse](h.t, h.do(c, http.MethodGet, "/api/auth/config", nil, nil))
}

// silent starts /auth/oidc/login?prompt=none and follows redirects through
// the provider and back, returning where the browser lands.
func (h *harness) silent(c *http.Client) string {
	h.t.Helper()
	res := h.do(c, http.MethodGet, "/auth/oidc/login?prompt=none", nil, nil)
	for i := 0; res.StatusCode == http.StatusFound && i < 5; i++ {
		loc := res.Header.Get("Location")
		if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "/auth/") {
			return loc
		}
		res = h.do(c, http.MethodGet, loc, nil, nil)
	}
	h.t.Fatalf("silent sign-in ended on %d", res.StatusCode)
	return ""
}

// signIn runs an interactive Zitadel sign-in to completion.
func (h *harness) signIn(c *http.Client) meResponse {
	h.t.Helper()
	h.do(c, http.MethodGet, h.oidcFlow(c), nil, nil)
	me, status := h.me(c)
	if status != http.StatusOK {
		h.t.Fatalf("Zitadel sign-in: me = %d", status)
	}
	return me
}

func (h *harness) backchannel(token string, header http.Header) *http.Response {
	h.t.Helper()
	req, err := http.NewRequest(http.MethodPost, h.ts.URL+"/auth/oidc/backchannel-logout",
		strings.NewReader(url.Values{"logout_token": {token}}.Encode()))
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, vs := range header {
		req.Header[k] = vs
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}

func countPrompt(prompts []string, want string) int {
	n := 0
	for _, p := range prompts {
		if p == want {
			n++
		}
	}
	return n
}

// Signed in to another suite app (the provider has a session): opening
// Keyhole signs in silently, with prompt=none, and keeps the sid.
func TestSilentSignInSuccess(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthZitadel, nil)
		c := h.client()
		if !h.authConfig(c).SilentSignIn {
			t.Fatal("silent_sign_in = false for a fresh visitor")
		}
		if got := h.silent(c); got != "/" {
			t.Fatalf("silent sign-in landed on %q, want /", got)
		}
		if n := countPrompt(h.idp.Prompts(), "none"); n != 1 {
			t.Fatalf("provider saw %d prompt=none requests, want 1", n)
		}
		me, status := h.me(c)
		if status != http.StatusOK || me.Method != "zitadel" || me.User.Email != "zed@example.com" {
			t.Fatalf("me after silent sign-in = %d %+v", status, me)
		}
		// The session holds the provider's sid: a logout token for it ends it.
		if res := h.backchannel(h.idp.LogoutToken(t, h.idp.SID(), "", nil), nil); res.StatusCode != http.StatusOK {
			t.Fatalf("back-channel logout = %d", res.StatusCode)
		}
		if _, status := h.me(c); status != http.StatusUnauthorized {
			t.Fatalf("me after back-channel logout = %d, want 401", status)
		}
	})
}

// No provider session: login_required lands on the sign-in page without an
// error, and it is not tried again.
func TestSilentSignInLoginRequiredDoesNotLoop(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthZitadel, nil)
		h.idp.EndSession()
		c := h.client()
		if got := h.silent(c); got != "/" {
			t.Fatalf("login_required landed on %q, want / with no error", got)
		}
		if _, status := h.me(c); status != http.StatusUnauthorized {
			t.Fatalf("me = %d, want 401", status)
		}
		if h.authConfig(c).SilentSignIn {
			t.Fatal("silent_sign_in still true straight after a failed attempt: the web app would loop")
		}
		// Even if the web app asked again, the server does not go back to the provider.
		res := h.do(c, http.MethodGet, "/auth/oidc/login?prompt=none", nil, nil)
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/" {
			t.Fatalf("second silent attempt = %d → %q, want 302 → /", res.StatusCode, res.Header.Get("Location"))
		}
		if n := countPrompt(h.idp.Prompts(), "none"); n != 1 {
			t.Fatalf("provider saw %d prompt=none requests, want 1", n)
		}
		// The interactive button still works.
		h.signIn(c)
	})
}

// The marker is short-lived: once it expires one more silent attempt is made.
func TestSilentSignInMarkerIsShortLived(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthZitadel, nil)
	res := h.do(h.client(), http.MethodGet, "/auth/oidc/login?prompt=none", nil, nil)
	for _, ck := range res.Cookies() {
		if ck.Name == ssoTriedCookie {
			if ck.MaxAge <= 0 || ck.MaxAge > 300 || !ck.HttpOnly || ck.Path != "/" {
				t.Fatalf("sso-tried marker = %+v; want HttpOnly, Path=/, Max-Age 1..300s", ck)
			}
			return
		}
	}
	t.Fatal("no sso-tried marker set before the silent redirect")
}

// Signing out on purpose: no silent sign-in straight back in, even though
// the provider (in this test) still has a session.
func TestNoSilentSignInAfterSignOut(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthBoth, nil)
		c := h.client()
		me := h.signIn(c)
		if _, status := h.logout(c, me.CSRFToken); status != http.StatusOK {
			t.Fatalf("logout = %d", status)
		}
		if h.authConfig(c).SilentSignIn {
			t.Fatal("silent_sign_in = true straight after sign-out")
		}
		before := countPrompt(h.idp.Prompts(), "none")
		res := h.do(c, http.MethodGet, "/auth/oidc/login?prompt=none", nil, nil)
		if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "/" {
			t.Fatalf("silent attempt after sign-out = %d → %q, want 302 → /", res.StatusCode, res.Header.Get("Location"))
		}
		if countPrompt(h.idp.Prompts(), "none") != before {
			t.Fatal("silent attempt after sign-out reached the provider")
		}
		if _, status := h.me(c); status != http.StatusUnauthorized {
			t.Fatal("signed back in after an explicit sign-out")
		}

		// Signing in again (any method) clears the marker.
		h.signIn(c)
		for _, ck := range c.Jar.Cookies(mustURL(h.ts.URL)) {
			if ck.Name == signedOutCookie {
				t.Fatal("signed-out marker survived the next sign-in")
			}
		}
	})
}

// A valid logout token ends exactly the sessions with its sid, so their
// cookies stop working; a token with only sub ends all that subject's
// Zitadel sessions. It needs no session, CSRF token or same-origin request.
func TestBackchannelLogoutEndsMatchingSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthBoth, nil)
		first := h.client()
		h.signIn(first)
		firstSID := h.idp.SID()

		h.idp.EndSession() // a new provider session, with a new sid
		second := h.client()
		h.signIn(second)

		h.createUser("local@example.com", testPassword)
		local := h.client()
		if res := h.login(local, "local@example.com", testPassword); res.StatusCode != http.StatusOK {
			t.Fatalf("local login = %d", res.StatusCode)
		}

		res := h.backchannel(h.idp.LogoutToken(t, firstSID, "user-123", nil),
			http.Header{"Origin": {"https://auth.example"}, "Sec-Fetch-Site": {"cross-site"}})
		if res.StatusCode != http.StatusOK || res.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("back-channel logout = %d (Cache-Control %q), want 200 no-store", res.StatusCode, res.Header.Get("Cache-Control"))
		}
		if _, status := h.me(first); status != http.StatusUnauthorized {
			t.Fatalf("session with the logged-out sid: me = %d, want 401", status)
		}
		if _, status := h.me(second); status != http.StatusOK {
			t.Fatalf("session with another sid: me = %d, want 200", status)
		}

		if res := h.backchannel(h.idp.LogoutToken(t, "", "user-123", nil), nil); res.StatusCode != http.StatusOK {
			t.Fatalf("sub-only back-channel logout = %d", res.StatusCode)
		}
		if _, status := h.me(second); status != http.StatusUnauthorized {
			t.Fatalf("sub-only logout left the subject's session: me = %d", status)
		}
		if _, status := h.me(local); status != http.StatusOK {
			t.Fatalf("back-channel logout ended a local session: me = %d", status)
		}

		// A token for a session Keyhole never saw is still a 200.
		if res := h.backchannel(h.idp.LogoutToken(t, "sid-unknown", "", nil), nil); res.StatusCode != http.StatusOK {
			t.Fatalf("logout for an unknown sid = %d, want 200", res.StatusCode)
		}
	})
}

func TestBackchannelLogoutRejectsBadTokens(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthZitadel, nil)
		c := h.client()
		h.signIn(c)
		sid := h.idp.SID()
		tok := func(mutate func(map[string]any)) string { return h.idp.LogoutToken(t, sid, "user-123", mutate) }

		cases := map[string]string{
			"wrong audience":       tok(func(c map[string]any) { c["aud"] = []string{"some-other-app"} }),
			"wrong issuer":         tok(func(c map[string]any) { c["iss"] = "https://evil.example" }),
			"expired":              tok(func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }),
			"no exp":               tok(func(c map[string]any) { delete(c, "exp") }),
			"no iat":               tok(func(c map[string]any) { delete(c, "iat") }),
			"iat in the future":    tok(func(c map[string]any) { c["iat"] = time.Now().Add(time.Hour).Unix() }),
			"missing event":        tok(func(c map[string]any) { delete(c, "events") }),
			"other event":          tok(func(c map[string]any) { c["events"] = map[string]any{"urn:other": map[string]any{}} }),
			"event not an object":  tok(func(c map[string]any) { c["events"] = map[string]any{backchannelEvent: "yes"} }),
			"with nonce":           tok(func(c map[string]any) { c["nonce"] = "n-1" }),
			"no sid and no sub":    tok(func(c map[string]any) { delete(c, "sid"); delete(c, "sub") }),
			"no jti":               tok(func(c map[string]any) { delete(c, "jti") }),
			"bad signature":        h.idp.ForgedLogoutToken(t, sid, "user-123"),
			"tampered payload":     tamper(tok(nil)),
			"not a JWT":            "not-a-jwt",
			"empty logout_token":   "",
			"an ID token-like JWT": tok(func(c map[string]any) { delete(c, "events"); c["nonce"] = "n" }),
		}
		for name, token := range cases {
			res := h.backchannel(token, nil)
			if res.StatusCode != http.StatusBadRequest {
				t.Errorf("%s: status %d, want 400", name, res.StatusCode)
			}
		}
		if _, status := h.me(c); status != http.StatusOK {
			t.Fatalf("a refused logout token ended the session: me = %d", status)
		}

		// A reused jti is refused, and does nothing.
		valid := tok(nil)
		if res := h.backchannel(valid, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("valid logout token = %d", res.StatusCode)
		}
		h.signIn(c) // the provider session (and sid) is unchanged
		if res := h.backchannel(valid, nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("replayed logout token = %d, want 400", res.StatusCode)
		}
		if _, status := h.me(c); status != http.StatusOK {
			t.Fatal("a replayed logout token ended a newer session")
		}

		// Not a form body.
		res := h.do(h.client(), http.MethodPost, "/auth/oidc/backchannel-logout", map[string]string{"logout_token": valid}, nil)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("JSON body = %d, want 400", res.StatusCode)
		}
	})
}

const backchannelEvent = "http://schemas.openid.net/event/backchannel-logout"

// tamper flips a character in a JWT's payload, keeping the signature.
func tamper(jwt string) string {
	parts := strings.Split(jwt, ".")
	p := []byte(parts[1])
	if p[5] == 'A' {
		p[5] = 'B'
	} else {
		p[5] = 'A'
	}
	parts[1] = string(p)
	return strings.Join(parts, ".")
}

func TestBackchannelLogoutLocalOnly(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	if res := h.backchannel("anything", nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("back-channel logout with Zitadel off = %d, want 404", res.StatusCode)
	}
}

func TestAuthConfigSuiteApps(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.cfg.SuiteApps = []config.SuiteApp{{Name: "Dispatch", URL: "https://dispatch.vbcdx.com"}, {Name: "Keyhole", URL: h.ts.URL + "/"}}
	got := h.authConfig(h.client())
	if got.SilentSignIn {
		t.Fatal("silent_sign_in = true with Zitadel off")
	}
	want := []suiteApp{{"Dispatch", "https://dispatch.vbcdx.com", false}, {"Keyhole", h.ts.URL + "/", true}}
	if len(got.SuiteApps) != 2 || got.SuiteApps[0] != want[0] || got.SuiteApps[1] != want[1] {
		t.Fatalf("suite_apps = %+v, want %+v", got.SuiteApps, want)
	}
}

// The signed-out marker is a session cookie, as in Dispatch: no Max-Age or
// Expires, so it lasts only until the browser closes. Both sign-out paths
// (with a session, and with none) set it that way.
func TestSignedOutMarkerIsASessionCookie(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthBoth, nil)
	check := func(name string, res *http.Response) {
		t.Helper()
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s: logout = %d", name, res.StatusCode)
		}
		for _, raw := range res.Header.Values("Set-Cookie") {
			if !strings.HasPrefix(raw, signedOutCookie+"=") {
				continue
			}
			lower := strings.ToLower(raw)
			if strings.Contains(lower, "max-age") || strings.Contains(lower, "expires") {
				t.Fatalf("%s: signed-out marker %q has a lifetime; want a session cookie", name, raw)
			}
			for _, want := range []string{"path=/", "httponly", "samesite=lax"} {
				if !strings.Contains(lower, want) {
					t.Fatalf("%s: signed-out marker %q lacks %s", name, raw, want)
				}
			}
			return
		}
		t.Fatalf("%s: no signed-out marker set", name)
	}

	c := h.client()
	me := h.signIn(c)
	check("signed in", h.do(c, http.MethodPost, "/api/auth/logout", nil, http.Header{csrfHeader: {me.CSRFToken}}))
	check("no session", h.do(h.client(), http.MethodPost, "/api/auth/logout", nil, nil))
}
