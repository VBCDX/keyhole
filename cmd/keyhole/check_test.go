package main

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/service/servicetest"
)

// linkEnv writes a complete control-plane-mode env file (as K-b's installer would)
// and returns its path and the files it names.
func linkEnv(t *testing.T) (envPath string, files map[string]string) {
	t.Helper()
	dir := t.TempDir()
	ca := servicetest.NewCA(t)
	cert, key := servicetest.Enrolled(t, ca)
	caFile := filepath.Join(dir, "control-ca.pem")
	if err := os.WriteFile(caFile, ca.PEM, 0o644); err != nil {
		t.Fatal(err)
	}
	kek := filepath.Join(dir, "kek")
	if err := envelope.GenerateKEK(kek); err != nil {
		t.Fatal(err)
	}
	files = map[string]string{"cert": cert, "key": key, "ca": caFile, "kek": kek, "data": filepath.Join(dir, "data")}
	envPath = writeEnv(t, dir, strings.Join([]string{
		"# Written by vbcdx install service keyhole-server",
		"LISTEN_ADDR=127.0.0.1:18080",
		"PUBLIC_URL=http://127.0.0.1:18080",
		"DATA_DIR=" + files["data"],
		"SERVICE_LISTEN_ADDR=127.0.0.1:18443",
		"SERVICE_CERT_FILE=" + cert,
		"SERVICE_KEY_FILE=" + key,
		"CONTROL_CA_FILE=" + caFile,
		"CONTROL_URL=https://127.0.0.1:8443",
		"KEK_FILE=" + kek,
		"",
	}, "\n"))
	return envPath, files
}

func writeEnv(t *testing.T, dir, text string) string {
	t.Helper()
	p := filepath.Join(dir, "keyhole.env")
	if err := os.WriteFile(p, []byte(text), 0o640); err != nil {
		t.Fatal(err)
	}
	return p
}

// clearEnv makes sure the settings come from the file only, and that what
// run sets is undone after the test.
func clearEnv(t *testing.T) {
	for _, k := range []string{"LISTEN_ADDR", "PUBLIC_URL", "DATA_DIR", "DATABASE_URL", "AUTH_MODE", "SERVICE_LISTEN_ADDR",
		"SERVICE_CERT_FILE", "SERVICE_KEY_FILE", "CONTROL_CA_FILE", "CONTROL_URL", "CONTROL_SERVER_NAME", "KEK_FILE"} {
		t.Setenv(k, "")
	}
}

func TestCheckWithEnvFilePassesAndOpensNothing(t *testing.T) {
	clearEnv(t)
	env, files := linkEnv(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--env-file", env, "check"}, nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ok: settings (listen 127.0.0.1:18080", "ok: control-plane link (forward listener 127.0.0.1:18443, control https://127.0.0.1:8443"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q lacks %q", out.String(), want)
		}
	}
	// No database, no data directory: check opens nothing.
	if _, err := os.Stat(files["data"]); !os.IsNotExist(err) {
		t.Fatalf("check created %s (err %v)", files["data"], err)
	}
	// The --env-file=F spelling is the same.
	if err := run(context.Background(), []string{"--env-file=" + env, "check"}, nil, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
}

