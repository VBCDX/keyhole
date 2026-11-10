package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
)

// listedCommands is every command line the top-level usage lists, as the
// words to type before --help: the command, and its first sub-command word
// when it has one ("grants disable|enable" -> grants disable).
func listedCommands(t *testing.T) [][]string {
	t.Helper()
	body := usage[strings.Index(usage, "commands:\n")+len("commands:\n") : strings.Index(usage, "exit codes:")]
	var out [][]string
	for _, l := range strings.Split(body, "\n") {
		if !strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "   ") {
			continue
		}
		f := strings.Fields(l)
		if f[0] == "help," || f[0] == "version," {
			continue
		}
		words := []string{f[0]}
		if len(f) > 1 && regexp.MustCompile(`^[a-z][a-z-]*(\|[a-z]+)?$`).MatchString(f[1]) {
			words = append(words, strings.Split(f[1], "|")[0])
		}
		out = append(out, words)
	}
	if len(out) < 12 {
		t.Fatalf("found only %d commands in the usage: %v", len(out), out)
	}
	return out
}

func noServe(t *testing.T) func(context.Context) error {
	return func(context.Context) error {
		t.Error("the server started")
		return nil
	}
}

// AC1 + AC3: <command> [<sub>] --help and -h, for every
// command the top-level usage lists: no error, that command's usage on
// stdout with --flag forms, nothing from Go's flag package.
func TestEveryCommandHasItsOwnHelp(t *testing.T) {
	oneDash := regexp.MustCompile(`(^|[\s\[(|])-[a-z]`)
	for _, words := range listedCommands(t) {
		for _, flag := range []string{"--help", "-h", "-help"} {
			name := strings.Join(append(append([]string{}, words...), flag), " ")
			t.Run(name, func(t *testing.T) {
				var out bytes.Buffer
				args := append(append([]string{}, words...), flag)
				if err := run(context.Background(), args, strings.NewReader(""), &out, noServe(t)); err != nil {
					t.Fatalf("err %v", err)
				}
				s := out.String()
				if !strings.HasPrefix(s, "usage: keyhole "+words[0]) {
					t.Fatalf("printed %q", s)
				}
				for _, bad := range []string{"help requested", "Usage of", "exit codes:"} {
					if strings.Contains(s, bad) {
						t.Errorf("%q in %q", bad, s)
					}
				}
				if m := oneDash.FindString(s); m != "" {
					t.Errorf("a one-dash flag %q in %q", m, s)
				}
				if strings.Contains(strings.SplitN(s, "\n", 2)[0], " -") && !strings.Contains(s, "--") {
					t.Errorf("no --flag form in %q", s)
				}
			})
		}
	}
	// The placeholders come from the usage, not from backquoted flag text.
	for _, c := range []struct{ args []string }{{[]string{"keys", "put", "--help"}}, {[]string{"service", "csr", "--help"}}} {
		var out bytes.Buffer
		if err := run(context.Background(), c.args, strings.NewReader(""), &out, noServe(t)); err != nil {
			t.Fatal(err)
		}
		if s := out.String(); strings.Contains(s, "keyhole kek generate") || strings.Contains(s, "vbcdx-control services add keyhole --csr") {
			t.Errorf("%v: a placeholder from backquoted usage text: %q", c.args, s)
		}
	}
}

// AC2: healthcheck --help prints help and dials nothing. The control:
// without --help the same setup does dial, so the listener would see it.
func TestHealthcheckHelpDialsNothing(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	t.Setenv("LISTEN_ADDR", ln.Addr().String())
	accepted := make(chan struct{}, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted <- struct{}{}
			c.Close()
		}
	}()
	for _, flag := range []string{"--help", "-h"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{"healthcheck", flag}, strings.NewReader(""), &out, noServe(t)); err != nil {
			t.Fatalf("healthcheck %s: %v", flag, err)
		}
		if !strings.HasPrefix(out.String(), "usage: keyhole healthcheck") {
			t.Fatalf("printed %q", out.String())
		}
	}
	select {
	case <-accepted:
		t.Fatal("healthcheck --help dialed /healthz")
	case <-time.After(200 * time.Millisecond):
	}
	// The control: a real healthcheck reaches the listener.
	_ = run(context.Background(), []string{"healthcheck"}, strings.NewReader(""), io.Discard, noServe(t))
	select {
	case <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("the control healthcheck never dialed: this test can't see a dial")
	}
}

