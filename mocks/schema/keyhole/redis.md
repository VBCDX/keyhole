# Keyhole Redis key-space

Redis carries Keyhole's operational state: proxying, presence, and invalidation. Configuration and audit are in Mongo. **Redis holds no secret values, tokens or data keys.** Keys are prefixed `kh:`. The part in `{…}` is the cluster hash tag, so keys that a Lua script touches together share a slot. Times are Unix milliseconds unless noted.

| Key pattern | Type | TTL | Writer | Reader | Purpose |
|---|---|---|---|---|---|
| `kh:{org_…}:cfgver` | STRING (int) | none | admin API, projector (after the Mongo `$inc` commits) | gateway, broker | Mirror of `org_settings.configVersion`. A component reconnecting with a lower version gets a delta or a full resync. Rebuilt from Mongo if lost. |
| `kh:inv:{org_…}` | pub/sub channel | — | admin API, projector, suite relay | every gateway and broker pod | Invalidations for rotate, revoke, unassign, un-expose, availability narrowed, grant removed, store changed, and agent suspended or retired. Pods evict in-process entries and forward to the components they hold (`cache.md` §4). Lossy by design; `cfgver` catches gaps. |
| `kh:ctl:{cn_…\|sc_…}` | pub/sub channel | — | admin API | the gateway pod holding that component's tunnel | Targeted control: a sealed new credential, `end_grace`, `wipe`, `config_changed`, `cache_flush`. |
| `kh:tun:{cn_…\|sc_…}` | HASH `{pod, connId, since, version, ip}` | 45 s, refreshed by each heartbeat | gateway | broker | Routes a proxied request to the pod that holds the component's outbound tunnel. |
| `kh:hb:{cn_…\|sc_…}` | HASH `{at, version, configVersion, cacheEntries, hitRatio, staleServed, ip}` | 120 s | gateway | health job, UI | The latest heartbeat. Counts only. |
| `kh:presence:{org_…}` | ZSET member = component ID, score = last heartbeat | none (members older than 1 h trimmed) | gateway | health job | healthy < 45 s, degraded < 90 s, else offline. Transitions are written to `connectors.health` and to an audit row. |
| `kh:rl:{ws_…}:g:{gr_…}:{epochMinute}` | STRING (INCR) | 120 s | broker (Lua: INCR + compare) | broker | The grant's `perMinute` limit for this workspace. |
| `kh:rl:{ws_…}:a:{ag_…}:{epochMinute}` | STRING (INCR) | 120 s | broker | broker | The agent's `rateLimitPerMinute`. |
| `kh:rl:{org_…}:t:{t_…}:{epochMinute}` | STRING (INCR) | 120 s | broker | broker | The tool's own `perMinute` across workspaces. |
| `kh:budget:{st_…}:{epochHour}` | STRING (INCR) | 2 h | broker, connector (reported) | broker | Vendor-quota guard, for 1Password service-account hourly limits ◐ and Doppler per-token limits ◐. Past the budget, reads are served from cache, or refused with `rate_limited`. |
| `kh:inflight:{ag_…}` | ZSET member = request ID, score = deadline | refreshed to the longest deadline + 5 s | broker | broker | Concurrency cap per agent. On revoke or suspend, the broker cancels these requests (rule 5: the next call is refused; an in-flight call is cut when the agent is revoked). |
| `kh:req:{ag_…}:{requestId}` | HASH `{orgId, wsId, toolId, grantId, route, componentId, startedAt, deadline}` | tool `timeoutMs` + 5 s | broker | broker, support tooling (IDs only) | Tracks an in-flight request. No headers or bodies. |
| `kh:vlogin:{st_…}` | STRING (SET NX PX 10000) | 10 s | broker, connector | same | Single-flight vendor login: AppRole num_uses, Conjur's 8-minute tokens, token renewals. |
| `kh:storehealth:{st_…}` | HASH `{status, code, checkedAt}` | 10 min | health job, test runner | UI | Cheap latest health; the durable record is in `stores.health`. |
| `kh:evt:{esrc_…}:{vendorEventId}` | STRING (SET NX) | 24 h | inbound event endpoint | same | Dedupes vendor change events (EventBridge, Event Grid, Pub/Sub, Infisical, Doppler). |
| `kh:rl:enroll:{ip}` | STRING (INCR) | 15 min | enroll endpoint | same | 10 attempts per 15 minutes per IP, against enrollment-token guessing. |
| **(none)** | — | — | — | — | **No decrypted-secret cache on Keyhole's side.** The broker keeps resolved values in process memory only (mlocked where possible, zeroed on eviction), keyed by `(orgId, storeId, address, jsonKey)` with the key's TTL, and evicted on `kh:inv`. Vendor sessions (Vault tokens, STS credentials, Entra tokens) are also process-memory only. |

Durability: none of these needs AOF. After a flush:
- presence rebuilds within one heartbeat;
- rate windows reset (acceptable);
- `cfgver` reloads from Mongo;
- in-flight requests are orphaned, and their callers time out.
