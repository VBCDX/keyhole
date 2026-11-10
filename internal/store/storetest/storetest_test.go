package storetest

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

// TestPostgresIsReal proves the Postgres leg talks to a real server, and
// prints which one, so the CI log shows the database tests did not quietly
// run on SQLite only.
func TestPostgresIsReal(t *testing.T) {
	url := os.Getenv("KEYHOLE_TEST_POSTGRES_URL")
	if url == "" {
		if os.Getenv("KEYHOLE_TEST_REQUIRE_POSTGRES") == "1" {
			t.Fatal("KEYHOLE_TEST_REQUIRE_POSTGRES=1 but KEYHOLE_TEST_POSTGRES_URL is not set")
		}
		t.Skip("KEYHOLE_TEST_POSTGRES_URL is not set")
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version string
	if err := db.QueryRow("SELECT version()").Scan(&version); err != nil {
		t.Fatalf("query Postgres: %v", err)
	}
	if !strings.HasPrefix(version, "PostgreSQL ") {
		t.Fatalf("server is not Postgres: %q", version)
	}
	t.Logf("Postgres server: %s", version)

	if names := backendNames(Backends(t)); names != "sqlite,postgres" {
		t.Fatalf("backends = %s, want sqlite,postgres", names)
	}
}

func backendNames(bs []Backend) string {
	var names []string
	for _, b := range bs {
		names = append(names, b.Name)
	}
	return strings.Join(names, ",")
}
