package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Read queries for the screens. Every query on tenant data takes the org ID
// and filters on it, so a caller can never read another org's rows by
// guessing an ID: a row that exists in another org is ErrNotFound. Nothing
// here returns a secret: tokens come back as prefix and last four, store
// credentials as secretRefs, and Local-store values are never read.

// Ref names who did something, or a workspace member or player: kind is
// user, agent, support or system (or a component kind in audit rows).
type Ref struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ---------------------------------------------------------------------------
// Organizations and people.

// Org is an organization.
type Org struct {
	ID                 string
	Name               string
	Status             string
	AuditRetentionDays *int
	CreatedAt          time.Time
	CreatedBy          Ref
	UpdatedAt          time.Time
	DeletedAt          *time.Time
	Rev                int64
}

const orgCols = "o.id, o.name, o.status, o.audit_retention_days, o.created_at, o.created_by_kind, o.created_by_id, o.updated_at, o.deleted_at, o.rev"

func orgDest(o *Org) []any {
	return []any{&o.ID, &o.Name, &o.Status, intOrNil{&o.AuditRetentionDays}, at{&o.CreatedAt},
		text{&o.CreatedBy.Kind}, text{&o.CreatedBy.ID}, at{&o.UpdatedAt}, atOrNil{&o.DeletedAt}, &o.Rev}
}

func scanOrg(r rowScanner) (Org, error) {
	var o Org
	return o, r.Scan(orgDest(&o)...)
}

// GetOrg loads an organization.
func (s *Store) GetOrg(ctx context.Context, orgID string) (Org, error) {
	return one(ctx, s, scanOrg, "SELECT "+orgCols+" FROM orgs o WHERE o.id = ?", orgID)
}

// UserOrg is one organization a user belongs to, with their role there.
type UserOrg struct {
	Org      Org
	Role     string // Owner, userAdmin or user
	Status   string // active, invited or suspended
	JoinedAt *time.Time
}

// ListUserOrgs lists the organizations a user belongs to, by name: the org
// switcher and the Organizations page.
func (s *Store) ListUserOrgs(ctx context.Context, userID string) ([]UserOrg, error) {
	return list(ctx, s, func(r rowScanner) (UserOrg, error) {
		var u UserOrg
		return u, r.Scan(append(orgDest(&u.Org), &u.Role, &u.Status, atOrNil{&u.JoinedAt})...)
	}, "SELECT "+orgCols+", m.role, m.status, m.joined_at FROM org_memberships m JOIN orgs o ON o.id = m.org_id"+
		" WHERE m.user_id = ? AND o.status = 'active' ORDER BY o.name, o.id", userID)
}

// OrgMember is a person in an organization's member list.
type OrgMember struct {
	MembershipID  string
	UserID        string
	Email         string
	Name          string
	AccountStatus string // active or pending (never signed in)
	Role          string
	Status        string // active, invited or suspended
	SuspendedFrom string
	JoinedAt      *time.Time
	LastSignInAt  *time.Time
	LockedAt      *time.Time // a support lock, account-wide
}

// ListOrgMembers lists an organization's people, by name.
func (s *Store) ListOrgMembers(ctx context.Context, orgID string) ([]OrgMember, error) {
	return list(ctx, s, func(r rowScanner) (OrgMember, error) {
		var m OrgMember
		return m, r.Scan(&m.MembershipID, &m.UserID, &m.Email, &m.Name, &m.AccountStatus, &m.Role, &m.Status,
			text{&m.SuspendedFrom}, atOrNil{&m.JoinedAt}, atOrNil{&m.LastSignInAt}, atOrNil{&m.LockedAt})
	}, `SELECT m.id, u.id, u.email, u.name, u.status, m.role, m.status, m.suspended_from, m.joined_at,
	           u.last_sign_in_at, u.lock_locked_at
	      FROM org_memberships m JOIN users u ON u.id = m.user_id
	     WHERE m.org_id = ? ORDER BY u.name, u.id`, orgID)
}

// WorkspaceGrant is a workspace an invitation adds the person to.
type WorkspaceGrant struct {
	WorkspaceID string `json:"workspaceId"`
	Role        string `json:"role"`
}

// Invitation is an invitation to join an organization. Only the link
// token's last four characters are kept.
type Invitation struct {
	ID              string
	OrgID           string
	OrgName         string
	Email           string
	Role            string
	WorkspaceGrants []WorkspaceGrant
	Status          string // pending, accepted, declined, revoked or expired
	InvitedAt       time.Time
	InvitedBy       Ref
	InvitedByName   string
	ExpiresAt       time.Time
	AcceptedAt      *time.Time
	RevokedAt       *time.Time
}

const invitationQuery = `SELECT i.id, i.org_id, o.name, i.email, i.role, i.workspace_grants, i.status, i.invited_at,
       i.invited_by_kind, i.invited_by_id, u.name, i.expires_at, i.accepted_at, i.revoked_at
  FROM invitations i JOIN orgs o ON o.id = i.org_id
  LEFT JOIN users u ON i.invited_by_kind = 'user' AND u.id = i.invited_by_id`

func scanInvitation(r rowScanner) (Invitation, error) {
	var i Invitation
	err := r.Scan(&i.ID, &i.OrgID, &i.OrgName, &i.Email, &i.Role, jsonInto{&i.WorkspaceGrants}, &i.Status,
		at{&i.InvitedAt}, text{&i.InvitedBy.Kind}, text{&i.InvitedBy.ID}, text{&i.InvitedByName},
		at{&i.ExpiresAt}, atOrNil{&i.AcceptedAt}, atOrNil{&i.RevokedAt})
	return i, err
}

// ListInvitations lists an organization's invitations, newest first. An
// empty status lists all of them.
func (s *Store) ListInvitations(ctx context.Context, orgID, status string) ([]Invitation, error) {
	if status == "" {
		return list(ctx, s, scanInvitation, invitationQuery+" WHERE i.org_id = ? ORDER BY i.invited_at DESC, i.id", orgID)
	}
	return list(ctx, s, scanInvitation, invitationQuery+" WHERE i.org_id = ? AND i.status = ? ORDER BY i.invited_at DESC, i.id", orgID, status)
}

// ListIncomingInvitations lists the invitations sent to an email address,
// across organizations, newest first: the Invitations page.
func (s *Store) ListIncomingInvitations(ctx context.Context, email string) ([]Invitation, error) {
	return list(ctx, s, scanInvitation, invitationQuery+" WHERE i.email_lower = ? ORDER BY i.invited_at DESC, i.id",
		strings.ToLower(strings.TrimSpace(email)))
}

