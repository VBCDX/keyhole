package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

const testPassword = "correct horse battery"

// Acceptance criterion 1: sign in, see who I am, sign out, and the old
// cookie stops working.
func TestLocalSignInWhoAmISignOut(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h := newHarness(t, b, config.AuthLocal, nil)
		u := h.createUser("alex@example.com", testPassword)
		c := h.client()

		if _, status := h.me(c); status != http.StatusUnauthorized {
			t.Fatalf("me before sign-in = %d, want 401", status)
		}
		res := h.login(c, "  ALEX@Example.com ", testPassword)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("login = %d", res.StatusCode)
		}
		var cookie *http.Cookie
		for _, ck := range res.Cookies() {
			if ck.Name == "keyhole_session" {
				cookie = ck
			}
		}
		if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
			t.Fatalf("session cookie = %+v; want HttpOnly, SameSite=Lax, Path=/", cookie)
		}

		me, status := h.me(c)
		if status != http.StatusOK || me.User.ID != u.ID || me.User.Email != "alex@example.com" ||
			me.Method != "local" || me.SessionStartedAt.IsZero() || me.CSRFToken == "" {
			t.Fatalf("me = %d %+v", status, me)
		}

		// A copy of the cookie, as an attacker who stole it would hold.
		replay := h.client()
		replay.Jar.SetCookies(mustURL(h.ts.URL), []*http.Cookie{{Name: cookie.Name, Value: cookie.Value}})
		if _, status := h.me(replay); status != http.StatusOK {
			t.Fatalf("copied cookie before sign-out = %d, want 200", status)
		}

		redirect, status := h.logout(c, me.CSRFToken)
		if status != http.StatusOK || redirect != "/" {
			t.Fatalf("logout = %d %q", status, redirect)
		}
		if _, status := h.me(c); status != http.StatusUnauthorized {
			t.Fatalf("me after sign-out = %d, want 401", status)
		}
		if _, status := h.me(replay); status != http.StatusUnauthorized {
			t.Fatalf("old session cookie after sign-out = %d, want 401 (server session must be deleted)", status)
		}
	})
}

func TestLocalSignInFailures(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.createUser("alex@example.com", testPassword)
	c := h.client()

	for name, tc := range map[string]struct {
		body   any
		header http.Header
		want   int
	}{
		"wrong password": {map[string]string{"email": "alex@example.com", "password": "wrong password!"}, nil, 401},
		"unknown email":  {map[string]string{"email": "nobody@example.com", "password": testPassword}, nil, 401},
		"empty":          {map[string]string{"email": "", "password": ""}, nil, 400},
		"extra fields":   {map[string]any{"email": "alex@example.com", "password": testPassword, "admin": true}, nil, 400},
	} {
		res := h.do(c, http.MethodPost, "/api/auth/login", tc.body, tc.header)
		if res.StatusCode != tc.want {
			t.Errorf("%s: status %d, want %d", name, res.StatusCode, tc.want)
		}
		if len(res.Cookies()) != 0 {
			t.Errorf("%s: a failed sign-in set a cookie", name)
		}
	}

	// A form post (not JSON) is refused, which also stops cross-site HTML forms.
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/api/auth/login", nil)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("form-encoded login = %d, want 400", res.StatusCode)
	}
}

// behindProxy makes the test server's peer, loopback, a trusted proxy, so
// X-Forwarded-For names the client.
func (h *harness) behindProxy() {
	h.cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("::1/128")}
}

// loginFrom signs in as a client at ip, as a trusted proxy reports it.
func (h *harness) loginFrom(c *http.Client, email, password, ip string) *http.Response {
	h.t.Helper()
	return h.do(c, http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": password},
		http.Header{"X-Forwarded-For": {ip}})
}

// fastBackoff keeps the account slow-down's shape with delays short enough
// for tests.
func (h *harness) fastBackoff() {
	h.srv.accountBackoff = auth.NewBackoff(accountFreeFailures, 20*time.Millisecond, 300*time.Millisecond, failWindow)
}

