package store_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// k1Store loads the K1 fixture plus a control-plane user (usr_t1) as a
// member of ws_team, the way a resolve names its caller.
func k1Store(t *testing.T, b storetest.Backend) *store.Store {
	t.Helper()
	ctx := context.Background()
	st := storetest.Open(t, b)
	f, err := os.Open("../../testdata/litellm.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fx, err := store.ParseFixture(f)
	if err != nil {
		t.Fatal(err)
	}
	fx.Tables = append(fx.Tables, store.FixtureTable{Table: "workspace_memberships", Rows: []map[string]any{{
		"id": "wm_team_usr_t1", "org_id": "org_team", "workspace_id": "ws_team", "member_kind": "user",
		"member_id": "usr_t1", "role": "member", "added_at": "@now", "added_by_kind": "system",
		"added_by_id": "t", "updated_at": "@now", "rev": json.Number("0"),
	}}})
	if _, err := st.LoadFixture(ctx, fx, time.Now()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestResolveSlot(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key")
		if err != nil {
			t.Fatal(err)
		}
		if got.GrantID != "gr_team_litellm" || got.WorkspaceID != "ws_team" || got.KeyID != "k_litellm_test" || got.TTL != 300 || got.ToolVersion != 1 {
			t.Fatalf("ResolveSlot = %+v", got)
		}
		// The published version's definition, as the sidecar needs it (P4).
		tl := got.Tool
		if tl.BaseURL != "https://litellm.example.invalid" || tl.TimeoutMs != 60000 || len(tl.Actions) != 2 ||
			tl.Actions[0].Method != "GET" || tl.Actions[0].Path != "/v1/models" || tl.Actions[1].Path != "/v1/chat/completions" ||
			len(tl.Slots) != 1 || tl.Slots[0].Name != "litellm_key" || tl.Slots[0].Field != "Authorization" || tl.Slots[0].Scheme != "Bearer" {
			t.Fatalf("tool = %+v", tl)
		}
		// The agent row from the fixture resolves the same way, by its own kind.
		if _, err := st.ResolveSlot(ctx, "org_team", "agent", "ag_team_790", "t_litellm", "litellm_key"); err != nil {
			t.Fatalf("the fixture's agent: %v", err)
		}
		db := store.DBOf(st)
		if _, err := db.Exec(`UPDATE keys SET cache_ttl_seconds = 60 WHERE id = 'k_litellm_test'`); err != nil {
			t.Fatal(err)
		}
		if got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key"); err != nil || got.TTL != 60 {
			t.Fatalf("a key's own cache TTL: %+v, %v", got, err)
		}
		if _, err := db.Exec(`UPDATE keys SET cache_ttl_seconds = NULL WHERE id = 'k_litellm_test';
			UPDATE stores SET cache_default_ttl_seconds = 0 WHERE id = 'st_team_local'`); err != nil {
			t.Fatal(err)
		}
		if got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key"); err != nil || got.TTL != 0 {
			t.Fatalf("a store TTL of 0 (use once): %+v, %v", got, err)
		}
	})
}

