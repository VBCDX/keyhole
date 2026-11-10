# Store types: the contract

A **store type** is where a key's value comes from. Every store type, the
built-in Local store included, implements the same contract, and Keyhole's
resolve path knows none of them in particular.

## How a key names its type

Each key row has a `provider`: the store type that holds its value. The Local
store's is `local`. When a sidecar resolves a slot, Keyhole finds the grant
and the key, then asks the store type registered for that key's `provider`
for the key's current value. It seals the value to the sidecar exactly as
described in [resolve.md](resolve.md).

A key whose `provider` no registered type serves is **refused**, with the same
reply as a key with no value (`404 not_found`); the audit row says
`refused: no store type`.

Every key granted for a slot counts, whatever its type: if two enabled grants
a caller holds name different keys for the same slot, the slot is ambiguous
and refused (`409 conflict`), even when one of the keys is a Local one.

## The contract

In Go, `internal/store`:

```go
type StoreType interface {
	// Kind is the provider value this type serves, e.g. "local".
	Kind() string
	// Value returns the current value of key keyID in org orgID and its
	// version, or store.ErrNotFound when the key has no current value.
	Value(ctx context.Context, orgID, keyID string) (value []byte, version int, err error)
}
```

An implementation must:

- **make no outbound call.** It runs inside the Keyhole server, and the
  server never calls out (architecture rule 4; Keyhole's egress test fails on
  a new network-capable dependency or import). Its values live in Keyhole's
  own database, or reach it some other way that isn't the server dialling a
  store.
- **never put a value in an error, a log line or a metric.** Resolve logs
  only the key id when a value can't be opened.
- **return `store.ErrNotFound`** when the key has no current value, so the
  caller gets `404 not_found` and nothing else.
- **be safe for concurrent use.** Resolves run in parallel.
- **give a version** that changes whenever the value does (the Local store
  counts puts). Resolve doesn't use it yet: it discards the version and
  seals the value alone. It is there for a later cache or audit.

The caller clears the returned bytes after sealing them.

Writing values isn't part of the contract. The Local store's values come in
through `keyhole keys put` ([local-store.md](local-store.md)); another type
can keep its values however it likes.

## Registering a store type

Store types are registered where the server starts, in
`cmd/keyhole/service.go`:

```go
stores, err := store.NewStoreTypes(st.LocalType(kek) /*, your type */)
```

`NewStoreTypes` refuses two types for one kind, and an empty kind. In this
release a store type is part of the Keyhole binary: adding one is a source
change (implement the interface, register it, build). Loading a store type
from outside the binary isn't supported.

## What exists today

| Kind | Where values live |
|---|---|
| `local` | Keyhole's own database, each value sealed under the KEK ([local-store.md](local-store.md)) |

External stores, such as OpenBao, are planned. Because the server never dials
out, a store whose values live elsewhere fits the contract only if its values
reach Keyhole without the server calling it (for example through the
sidecar, which runs in the agent's environment); that design isn't settled.

## Testing a store type

`internal/server/storetype_test.go` registers a store type that isn't the
Local store, points a key at it and resolves through the real handler. Copy
that shape: the sidecar must open your type's value, and the value must not
appear in the reply body or the log.
