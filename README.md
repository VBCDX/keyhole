# Keyhole

**Keyhole is a secrets broker.** It brokers access to secrets so applications get what they need
without the secrets ever being exposed to them.

Part of the [VBCDX](https://github.com/VBCDX) suite. **Status: in development** — this repo is a
published snapshot; canonical development happens on the VBCDX Forgejo.

---

## The idea

An application should be able to use a credential without ever holding it. Keyhole sits between the
two: it connects to the places secrets actually live, and hands out scoped, short-lived access
instead of copies.

Keyhole is **standalone first**. It ships its own web admin — UI and API — and stands up on its own
with `docker-compose up`. No centralized console, no dependency on the rest of the suite.

## Stores and connectors

Keyhole reads secrets from **stores**. The first supported store is
[OpenBao](https://openbao.org/), the open-source fork of Vault.

A store has to be reachable, and there are two honest answers to how:

| | |
|---|---|
| **It's public** | Keyhole reaches the store directly over the network. You allow-list Keyhole's egress addresses on your side. |
| **Through a Keyhole connector** | The store is private. You enroll a connector — a small agent that runs next to the store and dials **out** to Keyhole, so nothing inbound has to be opened. |

The connector exists because the common case is a vault that is deliberately not on the public
internet. Asking someone to expose it in order to secure it is the wrong trade.

## Connecting a store

The wizard is six steps, and each one is allowed to fail in a way you can act on.

1. **Basics** — name the store and point at it.
2. **How to reach it** — public, or through an enrolled connector. Picking a connector (`edge-01`,
   say) routes traffic through it rather than direct.
3. **Sign in** — AppRole is the first supported method.
4. **Secrets** — choose which paths this store exposes.
5. **Scope** — decide who and what may ask for them.
6. **Test** — Keyhole actually connects and reports what it found.

### Failure is part of the flow, not an afterthought

Two states matter enough to design for explicitly:

- **Sealed.** An OpenBao vault that has been restarted is sealed, and *sealed is not broken* — it is
  the expected state waiting for unseal. Keyhole says so and tells you what to do, rather than
  reporting a generic connection failure.
- **No route to the address.** Usually means the store is private after all. The error says exactly
  that, and points at the connector as the fix — because that is almost always what the person
  actually needs.

A store that passes step 6 is reported **healthy**, and unhealthy stores say which of the above
they are.

## Design commitments

- **The secret does not pass through the application.** That is the whole point; anything that
  compromises it is a bug.
- **Errors name the cause and the next action.** "Sealed — unseal it" beats "connection failed".
  A message that does not tell you what to do next is not an error message.
- **Outbound-only for private stores.** Enrolling a connector must never require an inbound hole.
- **Standalone.** Own web admin, own backend, own deploy. Runs with or without the suite.
- **Auth vs authz split.** Identity comes from the suite auth layer over an OIDC seam and the IdP is
  swappable; **workspace and module authorization belongs to Keyhole**, not the IdP.
- **Open core.** This module is FOSS. The commercial layer depends on it, never the reverse.

## Where the work happens

Canonical development is on the VBCDX Forgejo. **This GitHub repo is a published snapshot** — it is
pushed to, not developed in. Issues and PRs opened here may be moved.

## Status

Greenfield. The flows above are specified and under active design; the stack, auth and licence
decisions are still open. Nothing here is released yet.