// One client is refused after accountFailLimit failures on an account, but
// that never locks the owner out: from another address the correct
// password still works, only slowed down.
func TestFailedSignInsAreRateLimited(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.behindProxy()
	h.fastBackoff()
	h.createUser("alex@example.com", testPassword)
	c := h.client()
	const attacker, owner = "203.0.113.9", "198.51.100.7"
	for i := 0; i < accountFailLimit; i++ {
		if res := h.loginFrom(c, "alex@example.com", "not the password", attacker); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i, res.StatusCode)
		}
	}
	res := h.loginFrom(c, "ALEX@example.com", testPassword, attacker)
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("after %d failures the same client got %d (Retry-After %q); want 429",
			accountFailLimit, res.StatusCode, res.Header.Get("Retry-After"))
	}

	start := time.Now()
	if res := h.loginFrom(h.client(), "alex@example.com", testPassword, owner); res.StatusCode != http.StatusOK {
		t.Fatalf("the owner from another address got %d; failures elsewhere must not lock them out", res.StatusCode)
	}
	if took := time.Since(start); took < 300*time.Millisecond {
		t.Fatalf("the owner's sign-in took %v; after %d failures the account must be slowed down", took, accountFailLimit)
	}
	if d := h.srv.accountBackoff.Delay("acct:alex@example.com"); d != 0 {
		t.Fatalf("a successful sign-in left a %v delay on the account", d)
	}
	if res := h.loginFrom(c, "alex@example.com", testPassword, attacker); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("the limited client got %d after the owner signed in; want still 429", res.StatusCode)
	}

	h.createUser("other@example.com", testPassword)
	if res := h.loginFrom(c, "other@example.com", testPassword, attacker); res.StatusCode != http.StatusOK {
		t.Fatalf("another account from the same address = %d; the per-account limit must not spill over", res.StatusCode)
	}
}

// Behind a proxy many people can share an address. A successful sign-in
// clears the address's count, so their mistakes do not add up to a lockout.
func TestSuccessfulSignInClearsTheAddressCount(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.behindProxy()
	h.fastBackoff()
	h.srv.addressLimiter = auth.NewLimiter(3, failWindow)
	h.createUser("alex@example.com", testPassword)
	h.createUser("other@example.com", testPassword)
	c := h.client()
	const office = "198.51.100.20"
	for round := 0; round < 3; round++ {
		for i := 0; i < 2; i++ {
			if res := h.loginFrom(c, "alex@example.com", "not the password", office); res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("round %d failure %d = %d, want 401", round, i, res.StatusCode)
			}
		}
		if res := h.loginFrom(c, "other@example.com", testPassword, office); res.StatusCode != http.StatusOK {
			t.Fatalf("round %d: correct password = %d, want 200", round, res.StatusCode)
		}
	}
	for i := 0; i < 3; i++ {
		h.loginFrom(c, "alex@example.com", "not the password", office)
	}
	if res := h.loginFrom(c, "other@example.com", testPassword, office); res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("after 3 failures in a row the address got %d; want 429", res.StatusCode)
	}
	if res := h.loginFrom(c, "other@example.com", testPassword, "198.51.100.21"); res.StatusCode != http.StatusOK {
		t.Fatalf("a different address behind the same proxy got %d; want 200", res.StatusCode)
	}
}

