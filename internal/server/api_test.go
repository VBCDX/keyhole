package server

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// apiFixture adds, on top of the demo data, a store in Northwind Labs and
// three local accounts: one that joins Acme Corp (the demo organization)
// on first sign-in, one that is only in Northwind Labs, and one that is
// suspended in Acme Corp.
func apiFixture(t *testing.T, b storetest.Backend) (h *harness, acme, northwind, suspended *http.Client) {
	t.Helper()
	ctx := context.Background()
	h = newHarness(t, b, config.AuthLocal, nil)
	h.cfg.SeedDemoData = true
	now := time.Now()
	if _, err := h.st.SeedDemo(ctx, now); err != nil {
		t.Fatal(err)
	}
	nw := h.createUser("nw@example.com", testPassword)
	susp := h.createUser("suspended@example.com", testPassword)
	f := store.Fixture{Tables: []store.FixtureTable{
		{Table: "org_memberships", Rows: []map[string]any{
			{"id": "om_nw_test", "org_id": "org_nw", "user_id": nw.ID, "role": "Owner", "status": "active",
				"joined_at": "@now-1d", "created_at": "@now-1d", "updated_at": "@now-1d", "rev": json.Number("0")},
			{"id": "om_acme_susp", "org_id": "org_acme", "user_id": susp.ID, "role": "user", "status": "suspended",
				"suspended_from": "active", "suspension_id": "susp_jo",
				"joined_at": "@now-1d", "created_at": "@now-1d", "updated_at": "@now-1d", "rev": json.Number("1")},
		}},
		{Table: "stores", Rows: []map[string]any{
			{"id": "st_nw", "org_id": "org_nw", "name": "Northwind local", "provider": "local", "status": "active",
				"route_mode": "internal", "network_exposure": "unknown", "health_status": "healthy",
				"health_checked_at": "@now-1m", "cache_default_ttl_seconds": json.Number("300"), "created_at": "@now-1d",
				"created_by_kind": "system", "created_by_id": "org_setup", "updated_at": "@now-1d", "rev": json.Number("0")},
		}},
	}}
	if _, err := h.st.LoadFixture(ctx, f, now); err != nil {
		t.Fatal(err)
	}
	h.createUser("acme@example.com", testPassword)

	signIn := func(email string) *http.Client {
		c := h.client()
		if res := h.login(c, email, testPassword); res.StatusCode != http.StatusOK {
			t.Fatalf("login %s = %d", email, res.StatusCode)
		}
		return c
	}
	return h, signIn("acme@example.com"), signIn("nw@example.com"), signIn("suspended@example.com")
}

func (h *harness) get(c *http.Client, path string) (*http.Response, string) {
	h.t.Helper()
	res := h.do(c, http.MethodGet, path, nil, nil)
	return res, readAll(h.t, res)
}

func TestAPIRequiresSession(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h, _, _, _ := apiFixture(t, b)
		for _, p := range []string{
			"/api/v1/orgs",
			"/api/v1/orgs/org_acme",
			"/api/v1/orgs/org_acme/stores",
			"/api/v1/orgs/org_acme/stores/st_bao",
			"/api/v1/orgs/org_nope/stores/st_nope",
		} {
			res, body := h.get(h.client(), p)
			if res.StatusCode != http.StatusUnauthorized || !contains(body, `"unauthenticated"`) {
				t.Errorf("GET %s without a session = %d %s; want 401", p, res.StatusCode, body)
			}
		}
	})
}

