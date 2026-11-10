package store_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
)

func mode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

// looseStore makes what an older release left under umask 022: DATA_DIR
// 0750 and a keyhole.db (with -wal and -shm) at 0644, last written then.
func looseStore(t *testing.T, then time.Time) (dir, db string) {
	t.Helper()
	dir = filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o750)
	db = filepath.Join(dir, "keyhole.db")
	for _, p := range []string{db, db + "-wal", db + "-shm"} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, 0o644)
		if err := os.Chtimes(p, then, then); err != nil {
			t.Fatal(err)
		}
	}
	os.Remove(db) // a real, empty database: let SQLite make it, then loosen it
	st, err := store.Open(context.Background(), "sqlite://"+db)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := os.Chmod(db, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(db, then, then)
	return dir, db
}

// AC5: a database and DATA_DIR from an older release are tightened on open,
// each with one honest warning; a second open finds nothing to say.
func TestOpenTightensALooseDatabaseAndWarnsOnce(t *testing.T) {
	then := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	dir, db := looseStore(t, then)
	for _, p := range []string{db + "-wal", db + "-shm"} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, 0o644)
		os.Chtimes(p, then, then)
	}
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	st, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir), store.WithLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if got := mode(t, dir); got != 0o700 {
		t.Errorf("DATA_DIR mode %04o, want 0700", got)
	}
	if got := mode(t, db); got != 0o600 {
		t.Errorf("keyhole.db mode %04o, want 0600", got)
	}
	for _, p := range []string{db + "-wal", db + "-shm"} {
		if fi, err := os.Lstat(p); err == nil && fi.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s mode %04o, still group/other", p, fi.Mode().Perm())
		}
	}
	text := logs.String()
	for _, want := range []string{
		db + " was mode 0644 (readable by other users) since 2026-10-01T12:00:00Z",
		"local accounts' password hashes and emails",
		"key values stay sealed under the KEK",
		"consider resetting local passwords",
		"now 0600",
		dir + " was mode 0750 (readable by its group)",
		db + "-wal was mode 0644 (readable by other users)",
		"now 0700",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the warnings lack %q:\n%s", want, text)
		}
	}
	// One per fixed path: DATA_DIR, keyhole.db, -wal, -shm.
	if n := strings.Count(text, "level=WARN"); n != 4 {
		t.Errorf("%d warnings, want exactly one per fixed path (4):\n%s", n, text)
	}
	// Once fixed, nothing to say.
	logs.Reset()
	st, err = store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir), store.WithLogger(log))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if logs.Len() != 0 {
		t.Errorf("a second open warned again:\n%s", logs.String())
	}
}

// A database outside keyhole's own DATA_DIR (DATABASE_URL elsewhere): its
// file is fixed, its directory, which keyhole doesn't own, is never touched.
func TestOpenNeverTouchesADirectoryThatIsNotTheDataDir(t *testing.T) {
	dir, db := looseStore(t, time.Now())
	os.Chmod(dir, 0o755)
	st, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(filepath.Join(t.TempDir(), "elsewhere")),
		store.WithLogger(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if got := mode(t, dir); got != 0o755 {
		t.Errorf("the database's directory became %04o; it isn't keyhole's to change", got)
	}
	if got := mode(t, db); got != 0o600 {
		t.Errorf("keyhole.db mode %04o, want 0600", got)
	}
}

// What keyhole can't vouch for refuses to open, and nothing is changed.
func TestOpenRefusesASymlinkedOrForeignDatabase(t *testing.T) {
	t.Run("a symlinked keyhole.db", func(t *testing.T) {
		dir, db := looseStore(t, time.Now())
		real := filepath.Join(dir, "real.db")
		if err := os.Rename(db, real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, db); err != nil {
			t.Fatal(err)
		}
		_, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir))
		if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("err %v", err)
		}
		if got := mode(t, real); got != 0o644 {
			t.Errorf("the link's target became %04o", got)
		}
	})
	t.Run("a symlinked DATA_DIR", func(t *testing.T) {
		_, db := looseStore(t, time.Now())
		link := filepath.Join(t.TempDir(), "data-link")
		if err := os.Symlink(filepath.Dir(db), link); err != nil {
			t.Fatal(err)
		}
		_, err := store.Open(context.Background(), "sqlite://"+filepath.Join(link, "keyhole.db"), store.WithDataDir(link))
		if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "is a symlink") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("a -wal that is a directory", func(t *testing.T) {
		dir, db := looseStore(t, time.Now())
		if err := os.Mkdir(db+"-wal", 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir))
		if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("err %v", err)
		}
	})
	t.Run("owned by another user", func(t *testing.T) {
		if os.Geteuid() != 0 {
			t.Skip("needs root to chown")
		}
		dir, db := looseStore(t, time.Now())
		if err := os.Chown(db, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		_, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir))
		if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "is not owned by this user") {
			t.Fatalf("err %v", err)
		}
		if got := mode(t, db); got != 0o644 {
			t.Errorf("another user's file was changed to %04o", got)
		}
	})
}

