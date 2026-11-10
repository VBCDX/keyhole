// Package storetest gives tests a fresh database on every backend that is
// available: SQLite always, and Postgres when KEYHOLE_TEST_POSTGRES_URL is
// set. CI sets KEYHOLE_TEST_REQUIRE_POSTGRES=1 so a missing Postgres fails
// the run instead of silently testing SQLite only.
package storetest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VBCDX/keyhole/internal/store"
)

// Backend is a database URL a test can open.
type Backend struct {
	Name string
	URL  string
}

// Backends returns one fresh, empty database per available backend.
func Backends(t testing.TB) []Backend {
	t.Helper()
	bs := []Backend{{Name: "sqlite", URL: "sqlite://" + filepath.Join(t.TempDir(), "keyhole.db")}}
	pg := os.Getenv("KEYHOLE_TEST_POSTGRES_URL")
	if pg == "" {
		if os.Getenv("KEYHOLE_TEST_REQUIRE_POSTGRES") == "1" {
			t.Fatal("KEYHOLE_TEST_REQUIRE_POSTGRES=1 but KEYHOLE_TEST_POSTGRES_URL is not set")
		}
		return bs
	}
	return append(bs, Backend{Name: "postgres", URL: freshSchema(t, pg)})
}

// Run calls fn once per backend as a subtest.
func Run(t *testing.T, fn func(t *testing.T, b Backend)) {
	for _, b := range Backends(t) {
		t.Run(b.Name, func(t *testing.T) { fn(t, b) })
	}
}

// Open returns an open, migrated store for b, closed when the test ends.
func Open(t testing.TB, b Backend) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), b.URL)
	if err != nil {
		t.Fatalf("open %s: %v", b.Name, err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// freshSchema creates an empty Postgres schema and returns a URL whose
// search_path points at it, so tests never see each other's tables.
func freshSchema(t testing.TB, base string) string {
	t.Helper()
	schema := "t_" + strings.ToLower(rand.Text())
	db, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		db, err := sql.Open("pgx", base)
		if err != nil {
			return
		}
		defer db.Close()
		_, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE")
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}