func TestResolveSlotRefuses(t *testing.T) {
	type tc struct {
		org, kind, sub, tool, slot string
		mutate                     string // SQL applied first, on a fresh store
		want                       error
	}
	ok := tc{org: "org_team", kind: "user", sub: "usr_t1", tool: "t_litellm", slot: "litellm_key"}
	with := func(f func(*tc)) tc { c := ok; f(&c); return c }
	cases := map[string]tc{
		"not a member":       with(func(c *tc) { c.sub = "usr_t2" }),
		"wrong kind":         with(func(c *tc) { c.kind = "agent" }),
		"other org":          with(func(c *tc) { c.org = "org_other" }),
		"other tool":         with(func(c *tc) { c.tool = "t_nope" }),
		"other slot":         with(func(c *tc) { c.slot = "other" }),
		"grant disabled":     with(func(c *tc) { c.mutate = `UPDATE grants SET enabled = FALSE` }),
		"tool draft":         with(func(c *tc) { c.mutate = `UPDATE tools SET status = 'draft', published_version = NULL` }),
		"tool deleted":       with(func(c *tc) { c.mutate = `UPDATE tools SET deleted_at = 1` }),
		"slot unfilled":      with(func(c *tc) { c.mutate = `UPDATE grant_slots SET key_id = NULL` }),
		"key removed":        with(func(c *tc) { c.mutate = `UPDATE keys SET status = 'source_removed'` }),
		"workspace deleted":  with(func(c *tc) { c.mutate = `UPDATE workspaces SET status = 'deleted'` }),
		"org deleted":        with(func(c *tc) { c.mutate = `UPDATE orgs SET status = 'deleted'` }),
		"membership removed": with(func(c *tc) { c.mutate = `DELETE FROM workspace_memberships WHERE member_id = 'usr_t1'` }),
	}
	ctx := context.Background()
	for name, c := range cases {
		// A fresh database per case (storetest.Run inside the case): a
		// mutation must not carry into the next case and refuse it for the
		// wrong reason.
		t.Run(name, func(t *testing.T) {
			storetest.Run(t, func(t *testing.T, b storetest.Backend) {
				st := k1Store(t, b)
				if c.mutate != "" {
					if _, err := store.DBOf(st).Exec(c.mutate); err != nil {
						t.Fatal(err)
					}
				}
				want := c.want
				if want == nil {
					want = store.ErrNotFound
				}
				if got, err := st.ResolveSlot(ctx, c.org, c.kind, c.sub, c.tool, c.slot); !errors.Is(err, want) {
					t.Fatalf("ResolveSlot = %+v, %v; want %v", got, err, want)
				}
			})
		})
	}
}

func TestResolveSlotAmbiguity(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		db := store.DBOf(st)
		// A second workspace, the same caller, the same tool and slot.
		if _, err := db.Exec(`INSERT INTO workspaces (id, org_id, name, status, created_at, created_by_kind, created_by_id, updated_at, rev)
			VALUES ('ws_two', 'org_team', 'Two', 'active', 1, 'system', 't', 1, 0);
			INSERT INTO workspace_memberships (id, org_id, workspace_id, member_kind, member_id, role, added_at, added_by_kind, added_by_id, updated_at, rev)
			VALUES ('wm_two', 'org_team', 'ws_two', 'user', 'usr_t1', 'member', 1, 'system', 't', 1, 0);
			INSERT INTO grants (id, org_id, workspace_id, tool_id, enabled, per_minute, granted_at, granted_by_kind, granted_by_id, updated_at, rev)
			VALUES ('gr_two', 'org_team', 'ws_two', 't_litellm', TRUE, 30, 1, 'system', 't', 1, 0);
			INSERT INTO grant_slots (grant_id, org_id, slot, key_id) VALUES ('gr_two', 'org_team', 'litellm_key', 'k_litellm_test')`); err != nil {
			t.Fatal(err)
		}
		if got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key"); err != nil || got.KeyID != "k_litellm_test" {
			t.Fatalf("two grants, one key: %+v, %v", got, err)
		}
		if _, err := db.Exec(`INSERT INTO keys (id, org_id, name, store_id, provider, pinned, notes, status, created_at, created_by_kind, created_by_id, updated_at, rev)
			VALUES ('k_other', 'org_team', 'other', 'st_team_local', 'local', FALSE, '', 'active', 1, 'system', 't', 1, 0);
			UPDATE grant_slots SET key_id = 'k_other' WHERE grant_id = 'gr_two'`); err != nil {
			t.Fatal(err)
		}
		if got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key"); !errors.Is(err, store.ErrAmbiguous) {
			t.Fatalf("two grants, two keys: %+v, %v; want ErrAmbiguous", got, err)
		}
	})
}

