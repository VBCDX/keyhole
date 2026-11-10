package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/VBCDX/keyhole/internal/envelope"
)

// StoreType is the contract every store type implements, the built-in Local
// store included (docs/stores.md). A key row's provider
// column names its type; resolve finds the type through StoreTypes and asks
// it for the key's current value. Nothing on the resolve path knows any
// particular type.
//
// An implementation runs inside the Keyhole server, so it makes no
// outbound call (architecture rule 4; the egress test enforces it): its
// values are in Keyhole's own database, or reach it some other way that
// isn't the server dialling a store. Its errors never carry a value.
type StoreType interface {
	// Kind is the provider value this type serves, e.g. "local".
	Kind() string
	// Value returns the current value of key keyID in org orgID and its
	// version, or ErrNotFound when the key has no current value. The caller
	// clears the bytes after use. It must be safe for concurrent use.
	Value(ctx context.Context, orgID, keyID string) (value []byte, version int, err error)
}

// ErrNoStoreType is returned for a key whose provider no registered store
// type serves. Resolve fails closed on it.
var ErrNoStoreType = errors.New("store: no store type serves this key's provider")

// StoreTypes is the registry of store types, by Kind.
type StoreTypes struct{ byKind map[string]StoreType }

// NewStoreTypes registers types. An empty or duplicate kind is refused.
func NewStoreTypes(types ...StoreType) (*StoreTypes, error) {
	r := &StoreTypes{byKind: make(map[string]StoreType, len(types))}
	for _, t := range types {
		k := t.Kind()
		if k == "" {
			return nil, errors.New("store: a store type has an empty kind")
		}
		if _, dup := r.byKind[k]; dup {
			return nil, fmt.Errorf("store: two store types serve %q", k)
		}
		r.byKind[k] = t
	}
	return r, nil
}

// Value asks the type registered for provider for the key's current value.
func (r *StoreTypes) Value(ctx context.Context, provider, orgID, keyID string) ([]byte, int, error) {
	t, ok := r.byKind[provider]
	if !ok {
		return nil, 0, ErrNoStoreType
	}
	return t.Value(ctx, orgID, keyID)
}

// LocalKind is the Local store's provider value.
const LocalKind = "local"

// localType is the built-in Local store: values in Keyhole's own database,
// each sealed under the KEK (docs/local-store.md).
type localType struct {
	s   *Store
	kek *envelope.KEK
}

// LocalType is the Local store as a StoreType, opening values with kek.
func (s *Store) LocalType(kek *envelope.KEK) StoreType { return localType{s: s, kek: kek} }

func (l localType) Kind() string { return LocalKind }

func (l localType) Value(ctx context.Context, orgID, keyID string) ([]byte, int, error) {
	return l.s.LocalValue(ctx, l.kek, orgID, keyID)
}
