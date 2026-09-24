import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { COMING_SOON_STORES } from '../lib/catalog'
import { DAY, ago, plural, until } from '../lib/format'
import { actions, isAdmin, keyUsage, lastUsedForKey, orgConnectors, orgKeys, orgStores, storeById, useDB, useNow } from '../lib/store'
import type { Key, SecretStore } from '../lib/types'
import { LockedDots, SECRET_LINE, SecretField } from '../components/keyhole'
import { ImpactDialog, ListBody } from '../components/shared'
import { Button, Checkbox, Dot, FOCUS_RING, Field, Footer, Input, Menu, Modal, Pill, Row, Select, SlideOver, Table, Textarea, activateOnKey, cx } from '../components/ui'

function storeHealth(d: ReturnType<typeof useDB>, s: SecretStore) {
  const conn = s.route && s.route !== 'public' ? d.connectors.find((c) => c.id === s.route) : null
  if (s.health === 'sealed') return { dot: 'error' as const, text: 'Sealed', tone: 'text-red-400' }
  if (s.health === 'unreachable') return { dot: 'error' as const, text: 'Can’t reach', tone: 'text-red-400' }
  if (s.health === 'denied') return { dot: 'error' as const, text: 'Not allowed', tone: 'text-red-400' }
  if (conn?.health === 'offline') return { dot: 'degraded' as const, text: `Connector ${conn.name} offline`, tone: 'text-amber-400' }
  return { dot: 'healthy' as const, text: '', tone: '' }
}

function TypeMark({ type }: { type: SecretStore['type'] }) {
  return <span className="flex size-6 min-w-6 items-center justify-center rounded-md border border-chip bg-line text-2xs font-bold text-zinc-400">{type === 'local' ? 'L' : 'B'}</span>
}

