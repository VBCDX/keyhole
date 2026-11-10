package main

import (
	"fmt"
	"io"
	"net/url"

	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/service"
	"github.com/VBCDX/keyhole/internal/store"
)

// check loads the settings by the same rules serve does and, when the
// control-plane link is on (SERVICE_LISTEN_ADDR), opens each of its files as
// serve would: the service certificate and key (0600, the pair fits, it names
// vbcdx:service/keyhole), the control CA and the KEK (0600, owned by this
// user). It dials nothing, binds nothing and opens no database, so it can run
// before the server starts (the installer's gate and ExecStartPre).
func check(stdout io.Writer) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	// Only scheme://host of each URL is printed: never a userinfo part, which
	// could carry a credential.
	fmt.Fprintf(stdout, "ok: settings (listen %s, public %s, auth %s)\n", cfg.ListenAddr, cfg.Origin(), cfg.AuthMode)
	// The SQLite files' modes, reported, never changed here (serve fixes
	// them, AC5); a symlinked or foreign database fails, as serve would.
	if path, ok := store.SQLitePath(cfg.DatabaseURL); ok {
		findings, err := store.SQLitePermissions(path, cfg.DataDir, false)
		if err != nil {
			return fmt.Errorf("check: %w", err)
		}
		for _, f := range findings {
			fmt.Fprintf(stdout, "warn: %s\n", f.Message())
		}
	}
	s := cfg.Service
	if s.ListenAddr == "" {
		fmt.Fprintln(stdout, "ok: no control-plane link (SERVICE_LISTEN_ADDR is unset)")
		return nil
	}
	id, err := service.LoadIdentity(s.CertFile, s.KeyFile)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	ca, err := service.LoadCA(s.ControlCAFile)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	if _, err := envelope.LoadKEK(s.KEKFile); err != nil {
		return fmt.Errorf("check: %w", err)
	}
	if _, err := service.NewControl(s.ControlURL, s.ControlServerName, id, ca); err != nil {
		return fmt.Errorf("check: %w", err)
	}
	fmt.Fprintf(stdout, "ok: control-plane link (forward listener %s, control %s, certificate until %s)\n",
		s.ListenAddr, origin(s.ControlURL), id.Leaf.NotAfter.UTC().Format("2006-01-02T15:04:05Z"))
	return nil
}

// origin is scheme://host of a URL that already passed its checks.
func origin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable)"
	}
	return u.Scheme + "://" + u.Host
}
