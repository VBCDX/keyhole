package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Queries the HTTP API needs on top of the screen reads in read.go.

// GetUserOrg returns a user's membership of one active organization, or
// ErrNotFound when they are not a member or the organization is not
// active. The caller decides what the membership status allows.
func (s *Store) GetUserOrg(ctx context.Context, userID, orgID string) (UserOrg, error) {
	return one(ctx, s, func(r rowScanner) (UserOrg, error) {
		var u UserOrg
		return u, r.Scan(append(orgDest(&u.Org), &u.Role, &u.Status, atOrNil{&u.JoinedAt})...)
	}, "SELECT "+orgCols+", m.role, m.status, m.joined_at FROM org_memberships m JOIN orgs o ON o.id = m.org_id"+
		" WHERE m.user_id = ? AND m.org_id = ? AND o.status = 'active'", userID, orgID)
}

// KeyUsageCounts counts, per key in an organization, the tool slots it
// fills: workspace grants and cabinet tools. Keys that fill no slot are
// absent from the map.
func (s *Store) KeyUsageCounts(ctx context.Context, orgID string) (map[string]int, error) {
	counts := map[string]int{}
	grants, err := list(ctx, s, scanString, "SELECT key_id FROM grant_slots WHERE org_id = ? AND key_id IS NOT NULL", orgID)
	if err != nil {
		return nil, err
	}
	for _, id := range grants {
		counts[id]++
	}
	maps, err := list(ctx, s, func(r rowScanner) (map[string]*string, error) {
		var m map[string]*string
		return m, r.Scan(jsonInto{&m})
	}, "SELECT slot_map FROM cabinet_tools WHERE org_id = ?", orgID)
	if err != nil {
		return nil, err
	}
	for _, m := range maps {
		for _, id := range m {
			if id != nil && *id != "" {
				counts[*id]++
			}
		}
	}
	return counts, nil
}

// ErrBadMember is returned for a member kind other than user or agent, or
// a member id that isn't a single id (1-64 of A-Z a-z 0-9 _ -).
var ErrBadMember = errors.New("store: a member is kind user or agent, with an id of 1-64 letters, digits, _ or -")

func memberOK(kind, id string) bool {
	if kind != "user" && kind != "agent" {
		return false
	}
	if id == "" || len(id) > 64 {
		return false
	}
	for _, b := range []byte(id) {
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

// AddWorkspaceMember makes (kind, id) a plain member of workspace wsID in
// orgID, as a resolve names its caller: kind and id are the assertion's
// `kind` and `sub` (a vbcdx-control user or agent). It reports whether
// anything changed and writes an admin audit row only when it did; the
// insert itself decides, so a repeat or a concurrent add changes nothing
// twice. An unknown or inactive workspace, or one in another org, is
// ErrNotFound.
func (s *Store) AddWorkspaceMember(ctx context.Context, orgID, wsID, kind, id, actorID string, now time.Time) (bool, error) {
	if !memberOK(kind, id) {
		return false, ErrBadMember
	}
	return s.memberChange(ctx, orgID, wsID, kind, id, actorID, now, "member added", func(tx *sql.Tx, at int64) (sql.Result, error) {
		return tx.ExecContext(ctx, s.q(`INSERT INTO workspace_memberships
			(id, org_id, workspace_id, member_kind, member_id, role, added_at, added_by_kind, added_by_id, updated_at, rev)
			VALUES (?, ?, ?, ?, ?, 'member', ?, 'system', ?, ?, 0)
			ON CONFLICT (org_id, workspace_id, member_kind, member_id) DO NOTHING`),
			newPrefixedID("wm"), orgID, wsID, kind, id, at, actorID, at)
	})
}

// RemoveWorkspaceMember removes (kind, id) from workspace wsID in orgID; a
// resolve for them then finds no grant through it. As AddWorkspaceMember:
// changed or not, audited only on a change, ErrNotFound for the workspace.
// It removes a plain member only: a workspace_admin row is vbcdx-control's
// to change, so it is left as it is (reported as not changed).
func (s *Store) RemoveWorkspaceMember(ctx context.Context, orgID, wsID, kind, id, actorID string, now time.Time) (bool, error) {
	if !memberOK(kind, id) {
		return false, ErrBadMember
	}
	return s.memberChange(ctx, orgID, wsID, kind, id, actorID, now, "member removed", func(tx *sql.Tx, _ int64) (sql.Result, error) {
		return tx.ExecContext(ctx, s.q(`DELETE FROM workspace_memberships
			WHERE org_id = ? AND workspace_id = ? AND member_kind = ? AND member_id = ? AND role = 'member'`), orgID, wsID, kind, id)
	})
}

func (s *Store) memberChange(ctx context.Context, orgID, wsID, kind, id, actorID string, now time.Time, what string,
	change func(*sql.Tx, int64) (sql.Result, error)) (bool, error) {
	changed := false
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var retention int
		err := tx.QueryRowContext(ctx, s.q(`SELECT o.audit_retention_days FROM workspaces w JOIN orgs o ON o.id = w.org_id
			WHERE w.org_id = ? AND w.id = ? AND w.status = 'active' AND o.status = 'active'`), orgID, wsID).Scan(&retention)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		at := millis(now)
		res, err := change(tx, at)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO audit_events
			(id, org_id, workspace_id, app, shared, type, severity, at, actor_kind, actor_id, actor_label,
			 subject_kind, subject_id, subject_label, object, result, trk, expire_at)
			VALUES (?, ?, ?, 'keyhole', FALSE, 'admin', 'ok', ?, 'system', ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
			newPrefixedID("ev"), orgID, wsID, at, actorID, actorID, kind, id, id,
			kind+" "+id+" in workspace "+wsID, what, newPrefixedID("trk"), millis(now.AddDate(0, 0, retention))); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}
