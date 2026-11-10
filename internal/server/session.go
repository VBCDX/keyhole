package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/store"
)

// Sign-in methods recorded on a session.
const (
	methodLocal   = "local"
	methodZitadel = "zitadel"
)

// csrfHeader carries the session's CSRF token on state-changing requests.
const csrfHeader = "X-CSRF-Token"

// cookieName uses the __Host- prefix when cookies are Secure: the browser
// then refuses the cookie unless it is Secure, host-only and Path=/.
func (s *Server) cookieName() string {
	if s.cfg.SecureCookies() {
		return "__Host-keyhole_session"
	}
	return "keyhole_session"
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// startSession creates a server-side session and sets its cookie. Any
// session the browser already had is deleted first, so a session ID planted
// before sign-in never becomes a signed-in one. oidc is the Zitadel sign-in
// (zero for a local one): its ID token, sid and subject are kept for
// sign-out and back-channel logout. Signing in clears the signed-out marker.
func (s *Server) startSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID, method string, oidc auth.Identity) (store.Session, error) {
	if c, err := r.Cookie(s.cookieName()); err == nil && c.Value != "" {
		_ = s.store.DeleteSession(ctx, hashToken(c.Value))
	}
	token := rand.Text()
	now := s.now()
	sess := store.Session{
		IDHash:      hashToken(token),
		UserID:      userID,
		Method:      method,
		CSRFToken:   rand.Text(),
		IDTokenHint: oidc.IDToken,
		OIDCSID:     oidc.SID,
		OIDCSub:     oidc.Subject,
		CreatedAt:   now,
		ExpiresAt:   now.Add(s.cfg.SessionTTL),
	}
	if err := s.store.CreateSession(ctx, sess); err != nil {
		return store.Session{}, err
	}
	s.joinDemoOrg(ctx, userID)
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: token, Path: "/",
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
	s.clearMarker(w, signedOutCookie)
	s.clearMarker(w, ssoTriedCookie)
	return sess, nil
}

// joinDemoOrg makes a user who belongs to no organization an Owner of the
// demo organization, when SEED_DEMO_DATA is on. A failure is logged and
// does not block the sign-in.
func (s *Server) joinDemoOrg(ctx context.Context, userID string) {
	if !s.cfg.SeedDemoData {
		return
	}
	joined, err := s.store.JoinDemoOrg(ctx, userID)
	switch {
	case err != nil:
		s.log.Warn("join demo organization failed", "user", userID, "err", err)
	case joined:
		s.log.Info("user joined the demo organization as Owner", "user", userID, "org", store.DemoOrgID)
	}
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: s.cfg.SecureCookies(), SameSite: http.SameSiteLaxMode,
	})
}

var errNoSession = errors.New("no session")

// currentSession resolves the request's session cookie.
func (s *Server) currentSession(r *http.Request) (store.Session, store.User, error) {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" {
		return store.Session{}, store.User{}, errNoSession
	}
	sess, u, err := s.store.GetSession(r.Context(), hashToken(c.Value), s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, store.User{}, errNoSession
	}
	return sess, u, err
}

type sessionKey struct{}

type authed struct {
	session store.Session
	user    store.User
}

// requireSession rejects requests without a valid session, and checks the
// CSRF token on any method that is not GET, HEAD or OPTIONS.
func (s *Server) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, u, err := s.currentSession(r)
		if errors.Is(err, errNoSession) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", "sign in first")
			return
		}
		if err != nil {
			s.log.Error("session lookup failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "session lookup failed")
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !validCSRF(r, sess) {
				writeError(w, http.StatusForbidden, "csrf", "missing or wrong "+csrfHeader+" header")
				return
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, authed{sess, u})))
	}
}

func validCSRF(r *http.Request, sess store.Session) bool {
	got := r.Header.Get(csrfHeader)
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) == 1
}

func fromContext(ctx context.Context) authed {
	a, _ := ctx.Value(sessionKey{}).(authed)
	return a
}
