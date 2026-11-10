// Package server is Keyhole's HTTP surface: the JSON API, sign-in and the
// embedded web app.
package server

import (
	"context"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store"
)

// Failed sign-ins. One client address (an IPv6 client's /64) is refused
// for failWindow after accountFailLimit failures on one account, or
// addressFailLimit in all.
// Across every address an account is slowed, never refused: past
// accountFreeFailures each attempt waits accountDelay, doubling per failure
// up to accountMaxDelay, so a guesser elsewhere cannot lock the owner out of
// their correct password. A successful sign-in clears all three. At most
// waitPerAddress attempts from one address, and waitTotal in all, wait out
// a slow-down at once; any more get 429 straight away.
const (
	accountFailLimit    = 10
	addressFailLimit    = 50
	failWindow          = 15 * time.Minute
	accountFreeFailures = 5
	accountDelay        = time.Second
	accountMaxDelay     = 10 * time.Second
	waitPerAddress      = 2
	waitTotal           = 256
)

// Server holds the dependencies every handler needs.
type Server struct {
	cfg   *config.Config
	store *store.Store
	oidc  *auth.OIDC
	log   *slog.Logger
	web   fs.FS

	accountLimiter *auth.Limiter // keyed by account and client address
	addressLimiter *auth.Limiter
	accountBackoff *auth.Backoff
	waiting        *auth.Waiting // keyed by client address
	// hashSlots caps concurrent password checks: each argon2id check holds
	// 19 MiB, so unbounded parallel sign-ins could exhaust memory.
	hashSlots chan struct{}
	now       func() time.Time
}

// New wires a Server. web is the built web app (may be empty); oidc is nil
// unless AUTH_MODE includes zitadel.
func New(cfg *config.Config, st *store.Store, oidc *auth.OIDC, web fs.FS, log *slog.Logger) *Server {
	return &Server{
		cfg: cfg, store: st, oidc: oidc, web: web, log: log,
		accountLimiter: auth.NewLimiter(accountFailLimit, failWindow),
		addressLimiter: auth.NewLimiter(addressFailLimit, failWindow),
		accountBackoff: auth.NewBackoff(accountFreeFailures, accountDelay, accountMaxDelay, failWindow),
		waiting:        auth.NewWaiting(waitPerAddress, waitTotal),
		hashSlots:      make(chan struct{}, max(2, runtime.NumCPU())),
		now:            time.Now,
	}
}

// Handler returns the full HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	mux.HandleFunc("GET /api/auth/config", s.handleAuthConfig)
	mux.HandleFunc("POST /api/auth/login", s.handleLocalLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/me", s.requireSession(s.handleMe))
	s.apiRoutes(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "no such endpoint")
	})

	mux.HandleFunc("GET /auth/oidc/login", s.handleOIDCLogin)
	mux.HandleFunc("GET /auth/oidc/callback", s.handleOIDCCallback)
	mux.HandleFunc("POST /auth/oidc/backchannel-logout", s.handleBackchannelLogout)

	mux.Handle("/", s.staticHandler())

	// Go's cross-origin protection rejects unsafe-method requests a browser
	// marks as cross-site (Sec-Fetch-Site, falling back to Origin vs Host).
	// https://pkg.go.dev/net/http#CrossOriginProtection
	cop := http.NewCrossOriginProtection()
	if err := cop.AddTrustedOrigin(s.cfg.Origin()); err != nil {
		s.log.Warn("cross-origin protection: could not trust PUBLIC_URL origin", "err", err)
	}
	// Zitadel calls back-channel logout server to server, and the signed
	// logout token, not the request's origin, is what authenticates it.
	cop.AddInsecureBypassPattern("POST /auth/oidc/backchannel-logout")
	return s.withSecurityHeaders(cop.Handler(mux))
}

// RunMaintenance removes expired sessions, the IDs of expired logout tokens
// and stale limiter entries until
// ctx is cancelled.
func (s *Server) RunMaintenance(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.store.DeleteExpiredSessions(ctx, s.now()); err != nil {
				s.log.Warn("expired session cleanup failed", "err", err)
			} else if n > 0 {
				s.log.Info("expired sessions removed", "count", n)
			}
			if _, err := s.store.DeleteExpiredLogoutJTIs(ctx, s.now()); err != nil {
				s.log.Warn("expired logout token cleanup failed", "err", err)
			}
			s.accountLimiter.Sweep()
			s.addressLimiter.Sweep()
			s.accountBackoff.Sweep()
		}
	}
}

// securityHeaders sets the headers every response carries. hsts adds
// Strict-Transport-Security, which only means something when Keyhole is
// served over https. The CSP allows the GA4 tag hosts only when analytics is
// configured (GA_MEASUREMENT_ID); with no measurement id the strict default
// policy stands unchanged.
func securityHeaders(next http.Handler, hsts bool, gaMeasurementID string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if hsts {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy(gaMeasurementID))
		next.ServeHTTP(w, r)
	})
}

// withSecurityHeaders applies securityHeaders per request so the CSP (and
// HSTS) follow the current configuration.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(next, s.cfg.SecureCookies(), s.cfg.GAMeasurementID).ServeHTTP(w, r)
	})
}

// contentSecurityPolicy is the CSP header. The GA4 hosts enter the policy only
// when a measurement id is configured; with analytics off nothing about the
// policy changes.
func contentSecurityPolicy(gaMeasurementID string) string {
	const base = "default-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
	if gaMeasurementID == "" {
		return base
	}
	return "default-src 'self'; " +
		"img-src 'self' data: https://*.google-analytics.com https://www.googletagmanager.com; " +
		"script-src 'self' https://www.googletagmanager.com; " +
		"connect-src 'self' https://*.google-analytics.com https://*.analytics.google.com https://www.googletagmanager.com; " +
		"object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"database": "ok"}
	status := http.StatusOK
	if err := s.store.Ping(ctx); err != nil {
		// The driver error can name hosts and users; it goes to the log,
		// not to unauthenticated callers.
		s.log.Warn("readiness: database unreachable", "err", err)
		checks["database"] = "unavailable"
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, checks)
}
