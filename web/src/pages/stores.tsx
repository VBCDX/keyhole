// Ported from mocks/v1/src/pages/stores.tsx and wired to the read API.
// Adding, editing and removing stores and keys are later issues: their
// buttons are shown, disabled, with a "Coming soon" tooltip.
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, type Key, type Store, type StoreDetail as StoreDetailData } from '../api'
import { DAY, ago, ms, plural, until } from '../lib/format'
import { useLoad } from '../lib/useLoad'
import { isAdmin, useSession } from '../session'
import { LockedDots } from '../components/keyhole'
import { Button, Callout, Dot, ErrorBox, FOCUS_RING, Pill, Row, Table, activateOnKey, cx, type Health } from '../components/ui'
import { NoAccess, Soon } from './misc'

type StoreHealthView = { dot: Health; text: string }

/** What the store's health dot and pill say, as in the mock's storeHealth. */
function storeHealth(s: Store): StoreHealthView {
  const conn = s.route_connector
  if (s.removed_at) return { dot: 'offline', text: 'Removed' }
  if (s.health.status === 'sealed') return { dot: 'error', text: 'Sealed' }
  if (s.health.status === 'unreachable') return { dot: 'error', text: 'Can’t reach' }
  if (s.health.status === 'denied') return { dot: 'error', text: 'Not allowed' }
  // A route through a connector that no longer exists can't reach anything.
  if (s.route_mode === 'connector' && (!conn || conn.status === 'revoked')) return { dot: 'error', text: 'Connector revoked' }
  if (conn?.health_status === 'offline') return { dot: 'degraded', text: `Connector ${conn.name} offline` }
  // Reachable, but anyone on the path could read the traffic — never shown as plain healthy.
  if (s.skip_tls_verify) return { dot: 'degraded', text: 'Unverified TLS' }
  if (s.health.status === 'degraded') return { dot: 'degraded', text: 'Degraded' }
  if (s.health.status === 'unknown') return { dot: 'offline', text: 'Not checked yet' }
  return { dot: 'healthy', text: '' }
}

function TypeMark({ provider }: { provider: Store['provider'] }) {
  return <span className="flex size-6 min-w-6 items-center justify-center rounded-md border border-chip bg-line text-2xs font-bold text-zinc-400">{provider === 'local' ? 'L' : 'B'}</span>
}