// AC4: a wrong command line exits 2, a failed command 1, success 0.
func TestExitCodes(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"bogus"}, exitUsage},
		{[]string{"--bogus"}, exitUsage},
		{[]string{"--env-file"}, exitUsage},
		{[]string{"check", "extra"}, exitUsage},
		{[]string{"seed", "extra"}, exitUsage},
		{[]string{"user"}, exitUsage},
		{[]string{"user", "create"}, exitUsage},
		{[]string{"user", "create", "--nope"}, exitUsage},
		{[]string{"load"}, exitUsage},
		{[]string{"load", "--file"}, exitUsage},
		{[]string{"kek"}, exitUsage},
		{[]string{"kek", "generate"}, exitUsage},
		{[]string{"keys", "put"}, exitUsage},
		{[]string{"grants", "disable"}, exitUsage},
		{[]string{"members", "add", "--org"}, exitUsage},
		{[]string{"tools", "private-allow"}, exitUsage},
		{[]string{"service", "csr"}, exitUsage},
		{[]string{"service", "csr", "--key-out", "x", "--csr-out", "y", "extra"}, exitUsage},
		// A command that ran and failed is 1: the file doesn't exist.
		{[]string{"--env-file", "/nonexistent/keyhole.env", "version"}, exitFailure},
		{[]string{"help"}, 0},
	} {
		err := run(ctx, c.args, strings.NewReader(""), io.Discard, noServe(t))
		if got := exitCode(err); got != c.want {
			t.Errorf("keyhole %v: exit %d, want %d (err %v)", c.args, got, c.want, err)
		}
	}
	// The flag is named as --flag, as the usage does.
	err := run(ctx, []string{"user", "create", "--nope"}, strings.NewReader(""), io.Discard, noServe(t))
	if err == nil || !strings.Contains(err.Error(), "unknown flag --nope") || strings.Contains(err.Error(), " -nope") {
		t.Errorf("err %v", err)
	}
	// A wrapped cause still matches.
	if u := usageErr("--cidr: %w", store.ErrBadPrivateAllow); !errors.Is(u, store.ErrBadPrivateAllow) || exitCode(u) != exitUsage {
		t.Errorf("usageErr lost its cause: %v", u)
	}
	if exitCode(errors.New("x")) != exitFailure || exitCode(fmt.Errorf("w: %w", usageErr("u"))) != exitUsage {
		t.Error("exitCode doesn't see through wrapping")
	}
}

// The real main() exits with those codes (not just run's errors).
func TestMainExitsTwoForAUsageError(t *testing.T) {
	for _, c := range []struct {
		args []string
		want int
	}{
		{[]string{"bogus"}, exitUsage},
		{[]string{"user", "create", "--nope"}, exitUsage},
		{[]string{"--env-file", "/nonexistent/keyhole.env", "version"}, exitFailure},
		{[]string{"healthcheck", "--help"}, 0},
	} {
		cmd := mainUnder(t, nil, "", c.args...)
		err := cmd.Wait()
		got := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			got = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("keyhole %v: exit %d, want %d", c.args, got, c.want)
		}
	}
}

// The top-level usage lists exactly the codes main can exit with.
func TestUsageListsTheExitCodes(t *testing.T) {
	i := strings.Index(usage, "exit codes:\n")
	if i < 0 || !strings.HasSuffix(usage, exitCodesHelp) {
		t.Fatalf("the usage doesn't end with the exit codes:\n%s", usage)
	}
	var listed []string
	for _, l := range strings.Split(exitCodesHelp, "\n")[1:] {
		if f := strings.Fields(l); len(f) > 0 && regexp.MustCompile(`^\d+$`).MatchString(f[0]) {
			listed = append(listed, f[0])
		}
	}
	want := []string{"0", fmt.Sprint(exitFailure), fmt.Sprint(exitUsage)}
	if strings.Join(listed, ",") != strings.Join(want, ",") {
		t.Errorf("listed %v, want %v", listed, want)
	}
}

// parseFlags never lets Go's flag package print (its default usage writes
// -flag with one dash, to stderr), whatever output the FlagSet had.
func TestParseFlagsPrintsNothing(t *testing.T) {
	for _, args := range [][]string{{"--nope"}, {"--help"}, {"-h"}, {"--n"}} {
		var buf bytes.Buffer
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.SetOutput(&buf)
		fs.String("n", "", "a value from `keyhole other`")
		err := parseFlags(fs, args, "usage: keyhole x --n N")
		if exitCode(err) != exitUsage {
			t.Errorf("%v: err %v, want a usage error", args, err)
		}
		if buf.Len() != 0 {
			t.Errorf("%v: Go's flag package printed %q", args, buf.String())
		}
	}
}