// UserSession is a live browser session, for "Active sessions".
type UserSession struct {
	IDHash     string
	Method     string
	Client     string
	Device     string
	Place      string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt *time.Time
	ExpiresAt  time.Time
}

// ListUserSessions lists a user's unexpired, unrevoked sessions, most
// recently seen first.
func (s *Store) ListUserSessions(ctx context.Context, userID string, now time.Time) ([]UserSession, error) {
	return list(ctx, s, func(r rowScanner) (UserSession, error) {
		var x UserSession
		return x, r.Scan(&x.IDHash, &x.Method, &x.Client, text{&x.Device}, text{&x.Place}, text{&x.UserAgent},
			at{&x.CreatedAt}, atOrNil{&x.LastSeenAt}, at{&x.ExpiresAt})
	}, `SELECT id_hash, method, client, device, place, user_agent, created_at, last_seen_at, expires_at
	      FROM sessions WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
	     ORDER BY COALESCE(last_seen_at, created_at) DESC, id_hash`, userID, millis(now))
}

// SupportLock is an account-wide lock set by Keyhole support.
type SupportLock struct {
	ID             string
	UserID         string
	Status         string
	LockedAt       time.Time
	LockedBy       Ref
	Reason         string
	UnlockRequests json.RawMessage // [{byUserId, orgId, at}]
}

// GetActiveSupportLock returns a user's active support lock, or ErrNotFound.
func (s *Store) GetActiveSupportLock(ctx context.Context, userID string) (SupportLock, error) {
	return one(ctx, s, func(r rowScanner) (SupportLock, error) {
		var l SupportLock
		return l, r.Scan(&l.ID, &l.UserID, &l.Status, at{&l.LockedAt}, text{&l.LockedBy.Kind}, text{&l.LockedBy.ID},
			&l.Reason, rawJSON{&l.UnlockRequests})
	}, `SELECT id, user_id, status, locked_at, locked_by_kind, locked_by_id, reason, unlock_requests
	      FROM support_locks WHERE user_id = ? AND status = 'active' ORDER BY locked_at DESC LIMIT 1`, userID)
}

// ---------------------------------------------------------------------------
// Workspaces, members and agents.

// Workspace is a workspace with Keyhole's settings for it.
type Workspace struct {
	ID         string
	OrgID      string
	Name       string
	Status     string
	Slug       string
	HTTPS      bool
	MCP        bool
	CreatedAt  time.Time
	CreatedBy  Ref
	UpdatedAt  time.Time
	Rev        int64
	UserCount  int // people who are explicit members
	AgentCount int // agents in it that are not revoked in Keyhole
}

const workspaceQuery = `SELECT w.id, w.org_id, w.name, w.status, ws.slug, ws.https, ws.mcp, w.created_at,
       w.created_by_kind, w.created_by_id, w.updated_at, w.rev,
       (SELECT COUNT(*) FROM workspace_memberships m
         WHERE m.org_id = w.org_id AND m.workspace_id = w.id AND m.member_kind = 'user'),
       (SELECT COUNT(*) FROM workspace_memberships m JOIN agent_profiles p ON p.agent_id = m.member_id
         WHERE m.org_id = w.org_id AND m.workspace_id = w.id AND m.member_kind = 'agent' AND p.status <> 'revoked')
  FROM workspaces w LEFT JOIN workspace_settings ws ON ws.workspace_id = w.id`

func scanWorkspace(r rowScanner) (Workspace, error) {
	var w Workspace
	var https, mcp *bool
	err := r.Scan(&w.ID, &w.OrgID, &w.Name, &w.Status, text{&w.Slug}, nullBool{&https}, nullBool{&mcp},
		at{&w.CreatedAt}, text{&w.CreatedBy.Kind}, text{&w.CreatedBy.ID}, at{&w.UpdatedAt}, &w.Rev,
		&w.UserCount, &w.AgentCount)
	w.HTTPS, w.MCP = https != nil && *https, mcp != nil && *mcp
	return w, err
}

// ListWorkspaces lists an organization's active workspaces, by name.
func (s *Store) ListWorkspaces(ctx context.Context, orgID string) ([]Workspace, error) {
	return list(ctx, s, scanWorkspace, workspaceQuery+" WHERE w.org_id = ? AND w.status = 'active' ORDER BY w.name, w.id", orgID)
}

// GetWorkspace loads one of an organization's workspaces.
func (s *Store) GetWorkspace(ctx context.Context, orgID, workspaceID string) (Workspace, error) {
	return one(ctx, s, scanWorkspace, workspaceQuery+" WHERE w.org_id = ? AND w.id = ?", orgID, workspaceID)
}

// WorkspaceMember is a person or agent in a workspace.
type WorkspaceMember struct {
	ID          string
	WorkspaceID string
	Member      Ref
	Label       string // the person's name or the agent's label
	Role        string // member or workspace_admin
	AddedAt     time.Time
	AddedBy     Ref
}

// ListWorkspaceMembers lists a workspace's explicit members, people first,
// then agents, each by name. Default admins (the org's Owners and
// userAdmins) are derived by the caller, never stored.
func (s *Store) ListWorkspaceMembers(ctx context.Context, orgID, workspaceID string) ([]WorkspaceMember, error) {
	return list(ctx, s, func(r rowScanner) (WorkspaceMember, error) {
		var m WorkspaceMember
		return m, r.Scan(&m.ID, &m.WorkspaceID, &m.Member.Kind, &m.Member.ID, text{&m.Label}, &m.Role,
			at{&m.AddedAt}, text{&m.AddedBy.Kind}, text{&m.AddedBy.ID})
	}, `SELECT m.id, m.workspace_id, m.member_kind, m.member_id, COALESCE(u.name, a.label), m.role,
	           m.added_at, m.added_by_kind, m.added_by_id
	      FROM workspace_memberships m
	      LEFT JOIN users u ON m.member_kind = 'user' AND u.id = m.member_id
	      LEFT JOIN agents a ON m.member_kind = 'agent' AND a.id = m.member_id AND a.org_id = m.org_id
	     WHERE m.org_id = ? AND m.workspace_id = ?
	     ORDER BY m.member_kind DESC, COALESCE(u.name, a.label), m.member_id`, orgID, workspaceID)
}

// Agent is an agent with its Keyhole profile. The kh_live_ token is only
// ever its prefix and last four.
type Agent struct {
	ID                 string
	OrgID              string
	Label              string
	CreatedAt          time.Time
	CreatedBy          Ref
	CreatedByLabel     string
	RetiredAt          *time.Time
	Status             string // active, suspended or revoked (Keyhole's view)
	TokenPrefix        string
	TokenLast4         string
	TokenIssuedAt      *time.Time
	TokenRotatedAt     *time.Time
	TokenGraceUntil    *time.Time
	ExpiresAt          *time.Time
	RateLimitPerMinute *int
	LastUsedAt         *time.Time
	SuspendedAt        *time.Time
	RevokedAt          *time.Time
	WorkspaceIDs       []string
}

