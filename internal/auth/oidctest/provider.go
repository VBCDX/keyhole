// Package oidctest is a minimal fake OpenID Connect provider for tests. It
// implements discovery, JWKS, the authorization endpoint (which signs the
// user straight in, or with prompt=none answers login_required when the
// provider session has ended), the token endpoint with client
// authentication and PKCE checks, userinfo and end-session. It mints
// back-channel logout tokens. Hooks let a test tamper with the flow, for
// example to issue an ID token with the wrong nonce.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// User is who the provider signs in.
type User struct {
	Subject string
	Email   string
	Name    string
}

// Provider is a running fake provider.
type Provider struct {
	*httptest.Server
	ClientID     string
	ClientSecret string

	key    *rsa.PrivateKey
	signer jose.Signer

	mu       sync.Mutex
	user     User
	codes    map[string]grant
	signedIn bool   // the browser has a provider session
	sid      string // that session's ID, the ID token's "sid"
	prompts  []string

	// IDTokenClaims, if set, may change the ID token claims before signing.
	IDTokenClaims func(claims map[string]any)
	// IncludeProfileInIDToken puts email and name in the ID token; when
	// false they are only available from userinfo, as Zitadel does by default.
	IncludeProfileInIDToken bool
}

type grant struct {
	clientID    string
	redirectURI string
	nonce       string
	challenge   string
	method      string
	sid         string
	expires     time.Time
}

// New starts a provider; it is closed when the test ends.
func New(t testing.TB) *Provider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	p := &Provider{
		ClientID: "keyhole-test", ClientSecret: "test-secret",
		key: key, signer: signer, codes: map[string]grant{}, signedIn: true, sid: "sid-" + rand.Text(),
		user: User{Subject: "user-123", Email: "zed@example.com", Name: "Zed Example"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("GET /keys", p.jwks)
	mux.HandleFunc("GET /authorize", p.authorize)
	mux.HandleFunc("POST /token", p.token)
	mux.HandleFunc("GET /userinfo", p.userinfo)
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// Issuer is the provider's issuer URL.
func (p *Provider) Issuer() string { return p.URL }

// SetUser changes who the next sign-in is.
func (p *Provider) SetUser(u User) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.user = u
}

// SID is the current provider session's ID.
func (p *Provider) SID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sid
}

// EndSession ends the provider session, as signing out of another app
// would: a prompt=none request now gets login_required, and the next
// interactive sign-in starts a session with a new sid.
func (p *Provider) EndSession() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signedIn = false
	p.sid = "sid-" + rand.Text()
}

// Prompts lists the prompt parameter of every authorization request so far.
func (p *Provider) Prompts() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.prompts...)
}

// LogoutToken mints a back-channel logout token for sid and sub, signed
// with the provider's key. mutate, if set, may change the claims first.
func (p *Provider) LogoutToken(t testing.TB, sid, sub string, mutate func(claims map[string]any)) string {
	return p.signLogout(t, p.signer, sid, sub, mutate)
}

// ForgedLogoutToken is LogoutToken signed with a key the provider does not
// publish (but the same key ID).
func (p *Provider) ForgedLogoutToken(t testing.TB, sid, sub string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "test-key"))
	if err != nil {
		t.Fatal(err)
	}
	return p.signLogout(t, signer, sid, sub, nil)
}

func (p *Provider) signLogout(t testing.TB, signer jose.Signer, sid, sub string, mutate func(map[string]any)) string {
	t.Helper()
	now := time.Now()
	claims := map[string]any{
		"iss": p.URL, "aud": []string{p.ClientID},
		"iat": now.Unix(), "exp": now.Add(15 * time.Minute).Unix(), "jti": rand.Text(),
		"events": map[string]any{"http://schemas.openid.net/event/backchannel-logout": map[string]any{}},
	}
	if sid != "" {
		claims["sid"] = sid
	}
	if sub != "" {
		claims["sub"] = sub
	}
	if mutate != nil {
		mutate(claims)
	}
	tok, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/authorize",
		"token_endpoint":                        p.URL + "/token",
		"userinfo_endpoint":                     p.URL + "/userinfo",
		"jwks_uri":                              p.URL + "/keys",
		"end_session_endpoint":                  p.URL + "/end_session",
		"backchannel_logout_supported":          true,
		"backchannel_logout_session_supported":  true,
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
		Key: &p.key.PublicKey, KeyID: "test-key", Algorithm: "RS256", Use: "sig",
	}}})
}

// authorize signs the configured user in immediately and redirects back.
// With prompt=none and no provider session it redirects back with
// error=login_required instead.
func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("response_type") != "code" || q.Get("client_id") != p.ClientID {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		http.Error(w, "PKCE S256 required", http.StatusBadRequest)
		return
	}
	back, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	bq := back.Query()
	bq.Set("state", q.Get("state"))

	p.mu.Lock()
	p.prompts = append(p.prompts, q.Get("prompt"))
	if q.Get("prompt") == "none" && !p.signedIn {
		p.mu.Unlock()
		bq.Set("error", "login_required")
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
		return
	}
	p.signedIn = true
	code := rand.Text()
	p.codes[code] = grant{
		clientID: q.Get("client_id"), redirectURI: q.Get("redirect_uri"), nonce: q.Get("nonce"),
		challenge: q.Get("code_challenge"), method: q.Get("code_challenge_method"),
		sid: p.sid, expires: time.Now().Add(time.Minute),
	}
	p.mu.Unlock()
	bq.Set("code", code)
	back.RawQuery = bq.Encode()
	http.Redirect(w, r, back.String(), http.StatusFound)
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.ClientID || secret != p.ClientSecret {
		tokenError(w, "invalid_client")
		return
	}
	code := r.PostForm.Get("code")
	p.mu.Lock()
	g, found := p.codes[code]
	delete(p.codes, code) // codes are single use
	user := p.user
	p.mu.Unlock()
	if !found || time.Now().After(g.expires) || r.PostForm.Get("redirect_uri") != g.redirectURI {
		tokenError(w, "invalid_grant")
		return
	}
	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge {
		tokenError(w, "invalid_grant")
		return
	}

	now := time.Now()
	claims := map[string]any{
		"iss": p.URL, "sub": user.Subject, "aud": p.ClientID,
		"iat": now.Unix(), "exp": now.Add(5 * time.Minute).Unix(),
		"nonce": g.nonce, "sid": g.sid,
	}
	if p.IncludeProfileInIDToken {
		claims["email"], claims["name"] = user.Email, user.Name
	}
	if p.IDTokenClaims != nil {
		p.IDTokenClaims(claims)
	}
	idToken, err := jwt.Signed(p.signer).Claims(claims).Serialize()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{
		"access_token": "at-" + user.Subject, "token_type": "Bearer", "expires_in": 300, "id_token": idToken,
	})
}

func (p *Provider) userinfo(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	user := p.user
	p.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer at-"+user.Subject {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, map[string]any{"sub": user.Subject, "email": user.Email, "email_verified": true, "name": user.Name})
}

func tokenError(w http.ResponseWriter, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
