package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/VBCDX/keyhole/internal/auth"
	"github.com/VBCDX/keyhole/internal/config"
	"github.com/VBCDX/keyhole/internal/store"
	"github.com/VBCDX/keyhole/internal/store/storetest"
)

// TestCreateLocalUserPreservesErrExists pins the sentinel the bootstrap
// convergence relies on: the friendly duplicate message must still satisfy
// errors.Is(err, store.ErrExists) so callers can detect it.
func TestCreateLocalUserPreservesErrExists(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)

		if _, err := createLocalUser(ctx, st, "dup@example.com", "Dup", "a long enough password"); err != nil {
			t.Fatal(err)
		}
		_, err := createLocalUser(ctx, st, "Dup@Example.com", "Dup", "another long enough password")
		if !errors.Is(err, store.ErrExists) {
			t.Fatalf("duplicate create error = %v; want store.ErrExists", err)
		}
		// The message doesn't echo the email (case-blind: it
		// is lowercased first).
		if strings.Contains(strings.ToLower(err.Error()), "dup@example.com") {
			t.Fatalf("duplicate create echoed the email: %v", err)
		}
	})
}

// TestBootstrapConvergesOnExistingAccount is the deterministic form of the
// concurrent first start: the empty-database check saw no users, but another
// instance created the account before this one's insert. The creation step
// must treat the existing account as success and converge, not fail.
func TestBootstrapConvergesOnExistingAccount(t *testing.T) {
	storetest.Run(t, func(t *testing.T, b storetest.Backend) {
		ctx := context.Background()
		st := storetest.Open(t, b)
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		boot := config.Bootstrap{Email: "admin@example.com", Name: "Admin", Password: "bootstrap password"}

		// The winner creates the account; the loser then runs the create
		// step its own empty check authorized before the winner committed.
		if err := createBootstrapAccount(ctx, st, boot, log); err != nil {
			t.Fatal(err)
		}
		if err := createBootstrapAccount(ctx, st, boot, log); err != nil {
			t.Fatalf("losing instance did not converge: %v", err)
		}
		// A different password on the losing instance must not overwrite
		// the winner's account either.
		loser := boot
		loser.Password = "a different bootstrap password"
		if err := createBootstrapAccount(ctx, st, loser, log); err != nil {
			t.Fatalf("losing instance with a different password did not converge: %v", err)
		}
		if n, err := st.CountUsers(ctx); err != nil || n != 1 {
			t.Fatalf("users = %d, %v after convergence; want 1", n, err)
		}
		_, hash, err := st.LocalCredentials(ctx, boot.Email)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := auth.VerifyPassword(hash, boot.Password); err != nil || !ok {
			t.Fatalf("the winner's credentials must survive the losers: ok=%v err=%v", ok, err)
		}
	})
}
