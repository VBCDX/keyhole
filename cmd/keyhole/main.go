// Command keyhole is the Keyhole server: the API and the web app in one
// binary.
//
//	keyhole [serve]                     run the server (the default)
//	keyhole user create --email E ...   create a local account
//	keyhole seed                        load the demo data (idempotent)
//	keyhole load --file F               load tool/grant rows from a fixture
//	keyhole kek generate --out F        write a new key-encryption key file
//	keyhole keys put --org O --key K --kek-file F
//	                                    store a Local key's value from stdin
//	keyhole grants disable|enable --org O --grant G
//	                                    switch a grant off or on (audited)
//	keyhole members add|remove --org O --ws W --sub ID [--kind user|agent]
//	                                    who a workspace's grants cover (audited)
//	keyhole tools private-allow --org O --tool T (--cidr C ... | --none)
//	                                    the private addresses a tool may reach (audited)
//	keyhole service csr --key-out K --csr-out C
//	                                    the key + CSR for `vbcdx-control services add`
//	keyhole healthcheck                 exit 0 if /healthz answers (for containers)
//	keyhole check                       check the settings and link files; start nothing
//	keyhole --env-file F <command>      read F (systemd EnvironmentFile= syntax) first
//	keyhole version, --version          print "keyhole <version>"
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/server"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/web"
)

const usage = `usage: keyhole [--env-file FILE] [command]

  --env-file FILE                        read KEY=VALUE settings from FILE first (the
                                         unit's EnvironmentFile=); they override the
                                         environment. Quotes and backslashes are refused

commands:
  serve                                  run the server (the default)
  user create --email EMAIL [--name NAME] [--password-stdin]
                                         create a local account
  seed                                   load the demo organization; running it
                                         again changes nothing
  load --file FIXTURE.json               load non-secret rows (workspaces, agents,
                                         stores, keys, tools, grants); idempotent
  kek generate --out FILE                write a new key-encryption key (0600)
  keys put --org ORG --key KEY --kek-file FILE
                                         store the next version of a Local key's
                                         value, read from standard input
  grants disable|enable --org ORG --grant GRANT
                                         switch a grant off or on (audited)
  members add|remove --org ORG --ws WS --sub ID [--kind user|agent]
                                         who a workspace's grants cover (audited): an
                                         added member gets all its enabled grants; a
                                         removal stops at the next resolve (a held value
                                         lives at most its ttl); plain members only
  tools private-allow --org ORG --tool TOOL (--cidr IP|CIDR ... | --none)
                                         the private addresses (loopback, RFC 1918,
                                         ULA, CGNAT) a sidecar may let this tool reach;
                                         publishes a new tool version (audited).
                                         Link-local is never reachable
  service csr --key-out FILE --csr-out FILE
                                         make the key and CSR to enroll Keyhole
                                         with vbcdx-control (services add)
  healthcheck                            exit 0 if /healthz answers (for containers)
  check                                  check the settings and, with SERVICE_LISTEN_ADDR,
                                         the link's certificate, key, control CA and KEK;
                                         dials, binds and opens nothing
  version, --version                     print "keyhole <version>"
  help, -h, --help                       print this message; <command> --help prints
                                         one command's usage

` + exitCodesHelp

// exitCodesHelp ends the top-level usage. The tests check every code main
// can exit with is here, and nothing else.
const exitCodesHelp = `exit codes:
  0                                      success (healthcheck: /healthz answered)
  1                                      the command failed; the message says why
  2                                      usage: an unknown command or flag, or a
                                         missing or bad argument
`

// version is the release version, stamped at build time by the release job:
// go build -ldflags "-X main.version=<version>". A build without it says so.
var version = "0.0.0-dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Whatever the caller's umask, nothing keyhole creates (the SQLite
	// database and its -wal/-shm, the data directory, keys) is readable by
	// group or others.
	syscall.Umask(0o077)

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, serve); err != nil {
		fmt.Fprintln(os.Stderr, "keyhole:", err)
		os.Exit(exitCode(err))
	}
}

