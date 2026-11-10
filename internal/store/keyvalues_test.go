package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

func testKEK(t *testing.T) *envelope.KEK {
	t.Helper()
	p := filepath.Join(t.TempDir(), "kek")
	if err := envelope.GenerateKEK(p); err != nil {
		t.Fatal(err)
	}
	k, err := envelope.LoadKEK(p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// dumpAll returns every row of every table as text, for canary scans.
func dumpAll(t *testing.T, db *sql.DB) string {
	t.Helper()
	var out bytes.Buffer
	for _, table := range []string{"keys", "local_key_values", "audit_events", "outbox"} {
		rows, err := db.Query("SELECT * FROM " + table)
		if err != nil {
			t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			for _, v := range vals {
				if b, ok := v.([]byte); ok {
					v = string(b)
				}
				fmt.Fprintf(&out, "%v\x1f", v)
			}
		}
		rows.Close()
	}
	return out.String()
}

func TestPutLocalValue(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		kek := testKEK(t)
		canary1 := []byte("sk-CANARY-one-7f3a9c")
		canary2 := []byte("sk-CANARY-two-1b2d4e")
		now := time.Now()

		if _, _, err := st.LocalValue(ctx, kek, store.DemoOrgID, "k_stripe"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("LocalValue before any put: %v, want ErrNotFound", err)
		}
		v1, err := st.PutLocalValue(ctx, kek, store.DemoOrgID, "k_stripe", canary1, "keyhole-cli", now)
		if err != nil || v1 != 1 {
			t.Fatalf("first put = %d, %v", v1, err)
		}
		got, ver, err := st.LocalValue(ctx, kek, store.DemoOrgID, "k_stripe")
		if err != nil || ver != 1 || !bytes.Equal(got, canary1) {
			t.Fatalf("LocalValue = %q v%d, %v", got, ver, err)
		}
		v2, err := st.PutLocalValue(ctx, kek, store.DemoOrgID, "k_stripe", canary2, "keyhole-cli", now.Add(time.Second))
		if err != nil || v2 != 2 {
			t.Fatalf("second put = %d, %v", v2, err)
		}
		got, ver, err = st.LocalValue(ctx, kek, store.DemoOrgID, "k_stripe")
		if err != nil || ver != 2 || !bytes.Equal(got, canary2) {
			t.Fatalf("after re-put LocalValue = %q v%d, %v", got, ver, err)
		}

		db := store.DBOf(st)
		var current, superseded int
		if err := db.QueryRow(`SELECT
			SUM(CASE WHEN superseded_at IS NULL THEN 1 ELSE 0 END),
			SUM(CASE WHEN superseded_at IS NOT NULL THEN 1 ELSE 0 END)
			FROM local_key_values WHERE key_id = 'k_stripe'`).Scan(&current, &superseded); err != nil {
			t.Fatal(err)
		}
		if current != 1 || superseded != 1 {
			t.Fatalf("versions: %d current, %d superseded; want 1 and 1", current, superseded)
		}

		k, err := st.GetKey(ctx, store.DemoOrgID, "k_stripe")
		if err != nil {
			t.Fatal(err)
		}
		var addr struct {
			LocalValueID string `json:"localValueId"`
			Version      int    `json:"version"`
		}
		if err := json.Unmarshal(k.Address, &addr); err != nil || addr.Version != 2 || addr.LocalValueID == "" {
			t.Fatalf("key address = %s, %v", k.Address, err)
		}
		// The demo row starts with a length; a put clears it, since reads return it.
		if k.Length != nil || k.RotatedAt == nil {
			t.Fatalf("key length/rotated = %v/%v; want no length and a rotation time", k.Length, k.RotatedAt)
		}
		var audits int
		if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE subject_id = 'k_stripe' AND type = 'admin'
			AND actor_kind = 'system' AND actor_id = 'keyhole-cli'`).Scan(&audits); err != nil || audits != 2 {
			t.Fatalf("audit rows for the puts = %d, %v; want 2", audits, err)
		}

		// No row anywhere holds a value in clear, and no read API returns one.
		dump := dumpAll(t, db)
		keys, _ := st.ListKeys(ctx, store.DemoOrgID)
		reads, _ := json.Marshal([]any{k, keys})
		for _, c := range [][]byte{canary1, canary2} {
			if bytes.Contains([]byte(dump), c) || bytes.Contains(reads, c) {
				t.Fatalf("value %q found in the database or a read result", c)
			}
		}
	})
}

func TestPutLocalValueRefuses(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		// A Local key whose source is removed: only the status check refuses it.
		if _, err := st.LoadFixture(ctx, store.Fixture{Tables: []store.FixtureTable{{Table: "keys", Rows: []map[string]any{{
			"id": "k_local_removed", "org_id": store.DemoOrgID, "name": "removed_local", "store_id": "st_local",
			"provider": "local", "pinned": false, "status": "source_removed", "created_at": "@now",
			"created_by_kind": "system", "created_by_id": "t", "updated_at": "@now", "rev": json.Number("0"),
		}}}}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		kek := testKEK(t)
		v := []byte("x")
		cases := map[string]struct {
			org, key string
			want     error
		}{
			"openbao key":       {store.DemoOrgID, "k_bao1", store.ErrNotLocal},
			"removed openbao":   {store.DemoOrgID, "k_old", store.ErrNotLocal},
			"removed local key": {store.DemoOrgID, "k_local_removed", store.ErrNotLocal},
			"unknown key":       {store.DemoOrgID, "k_nope", store.ErrNotFound},
			// org_nw is a real organization, so only the org scope refuses it.
			"other org's key": {"org_nw", "k_stripe", store.ErrNotFound},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				if _, err := st.PutLocalValue(ctx, kek, c.org, c.key, v, "keyhole-cli", time.Now()); !errors.Is(err, c.want) {
					t.Fatalf("err = %v, want %v", err, c.want)
				}
			})
		}
		if _, err := st.PutLocalValue(ctx, kek, store.DemoOrgID, "k_stripe", nil, "keyhole-cli", time.Now()); err == nil {
			t.Fatal("an empty value was stored")
		}
		var n int
		if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM local_key_values`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("refused puts left %d value rows (%v)", n, err)
		}
	})
}

