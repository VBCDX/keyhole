import { useEffect, useState } from 'react'
import { initials, plural } from '../lib/format'
import { actions, agentById, hasWorkspaceAccess, isAdminRole, keyById, liveTool, me, orgTools, toolById, useDB, userById, workspacePeople, canAdminWorkspace } from '../lib/store'
import type { Cabinet, PlayerRef, Workspace } from '../lib/types'
import { ImpactDialog, ImpactRows } from '../components/shared'
import { SECRET_LINE, SecretField } from '../components/keyhole'
import { Avatar, Button, Checkbox, Dot, Field, Footer, Input, Modal, Pill, Radio, Select } from '../components/ui'
import { useWorkspace } from './workspaces'

function LockGlyph() {
  return (
    <svg width="9" height="9" viewBox="0 0 24 24" fill="none" stroke="#a1a1aa" strokeWidth="2.5" aria-hidden>
      <rect x="4" y="10" width="16" height="11" rx="2" />
      <path d="M8 10V7a4 4 0 0 1 8 0v3" />
    </svg>
  )
}

export function CabinetsTab() {
  const d = useDB()
  const w = useWorkspace()
  // Workspace admins manage every cabinet in their workspace, like org admins.
  const admin = canAdminWorkspace(d, w.id)
  const cabinets = d.cabinets.filter((c) => c.workspaceId === w.id)
  const [creating, setCreating] = useState(false)
  const [lockFor, setLockFor] = useState<Cabinet | null>(null)
  const [reassign, setReassign] = useState<Cabinet | null>(null)
  const [newOwner, setNewOwner] = useState('')
  const [deleting, setDeleting] = useState<Cabinet | null>(null)
  const isMember = w.userIds.includes(d.currentUserId) || admin

  const lockSummary = (c: Cabinet) => (c.access === 'everyone' ? 'everyone in this workspace' : `${plural(c.access.length, 'player')} on the lock list`)

  return (
    <div className="mt-5 max-w-[1000px]">
      <div className="flex items-center justify-between">
        <div className="text-sm2 text-zinc-500">A cabinet is your own small set of keys and tools inside this workspace.</div>
        {isMember && (
          <Button variant="primary" onClick={() => setCreating(true)}>
            New cabinet
          </Button>
        )}
      </div>
      {cabinets.length === 0 ? (
        <div className="mt-4 rounded-[10px] border border-edge bg-panel p-10 text-center text-[13px] text-zinc-400">No cabinets yet. Make one to keep a few keys and tools of your own here, optionally locked to chosen people or agents.</div>
      ) : (
        <div className="mt-4 grid grid-cols-3 gap-4">
          {cabinets.map((c) => {
            // Creating a cabinet grants nothing (rule 3): its manager manages it. When there's no manager who
            // can use the workspace, it keeps working exactly as before and the org admins manage it.
            const creator = userById(d, c.createdBy)
            const manager = userById(d, c.managedBy)
            const managerHere = hasWorkspaceAccess(d, c.managedBy, w.id)
            const mine = managerHere && c.managedBy === d.currentUserId
            const creatorRole = creator?.roles[w.orgId]
            const creatorStillHere = !!creatorRole && (isAdminRole(creatorRole) || w.userIds.includes(creator!.id))
            const why = !manager ? (creatorStillHere ? 'Manager left' : 'Creator left') : manager.locked ? 'Manager locked' : 'Manager suspended'
            return (
              <div key={c.id} className="flex flex-col gap-3 rounded-[10px] border border-edge bg-panel p-[18px]">
                <div className="flex items-center gap-2">
                  <div className="truncate text-md font-semibold">{c.name}</div>
                  {c.access !== 'everyone' && (
                    <Pill>
                      <LockGlyph />
                      Locked
                    </Pill>
                  )}
                </div>
                <div className="flex flex-col gap-1.5 text-xs text-zinc-500">
                  {creator && <div>Created by {creator.name}</div>}
                  {manager && managerHere ? (
                    <div className="flex items-center gap-2">
                      <Avatar initials={initials(manager.name)} />
                      <span>Managed by {mine ? `${manager.name} (you)` : manager.name}</span>
                    </div>
                  ) : (
                    <div>{why} — still working; admins manage it</div>
                  )}
                </div>
                <div className="text-xs text-zinc-400">
                  {plural(c.keyIds.length, 'key')} · {plural(c.tools.length, 'tool')} · {lockSummary(c)}
                </div>
                {!managerHere && admin ? (
                  <div className="mt-auto flex gap-3">
                    <button className="text-sm2 text-brass hover:text-brass-light" onClick={() => setReassign(c)}>
                      Reassign
                    </button>
                    <button className="text-sm2 text-brass hover:text-brass-light" onClick={() => setLockFor(c)}>
                      Change lock
                    </button>
                    <button className="text-sm2 text-red-400 hover:text-red-300" onClick={() => setDeleting(c)}>
                      Delete
                    </button>
                  </div>
                ) : mine || admin ? (
                  <div className="mt-auto flex gap-3">
                    {mine && (
                      <button className="text-sm2 text-brass hover:text-brass-light" onClick={() => setLockFor(c)}>
                        Change lock
                      </button>
                    )}
                    <button className="text-sm2 text-red-400 hover:text-red-300" onClick={() => setDeleting(c)}>
                      Delete
                    </button>
                  </div>
                ) : null}
              </div>
            )
          })}
        </div>
      )}

      <NewCabinetModal open={creating} onClose={() => setCreating(false)} ws={w} />
      <ChangeLockModal cabinet={lockFor} onClose={() => setLockFor(null)} ws={w} />
      <Modal open={!!reassign} onClose={() => setReassign(null)} title={`Who manages ${reassign?.name}?`} width={440}>
        <div className="-mt-2 text-xs text-zinc-500">The manager changes its keys, tools and lock list. “Created by” doesn’t change.</div>
        <Field label="New manager">
          <Select value={newOwner} onChange={(e) => setNewOwner(e.target.value)}>
            <option value="">Pick a person on this workspace…</option>
            {workspacePeople(d, w).map((u) => (
              <option key={u.id} value={u.id}>
                {u.name}
              </option>
            ))}
          </Select>
        </Field>
        <Footer>
          <Button size="lg" onClick={() => setReassign(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            disabled={!newOwner}
            onClick={() => {
              actions.reassignCabinet(reassign!.id, newOwner)
              setReassign(null)
              setNewOwner('')
            }}
          >
            Reassign
          </Button>
        </Footer>
      </Modal>
      <ImpactDialog
        open={!!deleting}
        onClose={() => setDeleting(null)}
        title={`Delete ${deleting?.name}?`}
        rows={
          deleting
            ? [
                ['Keys deleted with it', deleting.keyIds.map((id) => keyById(d, id)?.name).join(', ') || 'None', 'amber'],
                ['Tools', deleting.tools.map((t) => toolById(d, t.toolId)?.displayName).join(', ') || 'None'],
                ['Who could use it', lockSummary(deleting)],
              ]
            : []
        }
        body="Agents on its lock list lose these keys on their next request."
        confirmLabel="Delete cabinet"
        onConfirm={() => deleting && actions.deleteCabinet(deleting.id)}
      />
    </div>
  )
}

/** Everyone who can currently use the workspace: its people (members plus org admins) and active agents. */
const workspacePlayers = (d: ReturnType<typeof useDB>, ws: Workspace): PlayerRef[] => [
  ...workspacePeople(d, ws).map((u) => ({ kind: 'user' as const, id: u.id })),
  ...ws.agentIds.filter((id) => agentById(d, id)?.status === 'active').map((id) => ({ kind: 'agent' as const, id })),
]

/* ------------------------------------------------------------------ */
/* Access chooser — "Everyone" or "Only these players" (people + agents) */
/* ------------------------------------------------------------------ */
function AccessPicker({ ws, value, onChange }: { ws: Workspace; value: 'everyone' | PlayerRef[]; onChange: (v: 'everyone' | PlayerRef[]) => void }) {
  const d = useDB()
  const list = value === 'everyone' ? [] : value
  const options: (PlayerRef & { label: string })[] = [
    ...workspacePeople(d, ws).map((u) => ({ kind: 'user' as const, id: u.id, label: u.name })),
    ...ws.agentIds.filter((id) => agentById(d, id)?.status === 'active').map((id) => ({ kind: 'agent' as const, id, label: agentById(d, id)!.label })),
  ].filter((o) => !list.some((p) => p.kind === o.kind && p.id === o.id))
  const label = (p: PlayerRef) => (p.kind === 'user' ? userById(d, p.id)?.name : agentById(d, p.id)?.label)
  return (
    <div className="flex flex-col gap-2">
      <Radio name="access" checked={value === 'everyone'} onChange={() => onChange('everyone')}>
        <span className={value === 'everyone' ? 'text-[13px]' : 'text-[13px] text-zinc-400'}>Everyone in this workspace</span>
      </Radio>
      <Radio name="access" checked={value !== 'everyone'} onChange={() => value === 'everyone' && onChange([{ kind: 'user', id: d.currentUserId }])}>
        <span className={value !== 'everyone' ? 'text-[13px]' : 'text-[13px] text-zinc-400'}>Only these players</span>
        {value !== 'everyone' && (
          <div className="mt-2 flex flex-wrap gap-2">
            {list.map((p) => (
              <span key={p.kind + p.id} className="inline-flex items-center gap-1.5 rounded-full border border-chip bg-line px-2.5 py-1 text-xs">
                {p.kind === 'agent' && <span className="font-mono text-2xs text-zinc-400">agent</span>}
                {label(p)}
                <button type="button" aria-label={`Remove ${label(p)}`} className="text-zinc-600 hover:text-zinc-300" onClick={(e) => { e.preventDefault(); onChange(list.filter((x) => !(x.kind === p.kind && x.id === p.id))) }}>
                  ✕
                </button>
              </span>
            ))}
            {options.length > 0 && (
              <select
                aria-label="Add player"
                value=""
                onChange={(e) => {
                  const [kind, id] = e.target.value.split(':') as ['user' | 'agent', string]
                  if (id) onChange([...list, { kind, id }])
                }}
                className="cursor-pointer appearance-none bg-transparent text-xs text-zinc-500 outline-none hover:text-zinc-300"
              >
                <option value="">+ add player</option>
                <optgroup label="People">
                  {options.filter((o) => o.kind === 'user').map((o) => (
                    <option key={o.id} value={`user:${o.id}`}>
                      {o.label}
                    </option>
                  ))}
                </optgroup>
                <optgroup label="Agents">
                  {options.filter((o) => o.kind === 'agent').map((o) => (
                    <option key={o.id} value={`agent:${o.id}`}>
                      {o.label}
                    </option>
                  ))}
                </optgroup>
              </select>
            )}
          </div>
        )}
      </Radio>
    </div>
  )
}

function ChangeLockModal({ cabinet, onClose, ws }: { cabinet: Cabinet | null; onClose: () => void; ws: Workspace }) {
  const d = useDB()
  const [access, setAccess] = useState<Cabinet['access']>('everyone')
  useEffect(() => {
    if (cabinet) setAccess(cabinet.access)
  }, [cabinet])
  // Narrowing the lock list takes the cabinet away from someone, so the change is previewed.
  const name = (p: PlayerRef) => (p.kind === 'user' ? userById(d, p.id)?.name : agentById(d, p.id)?.label) ?? p.id
  const before = cabinet ? (cabinet.access === 'everyone' ? workspacePlayers(d, ws) : cabinet.access) : []
  const after = access === 'everyone' ? workspacePlayers(d, ws) : access
  const losing = before.filter((p) => !after.some((x) => x.kind === p.kind && x.id === p.id))
  return (
    <Modal open={!!cabinet} onClose={onClose} width={520} title={`Who can use ${cabinet?.name}?`}>
      <AccessPicker ws={ws} value={access} onChange={setAccess} />
      {losing.length > 0 && <ImpactRows rows={[['Lose use of this cabinet', losing.map(name).join(', '), 'amber']]} />}
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant={losing.length ? 'danger' : 'primary'}
          disabled={Array.isArray(access) && !access.length}
          onClick={() => {
            actions.setCabinetAccess(cabinet!.id, access)
            onClose()
          }}
        >
          Save lock
        </Button>
      </Footer>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* New cabinet                                                         */
/* ------------------------------------------------------------------ */
type NewKey = { rid: number; name: string; length: number }
const KEY_NAME = /^[A-Za-z0-9_./-]+$/

function NewCabinetModal({ open, onClose, ws }: { open: boolean; onClose: () => void; ws: Workspace }) {
  const d = useDB()
  const [name, setName] = useState('')
  const [newKeys, setNewKeys] = useState<NewKey[]>([])
  const [picking, setPicking] = useState(false)
  const [picked, setPicked] = useState<string[]>([])
  const [toolId, setToolId] = useState('')
  const [slotMap, setSlotMap] = useState<Record<string, string | null>>({})
  /** The key the person picked by hand for each slot, as opposed to ones filled for them. */
  const [manual, setManual] = useState<Record<string, string>>({})
  const [access, setAccess] = useState<Cabinet['access']>('everyone')
  useEffect(() => {
    if (open) {
      setName(`${me(d).name.split(' ')[0]}'s set`)
      setNewKeys([{ rid: 1, name: '', length: 0 }])
      setPicking(false)
      setPicked([])
      setToolId('')
      setSlotMap({})
      setManual({})
      setAccess('everyone')
    }
  }, [open]) // eslint-disable-line

  // The workspace's exposed keys are the access boundary: a cabinet can only reuse keys already exposed here.
  const localKeys = ws.keyIds.map((id) => keyById(d, id)).filter((k): k is NonNullable<typeof k> => !!k && k.storeId === 'st_local' && !k.cabinetId && !k.sourceRemoved)
  // A half-filled row would silently drop the pasted value, so every row is either empty or complete.
  const rowError = (k: NewKey, i: number) => {
    const n = k.name.trim()
    if (!n && !k.length) return null
    if (!n) return 'Name this key, or clear its value.'
    if (!k.length) return `Paste a value for ${n}.`
    if (!KEY_NAME.test(n)) return 'Use letters, numbers, and _ . / - only.'
    if (newKeys.some((x, j) => j < i && x.name.trim() === n)) return `Another key here is already named ${n}.`
    if (picked.some((id) => keyById(d, id)?.name === n)) return `${n} is already picked from ${ws.name}.`
    return null
  }
  const errors = newKeys.map(rowError)
  const validNew = newKeys.filter((k, i) => k.name.trim() && k.length && !errors[i])
  const cabinetKeyOptions = [...validNew.map((k) => ({ value: `new:${k.name.trim()}`, label: k.name.trim() })), ...picked.map((id) => ({ value: id, label: keyById(d, id)!.name }))]
  // Like workspaces, cabinets only take published tools, filled from the version agents actually get.
  const tool = liveTool(toolById(d, toolId))
  const tools = orgTools(d).filter((t) => t.published)

  // Auto-fill slots: a hand-picked key stays, then an exact name match, else the only cabinet key.
  useEffect(() => {
    if (!tool) return
    setSlotMap(
      Object.fromEntries(
        tool.slots.map((s) => {
          const current = slotMap[s.name]
          const stillThere = !!current && cabinetKeyOptions.some((o) => o.value === current)
          if (manual[s.name] === current && stillThere) return [s.name, current]
          const exact = cabinetKeyOptions.find((o) => o.label === s.name)
          return [s.name, exact?.value ?? (cabinetKeyOptions.length === 1 ? cabinetKeyOptions[0].value : stillThere ? current : null)]
        }),
      ),
    )
  }, [toolId, cabinetKeyOptions.map((o) => o.value).join()]) // eslint-disable-line

  const ok = name.trim() && !errors.some(Boolean) && (validNew.length || picked.length) && (!tool || tool.slots.every((s) => slotMap[s.name])) && (access === 'everyone' || access.length > 0)

  return (
    <Modal open={open} onClose={onClose} width={560} title="New cabinet">
      <Field label="Name">
        <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
      </Field>
      <Field label="Keys" optional="cabinets hold Local keys only" hint={SECRET_LINE}>
        <div className="flex flex-col gap-2">
          {newKeys.map((k, i) => (
            <div key={k.rid} className="flex flex-col gap-1">
              <SecretField
                id={`cabinet-key-${k.rid}`}
                compact
                onLength={(n) => setNewKeys((prev) => prev.map((x, j) => (j === i ? { ...x, length: n } : x)))}
                placeholder="Paste the value"
                prefix={
                  <input
                    aria-label="Key name"
                    value={k.name}
                    placeholder="key_name"
                    onChange={(e) => setNewKeys(newKeys.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                    className="w-40 border-r border-brass/20 bg-transparent pr-2 font-mono text-sm2 text-zinc-200 outline-none placeholder:text-zinc-600"
                  />
                }
              />
              {errors[i] && <div className="text-xs2 text-red-400">{errors[i]}</div>}
            </div>
          ))}
        </div>
      </Field>
      <div className="-mt-2 flex gap-4">
        <button type="button" className="text-sm2 text-brass hover:text-brass-light" onClick={() => setNewKeys([...newKeys, { rid: Date.now(), name: '', length: 0 }])}>
          Add another key
        </button>
        <button type="button" className="text-sm2 text-brass hover:text-brass-light" onClick={() => setPicking(!picking)}>
          Or pick a key {ws.name} exposes
        </button>
      </div>
      {picking && (
        <div className="-mt-1 flex flex-col gap-2 rounded-lg border border-edge bg-page p-3">
          <div className="eyebrow-sm">Local keys exposed in {ws.name}</div>
          {localKeys.length ? (
            localKeys.map((k) => <Checkbox key={k.id} checked={picked.includes(k.id)} onChange={(v) => setPicked(v ? [...picked, k.id] : picked.filter((x) => x !== k.id))} label={<span className="font-mono text-sm2">{k.name}</span>} />)
          ) : (
            <span className="text-xs text-zinc-500">{ws.name} doesn’t expose any Local keys.</span>
          )}
          <div className="text-xs2 text-zinc-500">Only keys this workspace already exposes. Admins choose those on the Summary tab.</div>
        </div>
      )}
      <Field label="Tool" optional>
        <Select value={toolId} onChange={(e) => setToolId(e.target.value)}>
          <option value="">No tool</option>
          {tools.map((t) => (
            <option key={t.id} value={t.id}>
              {t.published!.displayName} · v{t.published!.version}
            </option>
          ))}
        </Select>
      </Field>
      {tool &&
        tool.slots.map((s) => (
          <div key={s.id} className="-mt-2 flex items-center gap-3 rounded-lg border border-edge bg-page p-3">
            <span className="text-[13px]">{tool.displayName}</span>
            <span className="font-mono text-xs text-brass-light">{s.name}</span>
            <span className="text-zinc-600">→</span>
            <select
              aria-label={`Key for ${s.name}`}
              value={slotMap[s.name] ?? ''}
              onChange={(e) => {
                setSlotMap({ ...slotMap, [s.name]: e.target.value || null })
                setManual({ ...manual, [s.name]: e.target.value })
              }}
              className="min-w-0 flex-1 appearance-none bg-transparent font-mono text-xs text-zinc-200 outline-none"
            >
              <option value="">Pick a cabinet key…</option>
              {cabinetKeyOptions.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
            {slotMap[s.name] ? (
              <span className="inline-flex shrink-0 items-center gap-1.5 text-xs2 text-zinc-500">
                <Dot health="healthy" size={6} />
                {cabinetKeyOptions.find((o) => o.value === slotMap[s.name])?.label === s.name ? 'Matched by name' : manual[s.name] === slotMap[s.name] ? 'Chosen by you' : 'Chosen for you (only key)'}
              </span>
            ) : (
              <span className="text-xs2 text-amber-400">Missing</span>
            )}
          </div>
        ))}
      <Field label="Who can use it">
        <AccessPicker ws={ws} value={access} onChange={setAccess} />
      </Field>
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant="primary"
          disabled={!ok}
          onClick={() => {
            actions.createCabinet({ workspaceId: ws.id, name: name.trim(), newKeys: validNew.map((k) => ({ name: k.name.trim(), length: k.length })), pickedKeyIds: picked, tools: tool ? [{ toolId, slotMap }] : [], access })
            onClose()
          }}
        >
          Create cabinet
        </Button>
      </Footer>
    </Modal>
  )
}
