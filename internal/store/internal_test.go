package store

import (
	"reflect"
	"testing"
)

func TestRebindForPostgres(t *testing.T) {
	pg := &Store{dialect: Postgres}
	if got := pg.q("SELECT a FROM t WHERE b = ? AND c = ?"); got != "SELECT a FROM t WHERE b = $1 AND c = $2" {
		t.Fatalf("postgres rebind = %q", got)
	}
	lite := &Store{dialect: SQLite}
	if got := lite.q("WHERE b = ?"); got != "WHERE b = ?" {
		t.Fatalf("sqlite must keep ?: %q", got)
	}
}

func TestStatementsSplit(t *testing.T) {
	src := "-- a comment; with a semicolon\nCREATE TABLE a (x TEXT);\n\nCREATE INDEX i ON a (x);\n"
	want := []string{"CREATE TABLE a (x TEXT)", "CREATE INDEX i ON a (x)"}
	if got := statements(src); !reflect.DeepEqual(got, want) {
		t.Fatalf("statements = %q, want %q", got, want)
	}
}

func TestMigrationsLoad(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil || len(ms) == 0 || ms[0].version != 1 {
		t.Fatalf("loadMigrations = %+v, %v", ms, err)
	}
}

// The grants and slots reads are separate statements, so a slot can arrive
// for a grant committed between them. The merge must skip it, not index the
// map blind: the blind index panicked before this guard.
func TestMergeGrantSlotsSkipsUnknownGrant(t *testing.T) {
	k1, k2 := "k1", "k2"
	grants := []Grant{
		{ID: "g1", SlotMap: map[string]*string{}},
		{ID: "g2", SlotMap: map[string]*string{}},
	}
	slots := []grantSlot{
		{grantID: "g1", name: "api_key", keyID: k1},
		{grantID: "g2", name: "api_key"},               // unfilled slot
		{grantID: "g_new", name: "api_key", keyID: k2}, // committed between the two reads
	}
	mergeGrantSlots(grants, slots)
	if v := grants[0].SlotMap["api_key"]; v == nil || *v != k1 {
		t.Errorf("g1 SlotMap = %v; want api_key=%s", grants[0].SlotMap, k1)
	}
	if v, ok := grants[1].SlotMap["api_key"]; !ok || v != nil {
		t.Errorf("g2 SlotMap = %v; want the unfilled slot recorded as nil", grants[1].SlotMap)
	}
}

// A Grant with no map yet must be filled, not panic: the helper is
// package-visible and tests build Grants by hand.
func TestMergeGrantSlotsToleratesNilSlotMap(t *testing.T) {
	grants := []Grant{{ID: "g1"}} // zero SlotMap
	slots := []grantSlot{{grantID: "g1", name: "api_key", keyID: "k1"}}
	mergeGrantSlots(grants, slots)
	if v := grants[0].SlotMap["api_key"]; v == nil || *v != "k1" {
		t.Fatalf("SlotMap = %v; want api_key=k1", grants[0].SlotMap)
	}
}

// Postgres scopes advisory locks to one database, so two applications collide
// only when they share one; the ID must be Keyhole's own, and the "keyhole" in
// the comment must match the value. Dispatch owns 0x646973706174 ("dispat"); a
// copy-paste of its lock ID fails here.
func TestMigrationLockIDMatchesItsName(t *testing.T) {
	var want uint64
	for _, c := range "keyhole" {
		want = want<<8 | uint64(c)
	}
	if migrationLockID != want {
		t.Fatalf("migrationLockID = %#x, want %#x (ASCII \"keyhole\")", migrationLockID, want)
	}
}