// A client must not be able to choose the address its
// failures are counted against, by sending a different forwarding header on
// every request.
func TestForgedClientAddressDoesNotEvadeLimits(t *testing.T) {
	for _, tc := range []struct {
		name        string
		behindProxy bool
		header      func(i int) http.Header
	}{
		// No trusted proxies (the default): every forged header is ignored and
		// the TCP peer is the client.
		{"direct", false, func(i int) http.Header {
			forged := fmt.Sprintf("198.51.100.%d", i+1)
			return http.Header{"X-Forwarded-For": {forged}, "Cf-Connecting-Ip": {forged}, "X-Real-Ip": {forged}}
		}},
		// Behind a trusted proxy: what the client wrote left of the address
		// the proxy appended is ignored.
		{"behind a proxy", true, func(i int) http.Header {
			forged := fmt.Sprintf("198.51.100.%d", i+1)
			return http.Header{"X-Forwarded-For": {forged + ", 203.0.113.9"}, "Cf-Connecting-Ip": {forged}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
			if tc.behindProxy {
				h.behindProxy()
			}
			h.fastBackoff()
			h.createUser("alex@example.com", testPassword)
			c := h.client()
			body := map[string]string{"email": "alex@example.com", "password": "not the password"}
			for i := 0; i < accountFailLimit; i++ {
				if res := h.do(c, http.MethodPost, "/api/auth/login", body, tc.header(i)); res.StatusCode != http.StatusUnauthorized {
					t.Fatalf("failure %d = %d, want 401", i, res.StatusCode)
				}
			}
			if res := h.do(c, http.MethodPost, "/api/auth/login", body, tc.header(accountFailLimit)); res.StatusCode != http.StatusTooManyRequests {
				t.Fatalf("after %d failures with a new forged address each time: %d, want 429", accountFailLimit, res.StatusCode)
			}
		})
	}
}

type attempt struct {
	from   string
	status int
	retry  string
	took   time.Duration
	err    error
}

// tryLogin is a sign-in that is safe to run on another goroutine.
func (h *harness) tryLogin(email, password, from string, out chan<- attempt) {
	b, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequest(http.MethodPost, h.ts.URL+"/api/auth/login", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", from)
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	a := attempt{from: from, took: time.Since(start), err: err}
	if err == nil {
		a.status, a.retry = res.StatusCode, res.Header.Get("Retry-After")
		res.Body.Close()
	}
	out <- a
}

// Many parallel attempts on a slowed-down account must not each hold a
// connection open for the delay: only a few per address, and a bounded
// number in all, wait; the rest are refused at once. The owner, at another
// address, still gets in while an attacker's attempts are waiting.
func TestSlowDownDoesNotHoldConnections(t *testing.T) {
	const delay = 2 * time.Second
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.behindProxy()
	h.srv.accountBackoff = auth.NewBackoff(0, delay, delay, failWindow)
	h.srv.accountBackoff.Fail("acct:alex@example.com")
	const perAddress, total = 2, 5
	h.srv.waiting = auth.NewWaiting(perAddress, total)
	h.createUser("alex@example.com", testPassword)

	check := func(results []attempt, maxWaited int) []attempt {
		t.Helper()
		var waited []attempt
		for _, a := range results {
			switch {
			case a.err != nil:
				t.Fatalf("attempt from %s: %v", a.from, a.err)
			case a.status == http.StatusTooManyRequests:
				if a.took > delay/2 || a.retry != "2" {
					t.Errorf("refused attempt took %v with Retry-After %q; want an immediate 429 with Retry-After 2", a.took, a.retry)
				}
			default:
				waited = append(waited, a)
			}
		}
		if len(waited) > maxWaited {
			t.Fatalf("%d of %d parallel attempts were held for the slow-down; want at most %d", len(waited), len(results), maxWaited)
		}
		return waited
	}

	// From many addresses at once: at most total wait.
	out := make(chan attempt)
	for i := 0; i < 20; i++ {
		go h.tryLogin("alex@example.com", "not the password", fmt.Sprintf("198.51.100.%d", i+1), out)
	}
	var results []attempt
	for i := 0; i < 20; i++ {
		results = append(results, <-out)
	}
	for _, a := range check(results, total) {
		if a.status != http.StatusUnauthorized {
			t.Fatalf("a waited wrong-password attempt got %d, want 401", a.status)
		}
	}

	// From one address at once: at most perAddress wait, and the owner at
	// another address is not shut out by them.
	for i := 0; i < 20; i++ {
		go h.tryLogin("alex@example.com", "not the password", "203.0.113.9", out)
	}
	time.Sleep(200 * time.Millisecond)
	owner := make(chan attempt)
	go h.tryLogin("alex@example.com", testPassword, "198.51.100.200", owner)
	results = results[:0]
	for i := 0; i < 20; i++ {
		results = append(results, <-out)
	}
	check(results, perAddress)
	if a := <-owner; a.err != nil || a.status != http.StatusOK {
		t.Fatalf("the owner's sign-in while an attacker's attempts waited: %d %v; want 200", a.status, a.err)
	}
}

func TestCSRFProtection(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.createUser("alex@example.com", testPassword)
	c := h.client()
	h.login(c, "alex@example.com", testPassword)
	me, _ := h.me(c)

	if _, status := h.logout(c, ""); status != http.StatusForbidden {
		t.Fatalf("logout without CSRF header = %d, want 403", status)
	}
	if _, status := h.logout(c, "wrong-token"); status != http.StatusForbidden {
		t.Fatalf("logout with wrong CSRF token = %d, want 403", status)
	}
	res := h.do(c, http.MethodPost, "/api/auth/logout", nil,
		http.Header{csrfHeader: {me.CSRFToken}, "Sec-Fetch-Site": {"cross-site"}})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site logout = %d, want 403", res.StatusCode)
	}
	res = h.do(c, http.MethodPost, "/api/auth/login", map[string]string{"email": "alex@example.com", "password": testPassword},
		http.Header{"Origin": {"https://evil.example"}})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin login = %d, want 403", res.StatusCode)
	}
	if _, status := h.me(c); status != http.StatusOK {
		t.Fatal("session should have survived the refused requests")
	}
}

func TestSignInReplacesExistingSession(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.createUser("alex@example.com", testPassword)
	c := h.client()
	first := h.login(c, "alex@example.com", testPassword).Cookies()[0]
	h.login(c, "alex@example.com", testPassword)
	old := h.client()
	old.Jar.SetCookies(mustURL(h.ts.URL), []*http.Cookie{{Name: first.Name, Value: first.Value}})
	if _, status := h.me(old); status != http.StatusUnauthorized {
		t.Fatalf("previous session after re-sign-in = %d, want 401", status)
	}
}

