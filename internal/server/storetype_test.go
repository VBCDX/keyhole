package server

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/VBCDX/keyhole/internal/seal"
	"github.com/VBCDX/keyhole/internal/store"
)

// fakeType is a store type that isn't the Local store: what a third party
// would write to the contract (docs/stores.md).
type fakeType struct {
	kind, value string
	mu          sync.Mutex
	asked       []string
}

func (f *fakeType) Kind() string { return f.kind }

func (f *fakeType) Value(_ context.Context, orgID, keyID string) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, orgID+"/"+keyID)
	return []byte(f.value), 7, nil
}

// AC2 + AC4: resolve finds a key's store type through the
// registry and nothing else. A key whose provider is "fake" resolves to the
// fake type's value, sealed the same way, through the real resolve path.
func TestResolveGoesThroughTheStoreTypeRegistry(t *testing.T) {
	r := newResolveRig(t)
	if _, err := r.raw.Exec(`UPDATE keys SET provider = 'fake' WHERE id = 'k_litellm_test'`); err != nil {
		t.Fatal(err)
	}
	fake := &fakeType{kind: "fake", value: "FAKE-STORE-VALUE"}
	stores, err := store.NewStoreTypes(r.st.LocalType(r.kek), fake)
	if err != nil {
		t.Fatal(err)
	}
	r.rv.Stores = stores
	sk, pub := ephKey(t)
	w := r.post("/orgs/org_team/resolve", r.assertion(nil), map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve through the fake type: %d %s", w.Code, w.Body)
	}
	rep := decodeReply(t, w.Body.Bytes())
	got, err := seal.Open(sk, rep.Sealed, seal.AAD(rep.JTI, "t_litellm", "litellm_key", rep.Tool.sealDef()))
	if err != nil || string(got) != "FAKE-STORE-VALUE" {
		t.Fatalf("the sidecar opens %q, %v; want the fake type's value", got, err)
	}
	if strings.Join(fake.asked, ",") != "org_team/k_litellm_test" {
		t.Fatalf("the fake type was asked %v", fake.asked)
	}
	if strings.Contains(w.Body.String(), "FAKE-STORE-VALUE") || strings.Contains(r.logs.String(), "FAKE-STORE-VALUE") {
		t.Fatal("the value appears in the reply body or the log")
	}
}

// AC2: a key whose provider no registered type serves fails
// closed: the same 404 as no value, the reason in the audit, no value.
func TestResolveRefusesAKeyNoStoreTypeServes(t *testing.T) {
	r := newResolveRig(t)
	if _, err := r.raw.Exec(`UPDATE keys SET provider = 'openbao' WHERE id = 'k_litellm_test'`); err != nil {
		t.Fatal(err)
	}
	_, pub := ephKey(t)
	body := map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub}
	w := r.post("/orgs/org_team/resolve", r.assertion(nil), body)
	if w.Code != http.StatusNotFound {
		t.Fatalf("an unserved provider: %d %s", w.Code, w.Body)
	}
	// No oracle: the reply is byte-equal,
	// body and headers, to a key with no value, on a rig of its own.
	nv := newResolveRig(t)
	if _, err := nv.raw.Exec(`DELETE FROM local_key_values`); err != nil {
		t.Fatal(err)
	}
	want := nv.post("/orgs/org_team/resolve", nv.assertion(nil), body)
	if want.Code != w.Code || want.Body.String() != w.Body.String() || !reflect.DeepEqual(want.Header(), w.Header()) {
		t.Fatalf("an unserved provider answers %d %v %q; no value answers %d %v %q",
			w.Code, w.Header(), w.Body, want.Code, want.Header(), want.Body)
	}
	if !strings.Contains(nv.audit(), "|refused: no value|") {
		t.Fatalf("the baseline isn't the no-value case: %q", nv.audit())
	}
	if !strings.Contains(r.audit(), "|refused: no store type|") {
		t.Fatalf("audit = %q", r.audit())
	}
	if strings.Contains(w.Body.String(), resolveCanary) || strings.Contains(r.logs.String(), resolveCanary) {
		t.Fatal("a value leaked")
	}
}

