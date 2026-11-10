package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// A tool's privateAllow is IPs or CIDRs only, canonical.
func TestNormalizePrivateAllow(t *testing.T) {
	for _, c := range []struct {
		in   []string
		want []string
	}{
		{nil, []string{}},
		{[]string{"192.168.10.90"}, []string{"192.168.10.90/32"}},
		{[]string{"192.168.10.90/32", "192.168.10.90"}, []string{"192.168.10.90/32"}},
		{[]string{"10.20.0.0/16", "127.0.0.1"}, []string{"10.20.0.0/16", "127.0.0.1/32"}},
		{[]string{"::1"}, []string{"::1/128"}},
		{[]string{"fd12:3456::/32"}, []string{"fd12:3456::/32"}},
		{[]string{"FD12:3456::/32"}, []string{"fd12:3456::/32"}},
		{[]string{"10.0.0.0/8"}, []string{"10.0.0.0/8"}},
		{[]string{"::ffff:192.168.10.90"}, []string{"192.168.10.90/32"}},
		{[]string{"::ffff:127.0.0.1"}, []string{"127.0.0.1/32"}},
		// Sorted whatever the input order.
		{[]string{"192.168.10.90", "10.20.0.0/16"}, []string{"10.20.0.0/16", "192.168.10.90/32"}},
		{[]string{"fd12:3456::/32", "127.0.0.1", "::1"}, []string{"127.0.0.1/32", "::1/128", "fd12:3456::/32"}},
	} {
		got, err := store.NormalizePrivateAllow(c.in)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{
		"0.0.0.0/0", "::/0", "10.0.0.1/24", "fd12::1/16", "10.0.0.0/33",
		"litellm.example.invalid", "localhost", "", "*", "10.0.0.0/", "::ffff:10.0.0.0/104",
		// Space whose sidecar reading
		// differs from what is written.
		"2002:a00::/24", "2002::/16", "64:ff9b::/96", "2002:c0a8:2a5a::1", "64:ff9b::c0a8:2a5a",
		"64:ff9b:1::/48", "64:ff9b:1::a00:1", "::ffff:0:0/96", "::ffff:192.168.10.90/128",
		// Two halves of /1 rebuild /0: a floor of /8 (v4) and /16 (v6).
		"0.0.0.0/1", "128.0.0.0/1", "::/1", "8000::/1", "10.0.0.0/7", "fd00::/8",
		// A zone id; never-reached ranges.
		"fe80::1%eth0", "fd12::1%eth0", "10.0.0.1%eth0", "169.254.169.254", "169.254.0.0/16", "0.0.0.0", "224.0.0.1", "255.255.255.255",
		"240.0.0.0/8", "fe80::1", "ff02::1", "::",
		// Not quite an address.
		" 10.0.0.1", "10.0.0.1 ", "010.0.0.1", "10.0.0.01/32", "10.0.0.0/08",
	} {
		if _, err := store.NormalizePrivateAllow([]string{"127.0.0.1", bad}); !errors.Is(err, store.ErrBadPrivateAllow) {
			t.Errorf("%q: %v, want ErrBadPrivateAllow", bad, err)
		}
	}
}

// /0 has its own message, before the width floor would refuse it anyway.
func TestNormalizePrivateAllowNamesEverything(t *testing.T) {
	for _, all := range []string{"0.0.0.0/0", "::/0"} {
		if _, err := store.NormalizePrivateAllow([]string{all}); err == nil || !strings.Contains(err.Error(), "allows every address") {
			t.Errorf("%s: %v", all, err)
		}
	}
}

func TestSetToolPrivateAllow(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		db := store.DBOf(st)
		resolve := func() store.ResolvedSlot {
			t.Helper()
			r, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key")
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		versions := func() int {
			var n int
			if err := db.QueryRow(`SELECT COUNT(*) FROM tool_versions WHERE tool_id = 't_litellm'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if r := resolve(); r.ToolVersion != 1 || len(r.Tool.PrivateAllow) != 0 {
			t.Fatalf("before: v%d %q", r.ToolVersion, r.Tool.PrivateAllow)
		}

		at := time.UnixMilli(1793000000123)
		res, err := st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", []string{"192.168.10.90"}, "op-7", at)
		if err != nil || !res.Changed || res.Version != 2 || !slices.Equal(res.PrivateAllow, []string{"192.168.10.90/32"}) {
			t.Fatalf("set: %+v, %v", res, err)
		}
		// A resolve now gets version 2 with the list, and the rest of the
		// snapshot unchanged.
		r := resolve()
		if r.ToolVersion != 2 || !slices.Equal(r.Tool.PrivateAllow, []string{"192.168.10.90/32"}) ||
			r.Tool.BaseURL != "https://litellm.example.invalid" || len(r.Tool.Actions) != 2 || len(r.Tool.Slots) != 1 {
			t.Fatalf("after: v%d %+v", r.ToolVersion, r.Tool)
		}
		// Version 2 is version 1's snapshot with only privateAllow added.
		var s1, s2 map[string]any
		for id, dst := range map[string]*map[string]any{"t_litellm@1": &s1, "t_litellm@2": &s2} {
			var raw string
			if err := db.QueryRow(store.QOf(st, `SELECT snapshot FROM tool_versions WHERE id = ?`), id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(raw), dst); err != nil {
				t.Fatal(err)
			}
		}
		if got := s2["privateAllow"]; len(got.([]any)) != 1 || got.([]any)[0] != "192.168.10.90/32" {
			t.Fatalf("v2 privateAllow: %v", got)
		}
		delete(s2, "privateAllow")
		a, _ := json.Marshal(s1)
		b2, _ := json.Marshal(s2)
		if string(a) != string(b2) {
			t.Fatalf("v2 isn't v1 plus privateAllow:\n v1 %s\n v2 %s", a, b2)
		}
		// Version 1 is untouched (versions are immutable).
		var v1 string
		if err := db.QueryRow(`SELECT snapshot FROM tool_versions WHERE id = 't_litellm@1'`).Scan(&v1); err != nil || strings.Contains(v1, "privateAllow") {
			t.Fatalf("version 1 changed: %v %s", err, v1)
		}
		// The audit row, and the changes recorded on the version.
		var object, actor string
		var auditAt, expire int64
		if err := db.QueryRow(`SELECT object, actor_id, at, expire_at FROM audit_events WHERE org_id = 'org_team' AND subject_kind = 'tool'
			AND subject_id = 't_litellm' AND result = 'tool version published'`).Scan(&object, &actor, &auditAt, &expire); err != nil {
			t.Fatal(err)
		}
		var retention int
		if err := db.QueryRow(`SELECT audit_retention_days FROM orgs WHERE id = 'org_team'`).Scan(&retention); err != nil {
			t.Fatal(err)
		}
		if object != "version 2: privateAllow [192.168.10.90/32]" || actor != "op-7" || auditAt != at.UnixMilli() ||
			expire != at.AddDate(0, 0, retention).UnixMilli() {
			t.Fatalf("audit: %q %q %d %d", object, actor, auditAt, expire)
		}
		var changes string
		if err := db.QueryRow(`SELECT changes FROM tool_versions WHERE id = 't_litellm@2'`).Scan(&changes); err != nil {
			t.Fatal(err)
		}
		var ch map[string]map[string][]string
		if err := json.Unmarshal([]byte(changes), &ch); err != nil || !slices.Equal(ch["privateAllow"]["to"], []string{"192.168.10.90/32"}) ||
			len(ch["privateAllow"]["from"]) != 0 {
			t.Fatalf("changes: %s %v", changes, err)
		}

		// The same list again: nothing published.
		res, err = st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", []string{"192.168.10.90/32"}, "op-7", time.Now())
		if err != nil || res.Changed || res.Version != 2 || versions() != 2 {
			t.Fatalf("repeat: %+v, %v, %d versions", res, err, versions())
		}
		// A bad list: refused, nothing written.
		if _, err := st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", []string{"0.0.0.0/0"}, "op-7", time.Now()); !errors.Is(err, store.ErrBadPrivateAllow) || versions() != 2 {
			t.Fatalf("bad: %v, %d versions", err, versions())
		}
		// Unknown tool, another org: not found.
		for _, c := range [][2]string{{"org_team", "t_nope"}, {"org_acme", "t_litellm"}} {
			if _, err := st.SetToolPrivateAllow(ctx, c[0], c[1], []string{"10.0.0.0/8"}, "op-7", time.Now()); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%v: %v, want ErrNotFound", c, err)
			}
		}
		// A grant pinned to version 2 keeps it when version 3 clears the list.
		if _, err := db.Exec(`UPDATE grants SET pinned_version = 2 WHERE id = 'gr_team_litellm'`); err != nil {
			t.Fatal(err)
		}
		res, err = st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", nil, "op-8", time.Now())
		if err != nil || !res.Changed || res.Version != 3 || len(res.PrivateAllow) != 0 || res.Pinned != 1 {
			t.Fatalf("clear: %+v, %v", res, err)
		}
		if r := resolve(); r.ToolVersion != 2 || !slices.Equal(r.Tool.PrivateAllow, []string{"192.168.10.90/32"}) {
			t.Fatalf("pinned grant: v%d %q", r.ToolVersion, r.Tool.PrivateAllow)
		}
		// Unpinned, it follows the published version 3 (no list).
		if _, err := db.Exec(`UPDATE grants SET pinned_version = NULL WHERE id = 'gr_team_litellm'`); err != nil {
			t.Fatal(err)
		}
		if r := resolve(); r.ToolVersion != 3 || len(r.Tool.PrivateAllow) != 0 {
			t.Fatalf("unpinned: v%d %q", r.ToolVersion, r.Tool.PrivateAllow)
		}
		// A higher, unpublished version already exists: the new one comes after it.
		if _, err := db.Exec(`INSERT INTO tool_versions (id, org_id, tool_id, version, snapshot, published_at, published_by_kind, published_by_id)
			SELECT 't_litellm@7', org_id, tool_id, 7, snapshot, published_at, published_by_kind, published_by_id FROM tool_versions WHERE id = 't_litellm@3'`); err != nil {
			t.Fatal(err)
		}
		res, err = st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", []string{"10.0.0.0/8"}, "op-8", time.Now())
		if err != nil || res.Version != 8 {
			t.Fatalf("after an unpublished v7: %+v, %v", res, err)
		}
		// A draft or deleted tool takes no change.
		for _, q := range []string{
			`UPDATE tools SET status = 'draft' WHERE id = 't_litellm'`,
			`UPDATE tools SET status = 'published', deleted_at = 1 WHERE id = 't_litellm'`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(err)
			}
			if _, err := st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", []string{"10.0.0.0/16"}, "op-8", time.Now()); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: %v, want ErrNotFound", q, err)
			}
		}
	})
}

// A fixture's tool version is checked at load: a bad or non-canonical
// privateAllow fails the load, with nothing written.
func TestLoadChecksPrivateAllow(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		for _, c := range []struct {
			allow any
			ok    bool
		}{
			{[]any{"192.168.10.90/32"}, true},
			{[]any{}, true},
			{[]any{"0.0.0.0/0"}, false},
			{[]any{"192.168.10.90"}, false}, // not canonical
			{[]any{"litellm.example.invalid"}, false},
			{[]any{"10.0.0.0/8", "10.0.0.0/8"}, false}, // duplicate
			{[]any{"2002:a00::/24"}, false},
			{[]any{"2002::/16"}, false},
			{[]any{"64:ff9b::/96"}, false},
			{[]any{"2002:c0a8:2a5a::1/128"}, false},
			{[]any{"64:ff9b::c0a8:2a5a/128"}, false},
			{"10.0.0.0/8", false}, // not a list
		} {
			st := storetest.Open(t, b)
			fx := store.Fixture{Tables: []store.FixtureTable{
				{Table: "orgs", Rows: []map[string]any{{"id": "org_x", "name": "X", "status": "active", "audit_retention_days": json.Number("30"),
					"created_at": "@now", "created_by_kind": "system", "created_by_id": "t", "updated_at": "@now", "rev": json.Number("0")}}},
				{Table: "tool_versions", Rows: []map[string]any{{"id": "t_x@1", "org_id": "org_x", "tool_id": "t_x", "version": json.Number("1"),
					"snapshot":     map[string]any{"baseUrl": "https://x.example", "privateAllow": c.allow},
					"published_at": "@now", "published_by_kind": "system", "published_by_id": "t"}}},
			}}
			_, err := st.LoadFixture(ctx, fx, time.Now())
			if c.ok {
				// The privateAllow check passes; the row then fails on its
				// missing tool (a foreign key), which isn't this test's concern.
				if errors.Is(err, store.ErrBadPrivateAllow) {
					t.Errorf("%v: refused: %v", c.allow, err)
				}
				continue
			}
			if err == nil || (!errors.Is(err, store.ErrBadPrivateAllow) && !strings.Contains(err.Error(), "snapshot")) {
				t.Errorf("%v: %v, want a privateAllow refusal", c.allow, err)
			}
			var n int
			if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM orgs WHERE id = 'org_x'`).Scan(&n); err != nil || n != 0 {
				t.Errorf("%v: a refused load wrote %d orgs (%v)", c.allow, n, err)
			}
		}
	})
}