func TestSetGrantEnabled(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		audits := func() int {
			var n int
			if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM audit_events WHERE org_id = 'org_team' AND subject_kind = 'grant' AND subject_id = 'gr_team_litellm'`).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		resolves := func() error {
			_, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key")
			return err
		}
		// rev and updated_by_id move on a real change only.
		var rev0 int64
		row := func() (int64, string) {
			var rev int64
			var by sql.NullString
			if err := store.DBOf(st).QueryRow(`SELECT rev, updated_by_id FROM grants WHERE id = 'gr_team_litellm'`).Scan(&rev, &by); err != nil {
				t.Fatal(err)
			}
			return rev, by.String
		}
		rev0, _ = row()
		if err := resolves(); err != nil {
			t.Fatal(err)
		}
		t1 := time.Now()
		t2 := t1.Add(time.Second)
		changed, err := st.SetGrantEnabled(ctx, "org_team", "gr_team_litellm", false, "keyhole-cli", t1)
		if err != nil || !changed || audits() != 1 {
			t.Fatalf("disable: %v, %v, %d audit rows", changed, err, audits())
		}
		if rev, by := row(); rev != rev0+1 || by != "keyhole-cli" {
			t.Fatalf("after disable: rev %d (was %d), updated_by %q", rev, rev0, by)
		}
		if err := resolves(); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a disabled grant resolved: %v", err)
		}
		// Again: nothing changes, no second row.
		changed, err = st.SetGrantEnabled(ctx, "org_team", "gr_team_litellm", false, "other-actor", time.Now())
		if err != nil || changed || audits() != 1 {
			t.Fatalf("a second disable: %v, %v, %d audit rows", changed, err, audits())
		}
		if rev, by := row(); rev != rev0+1 || by != "keyhole-cli" {
			t.Fatalf("a no-op moved rev or updated_by: %d, %q", rev, by)
		}
		var slots int
		if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM grant_slots WHERE grant_id = 'gr_team_litellm' AND key_id = 'k_litellm_test'`).Scan(&slots); err != nil || slots != 1 {
			t.Fatalf("disabling touched the slots: %d, %v", slots, err)
		}
		changed, err = st.SetGrantEnabled(ctx, "org_team", "gr_team_litellm", true, "keyhole-cli", t2)
		if err != nil || !changed || audits() != 2 {
			t.Fatalf("enable: %v, %v, %d audit rows", changed, err, audits())
		}
		if rev, _ := row(); rev != rev0+2 {
			t.Fatalf("after enable: rev %d, want %d", rev, rev0+2)
		}
		if err := resolves(); err != nil {
			t.Fatalf("re-enabled but no resolve: %v", err)
		}
		// The two admin rows, in order (disable at t1, enable at t2), with their object.
		rows, err := store.DBOf(st).Query(`SELECT result, object FROM audit_events WHERE org_id = 'org_team' AND subject_kind = 'grant' AND subject_id = 'gr_team_litellm' ORDER BY at`)
		if err != nil {
			t.Fatal(err)
		}
		var got [][2]string
		for rows.Next() {
			var r, o string
			if err := rows.Scan(&r, &o); err != nil {
				t.Fatal(err)
			}
			got = append(got, [2]string{r, o})
		}
		rows.Close()
		want := [][2]string{{"grant disabled", "grant gr_team_litellm (tool t_litellm)"}, {"grant enabled", "grant gr_team_litellm (tool t_litellm)"}}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("audit rows = %q, want %q", got, want)
		}
		for name, c := range map[string][2]string{
			"unknown grant":   {"org_team", "gr_nope"},
			"other org":       {"org_team", "gr_prod_stripe"}, // a real grant, in org_acme
			"grant elsewhere": {"org_acme", "gr_team_litellm"},
		} {
			if _, err := st.SetGrantEnabled(ctx, c[0], c[1], false, "keyhole-cli", time.Now()); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: %v, want ErrNotFound", name, err)
			}
		}
		var stripe bool
		if err := store.DBOf(st).QueryRow(`SELECT enabled FROM grants WHERE id = 'gr_prod_stripe'`).Scan(&stripe); err != nil || !stripe {
			t.Fatalf("another org's grant changed: %v, %v", stripe, err)
		}
	})
}