// Report-only (keyhole check): the findings, nothing changed.
func TestSQLitePermissionsReportsWithoutFixing(t *testing.T) {
	dir, db := looseStore(t, time.Now())
	findings, err := store.SQLitePermissions(db, dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 {
		t.Fatalf("findings %+v, want the dir and keyhole.db", findings)
	}
	for _, f := range findings {
		if f.Fixed || !strings.HasSuffix(f.Message(), "serve makes it "+map[bool]string{true: "0700", false: "0600"}[f.IsDir]) {
			t.Errorf("finding %+v: %s", f, f.Message())
		}
	}
	if mode(t, db) != 0o644 || mode(t, dir) != 0o750 {
		t.Error("report-only changed a mode")
	}
}

func TestSQLitePath(t *testing.T) {
	for in, want := range map[string]string{"sqlite://data/keyhole.db": "data/keyhole.db", "sqlite:///var/lib/k.db?x=1": "/var/lib/k.db"} {
		if got, ok := store.SQLitePath(in); !ok || got != want {
			t.Errorf("SQLitePath(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"postgres://u@h/db", "postgresql://h/db", "sqlite://", "file:x.db"} {
		if _, ok := store.SQLitePath(in); ok {
			t.Errorf("SQLitePath(%q) ok", in)
		}
	}
}

// r2 B1: a FIFO or a socket where a database file should be is refused, and
// opening it never hangs (O_NONBLOCK: a FIFO with no writer would block an
// open for read forever).
func TestOpenRefusesAFIFOOrSocketWithoutHanging(t *testing.T) {
	for _, kind := range []string{"fifo", "socket"} {
		for _, which := range []string{"", "-wal"} {
			t.Run(kind+" at keyhole.db"+which, func(t *testing.T) {
				// A short directory: a unix socket's path must fit in 108
				// bytes, and t.TempDir's names don't.
				base, err := os.MkdirTemp("/tmp", "kh")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(base) })
				dir := filepath.Join(base, "d")
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				db := filepath.Join(dir, "keyhole.db")
				p := db + which
				switch kind {
				case "fifo":
					if err := syscall.Mkfifo(p, 0o600); err != nil {
						t.Fatal(err)
					}
				case "socket":
					l, err := net.Listen("unix", p)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { l.Close() })
				}
				done := make(chan error, 1)
				go func() {
					_, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir))
					done <- err
				}()
				select {
				case err := <-done:
					if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "not a regular file") {
						t.Fatalf("err %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("Open hung on a " + kind + " (O_NONBLOCK missing?)")
				}
			})
		}
	}
}

// r2 B2: only group and other bits come off; the owner's stay as they are.
// A file the owner can't read at all becomes 0600 (keyhole must use it).
func TestTightenOnlyNarrows(t *testing.T) {
	for _, c := range []struct {
		from, want os.FileMode
		finding    bool
	}{
		{0o400, 0o400, false}, // already private: untouched
		{0o600, 0o600, false},
		{0o440, 0o400, true}, // group read off; never widened to 0600
		{0o640, 0o600, true},
		{0o644, 0o600, true},
		{0o604, 0o600, true},
	} {
		t.Run(fmt.Sprintf("%04o", c.from), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			db := filepath.Join(dir, "keyhole.db")
			if err := os.WriteFile(db, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(db, c.from); err != nil {
				t.Fatal(err)
			}
			findings, err := store.SQLitePermissions(db, dir, true)
			if err != nil {
				t.Fatal(err)
			}
			if got := mode(t, db); got != c.want {
				t.Errorf("%04o became %04o, want %04o", c.from, got, c.want)
			}
			if (len(findings) == 1) != c.finding {
				t.Errorf("findings %+v", findings)
			}
		})
	}
}

