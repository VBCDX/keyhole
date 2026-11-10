package store

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Option configures Open.
type Option func(*openOptions)

type openOptions struct {
	dataDir string
	log     *slog.Logger
}

// WithDataDir names keyhole's own data directory (the DATA_DIR setting).
// When the SQLite database lives directly in it, Open also tightens the
// directory itself; a database elsewhere (a DATABASE_URL pointing into a
// directory keyhole doesn't own) gets its files checked, never its directory.
func WithDataDir(dir string) Option { return func(o *openOptions) { o.dataDir = dir } }

// WithLogger is where Open reports a permission it fixed.
func WithLogger(l *slog.Logger) Option { return func(o *openOptions) { o.log = l } }

// ErrForeignFile refuses a database file or directory keyhole can't vouch
// for: a symlink, not a regular file (or directory), or owned by another
// user. Running against another user's database would also leave it with
// -wal/-shm files that user can't open.
var ErrForeignFile = errors.New("store: refusing the SQLite database")

// A permission problem found on the SQLite files: what it was, and whether it
// was fixed.
type PermFinding struct {
	Path    string
	OldMode os.FileMode
	NewMode os.FileMode
	Since   time.Time // the file's mtime: how long it may have been exposed
	Fixed   bool
	IsDir   bool
}

// What the database holds, for the warning: a reader of an exposed copy
// may have these. The key values stay sealed under the KEK (which is never
// fixed this way: a loose KEK still refuses, in envelope.LoadKEK).
const exposedContents = "it holds local accounts' password hashes and emails and the grant and tool metadata " +
	"(key values stay sealed under the KEK); consider resetting local passwords"

// Message is the one-line warning for a finding.
func (f PermFinding) Message() string {
	what := exposure(f.OldMode)
	if f.IsDir {
		return fmt.Sprintf("%s was mode %04o (%s) since %s; %s", f.Path, f.OldMode, what, f.Since.UTC().Format(time.RFC3339), f.after())
	}
	return fmt.Sprintf("%s was mode %04o (%s) since %s: %s; %s", f.Path, f.OldMode, what,
		f.Since.UTC().Format(time.RFC3339), exposedContents, f.after())
}

// exposure says who could do what: "readable by other users", "readable and
// writable by its group", … (the widest class with any bit set).
func exposure(m os.FileMode) string {
	who, bits := "other users", m&0o007
	if bits == 0 {
		who, bits = "its group", (m>>3)&0o007
	}
	switch {
	case bits&0o4 != 0 && bits&0o2 != 0:
		return "readable and writable by " + who
	case bits&0o2 != 0:
		return "writable by " + who
	case bits&0o4 != 0:
		return "readable by " + who
	default:
		return "accessible to " + who
	}
}

func (f PermFinding) after() string {
	if f.Fixed {
		return fmt.Sprintf("now %04o", f.NewMode)
	}
	return fmt.Sprintf("serve makes it %04o", f.NewMode)
}

// SQLitePermissions checks the SQLite database at path (and its -wal and
// -shm, when present) and, when it is the data directory's own, the
// directory: each must be keyhole's (a regular file or directory, owned by
// this user, not a symlink). Anything else is ErrForeignFile. With fix,
// group or other permission bits are taken off (0600 files, 0700 directory)
// on the opened file, never by path, so a file swapped in between can't
// be the one changed. Without fix it only reports (keyhole check).
func SQLitePermissions(path, dataDir string, fix bool) ([]PermFinding, error) {
	var out []PermFinding
	if dataDir != "" && sameDir(filepath.Dir(path), dataDir) {
		f, err := tighten(filepath.Dir(path), true, fix)
		if err != nil {
			return nil, err
		}
		if f != nil {
			out = append(out, *f)
		}
	}
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		f, err := tighten(p, false, fix)
		if err != nil {
			return nil, err
		}
		if f != nil {
			out = append(out, *f)
		}
	}
	return out, nil
}

