# Shared collections

Validators and indexes are in `indexes.json` (databases `suite` and `auth`). Generate the mongosh script with:

```sh
python3 tools/mongo_validator.py --indexes indexes.json > suite.mongosh.js
```

## suite

| Collection | Schema | Key queries (index) | Retention |
|---|---|---|---|
| `orgs` | `org.schema.json` | by `_id`; the org switcher resolves names for the IDs in the token's `orgs` claim | soft delete |
| `workspaces` | `workspace.schema.json` | an org's workspaces by status and name (`org_status_name`) | soft delete |
| `workspace_memberships` | `workspace_membership.schema.json` | a workspace's members (`org_ws_member`, unique per member); a player's workspaces (`org_member`); a workspace's explicit admins (`org_ws_role`) | hard delete; history in audit |
| `agents` | `agent.schema.json` | an org's agents; label uniqueness (`org_label`) | kept; `retiredAt` |
| `audit_events` | `audit_event.schema.json` (shared rows only) | org, workspace, actor, subject, `trk`, `causationId` — each by time | TTL `expireAt` (org retention) |
| `outbox` | `outbox_event.schema.json` | change stream; catch-up scan by `publishedAt` | TTL 7 days |
| `outbox_consumers` | `outbox_consumer.schema.json` | by consumer name | — |

The context-aware Audit views (SUITE_SHARED §6) merge two sorted streams:

```
suite.audit_events  {orgId, [workspaceId | actor.id | subject.id], at < cursor}   -- shared rows
<app>.audit_events  {orgId, [workspaceId | actor.id | subject.id], at < cursor}   -- this app's rows
```

For the `user` role, the app adds `workspaceId ∈ my workspaces` or `actor.id = me`. "Last active (this org)" is the newest `at` with `actor.kind = user, actor.id = u_…` across both streams (the `org_actor_at` index). Keyhole's old "Signed in" admin rows are no longer needed for this: the auth service's `sessions.lastSeenAt` covers sign-in, and audit covers activity.

## auth (logical)

| Collection | Uniqueness | Lookups |
|---|---|---|
| `users` | `emailLower` | by `_id` and email |
| `identities` | (`provider`, `connectionId`, `subject`) | a user's identities |
| `sessions` | refresh token digest | a user's live sessions (sign out everywhere); TTL on `expiresAt` |
| `mfa_factors` | WebAuthn credential ID | a user's factors |
| `org_memberships` | (`orgId`, `userId`) | a user's orgs (claims); an org's admins (`org_role_status`) |
| `invitations` | token digest | an org's pending invitations; pending invitations for an email (incoming invites) |
| `support_locks` | — | a user's active lock |
| `suspensions` | — | history per org and user |

## App databases

Both `keyhole` and `dispatch` also contain `audit_events` (app-only rows, same envelope, `detail` validated by `<app>/audit_detail.schema.json`) and `applied_events` (projector idempotency, TTL 14 days).
