package store_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

var seedNow = time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

func seeded(t *testing.T, b storetest.Backend) *store.Store {
	t.Helper()
	st := storetest.Open(t, b)
	if _, err := st.SeedDemo(context.Background(), seedNow); err != nil {
		t.Fatal(err)
	}
	return st
}

func ago(d time.Duration) time.Time { return seedNow.Add(-d) }

const day = 24 * time.Hour

func ids[T any](xs []T, id func(T) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = id(x)
	}
	return out
}

func eq(t *testing.T, what string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", what, got, want)
	}
}

func notFound(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("%s: err = %v, want ErrNotFound", what, err)
	}
}

func TestReadOrgsAndPeople(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := seeded(t, b)

		org, err := st.GetOrg(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "org", []any{org.Name, org.Status, *org.AuditRetentionDays, org.CreatedBy, org.CreatedAt.Equal(ago(60 * day))},
			[]any{"Acme Corp", "active", 365, store.Ref{Kind: "user", ID: "u_dana"}, true})
		_, err = st.GetOrg(ctx, "org_missing")
		notFound(t, "GetOrg(missing)", err)

		orgs, err := st.ListUserOrgs(ctx, "u_mia")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Mia's orgs", ids(orgs, func(o store.UserOrg) string { return o.Org.Name + ":" + o.Role }),
			[]string{"Acme Corp:user", "Northwind Labs:user"})
		if orgs, _ := st.ListUserOrgs(ctx, "u_sam"); len(orgs) != 0 {
			t.Errorf("Sam (invited, not joined) has orgs %+v", orgs)
		}

		members, err := st.ListOrgMembers(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Acme members", ids(members, func(m store.OrgMember) string { return m.Name + ":" + m.Role + ":" + m.Status }),
			[]string{"Dana Keller:Owner:active", "Jo Reyes:user:suspended", "Mia Chen:user:active", "Ravi Mehta:userAdmin:active"})
		if jo := members[1]; jo.SuspendedFrom != "active" || jo.Email != "jo@acme.com" || jo.LastSignInAt == nil {
			t.Errorf("Jo = %+v", jo)
		}

		invs, err := st.ListInvitations(ctx, "org_acme", "pending")
		if err != nil {
			t.Fatal(err)
		}
		if len(invs) != 1 || invs[0].ID != "inv_sam" || invs[0].InvitedByName != "Dana Keller" || invs[0].Role != "user" ||
			!reflect.DeepEqual(invs[0].WorkspaceGrants, []store.WorkspaceGrant{{WorkspaceID: "ws_prod", Role: "member"}}) ||
			!invs[0].ExpiresAt.Equal(seedNow.Add(5*day)) {
			t.Errorf("pending Acme invitations = %+v", invs)
		}
		if all, _ := st.ListInvitations(ctx, "org_nw", ""); len(all) != 1 || all[0].Status != "expired" {
			t.Errorf("Northwind invitations = %+v", all)
		}
		incoming, err := st.ListIncomingInvitations(ctx, " Sam@Acme.com ")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Sam's incoming invitations", ids(incoming, func(i store.Invitation) string { return i.OrgName + ":" + i.Status }),
			[]string{"Acme Corp:pending", "Northwind Labs:expired"})

		sessions, err := st.ListUserSessions(ctx, "u_dana", seedNow)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Dana's sessions", ids(sessions, func(s store.UserSession) string { return s.Device + "@" + s.Place }),
			[]string{"MacBook Pro@San Francisco", "iPhone@San Francisco"})
		if later, _ := st.ListUserSessions(ctx, "u_dana", seedNow.Add(2*day)); len(later) != 0 {
			t.Errorf("expired sessions listed: %+v", later)
		}
		_, err = st.GetActiveSupportLock(ctx, "u_dana")
		notFound(t, "GetActiveSupportLock(unlocked user)", err)
	})
}