func sameDir(a, b string) bool {
	aa, err1 := filepath.Abs(a)
	bb, err2 := filepath.Abs(b)
	return err1 == nil && err2 == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

// tighten opens p without following a symlink, checks what it opened, and
// with fix takes group and other bits off it. A p that doesn't exist yet is
// fine (keyhole creates it under umask 077).
func tighten(p string, isDir, fix bool) (*PermFinding, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if isDir {
		flags |= syscall.O_DIRECTORY
	}
	f, err := os.OpenFile(p, flags, 0)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case errors.Is(err, syscall.ELOOP):
		return nil, fmt.Errorf("%w: %s is a symlink", ErrForeignFile, p)
	case errors.Is(err, syscall.ENOTDIR):
		// O_DIRECTORY|O_NOFOLLOW on a symlink fails ENOTDIR on Linux. The
		// open already refused; lstat only picks the message.
		if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%w: %s is a symlink", ErrForeignFile, p)
		}
		return nil, fmt.Errorf("%w: %s is not a directory", ErrForeignFile, p)
	case errors.Is(err, os.ErrPermission):
		return nil, permissionRefusal(p, err)
	case errors.Is(err, syscall.ENXIO):
		// A unix socket (or a FIFO/device with no other end) can't be opened.
		return nil, fmt.Errorf("%w: %s is not a regular file (a socket or device)", ErrForeignFile, p)
	case err != nil:
		return nil, fmt.Errorf("store: %s: %w", p, err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", p, err)
	}
	if isDir != fi.IsDir() || (!isDir && !fi.Mode().IsRegular()) {
		return nil, fmt.Errorf("%w: %s is not a regular %s", ErrForeignFile, p, map[bool]string{true: "directory", false: "file"}[isDir])
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("%w: %s is not owned by this user (uid %d); run keyhole as the user that owns it", ErrForeignFile, p, os.Geteuid())
	}
	perm := fi.Mode().Perm()
	if perm&0o077 == 0 {
		return nil, nil
	}
	// Group and other bits come off; the owner's stay as they are (0440 →
	// 0400, never widened to 0600). A file whose owner has no access at all
	// (0040, say) becomes 0600 when keyhole can open it at all (as root):
	// keyhole has to read and write its own database. Any other user gets
	// EACCES above and a refusal that says to chmod u+rw.
	want := perm &^ 0o077
	if isDir {
		want = 0o700
	} else if want == 0 {
		want = 0o600
	}
	finding := &PermFinding{Path: p, OldMode: perm, NewMode: want, Since: fi.ModTime(), IsDir: isDir}
	if fix {
		if err := f.Chmod(want); err != nil {
			return nil, fmt.Errorf("store: tightening %s: %w", p, err)
		}
		finding.Fixed = true
	}
	return finding, nil
}

// SQLitePath is the file a sqlite:// DATABASE_URL names, or ok false for any
// other database.
func SQLitePath(rawURL string) (path string, ok bool) {
	const scheme = "sqlite://"
	if len(rawURL) < len(scheme) || rawURL[:len(scheme)] != scheme {
		return "", false
	}
	path = rawURL[len(scheme):]
	for i := 0; i < len(path); i++ {
		if path[i] == '?' {
			path = path[:i]
			break
		}
	}
	return path, path != ""
}

// permissionRefusal is the refusal for an open that failed EACCES. That is
// either this user's own file with no owner read (0040, say: fixable by its
// owner) or something else: another user's file, or a parent directory this
// user can't search. lstat only picks the message; the open already refused.
func permissionRefusal(p string, err error) error {
	if fi, lerr := os.Lstat(p); lerr == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o400 == 0 {
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) == os.Geteuid() {
			return fmt.Errorf("%w: %s isn't readable by its owner (mode %04o): give it owner read and write (chmod u+rw)",
				ErrForeignFile, p, fi.Mode().Perm())
		}
	}
	return fmt.Errorf("%w: %s can't be opened by this user (uid %d: another user's file, or a directory above it this user can't search): %v",
		ErrForeignFile, p, os.Geteuid(), err)
}