func TestLocalValueIsBoundToItsRow(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		kek := testKEK(t)
		for _, k := range []string{"k_stripe", "k_slack"} {
			if _, err := st.PutLocalValue(ctx, kek, store.DemoOrgID, k, []byte("value-of-"+k), "keyhole-cli", time.Now()); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := st.LocalValue(ctx, testKEK(t), store.DemoOrgID, "k_stripe"); !errors.Is(err, envelope.ErrOpen) {
			t.Fatalf("a different KEK opened the value: %v", err)
		}
		db := store.DBOf(st)
		// Copy k_slack's envelope onto k_stripe's row: it must not open as k_stripe.
		if _, err := db.Exec(`UPDATE local_key_values SET envelope =
			(SELECT envelope FROM local_key_values WHERE key_id = 'k_slack') WHERE key_id = 'k_stripe'`); err != nil {
			t.Fatal(err)
		}
		if got, _, err := st.LocalValue(ctx, kek, store.DemoOrgID, "k_stripe"); !errors.Is(err, envelope.ErrOpen) {
			t.Fatalf("a swapped envelope opened: %q, %v", got, err)
		}
		// A key whose source is removed resolves to nothing, though its value row remains.
		if _, err := db.Exec(`UPDATE keys SET status = 'source_removed' WHERE id = 'k_slack'`); err != nil {
			t.Fatal(err)
		}
		if got, _, err := st.LocalValue(ctx, kek, store.DemoOrgID, "k_slack"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a removed key resolved: %q, %v", got, err)
		}
	})
}

// TestRacingPutsDoNotOverwrite: two puts that pick the same next version
// can't both land; the second gets ErrExists and its transaction rolls back.
func TestRacingPutsDoNotOverwrite(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		kek := testKEK(t)
		if _, err := st.PutLocalValue(ctx, kek, store.DemoOrgID, "k_stripe", []byte("first"), "keyhole-cli", time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := store.InsertLocalValueForTest(ctx, st, store.DemoOrgID, "k_stripe", 1); !errors.Is(err, store.ErrExists) {
			t.Fatalf("a second row at version 1: %v, want ErrExists", err)
		}
		got, v, err := st.LocalValue(ctx, kek, store.DemoOrgID, "k_stripe")
		if err != nil || v != 1 || string(got) != "first" {
			t.Fatalf("after the lost race: %q v%d, %v", got, v, err)
		}
	})
}