func TestReadWorkspacesAndAgents(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := seeded(t, b)

		wss, err := st.ListWorkspaces(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "workspaces", ids(wss, func(w store.Workspace) string { return w.Name }), []string{"Production", "Sandbox", "Staging"})
		prod := wss[0]
		eq(t, "Production", []any{prod.Slug, prod.HTTPS, prod.MCP, prod.UserCount, prod.AgentCount},
			[]any{"ws_1a2b", false, true, 3, 4})
		// old-ci is revoked, so Sandbox counts no agents.
		eq(t, "Sandbox agents", wss[1].AgentCount, 0)
		if ws, _ := st.ListWorkspaces(ctx, "org_nw"); len(ws) != 0 {
			t.Errorf("Northwind sees workspaces %+v", ws)
		}
		staging, err := st.GetWorkspace(ctx, "org_acme", "ws_staging")
		if err != nil || !staging.HTTPS || staging.MCP || staging.Slug != "ws_7c3d" {
			t.Errorf("GetWorkspace(ws_staging) = %+v, %v", staging, err)
		}
		_, err = st.GetWorkspace(ctx, "org_nw", "ws_staging")
		notFound(t, "GetWorkspace from another org", err)

		members, err := st.ListWorkspaceMembers(ctx, "org_acme", "ws_staging")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Staging members", ids(members, func(m store.WorkspaceMember) string { return m.Member.Kind + ":" + m.Label }),
			[]string{"user:Jo Reyes", "user:Ravi Mehta", "agent:docs-agent"})

		agents, err := st.ListAgents(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "agents", ids(agents, func(a store.Agent) string { return a.Label + ":" + a.Status }),
			[]string{"billing-agent:active", "docs-agent:active", "old-ci:revoked", "ops-agent:active", "reporting-bot:suspended"})
		docs := agents[1]
		eq(t, "docs-agent", []any{docs.TokenPrefix, docs.TokenLast4, docs.WorkspaceIDs, docs.RateLimitPerMinute == nil, docs.ExpiresAt == nil, docs.CreatedByLabel},
			[]any{"kh_live_", "X2dk", []string{"ws_prod", "ws_staging"}, true, true, "Ravi Mehta"})
		billing, err := st.GetAgent(ctx, "org_acme", "ag_billing")
		if err != nil || *billing.RateLimitPerMinute != 60 || !billing.ExpiresAt.Equal(seedNow.Add(87*day)) ||
			!reflect.DeepEqual(billing.WorkspaceIDs, []string{"ws_prod"}) {
			t.Errorf("GetAgent(ag_billing) = %+v, %v", billing, err)
		}
		_, err = st.GetAgent(ctx, "org_nw", "ag_billing")
		notFound(t, "GetAgent from another org", err)
	})
}