// Each link file check serve makes, check makes too: break one, check fails
// naming it.
func TestCheckRefusesEachBrokenLinkFile(t *testing.T) {
	cases := []struct {
		name, want string
		breakIt    func(t *testing.T, f map[string]string)
	}{
		{"key mode 0644", "it must be 0600", func(t *testing.T, f map[string]string) { chmod(t, f["key"], 0o644) }},
		{"key missing", "key file", func(t *testing.T, f map[string]string) { rm(t, f["key"]) }},
		{"cert missing", "identity", func(t *testing.T, f map[string]string) { rm(t, f["cert"]) }},
		{"cert from another key", "identity", func(t *testing.T, f map[string]string) {
			other, _ := servicetest.Enrolled(t, servicetest.NewCA(t))
			cp(t, other, f["cert"])
		}},
		{"cert names another service", "does not name", func(t *testing.T, f map[string]string) {
			ca := servicetest.NewCA(t)
			c, k := servicetest.Enrolled(t, ca, "vbcdx:service/dispatch")
			cp(t, c, f["cert"])
			cp(t, k, f["key"])
			chmod(t, f["key"], 0o600)
		}},
		{"CA not PEM", "no PEM certificate", func(t *testing.T, f map[string]string) { write(t, f["ca"], "not a cert") }},
		{"CA missing", "control CA", func(t *testing.T, f map[string]string) { rm(t, f["ca"]) }},
		{"KEK mode 0640", "it must be 0600", func(t *testing.T, f map[string]string) { chmod(t, f["kek"], 0o640) }},
		{"KEK missing", "KEK file", func(t *testing.T, f map[string]string) { rm(t, f["kek"]) }},
		{"key owned by another user", "is not owned by this user", func(t *testing.T, f map[string]string) { chownNobody(t, f["key"]) }},
		{"KEK owned by another user", "is not owned by this user", func(t *testing.T, f map[string]string) { chownNobody(t, f["kek"]) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if strings.Contains(c.name, "owned by another user") && os.Geteuid() != 0 {
				t.Skip("needs root to chown")
			}
			clearEnv(t)
			env, files := linkEnv(t)
			c.breakIt(t, files)
			err := run(context.Background(), []string{"--env-file", env, "check"}, nil, &bytes.Buffer{}, nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want %q", err, c.want)
			}
		})
	}
}

func TestCheckRefusesBadSettings(t *testing.T) {
	for name, edit := range map[string]func(string) string{
		"non-loopback forward listener": func(s string) string {
			return strings.Replace(s, "SERVICE_LISTEN_ADDR=127.0.0.1:18443", "SERVICE_LISTEN_ADDR=0.0.0.0:18443", 1)
		},
		"a link file unset": func(s string) string { return strings.Replace(s, "KEK_FILE=", "#KEK_FILE=", 1) },
		"bad control URL":   func(s string) string { return strings.Replace(s, "CONTROL_URL=https://", "CONTROL_URL=http://", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			env, _ := linkEnv(t)
			raw, _ := os.ReadFile(env)
			edited := edit(string(raw))
			if edited == string(raw) {
				t.Fatal("the edit changed nothing")
			}
			write(t, env, edited)
			if err := run(context.Background(), []string{"--env-file", env, "check"}, nil, &bytes.Buffer{}, nil); err == nil {
				t.Fatal("check passed")
			}
		})
	}
}

// check prints no URL's userinfo: a credential put there never reaches
// its output.
func TestCheckPrintsNoURLCredentials(t *testing.T) {
	clearEnv(t)
	env, _ := linkEnv(t)
	raw, _ := os.ReadFile(env)
	edited := strings.NewReplacer(
		"PUBLIC_URL=http://127.0.0.1:18080", "PUBLIC_URL=https://u:CANARY-pub-7f3a@keyhole.example.com",
		"CONTROL_URL=https://127.0.0.1:8443", "CONTROL_URL=https://u:CANARY-ctl-7f3a@127.0.0.1:8443",
	).Replace(string(raw))
	if strings.Count(edited, "CANARY") != 2 {
		t.Fatal("the edit changed nothing")
	}
	write(t, env, edited)
	var out bytes.Buffer
	err := run(context.Background(), []string{"--env-file", env, "check"}, nil, &out, nil)
	if strings.Contains(out.String(), "CANARY") || (err != nil && strings.Contains(err.Error(), "CANARY")) {
		t.Fatalf("a credential was printed: %q (err %v)", out.String(), err)
	}
	if !strings.Contains(out.String(), "public https://keyhole.example.com,") {
		t.Fatalf("output %q", out.String())
	}
}

func TestCheckWithoutLinkSaysSo(t *testing.T) {
	clearEnv(t)
	var out bytes.Buffer
	if err := run(context.Background(), []string{"check"}, nil, &out, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no control-plane link") {
		t.Fatalf("output %q", out.String())
	}
	if err := run(context.Background(), []string{"check", "extra"}, nil, &out, nil); err == nil {
		t.Fatal("check took an argument")
	}
}

