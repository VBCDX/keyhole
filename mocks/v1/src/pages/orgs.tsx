import { useEffect, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom'
import { ago, plural, until } from '../lib/format'
import {
  actions,
  autoMatch,
  isAdmin,
  keyById,
  me,
  myOrgs,
  org,
  orgAgents,
  orgKeys,
  orgStores,
  orgTools,
  orgWorkspaces,
  storeById,
  toolWorkspaces,
  useDB,
  useNow,
  visibleEvents,
} from '../lib/store'
import type { Role, Tool } from '../lib/types'
import { AgentsTable, AuditLog, ImpactDialog, ListBody, UsersTable, useUserRows } from '../components/shared'
import { Breadcrumb, Button, Card, Checkbox, Dot, Field, Footer, Input, Modal, PageTitle, Pill, Row, Segmented, Select, Table, Tabs } from '../components/ui'
import { WorkspacesTable } from './workspaces'

/* ------------------------------------------------------------------ */
/* List                                                                */
/* ------------------------------------------------------------------ */
const ORG_COLS = '1.6fr 1fr 1fr 1fr 1fr'
export function OrgsList() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const orgs = myOrgs(d)
  return (
    <div>
      <PageTitle>Organizations</PageTitle>
      <Table cols={ORG_COLS} head={['Name', 'Your role', 'Members', 'Workspaces', 'Created']} className="mt-5 max-w-[1000px]">
        <ListBody cols={ORG_COLS} what="organizations">
          {orgs.map((o) => (
            <Row
              key={o.id}
              cols={ORG_COLS}
              onClick={() => {
                actions.setOrg(o.id)
                nav(`/orgs/${o.id}/overview`)
              }}
            >
              <div className="font-medium">{o.name}</div>
              <div className="text-zinc-400">{me(d).roles[o.id]}</div>
              <div className="text-zinc-400">{d.users.filter((u) => u.roles[o.id]).length}</div>
              <div className="text-zinc-400">{d.workspaces.filter((w) => w.orgId === o.id).length}</div>
              <div className="text-zinc-500">{ago(o.createdAt, now)}</div>
            </Row>
          ))}
        </ListBody>
      </Table>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Detail shell with tabs                                              */
/* ------------------------------------------------------------------ */
export function OrgDetail() {
  const d = useDB()
  const nav = useNavigate()
  const { orgId } = useParams()
  const { pathname } = useLocation()
  useEffect(() => {
    if (orgId && orgId !== d.currentOrgId && me(d).roles[orgId]) actions.setOrg(orgId)
  }, [orgId]) // eslint-disable-line
  const o = org(d, orgId)
  if (!o) return <div className="text-sm text-zinc-400">You don’t have access to this organization.</div>
  const base = `/orgs/${o.id}`
  if (pathname.includes('connect-openbao')) return <Outlet />
  const onStoresList = pathname.endsWith('/stores')
  return (
    <div>
      <Breadcrumb items={[{ label: 'Organizations', to: '/orgs' }, { label: o.name }]} />
      <PageTitle actions={onStoresList && isAdmin(d) ? <Button variant="primary" onClick={() => nav(`${base}/stores?add=store`)}>Add store</Button> : undefined}>{o.name}</PageTitle>
      <Tabs
        tabs={[
          { to: `${base}/overview`, label: 'Overview' },
          { to: `${base}/stores`, label: 'Stores' },
          { to: `${base}/members`, label: 'Members' },
          { to: `${base}/agents`, label: 'Agents' },
          { to: `${base}/tools`, label: 'Tools' },
          { to: `${base}/workspaces`, label: 'Workspaces' },
          { to: `${base}/audit`, label: 'Audit' },
        ]}
      />
      <Outlet />
    </div>
  )
}

export function OrgOverview() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const o = org(d)!
  const owner = d.users.find((u) => u.roles[o.id] === 'Owner')
  const role = me(d).roles[o.id]
  const [renaming, setRenaming] = useState(false)
  const [name, setName] = useState(o.name)
  const [deleting, setDeleting] = useState(false)
  const stores = orgStores(d)
  const bad = stores.filter((s) => s.health !== 'healthy')
  const counts: [string, number, string][] = [
    ['Workspaces', orgWorkspaces(d).length, 'workspaces'],
    ['Members', d.users.filter((u) => u.roles[o.id]).length, 'members'],
    ['Agents', orgAgents(d).filter((a) => a.status !== 'revoked').length, 'agents'],
    ['Tools', orgTools(d).length, 'tools'],
    ['Keys', orgKeys(d).filter((k) => !k.cabinetId).length, 'stores'],
  ]
  return (
    <div className="mt-5 max-w-[960px]">
      <div className="grid grid-cols-5 gap-4">
        {counts.map(([l, n, tab]) => (
          <Link key={l} to={`/orgs/${o.id}/${tab}`} className="rounded-[10px] border border-edge bg-panel p-4 hover:border-zinc-700">
            <div className="text-xs text-zinc-500">{l}</div>
            <div className="mt-1.5 text-[22px] font-semibold text-zinc-100">{n}</div>
          </Link>
        ))}
      </div>
      <Card className="mt-4 grid grid-cols-[160px_1fr] gap-x-3 gap-y-2.5 p-5 text-[13px]">
        <span className="text-zinc-500">Health</span>
        <span className="flex items-center gap-2">
          <Dot health={bad.length ? 'error' : 'healthy'} />
          {bad.length ? `${plural(bad.length, 'store')} need attention` : `All ${plural(stores.length, 'store')} healthy`}
        </span>
        <span className="text-zinc-500">Owner</span>
        <span>{owner?.name}</span>
        <span className="text-zinc-500">Created</span>
        <span>{ago(o.createdAt, now)}</span>
        <span className="text-zinc-500">Your role</span>
        <span>{role}</span>
      </Card>
      {role === 'Owner' && (
        <div className="mt-4 flex gap-2">
          <Button onClick={() => setRenaming(true)}>Rename organization</Button>
          <Button variant="danger" onClick={() => setDeleting(true)}>
            Delete organization
          </Button>
        </div>
      )}
      <Modal open={renaming} onClose={() => setRenaming(false)} title="Rename organization" width={420}>
        <Field label="Name">
          <Input value={name} onChange={(e) => setName(e.target.value)} autoFocus />
        </Field>
        <Footer>
          <Button size="lg" onClick={() => setRenaming(false)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            disabled={!name.trim() || name === o.name}
            onClick={() => {
              actions.renameOrg(name)
              setRenaming(false)
            }}
          >
            Rename
          </Button>
        </Footer>
      </Modal>
      <DeleteOrgDialog open={deleting} onClose={() => setDeleting(false)} onDeleted={() => nav('/')} />
    </div>
  )
}

export function DeleteOrgDialog({ open, onClose, onDeleted }: { open: boolean; onClose: () => void; onDeleted: () => void }) {
  const d = useDB()
  const o = org(d)
  if (!o) return null
  return (
    <ImpactDialog
      open={open}
      onClose={onClose}
      title={`Delete ${o.name}?`}
      typeToConfirm={o.name}
      rows={[
        ['Workspaces', String(d.workspaces.filter((w) => w.orgId === o.id).length)],
        ['Stores and keys', `${orgStores(d).length} stores · ${orgKeys(d).length} keys`],
        ['Tools', String(orgTools(d).length)],
        ['Agents', `${orgAgents(d).filter((a) => a.status === 'active').length} active — their tokens stop working`, 'amber'],
        ['Members', String(d.users.filter((u) => u.roles[o.id]).length)],
        ['Cabinets', String(d.cabinets.filter((c) => d.workspaces.some((w) => w.id === c.workspaceId && w.orgId === o.id)).length)],
      ]}
      body="Everything inside is deleted. Keys in your own OpenBao are untouched. This can't be undone."
      confirmLabel="Delete organization"
      onConfirm={() => {
        actions.deleteOrg()
        onDeleted()
      }}
    />
  )
}

/* ------------------------------------------------------------------ */
/* Members                                                             */
/* ------------------------------------------------------------------ */
export function InviteModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const d = useDB()
  const ws = orgWorkspaces(d)
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<Role>('user')
  const [sel, setSel] = useState<string[]>([])
  useEffect(() => {
    if (open) {
      setEmail('')
      setRole('user')
      setSel(ws[0] ? [ws[0].id] : [])
    }
  }, [open]) // eslint-disable-line
  const valid = /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)
  const already = d.users.some((u) => u.email === email && (u.roles[d.currentOrgId] || d.invites.some((i) => i.email === email && i.orgId === d.currentOrgId)))
  return (
    <Modal open={open} onClose={onClose} width={460} title="Invite user">
      <Field label="Email" error={already ? `${email} is already a member or has a pending invite.` : null}>
        <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder="mia@acme.com" autoFocus />
      </Field>
      <Field label="Role">
        <Segmented<Role>
          value={role}
          onChange={setRole}
          options={[
            { value: 'user', label: 'user' },
            { value: 'userAdmin', label: 'userAdmin' },
          ]}
        />
      </Field>
      <Field label="Workspaces">
        <div className="flex flex-col gap-2 rounded-lg border border-edge bg-page p-3">
          {ws.length ? ws.map((w) => <Checkbox key={w.id} checked={sel.includes(w.id)} onChange={(v) => setSel(v ? [...sel, w.id] : sel.filter((x) => x !== w.id))} label={w.name} />) : <span className="text-xs text-zinc-500">No workspaces yet — they’ll only see Home until you add them to one.</span>}
        </div>
      </Field>
      <div className="text-xs text-zinc-500">They sign in with Google. The invite expires in 7 days.</div>
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant="primary"
          disabled={!valid || already}
          onClick={() => {
            actions.invite({ email, role, workspaceIds: sel })
            onClose()
          }}
        >
          Send invite
        </Button>
      </Footer>
    </Modal>
  )
}