func TestReadStoresKeysAndTools(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := seeded(t, b)

		stores, err := st.ListStores(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		// The removed "Old vault" is not listed.
		eq(t, "stores", ids(stores, func(s store.SecretStore) string { return s.Name }), []string{"Local store", "Payments vault"})
		eq(t, "Local store", []any{stores[0].Provider, stores[0].RouteMode, stores[0].KeyCount}, []any{"local", "internal", 3})
		bao, err := st.GetStore(ctx, "org_acme", "st_bao")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Payments vault", []any{bao.Address, bao.RouteMode, bao.RouteConnectorID, bao.NetworkExposure, bao.AuthMethod,
			bao.AuthMount, bao.KVMount, bao.KVPathPrefix, bao.CABundleName, bao.SkipTLSVerify, bao.CacheDefaultTTLSeconds,
			bao.Health.Status, bao.Health.LastTestID, bao.KeyCount},
			[]any{"https://bao.acme.internal:8200", "connector", "cn_edge01", "private", "approle", "approle", "secret",
				"payments/", "vault-ca.pem", false, 60, "healthy", "ctest_bao_1", 4})
		var settings struct {
			Auth struct {
				SecretID struct{ Kind, URI string } `json:"secretId"`
			} `json:"auth"`
		}
		if err := json.Unmarshal(bao.Settings, &settings); err != nil || settings.Auth.SecretID.Kind != "sealed" ||
			!strings.HasPrefix(settings.Auth.SecretID.URI, "kss://") {
			t.Errorf("Payments vault secret_id = %+v, %v; want a sealed kss:// secretRef", settings, err)
		}
		removed, err := st.GetStore(ctx, "org_acme", "st_removed")
		if err != nil || removed.Status != "removed" || removed.RemovedAt == nil {
			t.Errorf("GetStore(st_removed) = %+v, %v", removed, err)
		}
		_, err = st.GetStore(ctx, "org_nw", "st_bao")
		notFound(t, "GetStore from another org", err)

		keys, err := st.ListKeys(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) != 11 {
			t.Errorf("ListKeys = %d keys, want 11", len(keys))
		}
		baoKeys, err := st.ListStoreKeys(ctx, "org_acme", "st_bao")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Payments vault keys", ids(baoKeys, func(k store.Key) string { return k.Name }),
			[]string{"payments/api_key", "payments/ledger_token", "payments/refund_key", "payments/webhook_secret"})
		var addr map[string]any
		if err := json.Unmarshal(baoKeys[0].Address, &addr); err != nil || addr["mount"] != "secret" || addr["field"] != "api_key" {
			t.Errorf("payments/api_key address = %s, %v", baoKeys[0].Address, err)
		}
		stripe, err := st.GetKey(ctx, "org_acme", "k_stripe")
		if err != nil || stripe.Address != nil || *stripe.Length != 32 || *stripe.RotationReminderDays != 90 ||
			!stripe.RotatedAt.Equal(ago(6*day)) || stripe.StoreName != "Local store" {
			t.Errorf("GetKey(k_stripe) = %+v, %v", stripe, err)
		}
		old, err := st.GetKey(ctx, "org_acme", "k_old")
		if err != nil || old.Status != "source_removed" || old.StoreName != "Old vault" {
			t.Errorf("GetKey(k_old) = %+v, %v", old, err)
		}
		mia, _ := st.GetKey(ctx, "org_acme", "k_mia1")
		eq(t, "k_mia1 cabinet", mia.CabinetID, "cb_mia")
		_, err = st.GetKey(ctx, "org_nw", "k_stripe")
		notFound(t, "GetKey from another org", err)

		tests, err := st.ListConnectionTests(ctx, "org_acme", "st_bao", 10)
		if err != nil || len(tests) != 1 || !tests[0].OK || tests[0].RanViaConnectorID != "cn_edge01" {
			t.Errorf("ListConnectionTests = %+v, %v", tests, err)
		}

		tools, err := st.ListTools(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "tools", ids(tools, func(x store.Tool) string { return x.Draft.DisplayName + ":" + x.Status }),
			[]string{"GitHub issues:published", "Slack messages:draft", "Stripe charges:published"})
		stripeTool, err := st.GetTool(ctx, "org_acme", "t_stripe")
		if err != nil {
			t.Fatal(err)
		}
		d := stripeTool.Draft
		eq(t, "Stripe draft", []any{d.InternalName, d.BaseURL, len(d.Actions), d.Actions[0].Path, d.Actions[0].Inputs[0].Name,
			d.Slots, d.PerMinute, d.TimeoutMs, *stripeTool.PublishedVersion, stripeTool.Catalog},
			[]any{"stripe_charges", "https://api.stripe.com", 2, "/v1/charges", "limit",
				[]store.KeySlot{{ID: "s_stripe", Name: "stripe_secret", Location: "header", Field: "Authorization", Scheme: "Bearer"}},
				60, 10000, 2, "stripe"})
		versions, err := st.ListToolVersions(ctx, "org_acme", "t_stripe")
		if err != nil || len(versions) != 1 || versions[0].Version != 2 || !reflect.DeepEqual(versions[0].Snapshot, d) {
			t.Errorf("ListToolVersions(t_stripe) = %+v, %v", versions, err)
		}
		slack, _ := st.GetTool(ctx, "org_acme", "t_slack")
		if slack.PublishedVersion != nil {
			t.Errorf("Slack draft has published version %d", *slack.PublishedVersion)
		}
		_, err = st.GetTool(ctx, "org_nw", "t_stripe")
		notFound(t, "GetTool from another org", err)
	})
}

