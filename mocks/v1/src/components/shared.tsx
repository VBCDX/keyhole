import { Fragment, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ago, clock, expiringSoon, initials, maskToken, until } from '../lib/format'
import { liveTick } from '../lib/simulate'
import { actions, agentsCreatedBy, canManageMember, isAdmin, isAdminRole, isDemotion, isLastOwner, myRole, org, orgAdmins, orgWorkspaces, useDB, useNow, wsById } from '../lib/store'
import type { Agent, AuditEvent, Role, User } from '../lib/types'
import { CopyChip, TokenPanel } from './keyhole'
import {
  Avatar,
  Button,
  FOCUS_RING,
  activateOnKey,
  Checkbox,
  ErrorBox,
  Field,
  Footer,
  Input,
  Menu,
  Modal,
  Row,
  Segmented,
  Select,
  SkeletonRows,
  StatusInline,
  Table,
  cx,
  useFakeLoad,
} from './ui'

/* ------------------------------------------------------------------ */
/* List state: loading → populated, with the demo override.           */
/* ------------------------------------------------------------------ */
export function useListState() {
  const d = useDB()
  const ready = useFakeLoad()
  const [retried, setRetried] = useState(0)
  const state: 'loading' | 'error' | 'ready' = d.listState === 'loading' || !ready ? 'loading' : d.listState === 'error' && retried === 0 ? 'error' : 'ready'
  return { state, retry: () => setRetried((n) => n + 1) }
}

export function ListBody({ cols, what, children, empty, rows = 3 }: { cols: string; what: string; children: ReactNode; empty?: ReactNode; rows?: number }) {
  const { state, retry } = useListState()
  if (state === 'loading') return <SkeletonRows cols={cols} n={rows} />
  if (state === 'error')
    return (
      <div className="p-3">
        <ErrorBox what={what} onRetry={retry} />
      </div>
    )
  return <>{empty ?? children}</>
}

/* ------------------------------------------------------------------ */
/* Impact preview — nothing is deleted without one.                   */
/* ------------------------------------------------------------------ */
export type ImpactRow = [string, ReactNode, ('amber' | 'red')?]
/** The key/value box of an impact preview, also used inline in dialogs that reduce access. */
export function ImpactRows({ rows }: { rows: ImpactRow[] }) {
  return (
    <div className="flex flex-col gap-2.5 rounded-[10px] border border-edge bg-rail p-4 text-sm2">
      {rows.map(([k, v, tone]) => (
        <div key={k} className="flex justify-between gap-6">
          <span className="shrink-0 text-zinc-500">{k}</span>
          <span className={cx('text-right', tone === 'amber' && 'font-semibold text-amber-400', tone === 'red' && 'font-semibold text-red-400')}>{v}</span>
        </div>
      ))}
    </div>
  )
}