// Two concurrent disables: exactly one changes the grant, bumps rev once
// and writes one audit row (the UPDATE is the
// guard, so this holds on Postgres's READ COMMITTED too).
func TestSetGrantEnabledIsAtomic(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		for round := 0; round < 5; round++ {
			var rev0 int64
			if err := store.DBOf(st).QueryRow(`SELECT rev FROM grants WHERE id = 'gr_team_litellm'`).Scan(&rev0); err != nil {
				t.Fatal(err)
			}
			want := round%2 == 1 // disable on even rounds, enable on odd
			type res struct {
				changed bool
				err     error
			}
			out := make(chan res, 2)
			start := make(chan struct{})
			for i := 0; i < 2; i++ {
				go func() {
					<-start
					c, err := st.SetGrantEnabled(ctx, "org_team", "gr_team_litellm", want, "race", time.Now())
					out <- res{c, err}
				}()
			}
			close(start)
			changes := 0
			for i := 0; i < 2; i++ {
				r := <-out
				if r.err != nil {
					t.Fatalf("round %d: %v", round, r.err)
				}
				if r.changed {
					changes++
				}
			}
			var rev int64
			var enabled bool
			if err := store.DBOf(st).QueryRow(`SELECT rev, enabled FROM grants WHERE id = 'gr_team_litellm'`).Scan(&rev, &enabled); err != nil {
				t.Fatal(err)
			}
			if changes != 1 || rev != rev0+1 || enabled != want {
				t.Fatalf("round %d: %d changes, rev %d → %d, enabled %v; want exactly one change", round, changes, rev0, rev, enabled)
			}
		}
		var audits int
		if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM audit_events WHERE subject_kind = 'grant' AND subject_id = 'gr_team_litellm'`).Scan(&audits); err != nil || audits != 5 {
			t.Fatalf("%d audit rows for 5 changes (%v)", audits, err)
		}
	})
}

// A grant pinned to an older version gets that version's definition, and
// two grants at different versions are ambiguous (P4a).
func TestResolveSlotUsesTheGrantedVersion(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		db := store.DBOf(st)
		// Version 2 moves the base URL (loaded as a fixture row, so the JSON
		// column is written the same way on SQLite and Postgres); the tool
		// now points at it.
		if _, err := st.LoadFixture(ctx, store.Fixture{Tables: []store.FixtureTable{{Table: "tool_versions", Rows: []map[string]any{{
			"id": "t_litellm@2", "org_id": "org_team", "tool_id": "t_litellm", "version": json.Number("2"),
			"snapshot": map[string]any{"internalName": "litellm", "displayName": "LiteLLM", "description": "v2",
				"baseUrl": "https://v2.example", "actions": []any{}, "slots": []any{}, "perMinute": json.Number("30"), "timeoutMs": json.Number("60000")},
			"published_at": "@now", "published_by_kind": "system", "published_by_id": "t",
		}}}}}, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE tools SET published_version = 2 WHERE id = 't_litellm'`); err != nil {
			t.Fatal(err)
		}
		got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key")
		if err != nil || got.ToolVersion != 2 || got.Tool.BaseURL != "https://v2.example" {
			t.Fatalf("the latest version: %+v, %v", got, err)
		}
		if _, err := db.Exec(`UPDATE grants SET pinned_version = 1 WHERE id = 'gr_team_litellm'`); err != nil {
			t.Fatal(err)
		}
		got, err = st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key")
		if err != nil || got.ToolVersion != 1 || got.Tool.BaseURL != "https://litellm.example.invalid" {
			t.Fatalf("the pinned version: %+v, %v", got, err)
		}
		// A pin to a version that doesn't exist resolves nothing.
		if _, err := db.Exec(`UPDATE grants SET pinned_version = 9 WHERE id = 'gr_team_litellm'`); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a pin to a missing version: %v", err)
		}
		// Two grants, the same key, different versions: ambiguous.
		if _, err := db.Exec(`UPDATE grants SET pinned_version = 1 WHERE id = 'gr_team_litellm';
			INSERT INTO workspaces (id, org_id, name, status, created_at, created_by_kind, created_by_id, updated_at, rev)
			VALUES ('ws_two', 'org_team', 'Two', 'active', 1, 'system', 't', 1, 0);
			INSERT INTO workspace_memberships (id, org_id, workspace_id, member_kind, member_id, role, added_at, added_by_kind, added_by_id, updated_at, rev)
			VALUES ('wm_two', 'org_team', 'ws_two', 'user', 'usr_t1', 'member', 1, 'system', 't', 1, 0);
			INSERT INTO grants (id, org_id, workspace_id, tool_id, enabled, per_minute, granted_at, granted_by_kind, granted_by_id, updated_at, rev)
			VALUES ('gr_two', 'org_team', 'ws_two', 't_litellm', TRUE, 30, 1, 'system', 't', 1, 0);
			INSERT INTO grant_slots (grant_id, org_id, slot, key_id) VALUES ('gr_two', 'org_team', 'litellm_key', 'k_litellm_test')`); err != nil {
			t.Fatal(err)
		}
		if got, err := st.ResolveSlot(ctx, "org_team", "user", "usr_t1", "t_litellm", "litellm_key"); !errors.Is(err, store.ErrAmbiguous) {
			t.Fatalf("two grants at versions 1 and 2: %+v, %v; want ErrAmbiguous", got, err)
		}
	})
}