// The file's values win over the process environment, as EnvironmentFile=
// does for the unit, and an admin command sees them.
func TestEnvFileOverridesTheEnvironmentForAnyCommand(t *testing.T) {
	clearEnv(t)
	dir := t.TempDir()
	data := filepath.Join(dir, "from-file")
	t.Setenv("DATA_DIR", filepath.Join(dir, "from-env"))
	env := writeEnv(t, dir, "DATA_DIR="+data+"\n")
	if err := run(context.Background(), []string{"--env-file", env, "seed"}, nil, &bytes.Buffer{}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(data, "keyhole.db")); err != nil {
		t.Fatalf("seed did not use the file's DATA_DIR: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "from-env")); !os.IsNotExist(err) {
		t.Fatalf("seed used the environment's DATA_DIR (err %v)", err)
	}
}

// The value-echo class: an --env-file error
// names the flag and a line number, never the path given or a key from the
// file. Both canaries are shaped like tokens; each case is run through run(),
// so what an operator sees is what is checked.
func TestEnvFileErrorsNeverEchoThePathOrAKey(t *testing.T) {
	const pathCanary, keyCanary = "ghp_PathCanary0123456789", "ghp_KeyCanary9876543210"
	dir := filepath.Join(t.TempDir(), pathCanary)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := func(name, text string, mode os.FileMode) string {
		p := filepath.Join(dir, name+"-"+pathCanary)
		write(t, p, text)
		chmod(t, p, mode)
		return p
	}
	fifo := filepath.Join(dir, "fifo-"+pathCanary)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link-"+pathCanary)
	if err := os.Symlink(file("target", "A=1\n", 0o600), link); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct {
		path, says string
	}{
		"missing":         {filepath.Join(dir, "missing-"+pathCanary), "can't be opened"},
		"a symlink":       {link, "can't be opened"},
		"a FIFO":          {fifo, "not a regular file"},
		"a directory":     {dir, ""},
		"writable":        {file("writable", "A=1\n", 0o666), "must not be able to write"},
		"too large":       {file("large", "A="+strings.Repeat("x", envFileMax)+"\n", 0o600), "larger than"},
		"a control char":  {file("ctl", keyCanary+"=1\x00\n", 0o600), "line 1 has a control character"},
		"not KEY=VALUE":   {file("nokv", keyCanary+"\n", 0o600), "line 1 is not KEY=VALUE"},
		"quotes":          {file("quotes", keyCanary+"=\"x\"\n", 0o600), "line 1: quotes"},
		"a key set twice": {file("dup", "A=1\n"+keyCanary+"=1\n"+keyCanary+"=2\n", 0o600), "line 3 sets the key line 2 set"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			err := run(context.Background(), []string{"--env-file", c.path, "check"}, nil, &bytes.Buffer{}, nil)
			if err == nil {
				t.Fatal("accepted")
			}
			msg := err.Error()
			if !strings.Contains(msg, "--env-file") || !strings.Contains(msg, c.says) {
				t.Errorf("%q doesn't say --env-file … %q", msg, c.says)
			}
			if strings.Contains(strings.ToLower(msg), "canary") {
				t.Errorf("echoed the path or a key: %q", msg)
			}
		})
	}
}

