package server

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/store"
)

type authConfigResponse struct {
	Local   bool `json:"local"`
	Zitadel bool `json:"zitadel"`
	// SilentSignIn tells the web app to try /auth/oidc/login?prompt=none
	// once before showing the sign-in page.
	SilentSignIn bool       `json:"silent_sign_in"`
	SuiteApps    []suiteApp `json:"suite_apps"`
	// GAMeasurementID is the GA4 measurement id; empty disables
	// analytics in the web app. Public config, like the OIDC client id.
	GAMeasurementID string `json:"ga_measurement_id"`
}

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, authConfigResponse{
		Local:           s.cfg.AuthMode.Local(),
		Zitadel:         s.cfg.AuthMode.Zitadel(),
		SilentSignIn:    s.silentSignInAllowed(r),
		SuiteApps:       s.suiteApps(),
		GAMeasurementID: s.cfg.GAMeasurementID,
	})
}

type meResponse struct {
	User struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	} `json:"user"`
	Method           string    `json:"method"`
	SessionStartedAt time.Time `json:"session_started_at"`
	SessionExpiresAt time.Time `json:"session_expires_at"`
	CSRFToken        string    `json:"csrf_token"`
}

func newMe(sess store.Session, u store.User) meResponse {
	var m meResponse
	m.User.ID, m.User.Email, m.User.Name = u.ID, u.Email, u.Name
	m.Method = sess.Method
	m.SessionStartedAt, m.SessionExpiresAt = sess.CreatedAt.UTC(), sess.ExpiresAt.UTC()
	m.CSRFToken = sess.CSRFToken
	return m
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := fromContext(r.Context())
	writeJSON(w, http.StatusOK, newMe(a.session, a.user))
}

