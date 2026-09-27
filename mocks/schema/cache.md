# Local encrypted credential cache

This cache is used by three components that run on customer hosts:
- the **Keyhole vault connector** (`keyholed connector`);
- the **Keyhole sidecar** (`keyholed sidecar`);
- the **Dispatch sidecar** (`dispatch sidecar run`).

It cuts round trips to Keyhole, Dispatch and the customer's vault, and keeps credentials encrypted at rest. The policy for each component is `cachePolicy` in `shared/common.schema.json`. The server sets it at enrollment and can change it through config sync. Local config can only tighten it.

Citations such as §11.2 refer to VAULT_FLOWS.md (research dated 2026-09-27).

## 1. What each component holds

The components hold two classes of data, handled differently:

- **Held credentials** are what the component needs in order to act. They have no TTL. They are replaced by server push on rotation and wiped on revoke.
- **Cached values** are copies of something whose source of truth is elsewhere. Each has a TTL.

| Component | Held credentials (class H) | Cached values (class V) |
|---|---|---|
| Vault connector | Its `kh_conn_` credential. The `connector_local` store secrets typed on the host (AppRole secret_id, API keys, client keys). | Secret values resolved from the stores it routes, keyed per store and address. Vendor sessions (Vault tokens, STS credentials, Entra and Google access tokens) are **memory only, never on disk**. |
| Keyhole sidecar | Its `kh_sc_` credential. The digest of its caller token. | Key values for the tool slots granted to its bound agent in its workspace. Keyhole seals each value to the sidecar's X25519 key before sending it. Also the config snapshot: grants, tools, allowed hosts, rate limits. |
| Dispatch sidecar | Its `dsp_sc_` credential. The bound agent's `dsp_agent_` token and its `dsp_ws_` tokens, delivered sealed to the sidecar's key, so the agent process never sees them. | The config snapshot (workspaces, access flags) for local pre-checks; the server re-checks every call. |

## 2. Format and encryption (§11.3)

- **Envelope encryption.**
  - A random 256-bit data key (DEK) encrypts each entry with AES-256-GCM and a unique 96-bit nonce.
  - The AAD is `orgId | componentId | entryKeyHash | dekVersion`, so a ciphertext can't be moved between entries, components or tenants.
  - **Expiry lives inside the authenticated plaintext** as `fetchedAt`, `expiresAt`, `staleUntil` and `hardExpiry`, and is enforced on read. Changing file timestamps can't extend a TTL.
- **Entry keys are hashed.** The on-disk index is HMAC-SHA256(macKey, cacheKey). `macKey` is derived from the DEK with HKDF, so the file doesn't reveal which secrets or paths are cached. Cache key: `(orgId, storeId, address incl. version/stage/alias, jsonKey)` (§11.2 #1). Entries are never shared across tenants, even when the address matches.
- **Where the DEK lives** (`cachePolicy.keyProtection`):

  | Mode | DEK | Survives restart | Useless if the file is copied off the box |
  |---|---|---|---|
  | `process_memory` (with `mode: memory`) | Generated at start and held only in RAM. There is no file. | no, the cache starts cold | n/a |
  | `tpm` | Sealed to the host's TPM 2.0, optionally with a PCR policy; unsealed at start | yes | yes |
  | `cloud_kms` | Wrapped by a KMS key that only this component's identity can use: AWS KMS `GenerateDataKey`/`Decrypt` with encryption context `{orgId, componentId}`, GCP Cloud KMS `encrypt`/`decrypt`, Azure Key Vault `wrapKey`/`unwrapKey`, or Vault/OpenBao Transit `transit/datakey/wrapped/<key>` ⚠ | yes | yes |

  `mode: disk` requires `tpm` or `cloud_kms` (the schema enforces this).
- **Unwrap failure at start.** If the DEK can't be unwrapped (KMS denied, TPM changed), the component discards the file and starts cold. It never falls back to plaintext.
- **DEK rotation.** The DEK is rotated every 30 days, on credential rotation, and on re-enrollment. Entries are re-encrypted lazily on next read; the old DEK is dropped once none remain. KEK rotation only re-wraps the DEK.
- **Files.**
  - Linux: `/var/lib/keyholed/<componentId>/cache.db` and `/var/lib/dispatch/<componentId>/cache.db`.
  - macOS: `~/Library/Application Support/…`.
  - Windows: `%ProgramData%\…`.

  The directory is mode 0700 and each file 0600, owned by the service user. Only one process writes at a time (an exclusive file lock).