export function PendingInvites() {
  const d = useDB()
  const now = useNow()
  const invites = d.invites.filter((i) => i.orgId === d.currentOrgId)
  const admin = isAdmin(d)
  if (!invites.length) return null
  return (
    <>
      <div className="eyebrow mt-7">Pending invites</div>
      <div className="mt-2.5 flex max-w-[1000px] flex-col gap-2">
        {invites.map((i) => {
          const expired = i.expiresAt < now
          return (
            <div key={i.id} className="flex items-center gap-4 rounded-[10px] border border-edge bg-panel px-4 py-[13px] text-[13px]">
              <span className="text-zinc-300">{i.email}</span>
              <Pill>{i.role}</Pill>
              <span className="text-xs text-zinc-500">
                Invited {ago(i.invitedAt, now)} · {expired ? <span className="text-amber-400">expired</span> : `expires ${until(i.expiresAt, now).toLowerCase()}`}
              </span>
              {admin && (
                <span className="ml-auto flex gap-3">
                  <button className="text-sm2 text-brass hover:text-brass-light" onClick={() => actions.resendInvite(i.id)}>
                    Resend
                  </button>
                  <button className="text-sm2 text-red-400 hover:text-red-300" onClick={() => actions.revokeInvite(i.id)}>
                    Revoke
                  </button>
                </span>
              )}
            </div>
          )
        })}
      </div>
    </>
  )
}