const agentQuery = `SELECT a.id, a.org_id, a.label, a.created_at, a.created_by_kind, a.created_by_id, a.created_by_label,
       a.retired_at, p.status, p.token_prefix, p.token_last4, p.token_issued_at, p.token_rotated_at,
       p.token_grace_until, p.expires_at, p.rate_limit_per_minute, p.last_used_at, p.suspended_at, p.revoked_at
  FROM agents a LEFT JOIN agent_profiles p ON p.agent_id = a.id`

func scanAgent(r rowScanner) (Agent, error) {
	var a Agent
	err := r.Scan(&a.ID, &a.OrgID, &a.Label, at{&a.CreatedAt}, text{&a.CreatedBy.Kind}, text{&a.CreatedBy.ID},
		text{&a.CreatedByLabel}, atOrNil{&a.RetiredAt}, text{&a.Status}, text{&a.TokenPrefix}, text{&a.TokenLast4},
		atOrNil{&a.TokenIssuedAt}, atOrNil{&a.TokenRotatedAt}, atOrNil{&a.TokenGraceUntil}, atOrNil{&a.ExpiresAt},
		intOrNil{&a.RateLimitPerMinute}, atOrNil{&a.LastUsedAt}, atOrNil{&a.SuspendedAt}, atOrNil{&a.RevokedAt})
	if a.Status == "" {
		a.Status = "revoked" // no Keyhole profile: the agent has no Keyhole token
	}
	return a, err
}

// ListAgents lists an organization's agents, by label, each with the
// workspaces it is in.
func (s *Store) ListAgents(ctx context.Context, orgID string) ([]Agent, error) {
	agents, err := list(ctx, s, scanAgent, agentQuery+" WHERE a.org_id = ? ORDER BY a.label_lower, a.id", orgID)
	if err != nil {
		return nil, err
	}
	ws, err := s.memberWorkspaces(ctx, orgID, "agent", "")
	if err != nil {
		return nil, err
	}
	for i := range agents {
		agents[i].WorkspaceIDs = nonNil(ws[agents[i].ID])
	}
	return agents, nil
}

// GetAgent loads one of an organization's agents.
func (s *Store) GetAgent(ctx context.Context, orgID, agentID string) (Agent, error) {
	a, err := one(ctx, s, scanAgent, agentQuery+" WHERE a.org_id = ? AND a.id = ?", orgID, agentID)
	if err != nil {
		return Agent{}, err
	}
	ws, err := s.memberWorkspaces(ctx, orgID, "agent", agentID)
	a.WorkspaceIDs = nonNil(ws[agentID])
	return a, err
}

// memberWorkspaces maps member ID → workspace IDs for one kind of member,
// optionally for one member only.
func (s *Store) memberWorkspaces(ctx context.Context, orgID, kind, memberID string) (map[string][]string, error) {
	query := "SELECT member_id, workspace_id FROM workspace_memberships WHERE org_id = ? AND member_kind = ?"
	args := []any{orgID, kind}
	if memberID != "" {
		query += " AND member_id = ?"
		args = append(args, memberID)
	}
	pairs, err := list(ctx, s, scanPair, query+" ORDER BY member_id, workspace_id", args...)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, p := range pairs {
		out[p[0]] = append(out[p[0]], p[1])
	}
	return out, nil
}

