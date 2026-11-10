# Keyhole data model: the SQL schema against every mock screen

This document checks every page of the clickable mock (`mocks/v1/src/pages` on `main`) against the SQL schema in `internal/store/migrations`. For each page it says:

- which tables and columns feed the page;
- what the page shows that the schema could not supply;
- what this change did about each of those gaps: a fix in the schema, or an explanation of why no column is needed.

The schema is a port of the JSON Schema on the `data-schema` branch (`mocks/schema/shared` and `mocks/schema/keyhole`). The read queries live in `internal/store/read.go`. The demo data the mock runs on is in `internal/store/seed/demo.json`; load it with `keyhole seed` or `SEED_DEMO_DATA=true`.

## How the JSON schema became SQL

There are three migrations. Postgres and SQLite run each file unchanged; the only per-database rewrite is that a `JSONB` column becomes `TEXT` on SQLite.

| File | What |
|---|---|
| `0001_init.sql` | The sign-in starter's users, identities and sessions, unchanged. |
| `0002_shared.sql` | `mocks/schema/shared`: the auth model (the sign-in starter's three tables grow the schema's columns, plus MFA factors, org memberships, suspensions, invitations and support locks); orgs, workspaces, workspace memberships, agents, the audit log, the outbox, outbox consumers and applied events. |
| `0003_keyhole.sql` | `mocks/schema/keyhole`: org and workspace settings, agent profiles, connectors and sidecars, enrollment tokens, stores, connection tests, event sources, issuer keys, cabinets, keys, Local-store values, tools and published versions, availability and exposure, grants, and per-person notification preferences. |

Conventions:

- **Names.** Field names are the schema's in snake_case. `_id` is `id`. An extension of a shared row is keyed by the shared ID: `workspace_settings.workspace_id`, `agent_profiles.agent_id`, `org_settings.org_id`.
- **IDs and times.** IDs are text, with the schema's prefixes. Times are Unix milliseconds in `BIGINT`, the same as the sign-in starter.
- **Small objects are flattened.** A fixed-shape object becomes `<field>_<subfield>`. For example, `createdBy {kind, id}` becomes `created_by_kind` and `created_by_id`.
- **Tokens.** A token digest becomes `<field>_alg`, `_hash`, `_prefix`, `_last4`, `_issued_at` and so on. A credential set adds `<field>_prev_*` and `<field>_grace_until` for the credential still in grace.
- **ID lists the schema indexes become child tables.** This applies to `availableKeyIds`, `exposedKeyIds`, `availableToolIds`, a grant's `slotMap`/`keyIds`, and a cabinet's `keyIds` and access list. "Which workspaces can use this key" is then an indexed join rather than a scan.
- **JSONB is for nested or free-form data only.** Examples: tool actions and slots, a key's provider-specific address, a store's per-provider settings, audit detail, connector install info and cache policy.
- **Left out of SQL.** Mongo, Redis and cache details are not ported: TTL indexes, change-stream resume semantics, and presence. A `*_at` expiry column (`expire_at`, `delete_at`, `destroy_at`) is swept by Keyhole, not by the database.
- **No secret value is readable in any column.**
  - Store credentials are `secretRef`s (`kss://` pointers into the sealed-secret service) inside `stores.settings`.
  - Tokens are HMAC digests with a prefix and last four.
  - Local-store values sit only in `local_key_values.envelope`, AES-256-GCM encrypted under a KMS-wrapped data key, and no read query returns them.
- **The org ID is always in the filter.** Every tenant query in `read.go` filters on it, so an ID from another org is "not found".

### Where the SQL deliberately differs from the JSON schema

