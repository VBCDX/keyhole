package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestGetSessionRefusesRevokedAndIdleExpired seeds the revocation and idle
// columns directly — nothing in the store writes them yet — and checks the
// authentication path: revoked and idle-expired rows must not load, while
// live rows (including one with a future idle expiry) still do.
//
// It opens both backends through upgradeBackends, the same fresh SQLite and
// Postgres databases the upgrade tests use; storetest cannot be imported
// here because it imports this package.
func TestGetSessionRefusesRevokedAndIdleExpired(t *testing.T) {
	for name, dbURL := range upgradeBackends(t) {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			st, err := Open(ctx, dbURL)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()

			u, err := st.CreateLocalUser(ctx, "sessions@example.com", "S", "hash")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			for _, id := range []string{"live", "revoked", "idle-dead", "idle-live"} {
				sess := Session{IDHash: id, UserID: u.ID, Method: "local", CSRFToken: "csrf-" + id,
					CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
				if err := st.CreateSession(ctx, sess); err != nil {
					t.Fatal(err)
				}
			}
			seed := func(query, id string, at time.Time) {
				t.Helper()
				if _, err := st.db.ExecContext(ctx, st.q(query), millis(at), id); err != nil {
					t.Fatalf("seed %s: %v", id, err)
				}
			}
			seed("UPDATE sessions SET revoked_at = ? WHERE id_hash = ?", "revoked", now)
			seed("UPDATE sessions SET idle_expires_at = ? WHERE id_hash = ?", "idle-dead", now.Add(-time.Minute))
			seed("UPDATE sessions SET idle_expires_at = ? WHERE id_hash = ?", "idle-live", now.Add(time.Minute))

			for _, id := range []string{"live", "idle-live"} {
				if _, _, err := st.GetSession(ctx, id, now); err != nil {
					t.Errorf("GetSession(%s) = %v; want a live session", id, err)
				}
			}
			for _, id := range []string{"revoked", "idle-dead"} {
				if _, _, err := st.GetSession(ctx, id, now); !errors.Is(err, ErrNotFound) {
					t.Errorf("GetSession(%s) = %v; want ErrNotFound", id, err)
				}
			}
		})
	}
}