export function StoreRow({ s, onClick, chevron = true }: { s: Store; onClick?: () => void; chevron?: boolean }) {
  const h = storeHealth(s)
  return (
    <div
      onClick={onClick}
      role={onClick ? 'link' : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={onClick ? activateOnKey(onClick) : undefined}
      className={cx('flex items-center gap-2.5 rounded-[10px] border border-edge bg-panel px-4 py-3.5', onClick && cx('cursor-pointer hover:border-zinc-700', FOCUS_RING))}
    >
      <TypeMark provider={s.provider} />
      <Dot health={h.dot} />
      <span className="text-md font-semibold">{s.name}</span>
      {s.provider === 'openbao' && <Pill>OpenBao</Pill>}
      <span className="text-xs text-zinc-500">
        {plural(s.key_count, 'key')} · checked {ago(ms(s.health.checked_at)).toLowerCase()}
      </span>
      {h.text && <Pill tone={h.dot === 'degraded' ? 'amber' : h.dot === 'error' ? 'red' : 'neutral'}>{h.text}</Pill>}
      {chevron && <span className="ml-auto text-xs text-zinc-600">›</span>}
    </div>
  )
}

function StoreRowsSkeleton() {
  return (
    <div className="flex flex-col gap-2.5" aria-busy="true" aria-label="Loading stores">
      {[0, 1].map((i) => (
        <div key={i} className="rounded-[10px] border border-edge bg-panel px-4 py-[18px]">
          <div className="skeleton h-3" style={{ width: `${40 + i * 15}%` }} />
        </div>
      ))}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Stores tab                                                          */
/* ------------------------------------------------------------------ */
export function StoresTab() {
  const nav = useNavigate()
  const { orgId } = useParams()
  const res = useLoad(`stores:${orgId}`, () => api.stores(orgId!))

  if (res.state === 'not-found') return <div className="mt-6"><NoAccess what="organization" to="/orgs" back="Back to your organizations" /></div>
  return (
    <div className="mt-5 max-w-[760px]">
      {res.state === 'loading' && <StoreRowsSkeleton />}
      {res.state === 'error' && <ErrorBox what="stores" onRetry={res.retry} />}
      {res.state === 'ready' && (
        <>
          <div className="flex flex-col gap-2.5">
            {res.data.stores.map((s) => (
              <StoreRow key={s.id} s={s} onClick={() => nav(`/orgs/${orgId}/stores/${s.id}`)} />
            ))}
          </div>
          {res.data.stores.some((s) => s.provider === 'local' && s.key_count === 0) && (
            <div className="mt-2 text-xs text-zinc-500">Included with your organization — paste your first key to get going.</div>
          )}
          {res.data.source_removed_keys.length > 0 && <SourceRemovedKeys keys={res.data.source_removed_keys} />}
        </>
      )}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Keys whose store was removed                                        */
/* ------------------------------------------------------------------ */
const ORPHAN_COLS = '1.6fr 1fr 1fr 1.4fr'
function SourceRemovedKeys({ keys }: { keys: Key[] }) {
  return (
    <div className="mt-8">
      <div className="eyebrow">Keys needing attention</div>
      <div className="mt-1 text-xs text-zinc-500">These keys came from a store that was removed while they were still in use.</div>
      <Table cols={ORPHAN_COLS} head={['Name', 'Value', 'Used by', 'Status']} className="mt-3">
        {keys.map((k) => (
          <Row key={k.id} cols={ORPHAN_COLS}>
            <div className="font-mono text-sm2 text-zinc-500 line-through">{k.name}</div>
            <div>
              <LockedDots dim />
            </div>
            <div className="text-zinc-400">{plural(k.used_by, 'tool')}</div>
            <div className="flex items-center gap-2.5">
              <Pill tone="amber">Source removed</Pill>
              <Soon>
                <button type="button" disabled className="text-xs text-brass opacity-40">
                  Resolve
                </button>
              </Soon>
            </div>
          </Row>
        ))}
      </Table>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Store detail (keys table, plus OpenBao connection details)          */
/* ------------------------------------------------------------------ */
const KEY_COLS = '1.6fr 1fr 1fr 1fr 0.9fr 0.9fr 36px'
const AUTH: Record<string, string> = { approle: 'AppRole', kubernetes: 'Kubernetes', token: 'Token' }

function route(s: StoreDetailData) {
  if (s.route_mode === 'connector') return `Through connector ${s.route_connector?.name ?? '(revoked)'}`
  if (s.route_mode === 'direct') return 'Reachable from the internet'
  return 'Inside Keyhole'
}

export function StoreDetail() {
  const { orgId, storeId } = useParams()
  const { current } = useSession()
  const admin = isAdmin(current)
  const res = useLoad(`store:${orgId}:${storeId}`, () => api.store(orgId!, storeId!))
  const back = `/orgs/${orgId}/stores`

  if (res.state === 'loading')
    return (
      <div className="mt-5 max-w-[760px]">
        <StoreRowsSkeleton />
      </div>
    )
  if (res.state === 'error')
    return (
      <div className="mt-5 max-w-[760px]">
        <ErrorBox what="this store" onRetry={res.retry} />
      </div>
    )
  if (res.state === 'not-found')
    return (
      <div className="mt-6 text-sm text-zinc-400">
        This store doesn’t exist, or isn’t in this organization. <Link to={back}>Back to stores</Link>
      </div>
    )
  const s = res.data
  const now = Date.now()
  const removed = !!s.removed_at

  return (
    <div className="mt-5">
      <div className="flex items-center justify-between gap-4">
        <div className="max-w-[760px] flex-1">
          <StoreRow s={s} chevron={false} />
        </div>
        {admin && !removed && (
          <div className="flex gap-2">
            {s.provider === 'local' ? (
              <>
                <Soon>
                  <Button disabled>Import from .env</Button>
                </Soon>
                <Soon>
                  <Button variant="primary" disabled>
                    Add key
                  </Button>
                </Soon>
              </>
            ) : (
              <>
                <Soon>
                  <Button disabled>Test connection</Button>
                </Soon>
                <Soon>
                  <Button variant="danger" disabled>
                    Remove store
                  </Button>
                </Soon>
              </>
            )}
          </div>
        )}
      </div>

      {removed && (
        <Callout tone="amber" className="mt-4 max-w-[760px]">
          This store was removed {ago(ms(s.removed_at)).toLowerCase()}. Keys still in use stay listed until someone picks a replacement.
        </Callout>
      )}

      {s.provider === 'openbao' && (
        <div className="mt-4 grid max-w-[760px] grid-cols-[160px_1fr] gap-x-3 gap-y-1.5 text-sm2">
          <span className="text-zinc-500">Vault address</span>
          <span className="font-mono text-xs">{s.address || '—'}</span>
          <span className="text-zinc-500">How Keyhole reaches it</span>
          <span>{route(s)}</span>
          <span className="text-zinc-500">Signs in with</span>
          <span>{AUTH[s.auth_method] ?? (s.auth_method || '—')}</span>
          <span className="text-zinc-500">Reads from</span>
          <span className="font-mono text-xs">{s.kv_mount ? `${s.kv_mount}/${s.kv_path_prefix}` : '—'}</span>
          <span className="text-zinc-500">Cache values for</span>
          <span>{s.cache_default_ttl_seconds} seconds</span>
          <span className="text-zinc-500">Certificate</span>
          {s.skip_tls_verify ? (
            <span className="flex items-center gap-3">
              <span className="font-semibold text-red-400">Not verified — traffic can be intercepted</span>
              {admin && (
                <Soon>
                  <button type="button" disabled className="text-brass opacity-40">
                    Upload certificate
                  </button>
                </Soon>
              )}
            </span>
          ) : (
            <span className="font-mono text-xs">{s.ca_bundle_name || '—'}</span>
          )}
        </div>
      )}

      {s.provider === 'local' && !admin && <div className="mt-3 text-xs text-zinc-500">Only admins manage organization keys. Put your own keys in a cabinet.</div>}

      {s.keys.length === 0 ? (
        <div className="mt-4 max-w-[960px] rounded-[10px] border border-edge bg-panel p-10 text-center text-[13px] text-zinc-400">
          {s.provider === 'local' ? 'No keys yet. Add one, or import from a .env file.' : 'No keys found under this path yet.'}
        </div>
      ) : (
        <Table cols={KEY_COLS} head={['Name', 'Created', 'Last rotated', 'Expiry', 'Used by', 'Value', '']} className="mt-4 max-w-[1060px]">
          {s.keys.map((k) => {
            const expires = ms(k.expires_at)
            const soon = expires != null && expires - now < 14 * DAY
            return (
              <Row key={k.id} cols={KEY_COLS}>
                <div className="min-w-0">
                  <div className={cx('truncate font-mono text-sm2', k.status === 'source_removed' && 'text-zinc-500 line-through')}>{k.name}</div>
                  {k.cabinet && <div className="truncate text-2xs text-zinc-500">in cabinet {k.cabinet.name}</div>}
                </div>
                <div className="text-zinc-400">{ago(ms(k.created_at), now)}</div>
                <div className="text-zinc-400">{k.rotated_at ? ago(ms(k.rotated_at), now) : '—'}</div>
                <div className={cx(soon ? 'font-medium text-amber-400' : 'text-zinc-400')}>{expires != null ? until(expires, now) : '—'}</div>
                <div className="text-zinc-400">{k.used_by ? plural(k.used_by, 'tool') : '—'}</div>
                <div>
                  <LockedDots />
                </div>
                <div className="text-right">
                  {s.provider === 'local' && admin && !removed && (
                    <Soon>
                      <button type="button" disabled aria-label={`Actions for ${k.name} (coming soon)`} className="rounded-md px-2 py-0.5 text-zinc-500 opacity-40">
                        ⋯
                      </button>
                    </Soon>
                  )}
                </div>
              </Row>
            )
          })}
        </Table>
      )}
      {s.provider === 'openbao' && <div className="mt-2 text-xs text-zinc-500">Values stay in your vault. Keyhole reads them at call time and caches for {s.cache_default_ttl_seconds} s.</div>}
    </div>
  )
}