func TestAPIOrgs(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h, acme, nw, susp := apiFixture(t, b)

		for _, tc := range []struct {
			c    *http.Client
			want []orgJSON
		}{
			{acme, []orgJSON{{ID: "org_acme", Name: "Acme Corp", Role: "Owner", Status: "active"}}},
			{nw, []orgJSON{{ID: "org_nw", Name: "Northwind Labs", Role: "Owner", Status: "active"}}},
			{susp, []orgJSON{{ID: "org_acme", Name: "Acme Corp", Role: "user", Status: "suspended"}}},
		} {
			res := h.do(tc.c, http.MethodGet, "/api/v1/orgs", nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("orgs = %d", res.StatusCode)
			}
			got := decode[struct{ Orgs []orgJSON }](t, res).Orgs
			for i := range got {
				if got[i].JoinedAt == nil {
					t.Errorf("org %s: joined_at missing", got[i].ID)
				}
				got[i].JoinedAt = nil
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("orgs = %+v, want %+v", got, tc.want)
			}
		}

		res := h.do(acme, http.MethodGet, "/api/v1/orgs/org_acme", nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("own org = %d", res.StatusCode)
		}
		if o := decode[orgJSON](t, res); o.Name != "Acme Corp" || o.Role != "Owner" {
			t.Errorf("own org = %+v", o)
		}
	})
}

// Another organization's ID, an ID that does not exist, and an
// organization the caller is suspended from all answer the same 404.
func TestAPIOrgScoping(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h, acme, nw, susp := apiFixture(t, b)
		const notFoundOrg = `{"error":"not_found","message":"no such organization"}` + "\n"
		const notFoundStore = `{"error":"not_found","message":"no such store"}` + "\n"

		for _, tc := range []struct {
			name string
			c    *http.Client
			path string
			want string
		}{
			{"other org", acme, "/api/v1/orgs/org_nw", notFoundOrg},
			{"other org's stores", acme, "/api/v1/orgs/org_nw/stores", notFoundOrg},
			{"other org's store", acme, "/api/v1/orgs/org_nw/stores/st_nw", notFoundOrg},
			{"missing org", acme, "/api/v1/orgs/org_nope/stores", notFoundOrg},
			{"other org, other way", nw, "/api/v1/orgs/org_acme/stores", notFoundOrg},
			{"other org's store, other way", nw, "/api/v1/orgs/org_acme/stores/st_bao", notFoundOrg},
			{"other org's store under own org", nw, "/api/v1/orgs/org_nw/stores/st_bao", notFoundStore},
			{"own store under own org, other way", acme, "/api/v1/orgs/org_acme/stores/st_nw", notFoundStore},
			{"missing store", acme, "/api/v1/orgs/org_acme/stores/st_nope", notFoundStore},
			{"suspended org", susp, "/api/v1/orgs/org_acme", notFoundOrg},
			{"suspended org's stores", susp, "/api/v1/orgs/org_acme/stores", notFoundOrg},
			{"suspended org's store", susp, "/api/v1/orgs/org_acme/stores/st_local", notFoundOrg},
		} {
			res, body := h.get(tc.c, tc.path)
			if res.StatusCode != http.StatusNotFound || body != tc.want {
				t.Errorf("%s: GET %s = %d %s; want 404 %s", tc.name, tc.path, res.StatusCode, body, tc.want)
			}
		}

		// The same caller reads its own organization's data.
		if res, body := h.get(nw, "/api/v1/orgs/org_nw/stores/st_nw"); res.StatusCode != http.StatusOK || !contains(body, "Northwind local") {
			t.Errorf("own store = %d %s", res.StatusCode, body)
		}
		res := h.do(nw, http.MethodGet, "/api/v1/orgs/org_nw/stores", nil, nil)
		got := decode[struct{ Stores []storeJSON }](t, res).Stores
		if len(got) != 1 || got[0].ID != "st_nw" {
			t.Errorf("northwind stores = %+v; want only st_nw", got)
		}
	})
}