// keyhole members add|remove (the master's G4 card): who a workspace's
// grants cover; idempotent, audited on a change, workspace-scoped.
func TestWorkspaceMembers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		resolves := func(sub string) error {
			_, err := st.ResolveSlot(ctx, "org_team", "user", sub, "t_litellm", "litellm_key")
			return err
		}
		audits := func(what string) int {
			var n int
			if err := store.DBOf(st).QueryRow(store.QOf(st, `SELECT COUNT(*) FROM audit_events WHERE org_id = 'org_team' AND workspace_id = 'ws_team'
				AND subject_kind = 'user' AND subject_id = 'usr_new' AND result = ?`), what).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		if err := resolves("usr_new"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("before: %v", err)
		}
		if c, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_new", "keyhole-cli", time.Now()); err != nil || !c || audits("member added") != 1 {
			t.Fatalf("add: %v, %v, %d", c, err, audits("member added"))
		}
		if err := resolves("usr_new"); err != nil {
			t.Fatalf("the new member doesn't resolve: %v", err)
		}
		if c, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_new", "keyhole-cli", time.Now()); err != nil || c || audits("member added") != 1 {
			t.Fatalf("a second add: %v, %v, %d", c, err, audits("member added"))
		}
		// The same id as an agent is another member: it doesn't resolve as the user.
		if c, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", "agent", "usr_new", "keyhole-cli", time.Now()); err != nil || !c {
			t.Fatalf("an agent with that id: %v, %v", c, err)
		}
		// A membership of the same user in another workspace of the org.
		if _, err := store.DBOf(st).Exec(`INSERT INTO workspaces (id, org_id, name, created_at, created_by_kind, created_by_id, updated_at)
			VALUES ('ws_side', 'org_team', 'side', 0, 'system', 'test', 0)`); err != nil {
			t.Fatal(err)
		}
		if c, err := st.AddWorkspaceMember(ctx, "org_team", "ws_side", "user", "usr_new", "keyhole-cli", time.Now()); err != nil || !c {
			t.Fatalf("add to ws_side: %v, %v", c, err)
		}
		if c, err := st.RemoveWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_new", "keyhole-cli", time.Now()); err != nil || !c || audits("member removed") != 1 {
			t.Fatalf("remove: %v, %v, %d", c, err, audits("member removed"))
		}
		if err := resolves("usr_new"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a removed member resolved: %v", err)
		}
		// Removing from ws_team left the user's ws_side membership.
		var side int
		if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id = 'ws_side' AND member_kind = 'user' AND member_id = 'usr_new'`).Scan(&side); err != nil || side != 1 {
			t.Fatalf("the ws_side membership: %d, %v", side, err)
		}
		// Removing the user left the agent with the same id.
		var agents int
		if err := store.DBOf(st).QueryRow(`SELECT COUNT(*) FROM workspace_memberships WHERE workspace_id = 'ws_team' AND member_kind = 'agent' AND member_id = 'usr_new'`).Scan(&agents); err != nil || agents != 1 {
			t.Fatalf("the agent membership: %d, %v", agents, err)
		}
		if c, err := st.RemoveWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_new", "keyhole-cli", time.Now()); err != nil || c || audits("member removed") != 1 {
			t.Fatalf("a second remove: %v, %v", c, err)
		}
		// The others are untouched.
		if err := resolves("usr_t1"); err != nil {
			t.Fatalf("usr_t1 lost its grant: %v", err)
		}
		for name, c := range map[string][4]string{
			"unknown workspace": {"org_team", "ws_nope", "user", "usr_x"},
			"another org's ws":  {"org_team", "ws_prod", "user", "usr_x"}, // ws_prod is org_acme's
			"ws in wrong org":   {"org_acme", "ws_team", "user", "usr_x"},
		} {
			if _, err := st.AddWorkspaceMember(ctx, c[0], c[1], c[2], c[3], "keyhole-cli", time.Now()); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s: %v, want ErrNotFound", name, err)
			}
			if _, err := st.RemoveWorkspaceMember(ctx, c[0], c[1], c[2], c[3], "keyhole-cli", time.Now()); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("%s (remove): %v, want ErrNotFound", name, err)
			}
		}
		for name, c := range map[string][2]string{
			"bad kind":   {"admin", "usr_x"},
			"empty id":   {"user", ""},
			"path in id": {"user", "usr/../x"},
			"long id":    {"user", strings.Repeat("a", 65)},
		} {
			if _, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", c[0], c[1], "keyhole-cli", time.Now()); !errors.Is(err, store.ErrBadMember) {
				t.Errorf("%s: %v, want ErrBadMember", name, err)
			}
		}
		// A deleted workspace takes no member changes.
		if _, err := store.DBOf(st).Exec(`UPDATE workspaces SET status = 'deleted' WHERE id = 'ws_team'`); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_y", "keyhole-cli", time.Now()); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a deleted workspace: %v", err)
		}
		// A deleted org takes none either, though its workspace is active.
		if _, err := store.DBOf(st).Exec(`UPDATE workspaces SET status = 'active' WHERE id = 'ws_team'`); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DBOf(st).Exec(`UPDATE orgs SET status = 'deleted' WHERE id = 'org_team'`); err != nil {
			t.Fatal(err)
		}
		if _, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_y", "keyhole-cli", time.Now()); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("a deleted org: %v", err)
		}
	})
}

// round 1: the id bounds and charset on both add and
// remove, the audit row's content, and that remove leaves a
// workspace_admin row alone.
func TestWorkspaceMemberIDsAuditAndRole(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := k1Store(t, b)
		if _, err := st.SeedDemo(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
		db := store.DBOf(st)
		count := func(q string) int {
			var n int
			if err := db.QueryRow(q).Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}
		const rows = `SELECT COUNT(*) FROM workspace_memberships`
		const audit = `SELECT COUNT(*) FROM audit_events`

		// Accepted: 1 and exactly 64 characters, and every allowed class.
		for _, id := range []string{"a", strings.Repeat("z", 64), "Ab-_09", "-", "_", "Z", "7"} {
			for _, kind := range []string{"user", "agent"} {
				if c, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", kind, id, "keyhole-cli", time.Now()); err != nil || !c {
					t.Errorf("add %s %q: %v, %v", kind, id, c, err)
				}
				if c, err := st.RemoveWorkspaceMember(ctx, "org_team", "ws_team", kind, id, "keyhole-cli", time.Now()); err != nil || !c {
					t.Errorf("remove %s %q: %v, %v", kind, id, c, err)
				}
			}
		}

		// Refused by add and by remove alike, with nothing written.
		before, beforeAudit := count(rows), count(audit)
		for name, c := range map[string][2]string{
			"bad kind":   {"admin", "usr_x"},
			"empty kind": {"", "usr_x"},
			"kind case":  {"User", "usr_x"},
			"empty id":   {"user", ""},
			"65 chars":   {"user", strings.Repeat("a", 65)},
			"space":      {"user", "a b"},
			"quote":      {"user", "a'b"},
			"dquote":     {"user", `a"b`},
			"multibyte":  {"user", "é"},
			"newline":    {"user", "a\nb"},
			"slash":      {"user", "a/b"},
			"dot":        {"user", "a.b"},
			"nul":        {"user", "a\x00b"},
		} {
			if _, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", c[0], c[1], "keyhole-cli", time.Now()); !errors.Is(err, store.ErrBadMember) {
				t.Errorf("add, %s: %v, want ErrBadMember", name, err)
			}
			if _, err := st.RemoveWorkspaceMember(ctx, "org_team", "ws_team", c[0], c[1], "keyhole-cli", time.Now()); !errors.Is(err, store.ErrBadMember) {
				t.Errorf("remove, %s: %v, want ErrBadMember", name, err)
			}
		}
		if count(rows) != before || count(audit) != beforeAudit {
			t.Fatalf("a refused change wrote: memberships %d → %d, audit %d → %d", before, count(rows), beforeAudit, count(audit))
		}

		// The audit row: who did it, what, when, and kept for the org's
		// retention (37 days here, so no default can pass).
		if _, err := db.Exec(`UPDATE orgs SET audit_retention_days = 37 WHERE id = 'org_team'`); err != nil {
			t.Fatal(err)
		}
		type row struct {
			app, typ, severity, actorKind, actorID, actorLabel, subjectKind, subjectID, object, ws string
			at, expire                                                                             int64
		}
		read := func(result string) row {
			var r row
			if err := db.QueryRow(store.QOf(st, `SELECT app, type, severity, actor_kind, actor_id, actor_label, subject_kind, subject_id,
				object, workspace_id, at, expire_at FROM audit_events WHERE org_id = 'org_team' AND subject_id = 'usr_audit' AND result = ?`), result).
				Scan(&r.app, &r.typ, &r.severity, &r.actorKind, &r.actorID, &r.actorLabel, &r.subjectKind, &r.subjectID, &r.object, &r.ws, &r.at, &r.expire); err != nil {
				t.Fatalf("%s: %v", result, err)
			}
			return r
		}
		added := time.UnixMilli(1793000000123)
		if c, err := st.AddWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_audit", "op-7", added); err != nil || !c {
			t.Fatalf("add: %v, %v", c, err)
		}
		removed := added.Add(time.Hour)
		if c, err := st.RemoveWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_audit", "op-8", removed); err != nil || !c {
			t.Fatalf("remove: %v, %v", c, err)
		}
		for _, c := range []struct {
			result, actor string
			when          time.Time
		}{{"member added", "op-7", added}, {"member removed", "op-8", removed}} {
			want := row{"keyhole", "admin", "ok", "system", c.actor, c.actor, "user", "usr_audit", "user usr_audit in workspace ws_team", "ws_team",
				c.when.UnixMilli(), c.when.AddDate(0, 0, 37).UnixMilli()}
			if got := read(c.result); got != want {
				t.Errorf("%s:\n got %+v\nwant %+v", c.result, got, want)
			}
		}

		// remove takes out plain members only: a workspace_admin row is
		// vbcdx-control's.
		if _, err := db.Exec(`INSERT INTO workspace_memberships (id, org_id, workspace_id, member_kind, member_id, role, added_at, added_by_kind, added_by_id, updated_at)
			VALUES ('wm_boss', 'org_team', 'ws_team', 'user', 'usr_boss', 'workspace_admin', 0, 'system', 'control', 0)`); err != nil {
			t.Fatal(err)
		}
		beforeAudit = count(audit)
		if c, err := st.RemoveWorkspaceMember(ctx, "org_team", "ws_team", "user", "usr_boss", "keyhole-cli", time.Now()); err != nil || c {
			t.Fatalf("remove of a workspace_admin: %v, %v; want not changed", c, err)
		}
		if n := count(`SELECT COUNT(*) FROM workspace_memberships WHERE id = 'wm_boss' AND role = 'workspace_admin'`); n != 1 || count(audit) != beforeAudit {
			t.Fatalf("the workspace_admin row: %d, audit %d → %d", n, beforeAudit, count(audit))
		}
	})
}
