package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// DefaultResolveTTL is how long a sidecar may keep a resolved value when
// neither its key nor its store sets a cache TTL, in seconds.
const DefaultResolveTTL = 300

// ErrAmbiguous is returned when the caller's workspaces grant the same
// tool with different keys in one slot, or at different tool versions:
// Keyhole doesn't pick one.
var ErrAmbiguous = errors.New("store: the grants for this tool name different keys")

// ResolvedSlot is the grant a resolve found: which key fills the slot,
// and how long the sidecar may keep the value.
type ResolvedSlot struct {
	GrantID     string
	WorkspaceID string
	KeyID       string
	// Provider is the key's store type (keys.provider); resolve asks the
	// StoreType registered for it for the value.
	Provider string
	TTL      int
	// The tool as the grant uses it: its pinned version, or the latest
	// published one. The sidecar calls the service from this (P4).
	ToolVersion int
	Tool        ToolSnapshot
}

// ResolveSlot finds the key that fills slot of the published tool toolID
// for the caller (memberKind, memberID) in orgID. It
// needs an active org and workspace, the caller a member of it, an enabled
// grant, a published tool that isn't deleted, and a filled slot whose key
// is active. The key's store type is the caller's to resolve through
// StoreTypes; no type is special here. Anything less is ErrNotFound; grants
// that would name different keys are ErrAmbiguous.
func (s *Store) ResolveSlot(ctx context.Context, orgID, memberKind, memberID, toolID, slot string) (ResolvedSlot, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT g.id, g.workspace_id, k.id, k.provider,
		COALESCE(k.cache_ttl_seconds, st.cache_default_ttl_seconds), tv.version, tv.snapshot
		FROM grants g
		JOIN orgs o ON o.id = g.org_id AND o.status = 'active'
		JOIN workspaces w ON w.id = g.workspace_id AND w.org_id = g.org_id AND w.status = 'active'
		JOIN workspace_memberships m ON m.org_id = g.org_id AND m.workspace_id = g.workspace_id
		     AND m.member_kind = ? AND m.member_id = ?
		JOIN tools t ON t.id = g.tool_id AND t.org_id = g.org_id AND t.status = 'published' AND t.deleted_at IS NULL
		JOIN grant_slots gs ON gs.grant_id = g.id AND gs.org_id = g.org_id AND gs.slot = ?
		JOIN keys k ON k.id = gs.key_id AND k.org_id = g.org_id AND k.status = 'active'
		JOIN stores st ON st.id = k.store_id AND st.org_id = g.org_id
		JOIN tool_versions tv ON tv.tool_id = t.id AND tv.org_id = g.org_id
		     AND tv.version = COALESCE(g.pinned_version, t.published_version)
		WHERE g.org_id = ? AND g.tool_id = ? AND g.enabled = TRUE
		ORDER BY g.id`), memberKind, memberID, slot, orgID, toolID)
	if err != nil {
		return ResolvedSlot{}, err
	}
	defer rows.Close()
	var found []ResolvedSlot
	for rows.Next() {
		var r ResolvedSlot
		var ttl *int
		if err := rows.Scan(&r.GrantID, &r.WorkspaceID, &r.KeyID, &r.Provider, &ttl, &r.ToolVersion, jsonInto{&r.Tool}); err != nil {
			return ResolvedSlot{}, err
		}
		r.TTL = DefaultResolveTTL
		if ttl != nil {
			r.TTL = *ttl
		}
		found = append(found, r)
	}
	if err := rows.Err(); err != nil {
		return ResolvedSlot{}, err
	}
	if len(found) == 0 {
		return ResolvedSlot{}, ErrNotFound
	}
	for _, r := range found[1:] {
		if r.KeyID != found[0].KeyID || r.ToolVersion != found[0].ToolVersion {
			return ResolvedSlot{}, ErrAmbiguous
		}
	}
	return found[0], nil
}

// ResolveAudit is one resolve's audit row: who asked through which
// sidecar, for which tool and slot, and the outcome. Never the value, its
// length or the key's address.
type ResolveAudit struct {
	OrgID, WorkspaceID string
	ActorKind, ActorID string
	SidecarID          string
	ToolID, Slot       string
	OK                 bool
	Result             string // "resolved", or the refusal's class
	RequestID          string // the assertion's jti
}

// AuditResolve writes a resolve's audit row. An org that doesn't exist
// gets no row (there is nowhere to keep it).
func (s *Store) AuditResolve(ctx context.Context, a ResolveAudit, now time.Time) error {
	typ, sev := "access", "ok"
	if !a.OK {
		typ, sev = "blocked", "blocked"
	}
	kind := a.ActorKind
	if kind != "user" && kind != "agent" {
		kind = "system"
	}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO audit_events
		(id, org_id, workspace_id, app, shared, type, severity, at, actor_kind, actor_id, actor_label,
		 actor_via_kind, actor_via_id, actor_via_label, object, result, trk, correlation_id, expire_at)
		SELECT ?, o.id, ?, 'keyhole', FALSE, ?, ?, CAST(? AS BIGINT), ?, ?, ?, 'sidecar', ?, ?, ?, ?, ?, ?,
		       CAST(? AS BIGINT) + CAST(o.audit_retention_days AS BIGINT) * 86400000
		FROM orgs o WHERE o.id = ?`),
		// Postgres types a bare parameter in INSERT … SELECT as text, so the
		// two times are cast; SQLite ignores the casts.
		newPrefixedID("ev"), nullable(a.WorkspaceID), typ, sev, millis(now), kind, a.ActorID, a.ActorID,
		a.SidecarID, a.SidecarID, "tool "+a.ToolID+" slot "+a.Slot, a.Result, newPrefixedID("trk"),
		nullable(a.RequestID), millis(now), a.OrgID)
	return err
}

