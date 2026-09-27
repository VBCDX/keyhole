# Shared records and the auth service

This folder is byte-identical in the Keyhole and Dispatch repos. Change it in both, in the same PR pair.

| File | Collection | Database |
|---|---|---|
| `common.schema.json` | `$defs` used everywhere: IDs, dates, principal refs, `tokenDigest`, `credentialSet`, `secretRef`, `envelope`, `localEndpoint`, `cachePolicy`, `componentHealth`, `enrollment`, `enrollmentTokenBase` | — |
| `org.schema.json` | `orgs` | suite |
| `workspace.schema.json` | `workspaces` | suite |
| `workspace_membership.schema.json` | `workspace_memberships` | suite |
| `agent.schema.json` | `agents` | suite |
| `audit_event.schema.json` | `audit_events` | suite (shared rows), keyhole and dispatch (app-only rows) |
| `outbox_event.schema.json` | `outbox` | suite |
| `outbox_consumer.schema.json` | `outbox_consumers` | suite |
| `applied_event.schema.json` | `applied_events` | keyhole, dispatch (projector idempotency) |
| `auth/*.schema.json` | users, identities, sessions, mfa_factors, org_memberships, invitations, support_locks, suspensions; `claims` (the access token) | auth service |
| `indexes.json` | validators and indexes for `suite` and the auth service's logical model | — |
| `outbox.md` | Outbox and change-stream contract | — |
| `collections.md` | What each shared collection is for and how it's queried | — |
| `tools/mongo_validator.py` | Converts any schema here to a MongoDB `$jsonSchema` validator, or a whole `indexes.json` to a mongosh script | — |

## Auth service

The auth service is a separate service with its own database. It owns humans; it never owns agents. Its schemas here are the logical model that both apps rely on. The storage engine is the auth service's choice.

### What it owns

| Record | Notes |
|---|---|
| users | Account-level status: `active`, or `pending` (never signed in). A `lock` summary is denormalised for the sign-in check. |
| identities | password (argon2id), Google, Microsoft, GitHub, or an org's OIDC/SAML connection. |
| sessions | Web and CLI. Refresh tokens rotate on every use; re-using an old one revokes the whole family. |
| mfa_factors | TOTP (the seed is a sealed secret), WebAuthn, and recovery codes (argon2id). |
| org_memberships | Role (Owner / userAdmin / user) and status (active / invited / suspended) per org. Suspension is per membership. |
| invitations | Email, org role, workspaces with role, and opaque per-app `extensions` (Dispatch read/write). |
| support_locks | Account-wide. Set only by Keyhole support (`sup_…`), with a reason or ticket. |
| suspensions | History; the current state is on the membership. |

### API contract (what both apps call)

| Endpoint | Used by | Purpose |
|---|---|---|
| `GET /.well-known/openid-configuration`, `GET /jwks` | both | Verify access tokens. |
| `GET /oauth2/authorize` + `POST /oauth2/token` (`authorization_code` + PKCE) | web apps | Sign in. `aud` = `keyhole` or `dispatch`. |
| `POST /oauth2/device_authorization`, `POST /oauth2/token` (`urn:ietf:params:oauth:grant-type:device_code`) | CLIs | `keyhole login` / `dispatch login` (RFC 8628). `aud` = `keyhole-cli` or `dispatch-cli`. |
| `POST /oauth2/token` (`refresh_token`) | both | Rotates the refresh token (`au_rt_…`). |
| `POST /oauth2/revoke`, `POST /oauth2/introspect` | both | Sign out; check a token whose `mver` is behind. |
| `GET/PATCH /v1/orgs/{orgId}/members[/{userId}]` | both | Change role, suspend, resume, remove, transfer ownership. Last-Owner rule enforced here. |
| `POST/GET/DELETE /v1/orgs/{orgId}/invitations` | both | Invite, list, revoke. The body carries `workspaceGrants` and `extensions`. |
| `POST /v1/invitations/{token}/accept` / `decline` | both | The invitee acts. |
| `POST /v1/support/users/{userId}/lock` / `unlock` / `signout-everywhere`, `POST /v1/support/invitations/{id}/resend` | support console only | The only support powers (PERMISSIONS rule 1). |

Every mutating call takes an `X-VBCDX-Audit` header: `{app, trk, correlationId}`. The auth service copies it into its outbox event, so the shared audit row names the app and the tracking code the person saw.

### The claims both apps get

The access token is a JWT. Its schema is `auth/claims.schema.json`. It lives 5 minutes.

```json
{
  "iss": "https://auth.vbcdx.dev",
  "sub": "u_mia",
  "aud": "keyhole",
  "iat": 1790518800, "exp": 1790519100, "jti": "…", "sid": "ses_…",
  "amr": ["pwd", "otp"],
  "email": "mia@acme.com", "email_verified": true, "name": "Mia Chen",
  "org": "org_acme",
  "orgs": [
    {"id": "org_acme", "role": "user", "status": "active"},
    {"id": "org_nw",   "role": "user", "status": "active"}
  ],
  "mver": 42
}
```

- **Org roles and status are in the token.** Workspace roles are not: apps read `suite.workspace_memberships` when an action happens (rule 3). Workspace admin is too fine-grained, and changes too often, to cache in a token.
- **Refusals don't wait for expiry** (rule 5). Apps consume `org.member.suspended`, `org.member.removed`, `org.member.role_changed` and `user.locked` from the outbox, and keep `mver` per user in memory. A request whose token `mver` is behind is re-checked with `/oauth2/introspect` before any admin action.
- **Support tokens** have `sub: sup_…`, `support: true`, an empty `orgs`, and a scope limited to the four support actions. Neither app's data API accepts them.
- **Suspended and invited memberships** stay in `orgs`, so the org switcher can show the markers. Apps refuse every action in an org where the status isn't `active`.

## Shared records (suite database)

- **orgs, workspaces.** Name and lifecycle only. App content hangs off their IDs in each app's database. Deletion is soft: `status: deleted`, with who and when, so audit rows keep resolving names.
- **workspace_memberships.** One row per (workspace, user-or-agent), with role `member` or `workspace_admin`. For agents the role is always `member`; Dispatch's agent admins live in `dispatch.membership_access`. Default admins are derived, never stored.
- **agents.** The shared identity: ID, label (unique per org, case-insensitive), created-by, and `retiredAt`. Status and credentials are per app.
- **audit_events.** The envelope both apps write (see the README, §5.6).
- **outbox, outbox_consumers.** See `outbox.md`.