// Acceptance criterion 3, and the per-mode endpoints.
func TestAuthConfigPerMode(t *testing.T) {
	for _, tc := range []struct {
		mode           config.AuthMode
		local, zitadel bool
	}{{config.AuthLocal, true, false}, {config.AuthZitadel, false, true}, {config.AuthBoth, true, true}} {
		t.Run(string(tc.mode), func(t *testing.T) {
			h := newHarness(t, storetest.Backends(t)[0], tc.mode, nil)
			c := h.client()
			got := decode[authConfigResponse](t, h.do(c, http.MethodGet, "/api/auth/config", nil, nil))
			if got.Local != tc.local || got.Zitadel != tc.zitadel || got.SilentSignIn != tc.zitadel || got.SuiteApps == nil || len(got.SuiteApps) != 0 {
				t.Fatalf("config = %+v", got)
			}
			res := h.login(c, "x@example.com", testPassword)
			if tc.local && res.StatusCode == http.StatusNotFound || !tc.local && res.StatusCode != http.StatusNotFound {
				t.Fatalf("local login endpoint status %d in mode %s", res.StatusCode, tc.mode)
			}
			res = h.do(c, http.MethodGet, "/auth/oidc/login", nil, nil)
			loc := res.Header.Get("Location")
			if tc.zitadel != (res.StatusCode == http.StatusFound && len(loc) > len(h.idpURL()) && loc[:len(h.idpURL())] == h.idpURL()) {
				t.Fatalf("Zitadel login in mode %s: %d → %q", tc.mode, res.StatusCode, loc)
			}
		})
	}
}

func TestHealthAndReady(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	c := h.client()
	if res := h.do(c, http.MethodGet, "/healthz", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("healthz = %d", res.StatusCode)
	}
	res := h.do(c, http.MethodGet, "/readyz", nil, nil)
	got := decode[map[string]string](t, res)
	if res.StatusCode != http.StatusOK || got["database"] != "ok" {
		t.Fatalf("readyz = %d %v", res.StatusCode, got)
	}
	h.st.Close()
	res = h.do(c, http.MethodGet, "/readyz", nil, nil)
	got = decode[map[string]string](t, res)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("readyz with the database closed = %d, want 503", res.StatusCode)
	}
	// The driver's error text stays in the log.
	if got["database"] != "unavailable" {
		t.Fatalf("readyz with the database closed = %v, want database unavailable", got)
	}
}

func TestStaticAppAndHeaders(t *testing.T) {
	web := fstest.MapFS{
		"index.html":      {Data: []byte("<!doctype html><title>Keyhole</title>")},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	}
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, web)
	c := h.client()
	for path, want := range map[string]string{"/": "Keyhole", "/me": "Keyhole", "/assets/app-1.js": "console.log(1)", "/favicon.svg": "<svg/>"} {
		res := h.do(c, http.MethodGet, path, nil, nil)
		body := readAll(t, res)
		if res.StatusCode != http.StatusOK || !contains(body, want) {
			t.Errorf("GET %s = %d %q", path, res.StatusCode, body)
		}
		if res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("GET %s missing security headers", path)
		}
		// The harness serves plain http, where HSTS means nothing.
		if hsts := res.Header.Get("Strict-Transport-Security"); hsts != "" {
			t.Errorf("GET %s over http sent Strict-Transport-Security %q", path, hsts)
		}
	}
	if res := h.do(c, http.MethodGet, "/assets/missing.js", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset = %d, want 404", res.StatusCode)
	}
	if res := h.do(c, http.MethodGet, "/assets/app-1.js", nil, nil); res.Header.Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("hashed asset Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	if res := h.do(c, http.MethodGet, "/", nil, nil); res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("index.html Cache-Control = %q", res.Header.Get("Cache-Control"))
	}
	if res := h.do(c, http.MethodGet, "/api/nope", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown API path = %d, want a JSON 404 rather than the app", res.StatusCode)
	}
}

func TestConcurrentSignInsAreBounded(t *testing.T) {
	h := newHarness(t, storetest.Backends(t)[0], config.AuthLocal, nil)
	h.createUser("alex@example.com", testPassword)
	// Fill every slot: a sign-in must wait, and gives up with 503 when the
	// client goes away rather than queueing unbounded work.
	for i := 0; i < cap(h.srv.hashSlots); i++ {
		h.srv.hashSlots <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.ts.URL+"/api/auth/login",
		strings.NewReader(`{"email":"alex@example.com","password":"`+testPassword+`"}`))
	req.Header.Set("Content-Type", "application/json")
	if res, err := h.client().Do(req); err == nil {
		res.Body.Close()
		t.Fatalf("sign-in with every hash slot taken returned %d; want it to wait", res.StatusCode)
	}
	for i := 0; i < cap(h.srv.hashSlots); i++ {
		<-h.srv.hashSlots
	}
	if res := h.login(h.client(), "alex@example.com", testPassword); res.StatusCode != http.StatusOK {
		t.Fatalf("sign-in after slots freed = %d", res.StatusCode)
	}
}
