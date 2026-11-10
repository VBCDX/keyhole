package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrationLockID is the Postgres advisory lock that serialises migrations
// when several Keyhole instances start at once. Postgres scopes advisory locks
// to one database, not the whole server (the lock tag includes the database
// OID), so the ID distinguishes applications that share a database: it is
// Keyhole's own and must stay distinct from every other application using the
// same database (Dispatch uses 0x646973706174, "dispat").
const migrationLockID = 0x6b6579686f6c65 // "keyhole"

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var ms []migration
	for _, e := range entries {
		prefix, _, ok := strings.Cut(e.Name(), "_")
		v, err := strconv.Atoi(prefix)
		if !ok || err != nil {
			return nil, fmt.Errorf("store: migration %q must be named NNNN_name.sql", e.Name())
		}
		body, err := migrationFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			return nil, err
		}
		ms = append(ms, migration{version: v, name: e.Name(), sql: string(body)})
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].version < ms[j].version })
	return ms, nil
}

// statements splits a migration file on semicolons that end a line, after
// dropping "--" comment lines, so each driver receives one statement at a time.
func statements(src string) []string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	var out []string
	for _, stmt := range strings.Split(b.String(), ";\n") {
		if stmt = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(stmt), ";")); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

// jsonType matches the JSONB column type. Migrations are written for
// Postgres; SQLite has no JSON column type, so it stores the same JSON as TEXT.
var jsonType = regexp.MustCompile(`\bJSONB\b`)

// forDialect adapts one migration statement to the database in use.
func forDialect(d Dialect, stmt string) string {
	if d == SQLite {
		return jsonType.ReplaceAllString(stmt, "TEXT")
	}
	return stmt
}

func (s *Store) migrate(ctx context.Context) error {
	ms, err := loadMigrations()
	if err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if s.dialect == Postgres {
		if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", migrationLockID); err != nil {
			return fmt.Errorf("store: migration lock: %w", err)
		}
		defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", migrationLockID)
	}

	if _, err := conn.ExecContext(ctx,
		"CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)"); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, m := range ms {
		if applied[m.version] {
			continue
		}
		if err := s.apply(ctx, conn, m); err != nil {
			return fmt.Errorf("store: migration %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) apply(ctx context.Context, conn *sql.Conn, m migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range statements(m.sql) {
		if _, err := tx.ExecContext(ctx, forDialect(s.dialect, stmt)); err != nil {
			return fmt.Errorf("%w\n%s", err, stmt)
		}
	}
	if _, err := tx.ExecContext(ctx, s.q("INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)"),
		m.version, time.Now().UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}
