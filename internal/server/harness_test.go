package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/auth/oidctest"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// harness runs a full Keyhole server (real database)
// behind an httptest server.
type harness struct {
	t   testing.TB
	cfg *config.Config
	st  *store.Store
	srv *Server
	ts  *httptest.Server
	idp *oidctest.Provider
}

func newHarness(t testing.TB, b storetest.Backend, mode config.AuthMode, web fs.FS) *harness {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	ts.Start()
	t.Cleanup(ts.Close)
	pub, _ := url.Parse(ts.URL)

	cfg := &config.Config{PublicURL: pub, AuthMode: mode, SessionTTL: time.Hour}
	st := storetest.Open(t, b)

	h := &harness{t: t, cfg: cfg, st: st, ts: ts}
	var oidc *auth.OIDC
	if mode.Zitadel() {
		h.idp = oidctest.New(t)
		cfg.OIDC = config.OIDC{
			Issuer: h.idp.Issuer(), ClientID: h.idp.ClientID, ClientSecret: h.idp.ClientSecret,
			RedirectURL:           ts.URL + "/auth/oidc/callback",
			PostLogoutRedirectURL: ts.URL + "/",
			Scopes:                []string{"openid", "profile", "email"},
		}
		sealer, err := auth.NewSealer("")
		if err != nil {
			t.Fatal(err)
		}
		oidc = auth.NewOIDC(cfg.OIDC, false, sealer)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.srv = New(cfg, st, oidc, web, log)
	ts.Config.Handler = h.srv.Handler()
	return h
}

// client is a browser-like client: it keeps cookies but does not follow
// redirects, so tests can inspect each hop.
func (h *harness) client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{
		Jar:           jar,
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (h *harness) createUser(email, password string) store.User {
	h.t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		h.t.Fatal(err)
	}
	u, err := h.st.CreateLocalUser(context.Background(), email, "Test User", hash)
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (h *harness) do(c *http.Client, method, path string, body any, header http.Header) *http.Response {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	target := path
	if len(path) > 0 && path[0] == '/' {
		target = h.ts.URL + path
	}
	req, err := http.NewRequest(method, target, rd)
	if err != nil {
		h.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		req.Header[k] = vs
	}
	res, err := c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { res.Body.Close() })
	return res
}

func (h *harness) login(c *http.Client, email, password string) *http.Response {
	h.t.Helper()
	return h.do(c, http.MethodPost, "/api/auth/login", map[string]string{"email": email, "password": password}, nil)
}

func (h *harness) me(c *http.Client) (meResponse, int) {
	h.t.Helper()
	res := h.do(c, http.MethodGet, "/api/me", nil, nil)
	var m meResponse
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
			h.t.Fatal(err)
		}
	}
	return m, res.StatusCode
}

func (h *harness) logout(c *http.Client, csrf string) (string, int) {
	h.t.Helper()
	res := h.do(c, http.MethodPost, "/api/auth/logout", nil, http.Header{csrfHeader: {csrf}})
	var out struct {
		RedirectURL string `json:"redirect_url"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	return out.RedirectURL, res.StatusCode
}

func decode[T any](t testing.TB, res *http.Response) T {
	t.Helper()
	var v T
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