// The shipped entry path, not just run(): the real main() in a child, as
// `keyhole serve --help` (the command that served on a test CT before this
// fix). It must print the usage and exit 0 at once, and leave an empty
// DATA_DIR empty: nothing opened, nothing bound, nothing written. The
// control runs plain `serve` with the same settings and sees it write there,
// so an empty directory means it didn't serve, not that the probe is blind.
func TestRealMainServeHelpServesNothing(t *testing.T) {
	child := func(ctx context.Context, dataDir string, args ...string) (*exec.Cmd, *bytes.Buffer) {
		a, _ := json.Marshal(args)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainHelper$", "-test.count=1")
		cmd.Env = []string{pathVar + "=" + os.Getenv(pathVar), "HOME=" + t.TempDir(), mainArgsVar + "=" + string(a),
			"DATA_DIR=" + dataDir, "LISTEN_ADDR=127.0.0.1:0"}
		cmd.Dir = t.TempDir()
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		return cmd, &out
	}
	for _, flag := range []string{"--help", "-h"} {
		dataDir := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd, out := child(ctx, dataDir, "serve", flag)
		err := cmd.Run()
		timedOut := ctx.Err() != nil
		cancel()
		if timedOut {
			t.Fatalf("serve %s still ran after 10 s: it served\n%s", flag, out)
		}
		if err != nil {
			t.Fatalf("serve %s: %v\n%s", flag, err, out)
		}
		if !strings.Contains(out.String(), "usage: keyhole serve") {
			t.Errorf("serve %s printed %q", flag, out)
		}
		if entries, err := os.ReadDir(dataDir); err != nil || len(entries) != 0 {
			t.Errorf("serve %s wrote into DATA_DIR: %v %v", flag, entries, err)
		}
	}
	// The control: plain serve, the same settings, writes its database.
	dataDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd, out := child(ctx, dataDir, "serve")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if entries, _ := os.ReadDir(dataDir); len(entries) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the control serve wrote nothing in 20 s, so this test can't see a serve\n%s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// r1 (security): a flag VALUE, or anything typed that isn't name-shaped, is
// never echoed: a value can be a secret. Each row passes a canary and
// checks it's absent from the error and from stdout, the flag is named
// --flag, and the exit is 2. serve with an argument refuses and serves
// nothing.
func TestErrorsNeverEchoAValue(t *testing.T) {
	const canary = "CANARYsk7f3q9"
	const hexCanary = "deadbeefcafe0123"
	for _, c := range []struct {
		args []string
		want string // in the error; "" = no flag name expected
	}{
		{[]string{"user", "create", "--password-stdin=" + canary}, "a bad value for --password-stdin"},
		{[]string{"tools", "private-allow", "--none=" + canary}, "a bad value for --none"},
		{[]string{"user", "create", "--" + canary + "=x"}, "an unknown flag"},
		{[]string{"user", "create", "--sk_" + canary}, "an unknown flag"},
		// r3 (Ivan): a lowercase-hex token is name-shaped, so it needs the
		// all-hex rule: as a flag, as a flag with a value, as a command.
		{[]string{"user", "create", "--" + hexCanary}, "an unknown flag"},
		{[]string{"user", "create", "--" + hexCanary + "=x"}, "an unknown flag"},
		{[]string{hexCanary}, "unknown command (want"},
		{[]string{"user", "create", "--email"}, "--email needs a value"},
		{[]string{canary}, "unknown command (want"},
		{[]string{"--" + canary}, "unknown command (want"},
		{[]string{"check", canary}, "check takes no arguments"},
		{[]string{"serve", canary}, "serve takes no arguments"},
		{[]string{"serve", "--nope"}, "serve takes no arguments"},
	} {
		var out bytes.Buffer
		err := run(context.Background(), c.args, strings.NewReader(""), &out, noServe(t))
		if exitCode(err) != exitUsage {
			t.Errorf("%v: exit %d (err %v), want %d", c.args, exitCode(err), err, exitUsage)
			continue
		}
		msg := err.Error()
		if strings.Contains(msg, canary) || strings.Contains(out.String(), canary) ||
			strings.Contains(msg, hexCanary) || strings.Contains(out.String(), hexCanary) {
			t.Errorf("%v: the canary was echoed: %q / %q", c.args, msg, out.String())
		}
		if !strings.Contains(msg, c.want) {
			t.Errorf("%v: %q doesn't say %q", c.args, msg, c.want)
		}
		if regexp.MustCompile(`(^|[\s(])-[a-z]`).MatchString(msg) {
			t.Errorf("%v: a one-dash flag in %q", c.args, msg)
		}
	}
	// Every form of Go's error text, including typed (non-bool) flags that
	// keyhole doesn't have today: only the kind and the name come through.
	for _, args := range [][]string{
		{"--n=" + canary}, {"--d=" + canary}, {"--b=" + canary}, {"--" + canary},
		{"--n"}, {"---" + canary}, {"-=" + canary},
	} {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.Int("n", 0, "a number")
		fs.Duration("d", 0, "a duration")
		fs.Bool("b", false, "a switch")
		err := parseFlags(fs, args, "usage: keyhole x")
		if err == nil || exitCode(err) != exitUsage {
			t.Errorf("%v: err %v", args, err)
			continue
		}
		if strings.Contains(err.Error(), canary) || strings.Contains(err.Error(), "invalid") {
			t.Errorf("%v: Go's text came through: %q", args, err)
		}
	}
}

// The same through the real main(): nothing on stdout or stderr carries the
// canary.
func TestRealMainNeverEchoesAValue(t *testing.T) {
	const canary = "CANARYsk7f3q9"
	for _, args := range [][]string{
		{"user", "create", "--password-stdin=" + canary},
		{"tools", "private-allow", "--none=" + canary},
		{canary},
	} {
		a, _ := json.Marshal(args)
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelper$", "-test.count=1")
		cmd.Env = []string{pathVar + "=" + os.Getenv(pathVar), "HOME=" + t.TempDir(), mainArgsVar + "=" + string(a),
			"DATA_DIR=" + t.TempDir(), "LISTEN_ADDR=127.0.0.1:0"}
		cmd.Dir = t.TempDir()
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() != exitUsage {
			t.Errorf("%v: %v, want exit %d\n%s", args, err, exitUsage, out.String())
		}
		if strings.Contains(out.String(), canary) {
			t.Errorf("%v: the canary was printed: %q", args, out.String())
		}
	}
}

// shownName: command-shaped words come through; anything that could be a
// token doesn't (the same table as vbcdx-core's shown_name).
func TestShownNameEchoesOnlyCommandShapedWords(t *testing.T) {
	for _, ok := range []string{"bogus", "nope", "email", "password-stdin", "x1", "--org", "a-b-c"} {
		if _, shown := shownName(ok); !shown {
			t.Errorf("%q not shown", ok)
		}
	}
	for _, no := range []string{"", "Bogus", "x_y", "1abc", "a b", "/etc/passwd", "deadbeef",
		"0123456789abcdef", "abc", "ffffffffffffffffffffffffffffffff",
		"abcdefghijklmnopqrstuvwxyzabcdefg", "sk_live_x", "é"} {
		if _, shown := shownName(no); shown {
			t.Errorf("%q shown", no)
		}
	}
}

// AC1: a malformed --email is a usage error (2) found before
// anything opens, and the typed value is never echoed.
func TestUserCreateRefusesAMalformedEmailBeforeOpeningAnything(t *testing.T) {
	const canary = "CANARYnoat9"
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	t.Setenv("DATABASE_URL", "")
	for _, bad := range []string{canary, canary + " x@y", "a b@c"} {
		err := run(context.Background(), []string{"user", "create", "--email", bad, "--password-stdin"},
			strings.NewReader("a long enough password\n"), io.Discard, noServe(t))
		if exitCode(err) != exitUsage || !strings.Contains(err.Error(), "--email must be an email address") {
			t.Fatalf("--email %q: exit %d, %v", bad, exitCode(err), err)
		}
		if low := strings.ToLower(err.Error()); strings.Contains(low, strings.ToLower(canary)) || strings.Contains(low, "a b@c") {
			t.Errorf("the typed email was echoed: %v", err)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("something was opened in DATA_DIR: %v %v", entries, err)
	}
}

// AC2: help needs no settings, so --env-file isn't read for it:
// a missing file doesn't stop `<command> --help`, `--help` or `help`. The
// control: a real command still reads the file and fails on a missing one.
func TestHelpDoesNotReadTheEnvFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.env")
	for _, args := range [][]string{
		{"--env-file", missing, "user", "create", "--help"},
		{"--env-file=" + missing, "healthcheck", "-h"},
		{"--env-file", missing, "--help"},
		{"--env-file", missing, "help"},
	} {
		var out bytes.Buffer
		if err := run(context.Background(), args, strings.NewReader(""), &out, noServe(t)); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !strings.HasPrefix(out.String(), "usage: keyhole") {
			t.Fatalf("%v printed %q", args, out.String())
		}
	}
	if err := run(context.Background(), []string{"--env-file", missing, "version"}, strings.NewReader(""), io.Discard, noServe(t)); err == nil {
		t.Fatal("a real command didn't read the missing env file")
	}
}

// failingVar is a flag whose own Set error carries text shaped like Go's
// " for -<flag>: ", as a third-party flag.Value might.
type failingVar struct{}

func (failingVar) String() string   { return "" }
func (failingVar) Set(string) error { return errors.New("rejected for -org: try again") }

// AC3: the flag named in "a bad value for --x" is the one Go
// reports, read after its quoted value: neither the typed value nor a
// flag's own error text can choose another one.
func TestABadValueNamesTheRealFlag(t *testing.T) {
	newFS := func() *flag.FlagSet {
		fs := flag.NewFlagSet("x", flag.ContinueOnError)
		fs.Bool("none", false, "")
		fs.String("org", "", "")
		fs.Var(failingVar{}, "cidr", "")
		fs.Int("n", 0, "")
		return fs
	}
	for _, c := range []struct{ arg, want string }{
		{"--none=x for -org: y", "a bad value for --none"},
		{"--none=x for flag -org: y", "a bad value for --none"},
		{"--cidr=v", "a bad value for --cidr"},
		{"--n=1 for -org: 2", "a bad value for --n"},
		{"--org", "--org needs a value"},
	} {
		err := parseFlags(newFS(), []string{c.arg}, "usage: keyhole x")
		if err == nil || !strings.HasPrefix(err.Error(), c.want+";") {
			t.Errorf("%s: %v, want %q", c.arg, err, c.want)
		}
	}
	if got := nameAfterQuoted(`"a \" for -org: b" for -none: parse error`); got != "none" {
		t.Errorf("an escaped quote in the value: %q", got)
	}
	if got := nameAfterQuoted(`no quote for -org: x`); got != "" {
		t.Errorf("not Go's shape: %q", got)
	}
}

// A bad-value error names a flag only when fs
// defines it. Go's own errors always name a defined flag, so this feeds
// flagProblem a foreign error directly: without the fs.Lookup guard it
// would name "ghost".
func TestABadValueNamesOnlyADefinedFlag(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	fs.Bool("none", false, "")
	for _, c := range []struct{ msg, want string }{
		{`invalid value "x" for flag -ghost: bad`, "a bad flag value"},
		{`invalid boolean value "x" for -ghost: bad`, "a bad flag value"},
		{`invalid boolean value "x" for -none: bad`, "a bad value for --none"},
	} {
		if got := flagProblem(errors.New(c.msg), fs); got != c.want {
			t.Errorf("%s: %q, want %q", c.msg, got, c.want)
		}
	}
}

// AC4: the leading-dash check on an unknown command isn't dead:
// shownName strips dashes, so without it `--bogus` would be named "bogus".
func TestAnUnknownDashCommandIsNotNamed(t *testing.T) {
	err := run(context.Background(), []string{"--bogus"}, strings.NewReader(""), io.Discard, noServe(t))
	if exitCode(err) != exitUsage || strings.Contains(err.Error(), `"bogus"`) || !strings.Contains(err.Error(), "unknown command (want") {
		t.Fatalf("--bogus: %v", err)
	}
	// A command-shaped typo is still named.
	err = run(context.Background(), []string{"bogus"}, strings.NewReader(""), io.Discard, noServe(t))
	if !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Fatalf("bogus: %v", err)
	}
}

// The same rule where bootstrap creates the first account (its email comes
// from the environment): refused before the store is touched, not echoed.
func TestCreateLocalUserDoesNotEchoAMalformedEmail(t *testing.T) {
	const canary = "CANARYnoat9"
	_, err := createLocalUser(context.Background(), nil, canary, "", "a long enough password")
	// Case-blind: the email is lowercased before it could be echoed.
	if err == nil || strings.Contains(strings.ToLower(err.Error()), strings.ToLower(canary)) {
		t.Fatalf("err %v", err)
	}
}
