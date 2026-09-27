# Outbox and change-stream contract for shared events

Shared records live once, in `suite`. Both apps react to changes in them: to refuse the next call, to filter queued messages, to create or delete their own extension rows, and to invalidate caches on customer hosts. The outbox makes those reactions reliable without two-phase commit.

## 1. Writing a shared change

In one MongoDB transaction (write concern `majority`, read concern `snapshot`):

1. **Update the shared record,** guarded by the `rev` the writer read: `{_id, rev: n}` → `$set …, $inc {rev: 1}`. A mismatch aborts; the caller re-reads and shows the conflict.
2. **Insert one shared audit row** into `suite.audit_events`, with `shared: true`, `app`, actor, `changes` (before and after of shared fields only), and `trk`.
3. **Insert one outbox row** into `suite.outbox` (`outbox_event.schema.json`). Its fields:
   - `type` and `aggregate {kind, id, rev}`;
   - `payload` (shared fields only);
   - `auditEventId`, `trk` and `correlationId`;
   - `refusal`;
   - `publishedAt: null`.
4. **Same cluster: write the app's own consequences in the same transaction.**
   - For example, `dispatch.membership_access` for a new agent membership, or `keyhole.workspace_availability` for a new workspace.
   - Each consequence goes in an app-only audit row whose `causationId` is the shared audit row's `_id`.
   - Then insert `<app>.applied_events {_id: outboxId}`, so the projector skips this event later.

After commit, when `refusal` is true, the writer publishes the event to Redis at once (`kh:inv:{orgId}`, `dsp:inv:{orgId}`) instead of waiting for the relay. Publishing twice is harmless, because every consumer is idempotent.

## 2. Relaying

- **`suite.relay`** tails a change stream on `suite.outbox`, filtered to `operationType: insert`, with `fullDocument`. For each event it publishes a compact message (`{id, type, orgId, workspaceId, aggregate, refusal}`) to both apps' invalidation channels, then sets `publishedAt`.
- **Checkpointing.** The relay checkpoints its resume token in `suite.outbox_consumers` after every batch. On restart it resumes from the token. If the token has fallen out of the oplog, it scans `published_order` for `publishedAt: null` instead.
- **Delivery is at least once.** Order is commit order within the replica set. Nothing depends on order across aggregates.
- **Retention.** Outbox rows expire 7 days after `occurredAt` (`deleteAt`).

## 3. Consuming (each app's projector)

- **`keyhole.projector` and `dispatch.projector`** each tail their own change stream on `suite.outbox`, with their own resume token in `suite.outbox_consumers`. Redis pub/sub is only the fast path.
- **Idempotent apply.** For each event, in one transaction in the app's database:
  - insert `applied_events {_id: event._id}`; a duplicate-key error means the event is already applied, so skip it;
  - drop the event if `aggregate.rev` ≤ the rev already applied for that aggregate (`workspace_memberships` rows carry `rev`, so the check is a read);
  - apply the app-specific consequences and write their app-only audit rows, with `causationId` set to `event.auditEventId`;
  - bump `keyhole.org_settings.configVersion` when components must re-sync (Keyhole only).
- **Push to components.** After commit, the app pushes invalidations to its connected components: Keyhole connectors and sidecars, Dispatch sidecars (see `cache.md` §4).

| Event | Keyhole reacts | Dispatch reacts |
|---|---|---|
| `workspace.membership.added` (agent) | nothing to create; sidecars for that agent may now enroll here | create `membership_access` with a new `dsp_ws_` token (shown once, or sealed to the agent's sidecar) |
| `workspace.membership.removed` | refuse sidecars acting as that agent in that workspace from the next request; release cabinets they managed; invalidate caches | delete `membership_access` (token stops now); filter queued receipts; drop lost fire-hook targets |
| `workspace.membership.role_changed` | re-derive the workspace's admins and default admins | same |
| `org.member.suspended` / `removed` / `user.locked` | refuse that person's next action; cabinets they managed fall to admins when they can no longer use the workspace | refuse next action; their messages and receipts stand (rule 4) |
| `agent.retired` | treat as revoked; wipe sidecar caches for it | treat as revoked; filter its queued receipts |
| `workspace.deleted` | revoke sidecars bound there; un-expose; delete grants and cabinets (app-only audit rows) | expire open messages; close listeners; delete settings |

## 4. Rules

1. **Shared payloads carry shared facts only.** Never "sidecars refused" or "cabinets released": those are app-only rows.
2. **Every shared change produces exactly one shared audit row and one outbox row,** in the same transaction.
3. **Consumers are idempotent** on the event `_id`, and they drop stale events by `aggregate.rev`.
4. **Refusals are enforced at action time as well as by events.** Permission checks read `suite` (or a cache the invalidation channel clears), so an event that is late or lost can delay a cleanup but never an access check.
5. **Auth-service events enter through the suite ingest endpoint** (`POST /internal/suite/events`, mTLS). It writes the shared audit row and the outbox row in one transaction, idempotent on the auth event ID. Consumers see one stream.
6. **Event types are versioned** (`schemaVersion`). A breaking payload change gets a new version, and both projectors must handle both versions before a writer emits the new one.
7. **Rebuild is possible.** A projector can be reset by clearing its `applied_events` and replaying from a snapshot of `suite`. Projections are derivable; the shared records are the truth.
