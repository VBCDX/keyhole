package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// rawDB opens a second handle on a test backend, to read tables the store
// has no query for.
func rawDB(t *testing.T, b storetest.Backend) *sql.DB {
	t.Helper()
	driver, dsn := "pgx", b.URL
	if path, ok := strings.CutPrefix(b.URL, "sqlite://"); ok {
		driver, dsn = "sqlite", "file:"+path+"?"+url.Values{"_pragma": {"busy_timeout(5000)"}}.Encode()
	}
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// snapshot renders every row of every table the fixture touches, so two
// snapshots are equal only if nothing was inserted, updated or deleted.
func snapshot(t *testing.T, db *sql.DB, f store.Fixture) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, tb := range f.Tables {
		rows, err := db.Query("SELECT * FROM " + tb.Table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		var lines []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			var b strings.Builder
			for i, v := range vals {
				if bs, ok := v.([]byte); ok {
					v = string(bs)
				}
				fmt.Fprintf(&b, "%s=%v;", cols[i], v)
			}
			lines = append(lines, b.String())
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
		sort.Strings(lines)
		out[tb.Table] = lines
	}
	return out
}

func TestSeedDemoLoadsOnceAndIsIdempotent(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		f, err := store.DemoFixture()
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		for _, tb := range f.Tables {
			want += len(tb.Rows)
		}

		if has, err := st.HasOrgs(ctx); err != nil || has {
			t.Fatalf("HasOrgs on an empty database = %v, %v", has, err)
		}
		n, err := st.SeedDemo(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if n != int64(want) {
			t.Fatalf("first seed inserted %d rows, want every fixture row (%d)", n, want)
		}
		if has, _ := st.HasOrgs(ctx); !has {
			t.Fatal("HasOrgs after seeding = false")
		}

		db := rawDB(t, b)
		before := snapshot(t, db, f)
		for _, tb := range f.Tables {
			if got := len(before[tb.Table]); got < len(tb.Rows) {
				t.Errorf("%s has %d rows after seeding, want at least %d", tb.Table, got, len(tb.Rows))
			}
		}

		// A later run, with a different clock, must change nothing at all.
		n, err = st.SeedDemo(ctx, time.Now().Add(48*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("second seed inserted %d rows, want 0", n)
		}
		after := snapshot(t, db, f)
		for table, rows := range before {
			if strings.Join(rows, "\n") != strings.Join(after[table], "\n") {
				t.Errorf("%s changed on the second seed", table)
			}
		}
	})
}

func TestSeedDemoTimesAreRelativeToLoad(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
		if _, err := st.SeedDemo(ctx, now); err != nil {
			t.Fatal(err)
		}
		var created, expire int64
		if err := rawDB(t, b).QueryRow("SELECT created_at FROM orgs WHERE id = 'org_acme'").Scan(&created); err != nil {
			t.Fatal(err)
		}
		if got, want := time.UnixMilli(created).UTC(), now.Add(-60*24*time.Hour); !got.Equal(want) {
			t.Fatalf("org_acme created_at = %v, want %v (@now-60d)", got, want)
		}
		// "@now+365d-2m" on ev_1.
		if err := rawDB(t, b).QueryRow("SELECT expire_at FROM audit_events WHERE id = 'ev_1'").Scan(&expire); err != nil {
			t.Fatal(err)
		}
		if got, want := time.UnixMilli(expire).UTC(), now.Add(365*24*time.Hour-2*time.Minute); !got.Equal(want) {
			t.Fatalf("ev_1 expire_at = %v, want %v", got, want)
		}
	})
}