export function ImpactDialog({
  open,
  onClose,
  title,
  rows,
  body,
  confirmLabel,
  onConfirm,
  typeToConfirm,
  secondary,
  confirmDisabled,
}: {
  open: boolean
  onClose: () => void
  title: ReactNode
  rows: ImpactRow[]
  body?: ReactNode
  confirmLabel: string
  onConfirm: () => void
  typeToConfirm?: string
  secondary?: { label: string; onClick: () => void }
  confirmDisabled?: boolean
}) {
  const [typed, setTyped] = useState('')
  useEffect(() => setTyped(''), [open])
  const ok = (!typeToConfirm || typed === typeToConfirm) && !confirmDisabled
  return (
    <Modal open={open} onClose={onClose} width={500} title={title}>
      <ImpactRows rows={rows} />
      {body && <div className="text-sm2 leading-relaxed text-zinc-400">{body}</div>}
      {typeToConfirm && (
        <Field label={<>Type “{typeToConfirm}” to confirm</>}>
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={typeToConfirm} autoFocus />
        </Field>
      )}
      <Footer>
        {secondary ? (
          <Button size="lg" onClick={secondary.onClick}>
            {secondary.label}
          </Button>
        ) : (
          <Button size="lg" onClick={onClose}>
            Cancel
          </Button>
        )}
        <Button
          size="lg"
          variant="danger"
          disabled={!ok}
          onClick={() => {
            onConfirm()
            onClose()
          }}
        >
          {confirmLabel}
        </Button>
      </Footer>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* The standard Users table — identical wherever it appears.           */
/* ------------------------------------------------------------------ */
export const USER_COLS = '1.5fr 1.8fr 1fr 1.1fr 1fr 1.4fr 36px'

type UserRow = { user: User; role: Role; invited: boolean }

export function useUserRows(filterWsId?: string): UserRow[] {
  const d = useDB()
  return useMemo(() => {
    const rows: UserRow[] = []
    for (const u of d.users) {
      const r = u.roles[d.currentOrgId]
      if (r) rows.push({ user: u, role: r, invited: false })
      else {
        const inv = d.invites.find((i) => i.orgId === d.currentOrgId && i.email === u.email)
        if (inv) rows.push({ user: u, role: inv.role, invited: true })
      }
    }
    if (!filterWsId) return rows
    const w = wsById(d, filterWsId)
    // Org admins are every workspace's admins by default, so they're listed on each one.
    return rows.filter((r) => w?.userIds.includes(r.user.id) || isAdminRole(r.role))
  }, [d, filterWsId])
}

export function UsersTable({ rows, className }: { rows: UserRow[]; className?: string }) {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const admin = isAdmin(d)
  const [roleFor, setRoleFor] = useState<UserRow | null>(null)
  const [wsFor, setWsFor] = useState<UserRow | null>(null)
  const [removeFor, setRemoveFor] = useState<UserRow | null>(null)
  const [suspendFor, setSuspendFor] = useState<UserRow | null>(null)
  const [transferTo, setTransferTo] = useState<User | null>(null)
  const workspaces = orgWorkspaces(d)
  const owner = myRole(d) === 'Owner'
  const wsNames = (u: User, role: Role) => (isAdminRole(role) ? 'All (org admin)' : workspaces.filter((w) => w.userIds.includes(u.id)).map((w) => w.name).join(', ') || '—')
  const invitedWs = (u: User) => d.invites.find((i) => i.email === u.email && i.orgId === d.currentOrgId)?.workspaceIds.map((id) => wsById(d, id)?.name).join(', ') || '—'

  const cabinetsCreated = removeFor ? d.cabinets.filter((c) => c.ownerId === removeFor.user.id && d.workspaces.some((w) => w.id === c.workspaceId && w.orgId === d.currentOrgId)) : []
  const menuFor = (row: UserRow) => {
    const { user: u, role } = row
    if (u.id === d.currentUserId) {
      // Your own row: only Owners get a menu, to transfer ownership or see why they can't step down.
      if (role !== 'Owner') return null
      const last = isLastOwner(d, u.id)
      return [
        last ? { label: 'Last Owner — transfer ownership to step down', disabled: true, onClick: () => {} } : { label: 'Change role', onClick: () => setRoleFor(row) },
        last ? { label: 'Can’t remove the last Owner', disabled: true, onClick: () => {} } : null,
      ]
    }
    if (!canManageMember(d, u.id)) return null
    return [
      { label: 'Change role', onClick: () => setRoleFor(row) },
      isAdminRole(role) ? null : { label: 'Assign workspaces', onClick: () => setWsFor(row) },
      owner && role !== 'Owner' && u.status === 'active' && !u.locked ? { label: 'Transfer ownership', onClick: () => setTransferTo(u) } : null,
      u.locked ? { label: u.unlockRequest ? 'Unlock requested' : 'Ask support to unlock', disabled: !!u.unlockRequest, onClick: () => actions.requestUnlock(u.id) } : null,
      u.status === 'suspended' ? { label: 'Reactivate', onClick: () => actions.setUserSuspended(u.id, false) } : { label: 'Suspend', onClick: () => setSuspendFor(row) },
      { label: 'Remove', danger: true, onClick: () => setRemoveFor(row) },
    ]
  }

  return (
    <>
      <Table cols={USER_COLS} head={['Name', 'Email', 'Role', 'Status', 'Last active', 'Workspaces', '']} className={className}>
        <ListBody cols={USER_COLS} what="users" empty={rows.length ? undefined : <div className="p-8 text-center text-[13px] text-zinc-400">No one here yet. Invite a teammate to share this workspace.</div>}>
          {rows.map(({ user: u, role, invited }) => (
            <Row key={u.id} cols={USER_COLS} onClick={invited ? undefined : () => nav(`/players/users/${u.id}`)}>
              <div className={cx('truncate font-medium', invited && 'text-zinc-400')}>{invited ? u.email : u.name}</div>
              <div className="truncate text-zinc-400">{u.email}</div>
              <div className="text-zinc-400">{role}</div>
              <div>
                {u.locked ? (
                  <StatusInline tone="amber">Locked</StatusInline>
                ) : invited ? (
                  <StatusInline tone="gray">Invited</StatusInline>
                ) : u.status === 'suspended' ? (
                  <StatusInline tone="amber">Suspended</StatusInline>
                ) : (
                  <StatusInline tone="green">Active</StatusInline>
                )}
              </div>
              <div className="text-zinc-500">{invited ? '—' : u.id === d.currentUserId ? 'Now' : ago(u.lastActive, now).replace(' ago', ' ago')}</div>
              <div className="truncate text-zinc-400">{invited ? invitedWs(u) : wsNames(u, role)}</div>
              <div className="text-right">
                {admin && !invited && menuFor({ user: u, role, invited }) && <Menu items={menuFor({ user: u, role, invited })!} />}
              </div>
            </Row>
          ))}
        </ListBody>
      </Table>

      <ImpactDialog
        open={!!suspendFor}
        onClose={() => setSuspendFor(null)}
        title={`Suspend ${suspendFor?.user.name}?`}
        rows={[
          ['Workspaces', suspendFor ? wsNames(suspendFor.user, suspendFor.role) : ''],
          ['Agents they created', suspendFor ? createdAgentsLabel(agentsCreatedBy(d, suspendFor.user).map((a) => a.label)) : ''],
          ['Last active', ago(suspendFor?.user.lastActive ?? null, now)],
        ]}
        body="They can’t sign in or use any workspace until you reactivate them. Nothing they created or did changes: agents, grants, keys and cabinets keep working."
        confirmLabel="Suspend user"
        onConfirm={() => suspendFor && actions.setUserSuspended(suspendFor.user.id, true)}
      />
      <ChangeRoleModal row={roleFor} onClose={() => setRoleFor(null)} />
      <TransferOwnershipDialog open={!!transferTo} to={transferTo} onClose={() => setTransferTo(null)} />
      <AssignWorkspacesModal row={wsFor} onClose={() => setWsFor(null)} />
      <ImpactDialog
        open={!!removeFor}
        onClose={() => setRemoveFor(null)}
        title={`Remove ${removeFor?.user.name}?`}
        rows={[
          ['Workspaces', removeFor ? wsNames(removeFor.user, removeFor.role) : ''],
          ['Cabinets they created', cabinetsCreated.length ? `${cabinetsCreated.map((c) => c.name).join(', ')} — keep working; admins take over management` : 'None'],
          ['Agents they created', removeFor ? createdAgentsLabel(agentsCreatedBy(d, removeFor.user).map((a) => a.label)) : ''],
          ['Last active', ago(removeFor?.user.lastActive ?? null, now)],
        ]}
        body="They lose access to every workspace in this organization. Nothing they created or did changes — tools they granted stay granted, keys and connectors they added stay — and the audit log keeps their name on it."
        confirmLabel="Remove user"
        onConfirm={() => removeFor && actions.removeUser(removeFor.user.id)}
      />
    </>
  )
}

const ROLE_HINT: Record<Role, string> = {
  Owner: 'Everything a userAdmin can do, plus renaming or deleting the organization and managing other Owners.',
  userAdmin: 'Administers every workspace: invites people, manages stores, workspaces, agents and tools.',
  user: 'Sees only the workspaces they’re added to and manages the cabinets they make.',
}

function ChangeRoleModal({ row, onClose }: { row: UserRow | null; onClose: () => void }) {
  const d = useDB()
  const [role, setRole] = useState<Role>('user')
  useEffect(() => {
    if (row) setRole(row.role)
  }, [row])
  const owner = myRole(d) === 'Owner'
  const demoting = !!row && isDemotion(row.role, role)
  // Who administers every workspace once this change is made. Rule 1 guarantees an Owner remains.
  const adminsAfter = orgAdmins(d)
    .map((u) => ({ u, r: u.id === row?.user.id ? role : u.roles[d.currentOrgId] }))
    .filter((x) => isAdminRole(x.r))
  const explicit = row ? orgWorkspaces(d).filter((w) => w.userIds.includes(row.user.id)).map((w) => w.name) : []
  return (
    <Modal open={!!row} onClose={onClose} title={`Change role for ${row?.user.name}`} width={480}>
      <Segmented<Role>
        value={role}
        onChange={setRole}
        options={[
          { value: 'user', label: 'user' },
          { value: 'userAdmin', label: 'userAdmin' },
          ...(owner ? [{ value: 'Owner' as Role, label: 'Owner' }] : []),
        ]}
      />
      <div className="text-xs text-zinc-500">{ROLE_HINT[role]}</div>
      {demoting && (
        <ImpactRows
          rows={[
            ['Role', `${row!.role} → ${role}`, 'amber'],
            ['Workspaces they can use', role === 'user' ? explicit.join(', ') || 'None — add them on a workspace' : 'All (org admin)', role === 'user' ? 'amber' : undefined],
            ['Workspace admins after this', `${adminsAfter.map((x) => `${x.u.name} (${x.r})`).join(', ')} — every workspace keeps an Owner`],
            ['Agents they created', 'Unaffected'],
          ]}
        />
      )}
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant={demoting ? 'danger' : 'primary'}
          disabled={!row || role === row.role}
          onClick={() => {
            if (row) actions.changeRole(row.user.id, role)
            onClose()
          }}
        >
          {demoting ? `Demote to ${role}` : 'Change role'}
        </Button>
      </Footer>
    </Modal>
  )
}

/** Hands the organization to another member; the current Owner becomes a userAdmin. Owners only. */
export function TransferOwnershipDialog({ open, to, onClose }: { open: boolean; to?: User | null; onClose: () => void }) {
  const d = useDB()
  const [picked, setPicked] = useState('')
  const close = () => {
    setPicked('')
    onClose()
  }
  const candidates = d.users.filter((u) => u.id !== d.currentUserId && u.roles[d.currentOrgId] && u.status === 'active' && !u.locked)
  const target = to ?? candidates.find((u) => u.id === picked)
  const role = target?.roles[d.currentOrgId]
  return (
    <ImpactDialog
      open={open}
      onClose={close}
      title={`Transfer ownership of ${org(d)?.name}?`}
      rows={[
        ['New Owner', target ? `${target.name} · ${role} → Owner` : 'Pick someone below'],
        ['You', 'Owner → userAdmin', 'amber'],
        ['You keep', 'Admin of every workspace'],
        ['You lose', 'Renaming or deleting the organization, managing Owners', 'amber'],
      ]}
      body={
        <div className="flex flex-col gap-3">
          {!to && (
            <Field label="New Owner">
              <Select value={picked} onChange={(e) => setPicked(e.target.value)}>
                <option value="">Pick a member…</option>
                {candidates.map((u) => (
                  <option key={u.id} value={u.id}>
                    {u.name} · {u.roles[d.currentOrgId]}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <span>Only the new Owner can give ownership back. The change is recorded in the audit log.</span>
        </div>
      }
      confirmLabel="Transfer ownership"
      confirmDisabled={!target}
      onConfirm={() => target && actions.transferOwnership(target.id)}
    />
  )
}

function AssignWorkspacesModal({ row, onClose }: { row: UserRow | null; onClose: () => void }) {
  const d = useDB()
  const workspaces = orgWorkspaces(d)
  const [sel, setSel] = useState<string[]>([])
  useEffect(() => {
    if (row) setSel(workspaces.filter((w) => w.userIds.includes(row.user.id)).map((w) => w.id))
  }, [row]) // eslint-disable-line
  const losing = row ? workspaces.filter((w) => w.userIds.includes(row.user.id) && !sel.includes(w.id)) : []
  const theirCabinets = d.cabinets.filter((c) => c.ownerId === row?.user.id && losing.some((w) => w.id === c.workspaceId)).map((c) => c.name)
  return (
    <Modal open={!!row} onClose={onClose} title={`Workspaces for ${row?.user.name}`} width={460}>
      <div className="flex flex-col gap-2 rounded-lg border border-edge bg-page p-3">
        {workspaces.map((w) => (
          <Checkbox key={w.id} checked={sel.includes(w.id)} onChange={(v) => setSel(v ? [...sel, w.id] : sel.filter((x) => x !== w.id))} label={w.name} />
        ))}
      </div>
      {losing.length > 0 && (
        <ImpactRows
          rows={[
            ['Loses access to', losing.map((w) => w.name).join(', '), 'amber'],
            ['Cabinets they created there', theirCabinets.length ? `${theirCabinets.join(', ')} — keep working; admins take over management` : 'None'],
          ]}
        />
      )}
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant={losing.length ? 'danger' : 'primary'}
          onClick={() => {
            if (row) actions.setUserWorkspaces(row.user.id, sel)
            onClose()
          }}
        >
          Save workspaces
        </Button>
      </Footer>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* The standard Agents table — identical wherever it appears.          */
/* ------------------------------------------------------------------ */
export const AGENT_COLS = '1.4fr 1.6fr 0.9fr 1.1fr 1fr 1fr 1fr 0.8fr 36px'

export function AgentsTable({ agents, className, emptyText }: { agents: Agent[]; className?: string; emptyText?: ReactNode }) {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const admin = isAdmin(d)
  const [editing, setEditing] = useState<string | null>(null)
  const [rotated, setRotated] = useState<{ agent: Agent; token: string } | null>(null)
  const [rotateFor, setRotateFor] = useState<Agent | null>(null)
  const [suspendFor, setSuspendFor] = useState<Agent | null>(null)
  const [revokeFor, setRevokeFor] = useState<Agent | null>(null)
  const wsNames = (a: Agent) => a.workspaceIds.map((id) => wsById(d, id)?.name).filter(Boolean).join(', ') || '—'
  const lockLists = (a: Agent) => d.cabinets.filter((c) => Array.isArray(c.access) && c.access.some((p) => p.kind === 'agent' && p.id === a.id)).map((c) => c.name)

  return (
    <>
      <Table cols={AGENT_COLS} head={['Label', 'Token', 'Status', 'Created', 'Expiry', 'Last used', 'Workspaces', 'Rate limit', '']} className={className}>
        <ListBody cols={AGENT_COLS} what="agents" empty={agents.length ? undefined : <div className="p-10 text-center text-[13px] text-zinc-400">{emptyText ?? 'No agents yet. Create one to let an AI assistant or program connect.'}</div>}>
          {agents.map((a) => {
            const revoked = a.status === 'revoked'
            const soon = !revoked && expiringSoon(a.expiresAt, now)
            return (
              <Row key={a.id} cols={AGENT_COLS} onClick={() => nav(`/players/agents/${a.id}`)} className={cx(revoked && 'text-zinc-500')}>
                <div className="min-w-0" onClick={(e) => editing === a.id && e.stopPropagation()}>
                  {editing === a.id ? (
                    <input
                      autoFocus
                      defaultValue={a.label}
                      aria-label="Agent label"
                      onBlur={(e) => {
                        actions.renameAgent(a.id, e.target.value)
                        setEditing(null)
                      }}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter') (e.target as HTMLInputElement).blur()
                        if (e.key === 'Escape') setEditing(null)
                      }}
                      className="w-full rounded-md border border-zinc-600 bg-page px-2 py-0.5 text-[13px] font-medium outline-none"
                    />
                  ) : (
                    <span className="group inline-flex items-center gap-1.5">
                      <span className={cx('truncate font-medium', revoked && 'line-through')}>{a.label}</span>
                      {admin && !revoked && (
                        <button
                          type="button"
                          aria-label="Edit label"
                          onClick={(e) => {
                            e.stopPropagation()
                            setEditing(a.id)
                          }}
                          className="text-2xs text-zinc-600 opacity-0 group-hover:opacity-100 hover:text-zinc-300"
                        >
                          ✎
                        </button>
                      )}
                    </span>
                  )}
                </div>
                {/* Masked only, and nothing to copy: the full token was shown once, at creation. */}
                <div className={cx('masked-token text-xs', revoked ? 'text-zinc-600' : 'text-zinc-400')}>{maskToken(a.tokenLast4)}</div>
                <div>
                  {a.status === 'active' ? <StatusInline tone="green">Active</StatusInline> : a.status === 'suspended' ? <StatusInline tone="amber">Suspended</StatusInline> : <StatusInline tone="gray">Revoked</StatusInline>}
                </div>
                <div className="text-zinc-400">
                  {ago(a.createdAt, now)} · {a.createdBy === d.users.find((u) => u.id === d.currentUserId)?.name ? 'you' : a.createdBy.split(' ')[0]}
                </div>
                <div className={cx(soon ? 'font-medium text-amber-400' : 'text-zinc-400', revoked && '!text-zinc-600')}>{revoked ? '—' : a.expiresAt ? until(a.expiresAt, now) : 'None'}</div>
                <div className="text-zinc-500">{ago(a.lastUsedAt, now)}</div>
                <div className="truncate text-zinc-400">{wsNames(a)}</div>
                <div className="text-zinc-400">{a.rateLimit ? `${a.rateLimit}/min` : '—'}</div>
                <div className="text-right">
                  {admin && !revoked && (
                    <Menu
                      items={[
                        { label: 'Rotate token', onClick: () => setRotateFor(a) },
                        a.status === 'suspended' ? { label: 'Resume', onClick: () => actions.setAgentStatus(a.id, 'active') } : { label: 'Suspend', onClick: () => setSuspendFor(a) },
                        { label: 'Revoke token', danger: true, onClick: () => setRevokeFor(a) },
                      ]}
                    />
                  )}
                </div>
              </Row>
            )
          })}
        </ListBody>
      </Table>

      <Modal open={!!rotated} onClose={() => {}} width={540} dismissable={false}>
        {rotated && (
          <TokenPanel
            token={rotated.token}
            title="Token rotated"
            subtitle={`${rotated.agent.label} · ${wsNames(rotated.agent)}`}
            note="The old token keeps working for 10 minutes so running agents can switch over."
            onDone={() => setRotated(null)}
          />
        )}
      </Modal>
      <RotateAgentDialog agent={rotateFor} onClose={() => setRotateFor(null)} onRotated={(agent, token) => setRotated({ agent, token })} />
      <SuspendAgentDialog agent={suspendFor} onClose={() => setSuspendFor(null)} />
      <RevokeAgentDialog agent={revokeFor} onClose={() => setRevokeFor(null)} lockLists={revokeFor ? lockLists(revokeFor) : []} />
    </>
  )
}

/** "billing-agent, docs-agent (keep working)" — agents belong to the organization. */
export const createdAgentsLabel = (labels: string[]) => (labels.length ? `${labels.join(', ')} — unaffected; agents belong to the organization` : 'None')

/** Rotating starts a 10-minute clock for a running agent, so it gets a preview first. */
export function RotateAgentDialog({ agent, onClose, onRotated }: { agent: Agent | null; onClose: () => void; onRotated: (agent: Agent, token: string) => void }) {
  const d = useDB()
  const now = useNow(5000)
  const recent = agent?.lastUsedAt && now - agent.lastUsedAt < 60 * 60_000
  return (
    <ImpactDialog
      open={!!agent}
      onClose={onClose}
      title={`Rotate token for ${agent?.label}?`}
      rows={[
        ['Last used', ago(agent?.lastUsedAt ?? null, now), recent ? 'amber' : undefined],
        ['Workspaces', agent?.workspaceIds.map((id) => wsById(d, id)?.name).join(', ') || 'None'],
        ['Current token', agent ? `${maskToken(agent.tokenLast4)} · works 10 more minutes` : '', 'amber'],
      ]}
      body="The new token is shown once. Put it in the agent’s config within 10 minutes — after that, requests with the old token are blocked."
      confirmLabel="Rotate token"
      onConfirm={() => agent && onRotated(agent, actions.rotateAgent(agent.id))}
    />
  )
}

export function SuspendAgentDialog({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const d = useDB()
  const now = useNow(5000)
  return (
    <ImpactDialog
      open={!!agent}
      onClose={onClose}
      title={`Suspend ${agent?.label}?`}
      rows={[
        ['Last used', ago(agent?.lastUsedAt ?? null, now)],
        ['Workspaces', agent?.workspaceIds.map((id) => wsById(d, id)?.name).join(', ') || 'None'],
      ]}
      body="Its requests are blocked and logged until you resume it. The token stays the same, so resuming needs no config change."
      confirmLabel="Suspend agent"
      onConfirm={() => agent && actions.setAgentStatus(agent.id, 'suspended')}
    />
  )
}

export function RevokeAgentDialog({ agent, onClose, lockLists }: { agent: Agent | null; onClose: () => void; lockLists: string[] }) {
  const d = useDB()
  const now = useNow(5000)
  const recent = agent?.lastUsedAt && now - agent.lastUsedAt < 60 * 60_000
  return (
    <ImpactDialog
      open={!!agent}
      onClose={onClose}
      title={`Revoke token for ${agent?.label}?`}
      rows={[
        ['Last used', ago(agent?.lastUsedAt ?? null, now), recent ? 'amber' : undefined],
        ['Workspaces affected', agent?.workspaceIds.map((id) => wsById(d, id)?.name).join(', ') || 'None'],
        ['Cabinets on its lock list', lockLists.join(', ') || 'None'],
      ]}
      body="Its next request is blocked and logged. Revoking can't be undone — create a new agent to reconnect."
      confirmLabel="Revoke token"
      onConfirm={() => agent && actions.revokeAgent(agent.id)}
    />
  )
}

/* ------------------------------------------------------------------ */
/* The activity log — one design, shown filtered everywhere.           */
/* ------------------------------------------------------------------ */
const TYPE_TONE: Record<AuditEvent['type'], string> = {
  request: 'text-zinc-400 bg-line border-chip',
  blocked: 'text-red-400 bg-red-500/10 border-red-500/30',
  error: 'text-amber-400 bg-amber-500/[0.08] border-amber-500/30',
  admin: 'text-zinc-400 bg-line border-chip',
  support: 'text-sky-400 bg-sky-500/10 border-sky-500/30',
  connection: 'text-zinc-400 bg-line border-chip',
  verification: 'text-green-400 bg-green-500/10 border-green-500/30',
}
const DOT: Record<AuditEvent['severity'], string> = { ok: 'bg-green-500', blocked: 'bg-red-500', warn: 'bg-amber-500', info: 'bg-zinc-400' }

function resultTone(e: AuditEvent) {
  if (e.severity === 'blocked') return 'text-red-400'
  if (e.severity === 'warn') return 'text-amber-400'
  if (e.severity === 'ok') return 'text-green-400'
  return 'text-zinc-400'
}

export function LogRow({ e, compact, expanded, onToggle, fresh }: { e: AuditEvent; compact?: boolean; expanded?: boolean; onToggle?: () => void; fresh?: boolean }) {
  const blocked = e.severity === 'blocked'
  const expandable = !compact && !!(e.reason || e.detail)
  return (
    <div className={cx('border-b border-line', blocked && 'border-l-[3px] border-l-red-600 bg-red-500/[0.04]', fresh && 'animate-row-in')}>
      <div
        className={cx('flex items-center gap-3.5 px-4 text-sm2', compact ? 'py-2.5' : 'py-[11px]', expandable && cx('cursor-pointer hover:bg-white/[0.015]', FOCUS_RING))}
        onClick={expandable ? onToggle : undefined}
        role={expandable ? 'button' : undefined}
        tabIndex={expandable ? 0 : undefined}
        onKeyDown={expandable && onToggle ? activateOnKey(onToggle) : undefined}
        aria-expanded={expandable ? expanded : undefined}
      >
        <span className={cx('shrink-0 font-mono text-xs2 text-zinc-500', blocked ? 'w-[61px]' : 'w-16')}>{clock(e.at)}</span>
        <span className={cx('size-[7px] min-w-[7px] rounded-full', DOT[e.severity])} />
        {!compact && <span className={cx('rounded-full border px-2 py-0.5 text-2xs font-semibold', TYPE_TONE[e.type])}>{e.type}</span>}
        <span className="flex min-w-0 items-center gap-2">
          <span className="shrink-0 text-zinc-300">{e.actor}</span>
          <span className="text-zinc-600">→</span>
          <span className="truncate text-zinc-400">
            {e.object}
            {!compact && e.destination ? ` · ${e.destination}` : ''}
          </span>
        </span>
        <span className={cx('ml-auto shrink-0 whitespace-nowrap', resultTone(e))}>{e.result}</span>
        <CopyChip value={e.trk} variant="inline" />
        {!compact && <span className={cx('w-2.5 text-[10px] text-zinc-600', !expandable && 'invisible')}>{expanded ? '▴' : '▾'}</span>}
      </div>
      {expanded && expandable && (
        <div className="flex flex-col gap-2 pr-4 pb-3.5 pl-[94px]">
          {e.reason && <div className="text-sm2 text-zinc-100">{e.reason}</div>}
          {e.detail && (
            <div className="grid max-w-[560px] grid-cols-[120px_1fr] gap-x-3 gap-y-1 text-xs text-zinc-400">
              {e.detail.map(([k, v]) => (
                <Fragment key={k}>
                  <span className="text-zinc-500">{k}</span>
                  <span className={cx(/^(GET|POST|PUT|PATCH|DELETE) |\/|kh_/.test(v) && 'font-mono text-xs2')}>{v}</span>
                </Fragment>
              ))}
            </div>
          )}
          {e.fix && (
            <Link to={e.fix.to} className="self-start text-sm2">
              {e.fix.label} →
            </Link>
          )}
        </div>
      )}
    </div>
  )
}

/** Compact feed (home dashboard). */
export function LogFeed({ events }: { events: AuditEvent[] }) {
  return (
    <div className="overflow-hidden rounded-[10px] border border-edge bg-panel [&>*:last-child]:border-b-0">
      {events.map((e) => (
        <LogRow key={e.id} e={e} compact />
      ))}
    </div>
  )
}

const FILTER_SEL = 'appearance-none rounded-lg border border-edge bg-panel py-2 pr-7 pl-3 text-sm2 text-zinc-400 outline-none hover:border-zinc-700 focus:border-zinc-500'
function Filter({ value, onChange, label, children }: { value: string; onChange: (v: string) => void; label: string; children: ReactNode }) {
  return (
    <div className="relative">
      <select aria-label={label} className={cx(FILTER_SEL, value && 'text-zinc-200')} value={value} onChange={(e) => onChange(e.target.value)}>
        <option value="">{label}</option>
        {children}
      </select>
      <span className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-[9px] text-zinc-500">▾</span>
    </div>
  )
}

export function AuditLog({ events, scopeLabel, hideWorkspaceFilter, initialExpand }: { events: AuditEvent[]; scopeLabel?: string; hideWorkspaceFilter?: boolean; initialExpand?: string }) {
  const d = useDB()
  const role = myRole(d)
  const [q, setQ] = useState('')
  const [type, setType] = useState('')
  const [actor, setActor] = useState('')
  const [ws, setWs] = useState('')
  const [result, setResult] = useState('')
  const [range, setRange] = useState('')
  const [live, setLive] = useState(false)
  const [expanded, setExpanded] = useState<Set<string>>(new Set(initialExpand ? [initialExpand] : []))
  const [seen] = useState(() => new Set(events.map((e) => e.id)))
  const { state, retry } = useListState()

  useEffect(() => {
    if (!live) return
    liveTick()
    const t = setInterval(liveTick, 2600)
    return () => clearInterval(t)
  }, [live])

  const actors = Array.from(new Set(events.map((e) => e.actor))).sort()
  const workspaces = orgWorkspaces(d)
  const now = Date.now()
  const filtered = events.filter((e) => {
    if (q) {
      const s = q.toLowerCase()
      if (![e.trk, e.actor, e.object, e.type, e.result, e.destination ?? ''].some((x) => x.toLowerCase().includes(s))) return false
    }
    if (type && e.type !== type) return false
    if (actor && e.actor !== actor) return false
    if (ws && e.workspaceId !== ws) return false
    if (result === 'ok' && e.severity !== 'ok') return false
    if (result === 'blocked' && e.severity !== 'blocked') return false
    if (result === 'other' && (e.severity === 'ok' || e.severity === 'blocked')) return false
    if (range === '1h' && now - e.at > 3_600_000) return false
    if (range === '24h' && now - e.at > 86_400_000) return false
    return true
  })

  const exportCsv = () => {
    const header = 'time,type,actor,object,destination,result,tracking_code\n'
    const body = filtered.map((e) => [new Date(e.at).toISOString(), e.type, e.actor, e.object, e.destination ?? '', e.result, e.trk].map((v) => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\n')
    const url = URL.createObjectURL(new Blob([header + body], { type: 'text/csv' }))
    const a = document.createElement('a')
    a.href = url
    a.download = `keyhole-audit${scopeLabel ? '-' + scopeLabel.toLowerCase().replace(/\s+/g, '-') : ''}.csv`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div>
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search — event, actor, or tracking code…"
          aria-label="Search the log"
          className="min-w-[180px] flex-1 rounded-lg border border-zinc-700 bg-panel px-3 py-2 text-[13px] text-zinc-100 outline-none placeholder:text-zinc-500 focus:border-zinc-500"
        />
        <Filter value={type} onChange={setType} label="Event type">
          {(['request', 'blocked', 'error', 'admin', 'support', 'connection', 'verification'] as const).map((t) => (
            <option key={t} value={t}>
              {t}
            </option>
          ))}
        </Filter>
        <Filter value={actor} onChange={setActor} label="Actor">
          {actors.map((a) => (
            <option key={a}>{a}</option>
          ))}
        </Filter>
        {!hideWorkspaceFilter && (
          <Filter value={ws} onChange={setWs} label="Workspace">
            {workspaces.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </Filter>
        )}
        <Filter value={result} onChange={setResult} label="Result">
          <option value="ok">Succeeded</option>
          <option value="blocked">Blocked</option>
          <option value="other">Errors and notices</option>
        </Filter>
        <Filter value={range} onChange={setRange} label="Any date">
          <option value="1h">Last hour</option>
          <option value="24h">Last 24 hours</option>
        </Filter>
        <button
          type="button"
          aria-pressed={live}
          onClick={() => setLive(!live)}
          className={cx(
            'inline-flex items-center gap-2 rounded-lg border px-3 py-2 text-sm2 font-semibold',
            live ? 'border-green-500/35 bg-green-500/[0.08] text-green-400' : 'border-edge bg-panel text-zinc-400 hover:border-zinc-700',
          )}
        >
          <span className={cx('size-[7px] rounded-full', live ? 'animate-khpulse-fast bg-green-500' : 'bg-zinc-600')} />
          Live
        </button>
        <button type="button" onClick={exportCsv} className="rounded-lg border border-edge bg-panel px-3 py-2 text-sm2 text-zinc-400 hover:border-zinc-700 hover:text-zinc-200">
          Export
        </button>
      </div>

      <div className="mt-3.5 overflow-hidden rounded-[10px] border border-edge bg-panel [&>*:last-child]:border-b-0">
        {state === 'loading' ? (
          <SkeletonRows cols="64px 8px 70px 1fr 90px 100px" n={5} />
        ) : state === 'error' ? (
          <div className="p-3">
            <ErrorBox what="the activity log" onRetry={retry} />
          </div>
        ) : filtered.length === 0 ? (
          <div className="p-10 text-center text-[13px] text-zinc-400">
            {events.length === 0 ? 'No activity yet. Requests appear here the moment your first call lands.' : 'Nothing matches these filters. Clear the search or pick a wider date range.'}
          </div>
        ) : (
          filtered.slice(0, 200).map((e) => (
            <LogRow
              key={e.id}
              e={e}
              fresh={!seen.has(e.id)}
              expanded={expanded.has(e.id)}
              onToggle={() => {
                const n = new Set(expanded)
                if (n.has(e.id)) n.delete(e.id)
                else n.add(e.id)
                setExpanded(n)
              }}
            />
          ))
        )}
      </div>
      <div className="mt-2.5 text-xs2 text-zinc-600">
        Logs kept 90 days on this plan.
        {role === 'user' && ' You see activity in your workspaces and your own actions.'}
      </div>
    </div>
  )
}

export function UserChip({ name }: { name: string }) {
  return (
    <span className="inline-flex items-center gap-2">
      <Avatar initials={initials(name)} />
      <span className="text-xs text-zinc-500">{name}</span>
    </span>
  )
}