| Change | Why |
|---|---|
| One `audit_events` table with a `shared` flag | Mongo splits shared rows (`suite`) from app rows (`keyhole`) across two databases. Keyhole has one database. The schema's rule that shared rows carry no detail or link is a `CHECK`. |
| `users.email_lower` is indexed but **not unique** | Keyhole links a sign-in to a user by provider, connection and subject only, never by email. A local account and a Zitadel account may therefore share an address. A unique email would make the second one fail to sign in. |
| `identities.issuer` → `connection_id`; provider `local` → `password` | These are the schema's names. A Zitadel identity's `connection_id` is its issuer URL; a password identity's is `''`. Existing rows are rewritten by the migration, and sign-in is unchanged (tested in `upgrade_internal_test.go`). |
| `sessions` keyed by `id_hash` (SHA-256 of the cookie), keeping `method`, `csrf_token` and `id_token_hint` | These are the sign-in starter's browser sessions. The schema's `ses_` ID and refresh-token family are for the auth service's CLI sessions; the `refresh_*` columns exist but nothing writes them yet. |
| `connectors.routesStoreIds` and `scope` are not stored | Both are derived from `stores.route_connector_id`. `Connector.RoutesStoreIDs` and `Connector.Scope()` compute them, so there's no second copy to keep in step. |
| `rev` is `BIGINT` | It's a counter on every mutable row, and `BIGINT` never overflows. |

The shared tables match Dispatch's port of the same folder ([VBCDX/dispatch](https://github.com/VBCDX/dispatch)) column for column. The two `0002_shared.sql` files still differ in a few places:

- index names;
- the `rev` type (`INTEGER` there, `BIGINT` here);
- `sessions.last_seen_at`, which is NOT NULL there and nullable here;
- `outbox_consumers.resume_token`, which is NOT NULL there and nullable here, because a SQL relay checkpoints on `last_event_id`;
- the default `client`;
- two extra audit indexes here, from `keyhole/indexes.json`.

These should be reconciled when both PRs land.

### Gaps the screen review found, and the schema fixes in this change

| # | Gap | Fix |
|---|---|---|
| 1 | Home's getting-started checklist needs to remember "dismissed" and "config downloaded". The schema had no place for either. | `org_settings.checklist_dismissed_at` and `org_settings.config_downloaded_at`. "First call" needs no column: it is the org's first `verification` or `request` audit row. |
| 2 | The OpenBao wizard's "What Keyhole may read" step asks for a secrets path (`secret/data/payments/`). The store schema had no field for it. | `stores.kv_mount` (`secret`) and `stores.kv_path_prefix` (`payments/`). |
| 3 | A Local key whose value has not been written yet (every demo key) has no `address`, but the schema requires one. | `keys.address` is nullable for Local keys only (`CHECK (address IS NOT NULL OR provider = 'local')`). |
| 4 | The mock's demo key `k_old` points at a store `st_removed` that the mock never defines. | The fixture adds that store as "Old vault", `status = removed`, so the key's "source removed" state resolves. |
| 5 | The mock records audit detail as display pairs (`[['Action', 'GET /v1/charges'], ...]`) plus a `destination`. | Detail is the structured `keyhole/audit_detail.schema.json` object (`kind`, `toolId`, `actionId`, `method`, `path`, `destinationHost`, `status`, `latencyMs`, `route`, `connectorId`). The page renders the labels. The mock's `fix` link is `link_label` / `link_to`. |
| 6 | Connector actors in the mock's audit rows (`edge-01`, `edge-02`) have no ID. | The fixture gives them `cn_edge01` / `cn_edge02`, so "activity for this connector" is an indexed lookup on `actor_id`. |
| 7 | "My settings" shows notification toggles as a person's own choice. The schema only has org-wide notifications in `org_settings`. | A new `notification_preferences (org_id, user_id, …)` table. The `org_settings` columns stay the org's defaults, and a person's row wins when present. This is the same design as Dispatch's port, so the suite behaves the same in both apps. |

## Pages

### Home (`Home.tsx`)

| Shows | From |
|---|---|
| Requests and blocked in the last 24 h | Count of `audit_events` with `type = 'request'` or `severity = 'blocked'` and `at` in the window. Index `audit_events_org_type_at`. |
| Tokens expiring in 14 days | `agent_profiles.expires_at` where `status <> 'revoked'`. |
| Store health, "N with unverified TLS" | `stores.health_status`, `stores.skip_tls_verify`. |
| Connectors degraded or offline | `connectors.health_status`. |
| Recent activity | `ListAuditEvents(org, {Limit: 10})`. |
| Getting-started checklist | The org's keys, tools (`published_version`), grants and agents; `workspace_settings.https`/`mcp`; `org_settings.config_downloaded_at` and `checklist_dismissed_at` (gap 1); the first `verification` audit row. |