// With no 'local' filter, a grant naming a
// non-local key for the same slot is visible, so two grants naming
// different keys are ambiguous and refused (409), where before the
// non-local grant was invisible and the Local key resolved. The safe way.
func TestAGrantOnAnotherStoreTypeMakesTheSlotAmbiguous(t *testing.T) {
	r := newResolveRig(t)
	for _, q := range []string{
		`CREATE TEMP TABLE k2 AS SELECT * FROM keys WHERE id = 'k_litellm_test'`,
		`UPDATE k2 SET id = 'k_bao_other', provider = 'openbao', name = 'bao-other'`,
		`INSERT INTO keys SELECT * FROM k2`,
		// A second workspace the same caller is in, with its own grant.
		`CREATE TEMP TABLE w2 AS SELECT * FROM workspaces WHERE id = (SELECT workspace_id FROM grants WHERE id = 'gr_team_litellm')`,
		`UPDATE w2 SET id = 'ws_team_bao', name = 'bao workspace'`,
		`INSERT INTO workspaces SELECT * FROM w2`,
		`CREATE TEMP TABLE m2 AS SELECT * FROM workspace_memberships WHERE workspace_id = (SELECT workspace_id FROM grants WHERE id = 'gr_team_litellm')`,
		`UPDATE m2 SET id = 'wm_bao_' || id, workspace_id = 'ws_team_bao'`,
		`INSERT INTO workspace_memberships SELECT * FROM m2`,
		`CREATE TEMP TABLE g2 AS SELECT * FROM grants WHERE id = 'gr_team_litellm'`,
		`UPDATE g2 SET id = 'gr_team_bao', workspace_id = 'ws_team_bao'`,
		`INSERT INTO grants SELECT * FROM g2`,
		`CREATE TEMP TABLE s2 AS SELECT * FROM grant_slots WHERE grant_id = 'gr_team_litellm'`,
		`UPDATE s2 SET grant_id = 'gr_team_bao', key_id = 'k_bao_other'`,
		`INSERT INTO grant_slots SELECT * FROM s2`,
	} {
		if _, err := r.raw.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_, pub := ephKey(t)
	w := r.post("/orgs/org_team/resolve", r.assertion(nil), map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub})
	if w.Code != http.StatusConflict || !strings.Contains(r.audit(), "|refused: grants name different keys|") {
		t.Fatalf("a Local and a non-local grant for one slot: %d %s; audit %q", w.Code, w.Body, r.audit())
	}
	if strings.Contains(w.Body.String(), resolveCanary) {
		t.Fatal("the Local value leaked")
	}
}

// The Keyhole half: the reply's sealed value is bound to the
// tool definition the same reply carries. The sidecar opens it with that
// definition and no other: swapping the destination, or any egress field,
// makes it fail to open.
func TestTheSealIsBoundToTheRepliedToolDefinition(t *testing.T) {
	r := newResolveRig(t)
	sk, pub := ephKey(t)
	w := r.post("/orgs/org_team/resolve", r.assertion(nil), map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub})
	if w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body)
	}
	rep := decodeReply(t, w.Body.Bytes())
	if got, err := seal.Open(sk, rep.Sealed, seal.AAD(rep.JTI, "t_litellm", "litellm_key", rep.Tool.sealDef())); err != nil || string(got) != resolveCanary {
		t.Fatalf("the definition the reply carries doesn't open it: %q %v", got, err)
	}
	for name, f := range map[string]func(*replyTool){
		"base_url":      func(x *replyTool) { x.BaseURL = "https://evil.example.invalid" },
		"version":       func(x *replyTool) { x.Version++ },
		"timeout":       func(x *replyTool) { x.TimeoutMs++ },
		"slot field":    func(x *replyTool) { x.Slot.Field = "X-Other" },
		"private_allow": func(x *replyTool) { x.PrivateAllow = append(x.PrivateAllow, "10.0.0.0/8") },
		"an action":     func(x *replyTool) { x.Actions = append(x.Actions, replyAction{"DELETE", "/"}) },
	} {
		def := rep.Tool
		def.Actions = append([]replyAction{}, rep.Tool.Actions...)
		def.PrivateAllow = append([]string{}, rep.Tool.PrivateAllow...)
		f(&def)
		if got, err := seal.Open(sk, rep.Sealed, seal.AAD(rep.JTI, "t_litellm", "litellm_key", def.sealDef())); err == nil {
			t.Errorf("%s changed, and the value still opened (%q)", name, got)
		}
	}
}

func decodeReply(t *testing.T, body []byte) resolveReply {
	t.Helper()
	var rep resolveReply
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	return rep
}