// run routes to a subcommand. serve is passed in so tests can check
// which arguments would start the server without starting it.
func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer, serve func(context.Context) error) error {
	// --env-file F first: the settings a systemd unit reads with
	// EnvironmentFile=, set over the process environment for any command.
	envFile := ""
	if len(args) > 0 && (args[0] == "--env-file" || strings.HasPrefix(args[0], "--env-file=")) {
		path, rest := strings.TrimPrefix(args[0], "--env-file="), args[1:]
		if args[0] == "--env-file" {
			if len(rest) == 0 {
				return usageErr("--env-file needs a file")
			}
			path, rest = rest[0], rest[1:]
		}
		if path == "" {
			return usageErr("--env-file needs a file")
		}
		// Read after the help checks below: help needs no settings, so a
		// missing or bad file doesn't stop it.
		envFile = path
		args = rest
	}
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	// <command> --help prints that command's usage and runs nothing: no
	// check, no dial (healthcheck), no store.
	if text, ok := commandHelp(cmd); ok && cmd != "help" && cmd != "version" {
		for _, a := range args {
			if isHelp(a) {
				_, err := io.WriteString(stdout, text)
				return err
			}
		}
	}
	if cmd == "help" || isHelp(cmd) {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	if envFile != "" {
		if err := applyEnvFile(envFile, os.Setenv); err != nil {
			return err
		}
	}
	switch cmd {
	case "check":
		if len(args) > 0 {
			return usageErr("check takes no arguments; usage: keyhole check")
		}
		return check(stdout)
	case "serve":
		// An argument it doesn't know is refused, never ignored: serve
		// binds a port and opens the store.
		if len(args) > 0 {
			return usageErr("serve takes no arguments; usage: keyhole serve")
		}
		return serve(ctx)
	case "user":
		return userCmd(ctx, args, stdin, stdout)
	case "seed":
		return seedCmd(ctx, args, stdout)
	case "load":
		return loadCmd(ctx, args, stdout)
	case "kek":
		return kekCmd(args, stdout)
	case "keys":
		return keysCmd(ctx, args, stdin, stdout)
	case "service":
		return serviceCmd(args, stdout)
	case "grants":
		return grantsCmd(ctx, args, stdout)
	case "members":
		return membersCmd(ctx, args, stdout)
	case "tools":
		return toolsCmd(ctx, args, stdout)
	case "healthcheck":
		return healthcheck(ctx)
	case "version", "--version":
		_, err := fmt.Fprintf(stdout, "keyhole %s\n", version)
		return err
	default:
		name, ok := shownName(cmd)
		if !ok || strings.HasPrefix(cmd, "-") {
			name = ""
		}
		if name == "" {
			return usageErr("unknown command (want serve, check, user, seed, load, kek, keys, service, grants, members, tools, healthcheck, version or help)")
		}
		return usageErr("unknown command %q (want serve, check, user, seed, load, kek, keys, service, grants, members, tools, healthcheck, version or help)", name)
	}
}

// openStore opens the configured database as every command does: for SQLite,
// a loose database or DATA_DIR from an older release is tightened, with a
// warning on log (stderr when nil), and a symlinked or foreign one refuses.
func openStore(ctx context.Context, cfg *config.Config, log *slog.Logger) (*store.Store, error) {
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	return store.Open(ctx, cfg.DatabaseURL, store.WithDataDir(cfg.DataDir), store.WithLogger(log))
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func serve(ctx context.Context) error {
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	log := newLogger(cfg.LogLevel)

	st, err := openStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer st.Close()
	log.Info("database ready", "dialect", st.Dialect())

	if err := bootstrapAdmin(ctx, st, cfg.Bootstrap, log); err != nil {
		return err
	}
	if err := seedDemoAtStart(ctx, st, cfg.SeedDemoData, log); err != nil {
		return err
	}

	var oidc *auth.OIDC
	if cfg.AuthMode.Zitadel() {
		if cfg.SessionSecret == "" {
			log.Warn("SESSION_SECRET is not set: a Zitadel sign-in in progress fails if Keyhole restarts or another instance takes the callback")
		}
		sealer, err := auth.NewSealer(cfg.SessionSecret)
		if err != nil {
			return err
		}
		oidc = auth.NewOIDC(cfg.OIDC, cfg.SecureCookies(), sealer)
	}

	srv := server.New(cfg, st, oidc, web.Dist(), log)
	go srv.RunMaintenance(ctx, 10*time.Minute)

	hs := newHTTPServer(cfg.ListenAddr, srv.Handler())
	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return err
	}
	errc := make(chan error, 2)
	go func() { errc <- hs.Serve(ln) }()
	log.Info("keyhole listening", "addr", cfg.ListenAddr, "public_url", cfg.PublicURL.String(), "auth_mode", cfg.AuthMode)

	servers := []*http.Server{hs}
	if cfg.Service.ListenAddr != "" {
		fs, fln, err := forwardServer(ctx, cfg.Service, st, log)
		if err != nil {
			_ = hs.Close()
			return err
		}
		go func() { errc <- fs.Serve(fln) }()
		servers = append(servers, fs)
		log.Info("forward listener for vbcdx-control", "addr", cfg.Service.ListenAddr, "control", cfg.Service.ControlURL)
	}

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var errs []error
	for _, s := range servers {
		errs = append(errs, s.Shutdown(shutdownCtx))
	}
	return errors.Join(errs...)
}

// newHTTPServer builds the server with every timeout pinned. ReadHeaderTimeout
// and ReadTimeout bound the request, WriteTimeout the response, and IdleTimeout an
// idle keep-alive connection. ReadTimeout (15s) bounds how long the server spends
// reading the whole request (headers and body) from the peer, so a client that
// dribbles a body cannot hold the connection (and its goroutine) open. WriteTimeout
// (30s) is the response deadline that the handler, and any outbound call it makes,
// runs under: it sits above the 10s upstream/OIDC client timeouts and the
// healthcheck's 3s client timeout, so a legitimate slow call is not cut off.
// ReadTimeout is independent of those outbound clocks. MaxHeaderBytes pins Go's
// 1 MiB default explicitly.
func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
}

