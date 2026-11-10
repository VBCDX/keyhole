package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/VBCDX/keyhole/internal/config"
)

// The cookie that carries state, nonce and the PKCE verifier from the
// redirect to the callback. It is scoped to the callback path.
const (
	oidcCookie     = "keyhole_oidc"
	oidcCookiePath = "/auth/oidc/"
	oidcFlowTTL    = 10 * time.Minute
	sealPurpose    = "oidc-flow"
)

// Errors a callback can fail with. All of them end the sign-in attempt.
var (
	ErrOIDCNoFlow        = errors.New("auth: no sign-in in progress (missing or expired flow cookie)")
	ErrOIDCStateMismatch = errors.New("auth: state does not match")
	ErrOIDCNonceMismatch = errors.New("auth: nonce does not match")
	ErrOIDCProvider      = errors.New("auth: the sign-in provider returned an error")
	// ErrOIDCSilent wraps any failure of a silent (prompt=none) sign-in. The
	// visitor then sees the normal sign-in page, without an error message.
	ErrOIDCSilent = errors.New("auth: silent sign-in did not complete")
)

// Identity is what a successful OpenID Connect sign-in tells us.
type Identity struct {
	Issuer  string
	Subject string
	Email   string
	Name    string
	IDToken string // raw ID token, kept as id_token_hint for sign-out
	SID     string // the provider's session ID ("sid"), matched by back-channel logout
}

// OIDC is a confidential OpenID Connect client using the authorization code
// flow with PKCE (S256). Discovery happens on first use and is retried until
// it succeeds, so Keyhole starts even if the provider is briefly down.
type OIDC struct {
	cfg    config.OIDC
	secure bool
	sealer *Sealer
	client *http.Client

	mu          sync.Mutex
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauth       oauth2.Config
	endSession  string
	lastAttempt time.Time
}

// NewOIDC builds the client. secureCookies sets the cookie's Secure flag.
func NewOIDC(cfg config.OIDC, secureCookies bool, sealer *Sealer) *OIDC {
	return &OIDC{cfg: cfg, secure: secureCookies, sealer: sealer, client: &http.Client{Timeout: 10 * time.Second}}
}

func (o *OIDC) ensure(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider != nil {
		return nil
	}
	if time.Since(o.lastAttempt) < 2*time.Second {
		return fmt.Errorf("auth: OpenID provider discovery failed recently; retrying shortly")
	}
	o.lastAttempt = time.Now()
	ctx = oidc.ClientContext(ctx, o.client)
	p, err := oidc.NewProvider(ctx, o.cfg.Issuer)
	if err != nil {
		return fmt.Errorf("auth: OpenID discovery for %s: %w", o.cfg.Issuer, err)
	}
	var extra struct {
		EndSession string `json:"end_session_endpoint"`
	}
	if err := p.Claims(&extra); err != nil {
		return fmt.Errorf("auth: read discovery document: %w", err)
	}
	o.provider = p
	o.verifier = p.VerifierContext(oidc.ClientContext(context.Background(), o.client), &oidc.Config{ClientID: o.cfg.ClientID})
	o.oauth = oauth2.Config{
		ClientID:     o.cfg.ClientID,
		ClientSecret: o.cfg.ClientSecret,
		Endpoint:     p.Endpoint(),
		RedirectURL:  o.cfg.RedirectURL,
		Scopes:       o.cfg.Scopes,
	}
	o.endSession = extra.EndSession
	return nil
}

type flow struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Expires  int64  `json:"e"`
	Silent   bool   `json:"q,omitempty"`
}

// Start begins a sign-in: it sets the flow cookie and returns the provider
// URL to redirect the browser to. A silent sign-in sends prompt=none, so the
// provider answers straight away: signed in if the visitor already has a
// provider session, an error (login_required) if not.
func (o *OIDC) Start(ctx context.Context, w http.ResponseWriter, silent bool) (string, error) {
	if err := o.ensure(ctx); err != nil {
		return "", err
	}
	f := flow{State: rand.Text(), Nonce: rand.Text(), Verifier: oauth2.GenerateVerifier(),
		Expires: time.Now().Add(oidcFlowTTL).Unix(), Silent: silent}
	payload, _ := json.Marshal(f)
	http.SetCookie(w, &http.Cookie{
		Name: oidcCookie, Value: o.sealer.Seal(sealPurpose, payload), Path: oidcCookiePath,
		MaxAge: int(oidcFlowTTL.Seconds()), HttpOnly: true, Secure: o.secure, SameSite: http.SameSiteLaxMode,
	})
	opts := []oauth2.AuthCodeOption{oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier)}
	if silent {
		opts = append(opts, oauth2.SetAuthURLParam("prompt", "none"))
	}
	return o.oauth.AuthCodeURL(f.State, opts...), nil
}