export function OrgMembers() {
  const d = useDB()
  const rows = useUserRows()
  const [inviting, setInviting] = useState(false)
  return (
    <div className="mt-5">
      {isAdmin(d) && (
        <div className="mb-3 flex max-w-[1000px] justify-end">
          <Button variant="primary" onClick={() => setInviting(true)}>
            Invite user
          </Button>
        </div>
      )}
      <UsersTable rows={rows.filter((r) => !r.invited)} className="max-w-[1000px]" />
      <PendingInvites />
      <InviteModal open={inviting} onClose={() => setInviting(false)} />
    </div>
  )
}

export function OrgAgents() {
  const d = useDB()
  const nav = useNavigate()
  return (
    <div className="mt-5">
      {isAdmin(d) && (
        <div className="mb-3 flex max-w-[1160px] justify-end">
          <Button variant="primary" onClick={() => nav('/players/agents?new=1')}>
            New agent
          </Button>
        </div>
      )}
      <AgentsTable agents={orgAgents(d)} className="max-w-[1160px]" />
    </div>
  )
}

export function OrgWorkspaces() {
  const d = useDB()
  const nav = useNavigate()
  return (
    <div className="mt-5">
      {isAdmin(d) && (
        <div className="mb-3 flex max-w-[1020px] justify-end">
          <Button variant="primary" onClick={() => nav('/workspaces?new=1')}>
            New workspace
          </Button>
        </div>
      )}
      <WorkspacesTable />
    </div>
  )
}