What the schema does not supply, and why that's fine:

- **`statsBase` request and blocked counters.** These are pre-existing traffic the mock didn't model as rows; it's prototype-only. Real counts come from the audit log. At high volume a rollup table may be worth adding, but nothing needs one yet.
- **The sparkline.** It's a hard-coded polyline in the mock. Real data is the same 24-hour count, grouped by hour.

### Organizations (`orgs.tsx`, the sidebar's org switcher, `misc.tsx` Account settings)

| Shows | From |
|---|---|
| Org switcher, the Organizations list, "your role" | `ListUserOrgs(user)`: `org_memberships` ⨝ `orgs` (role, status, `joined_at`). |
| Org name, created, retention | `GetOrg`: `orgs.name`, `created_at`, `audit_retention_days`. |
| Counts on the overview (workspaces, members, agents, tools, keys) | `ListWorkspaces`, `ListOrgMembers`, `ListAgents` (not revoked), `ListTools`, `ListKeys` (no `cabinet_id`). |
| Owners, "no active Owner" | `ListOrgMembers` with `role = 'Owner'`, `status` and `LockedAt` (`users.lock_locked_at`). |
| Members table: name, email, role, status, last active, workspaces | `ListOrgMembers`. Workspaces come from `workspace_memberships` (or "All" for Owners and userAdmins). Last active is `users.last_sign_in_at`, or the newest audit row by that actor (index `audit_events_org_actor_at`). |
| Locked, Suspended, Invited | `users.lock_*` / `support_locks`; `org_memberships.status`, `suspended_from`, `suspension_id` → `suspensions`; `invitations`. |
| Pending invitations: email, role, workspaces, invited by, expiry | `ListInvitations(org, "pending")`: `workspace_grants`, `invited_by_*` joined to the user's name, `expires_at`. |
| Tools tab: "Granted to" | `grants` by `tool_id` (index `grants_org_tool`). |
| Delete-org impact counts | Counts across `workspaces`, `stores`, `keys`, `tools`, `agent_profiles`, `org_memberships` and `cabinets`. |

What the schema does not supply:

- **"Billing — placeholder" and "Sign-in provider — Google".** These are static placeholders in the mock. Billing is out of scope. The sign-in provider is deployment config (`AUTH_MODE`), not data.
- **The invitation lifetime ("expires in 7 days").** It's a constant in the mock and stays a server constant; each row's `expires_at` records the result.

### Workspaces (`workspaces.tsx`, including Summary, Tools and the new-workspace wizard)

| Shows | From |
|---|---|
| List: name, HTTPS/MCP pills, "N users · N agents" | `ListWorkspaces`: `workspaces` ⨝ `workspace_settings` (`slug`, `https`, `mcp`), with member counts from `workspace_memberships`. Agents count only if their Keyhole profile is not revoked. |
| Health dot (connector down, missing slot, source removed) | `connectors.health_status` for the workspace's connectors; `grant_slots.key_id IS NULL`; `keys.status = 'source_removed'` among its available keys. |
| "Keys this workspace exposes" | `GetWorkspaceAvailability`: `workspace_available_keys` (`exposed = TRUE`). |
| Users and agents | `ListWorkspaceMembers`. |
| Workspace admins | Derived: the explicit `workspace_admin` rows; if there are none, the org's active Owners and userAdmins. Never stored, as the schema says. |
| Tools tab: per tool, slot → key, on/off, limit | `ListGrants`: `grants` (`enabled`, `per_minute`, `pinned_version`) plus `grant_slots`; the tool's live version from `tools.published_version`. |
| "Last called" in the tool-off preview | The newest `request` audit row with that `toolId` in its detail and this `workspace_id`. |

**Available vs exposed (a gap in the mock, not the schema).** The mock keeps a single list, `workspaces.keyIds`, which every screen calls "exposed". The schema splits it in two:

- org admins make keys and tools *available* to a workspace;
- workspace admins *expose* available keys.