func TestEnvFileRefusals(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"double quotes":  "DATA_DIR=\"/x\"\n",
		"single quotes":  "DATA_DIR='/x'\n",
		"backslash":      "DATA_DIR=/x\\\n",
		"no equals":      "DATA_DIR\n",
		"bad key":        "DATA-DIR=/x\n",
		"export keyword": "export DATA_DIR=/x\n",
		"repeated key":   "DATA_DIR=/x\nDATA_DIR=/y\n",
		// systemd ends a line at a bare \r too: here it would be one value.
		"bare CR":            "A=1\rB=2\n",
		"bare CR at the end": "A=1\r",
		"vertical tab":       "A=1\v\n",
		"form feed":          "A=\f1\n",
		"NUL":                "A=1\x00\n",
		"DEL":                "A=1\x7f\n",
		"escape":             "A=\x1b[0m\n",
		"UTF-8 BOM":          "\ufeffDATA_DIR=/x\n",
		// AC3: more systemd-parity rows.
		"CR CR LF":              "A=1\r\r\n",
		"NBSP in a key":         "DATA\u00a0DIR=/x\n",
		"NBSP leading a line":   "\u00a0DATA_DIR=/x\n",
		"U+2000 leading a line": "\u2000DATA_DIR=/x\n",
		"U+3000 in a key":       "DATA\u3000DIR=/x\n",
		"one line too long":     "A=" + strings.Repeat("x", envFileMax) + "\n",
		// Cut at the cap + 1 byte, this one still parses (a 17-byte line, then
		// 16-byte lines): only the size check can refuse it.
		"too large": "A=" + strings.Repeat("x", 14) + "\n" + manyLines((envFileMax+1-17)/16+8),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
			write(t, p, text)
			if _, err := readEnvFile(p); err == nil {
				t.Fatal("accepted")
			}
		})
	}
	t.Run("symlink", func(t *testing.T) {
		target := filepath.Join(dir, "target")
		write(t, target, "DATA_DIR=/x\n")
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readEnvFile(link); err == nil {
			t.Fatal("followed a symlink")
		}
	})
	t.Run("group- or world-writable", func(t *testing.T) {
		for _, m := range []os.FileMode{0o620, 0o602, 0o666} {
			p := filepath.Join(dir, "writable")
			write(t, p, "DATA_DIR=/x\n")
			chmod(t, p, m)
			if _, err := readEnvFile(p); err == nil || !strings.Contains(err.Error(), "must not be able to write") {
				t.Errorf("mode %04o: err %v", m, err)
			}
			rm(t, p)
		}
		// Readable by the group (the installer writes 0640 root:vbcdx-keyhole) is fine.
		p := filepath.Join(dir, "group-readable")
		write(t, p, "DATA_DIR=/x\n")
		chmod(t, p, 0o640)
		if _, err := readEnvFile(p); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("owned by another user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("needs root to chown")
		}
		p := filepath.Join(dir, "nobody")
		write(t, p, "DATA_DIR=/x\n")
		if err := os.Chown(p, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if _, err := readEnvFile(p); err == nil || !strings.Contains(err.Error(), "must be owned by root or this user") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("just under the size cap is read", func(t *testing.T) {
		p := filepath.Join(dir, "big-ok")
		text := manyLines(envFileMax/16 - 1)
		if len(text) > envFileMax {
			t.Fatalf("fixture is %d bytes", len(text))
		}
		write(t, p, text)
		if _, err := readEnvFile(p); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("a FIFO", func(t *testing.T) {
		p := filepath.Join(dir, "fifo")
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readEnvFile(p); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("a directory", func(t *testing.T) {
		if _, err := readEnvFile(dir); err == nil {
			t.Fatal("read a directory")
		}
	})
	t.Run("missing value for the flag", func(t *testing.T) {
		for _, args := range [][]string{{"--env-file"}, {"--env-file", ""}, {"--env-file="}} {
			if err := run(context.Background(), args, nil, &bytes.Buffer{}, nil); err == nil || !strings.Contains(err.Error(), "needs a file") {
				t.Errorf("%q: err %v", args, err)
			}
		}
	})
	t.Run("CRLF lines, and bytes systemd keeps (no-break space, U+0085) stay in the value", func(t *testing.T) {
		p := filepath.Join(dir, "crlf")
		write(t, p, "A=1\r\nB=x\u00a0\r\nC=\u0085y\n\tD\t=\t4\t\n")
		env, err := readEnvFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"A": "1", "B": "x\u00a0", "C": "\u0085y", "D": "4"}
		if len(env) != len(want) {
			t.Fatalf("env %q", env)
		}
		for k, v := range want {
			if env[k] != v {
				t.Errorf("%s = %q, want %q", k, env[k], v)
			}
		}
	})
	t.Run("a # or ; in the middle of a line is part of the value, as in systemd", func(t *testing.T) {
		p := filepath.Join(dir, "midline")
		write(t, p, "A=x#y\nB=x ;y\nC=#z\n")
		env, err := readEnvFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if env["A"] != "x#y" || env["B"] != "x ;y" || env["C"] != "#z" {
			t.Fatalf("env %q", env)
		}
	})
	t.Run("comments, blanks and spaces are read as systemd reads them", func(t *testing.T) {
		p := filepath.Join(dir, "ok")
		write(t, p, "# c\n; c\n\n  A = 1 \nB=two words\nC=\n")
		env, err := readEnvFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if env["A"] != "1" || env["B"] != "two words" || env["C"] != "" || len(env) != 3 {
			t.Fatalf("env %q", env)
		}
	})
}

// manyLines is n distinct 16-byte KEY=VALUE lines.
func manyLines(n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "K%06d=xxxxxxx\n", i)
	}
	return b.String()
}

func chownNobody(t *testing.T, p string) {
	t.Helper()
	if err := os.Chown(p, 65534, 65534); err != nil {
		t.Fatal(err)
	}
}

func chmod(t *testing.T, p string, m os.FileMode) {
	t.Helper()
	if err := os.Chmod(p, m); err != nil {
		t.Fatal(err)
	}
}

func rm(t *testing.T, p string) {
	t.Helper()
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func cp(t *testing.T, from, to string) {
	t.Helper()
	b, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	write(t, to, string(b))
}

// r2 B3: keyhole check fails on a symlinked or another user's keyhole.db, as
// serve would, and changes nothing.
func TestCheckFailsOnASymlinkedOrForeignDatabase(t *testing.T) {
	setup := func(t *testing.T) (dir, db string) {
		clearEnv(t)
		dir = filepath.Join(t.TempDir(), "data")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		db = filepath.Join(dir, "keyhole.db")
		t.Setenv("DATA_DIR", dir)
		return dir, db
	}
	t.Run("symlinked", func(t *testing.T) {
		dir, db := setup(t)
		real := filepath.Join(dir, "real.db")
		write(t, real, "")
		chmod(t, real, 0o644)
		if err := os.Symlink(real, db); err != nil {
			t.Fatal(err)
		}
		err := run(context.Background(), []string{"check"}, nil, &bytes.Buffer{}, nil)
		if err == nil || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("err %v", err)
		}
		if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o644 {
			t.Errorf("the link's target changed to %04o", fi.Mode().Perm())
		}
	})
	t.Run("another user's", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("needs root to chown")
		}
		_, db := setup(t)
		write(t, db, "")
		chmod(t, db, 0o644)
		chownNobody(t, db)
		err := run(context.Background(), []string{"check"}, nil, &bytes.Buffer{}, nil)
		if err == nil || !strings.Contains(err.Error(), "is not owned by this user") {
			t.Fatalf("err %v", err)
		}
		if fi, _ := os.Stat(db); fi.Mode().Perm() != 0o644 {
			t.Errorf("another user's file changed to %04o", fi.Mode().Perm())
		}
	})
}

// r2 B5: every command opens the store through openStore (so the permission
// check gets DATA_DIR and a logger): no bare store.Open is left in the
// command's own code.
func TestEveryCommandOpensTheStoreThroughOpenStore(t *testing.T) {
	// Parsed, not grepped: a comment or a string can't add or hide a call.
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	calls := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		// The store package's name in this file, an alias included
		// (NIT: matching the identifier "store" missed an alias).
		storeName := ""
		for _, imp := range file.Imports {
			if strings.Trim(imp.Path.Value, `"`) == "github.com/VBCDX/keyhole/internal/store" {
				storeName = "store"
				if imp.Name != nil {
					storeName = imp.Name.Name
				}
			}
		}
		// Every top-level declaration, a package-level var initializer
		// included (NIT: function bodies only missed those).
		for _, decl := range file.Decls {
			owner := ""
			if fn, ok := decl.(*ast.FuncDecl); ok {
				owner = fn.Name.Name
			}
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					if fun.Name == "openStore" {
						calls++
					}
				case *ast.SelectorExpr:
					if x, ok := fun.X.(*ast.Ident); ok && storeName != "" && x.Name == storeName && fun.Sel.Name == "Open" && owner != "openStore" {
						t.Errorf("%s: %q opens the store without openStore", fset.Position(call.Pos()), owner)
					}
				}
				return true
			})
		}
	}
	// The 8 commands that open it: serve, seedCmd, userCmd, keysCmd,
	// grantsCmd, membersCmd, loadCmd, toolsCmd.
	if calls != 8 {
		t.Errorf("%d openStore calls, want 8 (a command added or dropped? update this count)", calls)
	}
}
