package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The child's own variables: test plumbing, not keyhole settings (named by
// constant so internal/config's .env.example scan doesn't count them).
const (
	mainArgsVar = "KEYHOLE_TEST_MAIN_ARGS"
	pathVar     = "PATH"
)

// TestMainHelper is not a test: it is the child process the umask tests run,
// so that main() itself (and its umask) is what runs.
func TestMainHelper(t *testing.T) {
	raw := os.Getenv(mainArgsVar)
	if raw == "" {
		t.Skip("only as a child of the umask tests")
	}
	var args []string
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"keyhole"}, args...)
	main()
	os.Exit(0)
}

// mainUnder runs main() with args in a child process started under umask
// 022 (a typical shell's), with env added to a clean environment.
func mainUnder(t *testing.T, env []string, stdin string, args ...string) *exec.Cmd {
	t.Helper()
	a, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelper$", "-test.count=1")
	cmd.Env = append([]string{pathVar + "=" + os.Getenv(pathVar), "HOME=" + t.TempDir(), mainArgsVar + "=" + string(a)}, env...)
	cmd.Stdin = strings.NewReader(stdin)
	// Its own working directory: a relative default (DATA_DIR=data) never
	// lands in the source tree.
	cmd.Dir = t.TempDir()
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	return cmd
}

// modesUnder asserts every file under root is 0600 and every directory 0700,
// and that the database (and any alsoExpect file) exists.
func modesUnder(t *testing.T, root string, alsoExpect ...string) {
	t.Helper()
	for _, name := range append([]string{"keyhole.db"}, alsoExpect...) {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("no %s: %v", name, err)
		}
	}
	n := 0
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if fi.Mode().Perm() != want {
			t.Errorf("%s: mode %04o, want %04o", p, fi.Mode().Perm(), want)
		}
		n++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d entries under %s", n, root)
}

// Under umask 022, every entry path that creates keyhole's data directory and
// database leaves them 0700 / 0600, the WAL and SHM included.
func TestEntryPathsCreateThePrivateModesUnderUmask022(t *testing.T) {
	for _, c := range []struct {
		name  string
		env   []string
		stdin string
		args  []string
	}{
		{"user create", nil, "correct horse battery staple\n", []string{"user", "create", "--email", "a@example.com", "--password-stdin"}},
		{"seed", nil, "", []string{"seed"}},
		{"--env-file seed", nil, "", nil}, // args set below: the env file names DATA_DIR
		{"serve", nil, "", []string{"serve"}},
		{"serve with BOOTSTRAP_ADMIN_*", []string{"BOOTSTRAP_ADMIN_EMAIL=b@example.com", "BOOTSTRAP_ADMIN_PASSWORD=correct horse battery staple"}, "", []string{"serve"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "data")
			env := append([]string{"DATA_DIR=" + root, "LISTEN_ADDR=127.0.0.1:0"}, c.env...)
			args := c.args
			if args == nil {
				dir := t.TempDir()
				p := filepath.Join(dir, "keyhole.env")
				write(t, p, "DATA_DIR="+root+"\n")
				env, args = []string{"LISTEN_ADDR=127.0.0.1:0"}, []string{"--env-file", p, "seed"}
			}
			cmd := mainUnder(t, env, c.stdin, args...)
			if args[len(args)-1] == "serve" {
				// serve runs until stopped: wait for the WAL, then stop it.
				deadline := time.Now().Add(20 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(root, "keyhole.db-wal")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						_ = cmd.Process.Kill()
						t.Fatal("serve made no database WAL in 20 s")
					}
					time.Sleep(50 * time.Millisecond)
				}
				// While it runs: the WAL and SHM exist (a clean exit removes them).
				time.Sleep(200 * time.Millisecond)
				modesUnder(t, root, "keyhole.db-wal", "keyhole.db-shm")
				_ = cmd.Process.Signal(syscall.SIGTERM)
				_ = cmd.Wait()
			} else if err := cmd.Wait(); err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			modesUnder(t, root)
		})
	}
}

// AC5 through main(): a database and DATA_DIR an older release left
// readable are tightened by any command that opens the store, with the
// warning on stderr; keyhole check only reports them.
func TestMainTightensALooseStoreAndCheckOnlyReports(t *testing.T) {
	root := filepath.Join(t.TempDir(), "data")
	// An older release's store: made, then loosened as umask 022 would have.
	if c := mainUnder(t, []string{"DATA_DIR=" + root}, "", "seed"); c.Wait() != nil {
		t.Fatal("seed failed")
	}
	if err := os.Chmod(root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "keyhole.db"), 0o644); err != nil {
		t.Fatal(err)
	}
	perm := func(p string) os.FileMode {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	run := func(args ...string) (string, string) {
		a, _ := json.Marshal(args)
		cmd := exec.Command(os.Args[0], "-test.run=^TestMainHelper$", "-test.count=1")
		cmd.Env = []string{pathVar + "=" + os.Getenv(pathVar), "HOME=" + t.TempDir(), mainArgsVar + "=" + string(a), "DATA_DIR=" + root}
		cmd.Dir = t.TempDir()
		var out, errOut strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errOut
		if err := cmd.Run(); err != nil {
			t.Fatalf("%v: %v\n%s", args, err, errOut.String())
		}
		return out.String(), errOut.String()
	}
	// check: reported, nothing changed.
	out, _ := run("check")
	if !strings.Contains(out, "warn: "+root+" was mode 0750") || !strings.Contains(out, "keyhole.db was mode 0644 (readable by other users)") ||
		!strings.Contains(out, "serve makes it 0600") {
		t.Fatalf("check output:\n%s", out)
	}
	if perm(root) != 0o750 || perm(filepath.Join(root, "keyhole.db")) != 0o644 {
		t.Fatal("check changed a mode")
	}
	// seed (any command that opens the store): fixed, warned on stderr.
	_, errOut := run("seed")
	if !strings.Contains(errOut, "level=WARN") || !strings.Contains(errOut, "password hashes") || !strings.Contains(errOut, "now 0600") {
		t.Fatalf("seed stderr:\n%s", errOut)
	}
	if perm(root) != 0o700 || perm(filepath.Join(root, "keyhole.db")) != 0o600 {
		t.Fatalf("after seed: dir %04o, db %04o", perm(root), perm(filepath.Join(root, "keyhole.db")))
	}
	// And again: nothing left to say.
	if _, errOut := run("seed"); strings.Contains(errOut, "level=WARN") {
		t.Fatalf("a second seed warned again:\n%s", errOut)
	}
}