SQL keeps both as one row per key (`workspace_available_keys`) with an `exposed` flag, so exposed keys are always a subset of available ones. The demo data marks the mock's keys as both available and exposed. The UI's "Choose keys" step needs to say which of the two it changes; that is for the screen issues.

What the schema does not supply:

- **"Test call" results.** They're simulated (random latency). A real test call is a request through the broker and lands in the audit log.

### Stores, including the OpenBao connect-store wizard (`stores.tsx`, `OpenBaoWizard.tsx`)

| Shows | From |
|---|---|
| Store list: type, name, health, keys, last checked | `ListStores`: `provider`, `name`, `health_status`, `health_checked_at`, and `KeyCount` (keys not in a cabinet). Removed stores are not listed. |
| Store health that depends on its route | `route_mode` and `route_connector_id` ⨝ `connectors.health_status` / `status`; `skip_tls_verify`. |
| Store detail: address, route, sign-in method, path, cache, certificate | `address`, `route_mode`/`route_connector_id`, `network_exposure`, `auth_method`, `auth_mount`, `kv_mount` + `kv_path_prefix` (gap 2), `cache_default_ttl_seconds`, `ca_bundle_name` / `ca_bundle_pem`, `skip_tls_verify`. |
| Keys in a store: name, cabinet, created, rotated, expiry, used by | `ListStoreKeys`. "Used by" counts `grant_slots` and `cabinet_tools.slot_map` that reference the key (index `grant_slots_org_key`). |
| Keys whose source was removed | `keys.status = 'source_removed'`; the store still resolves through `GetStore` with `status = 'removed'` (gap 4). |
| Add key, replace value, import `.env` | Write paths for later issues. Keys keep only `length`; the value goes to `local_key_values` (envelope-encrypted). |
| Last connection test | `stores.health_last_test_id` → `ListConnectionTests` (steps and codes, never values). |

The **OpenBao wizard**, step by step:

| Step | Field in the mock | Column |
|---|---|---|
| Basics | Display name, vault address | `name`, `address` |
| How Keyhole reaches it | "Reachable from the internet" / "Through a connector" | `route_mode` (`direct` / `connector`), `route_connector_id`, `network_exposure` (`public` / `private`) |
| How Keyhole signs in | AppRole: role ID, secret ID | `auth_method = 'approle'`, `auth_mount`, `auth_role_id` (an identifier, stored in the clear); the secret ID is a sealed `secretRef` in `settings.auth.secretId` |
| | Kubernetes: role | `auth_method = 'kubernetes'`, `auth_role`; `route_mode` must be `connector` (enforced in the API, as the schema's `x-route` says) |
| | Token | `auth_method = 'token'`; the token is a sealed `secretRef` in `settings.auth.token` |
| What Keyhole may read | Secrets path, cache seconds | `kv_mount`, `kv_path_prefix` (gap 2); `cache_default_ttl_seconds` |
| Certificate | Uploaded CA file, or skip verification | `ca_bundle_pem` (public), `ca_bundle_name`, `skip_tls_verify` |
| Test | Result | `store_connection_tests` row, then `stores.health_*` |

What the schema does not supply:

- **The wizard's simulated test outcomes and the four invented key names.** These are prototype-only. Real keys are created from what the vault lists.
- **The egress IP list, the "Coming soon" store list and `bao operator unseal`.** These are product constants, not data.
- **Writing a secret ID or token.** This needs the sealed-secret service (`kss://`), which doesn't exist yet. The column shape is ready; the write path belongs to the stores issue.
- **The namespace and TLS server name.** The mock never asks for them, but the schema has them, so they're columns (`namespace`, `tls_server_name`) for OpenBao and Vault.

### Cabinets (`cabinets.tsx`)

| Shows | From |
|---|---|
| Card: name, locked, created by, managed by, keys, tools, access | `ListCabinets(org, workspace)`: `cabinets` (`created_by_*`, `managed_by`, `access_everyone`) plus `cabinet_keys`, `cabinet_tools` (`slot_map`) and `cabinet_access`. |
| "Manager left / locked / suspended — admins manage it" | Derived from `managed_by` against `workspace_memberships`, `org_memberships.status` and `users.lock_*`. `managed_by IS NULL` means the workspace admins manage it. |
| New cabinet: new Local keys | `keys.cabinet_id` (index `keys_org_cabinet`) in the Local store. |
| An agent's "lock lists" (Agents page) | `cabinet_access` by member (index `cabinet_access_org_member`). |

Nothing on this page is missing from the schema.

### Tools and the tool editor (`tools.tsx`, `ToolEditor.tsx`)

| Shows | From |
|---|---|
| Your tools: name, version, status, slots, actions, attached to, updated | `ListTools`: `draft_display_name`, `published_version`, `status`, `draft_slots`, `draft_actions`, `updated_at`; "attached to" comes from `grants`. |
| Installed from the catalog | `tools.catalog`. |
| Editor: internal name, display name, description, base URL | `draft_internal_name`, `draft_display_name`, `draft_description`, `draft_base_url`. |
| Actions and inputs, key slots | `draft_actions` / `draft_slots` (JSONB, typed as `ToolAction`, `ToolInput` and `KeySlot` in Go). |
| Limits | `draft_per_minute`, `draft_timeout_ms`. |
| "Draft vN on top of published vM", publish diff | `GetTool` (the draft) against `ListToolVersions` (immutable `tool_versions.snapshot`). |
| Delete impact: cabinets using it, last called | `cabinet_tools` by tool; the newest `request` audit row with that `toolId`. |

What the schema does not supply:

- **The starter catalog, the sample OpenAPI document and test-call input values.** The catalog and the sample are constants. Test-call inputs are typed in and never stored.
- **Version history before the latest published version.** The mock keeps only the latest published snapshot, so the demo data seeds that one version (`t_stripe@2`, `t_github@1`). Real publishes add a row per version.

**`privateAllow`** is a field of the snapshot only: the private addresses (IPs/CIDRs) a sidecar may let this tool reach. It is set by `keyhole tools private-allow`, which publishes a new version, and it is returned by resolve as `tool.private_allow` (`docs/resolve.md`). The draft columns (`tools.draft_*`) don't hold it yet, so a future draft → publish flow must carry it over.

### Connectors and Connect (`Connectors.tsx`, `connect.tsx`)

| Shows | From |
|---|---|
| Connector list: name, workspace, version, health, last seen, IP, enrolled when and by whom | `ListConnectors`: `name`, `workspace_id`, `health_reported_version`, `health_status`, `health_last_seen_at`, `health_ip`, `enrolled_at`, `enrolled_by_*` joined to the user's name. |
| Revoke impact: stores routed through it | `Connector.RoutesStoreIDs` (from `stores.route_connector_id`). |
| Enrollment panel: the one-time token and its expiry | `enrollment_tokens` (digest, prefix, last four, `intent`, `expires_at`). The token itself is shown once and never stored; the mock didn't store it either. |
| Connect: HTTPS and MCP toggles | `workspace_settings.https` / `mcp`. |
| Connect: agent picker, masked token | `ListWorkspaceMembers` (agents) ⨝ `agent_profiles.token_prefix` / `token_last4`. |
| "Last verified here" | The newest `verification` audit row for the workspace. |

What the schema does not supply:

- **The workspace address (`https://ws-1a2b.keyhole.dev`).** It's made up in the mock. The real address comes from `workspace_settings.slug` plus the deployment's public URL, so it needs no column.
- **The MCP client choice and the generated config file.** These are UI state; nothing is stored.
- **The connector's hard-coded version and random IP on enrollment.** These are simulated. Real values come from the component's heartbeat (`health_*`, `install`).
- **Sidecar listen address, exposure, local auth, app mode and cache mode.** The mock doesn't ask for them, but the schema covers them (`connectors.local`, `app_mode`, `proxy`, `cache_policy`; the same fields in `enrollment_tokens.intent`).

### Players: people and agents (`players.tsx`, the shared users and agents tables)

| Shows | From |
|---|---|
| Agents: label, masked token, status, created by, expiry, last used, workspaces, rate limit | `ListAgents`: `agents` (`label`, `created_by_label`) ⨝ `agent_profiles` (`status`, `token_prefix`/`token_last4`, `expires_at`, `last_used_at`, `rate_limit_per_minute`), plus workspaces from `workspace_memberships`. |
| Agent detail: rotated, grace | `token_rotated_at`, `token_grace_until`, `token_prev_*`. |
| Agent activity | `ListAuditEvents(org, {ActorID: agent})`. |
| People: the user detail header, lock banner, workspaces, cabinets they created, activity | `ListOrgMembers`, `GetActiveSupportLock` (`reason`, `unlock_requests`), `workspace_memberships`, `cabinets.created_by_id`, `ListAuditEvents(org, {ActorID: user})`. |

What the schema does not supply:

- **"The old token keeps working for 10 minutes" after a rotation.** It's hard-coded text in the mock. The schema stores the real grace (`token_grace_seconds`, `token_grace_until`) and the org default (`org_settings.default_grace_seconds`), so the copy can say what actually happens.
- **An agent with no Keyhole profile** (created in Dispatch only). It reads as `revoked` in Keyhole, because it has no Keyhole token.

### Settings pages in `misc.tsx`

| Page | Shows | From |
|---|---|---|
| Audit (`/audit` and every activity tab) | Time, severity, type, actor → object · destination, result, tracking code; expanded: reason, detail, the fix link | `ListAuditEvents` with filters on workspace, actor, subject and type, and a `Before` cursor for paging. `detail` and `link_*` are described under gap 5. |
| Audit filters | Event type, actor, workspace, result, date range, free text | Type, actor and workspace are indexed (`audit_events_org_type_at`, `_org_actor_at`, `_org_ws_at`). Result maps to `severity`. Date is the `Before` cursor plus a lower bound. Free-text search is left to the API issue: a scan, or `LIKE` on `object`/`trk`. |
| Invitations (`/invitations`) | Incoming invitations: org, invited by, role, expiry | `ListIncomingInvitations(email)`. |
| My settings | Name, email, active sessions with sign-out | `users`; `ListUserSessions(user, now)` (`device`, `place`, `last_seen_at`); per-session revoke is `sessions.revoked_at` / `revoke_reason`. |
| My settings: notifications | Six toggles | `GetNotificationPreferences(org, user)`: the person's own `notification_preferences` row if they saved one, otherwise the org's defaults in `org_settings.notify_*` (gap 7). |
| My settings: webhook | An https alert webhook | `org_settings.alert_webhook_url`; its signing secret is a sealed `secretRef`. |
| Account settings | Org name, delete | `orgs`. |
| Support console | Lock status, reason, unlock requests, orgs, sessions, pending invites | `users.lock_*`, `GetActiveSupportLock`, `ListUserOrgs`, `ListUserSessions`, `ListIncomingInvitations`. |

What the schema does not supply:

- **The alert webhook on a personal page.** In the mock, the webhook sits on "My settings" next to the toggles, but it is an organization's setting (`org_settings.alert_webhook_url`). The page should say it applies to the whole organization, and only admins should be able to change it.
- **"Logs kept 90 days on this plan."** It's hard-coded in the mock. The real value is `orgs.audit_retention_days`, which defaults to 365.
- **The Live toggle.** It shows simulated traffic. Real live updates are a query on new audit rows.
- **"Photo from Google".** It's static in the mock; there's no avatar field.

## Demo data

`internal/store/seed/demo.json` is the mock's *populated* scenario (`populatedDB()` in `seed.ts`), reshaped to these tables. Times are written as `@now-20d`-style offsets and resolved when the data is loaded, so the demo always looks recent.

Every token digest, session hash, role ID and `kss://` reference is an obvious `demo-placeholder:` value:

- there are no identities and no password hashes, so nobody can sign in as a demo person;
- there are no Local-store values, so keys show their length only.

`TestDemoFixtureHoldsNoSecrets` enforces both.

Changes from the mock's seed:

- the removed store (gap 4) and connector IDs (gap 6) are added;
- Sam Ortiz is an `active` account with no organization yet and a pending invitation, because the mock gives him a live session. The mock calls him `invited`; the schema's `pending` means "never signed in";
- the mock's expired Northwind invitation has no org ID and says it was sent by Sam. The demo data gives it to Northwind, sent by its Owner, Noor;
- "Granted GitHub issues to Staging" carries the Staging workspace ID.