func scanPair(r rowScanner) ([2]string, error) {
	var p [2]string
	return p, r.Scan(&p[0], &p[1])
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------------------------------------------------------------------------
// Stores, keys and connection tests.

// StoreHealth is a store's last known health.
type StoreHealth struct {
	Status     string // healthy, degraded, sealed, denied, unreachable or unknown
	Code       string
	Message    string
	HTTPStatus *int
	VendorCode string
	CheckedAt  time.Time
	LastTestID string
}

// SecretStore is a secret store. Credentials appear only as secretRefs
// inside Settings.
type SecretStore struct {
	ID                     string
	OrgID                  string
	Name                   string
	Provider               string
	Status                 string
	RouteMode              string // direct, connector or internal (Local)
	RouteConnectorID       string
	NetworkExposure        string
	Address                string
	Namespace              string
	CABundlePEM            string // public CA certificates
	CABundleName           string
	TLSServerName          string
	SkipTLSVerify          bool
	KVMount                string
	KVPathPrefix           string
	AuthMethod             string
	AuthMount              string
	AuthRoleID             string
	AuthRole               string
	IdentitySource         string
	Settings               json.RawMessage // {"connection": {...}, "auth": {...}}: the provider's other fields
	Health                 StoreHealth
	CacheDefaultTTLSeconds int
	Invalidation           json.RawMessage
	CreatedAt              time.Time
	CreatedBy              Ref
	UpdatedAt              time.Time
	RemovedAt              *time.Time
	Rev                    int64
	KeyCount               int
}

const storeQuery = `SELECT st.id, st.org_id, st.name, st.provider, st.status, st.route_mode, st.route_connector_id,
       st.network_exposure, st.address, st.namespace, st.ca_bundle_pem, st.ca_bundle_name, st.tls_server_name,
       st.skip_tls_verify, st.kv_mount, st.kv_path_prefix, st.auth_method, st.auth_mount, st.auth_role_id,
       st.auth_role, st.identity_source, st.settings, st.health_status, st.health_code, st.health_message,
       st.health_http_status, st.health_vendor_code, st.health_checked_at, st.health_last_test_id,
       st.cache_default_ttl_seconds, st.invalidation, st.created_at, st.created_by_kind, st.created_by_id,
       st.updated_at, st.removed_at, st.rev,
       (SELECT COUNT(*) FROM keys k WHERE k.org_id = st.org_id AND k.store_id = st.id AND k.cabinet_id IS NULL)
  FROM stores st`

func scanStore(r rowScanner) (SecretStore, error) {
	var x SecretStore
	h := &x.Health
	err := r.Scan(&x.ID, &x.OrgID, &x.Name, &x.Provider, &x.Status, &x.RouteMode, text{&x.RouteConnectorID},
		&x.NetworkExposure, text{&x.Address}, text{&x.Namespace}, text{&x.CABundlePEM}, text{&x.CABundleName},
		text{&x.TLSServerName}, boolean{&x.SkipTLSVerify}, text{&x.KVMount}, text{&x.KVPathPrefix},
		text{&x.AuthMethod}, text{&x.AuthMount}, text{&x.AuthRoleID}, text{&x.AuthRole}, text{&x.IdentitySource},
		rawJSON{&x.Settings}, &h.Status, text{&h.Code}, text{&h.Message}, intOrNil{&h.HTTPStatus},
		text{&h.VendorCode}, at{&h.CheckedAt}, text{&h.LastTestID}, &x.CacheDefaultTTLSeconds,
		rawJSON{&x.Invalidation}, at{&x.CreatedAt}, text{&x.CreatedBy.Kind}, text{&x.CreatedBy.ID},
		at{&x.UpdatedAt}, atOrNil{&x.RemovedAt}, &x.Rev, &x.KeyCount)
	return x, err
}

// ListStores lists an organization's stores that are not removed, by name.
func (s *Store) ListStores(ctx context.Context, orgID string) ([]SecretStore, error) {
	return list(ctx, s, scanStore, storeQuery+" WHERE st.org_id = ? AND st.status <> 'removed' ORDER BY st.name, st.id", orgID)
}

// GetStore loads one of an organization's stores, removed ones included
// (a key whose source was removed still names it).
func (s *Store) GetStore(ctx context.Context, orgID, storeID string) (SecretStore, error) {
	return one(ctx, s, scanStore, storeQuery+" WHERE st.org_id = ? AND st.id = ?", orgID, storeID)
}

// Key says where a secret value lives. It never carries the value.
type Key struct {
	ID                   string
	OrgID                string
	Name                 string
	StoreID              string
	StoreName            string
	Provider             string
	Address              json.RawMessage // provider-specific; nil for a Local key with no value yet
	Pinned               bool
	CacheTTLSeconds      *int
	Length               *int
	LastSeenVersion      string
	RotatedAt            *time.Time
	ExpiresAt            *time.Time
	RotationReminderDays *int
	Notes                string
	Status               string // active or source_removed
	CabinetID            string
	CreatedAt            time.Time
	CreatedBy            Ref
	UpdatedAt            time.Time
	Rev                  int64
}

const keyQuery = `SELECT k.id, k.org_id, k.name, k.store_id, st.name, k.provider, k.address, k.pinned,
       k.cache_ttl_seconds, k.length, k.last_seen_version, k.rotated_at, k.expires_at, k.rotation_reminder_days,
       k.notes, k.status, k.cabinet_id, k.created_at, k.created_by_kind, k.created_by_id, k.updated_at, k.rev
  FROM keys k JOIN stores st ON st.id = k.store_id`

func scanKey(r rowScanner) (Key, error) {
	var k Key
	err := r.Scan(&k.ID, &k.OrgID, &k.Name, &k.StoreID, &k.StoreName, &k.Provider, rawJSON{&k.Address},
		boolean{&k.Pinned}, intOrNil{&k.CacheTTLSeconds}, intOrNil{&k.Length}, text{&k.LastSeenVersion},
		atOrNil{&k.RotatedAt}, atOrNil{&k.ExpiresAt}, intOrNil{&k.RotationReminderDays}, &k.Notes, &k.Status,
		text{&k.CabinetID}, at{&k.CreatedAt}, text{&k.CreatedBy.Kind}, text{&k.CreatedBy.ID}, at{&k.UpdatedAt}, &k.Rev)
	return k, err
}

// ListKeys lists an organization's keys, cabinet keys included, by name.
func (s *Store) ListKeys(ctx context.Context, orgID string) ([]Key, error) {
	return list(ctx, s, scanKey, keyQuery+" WHERE k.org_id = ? ORDER BY k.name, k.id", orgID)
}

// ListStoreKeys lists the keys in one of an organization's stores, by name.
func (s *Store) ListStoreKeys(ctx context.Context, orgID, storeID string) ([]Key, error) {
	return list(ctx, s, scanKey, keyQuery+" WHERE k.org_id = ? AND k.store_id = ? ORDER BY k.name, k.id", orgID, storeID)
}

// GetKey loads one of an organization's keys.
func (s *Store) GetKey(ctx context.Context, orgID, keyID string) (Key, error) {
	return one(ctx, s, scanKey, keyQuery+" WHERE k.org_id = ? AND k.id = ?", orgID, keyID)
}

// ConnectionTest is one run of a store's connection check. Steps carry
// codes and timings, never values.
type ConnectionTest struct {
	ID                string
	StoreID           string
	Trigger           string
	RanViaMode        string
	RanViaConnectorID string
	StartedAt         time.Time
	FinishedAt        *time.Time
	OK                bool
	HealthCode        string
	Steps             json.RawMessage
	RequestedBy       Ref
}

// ListConnectionTests lists a store's most recent connection tests, newest
// first.
func (s *Store) ListConnectionTests(ctx context.Context, orgID, storeID string, limit int) ([]ConnectionTest, error) {
	return list(ctx, s, func(r rowScanner) (ConnectionTest, error) {
		var c ConnectionTest
		return c, r.Scan(&c.ID, &c.StoreID, &c.Trigger, &c.RanViaMode, text{&c.RanViaConnectorID}, at{&c.StartedAt},
			atOrNil{&c.FinishedAt}, boolean{&c.OK}, text{&c.HealthCode}, rawJSON{&c.Steps},
			text{&c.RequestedBy.Kind}, text{&c.RequestedBy.ID})
	}, `SELECT id, store_id, trigger, ran_via_mode, ran_via_connector_id, started_at, finished_at, ok, health_code,
	           steps, requested_by_kind, requested_by_id
	      FROM store_connection_tests WHERE org_id = ? AND store_id = ?
	     ORDER BY started_at DESC, id LIMIT ?`, orgID, storeID, clampLimit(limit))
}

func clampLimit(n int) int {
	if n <= 0 || n > 500 {
		return 500
	}
	return n
}

// ---------------------------------------------------------------------------
// Tools.

// ToolInput is one input of a tool action.
type ToolInput struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
}

// ToolAction is one HTTP call a tool may make.
type ToolAction struct {
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Summary string      `json:"summary"`
	Inputs  []ToolInput `json:"inputs"`
}

// KeySlot is where a tool puts a key on its calls.
type KeySlot struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Location string `json:"location"`
	Field    string `json:"field"`
	Scheme   string `json:"scheme"`
}

// ToolSnapshot is a tool's definition: the draft, or a published version.
type ToolSnapshot struct {
	InternalName string       `json:"internalName"`
	DisplayName  string       `json:"displayName"`
	Description  string       `json:"description"`
	BaseURL      string       `json:"baseUrl"`
	Actions      []ToolAction `json:"actions"`
	Slots        []KeySlot    `json:"slots"`
	PerMinute    int          `json:"perMinute"`
	TimeoutMs    int          `json:"timeoutMs"`
	// PrivateAllow is the private addresses (IPs/CIDRs) the sidecar may let
	// this tool reach. Empty: none.
	PrivateAllow []string `json:"privateAllow,omitempty"`
}

