package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

func TestMigrationsAreIdempotent(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		storetest.Open(t, b)
		st := storetest.Open(t, b) // second open re-runs migrate with nothing to do
		if n, err := st.CountUsers(context.Background()); err != nil || n != 0 {
			t.Fatalf("CountUsers = %d, %v; want 0, nil", n, err)
		}
	})
}

func TestLocalUsers(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)

		u, err := st.CreateLocalUser(ctx, "a@example.com", "A", "hash-a")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateLocalUser(ctx, "a@example.com", "Other", "hash-b"); !errors.Is(err, store.ErrExists) {
			t.Fatalf("duplicate email: err = %v, want ErrExists", err)
		}
		if n, _ := st.CountUsers(ctx); n != 1 {
			t.Fatalf("a rejected duplicate left a user row behind: %d users", n)
		}

		id, hash, err := st.LocalCredentials(ctx, "a@example.com")
		if err != nil || id != u.ID || hash != "hash-a" {
			t.Fatalf("LocalCredentials = %q, %q, %v", id, hash, err)
		}
		if _, _, err := st.LocalCredentials(ctx, "nobody@example.com"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("unknown email: err = %v, want ErrNotFound", err)
		}
		if err := st.TouchLocalIdentity(ctx, "a@example.com"); err != nil {
			t.Fatal(err)
		}
		got, err := st.GetUser(ctx, u.ID)
		if err != nil || got.Email != "a@example.com" || got.Name != "A" {
			t.Fatalf("GetUser = %+v, %v", got, err)
		}
		if _, err := st.GetUser(ctx, "missing"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetUser(missing) err = %v", err)
		}
	})
}

func TestOIDCUsersLinkByIssuerAndSubject(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)

		local, err := st.CreateLocalUser(ctx, "same@example.com", "Local", "h")
		if err != nil {
			t.Fatal(err)
		}
		first, err := st.UpsertOIDCUser(ctx, "https://idp", "sub-1", "same@example.com", "Zitadel Name")
		if err != nil {
			t.Fatal(err)
		}
		if first.ID == local.ID {
			t.Fatal("an OIDC sign-in was linked to a local account by email; it must link by issuer+subject only")
		}
		again, err := st.UpsertOIDCUser(ctx, "https://idp", "sub-1", "new@example.com", "Renamed")
		if err != nil {
			t.Fatal(err)
		}
		if again.ID != first.ID || again.Email != "new@example.com" || again.Name != "Renamed" {
			t.Fatalf("second sign-in = %+v; want same user with refreshed profile", again)
		}
		other, err := st.UpsertOIDCUser(ctx, "https://other-idp", "sub-1", "", "")
		if err != nil || other.ID == first.ID {
			t.Fatalf("same subject from another issuer must be a different user: %+v, %v", other, err)
		}
	})
}

func TestConcurrentFirstOIDCSignInMakesOneUser(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		var wg sync.WaitGroup
		ids := make([]string, 8)
		errs := make([]error, 8)
		for i := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				u, err := st.UpsertOIDCUser(ctx, "https://idp", "racer", "r@example.com", "R")
				ids[i], errs[i] = u.ID, err
			}()
		}
		wg.Wait()
		for i := range ids {
			if errs[i] != nil {
				t.Fatalf("sign-in %d failed: %v", i, errs[i])
			}
			if ids[i] != ids[0] {
				t.Fatalf("concurrent first sign-ins produced different users: %v", ids)
			}
		}
		if n, _ := st.CountUsers(ctx); n != 1 {
			t.Fatalf("users = %d, want 1", n)
		}
	})
}

func TestSessions(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		u, err := st.CreateLocalUser(ctx, "s@example.com", "S", "h")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		live := store.Session{IDHash: "live", UserID: u.ID, Method: "local", CSRFToken: "c1",
			CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
		dead := store.Session{IDHash: "dead", UserID: u.ID, Method: "zitadel", CSRFToken: "c2", IDTokenHint: "tok",
			CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(-time.Hour)}
		for _, s := range []store.Session{live, dead} {
			if err := st.CreateSession(ctx, s); err != nil {
				t.Fatal(err)
			}
		}

		got, gu, err := st.GetSession(ctx, "live", now)
		if err != nil || got.UserID != u.ID || gu.Email != "s@example.com" || got.CSRFToken != "c1" || got.IDTokenHint != "" {
			t.Fatalf("GetSession(live) = %+v, %+v, %v", got, gu, err)
		}
		if got.CreatedAt.UnixMilli() != now.UnixMilli() {
			t.Fatalf("CreatedAt = %v, want %v", got.CreatedAt, now)
		}
		if _, _, err := st.GetSession(ctx, "dead", now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("expired session: err = %v, want ErrNotFound", err)
		}

		n, err := st.DeleteExpiredSessions(ctx, now)
		if err != nil || n != 1 {
			t.Fatalf("DeleteExpiredSessions = %d, %v; want 1", n, err)
		}
		if err := st.DeleteSession(ctx, "live"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := st.GetSession(ctx, "live", now); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("deleted session still loads: %v", err)
		}
		if err := st.DeleteSession(ctx, "live"); err != nil {
			t.Fatalf("deleting a missing session must not fail: %v", err)
		}
	})
}

func TestOpenRejectsUnknownScheme(t *testing.T) {
	for _, u := range []string{"mysql://x", "", "sqlite://"} {
		if _, err := store.Open(context.Background(), u); err == nil {
			t.Errorf("Open(%q) succeeded; want an error", u)
		}
	}
}