// r2 B4: another user's DATA_DIR, -wal or -shm is refused like another
// user's keyhole.db, and left as it was.
func TestOpenRefusesAForeignDataDirWalOrShm(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	for _, which := range []string{"DATA_DIR", "-wal", "-shm"} {
		t.Run(which, func(t *testing.T) {
			dir, db := looseStore(t, time.Now())
			target := dir
			if which != "DATA_DIR" {
				target = db + which
				if err := os.WriteFile(target, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				os.Chmod(target, 0o644)
			}
			if err := os.Chown(target, 65534, 65534); err != nil {
				t.Fatal(err)
			}
			before := mode(t, target)
			_, err := store.Open(context.Background(), "sqlite://"+db, store.WithDataDir(dir))
			if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "is not owned by this user") {
				t.Fatalf("err %v", err)
			}
			if got := mode(t, target); got != before {
				t.Errorf("another user's %s changed %04o → %04o", which, before, got)
			}
		})
	}
}

// The warning says what the exposure was: a group- or world-writable file is
// "writable", not just "readable" (r2 NIT).
func TestWarningNamesWriteAccess(t *testing.T) {
	for from, want := range map[os.FileMode]string{
		0o660: "readable and writable by its group",
		0o620: "writable by its group",
		0o602: "writable by other users",
		0o644: "readable by other users",
	} {
		t.Run(fmt.Sprintf("%04o", from), func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "data")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			db := filepath.Join(dir, "keyhole.db")
			if err := os.WriteFile(db, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			os.Chmod(db, from)
			findings, err := store.SQLitePermissions(db, dir, false)
			if err != nil || len(findings) != 1 || !strings.Contains(findings[0].Message(), fmt.Sprintf("was mode %04o (%s)", from, want)) {
				t.Errorf("%v %+v", err, findings)
			}
		})
	}
}

// A DATA_DIR with no owner bits (0500: the owner can't write it) becomes
// 0700, as documented: keyhole has to create its database there.
func TestDataDirWithoutOwnerWriteBecomes0700(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o550); err != nil {
		t.Fatal(err)
	}
	findings, err := store.SQLitePermissions(filepath.Join(dir, "keyhole.db"), dir, true)
	if err != nil || len(findings) != 1 || !findings[0].IsDir {
		t.Fatalf("%v %+v", err, findings)
	}
	if got := mode(t, dir); got != 0o700 {
		t.Errorf("0550 became %04o, want 0700", got)
	}
}

// A DATA_DIR that doesn't exist yet is made 0700, even under umask 022, and
// that is no finding: nothing was ever exposed.
func TestOpenMakesANewDataDir0700WithoutAWarning(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "new-data")
	old := syscall.Umask(0o022)
	defer syscall.Umask(old)
	var logs bytes.Buffer
	st, err := store.Open(context.Background(), "sqlite://"+filepath.Join(dir, "keyhole.db"), store.WithDataDir(dir),
		store.WithLogger(slog.New(slog.NewTextHandler(&logs, nil))))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	if got := mode(t, dir); got != 0o700 {
		t.Errorf("a new DATA_DIR is %04o, want 0700", got)
	}
	if logs.Len() != 0 {
		t.Errorf("a new DATA_DIR warned:\n%s", logs.String())
	}
}

