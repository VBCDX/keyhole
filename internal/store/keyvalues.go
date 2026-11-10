package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/VBCDX/keyhole/internal/envelope"
)

// ErrNotLocal is returned when a value is written to or read from a key
// that is not in a Local store, or is not active.
var ErrNotLocal = errors.New("store: not an active Local key")

// PutLocalValue seals value under kek and stores it as the next version
// of the Local key keyID. The previous version is
// marked superseded, the key's address points at the new version, and an
// admin audit event records the write (the key and the version, never
// the value or its length). It returns the new version, or ErrExists when
// a concurrent put of the same key took that version first.
func (s *Store) PutLocalValue(ctx context.Context, kek *envelope.KEK, orgID, keyID string, value []byte, actorID string, now time.Time) (int, error) {
	var version int
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var provider, status, name string
		var retention int
		err := tx.QueryRowContext(ctx, s.q(`SELECT k.provider, k.status, k.name, o.audit_retention_days
			FROM keys k JOIN orgs o ON o.id = k.org_id WHERE k.org_id = ? AND k.id = ?`), orgID, keyID).
			Scan(&provider, &status, &name, &retention)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if provider != "local" || status != "active" {
			return ErrNotLocal
		}
		var last sql.NullInt64
		if err := tx.QueryRowContext(ctx, s.q(`SELECT MAX(version) FROM local_key_values WHERE org_id = ? AND key_id = ?`),
			orgID, keyID).Scan(&last); err != nil {
			return err
		}
		version = int(last.Int64) + 1
		env, err := kek.Seal(envelope.AAD(orgID, keyID, version), value)
		if err != nil {
			return err
		}
		envJSON, err := json.Marshal(env)
		if err != nil {
			return err
		}
		at := millis(now)
		valueID := newPrefixedID("lkv")
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE local_key_values SET superseded_at = ?
			WHERE org_id = ? AND key_id = ? AND superseded_at IS NULL`), at, orgID, keyID); err != nil {
			return err
		}
		if err := s.insertLocalValue(ctx, tx, valueID, orgID, keyID, version, string(envJSON), len(value), at, actorID); err != nil {
			return err
		}
		addr, err := json.Marshal(map[string]any{"localValueId": valueID, "version": version})
		if err != nil {
			return err
		}
		rotated := any(nil)
		if version > 1 {
			rotated = at
		}
		// keys.length is cleared for a Local key: GetKey and ListKeys return
		// it, and no read may say how long a value is.
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE keys SET address = ?, length = NULL, rotated_at = COALESCE(?, rotated_at),
			updated_at = ?, updated_by_kind = 'system', updated_by_id = ?, rev = rev + 1
			WHERE org_id = ? AND id = ?`),
			string(addr), rotated, at, actorID, orgID, keyID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, s.q(`INSERT INTO audit_events
			(id, org_id, app, shared, type, severity, at, actor_kind, actor_id, actor_label,
			 subject_kind, subject_id, subject_label, object, result, trk, expire_at)
			VALUES (?, ?, 'keyhole', FALSE, 'admin', 'ok', ?, 'system', ?, ?, 'key', ?, ?, ?, ?, ?, ?)`),
			newPrefixedID("ev"), orgID, at, actorID, actorID, keyID, name,
			"key "+name, fmt.Sprintf("value written (version %d)", version),
			newPrefixedID("trk"), millis(now.AddDate(0, 0, retention)))
		return err
	})
	if err != nil {
		return 0, err
	}
	return version, nil
}

// insertLocalValue writes one value row. A row already at that version
// means another put of the same key won the race: ErrExists, and the
// caller's transaction rolls back.
func (s *Store) insertLocalValue(ctx context.Context, tx *sql.Tx, id, orgID, keyID string, version int, env string, length int, at int64, actorID string) error {
	res, err := tx.ExecContext(ctx, s.q(`INSERT INTO local_key_values
		(id, org_id, key_id, version, envelope, length, created_at, created_by_kind, created_by_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'system', ?)
		ON CONFLICT (org_id, key_id, version) DO NOTHING`),
		id, orgID, keyID, version, env, length, at, actorID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrExists
	}
	return nil
}

// LocalValue opens the current version of the Local key keyID. It is the
// key-resolution path and nothing else calls it: no admin query or API
// response returns a value.
func (s *Store) LocalValue(ctx context.Context, kek *envelope.KEK, orgID, keyID string) ([]byte, int, error) {
	var raw string
	var version int
	err := s.db.QueryRowContext(ctx, s.q(`SELECT v.envelope, v.version FROM local_key_values v
		JOIN keys k ON k.id = v.key_id AND k.org_id = v.org_id
		WHERE v.org_id = ? AND v.key_id = ? AND v.superseded_at IS NULL
		  AND k.provider = 'local' AND k.status = 'active'`), orgID, keyID).Scan(&raw, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	var env envelope.Envelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, 0, envelope.ErrOpen
	}
	value, err := kek.Open(env, envelope.AAD(orgID, keyID, version))
	if err != nil {
		return nil, 0, err
	}
	return value, version, nil
}
