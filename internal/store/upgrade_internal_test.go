package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// upgradeBackends returns empty databases to upgrade: SQLite always, and a
// fresh Postgres schema when KEYHOLE_TEST_POSTGRES_URL is set. (storetest
// can't be used here: it imports this package.)
func upgradeBackends(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{"sqlite": "sqlite://" + filepath.Join(t.TempDir(), "upgrade.db")}
	base := os.Getenv("KEYHOLE_TEST_POSTGRES_URL")
	if base == "" {
		return out
	}
	schema := "t_" + strings.ToLower(rand.Text())
	db, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if db, err := sql.Open("pgx", base); err == nil {
			_, _ = db.Exec("DROP SCHEMA " + schema + " CASCADE")
			db.Close()
		}
	})
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	out["postgres"] = u.String()
	return out
}

// A database created by the sign-in starter (migration 0001 only), with a
// local account, a Zitadel account and a session, upgrades in place: the
// accounts can still sign in and the session still resolves.
func TestUpgradeFrom0001KeepsSignIn(t *testing.T) {
	for name, dbURL := range upgradeBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			s, err := open(dbURL, openOptions{log: slog.Default()})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			ms, err := loadMigrations()
			if err != nil {
				t.Fatal(err)
			}
			conn, err := s.db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := conn.ExecContext(ctx,
				"CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			if err := s.apply(ctx, conn, ms[0]); err != nil {
				t.Fatal(err)
			}
			conn.Close()

			now := time.Now()
			for _, stmt := range []struct {
				q    string
				args []any
			}{
				{"INSERT INTO users (id, email, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
					[]any{"0192-local", "Alex@Example.com", "Alex", millis(now), millis(now)}},
				{"INSERT INTO identities (id, user_id, provider, issuer, subject, password_hash, created_at) VALUES (?, ?, 'local', 'local', ?, ?, ?)",
					[]any{"0192-idn1", "0192-local", "alex@example.com", "hash-alex", millis(now)}},
				{"INSERT INTO users (id, email, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
					[]any{"0192-oidc", "zed@example.com", "Zed", millis(now), millis(now)}},
				{"INSERT INTO identities (id, user_id, provider, issuer, subject, created_at) VALUES (?, ?, 'oidc', ?, ?, ?)",
					[]any{"0192-idn2", "0192-oidc", "https://auth.example.com", "sub-zed", millis(now)}},
				{"INSERT INTO sessions (id_hash, user_id, method, csrf_token, created_at, expires_at) VALUES (?, ?, 'zitadel', ?, ?, ?)",
					[]any{"hash-of-cookie", "0192-oidc", "csrf", millis(now), millis(now.Add(time.Hour))}},
			} {
				if _, err := s.db.ExecContext(ctx, s.q(stmt.q), stmt.args...); err != nil {
					t.Fatalf("%s: %v", stmt.q, err)
				}
			}

			if err := s.migrate(ctx); err != nil {
				t.Fatalf("upgrade: %v", err)
			}

			id, hash, err := s.LocalCredentials(ctx, "alex@example.com")
			if err != nil || id != "0192-local" || hash != "hash-alex" {
				t.Fatalf("LocalCredentials after upgrade = %q, %q, %v", id, hash, err)
			}
			if err := s.TouchLocalIdentity(ctx, "alex@example.com"); err != nil {
				t.Fatal(err)
			}
			var emailLower, status string
			if err := s.db.QueryRowContext(ctx, s.q("SELECT email_lower, status FROM users WHERE id = ?"), "0192-local").
				Scan(&emailLower, &status); err != nil || emailLower != "alex@example.com" || status != "active" {
				t.Fatalf("upgraded user = %q %q, %v", emailLower, status, err)
			}
			u, err := s.UpsertOIDCUser(ctx, "https://auth.example.com", "sub-zed", "zed@example.com", "Zed")
			if err != nil || u.ID != "0192-oidc" {
				t.Fatalf("Zitadel sign-in after upgrade linked %q, %v; want the existing user", u.ID, err)
			}
			sess, _, err := s.GetSession(ctx, "hash-of-cookie", now)
			if err != nil || sess.UserID != "0192-oidc" {
				t.Fatalf("session after upgrade = %+v, %v", sess, err)
			}
			var amr string
			if err := s.db.QueryRowContext(ctx, s.q("SELECT amr FROM sessions WHERE id_hash = ?"), "hash-of-cookie").Scan(&amr); err != nil ||
				strings.ReplaceAll(amr, " ", "") != `["sso"]` {
				t.Fatalf("upgraded Zitadel session amr = %q, %v; want [\"sso\"]", amr, err)
			}
		})
	}
}