// item 7: a file whose owner has no bits (0040). Root can open
// it, so it is fixed to 0600; any other user can't open it at all, so it is
// refused by name with what to do, never with a bare "permission denied".
func TestNoOwnerBitsFixedAsRootRefusedOtherwise(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "keyhole.db")
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(db, 0o040); err != nil {
		t.Fatal(err)
	}
	findings, err := store.SQLitePermissions(db, dir, true)
	if os.Geteuid() == 0 {
		if err != nil {
			t.Fatal(err)
		}
		if got := mode(t, db); got != 0o600 {
			t.Errorf("as root, 0040 became %04o, want 0600", got)
		}
		// Fix-and-warn: exactly one finding, the fixed file (the assert the
		// TestTightenOnlyNarrows row carried before it moved here).
		if len(findings) != 1 || findings[0].Path != db || !findings[0].Fixed || findings[0].OldMode != 0o040 {
			t.Errorf("as root, findings %+v, want one fixed finding for %s from 0040", findings, db)
		}
		return
	}
	if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "chmod u+rw") {
		t.Fatalf("err %v", err)
	}
	if got := mode(t, db); got != 0o040 {
		t.Errorf("a refused file changed to %04o", got)
	}
}

// The EACCES refusal's two messages, driven directly so
// root CI runs them: this user's own file with no owner read gets "chmod
// u+rw"; anything else (here, a readable file whose EACCES came from a
// directory above it) gets the general message, never the chmod advice.
func TestPermissionRefusalMessages(t *testing.T) {
	dir := t.TempDir()
	noRead := filepath.Join(dir, "no-read.db")
	if err := os.WriteFile(noRead, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(noRead, 0o040); err != nil {
		t.Fatal(err)
	}
	err := store.PermissionRefusal(noRead, os.ErrPermission)
	if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "isn't readable by its owner (mode 0040)") || !strings.Contains(err.Error(), "chmod u+rw") {
		t.Errorf("own 0040 file: %v", err)
	}
	readable := filepath.Join(dir, "readable.db")
	if err := os.WriteFile(readable, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err = store.PermissionRefusal(readable, os.ErrPermission)
	if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "can't be opened by this user") || strings.Contains(err.Error(), "chmod u+rw") {
		t.Errorf("a readable file (EACCES from a parent): %v", err)
	}
	if os.Geteuid() == 0 {
		// Another user's 0040 file: not "ours", so the general message.
		if err := os.Chown(noRead, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		if err := store.PermissionRefusal(noRead, os.ErrPermission); strings.Contains(err.Error(), "chmod u+rw") {
			t.Errorf("another user's file got the owner advice: %v", err)
		}
	}
}

// The child's own variables: test plumbing, not keyhole settings (named by
// constant so internal/config's .env.example scan doesn't count them).
const (
	asNobodyVar = "KEYHOLE_TEST_AS_NOBODY"
	testDirVar  = "KEYHOLE_TEST_DIR"
)

// The EACCES branch of the real open path, which root never reaches: a child
// test process drops to uid/gid 65534 and opens its own 0040 file. CI runs as
// root, so this runs there; as non-root it skips (the test above covers the
// message).
func TestNoOwnerBitsRefusedForTheRealNonRootPath(t *testing.T) {
	if os.Getenv(asNobodyVar) != "" {
		if err := syscall.Setgid(65534); err != nil {
			t.Fatal(err)
		}
		if err := syscall.Setuid(65534); err != nil {
			t.Fatal(err)
		}
		dir := os.Getenv(testDirVar)
		_, err := store.SQLitePermissions(filepath.Join(dir, "keyhole.db"), dir, true)
		if !errors.Is(err, store.ErrForeignFile) || !strings.Contains(err.Error(), "isn't readable by its owner (mode 0040)") {
			t.Fatalf("as nobody: err %v", err)
		}
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root to drop to another uid")
	}
	base, err := os.MkdirTemp("/tmp", "kh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	dir := filepath.Join(base, "d")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(dir, "keyhole.db")
	if err := os.WriteFile(db, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The child is nobody: its own dir and its own 0040 file; /tmp/khX searchable.
	for _, p := range []string{dir, db} {
		if err := os.Chown(p, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	os.Chmod(base, 0o755)
	os.Chmod(db, 0o040)
	cmd := exec.Command(os.Args[0], "-test.run=^TestNoOwnerBitsRefusedForTheRealNonRootPath$", "-test.count=1")
	cmd.Env = append(os.Environ(), asNobodyVar+"=1", testDirVar+"="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child as nobody failed: %v\n%s", err, out)
	}
	if got := mode(t, db); got != 0o040 {
		t.Errorf("the refused file changed to %04o", got)
	}
}