// bootstrapAdmin creates the BOOTSTRAP_ADMIN_* account, but only into an
// empty database, so the variables are safe to leave set but do nothing
// after the first start.
func bootstrapAdmin(ctx context.Context, st *store.Store, b config.Bootstrap, log *slog.Logger) error {
	if b.Email == "" {
		return nil
	}
	n, err := st.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Info("BOOTSTRAP_ADMIN_* ignored: users already exist; you can remove these variables")
		return nil
	}
	return createBootstrapAccount(ctx, st, b, log)
}

// createBootstrapAccount creates the configured account and treats an
// already-existing account as success. The empty-database check above and
// this insert are not atomic, so under a concurrent first start another
// instance can create the account in between; the loser must converge
// rather than exit non-zero.
func createBootstrapAccount(ctx context.Context, st *store.Store, b config.Bootstrap, log *slog.Logger) error {
	u, err := createLocalUser(ctx, st, b.Email, b.Name, b.Password)
	if errors.Is(err, store.ErrExists) {
		log.Info("bootstrap account already created by another instance; continuing", "email", b.Email)
		return nil
	}
	if err != nil {
		return fmt.Errorf("bootstrap account: %w", err)
	}
	log.Info("bootstrap account created; remove BOOTSTRAP_ADMIN_PASSWORD from the environment", "user", u.ID)
	return nil
}

// seedDemoAtStart loads the demo data when SEED_DEMO_DATA is on and the
// database has no organizations yet.
func seedDemoAtStart(ctx context.Context, st *store.Store, on bool, log *slog.Logger) error {
	if !on {
		return nil
	}
	has, err := st.HasOrgs(ctx)
	if err != nil {
		return err
	}
	if has {
		log.Info("SEED_DEMO_DATA: organizations already exist; demo data not loaded")
		return nil
	}
	n, err := st.SeedDemo(ctx, time.Now())
	if err != nil {
		return fmt.Errorf("seed demo data: %w", err)
	}
	log.Info("demo data loaded", "rows", n, "org", store.DemoOrgID)
	return nil
}

// seedCmd loads the demo data. Rows that already exist are left alone, so
// a second run inserts nothing.
func seedCmd(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) > 0 {
		return usageErr("usage: keyhole seed")
	}
	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := st.SeedDemo(ctx, time.Now())
	if err != nil {
		return err
	}
	if n == 0 {
		fmt.Fprintln(stdout, "demo data already loaded; nothing changed")
		return nil
	}
	fmt.Fprintf(stdout, "loaded %d demo rows (organization %s)\n", n, store.DemoOrgID)
	return nil
}

