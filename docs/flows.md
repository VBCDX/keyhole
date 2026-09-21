# Keyhole — connect-a-store flows

Specification for connecting an **OpenBao** store to Keyhole, covering both routes and the failure
states that matter. Derived from the Flows 2 & 3 design walkthrough.

Two flows are described, and they share every step except step 2:

- **Flow 2 — public store.** Keyhole reaches OpenBao directly. Includes the sealed-vault detour,
  because a restarted vault is sealed and that is the normal case, not an exception.
- **Flow 3 — private store via connector.** Traffic routes through an enrolled Keyhole connector.

---

## Step 1 — Basics

Name the store and give its address.

The name is how it will be referred to everywhere else, so it should be the name the team already
uses for that vault, not a new one invented here.

## Step 2 — How to reach it

The one step where the two flows diverge. Two options, presented as a choice with consequences
rather than a setting:

### "It's public"

Keyhole connects directly. The user allow-lists Keyhole's egress addresses on their side.

Appropriate when the store already accepts traffic from outside. **It must be explicit** — nothing
should silently attempt a direct connection to something the user believes is private.

### "Through a Keyhole connector"

Traffic routes through an enrolled connector that runs next to the store and dials **out** to
Keyhole.

The user picks from their enrolled connectors — `edge-01` and so on. If they have none, this is
where they enroll one.

**Outbound-only is the requirement, not an implementation detail.** The reason to run a connector is
that the store is not exposed; a connector that needed an inbound hole would defeat its own purpose.

## Step 3 — Sign in

**AppRole** is the first supported auth method.

Credentials entered here are used to authenticate Keyhole to the store. They are a credential like
any other and must be handled as one.

## Steps 4 and 5 — Secrets and scope

Choose which paths the store exposes, and who may ask for them.

The default must be narrow. A store connected with everything exposed is a store that will
eventually leak something nobody remembered was reachable.

## Step 6 — Test

Keyhole connects for real and reports what it found. A store that passes is marked **healthy**.

This step exists to be *capable of failing*. A test that always passes tells you nothing, and the
two failures below are the ones worth designing for.

---

## Failure states

### Sealed

An OpenBao vault that has restarted is **sealed**. It is reachable, the address is right, the
credentials may be right — it simply will not serve secrets until unsealed.

**Sealed is not broken.** The UI must say sealed, and say that it needs unsealing. Reporting this as
a generic connection failure sends the user to check the address and the credentials, both of which
are fine — which is worse than saying nothing.

Recovery: unseal the vault, re-run the test, store goes healthy.

### No route to address

The address does not answer.

Occasionally a typo. **Usually it means the store is private** and the direct route was the wrong
choice at step 2. So the message names both possibilities and points at the connector:

> Can't reach it. No route to address — check it, or connect through a Keyhole connector.

Offering the connector at the moment of failure matters: that is when the user learns their store is
not public, and the fix should be one step away rather than something to go and read about.

---

## Notes for implementation

- **Every failure names a cause and a next action.** The two above are the worked examples; the rule
  is general.
- **Step 2 is a decision, not a toggle.** Both options carry a consequence — allow-listing on one
  side, enrolling a connector on the other — and the UI should say so before the choice, not after
  it fails.
- **Do not paper over sealed.** It is a distinct, expected, recoverable state and deserves its own
  handling everywhere a store's health is shown.
