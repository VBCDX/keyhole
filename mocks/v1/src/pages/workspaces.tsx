import { useEffect, useMemo, useState } from 'react'
import { Link, Outlet, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { plural } from '../lib/format'
import { testCall } from '../lib/simulate'
import {
  actions,
  agentById,
  autoMatch,
  canSeeWorkspace,
  isAdmin,
  filledSlot,
  isAdminRole,
  keyById,
  liveTool,
  missingSlots,
  orgAdmins,
  orgAgents,
  orgEvents,
  orgKeys,
  orgStores,
  orgTools,
  orgWorkspaces,
  slotCandidates,
  storeById,
  toolById,
  unexposeImpact,
  useDB,
  userById,
  visibleEvents,
  wsById,
} from '../lib/store'
import type { Key, Workspace } from '../lib/types'
import { AgentsTable, AuditLog, ImpactDialog, ImpactRows, ListBody, UsersTable, useUserRows } from '../components/shared'
import { CopyChip } from '../components/keyhole'
import { Breadcrumb, Button, Checkbox, ConnPill, Dot, Field, Footer, Input, Modal, PageTitle, Row, Select, Table, Tabs, Toggle, cx } from '../components/ui'
import { CabinetsTab } from './cabinets'
import { ConnectTab } from './connect'

/* ------------------------------------------------------------------ */
/* List                                                                */
/* ------------------------------------------------------------------ */
const WS_COLS = '1.4fr 1.2fr 1.6fr 1fr 1.2fr'

function wsHealth(d: ReturnType<typeof useDB>, w: Workspace) {
  const conns = d.connectors.filter((c) => c.workspaceId === w.id)
  const kind = (c: (typeof conns)[number]) => (c.kind === 'sidecar' ? 'Sidecar' : 'Vault connector')
  const off = conns.find((c) => c.health === 'offline')
  if (off) return { h: 'offline' as const, t: `${kind(off)} offline` }
  const deg = conns.find((c) => c.health === 'degraded')
  if (deg) return { h: 'degraded' as const, t: `${kind(deg)} degraded` }
  const missing = w.tools.some((t) => missingSlots(d, w, t).length)
  if (missing) return { h: 'degraded' as const, t: 'Missing key slot' }
  if (w.keyIds.some((id) => keyById(d, id)?.sourceRemoved)) return { h: 'degraded' as const, t: 'Key source removed' }
  return { h: 'healthy' as const, t: 'Healthy' }
}

export function WorkspacesTable() {
  const d = useDB()
  const nav = useNavigate()
  const list = orgWorkspaces(d)
  return (
    <Table cols={WS_COLS} head={['Name', 'Organization', 'Connections', 'Players', 'Health']} className="max-w-[1020px]">
      <ListBody
        cols={WS_COLS}
        what="workspaces"
        empty={list.length ? undefined : <div className="p-10 text-center text-[13px] text-zinc-400">No workspaces yet. Create one to choose which keys and tools an app or AI can reach.</div>}
      >
        {list.map((w) => {
          const h = wsHealth(d, w)
          const users = w.userIds.length
          const agents = w.agentIds.filter((id) => agentById(d, id)?.status !== 'revoked').length
          return (
            <Row key={w.id} cols={WS_COLS} onClick={() => nav(`/workspaces/${w.id}/summary`)}>
              <div className="font-medium">{w.name}</div>
              <div className="text-zinc-400">{d.orgs.find((o) => o.id === w.orgId)?.name}</div>
              <div className="flex gap-1.5">
                <ConnPill label="HTTPS" on={w.https} />
                <ConnPill label="MCP" on={w.mcp} />
              </div>
              <div className="text-zinc-400">
                {plural(users, 'user')} · {plural(agents, 'agent')}
              </div>
              <div className="flex items-center gap-2">
                <Dot health={h.h} />
                <span className={cx('text-sm2', h.h === 'healthy' ? 'text-zinc-300' : h.h === 'offline' ? 'text-zinc-500' : 'text-amber-400')}>{h.t}</span>
              </div>
            </Row>
          )
        })}
      </ListBody>
    </Table>
  )
}

export function WorkspacesList() {
  const d = useDB()
  const [params, setParams] = useSearchParams()
  const [creating, setCreating] = useState(params.get('new') === '1')
  useEffect(() => {
    if (params.get('new')) setParams({}, { replace: true })
  }, []) // eslint-disable-line
  return (
    <div>
      <PageTitle actions={isAdmin(d) && <Button variant="primary" onClick={() => setCreating(true)}>New workspace</Button>}>Workspaces</PageTitle>
      <div className="mt-5">
        <WorkspacesTable />
      </div>
      <NewWorkspaceWizard open={creating} onClose={() => setCreating(false)} />
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Key picker — searchable checklist grouped by store (names only)    */
/* ------------------------------------------------------------------ */
export function KeyPicker({ selected, onChange }: { selected: string[]; onChange: (ids: string[]) => void }) {
  const d = useDB()
  const [q, setQ] = useState('')
  const stores = orgStores(d)
  const keys = orgKeys(d).filter((k) => !k.cabinetId && !k.sourceRemoved && k.name.toLowerCase().includes(q.toLowerCase()))
  const usedBy = (k: Key) => d.workspaces.flatMap((w) => w.tools).filter((t) => Object.values(t.slotMap).includes(k.id)).length
  return (
    <div className="flex flex-col gap-4">
      <Input value={q} onChange={(e) => setQ(e.target.value)} placeholder="Search keys…" aria-label="Search keys" />
      <div className="flex max-h-72 flex-col gap-2 overflow-y-auto">
        {stores.map((s) => {
          const ks = keys.filter((k) => k.storeId === s.id)
          if (!ks.length) return null
          return (
            <div key={s.id} className="flex flex-col gap-2">
              <div className="eyebrow-sm mt-1">
                {s.name}
                {s.type === 'openbao' && ' · OpenBao'}
              </div>
              {ks.map((k) => {
                const u = usedBy(k)
                return (
                  <Checkbox
                    key={k.id}
                    checked={selected.includes(k.id)}
                    onChange={(v) => onChange(v ? [...selected, k.id] : selected.filter((x) => x !== k.id))}
                    label={
                      <span>
                        <span className="font-mono text-sm2">{k.name}</span>
                        {u > 0 && <span className="text-xs2 text-zinc-500"> · used by {plural(u, 'tool')}</span>}
                      </span>
                    }
                  />
                )
              })}
            </div>
          )
        })}
        {!keys.length && <div className="text-xs text-zinc-500">{q ? 'No keys match.' : 'No keys yet — add one to the Local store first.'}</div>}
      </div>
    </div>
  )
}

function PlayerPicker({ users, agents, onUsers, onAgents }: { users: string[]; agents: string[]; onUsers: (v: string[]) => void; onAgents: (v: string[]) => void }) {
  const d = useDB()
  const people = d.users.filter((u) => u.roles[d.currentOrgId])
  const bots = orgAgents(d).filter((a) => a.status !== 'revoked')
  return (
    <div className="grid grid-cols-2 gap-4">
      <div className="flex flex-col gap-2">
        <div className="eyebrow-sm">People</div>
        {people.map((u) =>
          // Org admins administer every workspace by default; they can't be unticked here.
          isAdminRole(u.roles[d.currentOrgId]) ? (
            <Checkbox key={u.id} checked disabled label={<span>{u.name} <span className="text-xs2 text-zinc-500">· {u.roles[d.currentOrgId]}, admin by default</span></span>} />
          ) : (
            <Checkbox key={u.id} checked={users.includes(u.id)} onChange={(v) => onUsers(v ? [...users, u.id] : users.filter((x) => x !== u.id))} label={u.name} />
          ),
        )}
      </div>
      <div className="flex flex-col gap-2">
        <div className="eyebrow-sm">Agents</div>
        {bots.length ? (
          bots.map((a) => <Checkbox key={a.id} checked={agents.includes(a.id)} onChange={(v) => onAgents(v ? [...agents, a.id] : agents.filter((x) => x !== a.id))} label={<span className="font-mono text-sm2">{a.label}</span>} />)
        ) : (
          <span className="text-xs text-zinc-500">No agents yet.</span>
        )}
      </div>
    </div>
  )
}

function NewWorkspaceWizard({ open, onClose }: { open: boolean; onClose: () => void }) {
  const d = useDB()
  const nav = useNavigate()
  const [step, setStep] = useState(0)
  const [name, setName] = useState('')
  const [keys, setKeys] = useState<string[]>([])
  const [users, setUsers] = useState<string[]>([])
  const [agents, setAgents] = useState<string[]>([])
  useEffect(() => {
    if (open) {
      setStep(0)
      setName(orgWorkspaces(d).length ? '' : 'Production')
      setKeys(orgKeys(d).filter((k) => !k.cabinetId && !k.sourceRemoved && k.storeId === 'st_local').map((k) => k.id))
      setUsers([d.currentUserId])
      setAgents([])
    }
  }, [open]) // eslint-disable-line
  const clash = orgWorkspaces(d).some((w) => w.name.toLowerCase() === name.trim().toLowerCase())
  const title = [<>New workspace</>, <>Which keys does “{name}” expose?</>, <>Who gets access to “{name}”?</>][step]
  return (
    <Modal open={open} onClose={onClose} width={560} title={title}>
      <div className="-mt-2 text-xs text-zinc-500">Step {step + 1} of 3</div>
      {step === 0 && (
        <Field label="Name" hint="One workspace per project or environment works well. Both connection methods start off." error={clash ? 'A workspace with that name already exists.' : null}>
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Production" autoFocus />
        </Field>
      )}
      {step === 1 && (
        <>
          <div className="-mt-2 text-sm2 text-zinc-400">Names only are shown here — never values.</div>
          <KeyPicker selected={keys} onChange={setKeys} />
        </>
      )}
      {step === 2 && <PlayerPicker users={users} agents={agents} onUsers={setUsers} onAgents={setAgents} />}
      <Footer>
        <Button size="lg" onClick={() => (step === 0 ? onClose() : setStep(step - 1))}>
          {step === 0 ? 'Cancel' : 'Back'}
        </Button>
        {step < 2 ? (
          <Button size="lg" variant="primary" disabled={step === 0 && (!name.trim() || clash)} onClick={() => setStep(step + 1)}>
            Continue
          </Button>
        ) : (
          <Button
            size="lg"
            variant="primary"
            onClick={() => {
              const id = actions.createWorkspace({ name: name.trim(), keyIds: keys, userIds: users, agentIds: agents })
              onClose()
              nav(`/workspaces/${id}/tools`)
            }}
          >
            Create workspace
          </Button>
        )}
      </Footer>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* Detail                                                              */
/* ------------------------------------------------------------------ */
export function WorkspaceDetail() {
  const d = useDB()
  const nav = useNavigate()
  const { wsId } = useParams()
  const w = wsById(d, wsId!)
  const [deleting, setDeleting] = useState(false)
  if (!w) return <div className="text-sm text-zinc-400">This workspace doesn’t exist anymore. <Link to="/workspaces">Back to workspaces</Link></div>
  if (!canSeeWorkspace(d, w.id)) return <div className="text-sm text-zinc-400">You don’t have access to this workspace. Ask an admin to add you. <Link to="/workspaces">Back to your workspaces</Link></div>
  const base = `/workspaces/${w.id}`
  const liveAgents = w.agentIds.filter((id) => agentById(d, id) && agentById(d, id)!.status !== 'revoked').length
  const conns = d.connectors.filter((c) => c.workspaceId === w.id)
  const routed = d.stores.filter((s) => conns.some((c) => c.id === s.route))
  return (
    <div>
      <Breadcrumb items={[{ label: 'Workspaces', to: '/workspaces' }, { label: w.name }]} />
      <PageTitle
        sub={
          <>
            <ConnPill label="HTTPS" on={w.https} />
            <ConnPill label="MCP" on={w.mcp} />
          </>
        }
        actions={isAdmin(d) && <Button variant="ghost" size="sm" className="text-zinc-500 hover:text-red-400" onClick={() => setDeleting(true)}>Delete workspace</Button>}
      >
        {w.name}
      </PageTitle>
      <Tabs
        tabs={[
          { to: `${base}/summary`, label: 'Summary' },
          { to: `${base}/tools`, label: 'Tools' },
          { to: `${base}/cabinets`, label: 'Cabinets' },
          { to: `${base}/connect`, label: 'Connect' },
          { to: `${base}/audit`, label: 'Audit' },
        ]}
      />
      <Outlet context={w} />
      <ImpactDialog
        open={deleting}
        onClose={() => setDeleting(false)}
        title={`Delete ${w.name}?`}
        typeToConfirm={w.name}
        rows={[
          ['Players', `${plural(w.userIds.length, 'user')} · ${plural(liveAgents, 'agent')} lose access`, 'amber'],
          ['Vault connectors enrolled here', conns.filter((c) => c.kind === 'vault').map((c) => c.name).join(', ') || 'None', conns.some((c) => c.kind === 'vault') ? 'amber' : undefined],
          ['Sidecars enrolled here', conns.filter((c) => c.kind === 'sidecar').map((c) => c.name).join(', ') || 'None', conns.some((c) => c.kind === 'sidecar') ? 'amber' : undefined],
          ['Stores that route through them', routed.map((s) => `${s.name} → becomes unreachable`).join('; ') || 'None', routed.length ? 'amber' : undefined],
          ['Tools granted', String(w.tools.length)],
          ['Cabinets', String(d.cabinets.filter((c) => c.workspaceId === w.id).length)],
          ['Connections', [w.https && 'HTTPS', w.mcp && 'MCP'].filter(Boolean).join(' + ') || 'Off'],
          ['Last request', (() => {
            const e = orgEvents(d).find((x) => x.workspaceId === w.id && x.type === 'request')
            return e ? new Date(e.at).toLocaleString() : 'Never'
          })()],
        ]}
        body={
          conns.length
            ? `Its address stops answering immediately. ${conns.map((c) => c.name).join(', ')} ${conns.length === 1 ? 'is' : 'are'} revoked with it${routed.length ? `, so ${routed.map((s) => s.name).join(', ')} can’t be reached until you pick another route for ${routed.length === 1 ? 'it' : 'them'}` : ''}. Keys stay in their stores.`
            : 'Its address stops answering immediately. Keys stay in their stores.'
        }
        confirmLabel="Delete workspace"
        onConfirm={() => {
          actions.deleteWorkspace(w.id)
          nav('/workspaces')
        }}
      />
    </div>
  )
}

export function useWorkspace() {
  const d = useDB()
  const { wsId } = useParams()
  return wsById(d, wsId!)!
}

export function WsSummary() {
  const d = useDB()
  const w = useWorkspace()
  const rows = useUserRows(w.id)
  const [editKeys, setEditKeys] = useState(false)
  const [keys, setKeys] = useState<string[]>([])
  const [confirmKeys, setConfirmKeys] = useState(false)
  const [editPlayers, setEditPlayers] = useState(false)
  const [pu, setPu] = useState<string[]>([])
  const [pa, setPa] = useState<string[]>([])
  const admin = isAdmin(d)
  // Keyhole has no per-workspace admin role: the org's Owners and userAdmins are every workspace's admins.
  const wsAdmins = orgAdmins(d)
  // Manage access: who would lose access (org admins keep it regardless).
  const losingIds = w.userIds.filter((id) => !pu.includes(id) && !isAdminRole(userById(d, id)?.roles[d.currentOrgId]))
  const losingPeople = losingIds.map((id) => userById(d, id)?.name ?? id)
  const losingAgents = w.agentIds.filter((id) => !pa.includes(id) && agentById(d, id)?.status !== 'revoked').map((id) => agentById(d, id)?.label ?? id)
  const losingCabinets = d.cabinets.filter((c) => c.workspaceId === w.id && !!c.managedBy && losingIds.includes(c.managedBy)).map((c) => c.name)
  const agents = w.agentIds.map((id) => agentById(d, id)).filter(Boolean) as NonNullable<ReturnType<typeof agentById>>[]
  const impact = unexposeImpact(d, w.id, keys)
  const saveKeys = () => {
    actions.setWorkspaceKeys(w.id, keys)
    setEditKeys(false)
  }
  return (
    <div className="mt-5 flex max-w-[1160px] flex-col gap-7">
      <div>
        <div className="flex items-center justify-between">
          <div className="eyebrow">Keys this workspace exposes</div>
          {admin && (
            <button
              className="text-sm2 text-brass hover:text-brass-light"
              onClick={() => {
                setKeys(w.keyIds)
                setEditKeys(true)
              }}
            >
              Choose keys
            </button>
          )}
        </div>
        <div className="mt-2.5 flex flex-wrap gap-2">
          {w.keyIds.length ? (
            w.keyIds.map((id) => {
              const k = keyById(d, id)
              if (!k) return null
              return (
                <span key={id} className={cx('inline-flex items-center gap-2 rounded-md border border-edge bg-panel px-2.5 py-1 font-mono text-xs', k.sourceRemoved ? 'text-zinc-500 line-through' : 'text-zinc-300')}>
                  {k.name}
                  <span className="font-sans text-2xs text-zinc-500 no-underline">{k.sourceRemoved ? 'source removed' : storeById(d, k.storeId)?.name}</span>
                </span>
              )
            })
          ) : (
            <span className="text-sm2 text-zinc-500">No keys exposed yet.</span>
          )}
        </div>
        <div className="mt-1.5 text-xs2 text-zinc-600">Names only — values are never shown.</div>
      </div>
      <div>
        <div className="mb-1 flex items-center justify-between">
          <div className="eyebrow">Users</div>
          {admin && (
            <button
              className="text-sm2 text-brass hover:text-brass-light"
              onClick={() => {
                setPu(w.userIds)
                setPa(w.agentIds)
                setEditPlayers(true)
              }}
            >
              Manage access
            </button>
          )}
        </div>
        <div className="mb-2.5 text-xs text-zinc-500">
          Workspace admins:{' '}
          <span className="text-zinc-300">{wsAdmins.map((u) => `${u.name} (${u.roles[d.currentOrgId]})`).join(', ')}</span> — org admins, by default.
        </div>
        <UsersTable rows={rows} />
      </div>
      <div>
        <div className="eyebrow mb-2.5">Agents</div>
        <AgentsTable agents={agents} emptyText={<>No agents on this workspace yet. <Link to="/players/agents?new=1">Create one on the Agents page.</Link></>} />
      </div>

      <Modal open={editKeys} onClose={() => setEditKeys(false)} width={560} title={`Which keys does “${w.name}” expose?`}>
        <div className="-mt-2 text-sm2 text-zinc-400">Names only are shown here — never values.</div>
        <KeyPicker selected={keys} onChange={setKeys} />
        <Footer>
          <Button size="lg" onClick={() => setEditKeys(false)}>
            Cancel
          </Button>
          <Button size="lg" variant="primary" onClick={() => (impact.slots.length || impact.cabinets.length ? setConfirmKeys(true) : saveKeys())}>
            Save keys
          </Button>
        </Footer>
      </Modal>
      <ImpactDialog
        open={confirmKeys}
        onClose={() => setConfirmKeys(false)}
        title={`Stop exposing ${impact.removed.map((id) => keyById(d, id)?.name).join(', ')} in ${w.name}?`}
        rows={[
          ['Tool slots that lose their key', impact.slots.map((x) => `${x.tool} · ${x.slot}`).join('; ') || 'None', impact.slots.length ? 'amber' : undefined],
          ['Cabinets that lose a key', impact.cabinets.map((c) => `${c.name} · ${c.keys.join(', ')}`).join('; ') || 'None', impact.cabinets.length ? 'amber' : undefined],
          ['Agents on this workspace', plural(agents.filter((a) => a.status === 'active').length, 'active agent')],
        ]}
        body="Those slots show Missing, and calls through them fail until another exposed key fills them. The keys themselves stay in their stores."
        confirmLabel="Stop exposing"
        onConfirm={saveKeys}
      />
      <Modal open={editPlayers} onClose={() => setEditPlayers(false)} width={560} title={`Who gets access to “${w.name}”?`}>
        <PlayerPicker users={pu} agents={pa} onUsers={setPu} onAgents={setPa} />
        {(losingPeople.length > 0 || losingAgents.length > 0) && (
          <ImpactRows
            rows={[
              ['People losing access', losingPeople.join(', ') || 'None', losingPeople.length ? 'amber' : undefined],
              ['Agents losing access', losingAgents.join(', ') || 'None', losingAgents.length ? 'amber' : undefined],
              ['Cabinets they manage here', losingCabinets.length ? `${losingCabinets.join(', ')} — keep working; admins take over management` : 'None'],
            ]}
          />
        )}
        <Footer>
          <Button size="lg" onClick={() => setEditPlayers(false)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant={losingPeople.length || losingAgents.length ? 'danger' : 'primary'}
            onClick={() => {
              actions.setWorkspacePlayers(w.id, pu, pa)
              setEditPlayers(false)
            }}
          >
            Save access
          </Button>
        </Footer>
      </Modal>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Tools tab + slot matching                                           */
/* ------------------------------------------------------------------ */
const WT_COLS = '1.4fr 2fr 70px 130px 110px 36px'

export function WsTools() {
  const d = useDB()
  const w = useWorkspace()
  const admin = isAdmin(d)
  const [adding, setAdding] = useState(false)
  const [results, setResults] = useState<Record<string, ReturnType<typeof testCall>>>({})
  const [removing, setRemoving] = useState<string | null>(null)
  const [turningOff, setTurningOff] = useState<string | null>(null)
  /** A slot change that takes a key away (remap or clear), waiting for its preview. */
  const [slotChange, setSlotChange] = useState<{ toolId: string; slot: string; from: string; to: string | null } | null>(null)
  /** A lower rate limit, waiting for its preview. Raising it applies straight away. */
  const [limitChange, setLimitChange] = useState<{ toolId: string; from: number; to: number } | null>(null)
  const available = orgTools(d).filter((t) => !w.tools.some((x) => x.toolId === t.id))
  const activeAgents = w.agentIds.filter((id) => agentById(d, id)?.status === 'active').length
  const lastCalled = (toolId: string | null) => {
    const e = orgEvents(d).find((x) => x.workspaceId === w.id && x.type === 'request' && x.object === toolById(d, toolId ?? '')?.displayName)
    return e ? new Date(e.at).toLocaleString() : 'Never'
  }
  return (
    <div className="mt-5 max-w-[1060px]">
      {admin && w.tools.length > 0 && (
        <div className="mb-3 flex justify-end">
          <Button variant="primary" onClick={() => setAdding(true)} disabled={!available.length}>
            Add tool
          </Button>
        </div>
      )}
      {w.tools.length === 0 ? (
        <div className="rounded-[10px] border border-edge bg-panel p-10 text-center">
          <div className="text-[13px] text-zinc-400">No tools granted yet. Add one from the organization.</div>
          {admin && (
            <div className="mt-4 flex justify-center gap-2">
              {orgTools(d).length ? (
                <Button variant="primary" onClick={() => setAdding(true)}>
                  Add tool
                </Button>
              ) : (
                <Link to="/tools">
                  <Button variant="primary">Create a tool first</Button>
                </Link>
              )}
            </div>
          )}
        </div>
      ) : (
        <Table cols={WT_COLS} head={['Tool', 'Key slots', 'On', 'Limit', '', '']}>
          {w.tools.map((wt) => {
            const t = toolById(d, wt.toolId)
            if (!t) return null
            // Agents get the published version; a newer draft doesn't change anything here until it's published.
            const live = liveTool(t)
            const slots = (live ?? t).slots
            const missing = missingSlots(d, w, wt)
            const r = results[wt.toolId]
            return (
              <div key={wt.toolId} className="border-b border-line">
                <Row cols={WT_COLS} className="border-b-0">
                  <div>
                    <Link to={`/tools/${t.id}`} className="font-medium text-zinc-100 hover:text-white">
                      {t.displayName}
                    </Link>
                    <div className="text-xs text-zinc-500">
                      {live ? `v${live.version}` : <span className="text-amber-400">Not published</span>}
                      {live && t.status === 'draft' && <span> · draft v{t.version} pending</span>}
                    </div>
                  </div>
                  <div className="flex flex-col gap-1">
                    {slots.map((s) => {
                      const k = keyById(d, filledSlot(d, w, wt, s.name))
                      return (
                        <div key={s.id} className="flex items-center gap-2 text-xs">
                          <span className="font-mono text-brass-light">{s.name}</span>
                          <span className="text-zinc-600">→</span>
                          {admin ? (
                            <select
                              aria-label={`Key for ${s.name}`}
                              value={k?.id ?? ''}
                              onChange={(e) => {
                                const to = e.target.value || null
                                // Filling an empty slot only adds access; replacing or clearing a key is previewed.
                                if (k) setSlotChange({ toolId: t.id, slot: s.name, from: k.id, to })
                                else actions.updateWorkspaceTool(w.id, t.id, { slotMap: { ...wt.slotMap, [s.name]: to } })
                              }}
                              className={cx('rounded-md border bg-page px-2 py-0.5 font-mono text-xs outline-none', k ? 'border-edge text-zinc-300' : 'border-amber-500/40 text-amber-400')}
                            >
                              <option value="">{k ? 'Clear this slot' : 'Missing — pick a key'}</option>
                              {slotCandidates(d, w.keyIds, s.name).map((c) => (
                                <option key={c.id} value={c.id}>
                                  {c.name}
                                </option>
                              ))}
                            </select>
                          ) : (
                            <span className={cx('font-mono', k ? 'text-zinc-300' : 'text-amber-400')}>{k?.name ?? 'Missing'}</span>
                          )}
                        </div>
                      )
                    })}
                    {!slots.length && <span className="text-xs text-zinc-500">No key needed</span>}
                  </div>
                  <div>
                    <Toggle on={wt.enabled} onChange={(v) => (v ? actions.updateWorkspaceTool(w.id, t.id, { enabled: true }) : setTurningOff(t.id))} label={`Turn ${t.displayName} on or off`} disabled={!admin} />
                  </div>
                  <div className="flex items-center gap-1.5 text-xs text-zinc-500">
                    <LimitInput key={wt.perMinute} value={wt.perMinute} disabled={!admin} onCommit={(n) => (n < wt.perMinute ? setLimitChange({ toolId: t.id, from: wt.perMinute, to: n }) : actions.updateWorkspaceTool(w.id, t.id, { perMinute: n }))} />
                    /min
                  </div>
                  <div>
                    <Button size="sm" disabled={!wt.enabled} onClick={() => setResults({ ...results, [t.id]: testCall({ toolId: t.id, wsId: w.id, missingSlot: missing[0] }) })}>
                      Test call
                    </Button>
                  </div>
                  <div className="text-right">
                    {admin && (
                      <button className="text-zinc-600 hover:text-red-400" aria-label="Remove tool" onClick={() => setRemoving(t.id)}>
                        ✕
                      </button>
                    )}
                  </div>
                </Row>
                {missing.map((m) => (
                  <div key={m} className="mx-4 mb-3 rounded-lg border border-amber-500/30 bg-amber-500/[0.06] px-3.5 py-2 text-xs text-amber-400">
                    Missing: <span className="font-mono text-xs2">{m}</span> — this workspace exposes no key for that slot, so calls fail.{' '}
                    {admin ? (
                      <>
                        Pick one above, or <Link to={`/workspaces/${w.id}/summary`}>expose a key</Link>.
                      </>
                    ) : (
                      'Ask an admin to fill it.'
                    )}
                  </div>
                ))}
                {r && (
                  <div className={cx('mx-4 mb-3 flex items-center gap-3 rounded-lg border px-3.5 py-2 text-xs', r.ok ? 'border-green-500/30 bg-green-500/[0.05]' : 'border-amber-500/30 bg-amber-500/[0.05]')}>
                    {r.ok ? (
                      <>
                        <span className="font-semibold text-green-400">200 OK</span>
                        <span className="text-zinc-400">{r.ms} ms</span>
                      </>
                    ) : (
                      <span className="text-amber-400">{r.message}</span>
                    )}
                    <CopyChip value={r.trk} />
                    <Link to={`/workspaces/${w.id}/audit`} className="ml-auto">
                      See it in the log
                    </Link>
                  </div>
                )}
              </div>
            )
          })}
        </Table>
      )}
      <AddToolModal open={adding} onClose={() => setAdding(false)} ws={w} />
      <ImpactDialog
        open={!!removing}
        onClose={() => setRemoving(null)}
        title={`Remove ${removing ? toolById(d, removing)?.displayName : ''} from ${w.name}?`}
        rows={[
          ['Agents that can call it', plural(activeAgents, 'agent')],
          ['Last called', lastCalled(removing)],
        ]}
        body="Agents stop seeing this tool on their next request. The tool itself stays in the organization."
        confirmLabel="Remove tool"
        onConfirm={() => removing && actions.removeWorkspaceTool(w.id, removing)}
      />
      <ImpactDialog
        open={!!turningOff}
        onClose={() => setTurningOff(null)}
        title={`Turn ${turningOff ? toolById(d, turningOff)?.displayName : ''} off in ${w.name}?`}
        rows={[
          ['Agents that can call it', plural(activeAgents, 'agent'), activeAgents ? 'amber' : undefined],
          ['Last called', lastCalled(turningOff)],
        ]}
        body="Agents stop seeing this tool on their next request. Its key slots stay filled, so turning it back on is instant."
        confirmLabel="Turn off"
        onConfirm={() => turningOff && actions.updateWorkspaceTool(w.id, turningOff, { enabled: false })}
      />
      <ImpactDialog
        open={!!slotChange}
        onClose={() => setSlotChange(null)}
        title={slotChange?.to ? `Use a different key for ${slotChange.slot}?` : `Clear ${slotChange?.slot} in ${w.name}?`}
        rows={[
          ['Tool', slotChange ? (toolById(d, slotChange.toolId)?.displayName ?? '') : ''],
          [slotChange?.slot ?? 'Slot', slotChange ? `${keyById(d, slotChange.from)?.name} → ${slotChange.to ? keyById(d, slotChange.to)?.name : 'Missing'}` : '', 'amber'],
          ['Agents that can call it', plural(activeAgents, 'agent')],
          ['Last called', lastCalled(slotChange?.toolId ?? null)],
        ]}
        body={slotChange?.to ? 'The next call through this slot uses the new key.' : 'Calls through this tool fail until a key fills the slot again. The key itself stays exposed and in its store.'}
        confirmLabel={slotChange?.to ? 'Use this key' : 'Clear slot'}
        onConfirm={() => {
          const wt = slotChange && w.tools.find((x) => x.toolId === slotChange.toolId)
          if (slotChange && wt) actions.updateWorkspaceTool(w.id, slotChange.toolId, { slotMap: { ...wt.slotMap, [slotChange.slot]: slotChange.to } })
        }}
      />
      <ImpactDialog
        open={!!limitChange}
        onClose={() => setLimitChange(null)}
        title={`Lower the ${limitChange ? toolById(d, limitChange.toolId)?.displayName : ''} limit in ${w.name}?`}
        rows={[
          ['Calls per minute', limitChange ? `${limitChange.from} → ${limitChange.to}` : '', 'amber'],
          ['Agents that can call it', plural(activeAgents, 'agent')],
          ['Last called', lastCalled(limitChange?.toolId ?? null)],
        ]}
        body="Calls over the new limit are refused from the next minute, and each refusal is logged."
        confirmLabel="Lower limit"
        onConfirm={() => limitChange && actions.updateWorkspaceTool(w.id, limitChange.toolId, { perMinute: limitChange.to })}
      />
    </div>
  )
}

/** Per-workspace rate limit. Saves (and logs) once, on blur or Enter — not on every keystroke. */
function LimitInput({ value, disabled, onCommit }: { value: number; disabled: boolean; onCommit: (n: number) => void }) {
  const [v, setV] = useState(String(value))
  const commit = () => {
    const n = Math.max(1, Math.round(Number(v)) || 1)
    // Show the saved value until the change lands; a lowered limit waits for its preview.
    setV(String(value))
    if (n !== value) onCommit(n)
  }
  return (
    <input
      type="number"
      min={1}
      aria-label="Calls per minute"
      value={v}
      disabled={disabled}
      onChange={(e) => setV(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => e.key === 'Enter' && e.currentTarget.blur()}
      className="w-14 rounded-md border border-edge bg-page px-2 py-1 text-[13px] text-zinc-200 outline-none focus:border-zinc-500"
    />
  )
}

export function AddToolModal({ open, onClose, ws }: { open: boolean; onClose: () => void; ws: Workspace }) {
  const d = useDB()
  const available = orgTools(d).filter((t) => !ws.tools.some((x) => x.toolId === t.id))
  // Only published tools can be granted: a draft isn't usable by agents yet.
  const publishable = available.filter((t) => t.published)
  const drafts = available.filter((t) => !t.published)
  const [toolId, setToolId] = useState('')
  const picked = toolById(d, toolId)
  const tool = liveTool(picked)
  const [map, setMap] = useState<Record<string, string | null>>({})
  useEffect(() => {
    if (open) setToolId(publishable.length === 1 ? publishable[0].id : '')
  }, [open]) // eslint-disable-line
  useEffect(() => {
    if (tool) setMap(Object.fromEntries(tool.slots.map((s) => [s.name, autoMatch(d, ws.keyIds, s.name)])))
  }, [toolId]) // eslint-disable-line
  const matched = useMemo(() => new Set(tool?.slots.filter((s) => map[s.name] && keyById(d, map[s.name])?.name === s.name).map((s) => s.name)), [tool, map, d])
  return (
    <Modal open={open} onClose={onClose} width={520} title={`Add tool to ${ws.name}`}>
      {publishable.length !== 1 || !tool ? (
        <Field label="Tool">
          <Select value={toolId} onChange={(e) => setToolId(e.target.value)}>
            <option value="">Pick a tool from the organization…</option>
            {available.map((t) => (
              <option key={t.id} value={t.id} disabled={!t.published}>
                {t.displayName} · {t.published ? `v${t.published.version}` : 'draft, publish it first'}
              </option>
            ))}
          </Select>
        </Field>
      ) : (
        <div className="-mt-3 text-[13px] text-zinc-400">
          {tool.displayName} · v{tool.version}
        </div>
      )}
      {drafts.length > 0 && !tool && (
        <div className="-mt-2 text-xs text-zinc-500">
          Drafts can’t be added until they’re published:{' '}
          {drafts.map((t, i) => (
            <span key={t.id}>
              {i > 0 && ', '}
              <Link to={`/tools/${t.id}?section=publish`}>{t.displayName}</Link>
            </span>
          ))}
          .
        </div>
      )}
      {tool && picked && (
        <>
          <div>
            <div className="eyebrow mt-1.5">Fill key slots</div>
            <div className="mt-1 text-xs text-zinc-500">This tool asks for keys by name. Pick which stored key fills each slot in this workspace.</div>
          </div>
          {tool.slots.map((s) => {
            const cands = slotCandidates(d, ws.keyIds, s.name)
            return (
              <div key={s.id}>
                <div className="flex items-center gap-3.5 rounded-[10px] border border-edge bg-rail px-4 py-3.5">
                  <span className="font-mono text-[13px] text-brass-light">{s.name}</span>
                  <span className="text-zinc-600">→</span>
                  <div className="flex-1">
                    <Select mono aria-label={`Key for ${s.name}`} value={map[s.name] ?? ''} onChange={(e) => setMap({ ...map, [s.name]: e.target.value || null })} className="bg-panel py-2">
                      <option value="">{cands.length ? 'Pick a key…' : 'No keys exposed here'}</option>
                      {cands.map((k) => (
                        <option key={k.id} value={k.id}>
                          {k.name} · {storeById(d, k.storeId)?.name}
                        </option>
                      ))}
                    </Select>
                  </div>
                </div>
                <div className="mt-2 flex items-center gap-1.5">
                  {map[s.name] ? (
                    <>
                      <Dot health="healthy" size={6} />
                      <span className="text-xs2 text-zinc-500">{matched.has(s.name) ? 'Matched by name' : 'Chosen by you'}</span>
                    </>
                  ) : (
                    <span className="text-xs2 text-amber-400">
                      Missing: <span className="font-mono">{s.name}</span> — this workspace exposes no matching key. <Link to={`/workspaces/${ws.id}/summary`}>Expose a key</Link>
                    </span>
                  )}
                </div>
              </div>
            )
          })}
          {!tool.slots.length && <div className="text-xs text-zinc-500">This tool doesn’t need a key.</div>}
        </>
      )}
      <Footer className="mt-2">
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant="primary"
          disabled={!tool || tool.slots.some((s) => !map[s.name])}
          onClick={() => {
            actions.grantTool(ws.id, picked!.id, map)
            onClose()
          }}
        >
          Add tool
        </Button>
      </Footer>
    </Modal>
  )
}

export function WsAudit() {
  const d = useDB()
  const w = useWorkspace()
  const [params] = useSearchParams()
  return (
    <div className="mt-5 max-w-[1080px]">
      <AuditLog events={visibleEvents(d).filter((e) => e.workspaceId === w.id)} scopeLabel={w.name} hideWorkspaceFilter initialExpand={params.get('event') ?? undefined} />
    </div>
  )
}

export { CabinetsTab as WsCabinets, ConnectTab as WsConnect }