- **Memory hygiene.** Plaintext values and the DEK live in mlocked buffers where the OS allows. They are zeroed on eviction and at exit. Core dumps are disabled (`RLIMIT_CORE=0`, `PR_SET_DUMPABLE=0`).
- **Held credentials (class H)** go in a separate `credentials.db`, with the same envelope and no TTL. With `process_memory` protection, the component's own credential still has to survive a restart, so it is protected by the OS instead: systemd `LoadCredentialEncrypted=`, the macOS Keychain, Windows DPAPI, or a Kubernetes Secret mounted read-only.

## 3. Freshness

| Rule | Default | Bounds | Source |
|---|---|---|---|
| Per-entry TTL, moving pointer (`latest`, `AWSCURRENT`, unversioned KV v2 / Azure / Doppler / Infisical / 1Password by name) | 300 s | 0–3600 s per key (`keys.cacheTtlSeconds`) | §11.2 #2; the AWS agent defaults to 300 s |
| Per-entry TTL, pinned version (an immutable value) | 86,400 s | ≤ 86,400 s | §11.2 #2 ("still bounded … to respect revocation") |
| TTL 0 | the value is never cached | — | |
| **Stale-if-error** | 900 s after expiry | 0–900 s | §11.2 #5 (≤ 15 min). Only on 5xx, network errors, timeouts, 429, or the control channel to Keyhole/Dispatch being down. **Never** on 401, 403 or 404, or an explicit revoke. |
| **Max-staleness cap** | 3600 s from `fetchedAt`, whatever else applies | server-set; the component can only lower it | Bounds how long a revocation can go unseen during a network partition. |
| **Negative caching** | **off** | `const false` | This is a deliberate departure from §11.2 #6. Not-founds and errors are never cached. Hammering is prevented instead by per-key single-flight, exponential backoff on upstream retries (base 1 s, factor 2, the AWS client's style), and the vendor budget guard (`kh:budget`). |
| Revalidation without moving values | when a moving entry expires | — | §11.2 #4: KV v2 `metadata.current_version`, AWS `DescribeSecret` `VersionIdsToStages`, Doppler `If-None-Match` (304), 1Password item `version`. If unchanged, the TTL is extended; otherwise the value is re-read. |
| Downstream feedback | on a target API's 401 | — | §11.2 #7: evict that entry and re-read once, since the value may have rotated. |

Class H credentials have no TTL. They stay valid until the server rotates or revokes them. A token in its rotation grace is kept only until `graceUntil`.

## 4. Invalidation (server push)

Each component keeps one outbound connection: WSS to the Keyhole gateway, or to the Dispatch sidecar gateway. The server sends:

```json
{"type": "invalidate", "configVersion": 413,
 "scope": {"kind": "key|store|grant|agent|workspace|all", "id": "k_bao1"},
 "reason": "rotate|revoke|unassign|unexpose|availability_narrowed|grant_removed|store_changed|vendor_event|suspend|end_grace",
 "at": "2026-09-27T14:02:11Z"}
```