// The demo data must hold nothing that works as a credential: no password
// hashes or identities (nobody can sign in as a demo person), no Local-store
// values, and every digest, token or secret reference an obvious
// placeholder.
func TestDemoFixtureHoldsNoSecrets(t *testing.T) {
	f, err := store.DemoFixture()
	if err != nil {
		t.Fatal(err)
	}
	for _, tb := range f.Tables {
		switch tb.Table {
		case "identities", "local_key_values", "mfa_factors", "issuer_keys":
			t.Errorf("fixture writes %s; demo data must not carry credentials or key material", tb.Table)
		}
		for i, row := range tb.Rows {
			for col, v := range row {
				switch {
				case col == "password_hash" || col == "csrf_token" && !placeholder(v):
					t.Errorf("%s[%d].%s holds a credential", tb.Table, i, col)
				case strings.HasSuffix(col, "_hash") || col == "id_hash" || col == "auth_role_id":
					if !placeholder(v) {
						t.Errorf("%s[%d].%s = %v, want a demo-placeholder value", tb.Table, i, col, v)
					}
				}
			}
			checkSecretRefs(t, fmt.Sprintf("%s[%d]", tb.Table, i), row)
		}
	}
}

func placeholder(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "demo-placeholder:")
}

// checkSecretRefs walks a row's JSON values: every secretRef must point at a
// demo-placeholder location, and no "value", "token", "secret" or
// "password" string may sit in it.
func checkSecretRefs(t *testing.T, where string, v any) {
	t.Helper()
	switch v := v.(type) {
	case map[string]any:
		if v["kind"] == "sealed" {
			if uri, _ := v["uri"].(string); !strings.Contains(uri, "demo-placeholder") {
				t.Errorf("%s: secretRef uri %q is not a placeholder", where, uri)
			}
		}
		for k, x := range v {
			lk := strings.ToLower(k)
			if s, ok := x.(string); ok && (lk == "value" || lk == "token" || lk == "secret" || lk == "password" || lk == "clientkey") {
				t.Errorf("%s.%s holds a plain string %q", where, k, s)
			}
			checkSecretRefs(t, where+"."+k, x)
		}
	case []any:
		for i, x := range v {
			checkSecretRefs(t, fmt.Sprintf("%s[%d]", where, i), x)
		}
	}
}

func TestLoadFixtureRejectsBadInput(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		for name, f := range map[string]store.Fixture{
			"bad table":   {Tables: []store.FixtureTable{{Table: "orgs; DROP TABLE users", Rows: []map[string]any{{"id": "x"}}}}},
			"bad column":  {Tables: []store.FixtureTable{{Table: "orgs", Rows: []map[string]any{{"id) --": "x"}}}}},
			"bad time":    {Tables: []store.FixtureTable{{Table: "orgs", Rows: []map[string]any{{"id": "org_x", "created_at": "@now-3w"}}}}},
			"missing col": {Tables: []store.FixtureTable{{Table: "orgs", Rows: []map[string]any{{"id": "org_x"}}}}},
		} {
			if _, err := st.LoadFixture(ctx, f, time.Now()); err == nil {
				t.Errorf("%s: accepted", name)
			}
		}
		if has, _ := st.HasOrgs(ctx); has {
			t.Fatal("a rejected fixture left rows behind")
		}
	})
}

func TestJoinDemoOrg(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		u, err := st.CreateLocalUser(ctx, "new@example.com", "New", "hash")
		if err != nil {
			t.Fatal(err)
		}
		if joined, err := st.JoinDemoOrg(ctx, u.ID); err != nil || joined {
			t.Fatalf("without demo data: joined = %v, %v; want false", joined, err)
		}
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		if joined, err := st.JoinDemoOrg(ctx, u.ID); err != nil || !joined {
			t.Fatalf("first join = %v, %v; want true", joined, err)
		}
		if joined, err := st.JoinDemoOrg(ctx, u.ID); err != nil || joined {
			t.Fatalf("second join = %v, %v; want false", joined, err)
		}
		var role, status string
		if err := rawDB(t, b).QueryRow(
			"SELECT role, status FROM org_memberships WHERE org_id = 'org_acme' AND user_id = '"+u.ID+"'").
			Scan(&role, &status); err != nil || role != "Owner" || status != "active" {
			t.Fatalf("membership = %q %q, %v; want Owner active", role, status, err)
		}
		// Someone who already belongs to an organization is left alone.
		if joined, err := st.JoinDemoOrg(ctx, "u_noor"); err != nil || joined {
			t.Fatalf("u_noor (Northwind Owner) joined = %v, %v; want false", joined, err)
		}
	})
}
