import { Fragment, useEffect, useMemo, useState, type ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { ago, clock, expiringSoon, initials, maskToken, until } from '../lib/format'
import { liveTick } from '../lib/simulate'
import { actions, agentsCreatedBy, isActive, isAdmin, isAdminRole, isDemotion, isLastOwner, isSuspended, lastActiveInOrg, myRole, org, orgAdmins, orgWorkspaces, useDB, useNow, workspaceAdmins, wsById } from '../lib/store'
import type { Agent, AuditEvent, Role, User, Workspace } from '../lib/types'
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
  type MenuItem,
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
/*
 * Players grids, shared with Dispatch (README › Shared with Dispatch): same columns, same ⋯ items in the
 * same order. Unavailable items stay in the menu, aria-disabled, with the reason.
 */
export const USER_COLS = '1.4fr 1.7fr 0.9fr 0.9fr 0.95fr 1.6fr 36px'
const USER_COLS_WS = '1.3fr 1.5fr 0.85fr 0.85fr 0.9fr 1.4fr 1.15fr 36px'

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

/** Member, Workspace admin, or Default admin (org) — the workspace Role column. */
function workspaceRoleLabel(role: Role, userId: string, w: Workspace) {
  if (isAdminRole(role)) return 'Default admin (org)'
  return w.adminIds.includes(userId) ? 'Workspace admin' : 'Member'
}

/** "Production (admin), Staging, Incidents +2" — explicit memberships; org admins cover every workspace. */
function workspaceList(d: ReturnType<typeof useDB>, u: User, role: Role) {
  if (isAdminRole(role)) return 'All (org admin)'
  const names = d.workspaces.filter((w) => w.orgId === d.currentOrgId && w.userIds.includes(u.id)).map((w) => `${w.name}${w.adminIds.includes(u.id) ? ' (admin)' : ''}`)
  if (!names.length) return '—'
  return names.length > 3 ? `${names.slice(0, 3).join(', ')} +${names.length - 3}` : names.join(', ')
}

export function UsersTable({ rows, className, ws }: { rows: UserRow[]; className?: string; ws?: Workspace }) {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const admin = isAdmin(d)
  const [roleFor, setRoleFor] = useState<UserRow | null>(null)
  const [wsFor, setWsFor] = useState<UserRow | null>(null)
  const [removeFor, setRemoveFor] = useState<UserRow | null>(null)
  const [suspendFor, setSuspendFor] = useState<UserRow | null>(null)
  const [resumeFor, setResumeFor] = useState<UserRow | null>(null)
  const [transferTo, setTransferTo] = useState<User | null>(null)
  const owner = myRole(d) === 'Owner'
  const cols = ws ? USER_COLS_WS : USER_COLS
  const invitedWs = (u: User) => {
    const inv = d.invites.find((i) => i.email === u.email && i.orgId === d.currentOrgId)
    return inv?.workspaceIds.map((id) => `${wsById(d, id)?.name}${inv.adminWorkspaceIds?.includes(id) ? ' (admin)' : ''}`).join(', ') || '—'
  }

  const cabinetsManaged = removeFor ? d.cabinets.filter((c) => c.managedBy === removeFor.user.id && d.workspaces.some((w) => w.id === c.workspaceId && w.orgId === d.currentOrgId)) : []

  /** Why the current person can't manage this member, or null when they can. */
  const blocked = (row: UserRow, action: 'role' | 'suspend' | 'remove'): string | null => {
    const { user: u, role } = row
    const self = u.id === d.currentUserId
    if (role === 'Owner' && !owner) return 'Only Owners manage Owners'
    if (self && action !== 'role') return 'Not for yourself'
    if (self && !owner) return 'Not for yourself'
    if (role === 'Owner' && isLastOwner(d, u.id)) return 'Last active Owner — transfer ownership first'
    return null
  }
  const menuFor = (row: UserRow): (MenuItem | 'separator' | null)[] => {
    const { user: u, role } = row
    const self = u.id === d.currentUserId
    const suspended = isSuspended(u, d.currentOrgId)
    const transferReason = !owner ? 'Owners only' : self ? 'That’s you' : role === 'Owner' ? 'Already an Owner' : !isActive(u, d.currentOrgId) ? 'Not active in this organization' : null
    return [
      { label: 'Change org role…', onClick: () => setRoleFor(row), disabled: !!blocked(row, 'role'), reason: blocked(row, 'role') ?? undefined },
      { label: 'Assign workspaces…', onClick: () => setWsFor(row), disabled: isAdminRole(role), reason: 'Org admins administer every workspace' },
      { label: 'Transfer ownership…', onClick: () => setTransferTo(u), disabled: !!transferReason, reason: transferReason ?? undefined },
      suspended
        ? { label: 'Resume…', onClick: () => setResumeFor(row), disabled: !!blocked(row, 'suspend'), reason: blocked(row, 'suspend') ?? undefined }
        : { label: 'Suspend…', onClick: () => setSuspendFor(row), disabled: !!blocked(row, 'suspend'), reason: blocked(row, 'suspend') ?? undefined },
      { label: 'Remove from organization…', danger: true, onClick: () => setRemoveFor(row), disabled: !!blocked(row, 'remove'), reason: blocked(row, 'remove') ?? undefined },
      // Keyhole only, after the shared items.
      'separator',
      u.locked ? { label: u.unlockRequest ? 'Unlock requested' : 'Ask support to unlock', disabled: !!u.unlockRequest, reason: 'Already asked', onClick: () => actions.requestUnlock(u.id) } : null,
    ]
  }

  return (
    <>
      <Table cols={cols} head={['Name', 'Email', 'Org role', 'Status', 'Last active (this org)', 'Workspaces', ...(ws ? ['Role'] : []), '']} className={className}>
        <ListBody cols={cols} what="users" empty={rows.length ? undefined : <div className="p-8 text-center text-[13px] text-zinc-400">No one here yet. Invite a teammate to share this workspace.</div>}>
          {rows.map(({ user: u, role, invited }) => (
            <Row key={u.id} cols={cols} onClick={invited ? undefined : () => nav(`/players/users/${u.id}`)}>
              <div className={cx('truncate font-medium', invited && 'text-zinc-400')}>{invited ? u.email : u.name}</div>
              <div className="truncate text-zinc-400">{u.email}</div>
              <div className="text-zinc-400">{role}</div>
              <div>
                {u.locked ? (
                  <StatusInline tone="amber">Locked</StatusInline>
                ) : invited ? (
                  <StatusInline tone="gray">Invited</StatusInline>
                ) : isSuspended(u, d.currentOrgId) ? (
                  <StatusInline tone="amber">Suspended</StatusInline>
                ) : (
                  <StatusInline tone="green">Active</StatusInline>
                )}
              </div>
              <div className="text-zinc-500">{invited ? '—' : u.id === d.currentUserId ? 'Now' : ago(lastActiveInOrg(d, u.id), now)}</div>
              <div className="truncate text-zinc-400">{invited ? invitedWs(u) : workspaceList(d, u, role)}</div>
              {ws && <div className={cx(workspaceRoleLabel(role, u.id, ws) === 'Member' ? 'text-zinc-400' : 'text-zinc-200')}>{invited ? '—' : workspaceRoleLabel(role, u.id, ws)}</div>}
              <div className="text-right">{admin && !invited && <Menu items={menuFor({ user: u, role, invited })} />}</div>
            </Row>
          ))}
        </ListBody>
      </Table>

      <ImpactDialog
        open={!!suspendFor}
        onClose={() => setSuspendFor(null)}
        title={`Suspend ${suspendFor?.user.name}?`}
        rows={[
          ['Workspaces', suspendFor ? workspaceList(d, suspendFor.user, suspendFor.role) : ''],
          ['Agents they created', suspendFor ? createdAgentsLabel(agentsCreatedBy(d, suspendFor.user).map((a) => a.label)) : ''],
          ['Last active (this org)', ago(suspendFor ? lastActiveInOrg(d, suspendFor.user.id) : null, now)],
        ]}
        body="They can’t use this organization until you resume them; their other organizations aren’t affected. Nothing they created or did changes: agents, grants, keys and cabinets keep working."
        confirmLabel="Suspend user"
        onConfirm={() => suspendFor && actions.setUserSuspended(suspendFor.user.id, true)}
      />
      <Modal open={!!resumeFor} onClose={() => setResumeFor(null)} title={`Resume ${resumeFor?.user.name}?`} width={440}>
        <div className="text-sm2 text-zinc-400">They can use this organization again right away, with the workspaces and roles they had.</div>
        <Footer>
          <Button size="lg" onClick={() => setResumeFor(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            onClick={() => {
              if (resumeFor) actions.setUserSuspended(resumeFor.user.id, false)
              setResumeFor(null)
            }}
          >
            Resume user
          </Button>
        </Footer>
      </Modal>
      <ChangeRoleModal row={roleFor} onClose={() => setRoleFor(null)} />
      <TransferOwnershipDialog open={!!transferTo} to={transferTo} onClose={() => setTransferTo(null)} />
      <AssignWorkspacesModal row={wsFor} onClose={() => setWsFor(null)} />
      <ImpactDialog
        open={!!removeFor}
        onClose={() => setRemoveFor(null)}
        title={`Remove ${removeFor?.user.name} from the organization?`}
        rows={[
          ['Workspaces', removeFor ? workspaceList(d, removeFor.user, removeFor.role) : ''],
          ['Cabinets they manage', cabinetsManaged.length ? `${cabinetsManaged.map((c) => c.name).join(', ')} — keep working; admins take over management` : 'None'],
          ['Agents they created', removeFor ? createdAgentsLabel(agentsCreatedBy(d, removeFor.user).map((a) => a.label)) : ''],
          ['Last active (this org)', ago(removeFor ? lastActiveInOrg(d, removeFor.user.id) : null, now)],
        ]}
        body="They lose access to every workspace in this organization. Nothing they created or did changes — tools they granted stay granted, keys and vault connectors they added stay — and the audit log keeps their name on it."
        confirmLabel="Remove user"
        onConfirm={() => removeFor && actions.removeUser(removeFor.user.id)}
      />
    </>
  )
}

const ROLE_HINT: Record<Role, string> = {
  Owner: 'Everything a userAdmin can do, plus renaming or deleting the organization and managing other Owners.',
  userAdmin: 'Administers every workspace: invites people, manages stores, workspaces, agents and tools.',
  user: 'Sees only the workspaces they’re added to and manages the cabinets assigned to them.',
}

function ChangeRoleModal({ row, onClose }: { row: UserRow | null; onClose: () => void }) {
  const d = useDB()
  const [role, setRole] = useState<Role>('user')
  useEffect(() => {
    if (row) setRole(row.role)
  }, [row])
  const owner = myRole(d) === 'Owner'
  const demoting = !!row && isDemotion(row.role, role)
  // Who administers every workspace once this change is made: active admins only. Rule 1 keeps an active Owner.
  const adminsAfter = orgAdmins(d)
    .map((u) => ({ u, r: u.id === row?.user.id ? role : u.roles[d.currentOrgId] }))
    .filter((x) => isAdminRole(x.r))
  const ownerAfter = adminsAfter.some((x) => x.r === 'Owner')
  const explicit = row ? orgWorkspaces(d).filter((w) => w.userIds.includes(row.user.id)).map((w) => w.name) : []
  // Demoted to user, they stop managing cabinets in workspaces they aren't a member of; admins take over.
  const released =
    row && role === 'user'
      ? d.cabinets.filter((c) => c.managedBy === row.user.id && d.workspaces.some((w) => w.id === c.workspaceId && w.orgId === d.currentOrgId && !w.userIds.includes(row.user.id))).map((c) => c.name)
      : []
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
            [
              'Workspace admins after this',
              `${adminsAfter.map((x) => `${x.u.name} (${x.r})`).join(', ') || 'None'} — ${ownerAfter ? 'every workspace keeps an active Owner' : 'no active Owner until Keyhole support unlocks one'}`,
              ownerAfter ? undefined : 'amber',
            ],
            ['Cabinets they manage', released.length ? `${released.join(', ')} — keep working; admins take over management` : 'Unaffected'],
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
  // Existing Owners already have ownership; transferring to them would only demote you.
  const candidates = d.users.filter((u) => u.id !== d.currentUserId && u.roles[d.currentOrgId] && u.roles[d.currentOrgId] !== 'Owner' && isActive(u, d.currentOrgId))
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
          <span>You can’t undo this yourself: after it, only an Owner can make you an Owner again. The change is recorded in the audit log.</span>
        </div>
      }
      confirmLabel="Transfer ownership"
      confirmDisabled={!target}
      onConfirm={() => target && actions.transferOwnership(target.id)}
    />
  )
}

type WsRole = 'member' | 'admin'

/**
 * Players › Assign workspaces, shared with Dispatch: each workspace gets a checkbox and a Member /
 * Workspace admin role. This is how org admins delegate workspace admin to a regular user.
 */
function AssignWorkspacesModal({ row, onClose }: { row: UserRow | null; onClose: () => void }) {
  const d = useDB()
  const workspaces = orgWorkspaces(d)
  const [sel, setSel] = useState<Record<string, WsRole>>({})
  useEffect(() => {
    if (row) setSel(Object.fromEntries(workspaces.filter((w) => w.userIds.includes(row.user.id)).map((w) => [w.id, w.adminIds.includes(row.user.id) ? 'admin' : 'member'])))
  }, [row]) // eslint-disable-line
  const id = row?.user.id ?? ''
  const before = (w: Workspace): WsRole | null => (w.adminIds.includes(id) ? 'admin' : w.userIds.includes(id) ? 'member' : null)
  const label = (r: WsRole | null) => (r === 'admin' ? 'workspace admin' : 'member')
  const added = workspaces.filter((w) => !before(w) && sel[w.id])
  const removed = workspaces.filter((w) => before(w) && !sel[w.id])
  const changed = workspaces.filter((w) => before(w) && sel[w.id] && before(w) !== sel[w.id])
  // Where they're the only explicit workspace admin and stop being one, the org admins take over by default.
  const takeover = workspaces.filter((w) => before(w) === 'admin' && sel[w.id] !== 'admin' && workspaceAdmins(d, w).users.every((u) => u.id === id) && !workspaceAdmins(d, w).byDefault)
  const released = d.cabinets.filter((c) => c.managedBy === id && removed.some((w) => w.id === c.workspaceId)).map((c) => c.name)
  const reducing = removed.length > 0 || changed.some((w) => sel[w.id] === 'member')
  const any = added.length + removed.length + changed.length > 0
  return (
    <Modal open={!!row} onClose={onClose} title={`Assign workspaces for ${row?.user.name}`} width={520}>
      <div className="flex flex-col rounded-lg border border-edge bg-page">
        {workspaces.map((w) => (
          <div key={w.id} className="flex items-center justify-between gap-3 border-b border-line px-3 py-2 last:border-b-0">
            <Checkbox checked={!!sel[w.id]} onChange={(v) => setSel(v ? { ...sel, [w.id]: 'member' } : Object.fromEntries(Object.entries(sel).filter(([k]) => k !== w.id)))} label={w.name} />
            <select
              aria-label={`Role in ${w.name}`}
              disabled={!sel[w.id]}
              value={sel[w.id] ?? 'member'}
              onChange={(e) => setSel({ ...sel, [w.id]: e.target.value as WsRole })}
              className="rounded-md border border-edge bg-panel px-2 py-1 text-xs text-zinc-300 outline-none focus:border-zinc-500 disabled:opacity-40"
            >
              <option value="member">Member</option>
              <option value="admin">Workspace admin</option>
            </select>
          </div>
        ))}
      </div>
      <div className="-mt-1 text-xs text-zinc-500">A workspace admin manages that workspace’s members, exposed keys, tool grants, connection methods, cabinets and connectors — nothing org-level.</div>
      {any && (
        <ImpactRows
          rows={[
            ['Added', added.map((w) => `${w.name} (${label(sel[w.id])})`).join(', ') || 'None'],
            ['Removed', removed.map((w) => w.name).join(', ') || 'None', removed.length ? 'amber' : undefined],
            ['Role changes', changed.map((w) => `${w.name}: ${label(before(w))} → ${label(sel[w.id])}`).join('; ') || 'None', changed.some((w) => sel[w.id] === 'member') ? 'amber' : undefined],
            ['Default admins take over', takeover.length ? `${takeover.map((w) => w.name).join(', ')} — org admins, by default` : 'None'],
            ['Cabinets they manage there', released.length ? `${released.join(', ')} — keep working; admins take over management` : 'None'],
          ]}
        />
      )}
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant={reducing ? 'danger' : 'primary'}
          disabled={!any}
          onClick={() => {
            if (row) actions.setUserWorkspaces(row.user.id, sel)
            onClose()
          }}
        >
          Apply changes
        </Button>
      </Footer>
    </Modal>
  )
}

/** Players › Agents › Assign workspaces. Agents are members; in Keyhole they don't hold workspace admin. */
function AssignAgentWorkspacesModal({ agent, onClose }: { agent: Agent | null; onClose: () => void }) {
  const d = useDB()
  const workspaces = orgWorkspaces(d)
  // Remounted per agent (keyed by its id), so the selection starts from its current workspaces.
  const [sel, setSel] = useState<string[]>(() => [...(agent?.workspaceIds ?? [])])
  const added = workspaces.filter((w) => sel.includes(w.id) && !agent?.workspaceIds.includes(w.id))
  const removed = workspaces.filter((w) => !sel.includes(w.id) && agent?.workspaceIds.includes(w.id))
  const sidecars = d.connectors.filter((c) => c.kind === 'sidecar' && c.agentId === agent?.id && removed.some((w) => w.id === c.workspaceId)).map((c) => c.name)
  return (
    <Modal open={!!agent} onClose={onClose} title={`Assign workspaces for ${agent?.label}`} width={480}>
      <div className="flex flex-col gap-2 rounded-lg border border-edge bg-page p-3">
        {workspaces.map((w) => (
          <Checkbox key={w.id} checked={sel.includes(w.id)} onChange={(v) => setSel(v ? [...sel, w.id] : sel.filter((x) => x !== w.id))} label={w.name} />
        ))}
      </div>
      {added.length + removed.length > 0 && (
        <ImpactRows
          rows={[
            ['Added', added.map((w) => w.name).join(', ') || 'None'],
            ['Removed', removed.map((w) => w.name).join(', ') || 'None', removed.length ? 'amber' : undefined],
            ['Sidecars acting as it there', sidecars.length ? `${sidecars.join(', ')} — refused from the next request` : 'None', sidecars.length ? 'amber' : undefined],
          ]}
        />
      )}
      <div className="text-xs text-zinc-500">Its token stays the same: it can call a workspace’s tools from its next request.</div>
      <Footer>
        <Button size="lg" onClick={onClose}>
          Cancel
        </Button>
        <Button
          size="lg"
          variant={removed.length ? 'danger' : 'primary'}
          disabled={!added.length && !removed.length}
          onClick={() => {
            if (agent) actions.setAgentWorkspaces(agent.id, sel)
            onClose()
          }}
        >
          Apply changes
        </Button>
      </Footer>
    </Modal>
  )
}

/* ------------------------------------------------------------------ */
/* The standard Agents table — identical wherever it appears.          */
/* ------------------------------------------------------------------ */
export const AGENT_COLS = '1.25fr 1fr 0.85fr 1.15fr 1.1fr 0.9fr 1.3fr 0.8fr 0.7fr 36px'
const AGENT_COLS_WS = '1.2fr 0.95fr 0.8fr 1.1fr 1.05fr 0.85fr 1.2fr 0.75fr 0.65fr 0.7fr 36px'

export function AgentsTable({ agents, className, emptyText, ws }: { agents: Agent[]; className?: string; emptyText?: ReactNode; ws?: Workspace }) {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const admin = isAdmin(d)
  const [editing, setEditing] = useState<string | null>(null)
  const [rotated, setRotated] = useState<{ agent: Agent; token: string } | null>(null)
  const [rotateFor, setRotateFor] = useState<Agent | null>(null)
  const [suspendFor, setSuspendFor] = useState<Agent | null>(null)
  const [resumeFor, setResumeFor] = useState<Agent | null>(null)
  const [revokeFor, setRevokeFor] = useState<Agent | null>(null)
  const [assignFor, setAssignFor] = useState<Agent | null>(null)
  const cols = ws ? AGENT_COLS_WS : AGENT_COLS
  const wsNames = (a: Agent) => {
    const names = a.workspaceIds.map((id) => wsById(d, id)?.name).filter(Boolean) as string[]
    return names.length > 3 ? `${names.slice(0, 3).join(', ')} +${names.length - 3}` : names.join(', ') || '—'
  }
  const lockLists = (a: Agent) => d.cabinets.filter((c) => Array.isArray(c.access) && c.access.some((p) => p.kind === 'agent' && p.id === a.id)).map((c) => c.name)
  const menuFor = (a: Agent): (MenuItem | 'separator' | null)[] => {
    const revoked = a.status === 'revoked' ? 'Revoked — create a new agent instead' : undefined
    return [
      { label: 'Assign workspaces…', onClick: () => setAssignFor(a), disabled: !!revoked, reason: revoked },
      { label: 'Rotate token…', onClick: () => setRotateFor(a), disabled: !!revoked, reason: revoked },
      a.status === 'suspended' ? { label: 'Resume…', onClick: () => setResumeFor(a) } : { label: 'Suspend…', onClick: () => setSuspendFor(a), disabled: !!revoked, reason: revoked },
      { label: 'Revoke…', danger: true, onClick: () => setRevokeFor(a), disabled: !!revoked, reason: revoked ? 'Already revoked' : undefined },
    ]
  }

  return (
    <>
      <Table cols={cols} head={['Label', 'Agent ID', 'Status', 'Token', 'Created', 'Last used', 'Workspaces', 'Expiry', 'Rate limit', ...(ws ? ['Role'] : []), '']} className={className}>
        <ListBody cols={cols} what="agents" empty={agents.length ? undefined : <div className="p-10 text-center text-[13px] text-zinc-400">{emptyText ?? 'No agents yet. Create one to let an AI assistant or program connect.'}</div>}>
          {agents.map((a) => {
            const revoked = a.status === 'revoked'
            const soon = !revoked && expiringSoon(a.expiresAt, now)
            return (
              <Row key={a.id} cols={cols} onClick={() => nav(`/players/agents/${a.id}`)} className={cx(revoked && 'text-zinc-500')}>
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
                          className="text-2xs text-zinc-600 opacity-0 group-hover:opacity-100 focus:opacity-100 hover:text-zinc-300"
                        >
                          ✎
                        </button>
                      )}
                    </span>
                  )}
                </div>
                <div className="truncate font-mono text-xs text-zinc-500">{a.id}</div>
                <div>
                  {a.status === 'active' ? <StatusInline tone="green">Active</StatusInline> : a.status === 'suspended' ? <StatusInline tone="amber">Suspended</StatusInline> : <StatusInline tone="gray">Revoked</StatusInline>}
                </div>
                {/* Masked only, and nothing to copy: the full token was shown once, at creation. */}
                <div className={cx('masked-token truncate text-xs', revoked ? 'text-zinc-600' : 'text-zinc-400')}>{maskToken(a.tokenLast4)}</div>
                <div className="truncate text-zinc-400">
                  {a.createdBy === d.users.find((u) => u.id === d.currentUserId)?.name ? 'You' : a.createdBy} · {ago(a.createdAt, now).toLowerCase()}
                </div>
                <div className="text-zinc-500">{ago(a.lastUsedAt, now)}</div>
                <div className="truncate text-zinc-400">{wsNames(a)}</div>
                <div className={cx(soon ? 'font-medium text-amber-400' : 'text-zinc-400', revoked && '!text-zinc-600')}>{revoked ? '—' : a.expiresAt ? until(a.expiresAt, now) : 'None'}</div>
                <div className="text-zinc-400">{a.rateLimit ? `${a.rateLimit}/min` : '—'}</div>
                {ws && <div className="text-zinc-400">Member</div>}
                <div className="text-right">{admin && <Menu items={menuFor(a)} />}</div>
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
      <Modal open={!!resumeFor} onClose={() => setResumeFor(null)} title={`Resume ${resumeFor?.label}?`} width={440}>
        <div className="text-sm2 text-zinc-400">Its next request is accepted again, with the same token and workspaces. Sidecars acting as it work again too.</div>
        <Footer>
          <Button size="lg" onClick={() => setResumeFor(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            onClick={() => {
              if (resumeFor) actions.setAgentStatus(resumeFor.id, 'active')
              setResumeFor(null)
            }}
          >
            Resume agent
          </Button>
        </Footer>
      </Modal>
      <AssignAgentWorkspacesModal key={assignFor?.id ?? 'none'} agent={assignFor} onClose={() => setAssignFor(null)} />
      <RotateAgentDialog agent={rotateFor} onClose={() => setRotateFor(null)} onRotated={(agent, token) => setRotated({ agent, token })} />
      <SuspendAgentDialog agent={suspendFor} onClose={() => setSuspendFor(null)} />
      <RevokeAgentDialog agent={revokeFor} onClose={() => setRevokeFor(null)} lockLists={revokeFor ? lockLists(revokeFor) : []} />
    </>
  )
}

/** Sidecars bound to an agent: they act as it, so they stop working with it. */
const sidecarsOf = (d: ReturnType<typeof useDB>, a: Agent | null) =>
  a ? d.connectors.filter((c) => c.kind === 'sidecar' && c.agentId === a.id).map((c) => c.name).join(', ') : ''

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
      onConfirm={() => {
        const token = agent && actions.rotateAgent(agent.id)
        if (agent && token) onRotated(agent, token)
      }}
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
        ['Sidecars acting as it', sidecarsOf(d, agent) || 'None', sidecarsOf(d, agent) ? 'amber' : undefined],
      ]}
      body="Its requests are blocked and logged until you resume it, including requests through its sidecars. The token stays the same, so resuming needs no config change."
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
        ['Sidecars acting as it', sidecarsOf(d, agent) || 'None', sidecarsOf(d, agent) ? 'amber' : undefined],
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
        {/* Org, workspace and player events both apps show. */}
        {e.shared && (
          <span title="Shared with Dispatch" className="shrink-0 rounded-full border border-chip px-2 py-0.5 text-2xs font-semibold text-zinc-400">
            Shared{e.source ? ` · ${e.source}` : ''}
          </span>
        )}
        <span className="flex min-w-0 items-center gap-2">
          <span className="shrink-0 text-zinc-300">
            {e.actor}
            {e.via && <span className="text-zinc-500"> via sidecar {e.via}</span>}
          </span>
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
      if (![e.trk, e.actor, e.via ? `via sidecar ${e.via}` : '', e.object, e.type, e.result, e.destination ?? ''].some((x) => x.toLowerCase().includes(s))) return false
    }
    if (type === 'shared' ? !e.shared : type && e.type !== type) return false
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
    const header = 'time,type,actor,via_sidecar,object,destination,result,tracking_code\n'
    const body = filtered.map((e) => [new Date(e.at).toISOString(), e.type, e.actor, e.via ?? '', e.object, e.destination ?? '', e.result, e.trk].map((v) => `"${String(v).replace(/"/g, '""')}"`).join(',')).join('\n')
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
          <option value="shared">Shared with Dispatch</option>
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
        Logs kept 90 days on this plan. Shows Keyhole’s events plus the org, workspace and player events shared with Dispatch (marked Shared).
        {role === 'user' && ' You see activity in your workspaces and your own actions.'}
      </div>
    </div>
  )
}

/**
 * The standard state for a record in an organization the viewer can't use. Records in organizations the
 * viewer belongs to are switched to before the page renders (see Routed), so this means "not yours".
 */
export function NoAccess({ what, to, back }: { what: string; to: string; back: string }) {
  return (
    <div className="text-sm text-zinc-400">
      You don’t have access to this {what}. <Link to={to}>{back}</Link>
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