- **Sources.**
  - Admin actions: rotate key, revoke agent, remove from workspace, un-expose, narrow availability, remove grant.
  - The suite outbox: membership removed, agent retired, user or agent suspended (`shared/outbox.md`).
  - Vendor change events: EventBridge, Event Grid, Pub/Sub, Infisical and Doppler webhooks, Vault Enterprise events. These are hints; the component re-reads (§11.2 #3).
- **Applying one.** The component deletes the matching entries before it acknowledges `{configVersion}`. The server records the acknowledged version in `componentHealth.configVersion`.
- **Gaps.** If `configVersion` jumps by more than 1, or on reconnect after a disconnect, the component flushes **all class V entries** and resyncs its config. Pub/sub is lossy, so a gap is treated as "anything may have changed".
- **New credentials.** Rotating a sidecar's agent token (Dispatch), or a component's own credential, pushes the new one sealed to the component's key. The old one is kept until `graceUntil`, or dropped at once when grace is 0 or ended.

## 5. Wipe on revoke

When the component is revoked, deleted, or bound to an agent that is revoked or retired, the server sends `{"type": "wipe"}` on the control channel. A component that was offline receives `401 component_revoked` when it next connects. The component then:

1. stops serving local callers (503 `revoked`);
2. deletes `cache.db` and `credentials.db` (overwriting where the filesystem allows);
3. zeroes the DEK and every in-memory buffer;
4. destroys the wrapped DEK (and, for `cloud_kms`, forgets the handle);
5. reports `cache_wiped` if it still can;
6. exits with status 78 (EX_CONFIG), so the service manager doesn't restart it in a loop.

A Keyhole sidecar whose bound agent is merely **suspended** (in the app or in the workspace) does not wipe. It refuses calls from the next request (rule 5) and evicts its class V entries. Resume re-syncs.

## 6. Never cached

- **Enrollment tokens** (`kh_enr_`, `dsp_enr_`). They are read from stdin, a file or an env var, used once, and zeroed. They are never written anywhere and never taken from argv.
- **The plaintext of local caller tokens** (`kh_call_`, `dsp_call_`). The component holds only the digest.
- **Request and response bodies,** headers from downstream APIs, and anything the proxied app sends.
- **Vendor session tokens on disk.** Vault tokens, STS credentials, Entra and Google access tokens, Conjur 8-minute tokens and Infisical access tokens stay in memory only.
- **Wrapping tokens** (AppRole response wrapping). They are used once, and the result is sealed as a `connector_local` secret.
- **Values for other agents or workspaces.** A Keyhole sidecar receives only its bound agent's granted slots in its workspace.
- **Keys with `cacheTtlSeconds: 0`.**
- **Errors and not-founds** (negative caching is off).
- **Human CLI sessions.** These are the auth service's `au_rt_` refresh tokens, kept in the OS keychain by the CLI and never in a component cache.
- **Anything on Keyhole's side in Redis.** Keyhole's broker caches resolved values in process memory only (`keyhole/redis.md`).

## 7. Status and metrics

`keyhole cache status` and `dispatch cache status` print the mode, key protection (KMS key ref or TPM handle), DEK age, entry counts by class, hit ratio, stale served in the last 24 h, oldest entry age against the max-staleness cap, last invalidation and `configVersion`. They never print keys, paths or values. The same counts go to the server in heartbeats (`componentHealth.cache`).

`… cache flush [--key k_… | --store st_… | --all]` evicts class V entries. Class H credentials can only be removed by `… uninstall --wipe` or a server wipe.

## 8. Vendor guidance this design follows (§11.1)

| Vendor | Guidance (from VAULT_FLOWS.md) | Where it shows up here |
|---|---|---|
| AWS Secrets Manager | *"We recommend that you cache your secret values by using client-side caching."* The caching client refreshes after 1 h, with exponential retry backoff. | Caching on by default; backoff on upstream retries. |
| AWS Workload Credentials Provider | TTL 0–3600, **default 300 s**; *"does not include cache invalidation"*; *"Secret values are not encrypted in the cache."* | 300 s default; we add push invalidation and encryption at rest. |
| Vault / OpenBao Agent and Proxy | They cache tokens and leased secrets. The persistent cache is Kubernetes-only and uses *"a generated encryption key"*, with the SA token as an integrity check. *"Secrets that are not renewable, such as KV v2, will not be persisted."* KV caching exists only in Enterprise Proxy, driven by events. | Keyhole caches KV itself (§2.6); the DEK is bound to the host (TPM or KMS), the same idea as Agent's; vendor tokens are never persisted. |
| Azure Key Vault ◐ | *"Cache the secrets you retrieve … in memory … Re-read … only when the cached copy stops working."* Back off on 429. | Memory mode is available; the downstream 401 triggers an evict-and-reread; stale-if-error on 429. |
| GCP Secret Manager ◐ | Pin versions in production; read at startup. | The pinned TTL tier. |
| 1Password | Service-account rate limits make caching mandatory ◐; Connect caches locally. | The vendor budget guard, and the item-`version` revalidation. |
| Doppler | The CLI keeps an encrypted fallback file and revalidates with an ETag. | Disk mode with revalidation through `If-None-Match`. |
| Infisical ⚠ | The SDKs cache in memory. | Memory mode; webhook invalidation. |