func createLocalUser(ctx context.Context, st *store.Store, email, name, password string) (store.User, error) {
	if !validEmail(email) {
		return store.User{}, errors.New("the email address isn't valid")
	}
	email = server.NormalizeEmail(email)
	if err := auth.ValidatePassword(password); err != nil {
		return store.User{}, err
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return store.User{}, err
	}
	if name == "" {
		name, _, _ = strings.Cut(email, "@")
	}
	u, err := st.CreateLocalUser(ctx, email, name, hash)
	if errors.Is(err, store.ErrExists) {
		// Preserve the sentinel: bootstrapAdmin treats ErrExists as a
		// benign concurrent-first-start outcome, not a fatal error.
		// The email isn't echoed: the operator typed it (or set it in
		// BOOTSTRAP_ADMIN_EMAIL), so naming it adds nothing.
		return store.User{}, fmt.Errorf("a local account with that email already exists: %w", store.ErrExists)
	}
	return u, err
}

// validEmail is the shape a local account's email needs, after
// NormalizeEmail (which it applies itself): an @, and no whitespace.
func validEmail(s string) bool {
	s = server.NormalizeEmail(s)
	return strings.Contains(s, "@") && !strings.ContainsAny(s, " \t\r\n")
}

const userCreateUse = "usage: keyhole user create --email EMAIL [--name NAME] [--password-stdin]"

func userCmd(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		return usageErr(userCreateUse)
	}
	fs := flag.NewFlagSet("user create", flag.ContinueOnError)
	email := fs.String("email", "", "email address (required)")
	name := fs.String("name", "", "display name (defaults to the part before @)")
	fromStdin := fs.Bool("password-stdin", false, "read the password from standard input instead of prompting")
	if err := parseFlags(fs, args[1:], userCreateUse); err != nil {
		return err
	}
	if *email == "" {
		return usageErr("--email is required; %s", userCreateUse)
	}
	// The shape is checked before anything opens, and the typed value is
	// never echoed.
	if !validEmail(*email) {
		return usageErr("--email must be an email address; %s", userCreateUse)
	}
	password, err := readPassword(stdin, stdout, *fromStdin)
	if err != nil {
		return err
	}

	cfg, err := config.FromEnv()
	if err != nil {
		return err
	}
	st, err := openStore(ctx, cfg, nil)
	if err != nil {
		return err
	}
	defer st.Close()
	u, err := createLocalUser(ctx, st, *email, *name, password)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "created %s (%s)\n", u.Email, u.ID)
	return nil
}

// healthURL is the /healthz URL the container healthcheck probes. The listen
// address comes from the config package, the single source of truth for its
// default, so the probe cannot drift from where the server binds.
func healthURL(getenv func(string) string) string {
	return "http://" + config.ProbeAddr(config.ListenAddrFromEnv(getenv)) + "/healthz"
}

// healthcheck probes /healthz through probeClient, which connects to loopback
// only, so the probe can't leave the host whatever URL it is given (VBCDX
// architecture rule 4: the server never calls out). A server bound to an
// explicit non-loopback host can't be probed this way; bind every interface
// (":8080") or loopback.
func healthcheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL(os.Getenv), nil)
	if err != nil {
		return err
	}
	res, err := probeClient(net.DefaultResolver.LookupNetIP).Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %s", res.Status)
	}
	return nil
}

// errNotLoopback refuses a probe target outside 127.0.0.0/8 and ::1.
var errNotLoopback = errors.New("healthcheck: refusing a non-loopback address")

// probeClient is an HTTP client that connects to loopback only. It resolves
// the host with lookup first and refuses the dial unless every address is
// loopback. It uses no proxy and follows no redirect.
func probeClient(lookup func(ctx context.Context, network, host string) ([]netip.Addr, error)) *http.Client {
	var d net.Dialer
	return &http.Client{
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := lookup(ctx, "ip", host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("%w: %s has no address", errNotLoopback, host)
				}
				for _, ip := range ips {
					if !ip.IsLoopback() { // IsLoopback unmaps a 4-in-6 address itself
						return nil, fmt.Errorf("%w: %s is %s", errNotLoopback, host, ip)
					}
				}
				return d.DialContext(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
			},
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
