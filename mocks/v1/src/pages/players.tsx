import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ago, initials, maskToken, until } from '../lib/format'
import { actions, agentById, canSeeWorkspace, isAdmin, isAdminRole, orgAgents, orgWorkspaces, useDB, useNow, userById, visibleEvents, wsById } from '../lib/store'
import type { Agent } from '../lib/types'
import { TokenPanel } from '../components/keyhole'
import { AgentsTable, AuditLog, RevokeAgentDialog, RotateAgentDialog, SuspendAgentDialog, UsersTable, useUserRows } from '../components/shared'
import { Avatar, Breadcrumb, Button, Card, Checkbox, Field, Footer, Input, Modal, PageTitle, Segmented, StatusInline, cx } from '../components/ui'
import { InviteModal, PendingInvites } from './orgs'

/* ------------------------------------------------------------------ */
/* Users                                                               */
/* ------------------------------------------------------------------ */
export function UsersPage() {
  const d = useDB()
  const rows = useUserRows()
  const [inviting, setInviting] = useState(false)
  return (
    <div>
      <PageTitle actions={isAdmin(d) && <Button variant="primary" onClick={() => setInviting(true)}>Invite user</Button>}>Users</PageTitle>
      <UsersTable rows={rows} className="mt-5 max-w-[1060px]" />
      <PendingInvites />
      <InviteModal open={inviting} onClose={() => setInviting(false)} />
    </div>
  )
}