func TestReadAvailabilityGrantsAndCabinets(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := seeded(t, b)

		avail, err := st.GetWorkspaceAvailability(ctx, "org_acme", "ws_prod")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "Production keys", avail.Keys, []store.AvailableKey{
			{KeyID: "k_bao1", Exposed: true}, {KeyID: "k_gh", Exposed: true}, {KeyID: "k_old", Exposed: true}, {KeyID: "k_stripe", Exposed: true}})
		eq(t, "Production tools", avail.ToolIDs, []string{"t_github", "t_stripe"})
		empty, err := st.GetWorkspaceAvailability(ctx, "org_acme", "ws_sandbox")
		if err != nil || len(empty.Keys) != 0 || len(empty.ToolIDs) != 0 {
			t.Errorf("Sandbox availability = %+v, %v", empty, err)
		}
		_, err = st.GetWorkspaceAvailability(ctx, "org_nw", "ws_prod")
		notFound(t, "GetWorkspaceAvailability from another org", err)

		grants, err := st.ListGrants(ctx, "org_acme", "ws_prod")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]any{}
		for _, g := range grants {
			got[g.ToolID] = []any{g.Enabled, g.PerMinute, *g.SlotMap[map[string]string{"t_stripe": "stripe_secret", "t_github": "github_token"}[g.ToolID]]}
		}
		eq(t, "Production grants", got, map[string]any{"t_stripe": []any{true, 60, "k_stripe"}, "t_github": []any{true, 30, "k_gh"}})
		if g, _ := st.ListGrants(ctx, "org_nw", "ws_prod"); len(g) != 0 {
			t.Errorf("Northwind sees Acme's grants: %+v", g)
		}

		cabs, err := st.ListCabinets(ctx, "org_acme", "ws_prod")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "cabinets", ids(cabs, func(c store.Cabinet) string { return c.Name }), []string{"Legacy imports", "Mia's billing set", "Team sandbox"})
		legacy, mia, team := cabs[0], cabs[1], cabs[2]
		eq(t, "Legacy imports", []any{legacy.CreatedBy, legacy.ManagedBy, legacy.AccessEveryone, legacy.KeyIDs, legacy.Tools},
			[]any{store.Ref{}, "", true, []string{"k_legacy_c"}, []store.CabinetTool{}})
		eq(t, "Mia's access", []any{mia.AccessEveryone, mia.Access, mia.ManagedBy, mia.KeyIDs},
			[]any{false, []store.Ref{{Kind: "user", ID: "u_mia"}, {Kind: "agent", ID: "ag_billing"}}, "u_mia", []string{"k_mia1", "k_mia2"}})
		if len(team.Tools) != 2 || team.Tools[1].ToolID != "t_stripe" || team.Tools[1].SlotMap["stripe_secret"] != nil {
			t.Errorf("Team sandbox tools = %+v; want Stripe with an unfilled slot", team.Tools)
		}
		one, err := st.GetCabinet(ctx, "org_acme", "cb_mia")
		if err != nil || !reflect.DeepEqual(one, mia) {
			t.Errorf("GetCabinet(cb_mia) = %+v, %v; want the listed cabinet", one, err)
		}
		_, err = st.GetCabinet(ctx, "org_nw", "cb_mia")
		notFound(t, "GetCabinet from another org", err)
	})
}

