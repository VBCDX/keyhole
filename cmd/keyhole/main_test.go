package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

func TestUserCreateCommand(t *testing.T) {
	b := storetest.Backends(t)[0]
	t.Setenv("DATABASE_URL", b.URL)
	var out bytes.Buffer
	ctx := context.Background()

	err := userCmd(ctx, []string{"create", "--email", "Alex@Example.com", "--name", "Alex", "--password-stdin"},
		strings.NewReader("correct horse battery\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "created alex@example.com") {
		t.Fatalf("output %q", out.String())
	}

	st := storetest.Open(t, b)
	_, hash, err := st.LocalCredentials(ctx, "alex@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := auth.VerifyPassword(hash, "correct horse battery"); !ok {
		t.Fatal("stored hash does not verify the password given on stdin")
	}

	for name, args := range map[string][]string{
		"duplicate":    {"create", "--email", "alex@example.com", "--password-stdin"},
		"no email":     {"create", "--password-stdin"},
		"bad email":    {"create", "--email", "not-an-email", "--password-stdin"},
		"no create":    {"delete"},
		"unknown flag": {"create", "--admin"},
	} {
		if err := userCmd(ctx, args, strings.NewReader("correct horse battery\n"), io.Discard); err == nil {
			t.Errorf("%s: succeeded", name)
		}
	}
	if err := userCmd(ctx, []string{"create", "--email", "short@example.com", "--password-stdin"},
		strings.NewReader("short\n"), io.Discard); err == nil {
		t.Error("short password accepted")
	}
}

func TestBootstrapOnlyIntoEmptyDatabase(t *testing.T) {
	b := storetest.Backends(t)[0]
	st := storetest.Open(t, b)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	boot := config.Bootstrap{Email: "admin@example.com", Name: "Admin", Password: "bootstrap password"}

	if err := bootstrapAdmin(ctx, st, config.Bootstrap{}, log); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.CountUsers(ctx); n != 0 {
		t.Fatal("bootstrap with no variables created a user")
	}
	if err := bootstrapAdmin(ctx, st, boot, log); err != nil {
		t.Fatal(err)
	}
	if n, _ := st.CountUsers(ctx); n != 1 {
		t.Fatalf("users = %d after bootstrap, want 1", n)
	}
	// Second start with the variables still set, and a changed password:
	// nothing happens, the original password stays.
	boot.Password = "a different password"
	if err := bootstrapAdmin(ctx, st, boot, log); err != nil {
		t.Fatal(err)
	}
	_, hash, _ := st.LocalCredentials(ctx, "admin@example.com")
	if ok, _ := auth.VerifyPassword(hash, "bootstrap password"); !ok {
		t.Fatal("bootstrap ran again on a non-empty database")
	}
}

func TestHelpPrintsUsageWithoutServing(t *testing.T) {
	ctx := context.Background()
	serve := func(context.Context) error {
		t.Fatal("the server started")
		return nil
	}
	for _, arg := range []string{"help", "-h", "-help", "--help"} {
		var out bytes.Buffer
		if err := run(ctx, []string{arg}, strings.NewReader(""), &out, serve); err != nil {
			t.Fatalf("keyhole %s: %v", arg, err)
		}
		if !strings.HasPrefix(out.String(), "usage: keyhole") {
			t.Fatalf("keyhole %s printed %q", arg, out.String())
		}
	}
	for _, arg := range []string{"--port", "-x", "bogus"} {
		if err := run(ctx, []string{arg}, strings.NewReader(""), io.Discard, serve); err == nil {
			t.Errorf("keyhole %s: no error", arg)
		}
	}
	served := false
	for _, args := range [][]string{nil, {"serve"}} {
		served = false
		err := run(ctx, args, strings.NewReader(""), io.Discard, func(context.Context) error { served = true; return nil })
		if err != nil || !served {
			t.Errorf("keyhole %v: served=%v err=%v; want the server", args, served, err)
		}
	}
}

// The release job stamps the version with -ldflags -X main.version=...; CI
// checks the built binary's output against the tag.
func TestVersionPrintsTheStampedVersionWithoutServing(t *testing.T) {
	serve := func(context.Context) error {
		t.Fatal("the server started")
		return nil
	}
	defer func(v string) { version = v }(version)
	version = "1.2.3-rc.1"
	for _, arg := range []string{"version", "--version"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{arg}, strings.NewReader(""), &out, serve); err != nil {
			t.Fatalf("keyhole %s: %v", arg, err)
		}
		if out.String() != "keyhole 1.2.3-rc.1\n" {
			t.Fatalf("keyhole %s printed %q", arg, out.String())
		}
	}
}