// A publish replaces the field whatever its key's spelling: resolve decodes
// the snapshot case-insensitively, so a hand-loaded "privateallow" would
// otherwise survive next to the new list.
func TestSetToolPrivateAllowReplacesAnySpelling(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		db := store.DBOf(st)
		var raw string
		if err := db.QueryRow(`SELECT snapshot FROM tool_versions WHERE id = 't_litellm@1'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var snap map[string]any
		if err := json.Unmarshal([]byte(raw), &snap); err != nil {
			t.Fatal(err)
		}
		snap["privateallow"] = []string{"10.9.0.0/16"}
		odd, _ := json.Marshal(snap)
		if _, err := db.Exec(store.QOf(st, `UPDATE tool_versions SET snapshot = ? WHERE id = 't_litellm@1'`), string(odd)); err != nil {
			t.Fatal(err)
		}
		res, err := st.SetToolPrivateAllow(ctx, "org_team", "t_litellm", []string{"192.168.10.90"}, "op", time.Now())
		if err != nil || !res.Changed {
			t.Fatalf("set: %+v, %v", res, err)
		}
		var v2 string
		if err := db.QueryRow(`SELECT snapshot FROM tool_versions WHERE id = 't_litellm@2'`).Scan(&v2); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(v2, "privateallow") || strings.Contains(v2, "10.9.0.0") {
			t.Fatalf("the old spelling survived: %s", v2)
		}
		r, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key")
		if err != nil || !slices.Equal(r.Tool.PrivateAllow, []string{"192.168.10.90/32"}) {
			t.Fatalf("resolve: %q, %v", r.Tool.PrivateAllow, err)
		}
	})
}
