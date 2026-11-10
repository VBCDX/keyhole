package store

import (
	"context"
	"database/sql"
)

// DBOf exposes the handle to this package's external tests only.
func DBOf(s *Store) *sql.DB { return s.db }

// QOf rebinds a query's ? placeholders for the store's backend, as the
// store does, for external tests' raw queries with arguments.
func QOf(s *Store, query string) string { return s.q(query) }

// InsertLocalValueForTest writes a placeholder value row at version, as a
// concurrent put that picked the same version would.
func InsertLocalValueForTest(ctx context.Context, s *Store, orgID, keyID string, version int) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		return s.insertLocalValue(ctx, tx, newPrefixedID("lkv"), orgID, keyID, version, "{}", 1, 0, "race")
	})
}

// PermissionRefusal exposes the EACCES refusal, so root CI (which opens
// everything) can test both of its messages.
func PermissionRefusal(p string, err error) error { return permissionRefusal(p, err) }