func TestAPIStores(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		h, acme, _, _ := apiFixture(t, b)

		res, body := h.get(acme, "/api/v1/orgs/org_acme/stores")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("stores = %d %s", res.StatusCode, body)
		}
		noSecrets(t, body)
		list := decode[struct {
			Stores            []storeJSON `json:"stores"`
			SourceRemovedKeys []keyJSON   `json:"source_removed_keys"`
		}](t, h.do(acme, http.MethodGet, "/api/v1/orgs/org_acme/stores", nil, nil))

		// The removed "Old vault" is not listed.
		if names := storeNames(list.Stores); !slices.Equal(names, []string{"Local store", "Payments vault"}) {
			t.Fatalf("store names = %v", names)
		}
		local, bao := list.Stores[0], list.Stores[1]
		if local.Provider != "local" || local.RouteMode != "internal" || local.KeyCount != 3 || local.RouteConnector != nil {
			t.Errorf("local store = %+v", local)
		}
		if bao.Provider != "openbao" || bao.RouteMode != "connector" || bao.KeyCount != 4 ||
			bao.RouteConnector == nil || bao.RouteConnector.Name != "edge-01" || bao.Health.Status != "healthy" {
			t.Errorf("openbao store = %+v (connector %+v)", bao, bao.RouteConnector)
		}
		if len(list.SourceRemovedKeys) != 1 || list.SourceRemovedKeys[0].Name != "billing/api_key" {
			t.Errorf("source-removed keys = %+v", list.SourceRemovedKeys)
		}

		res, body = h.get(acme, "/api/v1/orgs/org_acme/stores/st_bao")
		if res.StatusCode != http.StatusOK {
			t.Fatalf("store detail = %d %s", res.StatusCode, body)
		}
		noSecrets(t, body)
		d := decode[storeDetailJSON](t, h.do(acme, http.MethodGet, "/api/v1/orgs/org_acme/stores/st_bao", nil, nil))
		if d.Address != "https://bao.acme.internal:8200" || d.KVMount != "secret" || d.KVPathPrefix != "payments/" ||
			d.AuthMethod != "approle" || d.CABundleName != "vault-ca.pem" || d.CacheDefaultTTLSeconds != 60 {
			t.Errorf("openbao detail = %+v", d)
		}
		if len(d.Keys) != 4 || len(d.ConnectionTests) != 1 || !d.ConnectionTests[0].OK {
			t.Errorf("openbao keys = %+v, tests = %+v", d.Keys, d.ConnectionTests)
		}

		d = decode[storeDetailJSON](t, h.do(acme, http.MethodGet, "/api/v1/orgs/org_acme/stores/st_local", nil, nil))
		byName := map[string]keyJSON{}
		for _, k := range d.Keys {
			byName[k.Name] = k
		}
		// stripe_secret fills one grant slot; github_token fills two grant
		// slots and a cabinet tool slot; mia_stripe_test sits in a cabinet.
		if k := byName["stripe_secret"]; k.UsedBy != 1 || k.Cabinet != nil || k.RotatedAt == nil {
			t.Errorf("stripe_secret = %+v", k)
		}
		if k := byName["github_token"]; k.UsedBy != 3 || k.ExpiresAt == nil {
			t.Errorf("github_token = %+v", k)
		}
		if k := byName["mia_stripe_test"]; k.Cabinet == nil || k.Cabinet.Name != "Mia's billing set" || k.UsedBy != 1 {
			t.Errorf("mia_stripe_test = %+v (cabinet %+v)", k, k.Cabinet)
		}
		if len(d.Keys) != 6 {
			t.Errorf("local keys = %d, want 6 (three cabinet keys included)", len(d.Keys))
		}

		// A removed store still opens: a key whose source was removed links to it.
		if res, body := h.get(acme, "/api/v1/orgs/org_acme/stores/st_removed"); res.StatusCode != http.StatusOK || !contains(body, `"removed_at":"`) {
			t.Errorf("removed store = %d %s", res.StatusCode, body)
		}
	})
}

// noSecrets fails if a response carries the demo store's credential
// references or any of the fields that hold them.
func noSecrets(t *testing.T, body string) {
	t.Helper()
	for _, s := range []string{"demo-placeholder", "secretId", "kss://", "role_id", "settings", "ca_bundle_pem", "length"} {
		if strings.Contains(body, s) {
			t.Errorf("response carries %q: %s", s, body)
		}
	}
}

func storeNames(ss []storeJSON) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Name
	}
	return out
}