export function OrgAudit() {
  const d = useDB()
  return (
    <div className="mt-5 max-w-[1080px]">
      <AuditLog events={visibleEvents(d)} scopeLabel={org(d)?.name} />
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Tools adopted by the organization + "Attach tool" (Flow 5)          */
/* ------------------------------------------------------------------ */
const TOOL_COLS = '1.6fr 1fr 1.4fr 1.8fr 110px'
export function OrgTools() {
  const d = useDB()
  const nav = useNavigate()
  const tools = orgTools(d)
  const [attach, setAttach] = useState<string | null>(null)
  const admin = isAdmin(d)
  return (
    <div className="mt-5 max-w-[1040px]">
      {admin && (
        <div className="mb-3 flex justify-end">
          <Button variant="primary" onClick={() => setAttach('')} disabled={!tools.length}>
            Attach tool
          </Button>
        </div>
      )}
      {tools.length === 0 ? (
        <div className="rounded-[10px] border border-edge bg-panel p-10 text-center text-[13px] text-zinc-400">
          No tools yet. <Link to="/tools">Import an API description or start from the catalog.</Link>
        </div>
      ) : (
        <Table cols={TOOL_COLS} head={['Name', 'Version', 'Key slots', 'Granted to', '']}>
          <ListBody cols={TOOL_COLS} what="tools">
            {tools.map((t) => (
              <Row key={t.id} cols={TOOL_COLS} onClick={() => nav(`/tools/${t.id}`)}>
                <div className="font-medium">{t.displayName}</div>
                <div className="flex items-center gap-2">
                  v{t.version} <Pill tone={t.status === 'published' ? 'green' : 'neutral'}>{t.status === 'published' ? 'Published' : 'Draft'}</Pill>
                </div>
                <div className="font-mono text-xs text-zinc-400">{t.slots.map((s) => s.name).join(', ') || '—'}</div>
                <div className="text-zinc-400">{toolWorkspaces(d, t.id).map((w) => w.name).join(', ') || <span className="text-zinc-500">Not granted</span>}</div>
                <div className="text-right" onClick={(e) => e.stopPropagation()}>
                  {admin && (
                    <Button size="sm" onClick={() => setAttach(t.id)}>
                      Grant
                    </Button>
                  )}
                </div>
              </Row>
            ))}
          </ListBody>
        </Table>
      )}
      <AttachToolModal toolId={attach} onClose={() => setAttach(null)} />
    </div>
  )
}

function AttachToolModal({ toolId, onClose }: { toolId: string | null; onClose: () => void }) {
  const d = useDB()
  const tools = orgTools(d)
  const workspaces = orgWorkspaces(d)
  const [picked, setPicked] = useState('')
  const [sel, setSel] = useState<string[]>([])
  const [fixing, setFixing] = useState<{ wsId: string; slot: string } | null>(null)
  const [fixKey, setFixKey] = useState('')
  const [done, setDone] = useState<number | null>(null)
  /** Explicit slot fills chosen via "Expose a key" when the key's name differs from the slot. */
  const [overrides, setOverrides] = useState<Record<string, string>>({})
  useEffect(() => {
    if (toolId !== null) {
      setPicked(toolId)
      setOverrides({})
      setDone(null)
      setFixing(null)
      const t = tools.find((x) => x.id === toolId)
      setSel(t ? toolWorkspaces(d, t.id).map((w) => w.id) : [])
    }
  }, [toolId]) // eslint-disable-line
  const tool: Tool | undefined = tools.find((t) => t.id === picked)
  const slotState = (wsId: string) => {
    const w = workspaces.find((x) => x.id === wsId)!
    return tool!.slots.map((s) => ({ slot: s.name, keyId: overrides[`${wsId}:${s.name}`] ?? autoMatch(d, w.keyIds, s.name) }))
  }
  const ready = sel.filter((id) => slotState(id).every((s) => s.keyId))
  const missing = sel.filter((id) => !ready.includes(id))

  return (
    <Modal open={toolId !== null} onClose={onClose} width={560} title={tool ? `Grant “${tool.displayName}” to workspaces` : 'Attach tool'}>
      {!tool ? (
        <>
          <Field label="Tool">
            <Select value={picked} onChange={(e) => setPicked(e.target.value)}>
              <option value="">Pick a tool…</option>
              {tools.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.displayName} · v{t.version}
                </option>
              ))}
            </Select>
          </Field>
        </>
      ) : done !== null ? (
        <>
          {sel.map((id) => {
            const w = workspaces.find((x) => x.id === id)!
            const st = slotState(id)
            return (
              <div key={id} className="flex items-center gap-2.5 rounded-[10px] border border-edge bg-rail px-3.5 py-3">
                <Checkbox checked />
                <span className="text-[13px] font-medium">{w.name}</span>
                <span className="ml-auto inline-flex items-center gap-1.5 text-xs2 text-zinc-500">
                  <Dot health={st.every((s) => s.keyId) ? 'healthy' : 'degraded'} size={6} />
                  {st.map((s) => (s.keyId ? `${keyById(d, s.keyId)?.name} · ${storeById(d, keyById(d, s.keyId)!.storeId)?.name}` : `Missing ${s.slot}`)).join(', ')}
                </span>
              </div>
            )
          })}
          <div className="rounded-lg border border-green-500/30 bg-green-500/[0.06] px-3.5 py-2.5 text-sm2 text-green-400">Granted to {plural(done, 'workspace')}.</div>
          <Footer>
            <Button size="lg" variant="primary" onClick={onClose}>
              Done
            </Button>
          </Footer>
        </>
      ) : (
        <>
          <div className="-mt-2 text-xs text-zinc-500">Key slots: {tool.slots.map((s) => s.name).join(', ') || 'none'}. Each workspace fills them with a key it exposes.</div>
          <div className="flex flex-col gap-2.5">
            {workspaces.map((w) => {
              const checked = sel.includes(w.id)
              const st = slotState(w.id)
              const miss = st.filter((s) => !s.keyId)
              return (
                <div key={w.id} className="rounded-[10px] border border-edge bg-rail px-3.5 py-3">
                  <div className="flex items-center gap-2.5">
                    <Checkbox checked={checked} onChange={(v) => setSel(v ? [...sel, w.id] : sel.filter((x) => x !== w.id))} label={<span className="font-medium text-zinc-100">{w.name}</span>} />
                    {checked && !miss.length && (
                      <span className="ml-auto inline-flex items-center gap-1.5 text-xs2 text-zinc-500">
                        <Dot health="healthy" size={6} />
                        {tool.slots.length ? 'Slots filled' : 'No slots needed'}
                      </span>
                    )}
                  </div>
                  {checked &&
                    miss.map((m) => (
                      <div key={m.slot} className="mt-2 ml-6 rounded-lg border border-amber-500/30 bg-amber-500/[0.06] px-3 py-2 text-xs text-amber-400">
                        Missing: <span className="font-mono text-xs2">{m.slot}</span> — this workspace exposes no key for that slot.{' '}
                        {fixing?.wsId === w.id && fixing.slot === m.slot ? (
                          <span className="mt-2 flex items-center gap-2">
                            <select
                              aria-label="Key to expose"
                              value={fixKey}
                              onChange={(e) => setFixKey(e.target.value)}
                              className="flex-1 rounded-md border border-zinc-700 bg-page px-2 py-1 font-mono text-xs text-zinc-200 outline-none"
                            >
                              <option value="">Pick a key to expose…</option>
                              {orgKeys(d)
                                .filter((k) => !k.cabinetId && !k.sourceRemoved)
                                .sort((a, b) => Number(b.name === m.slot) - Number(a.name === m.slot))
                                .map((k) => (
                                  <option key={k.id} value={k.id}>
                                    {k.name} · {storeById(d, k.storeId)?.name}
                                  </option>
                                ))}
                            </select>
                            <Button
                              size="sm"
                              variant="primary"
                              disabled={!fixKey}
                              onClick={() => {
                                const k = keyById(d, fixKey)!
                                actions.exposeKey(w.id, fixKey)
                                // Name-based matching: if the exposed key has a different name, the slot is filled explicitly on grant.
                                if (k.name !== m.slot) setOverrides({ ...overrides, [`${w.id}:${m.slot}`]: fixKey })
                                setFixing(null)
                                setFixKey('')
                              }}
                            >
                              Expose
                            </Button>
                          </span>
                        ) : (
                          <button
                            type="button"
                            className="text-brass hover:text-brass-light"
                            onClick={() => {
                              setFixing({ wsId: w.id, slot: m.slot })
                              setFixKey(orgKeys(d).find((k) => k.name === m.slot)?.id ?? '')
                            }}
                          >
                            Expose a key
                          </button>
                        )}
                      </div>
                    ))}
                </div>
              )
            })}
          </div>
          {missing.length > 0 && <div className="text-xs text-zinc-500">{missing.map((id) => workspaces.find((w) => w.id === id)?.name).join(', ')} won’t be granted until {missing.length === 1 ? 'its slot is' : 'their slots are'} filled.</div>}
          <Footer>
            <Button size="lg" onClick={onClose}>
              Cancel
            </Button>
            <Button
              size="lg"
              variant="primary"
              disabled={!ready.length}
              onClick={() => {
                for (const id of ready) {
                  const map = Object.fromEntries(slotState(id).map((s) => [s.slot, s.keyId]))
                  actions.grantTool(id, tool.id, map)
                }
                setSel(ready)
                setDone(ready.length)
              }}
            >
              {ready.length ? `Grant to ${plural(ready.length, 'workspace')}` : 'Grant'}
            </Button>
          </Footer>
        </>
      )}
    </Modal>
  )
}