// Tool is a tool with its editable draft.
type Tool struct {
	ID               string
	OrgID            string
	Status           string // draft or published (the draft equals the latest version)
	Draft            ToolSnapshot
	PublishedVersion *int
	Catalog          string
	CreatedAt        time.Time
	CreatedBy        Ref
	UpdatedAt        time.Time
	Rev              int64
}

const toolQuery = `SELECT id, org_id, status, draft_internal_name, draft_display_name, draft_description, draft_base_url,
       draft_actions, draft_slots, draft_per_minute, draft_timeout_ms, published_version, catalog,
       created_at, created_by_kind, created_by_id, updated_at, rev
  FROM tools`

func scanTool(r rowScanner) (Tool, error) {
	var t Tool
	d := &t.Draft
	err := r.Scan(&t.ID, &t.OrgID, &t.Status, &d.InternalName, &d.DisplayName, &d.Description, &d.BaseURL,
		jsonInto{&d.Actions}, jsonInto{&d.Slots}, &d.PerMinute, &d.TimeoutMs, intOrNil{&t.PublishedVersion},
		text{&t.Catalog}, at{&t.CreatedAt}, text{&t.CreatedBy.Kind}, text{&t.CreatedBy.ID}, at{&t.UpdatedAt}, &t.Rev)
	return t, err
}

// ListTools lists an organization's tools that are not deleted, by name.
func (s *Store) ListTools(ctx context.Context, orgID string) ([]Tool, error) {
	return list(ctx, s, scanTool, toolQuery+" WHERE org_id = ? AND deleted_at IS NULL ORDER BY draft_display_name, id", orgID)
}

// GetTool loads one of an organization's tools.
func (s *Store) GetTool(ctx context.Context, orgID, toolID string) (Tool, error) {
	return one(ctx, s, scanTool, toolQuery+" WHERE org_id = ? AND id = ? AND deleted_at IS NULL", orgID, toolID)
}

// ToolVersion is an immutable published snapshot of a tool.
type ToolVersion struct {
	ID          string
	ToolID      string
	Version     int
	Snapshot    ToolSnapshot
	Changes     []string
	PublishedAt time.Time
	PublishedBy Ref
}

// ListToolVersions lists a tool's published versions, newest first.
func (s *Store) ListToolVersions(ctx context.Context, orgID, toolID string) ([]ToolVersion, error) {
	return list(ctx, s, func(r rowScanner) (ToolVersion, error) {
		var v ToolVersion
		return v, r.Scan(&v.ID, &v.ToolID, &v.Version, jsonInto{&v.Snapshot}, jsonInto{&v.Changes},
			at{&v.PublishedAt}, &v.PublishedBy.Kind, &v.PublishedBy.ID)
	}, `SELECT id, tool_id, version, snapshot, changes, published_at, published_by_kind, published_by_id
	      FROM tool_versions WHERE org_id = ? AND tool_id = ? ORDER BY version DESC`, orgID, toolID)
}

// ---------------------------------------------------------------------------
// What a workspace can use: availability, exposure and grants.

// AvailableKey is a key made available to a workspace; Exposed means its
// admins chose to expose it.
type AvailableKey struct {
	KeyID   string
	Exposed bool
}

// WorkspaceAvailability is what an org admin made available to a workspace
// and which of those keys the workspace exposes.
type WorkspaceAvailability struct {
	WorkspaceID       string
	Keys              []AvailableKey
	ToolIDs           []string
	AvailabilitySetAt *time.Time
	AvailabilitySetBy Ref
	ExposureSetAt     *time.Time
	ExposureSetBy     Ref
	UpdatedAt         time.Time
	Rev               int64
}

// GetWorkspaceAvailability loads a workspace's available and exposed keys
// and its available tools.
func (s *Store) GetWorkspaceAvailability(ctx context.Context, orgID, workspaceID string) (WorkspaceAvailability, error) {
	a, err := one(ctx, s, func(r rowScanner) (WorkspaceAvailability, error) {
		var a WorkspaceAvailability
		return a, r.Scan(&a.WorkspaceID, atOrNil{&a.AvailabilitySetAt}, text{&a.AvailabilitySetBy.Kind},
			text{&a.AvailabilitySetBy.ID}, atOrNil{&a.ExposureSetAt}, text{&a.ExposureSetBy.Kind},
			text{&a.ExposureSetBy.ID}, at{&a.UpdatedAt}, &a.Rev)
	}, `SELECT workspace_id, availability_set_at, availability_set_by_kind, availability_set_by_id,
	           exposure_set_at, exposure_set_by_kind, exposure_set_by_id, updated_at, rev
	      FROM workspace_availability WHERE org_id = ? AND workspace_id = ?`, orgID, workspaceID)
	if err != nil {
		return WorkspaceAvailability{}, err
	}
	if a.Keys, err = list(ctx, s, func(r rowScanner) (AvailableKey, error) {
		var k AvailableKey
		return k, r.Scan(&k.KeyID, boolean{&k.Exposed})
	}, "SELECT key_id, exposed FROM workspace_available_keys WHERE org_id = ? AND workspace_id = ? ORDER BY key_id",
		orgID, workspaceID); err != nil {
		return WorkspaceAvailability{}, err
	}
	a.ToolIDs, err = list(ctx, s, scanString,
		"SELECT tool_id FROM workspace_available_tools WHERE org_id = ? AND workspace_id = ? ORDER BY tool_id",
		orgID, workspaceID)
	return a, err
}

func scanString(r rowScanner) (string, error) {
	var v string
	return v, r.Scan(&v)
}

// Grant is a tool granted to a workspace. SlotMap maps each slot name to a
// key ID, or to nil while the slot is unfilled.
type Grant struct {
	ID            string
	WorkspaceID   string
	ToolID        string
	PinnedVersion *int
	Enabled       bool
	PerMinute     int
	SlotMap       map[string]*string
	GrantedAt     time.Time
	GrantedBy     Ref
	UpdatedAt     time.Time
	Rev           int64
}

// grantSlot is one slot row read for a workspace's grants.
type grantSlot struct{ grantID, name, keyID string }

