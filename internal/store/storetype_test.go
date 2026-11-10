package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/VBCDX/keyhole/internal/store"
)

type kindOnly string

func (k kindOnly) Kind() string { return string(k) }
func (k kindOnly) Value(context.Context, string, string) ([]byte, int, error) {
	return []byte("v-" + string(k)), 1, nil
}

// AC1: one registry, by kind; no kind twice, none empty.
func TestStoreTypesRegistry(t *testing.T) {
	if _, err := store.NewStoreTypes(kindOnly("a"), kindOnly("a")); err == nil {
		t.Error("two types for one kind were accepted")
	}
	if _, err := store.NewStoreTypes(kindOnly("")); err == nil {
		t.Error("an empty kind was accepted")
	}
	r, err := store.NewStoreTypes(kindOnly("a"), kindOnly("b"))
	if err != nil {
		t.Fatal(err)
	}
	if v, n, err := r.Value(context.Background(), "b", "org", "key"); err != nil || string(v) != "v-b" || n != 1 {
		t.Errorf("b: %q %d %v", v, n, err)
	}
	if _, _, err := r.Value(context.Background(), "c", "org", "key"); !errors.Is(err, store.ErrNoStoreType) {
		t.Errorf("an unregistered kind: %v, want ErrNoStoreType", err)
	}
	if store.LocalKind != "local" {
		t.Errorf("LocalKind = %q", store.LocalKind)
	}
	var s *store.Store
	if k := s.LocalType(nil).Kind(); k != store.LocalKind {
		t.Errorf("the Local store's kind = %q", k)
	}
}
