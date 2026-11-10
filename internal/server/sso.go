package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/store"
)

// Shared sign-in across the VBCDX suite (issue 9).
//
// Silent sign-in: a signed-out visitor is sent once through
// /auth/oidc/login?prompt=none. With a Zitadel session from another suite
// app they come back signed in; without one Zitadel answers
// login_required and they see the sign-in page. Two cookies keep it from
// looping or undoing a sign-out:
//
//   - ssoTriedCookie is set just before the silent redirect and blocks
//     another attempt while it lasts;
//   - signedOutCookie is set on an explicit sign-out and blocks silent
//     sign-in until the next successful sign-in or until the browser
//     closes, whichever comes first (a session cookie, as in Dispatch).
//
// Single sign-out: Zitadel POSTs a logout token to
// /auth/oidc/backchannel-logout when a Zitadel session ends, and every
// Keyhole session holding that session's sid is deleted.
const (
	ssoTriedCookie  = "keyhole_sso_tried"
	ssoTriedTTL     = time.Minute
	signedOutCookie = "keyhole_signed_out"

	maxLogoutBody = 64 << 10
)

// markerName uses the __Host- prefix when cookies are Secure, like the
// session cookie.
func (s *Server) markerName(base string) string {
	if s.cfg.SecureCookies() {
		return "__Host-" + base
	}
	return base
}

// setMarker sets a marker cookie for ttl; a zero ttl makes it a session
// cookie (no Max-Age or Expires) that lasts until the browser closes.
func (s *Server) setMarker(w http.ResponseWriter, base string, ttl time.Duration) {
	c := &http.Cookie{
		Name: s.markerName(base), Value: "1", Path: "/",
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	}
	if ttl > 0 {
		c.MaxAge = int(ttl.Seconds())
	}
	http.SetCookie(w, c)
}

func (s *Server) clearMarker(w http.ResponseWriter, base string) {
	http.SetCookie(w, &http.Cookie{
		Name: s.markerName(base), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) hasMarker(r *http.Request, base string) bool {
	c, err := r.Cookie(s.markerName(base))
	return err == nil && c.Value != ""
}

// silentSignInAllowed reports whether a silent sign-in may be tried now.
func (s *Server) silentSignInAllowed(r *http.Request) bool {
	return s.oidc != nil && !s.hasMarker(r, ssoTriedCookie) && !s.hasMarker(r, signedOutCookie)
}

type suiteApp struct {
	Name    string `json:"name"`
	URL     string `json:"url"`
	Current bool   `json:"current"`
}

// suiteApps is SUITE_APPS for the app switcher; the entry served from
// PUBLIC_URL's origin is the current app.
func (s *Server) suiteApps() []suiteApp {
	out := make([]suiteApp, 0, len(s.cfg.SuiteApps))
	for _, a := range s.cfg.SuiteApps {
		cur := false
		if u, err := url.Parse(a.URL); err == nil {
			cur = u.Scheme+"://"+u.Host == s.cfg.Origin()
		}
		out = append(out, suiteApp{Name: a.Name, URL: a.URL, Current: cur})
	}
	return out
}

// handleBackchannelLogout implements the relying-party side of OpenID
// Connect Back-Channel Logout 1.0: Zitadel POSTs a signed logout_token when
// a Zitadel session ends. It needs no session or CSRF token; the token's
// signature is the authentication, and nothing is trusted until it
// validates. 200 means done, 400 a refused token.
func (s *Server) handleBackchannelLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.oidc == nil {
		logoutError(w, http.StatusNotFound, "invalid_request", "Zitadel sign-in is not enabled")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLogoutBody)
	if err := r.ParseForm(); err != nil {
		logoutError(w, http.StatusBadRequest, "invalid_request", "body must be a form with logout_token")
		return
	}
	tok, err := s.oidc.VerifyLogoutToken(r.Context(), r.PostForm.Get("logout_token"))
	if errors.Is(err, auth.ErrLogoutToken) {
		s.log.Warn("back-channel logout refused", "err", err)
		logoutError(w, http.StatusBadRequest, "invalid_request", "invalid logout_token")
		return
	}
	if err != nil {
		s.log.Error("back-channel logout: provider unavailable", "err", err)
		logoutError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "cannot validate the logout_token right now")
		return
	}
	n, err := s.store.EndOIDCSessions(r.Context(), tok.JTI, tok.ExpiresAt, tok.SID, tok.Subject)
	if errors.Is(err, store.ErrReplayedLogout) {
		s.log.Warn("back-channel logout refused: jti already used")
		logoutError(w, http.StatusBadRequest, "invalid_request", "invalid logout_token")
		return
	}
	if err != nil {
		s.log.Error("back-channel logout: delete sessions failed", "err", err)
		logoutError(w, http.StatusInternalServerError, "server_error", "sign-out failed")
		return
	}
	s.log.Info("back-channel logout", "sessions_ended", n, "by_sid", tok.SID != "")
	w.WriteHeader(http.StatusOK)
}

// logoutError writes the OAuth-style error body the Back-Channel Logout
// spec suggests.
func logoutError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": desc})
}