export function UserDetail() {
  const d = useDB()
  const now = useNow()
  const { userId } = useParams()
  const u = userById(d, userId)
  if (!u || !u.roles[d.currentOrgId]) return <div className="text-sm text-zinc-400">This person isn’t in this organization. <Link to="/players/users">Back to users</Link></div>
  const role = u.roles[d.currentOrgId]
  const ws = orgWorkspaces(d).filter((w) => isAdminRole(role) || w.userIds.includes(u.id))
  const cabinets = d.cabinets.filter((c) => c.ownerId === u.id && canSeeWorkspace(d, c.workspaceId))
  const events = visibleEvents(d).filter((e) => e.actorId === u.id || e.object.includes(u.email) || e.object.includes(u.name))
  return (
    <div className="max-w-[1080px]">
      <Breadcrumb items={[{ label: 'Players' }, { label: 'Users', to: '/players/users' }, { label: u.name }]} />
      <div className="mt-2 flex items-center gap-3.5">
        <Avatar initials={initials(u.name)} size={40} />
        <div>
          <h1 className="m-0 text-lg font-semibold tracking-[-0.01em]">{u.name}</h1>
          <div className="text-xs text-zinc-500">
            {u.email} · {role} · {u.id === d.currentUserId ? 'active now' : `last active ${ago(u.lastActive, now).toLowerCase()}`}
          </div>
        </div>
        <span className="ml-auto">{u.locked ? <StatusInline tone="amber">Locked by support</StatusInline> : u.status === 'suspended' ? <StatusInline tone="amber">Suspended</StatusInline> : <StatusInline tone="green">Active</StatusInline>}</span>
      </div>
      {u.locked && (
        <div className="mt-5 flex items-center justify-between gap-4 rounded-lg border border-amber-500/30 bg-amber-500/[0.06] px-4 py-3 text-xs text-amber-400">
          <div className="leading-relaxed">
            Keyhole support locked this account {ago(u.lockedAt ?? null, now).toLowerCase()}
            {u.lockReason ? <> · <span className="text-zinc-300">{u.lockReason}</span></> : ''}. Only Keyhole support can unlock it.
          </div>
          {isAdmin(d) &&
            (u.unlockRequest ? (
              <span className="shrink-0 text-zinc-400">Unlock requested {ago(u.unlockRequest.at, now).toLowerCase()}</span>
            ) : (
              <Button size="sm" className="shrink-0" onClick={() => actions.requestUnlock(u.id)}>
                Ask support to unlock
              </Button>
            ))}
        </div>
      )}
      <div className="mt-6 grid grid-cols-2 gap-4">
        <Card className="p-5">
          <div className="eyebrow">Workspaces</div>
          <div className="mt-3 flex flex-wrap gap-2">
            {ws.length ? ws.map((w) => (
              <Link key={w.id} to={`/workspaces/${w.id}/summary`} className="rounded-md border border-edge bg-rail px-2.5 py-1 text-[13px] text-zinc-300 hover:text-white">
                {w.name}
              </Link>
            )) : <span className="text-sm2 text-zinc-500">None</span>}
          </div>
        </Card>
        <Card className="p-5">
          <div className="eyebrow">Cabinets they own</div>
          <div className="mt-3 flex flex-col gap-1.5">
            {cabinets.length ? cabinets.map((c) => (
              <Link key={c.id} to={`/workspaces/${c.workspaceId}/cabinets`} className="text-[13px] text-zinc-300 hover:text-white">
                {c.name} <span className="text-zinc-500">· {wsById(d, c.workspaceId)?.name} · {c.keyIds.length} keys</span>
              </Link>
            )) : <span className="text-sm2 text-zinc-500">None</span>}
          </div>
        </Card>
      </div>
      <div className="eyebrow mt-7 mb-3">Activity</div>
      <AuditLog events={events} scopeLabel={u.name} />
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Agents                                                              */
/* ------------------------------------------------------------------ */
export function AgentsPage() {
  const d = useDB()
  const [params, setParams] = useSearchParams()
  const [creating, setCreating] = useState(params.get('new') === '1')
  useEffect(() => {
    if (params.get('new')) setParams({}, { replace: true })
  }, []) // eslint-disable-line
  return (
    <div>
      <PageTitle actions={isAdmin(d) && <Button variant="primary" onClick={() => setCreating(true)}>New agent</Button>}>Agents</PageTitle>
      <AgentsTable agents={orgAgents(d)} className="mt-5 max-w-[1160px]" />
      <NewAgentModal open={creating} onClose={() => setCreating(false)} />
    </div>
  )
}

function NewAgentModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const d = useDB()
  const ws = orgWorkspaces(d)
  const [label, setLabel] = useState('')
  const [sel, setSel] = useState<string[]>([])
  const [expiry, setExpiry] = useState<'30' | '90' | '365' | 'none'>('90')
  const [rate, setRate] = useState('60')
  const [created, setCreated] = useState<{ token: string; agent: Agent } | null>(null)
  useEffect(() => {
    if (open) {
      setLabel(orgAgents(d).length ? '' : 'billing-agent')
      setSel(ws[0] ? [ws[0].id] : [])
      setExpiry('90')
      setRate('60')
      setCreated(null)
    }
  }, [open]) // eslint-disable-line
  const clash = orgAgents(d).some((a) => a.label === label.trim() && a.status !== 'revoked')
  const create = () => {
    const { id, token } = actions.createAgent({ label: label.trim(), workspaceIds: sel, expiryDays: expiry === 'none' ? null : Number(expiry), rateLimit: rate ? Number(rate) : null })
    setCreated({ token, agent: agentById(d, id) ?? ({ id, label: label.trim(), workspaceIds: sel } as Agent) })
  }
  if (created) {
    const names = created.agent.workspaceIds.map((id) => wsById(d, id)?.name).join(', ')
    return (
      <Modal open={open} onClose={() => {}} width={540} dismissable={false}>
        <TokenPanel token={created.token} title="Agent created" subtitle={`${label.trim()}${names ? ` · ${names}` : ''} · ${expiry === 'none' ? 'no expiry' : `expires in ${expiry} days`}`} onDone={onClose} />
      </Modal>
    )
  }
  return (
    <Modal open={open} onClose={onClose} width={480} title="New agent">
      <Field label="Label" hint="How it shows up in tables and logs." error={clash ? 'An active agent already uses this label.' : null}>
        <Input value={label} onChange={(e) => setLabel(e.target.value)} placeholder="billing-agent" autoFocus />
      </Field>
      <Field label="Workspaces">
        <div className="flex flex-col gap-2.5 rounded-lg border border-edge bg-page px-3 py-2.5">
          {ws.length ? ws.map((w) => <Checkbox key={w.id} checked={sel.includes(w.id)} onChange={(v) => setSel(v ? [...sel, w.id] : sel.filter((x) => x !== w.id))} label={w.name} />) : <span className="text-xs text-zinc-500">No workspaces yet — you can add this agent to one later.</span>}
        </div>
      </Field>
      <Field label="Expiry" optional>
        <Segmented
          value={expiry}
          onChange={setExpiry}
          options={[
            { value: '30', label: '30 days' },
            { value: '90', label: '90 days' },
            { value: '365', label: '365 days' },
            { value: 'none', label: 'None' },
          ]}
        />
      </Field>
      <Field label="Rate limit" optional>
        <div className="flex items-center gap-2">
          <Input type="number" min={1} value={rate} onChange={(e) => setRate(e.target.value)} className="w-[90px]" />
          <span className="text-sm2 text-zinc-500">calls per minute</span>
        </div>
      </Field>
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button size="lg" variant="primary" disabled={!label.trim() || clash} onClick={create}>
          Create agent
        </Button>
      </Footer>
    </Modal>
  )
}

export function AgentDetail() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const { agentId } = useParams()
  const a = agentById(d, agentId!)
  const [revoking, setRevoking] = useState(false)
  const [rotating, setRotating] = useState(false)
  const [suspending, setSuspending] = useState(false)
  const [rotated, setRotated] = useState<string | null>(null)
  const [label, setLabel] = useState(a?.label ?? '')
  if (!a) return <div className="text-sm text-zinc-400">This agent doesn’t exist. <Link to="/players/agents">Back to agents</Link></div>
  const admin = isAdmin(d)
  const revoked = a.status === 'revoked'
  const lockLists = d.cabinets.filter((c) => canSeeWorkspace(d, c.workspaceId) && Array.isArray(c.access) && c.access.some((p) => p.kind === 'agent' && p.id === a.id)).map((c) => c.name)
  const events = visibleEvents(d).filter((e) => e.actorId === a.id || e.object.includes(a.label))
  return (
    <div className="max-w-[1080px]">
      <Breadcrumb items={[{ label: 'Players' }, { label: 'Agents', to: '/players/agents' }, { label: a.label }]} />
      <PageTitle
        sub={a.status === 'active' ? <StatusInline tone="green">Active</StatusInline> : a.status === 'suspended' ? <StatusInline tone="amber">Suspended</StatusInline> : <StatusInline tone="gray">Revoked</StatusInline>}
        actions={
          admin &&
          !revoked && (
            <>
              <Button onClick={() => setRotating(true)}>Rotate token</Button>
              <Button onClick={() => (a.status === 'suspended' ? actions.setAgentStatus(a.id, 'active') : setSuspending(true))}>{a.status === 'suspended' ? 'Resume' : 'Suspend'}</Button>
              <Button variant="danger" onClick={() => setRevoking(true)}>
                Revoke token
              </Button>
            </>
          )
        }
      >
        <span className={cx(revoked && 'text-zinc-500 line-through')}>{a.label}</span>
      </PageTitle>
      <Card className="mt-5 grid grid-cols-[140px_1fr_140px_1fr] gap-x-4 gap-y-2.5 p-5 text-[13px]">
        <span className="text-zinc-500">Token</span>
        <span className="masked-token text-xs text-zinc-400">{maskToken(a.tokenLast4)}</span>
        <span className="text-zinc-500">Created</span>
        <span>
          {ago(a.createdAt, now)} · {a.createdBy}
        </span>
        <span className="text-zinc-500">Expiry</span>
        <span className={cx(a.expiresAt && a.expiresAt - now < 14 * 86_400_000 && !revoked && 'text-amber-400')}>{a.expiresAt ? until(a.expiresAt, now) : 'None'}</span>
        <span className="text-zinc-500">Last used</span>
        <span>{ago(a.lastUsedAt, now)}</span>
        <span className="text-zinc-500">Workspaces</span>
        <span>{a.workspaceIds.map((id) => wsById(d, id)?.name).join(', ') || '—'}</span>
        <span className="text-zinc-500">Rate limit</span>
        <span>{a.rateLimit ? `${a.rateLimit}/min` : '—'}</span>
        <span className="text-zinc-500">Lock lists</span>
        <span>{lockLists.join(', ') || 'None'}</span>
        <span className="text-zinc-500">Label</span>
        <span>
          {admin && !revoked ? (
            <input
              aria-label="Agent label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              onBlur={() => actions.renameAgent(a.id, label)}
              className="w-full rounded-md border border-transparent bg-transparent px-1 font-medium outline-none hover:border-edge focus:border-zinc-600"
            />
          ) : (
            a.label
          )}
        </span>
      </Card>
      <div className="eyebrow mt-7 mb-3">Activity</div>
      <AuditLog events={events} scopeLabel={a.label} />
      <RevokeAgentDialog agent={revoking ? a : null} onClose={() => setRevoking(false)} lockLists={lockLists} />
      <RotateAgentDialog agent={rotating ? a : null} onClose={() => setRotating(false)} onRotated={(_, token) => setRotated(token)} />
      <SuspendAgentDialog agent={suspending ? a : null} onClose={() => setSuspending(false)} />
      <Modal open={!!rotated} onClose={() => {}} width={540} dismissable={false}>
        {rotated && <TokenPanel token={rotated} title="Token rotated" subtitle={a.label} note="The old token keeps working for 10 minutes so running agents can switch over." onDone={() => setRotated(null)} />}
      </Modal>
      {revoked && (
        <div className="mt-4 text-xs text-zinc-500">
          Revoked tokens can’t be restored. <button className="text-brass hover:text-brass-light" onClick={() => nav('/players/agents?new=1')}>Create a new agent</button> to reconnect.
        </div>
      )}
    </div>
  )
}