// NormalizeEmail is how local account emails are compared and stored.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AuthMode.Local() {
		writeError(w, http.StatusNotFound, "not_enabled", "local accounts are not enabled")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	email := NormalizeEmail(req.Email)
	if email == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}

	ip := s.clientIP(r)
	addr := limitKey(ip)
	accountKey, clientKey, addressKey := "acct:"+email, "acct:"+email+"|addr:"+addr, "addr:"+addr
	for _, check := range []struct {
		l   *auth.Limiter
		key string
	}{{s.accountLimiter, clientKey}, {s.addressLimiter, addressKey}} {
		if ok, wait := check.l.Allowed(check.key); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many failed sign-ins; try again later")
			return
		}
	}
	if d := s.accountBackoff.Delay(accountKey); d > 0 {
		if !s.waiting.Enter(addressKey) {
			w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(d.Seconds()))))
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many sign-ins waiting; try again later")
			return
		}
		err := sleep(r.Context(), d)
		s.waiting.Leave(addressKey)
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "busy", "sign-in is busy; try again")
			return
		}
	}

	userID, hash, err := s.store.LocalCredentials(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("credential lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "sign-in failed")
		return
	}
	select {
	case s.hashSlots <- struct{}{}:
	case <-r.Context().Done():
		writeError(w, http.StatusServiceUnavailable, "busy", "sign-in is busy; try again")
		return
	}
	ok := false
	if errors.Is(err, store.ErrNotFound) {
		auth.BurnPasswordCheck(req.Password)
	} else if ok, err = auth.VerifyPassword(hash, req.Password); err != nil {
		s.log.Error("stored password hash is unreadable", "user", userID, "err", err)
	}
	<-s.hashSlots
	if !ok {
		s.accountLimiter.Fail(clientKey)
		s.addressLimiter.Fail(addressKey)
		s.accountBackoff.Fail(accountKey)
		s.log.Info("local sign-in failed", "ip", ip)
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "wrong email or password")
		return
	}
	s.accountLimiter.Reset(clientKey)
	s.addressLimiter.Reset(addressKey)
	s.accountBackoff.Reset(accountKey)
	_ = s.store.TouchLocalIdentity(r.Context(), email)

	sess, err := s.startSession(r.Context(), w, r, userID, methodLocal, auth.Identity{})
	if err != nil {
		s.log.Error("create session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "sign-in failed")
		return
	}
	u, err := s.store.GetUser(r.Context(), userID)
	if err != nil {
		s.log.Error("load user failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "sign-in failed")
		return
	}
	s.log.Info("local sign-in", "user", userID)
	writeJSON(w, http.StatusOK, newMe(sess, u))
}

// sleep waits d, or returns ctx's error if it ends first.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// handleLogout deletes the server session. For a Zitadel session it also
// returns the provider's end-session URL, which the web app navigates to so
// the Zitadel session ends as well; Zitadel then tells the other suite apps
// through back-channel logout. It marks the browser as signed out on
// purpose, so silent sign-in does not sign it straight back in.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, _, err := s.currentSession(r)
	if errors.Is(err, errNoSession) {
		s.clearSessionCookie(w)
		s.setMarker(w, signedOutCookie, 0)
		writeJSON(w, http.StatusOK, map[string]string{"redirect_url": "/"})
		return
	}
	if err != nil {
		s.log.Error("session lookup failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "sign-out failed")
		return
	}
	if !validCSRF(r, sess) {
		writeError(w, http.StatusForbidden, "csrf", "missing or wrong "+csrfHeader+" header")
		return
	}
	if err := s.store.DeleteSession(r.Context(), sess.IDHash); err != nil {
		s.log.Error("delete session failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal", "sign-out failed")
		return
	}
	s.clearSessionCookie(w)
	s.setMarker(w, signedOutCookie, 0)

	redirect := "/"
	if sess.Method == methodZitadel && s.oidc != nil {
		if u := s.oidc.EndSessionURL(r.Context(), sess.IDTokenHint); u != "" {
			redirect = u
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"redirect_url": redirect})
}

// handleOIDCLogin starts a Zitadel sign-in. With ?prompt=none it is a silent
// sign-in, tried at most once per ssoTriedTTL and never after an explicit
// sign-out; when it may not run, or cannot start, the browser goes back to
// the sign-in page without an error.
func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Redirect(w, r, "/?error=zitadel_not_enabled", http.StatusFound)
		return
	}
	silent := r.URL.Query().Get("prompt") == "none"
	if silent {
		if _, _, err := s.currentSession(r); err == nil || !s.silentSignInAllowed(r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		s.setMarker(w, ssoTriedCookie, ssoTriedTTL)
	}
	target, err := s.oidc.Start(r.Context(), w, silent)
	if err != nil && silent {
		s.log.Warn("start silent Zitadel sign-in failed", "err", err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err != nil {
		s.log.Error("start Zitadel sign-in failed", "err", err)
		http.Redirect(w, r, "/?error=provider_unavailable", http.StatusFound)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.Redirect(w, r, "/?error=zitadel_not_enabled", http.StatusFound)
		return
	}
	id, err := s.oidc.Callback(r.Context(), w, r)
	if errors.Is(err, auth.ErrOIDCSilent) {
		// login_required (no Zitadel session) is the usual answer; any
		// other failure also just shows the sign-in page.
		s.log.Debug("silent Zitadel sign-in did not complete", "err", err)
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if err != nil {
		s.log.Warn("Zitadel sign-in rejected", "err", err)
		http.Redirect(w, r, "/?error=signin_failed", http.StatusFound)
		return
	}
	u, err := s.store.UpsertOIDCUser(r.Context(), id.Issuer, id.Subject, id.Email, id.Name)
	if err != nil {
		s.log.Error("link Zitadel user failed", "err", err)
		http.Redirect(w, r, "/?error=signin_failed", http.StatusFound)
		return
	}
	if _, err := s.startSession(r.Context(), w, r, u.ID, methodZitadel, id); err != nil {
		s.log.Error("create session failed", "err", err)
		http.Redirect(w, r, "/?error=signin_failed", http.StatusFound)
		return
	}
	s.log.Info("Zitadel sign-in", "user", u.ID)
	http.Redirect(w, r, "/", http.StatusFound)
}