export function StoreRow({ s, onClick, chevron = true }: { s: SecretStore; onClick?: () => void; chevron?: boolean }) {
  const d = useDB()
  const now = useNow()
  const keys = d.keys.filter((k) => k.storeId === s.id && !k.cabinetId)
  const h = storeHealth(d, s)
  return (
    <div
      onClick={onClick}
      role={onClick ? 'link' : undefined}
      tabIndex={onClick ? 0 : undefined}
      onKeyDown={onClick ? activateOnKey(onClick) : undefined}
      className={cx('flex items-center gap-2.5 rounded-[10px] border border-edge bg-panel px-4 py-3.5', onClick && cx('cursor-pointer hover:border-zinc-700', FOCUS_RING))}
    >
      <TypeMark type={s.type} />
      <Dot health={h.dot} />
      <span className="text-md font-semibold">{s.name}</span>
      {s.type === 'openbao' && <Pill>OpenBao</Pill>}
      <span className="text-xs text-zinc-500">
        {plural(keys.length, 'key')} · checked {ago(s.checkedAt, now).toLowerCase()}
      </span>
      {h.text && <Pill tone={h.dot === 'degraded' ? 'amber' : 'red'}>{h.text}</Pill>}
      {chevron && <span className="ml-auto text-xs text-zinc-600">›</span>}
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Stores tab                                                          */
/* ------------------------------------------------------------------ */
export function StoresTab() {
  const d = useDB()
  const nav = useNavigate()
  const { orgId } = useParams()
  const stores = orgStores(d)
  const local = stores.find((s) => s.type === 'local')
  const localKeys = local ? d.keys.filter((k) => k.storeId === local.id).length : 0
  const orphans = orgKeys(d).filter((k) => k.sourceRemoved)
  const [chooser, setChooser] = useState(false)
  const admin = isAdmin(d)

  return (
    <div className="mt-5 max-w-[760px]">
      <div className="flex flex-col gap-2.5">
        {stores.map((s) => (
          <StoreRow key={s.id} s={s} onClick={() => nav(`/orgs/${orgId}/stores/${s.id}`)} />
        ))}
      </div>
      {localKeys === 0 && <div className="mt-2 text-xs text-zinc-500">Included with your organization — paste your first key to get going.</div>}
      {orphans.length > 0 && <SourceRemovedKeys keys={orphans} />}
      <ChooserModal open={chooser} onClose={() => setChooser(false)} onOpenBao={() => nav(`/orgs/${orgId}/stores/connect-openbao`)} />
      {admin && <AddStoreHook onOpen={() => setChooser(true)} />}
    </div>
  )
}

/** Lets the org header's "Add store" button open this tab's chooser. */
function AddStoreHook({ onOpen }: { onOpen: () => void }) {
  const [params, setParams] = useSearchParams()
  useEffect(() => {
    if (params.get('add') === 'store') {
      onOpen()
      params.delete('add')
      setParams(params, { replace: true })
    }
  }, [params, setParams, onOpen])
  return null
}

function ChooserModal({ open, onClose, onOpenBao }: { open: boolean; onClose: () => void; onOpenBao: () => void }) {
  return (
    <Modal open={open} onClose={onClose} width={640} title="Add store">
      <div className="-mt-3 text-[13px] text-zinc-400">A store is where keys come from.</div>
      <div className="mt-1 grid grid-cols-2 gap-3">
        <div className="flex flex-col gap-2 rounded-[10px] border border-edge bg-rail p-4" aria-disabled>
          <div className="flex items-center gap-2">
            <Dot health="healthy" />
            <span className="text-md font-semibold">Local</span>
          </div>
          <div className="text-xs leading-normal text-zinc-500">Keys kept encrypted by Keyhole.</div>
          <div className="mt-auto text-xs2 text-zinc-600">Included — already set up</div>
        </div>
        <div className="flex flex-col gap-2 rounded-[10px] border border-zinc-700 bg-rail p-4">
          <div className="text-md font-semibold">OpenBao</div>
          <div className="text-xs leading-normal text-zinc-500">Read keys from a vault you already run — even behind your firewall.</div>
          <Button variant="primary" size="sm" className="mt-auto self-start" onClick={onOpenBao}>
            Connect
          </Button>
        </div>
      </div>
      <div className="grid grid-cols-3 gap-3">
        {COMING_SOON_STORES.map((n) => (
          <div key={n} className="rounded-[10px] border border-line bg-rail px-4 py-3.5 opacity-55" aria-disabled>
            <div className="text-[13px] font-medium text-zinc-400">{n}</div>
            <div className="mt-1 text-2xs text-zinc-600">Coming soon</div>
          </div>
        ))}
      </div>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* Keys whose store was removed                                        */
/* ------------------------------------------------------------------ */
const ORPHAN_COLS = '1.6fr 1fr 1fr 1.4fr'
function SourceRemovedKeys({ keys }: { keys: Key[] }) {
  const d = useDB()
  const [resolving, setResolving] = useState<Key | null>(null)
  const [replacement, setReplacement] = useState('')
  const candidates = orgKeys(d).filter((k) => !k.sourceRemoved && !k.cabinetId)
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
            <div className="text-zinc-400">{plural(keyUsage(d, k.id).length, 'tool')}</div>
            <div className="flex items-center gap-2.5">
              <Pill tone="amber">Source removed</Pill>
              <button className="text-xs text-brass hover:text-brass-light" onClick={() => setResolving(k)}>
                Resolve
              </button>
            </div>
          </Row>
        ))}
      </Table>
      <Modal open={!!resolving} onClose={() => setResolving(null)} title={`Resolve ${resolving?.name}`} width={480}>
        <div className="text-sm2 text-zinc-400">
          Its store is gone. Pick a replacement key to fill the same slots — or remove it and leave those slots empty.
        </div>
        <Field label="Replacement key">
          <Select mono value={replacement} onChange={(e) => setReplacement(e.target.value)}>
            <option value="">Choose a key…</option>
            {candidates.map((k) => (
              <option key={k.id} value={k.id}>
                {k.name} · {storeById(d, k.storeId)?.name}
              </option>
            ))}
          </Select>
        </Field>
        <Footer>
          <Button
            size="lg"
            onClick={() => {
              actions.resolveKey(resolving!.id, null)
              setResolving(null)
            }}
          >
            Remove key
          </Button>
          <Button
            size="lg"
            variant="primary"
            disabled={!replacement}
            onClick={() => {
              actions.resolveKey(resolving!.id, replacement)
              setResolving(null)
              setReplacement('')
            }}
          >
            Use replacement
          </Button>
        </Footer>
      </Modal>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Store detail (Local keys table, or OpenBao details)                 */
/* ------------------------------------------------------------------ */
const KEY_COLS = '1.6fr 1fr 1fr 1fr 0.9fr 0.9fr 36px'

export function StoreDetail() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const { orgId, storeId } = useParams()
  const [params, setParams] = useSearchParams()
  const s = storeById(d, storeId!)
  const [adding, setAdding] = useState(params.get('add') === '1')
  const [importing, setImporting] = useState(false)
  const [replacing, setReplacing] = useState<Key | null>(null)
  const [deleting, setDeleting] = useState<Key | null>(null)
  const [removing, setRemoving] = useState(false)
  const admin = isAdmin(d)

  useEffect(() => {
    if (params.get('add')) {
      params.delete('add')
      setParams(params, { replace: true })
    }
  }, []) // eslint-disable-line

  if (!s) return <div className="mt-6 text-sm text-zinc-400">This store no longer exists. <Link to={`/orgs/${orgId}/stores`}>Back to stores</Link></div>
  const keys = d.keys.filter((k) => k.storeId === s.id)
  const route = s.route === 'public' ? 'Reachable from the internet' : s.route ? `Through connector ${orgConnectors(d).find((c) => c.id === s.route)?.name ?? '(revoked)'}` : ''

  const impactFor = (k: Key) => {
    const uses = keyUsage(d, k.id)
    const wsCount = new Set(uses.map((u) => u.ws?.id ?? u.cabinet?.workspaceId)).size
    return `Used by ${plural(uses.length, 'tool')} across ${plural(wsCount, 'workspace')} · last used ${ago(lastUsedForKey(d, k.id), now).toLowerCase()}`
  }

  return (
    <div className="mt-5">
      <div className="flex items-center justify-between gap-4">
        <div className="max-w-[760px] flex-1">
          <StoreRow s={s} chevron={false} />
        </div>
        {s.type === 'local' ? (
          <div className="flex gap-2">
            <Button onClick={() => setImporting(true)}>Import from .env</Button>
            <Button variant="primary" onClick={() => setAdding(true)}>
              Add key
            </Button>
          </div>
        ) : (
          admin && (
            <div className="flex gap-2">
              <Button onClick={() => nav(`/orgs/${orgId}/stores/connect-openbao?edit=${s.id}`)}>Test connection</Button>
              <Button variant="danger" onClick={() => setRemoving(true)}>
                Remove store
              </Button>
            </div>
          )
        )}
      </div>

      {s.type === 'openbao' && (
        <div className="mt-4 grid max-w-[760px] grid-cols-[160px_1fr] gap-x-3 gap-y-1.5 text-sm2">
          <span className="text-zinc-500">Vault address</span>
          <span className="font-mono text-xs">{s.address}</span>
          <span className="text-zinc-500">How Keyhole reaches it</span>
          <span>{route}</span>
          <span className="text-zinc-500">Signs in with</span>
          <span>{{ approle: 'AppRole', kubernetes: 'Kubernetes', token: 'Token' }[s.auth ?? 'approle']}</span>
          <span className="text-zinc-500">Reads from</span>
          <span className="font-mono text-xs">{s.path}</span>
          <span className="text-zinc-500">Cache values for</span>
          <span>{s.cacheSeconds} seconds</span>
        </div>
      )}

      {keys.length === 0 ? (
        <div className="mt-4 max-w-[960px] rounded-[10px] border border-edge bg-panel p-10 text-center text-[13px] text-zinc-400">
          {s.type === 'local' ? 'No keys yet. Add one, or import from a .env file.' : 'No keys found under this path yet.'}
        </div>
      ) : (
        <Table cols={KEY_COLS} head={['Name', 'Created', 'Last rotated', 'Expiry', 'Used by', 'Value', '']} className="mt-4 max-w-[1060px]">
          <ListBody cols={KEY_COLS} what="keys">
            {keys.map((k) => {
              const uses = keyUsage(d, k.id)
              const cab = k.cabinetId ? d.cabinets.find((c) => c.id === k.cabinetId) : null
              const soon = k.expiresAt && k.expiresAt - now < 14 * DAY
              return (
                <Row key={k.id} cols={KEY_COLS}>
                  <div className="min-w-0">
                    <div className="truncate font-mono text-sm2">{k.name}</div>
                    {cab && <div className="truncate text-2xs text-zinc-500">in cabinet {cab.name}</div>}
                  </div>
                  <div className="text-zinc-400">{ago(k.createdAt, now)}</div>
                  <div className="text-zinc-400">{k.rotatedAt ? ago(k.rotatedAt, now) : '—'}</div>
                  <div className={cx(soon ? 'font-medium text-amber-400' : 'text-zinc-400')}>{k.expiresAt ? until(k.expiresAt, now) : '—'}</div>
                  <div className="text-zinc-400">{uses.length ? plural(uses.length, 'tool') : '—'}</div>
                  <div>
                    <LockedDots />
                  </div>
                  <div className="text-right">
                    {s.type === 'local' && (
                      <Menu
                        items={[
                          { label: 'Replace value', onClick: () => setReplacing(k) },
                          { label: 'Delete key', danger: true, onClick: () => setDeleting(k) },
                        ]}
                      />
                    )}
                  </div>
                </Row>
              )
            })}
          </ListBody>
        </Table>
      )}
      {s.type === 'openbao' && <div className="mt-2 text-xs text-zinc-500">Values stay in your vault. Keyhole reads them at call time and caches for {s.cacheSeconds} s.</div>}

      <AddKeyPanel open={adding} onClose={() => setAdding(false)} storeId={s.id} />
      <ImportEnvModal open={importing} onClose={() => setImporting(false)} />
      <ReplaceValueModal k={replacing} onClose={() => setReplacing(null)} />
      <ImpactDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={`Delete ${deleting?.name}?`}
        rows={deleting ? [['Impact', impactFor(deleting), keyUsage(d, deleting.id).length ? 'amber' : undefined], ['Workspaces exposing it', d.workspaces.filter((w) => w.keyIds.includes(deleting.id)).map((w) => w.name).join(', ') || 'None']] : []}
        body="Tools that use it will fail with a missing key slot until another key fills it."
        confirmLabel="Delete key"
        onConfirm={() => deleting && actions.deleteKey(deleting.id)}
      />
      <ImpactDialog
        open={removing}
        onClose={() => setRemoving(false)}
        title={`Remove ${s.name}?`}
        typeToConfirm={s.name}
        rows={[
          ['Keys from this store', String(keys.length)],
          ['Keys in use', `${keys.filter((k) => keyUsage(d, k.id).length || d.workspaces.some((w) => w.keyIds.includes(k.id))).length} — they’ll show “source removed”`, 'amber'],
          ['Last checked', ago(s.checkedAt, now)],
        ]}
        body="Keys still in use stay listed, struck through, until you pick a replacement or remove them."
        confirmLabel="Remove store"
        onConfirm={() => {
          actions.removeStore(s.id)
          nav(`/orgs/${orgId}/stores`)
        }}
      />
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Add key slide-over                                                  */
/* ------------------------------------------------------------------ */
function AddKeyPanel({ open, onClose, storeId }: { open: boolean; onClose: () => void; storeId: string }) {
  const d = useDB()
  const [name, setName] = useState('')
  const [len, setLen] = useState(0)
  const [expiry, setExpiry] = useState('')
  const [reminder, setReminder] = useState('')
  const [notes, setNotes] = useState('')
  const [nonce, setNonce] = useState(0)
  useEffect(() => {
    if (open) {
      setName('')
      setLen(0)
      setExpiry('')
      setReminder('')
      setNotes('')
      setNonce((n) => n + 1)
    }
  }, [open])
  const clash = d.keys.some((k) => k.storeId === storeId && k.name === name.trim())
  const badName = name && !/^[A-Za-z0-9_./-]+$/.test(name)
  const save = () => {
    actions.addKey({ name: name.trim(), storeId, length: len, expiresAt: expiry ? Date.now() + Number(expiry) * DAY : null, rotationReminderDays: reminder ? Number(reminder) : null, notes })
    onClose()
  }
  return (
    <SlideOver
      open={open}
      onClose={onClose}
      title="Add key"
      footer={
        <>
          <Button size="lg" onClick={onClose}>
            Cancel
          </Button>
          <Button size="lg" variant="primary" disabled={!name.trim() || !len || clash || !!badName} onClick={save}>
            Save key
          </Button>
        </>
      }
    >
      <Field label="Name" htmlFor="key-name" error={clash ? `A key named ${name.trim()} already exists here — use Replace value on it instead.` : badName ? 'Use letters, numbers, and _ . / - only.' : null}>
        <Input id="key-name" mono value={name} onChange={(e) => setName(e.target.value)} placeholder="stripe_secret" autoFocus />
      </Field>
      <Field label="Value" htmlFor="key-value" hint={SECRET_LINE}>
        <SecretField key={nonce} id="key-value" onLength={setLen} />
      </Field>
      <div className="flex gap-3">
        <div className="flex-1">
          <Field label="Expiry" optional>
            <Select value={expiry} onChange={(e) => setExpiry(e.target.value)}>
              <option value="">None</option>
              <option value="30">In 30 days</option>
              <option value="90">In 90 days</option>
              <option value="365">In 365 days</option>
            </Select>
          </Field>
        </div>
        <div className="flex-1">
          <Field label="Rotation reminder" optional>
            <Select value={reminder} onChange={(e) => setReminder(e.target.value)}>
              <option value="">None</option>
              <option value="30">Every 30 days</option>
              <option value="90">Every 90 days</option>
              <option value="180">Every 180 days</option>
            </Select>
          </Field>
        </div>
      </div>
      <Field label="Notes" optional>
        <Textarea value={notes} onChange={(e) => setNotes(e.target.value)} placeholder="Restricted key — charges read only" />
      </Field>
    </SlideOver>
  )
}

function ReplaceValueModal({ k, onClose }: { k: Key | null; onClose: () => void }) {
  const [len, setLen] = useState(0)
  useEffect(() => setLen(0), [k])
  return (
    <Modal open={!!k} onClose={onClose} title={<>Replace value of <span className="font-mono text-sm">{k?.name}</span></>} width={480}>
      <div className="text-sm2 text-zinc-400">The old value stops being used the moment you save. Tools keep working with the new one.</div>
      <Field label="New value" hint={SECRET_LINE}>
        <SecretField key={k?.id} onLength={setLen} autoFocus />
      </Field>
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant="primary"
          disabled={!len}
          onClick={() => {
            actions.replaceKeyValue(k!.id, len)
            onClose()
          }}
        >
          Replace value
        </Button>
      </Footer>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* Import from .env                                                    */
/* ------------------------------------------------------------------ */
type EnvRow = { name: string; length: number; exists: boolean; selected: boolean; invalid?: boolean }

/** Parses .env text into names + value lengths. The text itself is dropped right away. */
function parseEnv(text: string, existing: Set<string>): EnvRow[] {
  const rows: EnvRow[] = []
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim()
    if (!line || line.startsWith('#')) continue
    const m = line.replace(/^export\s+/, '').match(/^([A-Za-z_][A-Za-z0-9_.-]*)\s*=\s*(.*)$/)
    if (!m) continue
    let v = m[2].trim()
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) v = v.slice(1, -1)
    else v = v.replace(/\s+#.*$/, '')
    const exists = existing.has(m[1])
    rows.push({ name: m[1], length: v.length, exists, selected: !exists && v.length > 0, invalid: v.length === 0 })
  }
  return rows
}

export function ImportEnvModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const d = useDB()
  const [rows, setRows] = useState<EnvRow[] | null>(null)
  const [drag, setDrag] = useState(false)
  useEffect(() => setRows(null), [open])
  const existing = new Set(d.keys.filter((k) => k.storeId === 'st_local').map((k) => k.name))
  const take = (text: string) => setRows(parseEnv(text, existing))
  const readFile = (f: File) => f.text().then(take)
  const sel = rows?.filter((r) => r.selected) ?? []
  const COLS = '24px 1.6fr 1fr 1.2fr'

  return (
    <Modal open={open} onClose={onClose} width={680} title="Import from .env">
      {!rows ? (
        <>
          <div className="text-sm2 text-zinc-400">Paste the contents of a .env file, or drop the file here. Keyhole reads each value once to store it — nothing is shown on screen.</div>
          <label
            onDragOver={(e) => {
              e.preventDefault()
              setDrag(true)
            }}
            onDragLeave={() => setDrag(false)}
            onDrop={(e) => {
              e.preventDefault()
              setDrag(false)
              const f = e.dataTransfer.files[0]
              if (f) readFile(f)
            }}
            className={cx('flex flex-col items-center gap-3 rounded-[10px] border border-dashed p-6 text-center', drag ? 'border-brass/60 bg-brass/[0.04]' : 'border-zinc-700')}
          >
            <input
              aria-label="Paste .env contents"
              placeholder="Click here and paste (⌘V / Ctrl+V)"
              value=""
              onChange={() => {}}
              onPaste={(e) => {
                e.preventDefault()
                take(e.clipboardData.getData('text'))
              }}
              className="w-full max-w-sm rounded-lg border border-brass/25 bg-secret-bg px-3 py-2.5 text-center text-[13px] text-brass outline-none placeholder:text-zinc-500 focus:border-brass/50"
            />
            <div className="text-xs text-zinc-500">
              or drop a file · <span className="cursor-pointer text-brass hover:text-brass-light">browse</span>
              <input type="file" accept=".env,text/plain,*" className="sr-only" onChange={(e) => e.target.files?.[0] && readFile(e.target.files[0])} />
            </div>
          </label>
          <div className="text-xs2 text-zinc-500">{SECRET_LINE}</div>
        </>
      ) : rows.length === 0 ? (
        <>
          <div className="text-sm2 text-amber-400">No KEY=value lines found. Check the file and paste it again.</div>
          <Footer>
            <Button size="lg" onClick={() => setRows(null)}>
              Try again
            </Button>
          </Footer>
        </>
      ) : (
        <>
          <div className="text-sm2 text-zinc-400">
            {plural(rows.length, 'key')} detected. Values were read once and won't be shown.
          </div>
          <div className="overflow-hidden rounded-[10px] border border-edge bg-rail">
            <div className="grid gap-3 border-b border-line px-4 py-2.5" style={{ gridTemplateColumns: COLS }}>
              <div />
              <div className="th">Name</div>
              <div className="th">Value</div>
              <div className="th">Status</div>
            </div>
            {rows.map((r, i) => (
              <div key={r.name + i} className="grid items-center gap-3 border-b border-line px-4 py-3 text-[13px] last:border-b-0" style={{ gridTemplateColumns: COLS }}>
                <Checkbox checked={r.selected} disabled={r.invalid} onChange={(v) => setRows(rows.map((x, j) => (j === i ? { ...x, selected: v } : x)))} />
                <div className="font-mono text-sm2">{r.name}</div>
                <div className="masked text-xs text-zinc-500 [letter-spacing:0.15em]">•••• {r.length}</div>
                <div className={cx('text-xs', r.invalid ? 'text-zinc-500' : r.exists ? 'text-amber-400' : 'text-green-400')}>
                  {r.invalid ? 'Empty value — skipped' : r.exists ? 'Name already exists — importing replaces the value' : 'New'}
                </div>
              </div>
            ))}
          </div>
          <Footer>
            <Button size="lg" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="lg"
              variant="primary"
              disabled={!sel.length}
              onClick={() => {
                actions.importEnv(sel.map((r) => ({ name: r.name, length: r.length })))
                onClose()
              }}
            >
              Import {plural(sel.length, 'key')}
            </Button>
          </Footer>
        </>
      )}
    </Modal>
  )
}