func TestReadConnectorsSettingsAndAudit(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := seeded(t, b)

		cs, err := st.ListConnectors(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "connectors", ids(cs, func(c store.Connector) string { return c.Name + ":" + c.Health.Status + ":" + c.Scope() }),
			[]string{"edge-01:healthy:org", "edge-02:degraded:workspace", "lab-runner:offline:workspace"})
		edge := cs[0]
		eq(t, "edge-01", []any{edge.Kind, edge.WorkspaceID, edge.Health.ReportedVersion, edge.Health.IP, edge.EnrolledByName,
			edge.CredentialPrefix, edge.RoutesStoreIDs, edge.EnrolledAt.Equal(ago(3 * day))},
			[]any{"vault", "ws_prod", "1.4.2", "10.2.14.7", "Dana Keller", "kh_conn_", []string{"st_bao"}, true})
		if lab := cs[2]; lab.Health.IP != "" || lab.Health.LastSeenAt == nil {
			t.Errorf("lab-runner = %+v", lab)
		}
		one, err := st.GetConnector(ctx, "org_acme", "cn_edge01")
		if err != nil || one.Scope() != "org" {
			t.Errorf("GetConnector(cn_edge01) = %+v, %v", one, err)
		}
		_, err = st.GetConnector(ctx, "org_nw", "cn_edge01")
		notFound(t, "GetConnector from another org", err)

		tokens, err := st.ListEnrollmentTokens(ctx, "org_acme")
		if err != nil || len(tokens) != 3 || tokens[0].Status != "consumed" || tokens[0].TokenPrefix != "kh_enr_" {
			t.Errorf("ListEnrollmentTokens = %+v, %v", tokens, err)
		}

		set, err := st.GetOrgSettings(ctx, "org_acme")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "notifications", set.Notifications, store.Notifications{
			ConnectorOffline: true, StoreUnhealthy: true, VerificationFailed: true, TokenExpiring: true})
		if set.ChecklistDismissedAt == nil || set.ConfigDownloadedAt == nil || set.AlertWebhookURL != "" {
			t.Errorf("org settings = %+v", set)
		}
		_, err = st.GetOrgSettings(ctx, "org_missing")
		notFound(t, "GetOrgSettings(missing)", err)

		prefs, personal, err := st.GetNotificationPreferences(ctx, "org_acme", "u_dana")
		if err != nil || personal || prefs != set.Notifications {
			t.Errorf("Dana's notifications = %+v, personal=%v, %v; want the org defaults", prefs, personal, err)
		}
		if _, err := st.LoadFixture(ctx, store.Fixture{Tables: []store.FixtureTable{{Table: "notification_preferences",
			Rows: []map[string]any{{"org_id": "org_acme", "user_id": "u_mia", "connector_offline": false, "store_unhealthy": true,
				"verification_failed": true, "token_expiring": true, "blocked_spike": true, "member_joined": false, "updated_at": "@now"}}}}},
			seedNow); err != nil {
			t.Fatal(err)
		}
		prefs, personal, err = st.GetNotificationPreferences(ctx, "org_acme", "u_mia")
		if err != nil || !personal || prefs.ConnectorOffline || !prefs.BlockedSpike {
			t.Errorf("Mia's notifications = %+v, personal=%v, %v; want her own choices", prefs, personal, err)
		}

		events, err := st.ListAuditEvents(ctx, "org_acme", store.AuditFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != 12 || events[0].ID != "ev_1" || events[11].ID != "ev_12" {
			t.Fatalf("audit log = %v, want ev_1 … ev_12 newest first", ids(events, func(e store.AuditEvent) string { return e.ID }))
		}
		blocked := events[1]
		var detail struct{ Kind, Rule, Attempted string }
		if err := json.Unmarshal(blocked.Detail, &detail); err != nil || detail.Kind != "blocked" || detail.Attempted != "GET /v1/payouts" ||
			blocked.LinkTo != "/tools/t_stripe?section=actions" || blocked.Actor.ID != "ag_ops" || blocked.Shared {
			t.Errorf("ev_2 = %+v (%+v, %v)", blocked, detail, err)
		}
		invite := events[9]
		if invite.ID != "ev_10" || !invite.Shared || invite.Detail != nil || invite.LinkTo != "" {
			t.Errorf("ev_10 = %+v; want a shared row with no detail or link", invite)
		}

		staging, _ := st.ListAuditEvents(ctx, "org_acme", store.AuditFilter{WorkspaceID: "ws_staging"})
		eq(t, "Staging events", ids(staging, func(e store.AuditEvent) string { return e.ID }), []string{"ev_4", "ev_6", "ev_7", "ev_8"})
		docs, _ := st.ListAuditEvents(ctx, "org_acme", store.AuditFilter{ActorID: "ag_docs", Type: "request"})
		eq(t, "docs-agent requests", ids(docs, func(e store.AuditEvent) string { return e.ID }), []string{"ev_3", "ev_6"})
		page, _ := st.ListAuditEvents(ctx, "org_acme", store.AuditFilter{Before: ago(time.Hour), Limit: 2})
		eq(t, "page before 1h ago", ids(page, func(e store.AuditEvent) string { return e.ID }), []string{"ev_7", "ev_8"})
		if other, _ := st.ListAuditEvents(ctx, "org_nw", store.AuditFilter{}); len(other) != 0 {
			t.Errorf("Northwind sees %d Acme audit rows", len(other))
		}
	})
}