// mergeGrantSlots fills each grant's SlotMap from the slot rows. The two
// reads are separate statements, not one snapshot, so a slot can name a
// grant that is absent from the grants result (a grant committed between
// the reads); those rows are skipped — the next read returns the grant.
func mergeGrantSlots(grants []Grant, slots []grantSlot) {
	byID := map[string]*Grant{}
	for i := range grants {
		byID[grants[i].ID] = &grants[i]
	}
	for _, x := range slots {
		g := byID[x.grantID]
		if g == nil {
			continue
		}
		if g.SlotMap == nil {
			// Grants from ListGrants always carry a map; a zero-value Grant
			// (tests, a future caller) must not panic on assignment.
			g.SlotMap = map[string]*string{}
		}
		var key *string
		if x.keyID != "" {
			key = &x.keyID
		}
		g.SlotMap[x.name] = key
	}
}

// ListGrants lists the tools granted to a workspace.
func (s *Store) ListGrants(ctx context.Context, orgID, workspaceID string) ([]Grant, error) {
	grants, err := list(ctx, s, func(r rowScanner) (Grant, error) {
		g := Grant{SlotMap: map[string]*string{}}
		return g, r.Scan(&g.ID, &g.WorkspaceID, &g.ToolID, intOrNil{&g.PinnedVersion}, boolean{&g.Enabled},
			&g.PerMinute, at{&g.GrantedAt}, &g.GrantedBy.Kind, &g.GrantedBy.ID, at{&g.UpdatedAt}, &g.Rev)
	}, `SELECT id, workspace_id, tool_id, pinned_version, enabled, per_minute, granted_at, granted_by_kind,
	           granted_by_id, updated_at, rev
	      FROM grants WHERE org_id = ? AND workspace_id = ? ORDER BY granted_at, id`, orgID, workspaceID)
	if err != nil {
		return nil, err
	}
	slots, err := list(ctx, s, func(r rowScanner) (grantSlot, error) {
		var x grantSlot
		return x, r.Scan(&x.grantID, &x.name, text{&x.keyID})
	}, `SELECT gs.grant_id, gs.slot, gs.key_id FROM grant_slots gs JOIN grants g ON g.id = gs.grant_id
	     WHERE g.org_id = ? AND g.workspace_id = ? ORDER BY gs.grant_id, gs.slot`, orgID, workspaceID)
	if err != nil {
		return nil, err
	}
	mergeGrantSlots(grants, slots)
	return grants, nil
}

// ---------------------------------------------------------------------------
// Cabinets.

// CabinetTool is a tool in a cabinet, with its slots filled from the
// cabinet's keys.
type CabinetTool struct {
	ToolID  string
	SlotMap map[string]*string
}

// Cabinet is a workspace-scoped bundle of keys and tools with its own
// access list.
type Cabinet struct {
	ID             string
	WorkspaceID    string
	Name           string
	CreatedBy      Ref    // zero when unknown
	ManagedBy      string // a user ID; "" means the workspace's admins manage it
	AccessEveryone bool
	Access         []Ref // players, when AccessEveryone is false
	KeyIDs         []string
	Tools          []CabinetTool
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Rev            int64
}

const cabinetQuery = `SELECT id, workspace_id, name, created_by_kind, created_by_id, managed_by, access_everyone,
       created_at, updated_at, rev FROM cabinets`

func scanCabinet(r rowScanner) (Cabinet, error) {
	var c Cabinet
	err := r.Scan(&c.ID, &c.WorkspaceID, &c.Name, text{&c.CreatedBy.Kind}, text{&c.CreatedBy.ID}, text{&c.ManagedBy},
		boolean{&c.AccessEveryone}, at{&c.CreatedAt}, at{&c.UpdatedAt}, &c.Rev)
	return c, err
}

// ListCabinets lists a workspace's cabinets, by name, with their keys,
// tools and access lists.
func (s *Store) ListCabinets(ctx context.Context, orgID, workspaceID string) ([]Cabinet, error) {
	cabs, err := list(ctx, s, scanCabinet, cabinetQuery+" WHERE org_id = ? AND workspace_id = ? ORDER BY name, id", orgID, workspaceID)
	if err != nil {
		return nil, err
	}
	return cabs, s.fillCabinets(ctx, orgID, cabs)
}

// GetCabinet loads one of an organization's cabinets.
func (s *Store) GetCabinet(ctx context.Context, orgID, cabinetID string) (Cabinet, error) {
	c, err := one(ctx, s, scanCabinet, cabinetQuery+" WHERE org_id = ? AND id = ?", orgID, cabinetID)
	if err != nil {
		return Cabinet{}, err
	}
	cabs := []Cabinet{c}
	err = s.fillCabinets(ctx, orgID, cabs)
	return cabs[0], err
}

