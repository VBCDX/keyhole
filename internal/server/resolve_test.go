package server

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/assertion"
	"github.com/VBCDX/keyhole/internal/envelope"
	"github.com/VBCDX/keyhole/internal/seal"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

var rb64 = base64.RawURLEncoding

const resolveCanary = "sk-CANARY-resolve-6d2f"

type resolveRig struct {
	rv    *Resolver
	kek   *envelope.KEK // the KEK the rig's Local values are sealed with
	h     http.Handler
	st    *store.Store
	raw   *sql.DB // the same SQLite file, opened directly
	sk    ed25519.PrivateKey
	logs  *bytes.Buffer
	now   time.Time
	jtiN  int
	audit func() string
}

func newResolveRig(t *testing.T) *resolveRig {
	t.Helper()
	ctx := context.Background()
	b := storetest.Backends(t)[0]
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
		"id": "wm_usr_t1", "org_id": "org_team", "workspace_id": "ws_team", "member_kind": "user",
		"member_id": "usr_t1", "role": "member", "added_at": "@now", "added_by_kind": "system",
		"added_by_id": "t", "updated_at": "@now", "rev": json.Number("0"),
	}}})
	if _, err := st.LoadFixture(ctx, fx, time.Now()); err != nil {
		t.Fatal(err)
	}
	kekPath := filepath.Join(t.TempDir(), "kek")
	if err := envelope.GenerateKEK(kekPath); err != nil {
		t.Fatal(err)
	}
	kek, err := envelope.LoadKEK(kekPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutLocalValue(ctx, kek, "org_team", "k_litellm_test", []byte(resolveCanary), "t", time.Now()); err != nil {
		t.Fatal(err)
	}
	pub, sk, _ := ed25519.GenerateKey(rand.Reader)
	jwks, _ := json.Marshal(map[string]any{"keys": []any{map[string]any{"kty": "OKP", "crv": "Ed25519", "kid": "k1", "x": rb64.EncodeToString(pub)}}})
	logs := &bytes.Buffer{}
	now := time.Unix(1_800_000_000, 0)
	stores, err := store.NewStoreTypes(st.LocalType(kek))
	if err != nil {
		t.Fatal(err)
	}
	rv := &Resolver{Store: st, Stores: stores, Verifier: assertion.New("keyhole", jwks, nil),
		Log: slog.New(slog.NewTextHandler(logs, nil)), Now: func() time.Time { return now }}
	raw, err := sql.Open("sqlite", strings.TrimPrefix(b.URL, "sqlite://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	r := &resolveRig{rv: rv, kek: kek, h: rv.Handler(), st: st, raw: raw, sk: sk, logs: logs, now: now}
	r.audit = func() string {
		var out strings.Builder
		rows, err := raw.Query(`SELECT type, severity, actor_id, actor_via_id, object, result, COALESCE(correlation_id, '') FROM audit_events WHERE app = 'keyhole' AND object LIKE 'tool %'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var a, b, c, d, e, f, g string
			if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g); err != nil {
				t.Fatal(err)
			}
			out.WriteString(strings.Join([]string{a, b, c, d, e, f, g}, "|") + "\n")
		}
		return out.String()
	}
	return r
}

func (r *resolveRig) assertion(edit func(p map[string]any)) string {
	r.jtiN++
	p := map[string]any{"iss": assertion.Issuer, "aud": "keyhole", "iat": r.now.Unix(), "exp": r.now.Unix() + 60,
		"jti": "req_" + string(rune('a'+r.jtiN)), "sub": "usr_t1", "kind": "user", "sidecar": "sc_790",
		"org": map[string]any{"id": "org_team", "role": "member"}}
	if edit != nil {
		edit(p)
	}
	h, _ := json.Marshal(map[string]any{"alg": "EdDSA", "typ": assertion.Typ, "kid": "k1"})
	pb, _ := json.Marshal(p)
	in := rb64.EncodeToString(h) + "." + rb64.EncodeToString(pb)
	return in + "." + rb64.EncodeToString(ed25519.Sign(r.sk, []byte(in)))
}

func ephKey(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k, rb64.EncodeToString(k.PublicKey().Bytes())
}

func (r *resolveRig) post(path, jws string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if jws != "" {
		req.Header.Set(AssertionHeader, jws)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	return w
}

func TestResolveSealsTheValueToTheSidecar(t *testing.T) {
	r := newResolveRig(t)
	sk, pub := ephKey(t)
	jws := r.assertion(nil)
	w := r.post("/orgs/org_team/resolve", jws, map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "CANARY") {
		t.Fatal("the reply holds the value in clear")
	}
	var rep resolveReply
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.TTL != 300 {
		t.Fatalf("ttl = %d", rep.TTL)
	}
	// The granted version's definition, for the sidecar's call (P4a).
	want := replyTool{Version: 1, BaseURL: "https://litellm.example.invalid", TimeoutMs: 60000,
		Actions:      []replyAction{{"GET", "/v1/models"}, {"POST", "/v1/chat/completions"}},
		Slot:         replySlot{Location: "header", Field: "Authorization", Scheme: "Bearer"},
		PrivateAllow: []string{}}
	if got, _ := json.Marshal(rep.Tool); string(got) != string(must(json.Marshal(want))) {
		t.Fatalf("tool = %s", got)
	}
	// No list is an empty one, never null or absent.
	if !strings.Contains(w.Body.String(), `"private_allow":[]`) {
		t.Fatalf("reply has no empty private_allow: %s", w.Body)
	}
	jti := "req_" + string(rune('a'+1))
	// The reply names the jti the AAD binds: the sidecar can't learn it otherwise.
	if rep.JTI != jti {
		t.Fatalf("reply jti = %q, want %q", rep.JTI, jti)
	}
	got, err := seal.Open(sk, rep.Sealed, seal.AAD(rep.JTI, "t_litellm", "litellm_key", rep.Tool.sealDef()))
	if err != nil || string(got) != resolveCanary {
		t.Fatalf("the sidecar opens %q, %v", got, err)
	}
	if a := r.audit(); !strings.Contains(a, "access|ok|usr_t1|sc_790|tool t_litellm slot litellm_key|resolved|"+jti) {
		t.Fatalf("audit = %q", a)
	}
	// The same assertion again is a replay.
	if w := r.post("/orgs/org_team/resolve", jws, map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub}); w.Code != http.StatusUnauthorized {
		t.Fatalf("a replay: %d", w.Code)
	}
}

func TestResolveRefusals(t *testing.T) {
	_, pub := ephKey(t)
	body := map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub}
	with := func(k, v string) map[string]string {
		b := map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub}
		b[k] = v
		return b
	}
	type tc struct {
		path   string
		edit   func(p map[string]any)
		noJWS  bool
		body   any
		mutate string
		status int
		result string // the audit result, "" for no row
	}
	cases := map[string]tc{
		"no assertion": {noJWS: true, status: 401},
		"for dispatch": {edit: func(p map[string]any) { p["aud"] = "dispatch" }, status: 401},
		"expired":      {edit: func(p map[string]any) { p["iat"] = 1_799_999_000; p["exp"] = 1_799_999_060 }, status: 401},
		"no sidecar":   {edit: func(p map[string]any) { delete(p, "sidecar") }, status: 403, result: "refused: no sidecar"},
		"no org claim": {edit: func(p map[string]any) { delete(p, "org") }, status: 403, result: "refused: org not asserted"},
		// org_acme isn't in this database: refused, and no row (an audit row needs its org).
		"another org":    {path: "/orgs/org_acme/resolve", status: 403},
		"not a member":   {edit: func(p map[string]any) { p["sub"] = "usr_t2" }, status: 404, result: "refused: no grant"},
		"unknown tool":   {body: with("tool", "t_nope"), status: 404, result: "refused: no grant"},
		"unknown slot":   {body: with("slot", "nope"), status: 404, result: "refused: no grant"},
		"low-order key":  {body: with("eph_pub", rb64.EncodeToString(make([]byte, 32))), status: 400, result: "refused: bad eph_pub"},
		"short key":      {body: with("eph_pub", "AAAA"), status: 400, result: "refused: bad eph_pub"},
		"missing field":  {body: map[string]string{"tool": "t_litellm", "slot": "litellm_key"}, status: 400},
		"extra field":    {body: map[string]any{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub, "value": "x"}, status: 400},
		"grant disabled": {mutate: `UPDATE grants SET enabled = FALSE`, status: 404, result: "refused: no grant"},
		"no value yet":   {mutate: `DELETE FROM local_key_values`, status: 404, result: "refused: no value"},
		// The grant's slot isn't in the tool version it uses.
		"slot not in tool": {mutate: `UPDATE grant_slots SET slot = 'other_key'`, body: with("slot", "other_key"), status: 404, result: "refused: slot not in the tool version"},
		"ambiguous grants": {mutate: ambiguousSQL, status: 409, result: "refused: grants name different keys"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := newResolveRig(t)
			if c.mutate != "" {
				if _, err := r.raw.Exec(c.mutate); err != nil {
					t.Fatal(err)
				}
			}
			jws := ""
			if !c.noJWS {
				jws = r.assertion(c.edit)
			}
			path, b := c.path, c.body
			if path == "" {
				path = "/orgs/org_team/resolve"
			}
			if b == nil {
				b = body
			}
			w := r.post(path, jws, b)
			if w.Code != c.status {
				t.Fatalf("status %d, want %d: %s", w.Code, c.status, w.Body)
			}
			if c.noJWS && !strings.Contains(w.Body.String(), "a Vbcdx-Assertion is required") {
				t.Fatalf("no assertion: %s", w.Body)
			}
			a := r.audit()
			if c.result == "" && a != "" {
				t.Fatalf("an audit row for a refusal before the caller is known: %q", a)
			}
			if c.result != "" && !strings.Contains(a, "blocked|blocked|") || c.result != "" && !strings.Contains(a, "|"+c.result+"|") {
				t.Fatalf("audit = %q, want a blocked row %q", a, c.result)
			}
			for _, s := range []string{w.Body.String(), r.logs.String(), a} {
				if strings.Contains(s, "CANARY") {
					t.Fatalf("the value leaked: %q", s)
				}
			}
		})
	}
}

// A second workspace grants the same tool and slot to usr_t1 with another key.
const ambiguousSQL = `INSERT INTO workspaces (id, org_id, name, status, created_at, created_by_kind, created_by_id, updated_at, rev)
	VALUES ('ws_two', 'org_team', 'Two', 'active', 1, 'system', 't', 1, 0);
INSERT INTO workspace_memberships (id, org_id, workspace_id, member_kind, member_id, role, added_at, added_by_kind, added_by_id, updated_at, rev)
	VALUES ('wm_two', 'org_team', 'ws_two', 'user', 'usr_t1', 'member', 1, 'system', 't', 1, 0);
INSERT INTO keys (id, org_id, name, store_id, provider, pinned, notes, status, created_at, created_by_kind, created_by_id, updated_at, rev)
	VALUES ('k_other', 'org_team', 'other', 'st_team_local', 'local', FALSE, '', 'active', 1, 'system', 't', 1, 0);
INSERT INTO grants (id, org_id, workspace_id, tool_id, enabled, per_minute, granted_at, granted_by_kind, granted_by_id, updated_at, rev)
	VALUES ('gr_two', 'org_team', 'ws_two', 't_litellm', TRUE, 30, 1, 'system', 't', 1, 0);
INSERT INTO grant_slots (grant_id, org_id, slot, key_id) VALUES ('gr_two', 'org_team', 'litellm_key', 'k_other')`

func TestResolveRouteOnly(t *testing.T) {
	r := newResolveRig(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/orgs/org_team/resolve"},
		{http.MethodPost, "/orgs/org_team"},
		{http.MethodPost, "/api/v1/orgs"},
		{http.MethodGet, "/healthz"},
	} {
		req := httptest.NewRequest(c.method, c.path, nil)
		w := httptest.NewRecorder()
		r.h.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound && w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d; the forward listener serves only resolve", c.method, c.path, w.Code)
		}
		_, _ = io.Copy(io.Discard, w.Body)
	}
}

// TestResolveFollowsTheGrantSwitch: AC 3, through the handler.
func TestResolveFollowsTheGrantSwitch(t *testing.T) {
	r := newResolveRig(t)
	_, pub := ephKey(t)
	body := map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub}
	post := func() int { return r.post("/orgs/org_team/resolve", r.assertion(nil), body).Code }
	ctx := context.Background()
	if c := post(); c != http.StatusOK {
		t.Fatalf("before: %d", c)
	}
	if _, err := r.st.SetGrantEnabled(ctx, "org_team", "gr_team_litellm", false, "keyhole-cli", time.Now()); err != nil {
		t.Fatal(err)
	}
	w := r.post("/orgs/org_team/resolve", r.assertion(nil), body)
	if w.Code != http.StatusNotFound || !strings.Contains(r.audit(), "|refused: no grant|") {
		t.Fatalf("disabled: %d %s; audit %q", w.Code, w.Body, r.audit())
	}
	if _, err := r.st.SetGrantEnabled(ctx, "org_team", "gr_team_litellm", true, "keyhole-cli", time.Now()); err != nil {
		t.Fatal(err)
	}
	if c := post(); c != http.StatusOK {
		t.Fatalf("re-enabled: %d", c)
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

// The private addresses an admin allowed travel with the
// granted tool version.
func TestResolveCarriesTheToolsPrivateAllow(t *testing.T) {
	r := newResolveRig(t)
	if _, err := r.st.SetToolPrivateAllow(context.Background(), "org_team", "t_litellm", []string{"192.168.10.90"}, "op", r.now); err != nil {
		t.Fatal(err)
	}
	_, pub := ephKey(t)
	w := r.post("/orgs/org_team/resolve", r.assertion(nil), map[string]string{"tool": "t_litellm", "slot": "litellm_key", "eph_pub": pub})
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var rep resolveReply
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Tool.Version != 2 || len(rep.Tool.PrivateAllow) != 1 || rep.Tool.PrivateAllow[0] != "192.168.10.90/32" {
		t.Fatalf("tool = %+v", rep.Tool)
	}
}