// Callback completes a sign-in from the provider's redirect. It always
// clears the flow cookie, so a flow can be used once.
func (o *OIDC) Callback(ctx context.Context, w http.ResponseWriter, r *http.Request) (Identity, error) {
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Path: oidcCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: o.secure, SameSite: http.SameSiteLaxMode})

	if err := o.ensure(ctx); err != nil {
		return Identity{}, err
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		return Identity{}, ErrOIDCNoFlow
	}
	raw, err := o.sealer.Open(sealPurpose, c.Value)
	if err != nil {
		return Identity{}, ErrOIDCNoFlow
	}
	var f flow
	if err := json.Unmarshal(raw, &f); err != nil || time.Now().Unix() > f.Expires {
		return Identity{}, ErrOIDCNoFlow
	}
	id, err := o.complete(ctx, r, f)
	if err != nil && f.Silent {
		return Identity{}, fmt.Errorf("%w: %w", ErrOIDCSilent, err)
	}
	return id, err
}

// complete checks the provider's redirect against the flow and exchanges
// the code.
func (o *OIDC) complete(ctx context.Context, r *http.Request, f flow) (Identity, error) {
	q := r.URL.Query()
	if subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(f.State)) != 1 {
		return Identity{}, ErrOIDCStateMismatch
	}
	if e := q.Get("error"); e != "" {
		return Identity{}, fmt.Errorf("%w: %s", ErrOIDCProvider, e)
	}
	code := q.Get("code")
	if code == "" {
		return Identity{}, fmt.Errorf("%w: no authorization code", ErrOIDCProvider)
	}

	ctx = oidc.ClientContext(ctx, o.client)
	tok, err := o.oauth.Exchange(ctx, code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return Identity{}, fmt.Errorf("auth: exchange code: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return Identity{}, errors.New("auth: token response has no id_token")
	}
	idt, err := o.verifier.Verify(ctx, rawID)
	if err != nil {
		return Identity{}, fmt.Errorf("auth: verify id_token: %w", err)
	}
	// go-oidc does not check the nonce; it is ours to compare.
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(f.Nonce)) != 1 {
		return Identity{}, ErrOIDCNonceMismatch
	}

	var claims struct {
		Email             string `json:"email"`
		Name              string `json:"name"`
		PreferredUsername string `json:"preferred_username"`
		SID               string `json:"sid"`
	}
	if err := idt.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("auth: id_token claims: %w", err)
	}
	// Zitadel leaves profile claims out of the ID token unless the app asks
	// for them, so fill gaps from the userinfo endpoint.
	if claims.Email == "" || claims.Name == "" {
		if ui, err := o.provider.UserInfo(ctx, oauth2.StaticTokenSource(tok)); err == nil && ui.Subject == idt.Subject {
			var more struct {
				Name              string `json:"name"`
				PreferredUsername string `json:"preferred_username"`
			}
			_ = ui.Claims(&more)
			if claims.Email == "" {
				claims.Email = ui.Email
			}
			if claims.Name == "" {
				claims.Name = more.Name
			}
			if claims.PreferredUsername == "" {
				claims.PreferredUsername = more.PreferredUsername
			}
		}
	}
	name := claims.Name
	if name == "" {
		name = claims.PreferredUsername
	}
	return Identity{Issuer: idt.Issuer, Subject: idt.Subject, Email: claims.Email, Name: name, IDToken: rawID, SID: claims.SID}, nil
}

// EndSessionURL is where to send the browser to end the provider session
// (RP-initiated logout). It is empty if the provider has no such endpoint.
func (o *OIDC) EndSessionURL(ctx context.Context, idTokenHint string) string {
	if err := o.ensure(ctx); err != nil || o.endSession == "" {
		return ""
	}
	u, err := url.Parse(o.endSession)
	if err != nil {
		return ""
	}
	q := u.Query()
	if idTokenHint != "" {
		q.Set("id_token_hint", idTokenHint)
	}
	q.Set("client_id", o.cfg.ClientID)
	q.Set("post_logout_redirect_uri", o.cfg.PostLogoutRedirectURL)
	u.RawQuery = q.Encode()
	return u.String()
}