func TestHTTPServerTimeoutsArePinned(t *testing.T) {
	// After the headers arrive, a client must not be able to hold the
	// connection (and its goroutine) by sending a body or reading a response
	// arbitrarily slowly.
	hs := newHTTPServer("127.0.0.1:0", http.NotFoundHandler())
	if hs.ReadHeaderTimeout != 10*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want 10s", hs.ReadHeaderTimeout)
	}
	if hs.ReadTimeout != 15*time.Second {
		t.Errorf("ReadTimeout = %v, want 15s", hs.ReadTimeout)
	}
	if hs.WriteTimeout != 30*time.Second {
		t.Errorf("WriteTimeout = %v, want 30s", hs.WriteTimeout)
	}
	if hs.IdleTimeout != 2*time.Minute {
		t.Errorf("IdleTimeout = %v, want 2m", hs.IdleTimeout)
	}
	if hs.MaxHeaderBytes != 1<<20 {
		t.Errorf("MaxHeaderBytes = %d, want %d", hs.MaxHeaderBytes, 1<<20)
	}
	if hs.Addr != "127.0.0.1:0" {
		t.Errorf("Addr = %q, want the caller's listen address", hs.Addr)
	}
	// The chosen values leave room for the service's own clients: outbound
	// calls run inside the handler, under the response (write) deadline, so
	// WriteTimeout must clear the 10s upstream/OIDC client timeouts and the
	// healthcheck's 3s client timeout. ReadTimeout only bounds reading
	// the request and is independent of those clocks.
	if hs.WriteTimeout <= 10*time.Second {
		t.Errorf("WriteTimeout = %v, must exceed the upstream/OIDC client timeouts (10s)", hs.WriteTimeout)
	}
	if hs.WriteTimeout <= 3*time.Second {
		t.Errorf("WriteTimeout = %v, must exceed the healthcheck client timeout (3s)", hs.WriteTimeout)
	}
}

// The healthcheck probes the address the server binds. With nothing set
// that is config's default; the Dockerfile's LISTEN_ADDR=:8080 is probed on
// IPv4 loopback.
func TestHealthURLFollowsConfig(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"unset", nil, "http://" + config.DefaultListenAddr + "/healthz"},
		{"blank", map[string]string{"LISTEN_ADDR": "  "}, "http://" + config.DefaultListenAddr + "/healthz"},
		{"container", map[string]string{"LISTEN_ADDR": ":8080"}, "http://127.0.0.1:8080/healthz"},
		{"explicit", map[string]string{"LISTEN_ADDR": "10.0.0.5:9000"}, "http://10.0.0.5:9000/healthz"},
	}
	for _, c := range cases {
		if got := healthURL(env(c.env)); got != c.want {
			t.Errorf("%s: healthURL = %q, want %q", c.name, got, c.want)
		}
	}
	// The default must be what the server itself would bind.
	t.Setenv("LISTEN_ADDR", "")
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := healthURL(env(nil)), "http://"+cfg.ListenAddr+"/healthz"; got != want {
		t.Errorf("healthURL = %q, server binds %q", got, want)
	}
}

func TestSeedCommandIsIdempotent(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		t.Setenv("DATABASE_URL", b.URL)
		ctx := context.Background()
		serve := func(context.Context) error { t.Fatal("the server started"); return nil }

		var out bytes.Buffer
		if err := run(ctx, []string{"seed"}, strings.NewReader(""), &out, serve); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out.String(), "loaded ") || !strings.Contains(out.String(), "org_acme") {
			t.Fatalf("first seed printed %q", out.String())
		}
		out.Reset()
		if err := run(ctx, []string{"seed"}, strings.NewReader(""), &out, serve); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "nothing changed") {
			t.Fatalf("second seed printed %q", out.String())
		}
		if err := run(ctx, []string{"seed", "--force"}, strings.NewReader(""), io.Discard, serve); err == nil {
			t.Error("keyhole seed --force: no error")
		}
	})
}

func TestSeedDemoAtStartOnlyIntoEmptyDatabase(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		log := slog.New(slog.NewTextHandler(io.Discard, nil))

		if err := seedDemoAtStart(ctx, st, false, log); err != nil {
			t.Fatal(err)
		}
		if has, _ := st.HasOrgs(ctx); has {
			t.Fatal("SEED_DEMO_DATA=false seeded the database")
		}
		if err := seedDemoAtStart(ctx, st, true, log); err != nil {
			t.Fatal(err)
		}
		if has, _ := st.HasOrgs(ctx); !has {
			t.Fatal("SEED_DEMO_DATA=true did not seed an empty database")
		}
		// Restarting with the setting still on is a no-op.
		if err := seedDemoAtStart(ctx, st, true, log); err != nil {
			t.Fatal(err)
		}
	})
}