func (s *Store) fillCabinets(ctx context.Context, orgID string, cabs []Cabinet) error {
	if len(cabs) == 0 {
		return nil
	}
	byID := map[string]*Cabinet{}
	ids := make([]any, 0, len(cabs)+1)
	ids = append(ids, orgID)
	for i := range cabs {
		cabs[i].Access, cabs[i].KeyIDs, cabs[i].Tools = []Ref{}, []string{}, []CabinetTool{}
		byID[cabs[i].ID] = &cabs[i]
		ids = append(ids, cabs[i].ID)
	}
	in := " AND cabinet_id IN (" + strings.TrimSuffix(strings.Repeat("?, ", len(cabs)), ", ") + ")"

	keys, err := list(ctx, s, scanPair, "SELECT cabinet_id, key_id FROM cabinet_keys WHERE org_id = ?"+in+" ORDER BY cabinet_id, key_id", ids...)
	if err != nil {
		return err
	}
	for _, p := range keys {
		byID[p[0]].KeyIDs = append(byID[p[0]].KeyIDs, p[1])
	}

	type access struct {
		cabinetID string
		player    Ref
	}
	players, err := list(ctx, s, func(r rowScanner) (access, error) {
		var a access
		return a, r.Scan(&a.cabinetID, &a.player.Kind, &a.player.ID)
	}, "SELECT cabinet_id, member_kind, member_id FROM cabinet_access WHERE org_id = ?"+in+" ORDER BY cabinet_id, member_kind DESC, member_id", ids...)
	if err != nil {
		return err
	}
	for _, a := range players {
		byID[a.cabinetID].Access = append(byID[a.cabinetID].Access, a.player)
	}

	type tool struct {
		cabinetID string
		tool      CabinetTool
	}
	tools, err := list(ctx, s, func(r rowScanner) (tool, error) {
		var t tool
		return t, r.Scan(&t.cabinetID, &t.tool.ToolID, jsonInto{&t.tool.SlotMap})
	}, "SELECT cabinet_id, tool_id, slot_map FROM cabinet_tools WHERE org_id = ?"+in+" ORDER BY cabinet_id, tool_id", ids...)
	if err != nil {
		return err
	}
	for _, t := range tools {
		byID[t.cabinetID].Tools = append(byID[t.cabinetID].Tools, t.tool)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Connectors and enrollment tokens.

// ComponentHealth is a connector's or sidecar's last reported state.
type ComponentHealth struct {
	Status           string // healthy, degraded or offline
	LastSeenAt       *time.Time
	IP               string
	MissedHeartbeats int
	ReportedVersion  string
	ConfigVersion    *int
}

// Connector is a vault connector or a sidecar. Its credential is only its
// prefix and last four.
type Connector struct {
	ID                   string
	OrgID                string
	Kind                 string // vault or sidecar
	Name                 string
	Status               string // active or revoked
	WorkspaceID          string
	AgentID              string
	EnrolledAt           time.Time
	EnrolledBy           Ref
	EnrolledByName       string
	EnrolledFromIP       string
	CredentialPrefix     string
	CredentialLast4      string
	CredentialRotatedAt  *time.Time
	CredentialGraceUntil *time.Time
	Install              json.RawMessage
	Health               ComponentHealth
	CachePolicy          json.RawMessage
	EgressTunnel         string
	EgressRegion         string
	Local                json.RawMessage
	AppMode              string
	Proxy                json.RawMessage
	CreatedAt            time.Time
	UpdatedAt            time.Time
	RevokedAt            *time.Time
	Rev                  int64
	// RoutesStoreIDs are the stores routed through this connector (derived
	// from stores.route_connector_id). Non-empty makes it org-scoped.
	RoutesStoreIDs []string
}

// Scope is "org" when the connector routes an org store, else "workspace".
func (c Connector) Scope() string {
	if len(c.RoutesStoreIDs) > 0 {
		return "org"
	}
	return "workspace"
}

const connectorQuery = `SELECT c.id, c.org_id, c.kind, c.name, c.status, c.workspace_id, c.agent_id, c.enrolled_at,
       c.enrolled_by_kind, c.enrolled_by_id, u.name, c.enrolled_from_ip, c.credential_prefix, c.credential_last4,
       c.credential_rotated_at, c.credential_grace_until, c.install, c.health_status, c.health_last_seen_at,
       c.health_ip, c.health_missed_heartbeats, c.health_reported_version, c.health_config_version,
       c.cache_policy, c.egress_tunnel, c.egress_region, c.local, c.app_mode, c.proxy, c.created_at,
       c.updated_at, c.revoked_at, c.rev
  FROM connectors c LEFT JOIN users u ON c.enrolled_by_kind = 'user' AND u.id = c.enrolled_by_id`

func scanConnector(r rowScanner) (Connector, error) {
	var c Connector
	h := &c.Health
	err := r.Scan(&c.ID, &c.OrgID, &c.Kind, &c.Name, &c.Status, text{&c.WorkspaceID}, text{&c.AgentID},
		at{&c.EnrolledAt}, &c.EnrolledBy.Kind, &c.EnrolledBy.ID, text{&c.EnrolledByName}, text{&c.EnrolledFromIP},
		text{&c.CredentialPrefix}, text{&c.CredentialLast4}, atOrNil{&c.CredentialRotatedAt},
		atOrNil{&c.CredentialGraceUntil}, rawJSON{&c.Install}, &h.Status, atOrNil{&h.LastSeenAt}, text{&h.IP},
		&h.MissedHeartbeats, text{&h.ReportedVersion}, intOrNil{&h.ConfigVersion}, rawJSON{&c.CachePolicy},
		text{&c.EgressTunnel}, text{&c.EgressRegion}, rawJSON{&c.Local}, text{&c.AppMode}, rawJSON{&c.Proxy},
		at{&c.CreatedAt}, at{&c.UpdatedAt}, atOrNil{&c.RevokedAt}, &c.Rev)
	c.RoutesStoreIDs = []string{}
	return c, err
}

// ListConnectors lists an organization's connectors and sidecars, by name.
func (s *Store) ListConnectors(ctx context.Context, orgID string) ([]Connector, error) {
	cs, err := list(ctx, s, scanConnector, connectorQuery+" WHERE c.org_id = ? ORDER BY c.name, c.id", orgID)
	if err != nil {
		return nil, err
	}
	return cs, s.fillRoutes(ctx, orgID, cs)
}

// GetConnector loads one of an organization's connectors or sidecars.
func (s *Store) GetConnector(ctx context.Context, orgID, connectorID string) (Connector, error) {
	c, err := one(ctx, s, scanConnector, connectorQuery+" WHERE c.org_id = ? AND c.id = ?", orgID, connectorID)
	if err != nil {
		return Connector{}, err
	}
	cs := []Connector{c}
	err = s.fillRoutes(ctx, orgID, cs)
	return cs[0], err
}

func (s *Store) fillRoutes(ctx context.Context, orgID string, cs []Connector) error {
	routes, err := list(ctx, s, scanPair, `SELECT route_connector_id, id FROM stores
	 WHERE org_id = ? AND route_connector_id IS NOT NULL AND status <> 'removed' ORDER BY route_connector_id, id`, orgID)
	if err != nil {
		return err
	}
	byID := map[string]*Connector{}
	for i := range cs {
		byID[cs[i].ID] = &cs[i]
	}
	for _, p := range routes {
		if c := byID[p[0]]; c != nil {
			c.RoutesStoreIDs = append(c.RoutesStoreIDs, p[1])
		}
	}
	return nil
}

// EnrollmentToken is a single-use kh_enr_ token record: only its prefix and
// last four, and the configuration it enrolls.
type EnrollmentToken struct {
	ID                    string
	ComponentKind         string
	TokenPrefix           string
	TokenLast4            string
	Status                string // pending, consumed, expired or revoked
	Intent                json.RawMessage
	CreatedAt             time.Time
	CreatedBy             Ref
	ExpiresAt             time.Time
	ConsumedAt            *time.Time
	ConsumedByComponentID string
	RevokedAt             *time.Time
}

// ListEnrollmentTokens lists an organization's enrollment tokens, newest
// first.
func (s *Store) ListEnrollmentTokens(ctx context.Context, orgID string) ([]EnrollmentToken, error) {
	return list(ctx, s, func(r rowScanner) (EnrollmentToken, error) {
		var e EnrollmentToken
		return e, r.Scan(&e.ID, &e.ComponentKind, &e.TokenPrefix, &e.TokenLast4, &e.Status, rawJSON{&e.Intent},
			at{&e.CreatedAt}, &e.CreatedBy.Kind, &e.CreatedBy.ID, at{&e.ExpiresAt}, atOrNil{&e.ConsumedAt},
			text{&e.ConsumedByComponentID}, atOrNil{&e.RevokedAt})
	}, `SELECT id, component_kind, token_prefix, token_last4, status, intent, created_at, created_by_kind,
	           created_by_id, expires_at, consumed_at, consumed_by_component_id, revoked_at
	      FROM enrollment_tokens WHERE org_id = ? ORDER BY created_at DESC, id`, orgID)
}

// ---------------------------------------------------------------------------
// Settings and the audit log.

// Notifications are the alerts an organization turned on.
type Notifications struct {
	ConnectorOffline   bool `json:"connectorOffline"`
	StoreUnhealthy     bool `json:"storeUnhealthy"`
	VerificationFailed bool `json:"verificationFailed"`
	TokenExpiring      bool `json:"tokenExpiring"`
	BlockedSpike       bool `json:"blockedSpike"`
	MemberJoined       bool `json:"memberJoined"`
}

// OrgSettings is Keyhole's per-organization settings.
type OrgSettings struct {
	OrgID                string
	ConfigVersion        int64
	Notifications        Notifications
	AlertWebhookURL      string
	DefaultGraceSeconds  *int
	ChecklistDismissedAt *time.Time
	ConfigDownloadedAt   *time.Time
	UpdatedAt            time.Time
}

// GetOrgSettings loads an organization's Keyhole settings.
func (s *Store) GetOrgSettings(ctx context.Context, orgID string) (OrgSettings, error) {
	return one(ctx, s, func(r rowScanner) (OrgSettings, error) {
		var o OrgSettings
		n := &o.Notifications
		return o, r.Scan(&o.OrgID, &o.ConfigVersion, boolean{&n.ConnectorOffline}, boolean{&n.StoreUnhealthy},
			boolean{&n.VerificationFailed}, boolean{&n.TokenExpiring}, boolean{&n.BlockedSpike},
			boolean{&n.MemberJoined}, text{&o.AlertWebhookURL}, intOrNil{&o.DefaultGraceSeconds},
			atOrNil{&o.ChecklistDismissedAt}, atOrNil{&o.ConfigDownloadedAt}, at{&o.UpdatedAt})
	}, `SELECT org_id, config_version, notify_connector_offline, notify_store_unhealthy, notify_verification_failed,
	           notify_token_expiring, notify_blocked_spike, notify_member_joined, alert_webhook_url,
	           default_grace_seconds, checklist_dismissed_at, config_downloaded_at, updated_at
	      FROM org_settings WHERE org_id = ?`, orgID)
}

// AuditActor is who did something, as they were named at the time.
type AuditActor struct {
	Kind     string
	ID       string
	Label    string
	Via      Ref
	ViaLabel string
}

// AuditEvent is one row of the audit log: a shared row both apps show, or
// a Keyhole-only row with Detail (IDs and metadata, never values).
type AuditEvent struct {
	ID            string
	OrgID         string
	WorkspaceID   string
	App           string
	Shared        bool
	Type          string
	Severity      string
	At            time.Time
	Actor         AuditActor
	Subject       Ref
	SubjectLabel  string
	Object        string
	Result        string
	Reason        string
	Trk           string
	CorrelationID string
	CausationID   string
	Changes       json.RawMessage
	Detail        json.RawMessage
	LinkLabel     string
	LinkTo        string
}

// AuditFilter narrows ListAuditEvents. Zero fields don't filter.
type AuditFilter struct {
	WorkspaceID string
	ActorID     string
	SubjectID   string
	Type        string
	Before      time.Time // only events strictly before this time (paging)
	Limit       int       // default and maximum 500
}

// ListAuditEvents lists an organization's audit events, newest first.
func (s *Store) ListAuditEvents(ctx context.Context, orgID string, f AuditFilter) ([]AuditEvent, error) {
	query := `SELECT id, org_id, workspace_id, app, shared, type, severity, at, actor_kind, actor_id, actor_label,
	                 actor_via_kind, actor_via_id, actor_via_label, subject_kind, subject_id, subject_label, object,
	                 result, reason, trk, correlation_id, causation_id, changes, detail, link_label, link_to
	            FROM audit_events WHERE org_id = ?`
	args := []any{orgID}
	for _, c := range []struct{ col, v string }{
		{"workspace_id", f.WorkspaceID}, {"actor_id", f.ActorID}, {"subject_id", f.SubjectID}, {"type", f.Type},
	} {
		if c.v != "" {
			query += " AND " + c.col + " = ?"
			args = append(args, c.v)
		}
	}
	if !f.Before.IsZero() {
		query += " AND at < ?"
		args = append(args, millis(f.Before))
	}
	query += " ORDER BY at DESC, id DESC LIMIT ?"
	args = append(args, clampLimit(f.Limit))
	return list(ctx, s, func(r rowScanner) (AuditEvent, error) {
		var e AuditEvent
		a := &e.Actor
		return e, r.Scan(&e.ID, &e.OrgID, text{&e.WorkspaceID}, &e.App, boolean{&e.Shared}, &e.Type, &e.Severity,
			at{&e.At}, &a.Kind, text{&a.ID}, &a.Label, text{&a.Via.Kind}, text{&a.Via.ID}, text{&a.ViaLabel},
			text{&e.Subject.Kind}, text{&e.Subject.ID}, text{&e.SubjectLabel}, &e.Object, &e.Result,
			text{&e.Reason}, &e.Trk, text{&e.CorrelationID}, text{&e.CausationID}, rawJSON{&e.Changes},
			rawJSON{&e.Detail}, text{&e.LinkLabel}, text{&e.LinkTo})
	}, query, args...)
}

// GetNotificationPreferences returns the alerts a person gets in an
// organization: their own choices if they saved any (personal = true),
// otherwise the organization's defaults from org_settings.
func (s *Store) GetNotificationPreferences(ctx context.Context, orgID, userID string) (n Notifications, personal bool, err error) {
	n, err = one(ctx, s, func(r rowScanner) (Notifications, error) {
		var n Notifications
		return n, r.Scan(boolean{&n.ConnectorOffline}, boolean{&n.StoreUnhealthy}, boolean{&n.VerificationFailed},
			boolean{&n.TokenExpiring}, boolean{&n.BlockedSpike}, boolean{&n.MemberJoined})
	}, `SELECT connector_offline, store_unhealthy, verification_failed, token_expiring, blocked_spike, member_joined
	      FROM notification_preferences WHERE org_id = ? AND user_id = ?`, orgID, userID)
	if err == nil {
		return n, true, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Notifications{}, false, err
	}
	set, err := s.GetOrgSettings(ctx, orgID)
	return set.Notifications, false, err
}
