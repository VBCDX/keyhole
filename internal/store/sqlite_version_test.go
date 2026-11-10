package store_test

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/VBCDX/keyhole/internal/store"
)

// The SQLite that modernc.org/sqlite bundles must be 3.54.0 or later:
// earlier releases can return uninitialised page-cache memory when a
// database and a -wal of a different page size are opened together
// (CVE-2026-119375). Keyhole's memory can hold decrypted
// values, and no Go vulnerability entry maps this to the module, so this
// test is what catches a downgrade.
func TestBundledSQLiteHasThePageCacheFix(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), "sqlite://"+filepath.Join(dir, "keyhole.db"), store.WithDataDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	var v string
	if err := store.DBOf(st).QueryRow(`SELECT sqlite_version()`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	ok, err := atLeast(v, [3]int{3, 54, 0})
	if err != nil {
		t.Fatalf("sqlite_version() %q: %v", v, err)
	}
	if !ok {
		t.Fatalf("the bundled SQLite is %s; 3.54.0 or later is required (CVE-2026-119375)", v)
	}
}

// atLeast compares a dotted SQLite version with min. A version it can't
// read is an error, never a pass.
func atLeast(v string, min [3]int) (bool, error) {
	parts := strings.Split(v, ".")
	if len(parts) < 3 {
		return false, strconv.ErrSyntax
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return false, strconv.ErrSyntax
		}
		if n != min[i] {
			return n > min[i], nil
		}
	}
	return true, nil
}

func TestAtLeastReadsSQLiteVersions(t *testing.T) {
	min := [3]int{3, 54, 0}
	for _, c := range []struct {
		v      string
		ok, er bool
	}{
		{"3.54.0", true, false},
		{"3.54.1", true, false},
		{"3.55.0", true, false},
		{"4.0.0", true, false},
		{"3.53.4", false, false},
		{"3.53.99", false, false},
		{"3.46.0", false, false},
		{"2.99.99", false, false},
		{"3.54", false, true},
		{"3.x.0", false, true},
		{"", false, true},
		{"3.-1.0", false, true},
	} {
		ok, err := atLeast(c.v, min)
		if ok != c.ok || (err != nil) != c.er {
			t.Errorf("atLeast(%q) = %v, %v; want %v, error %v", c.v, ok, err, c.ok, c.er)
		}
	}
}