// SetGrantEnabled switches the grant grantID in orgID on or off.
// A disabled grant resolves nothing (ResolveSlot needs
// `enabled`); its slots and keys stay as they are, so enabling restores it.
// It reports whether anything changed, and writes an admin audit row only
// when it did. An unknown grant, or one in another org, is ErrNotFound.
func (s *Store) SetGrantEnabled(ctx context.Context, orgID, grantID string, enabled bool, actorID string, now time.Time) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var toolID string
		var retention int
		err := tx.QueryRowContext(ctx, s.q(`SELECT g.tool_id, o.audit_retention_days
			FROM grants g JOIN orgs o ON o.id = g.org_id WHERE g.org_id = ? AND g.id = ?`), orgID, grantID).
			Scan(&toolID, &retention)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		at := millis(now)
		// The UPDATE is the guard: it changes the row only if it isn't
		// already in that state, so a repeat, or two concurrent toggles
		// (Postgres, READ COMMITTED), can't both report a change, bump rev
		// twice or audit twice. The SELECT above only finds the grant, its
		// tool and the retention.
		res, err := tx.ExecContext(ctx, s.q(`UPDATE grants SET enabled = ?, updated_at = ?, updated_by_kind = 'system',
			updated_by_id = ?, rev = rev + 1 WHERE org_id = ? AND id = ? AND enabled <> ?`), enabled, at, actorID, orgID, grantID, enabled)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return nil // another toggle got there first
		}
		what := "grant disabled"
		if enabled {
			what = "grant enabled"
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO audit_events
			(id, org_id, app, shared, type, severity, at, actor_kind, actor_id, actor_label,
			 subject_kind, subject_id, subject_label, object, result, trk, expire_at)
			VALUES (?, ?, 'keyhole', FALSE, 'admin', 'ok', ?, 'system', ?, ?, 'grant', ?, ?, ?, ?, ?, ?)`),
			newPrefixedID("ev"), orgID, at, actorID, actorID, grantID, grantID,
			"grant "+grantID+" (tool "+toolID+")", what, newPrefixedID("trk"), millis(now.AddDate(0, 0, retention))); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}
