import { useEffect, useState, useSyncExternalStore } from 'react'
import { CATALOG } from './catalog'
import { DAY, maskToken, newConnectorToken, newToken, plural, trackingCode, uid } from './format'
import { freshDB, populatedDB } from './seed'
import type {
  Agent,
  AuditEvent,
  Cabinet,
  Connector,
  DB,
  Key,
  PlayerRef,
  Role,
  SidecarProtocol,
  SecretStore,
  Tool,
  ToolSnapshot,
  User,
  Workspace,
  WorkspaceTool,
} from './types'

const LS_KEY = 'keyhole-mocks-v1'
const VERSION = 7

function load(): DB {
  try {
    const raw = localStorage.getItem(LS_KEY)
    if (raw) {
      const parsed = JSON.parse(raw)
      if (parsed.v === VERSION) return parsed.db as DB
    }
  } catch {
    /* storage unavailable — fall back to seed */
  }
  return populatedDB()
}

let db: DB = load()
const listeners = new Set<() => void>()

function save() {
  try {
    localStorage.setItem(LS_KEY, JSON.stringify({ v: VERSION, db }))
  } catch {
    /* ignore */
  }
}

export function getDB() {
  return db
}

export function update(recipe: (draft: DB) => void) {
  const draft = structuredClone(db)
  recipe(draft)
  db = draft
  save()
  listeners.forEach((l) => l())
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

export function useDB(): DB {
  return useSyncExternalStore(subscribe, getDB)
}

/** A ticking clock so relative times ("2 minutes ago") stay honest. */
export function useNow(ms = 15_000) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms)
    return () => clearInterval(t)
  }, [ms])
  return now
}

/* ------------------------------------------------------------------ */
/* Session-only secrets. Full agent tokens live here for this browser  */
/* session only so a config download can include them; never persisted */
/* and never rendered outside the one-time token panel.                */
/* ------------------------------------------------------------------ */
const sessionTokens = new Map<string, string>()
export const sessionTokenFor = (agentId: string) => sessionTokens.get(agentId) ?? null

/* ------------------------------------------------------------------ */
/* Selectors                                                           */
/* ------------------------------------------------------------------ */
export const me = (d: DB) => d.users.find((u) => u.id === d.currentUserId)!
export const myRole = (d: DB): Role | 'superAdmin' | null =>
  d.currentUserId === 'support' ? 'superAdmin' : (me(d)?.roles[d.currentOrgId] ?? null)
export const isAdmin = (d: DB) => {
  const r = myRole(d)
  return (r === 'Owner' || r === 'userAdmin') && isActive(me(d), d.currentOrgId)
}
export const org = (d: DB, id = d.currentOrgId) => d.orgs.find((o) => o.id === id)

/* Permission model (README › Permission model). Roles are checked when an action happens. */
const RANK: Record<Role, number> = { user: 0, userAdmin: 1, Owner: 2 }
export const isAdminRole = (r: Role | null | undefined) => r === 'Owner' || r === 'userAdmin'
/** Owners and userAdmins: they administer every workspace in the organization (rule 1). */
/** Suspended or support-locked people can't act, so they never count towards rules 1 and 2. */
export const isSuspended = (u: User | undefined, orgId: string) => !!u?.suspended?.[orgId]
/** Can this person act in this organization: signed up, not locked by support, not suspended there. */
export const isActive = (u: User | undefined, orgId: string) => !!u && u.status === 'active' && !u.locked && !isSuspended(u, orgId)
/** Active Owners and userAdmins: they administer every workspace in the organization (rule 1). */
export const orgAdmins = (d: DB) => d.users.filter((u) => isAdminRole(u.roles[d.currentOrgId]) && isActive(u, d.currentOrgId))
export const activeOwners = (d: DB) => d.users.filter((u) => u.roles[d.currentOrgId] === 'Owner' && isActive(u, d.currentOrgId))
/**
 * An Owner with no other active Owner beside them. Demoting, suspending or removing them would leave
 * the organization with nobody able to manage Owners, so it's blocked (transfer ownership instead).
 */
export const isLastOwner = (d: DB, userId: string) =>
  d.users.find((u) => u.id === userId)?.roles[d.currentOrgId] === 'Owner' && !activeOwners(d).some((u) => u.id !== userId)
export const isDemotion = (from: Role, to: Role) => RANK[to] < RANK[from]
/**
 * Whether the current person may change this member's role, suspend or remove them:
 * admins manage members, only Owners manage Owners, and the last Owner is never removed or demoted.
 */
export function canManageMember(d: DB, userId: string) {
  const r = d.users.find((u) => u.id === userId)?.roles[d.currentOrgId]
  if (!isAdmin(d) || !r || userId === d.currentUserId) return false
  return r !== 'Owner' || myRole(d) === 'Owner'
}
/**
 * Whether a person can use a workspace right now. Org admins are every workspace's admins by default
 * (rule 2), so they don't need to be listed on it.
 */
export function hasWorkspaceAccess(d: DB, userId: string | null | undefined, wsId: string) {
  const u = d.users.find((x) => x.id === userId)
  const w = d.workspaces.find((x) => x.id === wsId)
  const r = w && u?.roles[w.orgId]
  return !!r && isActive(u, w!.orgId) && (isAdminRole(r) || w!.userIds.includes(u!.id))
}
/*
 * Records belong to one organization. Record pages switch to that organization first (Routed uses
 * recordOrgFromPath), so an action whose record isn't in the actor's current organization is refused.
 * Permission is therefore always checked in the record's own organization, and log() writes there.
 */
const inMyOrg = (d: DB, orgId: string | undefined) => !!orgId && orgId === d.currentOrgId && isActive(me(d), orgId)
const adminIn = (d: DB, orgId: string | undefined) => inMyOrg(d, orgId) && isAdmin(d)
/** Admins manage every cabinet; a member manages the ones assigned to them while they can use the workspace. */
function canManageCabinet(d: DB, c: Cabinet) {
  const w = d.workspaces.find((x) => x.id === c.workspaceId)
  return !!w && inMyOrg(d, w.orgId) && (canAdminWorkspace(d, w.id) || (c.managedBy === d.currentUserId && hasWorkspaceAccess(d, d.currentUserId, w.id)))
}

/** Adds or removes an agent from a workspace, keeping both sides in step, with one shared log row. */
function applyAgentMembership(d: DB, w: Workspace, agentId: string, member: boolean) {
  const a = d.agents.find((x) => x.id === agentId)
  if (!a || a.orgId !== w.orgId || w.agentIds.includes(a.id) === member) return
  w.agentIds = member ? [...w.agentIds, a.id] : w.agentIds.filter((x) => x !== a.id)
  a.workspaceIds = member ? [...a.workspaceIds, w.id] : a.workspaceIds.filter((x) => x !== w.id)
  const sidecars = member ? [] : d.connectors.filter((c) => c.kind === 'sidecar' && c.agentId === a.id && c.workspaceId === w.id).map((c) => c.name)
  log(d, {
    object: member ? `Added ${a.label} to ${w.name}` : `Removed ${a.label} from ${w.name}`,
    workspaceId: w.id,
    shared: true,
    detail: [['Workspace access', member ? 'none → member' : 'member → none'], ...(sidecars.length ? [['Sidecars acting as it here', `${sidecars.join(', ')} — refused from the next request`] as [string, string]] : [])],
  })
}

/**
 * Workspace admins (rule 2). Keyhole's workspace-admin role manages one workspace: its members, the keys it
 * exposes, tool grants and slots, connection methods, cabinets, and connectors enrolled to it. It never
 * reaches org-level things: stores, creating keys, tools or agents, or org settings.
 */
export const isExplicitWorkspaceAdmin = (d: DB, userId: string, w: Workspace) => w.adminIds.includes(userId) && hasWorkspaceAccess(d, userId, w.id)
/** Who administers a workspace: its explicit workspace admins, or — when there are none — the org's active admins. */
export function workspaceAdmins(d: DB, w: Workspace) {
  const explicit = d.users.filter((u) => isExplicitWorkspaceAdmin(d, u.id, w))
  return explicit.length ? { users: explicit, byDefault: false } : { users: d.users.filter((u) => isAdminRole(u.roles[w.orgId]) && isActive(u, w.orgId)), byDefault: true }
}
/** Org admins administer every workspace (rule 1); a workspace admin administers theirs. */
export function canAdminWorkspace(d: DB, wsId: string) {
  const w = d.workspaces.find((x) => x.id === wsId)
  if (!w || !inMyOrg(d, w.orgId)) return false
  return isAdmin(d) || isExplicitWorkspaceAdmin(d, d.currentUserId, w)
}
const wsAdminIn = (d: DB, w: Workspace | undefined) => !!w && canAdminWorkspace(d, w.id)

/**
 * One membership change, written the same whichever path made it (Players › Assign workspaces or the
 * workspace's Members tab). role null removes. Org admins are admins by default and aren't changed here.
 */
function applyMembership(d: DB, w: Workspace, userId: string, role: 'member' | 'admin' | null) {
  const u = d.users.find((x) => x.id === userId)
  if (!u?.roles[w.orgId] || isAdminRole(u.roles[w.orgId])) return
  const before: 'member' | 'admin' | null = w.adminIds.includes(userId) ? 'admin' : w.userIds.includes(userId) ? 'member' : null
  if (before === role) return
  const label = (r: 'member' | 'admin' | null) => (r === 'admin' ? 'workspace admin' : r === 'member' ? 'member' : 'none')
  const hadExplicit = workspaceAdmins(d, w).byDefault === false
  w.userIds = role ? Array.from(new Set([...w.userIds, userId])) : w.userIds.filter((x) => x !== userId)
  w.adminIds = role === 'admin' ? Array.from(new Set([...w.adminIds, userId])) : w.adminIds.filter((x) => x !== userId)
  const released = settleCabinetManagement(d)
  const takeover = hadExplicit && workspaceAdmins(d, w).byDefault
  const object = !before ? `Added ${u.name} to ${w.name} as ${label(role)}` : !role ? `Removed ${u.name} from ${w.name}` : `Made ${u.name} ${label(role)} of ${w.name}`
  log(d, {
    object,
    workspaceId: w.id,
    shared: true,
    detail: [
      ['Workspace role', `${label(before)} → ${label(role)}`],
      ...(takeover ? [['Workspace admins', 'Org admins, by default'] as [string, string]] : []),
      ...(released.length ? [['Cabinets now managed by admins', released.join(', ')] as [string, string]] : []),
    ],
  })
}

export const protocolList = (c: Connector) => (c.protocols ?? []).map((p) => p.toUpperCase()).join(' · ')
export const connectorKind = (c: Connector) => (c.kind === 'sidecar' ? 'sidecar' : 'vault connector')
/** Requests through a sidecar in the last 24 hours: the seeded baseline plus modelled log rows. */
export const sidecarRequests24h = (d: DB, c: Connector) => (c.requestsBase ?? 0) + d.events.filter((e) => e.viaId === c.id && e.type === 'request' && Date.now() - e.at < 86_400_000).length
/** The healthy sidecar an agent uses in a workspace, if any. */
export const sidecarFor = (d: DB, agentId: string, wsId: string) => d.connectors.find((c) => c.kind === 'sidecar' && c.agentId === agentId && c.workspaceId === wsId && c.health !== 'offline')

/** The organization of the record a URL points at (#/orgs/:id, /workspaces/:id, /tools/:id, /players/agents/:id). */
export function recordOrgFromPath(d: DB, path: string): string | null {
  const m = path.match(/^\/(orgs|workspaces|tools|players\/agents)\/([^/?#]+)/)
  if (!m) return null
  const [, kind, id] = m
  if (kind === 'orgs') return d.orgs.some((o) => o.id === id) ? id : null
  if (kind === 'workspaces') return d.workspaces.find((w) => w.id === id)?.orgId ?? null
  if (kind === 'tools') return d.tools.find((t) => t.id === id)?.orgId ?? null
  return d.agents.find((a) => a.id === id)?.orgId ?? null
}

/** Membership regardless of status: suspension pauses a person, it doesn't take their place away. */
function isMember(d: DB, userId: string, w: Workspace) {
  const r = d.users.find((u) => u.id === userId)?.roles[w.orgId]
  return !!r && (isAdminRole(r) || w.userIds.includes(userId))
}
/**
 * A manager who is no longer a member of a cabinet's workspace stops managing it: the cabinet keeps working
 * and org admins manage it. Being added back later doesn't restore management — an admin reassigns it.
 * Returns the cabinets released, for the log.
 */
function settleCabinetManagement(d: DB) {
  const released: string[] = []
  for (const c of d.cabinets) {
    const w = d.workspaces.find((x) => x.id === c.workspaceId)
    if (c.managedBy && w && !isMember(d, c.managedBy, w)) {
      c.managedBy = null
      released.push(c.name)
    }
  }
  return released
}
/** Everyone who can use a workspace: its members plus the org admins. */
export const workspacePeople = (d: DB, w: Workspace) => d.users.filter((u) => hasWorkspaceAccess(d, u.id, w.id))
export const myOrgs = (d: DB) => d.orgs.filter((o) => me(d)?.roles[o.id])
export const orgUsers = (d: DB, orgId = d.currentOrgId) =>
  d.users.filter((u) => u.roles[orgId] || d.invites.some((i) => i.orgId === orgId && i.email === u.email))
export const orgStores = (d: DB) => d.stores.filter((s) => s.orgId === d.currentOrgId)
export const orgKeys = (d: DB) => d.keys.filter((k) => k.orgId === d.currentOrgId)
export const orgTools = (d: DB) => d.tools.filter((t) => t.orgId === d.currentOrgId)
export const orgAgents = (d: DB) => d.agents.filter((a) => a.orgId === d.currentOrgId)
export const orgConnectors = (d: DB) => d.connectors.filter((c) => c.orgId === d.currentOrgId)
export const orgWorkspaces = (d: DB) => {
  const all = d.workspaces.filter((w) => w.orgId === d.currentOrgId)
  if (isAdmin(d)) return all
  return all.filter((w) => w.userIds.includes(d.currentUserId))
}
export const orgEvents = (d: DB) => d.events.filter((e) => e.orgId === d.currentOrgId)
/** Connectors the current person may see: all for admins, those enrolled in their own workspaces for `user`. */
export const visibleConnectors = (d: DB) => orgConnectors(d).filter((c) => isAdmin(d) || canSeeWorkspace(d, c.workspaceId))
/** Whether the current person may open a workspace: admins any in the org, `user` only their own. */
export const canSeeWorkspace = (d: DB, wsId: string) => orgWorkspaces(d).some((w) => w.id === wsId)

/**
 * The one activity scope every log and feed uses. Admins see the whole
 * organization; the `user` role sees their workspaces (which covers their
 * cabinets) and what they did themselves. Creating an agent doesn't make it
 * yours (rule 3), so "created by" never widens what someone sees.
 */
export function visibleEvents(d: DB) {
  const all = orgEvents(d)
  if (myRole(d) !== 'user') return all
  const mine = new Set(orgWorkspaces(d).map((w) => w.id))
  return all.filter((e) => (e.workspaceId && mine.has(e.workspaceId)) || e.actorId === d.currentUserId)
}

export const userById = (d: DB, id: string | null | undefined) => d.users.find((u) => u.id === id)
export const agentById = (d: DB, id: string) => d.agents.find((a) => a.id === id)
export const toolById = (d: DB, id: string) => d.tools.find((t) => t.id === id)
export const keyById = (d: DB, id: string | null | undefined) => d.keys.find((k) => k.id === id)
export const storeById = (d: DB, id: string) => d.stores.find((s) => s.id === id)
export const wsById = (d: DB, id: string) => d.workspaces.find((w) => w.id === id)

export const keyUsage = (d: DB, keyId: string) => {
  const uses: { tool: Tool; ws: Workspace | null; cabinet: Cabinet | null }[] = []
  for (const w of d.workspaces)
    for (const wt of w.tools)
      if (Object.values(wt.slotMap).includes(keyId)) {
        const t = toolById(d, wt.toolId)
        if (t) uses.push({ tool: t, ws: w, cabinet: null })
      }
  for (const c of d.cabinets)
    for (const ct of c.tools)
      if (Object.values(ct.slotMap).includes(keyId)) {
        const t = toolById(d, ct.toolId)
        if (t) uses.push({ tool: t, ws: null, cabinet: c })
      }
  return uses
}

/** Agents a person created that still work. "Created by" is for observability only: agents belong to the organization. */
export const agentsCreatedBy = (d: DB, u: User, orgIds = [d.currentOrgId]) =>
  d.agents.filter((a) => orgIds.includes(a.orgId) && a.createdBy === u.name && a.status !== 'revoked')

/** Workspaces a tool is granted to — only the ones the current person can see. */
export const toolWorkspaces = (d: DB, toolId: string) => orgWorkspaces(d).filter((w) => w.tools.some((t) => t.toolId === toolId))

/** Keys a workspace can use to fill a slot, with the name-matched one first. */
export function slotCandidates(d: DB, keyIds: string[], slot: string) {
  const keys = keyIds.map((id) => keyById(d, id)).filter(Boolean) as Key[]
  return keys.sort((a, b) => Number(b.name === slot) - Number(a.name === slot))
}
export function autoMatch(d: DB, keyIds: string[], slot: string) {
  const exact = keyIds.map((id) => keyById(d, id)).find((k) => k && k.name === slot && !k.sourceRemoved)
  return exact?.id ?? null
}

/** The key filling a workspace tool's slot — only while the workspace still exposes that key. */
export function filledSlot(d: DB, w: Workspace, wt: WorkspaceTool, slot: string) {
  const id = wt.slotMap[slot]
  return id && w.keyIds.includes(id) && keyById(d, id) ? id : null
}
/**
 * What agents actually get: the last published snapshot. Drafts never serve traffic.
 * (Null for a tool that has never been published.)
 */
export const liveTool = (t: Tool | undefined) => (t?.published ?? null)

/** Slot names of a granted tool's published version that have no usable key in this workspace. */
export function missingSlots(d: DB, w: Workspace, wt: WorkspaceTool) {
  const t = toolById(d, wt.toolId)
  return (liveTool(t)?.slots ?? t?.slots ?? []).filter((s) => !filledSlot(d, w, wt, s.name)).map((s) => s.name)
}

/** What un-exposing keys would break: tool slots and cabinet keys in the workspace that point at them. */
export function unexposeImpact(d: DB, wsId: string, keyIds: string[]) {
  const w = wsById(d, wsId)
  const removed = w ? w.keyIds.filter((id) => !keyIds.includes(id)) : []
  const slots: { tool: string; slot: string; key: string }[] = []
  for (const wt of w?.tools ?? [])
    for (const [slot, kid] of Object.entries(wt.slotMap))
      if (kid && removed.includes(kid)) slots.push({ tool: toolById(d, wt.toolId)?.displayName ?? 'Tool', slot, key: keyById(d, kid)?.name ?? kid })
  const cabinets = d.cabinets
    .filter((c) => c.workspaceId === wsId && c.keyIds.some((k) => removed.includes(k)))
    .map((c) => ({ name: c.name, keys: c.keyIds.filter((k) => removed.includes(k)).map((k) => keyById(d, k)?.name ?? k) }))
  return { removed, slots, cabinets }
}

export function lastUsedForKey(d: DB, keyId: string) {
  const toolNames = new Set(keyUsage(d, keyId).map((u) => u.tool.displayName))
  const ev = orgEvents(d).find((e) => e.type === 'request' && toolNames.has(e.object))
  return ev?.at ?? null
}

/* ------------------------------------------------------------------ */
/* Actions                                                             */
/* ------------------------------------------------------------------ */
/** Display name of a person or agent, for log details. */
const playerName = (d: DB, p: PlayerRef) => (p.kind === 'user' ? (userById(d, p.id)?.name ?? p.id) : (agentById(d, p.id)?.label ?? p.id))
/** "Added"/"Removed" detail rows for a change to a list of players. */
function playerDiff(d: DB, before: PlayerRef[], after: PlayerRef[]) {
  const has = (list: PlayerRef[], p: PlayerRef) => list.some((x) => x.kind === p.kind && x.id === p.id)
  const added = after.filter((p) => !has(before, p)).map((p) => playerName(d, p))
  const removed = before.filter((p) => !has(after, p)).map((p) => playerName(d, p))
  const detail: [string, string][] = []
  if (added.length) detail.push(['Added', added.join(', ')])
  if (removed.length) detail.push(['Removed', removed.join(', ')])
  return { added, removed, detail }
}

function actorName(d: DB) {
  return d.currentUserId === 'support' ? 'Keyhole support' : (me(d)?.name ?? 'Someone')
}

export function log(d: DB, e: Partial<AuditEvent> & Pick<AuditEvent, 'object'>) {
  const ev: AuditEvent = {
    id: uid('ev'),
    at: Date.now(),
    orgId: d.currentOrgId,
    type: 'admin',
    severity: 'info',
    actor: actorName(d),
    actorKind: d.currentUserId === 'support' ? 'support' : 'user',
    actorId: d.currentUserId,
    result: 'Done',
    trk: trackingCode(),
    ...e,
  }
  d.events.unshift(ev)
  return ev
}

export const actions = {
  reset(scenario: 'fresh' | 'populated') {
    sessionTokens.clear()
    const next = scenario === 'fresh' ? freshDB() : populatedDB()
    update((d) => Object.assign(d, next))
  },
  setPersona(userId: string) {
    update((d) => {
      d.currentUserId = userId
      if (userId !== 'support') {
        const u = d.users.find((x) => x.id === userId)
        const first = u && Object.keys(u.roles)[0]
        if (u && !u.roles[d.currentOrgId] && first) d.currentOrgId = first
      }
    })
  },
  setOrg(orgId: string) {
    update((d) => void (d.currentOrgId = orgId))
  },
  setListState(s: DB['listState']) {
    update((d) => void (d.listState = s))
  },
  dismissChecklist() {
    update((d) => void (d.checklistDismissed = true))
  },
  markConfigDownloaded() {
    update((d) => void (d.configDownloadedAt ??= Date.now()))
  },

  /* Keys + stores */
  addKey(k: Omit<Key, 'id' | 'orgId' | 'createdAt' | 'rotatedAt'>) {
    const id = uid('k')
    update((d) => {
      if (!adminIn(d, storeById(d, k.storeId)?.orgId)) return
      d.keys.push({ ...k, id, orgId: d.currentOrgId, createdAt: Date.now(), rotatedAt: null })
      const s = storeById(d, k.storeId)
      if (s) s.checkedAt = Date.now()
      log(d, { object: `Added key ${k.name} to ${s?.name ?? 'Local store'}` })
    })
    return id
  },
  replaceKeyValue(id: string, length: number) {
    update((d) => {
      const k = keyById(d, id)
      const cab = k?.cabinetId ? d.cabinets.find((c) => c.id === k.cabinetId) : undefined
      if (!k || !(adminIn(d, k.orgId) || (cab && canManageCabinet(d, cab)))) return
      k.length = length
      k.rotatedAt = Date.now()
      log(d, { object: `Replaced value of ${k.name}` })
    })
  },
  deleteKey(id: string) {
    update((d) => {
      const k = keyById(d, id)
      if (!k || !adminIn(d, k.orgId)) return
      d.keys = d.keys.filter((x) => x.id !== id)
      for (const w of d.workspaces) {
        w.keyIds = w.keyIds.filter((x) => x !== id)
        for (const t of w.tools) for (const s in t.slotMap) if (t.slotMap[s] === id) t.slotMap[s] = null
      }
      for (const c of d.cabinets) {
        c.keyIds = c.keyIds.filter((x) => x !== id)
        for (const t of c.tools) for (const s in t.slotMap) if (t.slotMap[s] === id) t.slotMap[s] = null
      }
      log(d, { object: `Deleted key ${k.name}` })
    })
  },
  importEnv(rows: { name: string; length: number }[]) {
    update((d) => {
      if (!adminIn(d, d.currentOrgId)) return
      const replaced: string[] = []
      for (const r of rows) {
        const existing = d.keys.find((k) => k.orgId === d.currentOrgId && k.storeId === 'st_local' && !k.cabinetId && k.name === r.name)
        if (existing) {
          existing.length = r.length
          existing.rotatedAt = Date.now()
          replaced.push(r.name)
        } else {
          d.keys.push({ id: uid('k'), orgId: d.currentOrgId, name: r.name, storeId: 'st_local', length: r.length, createdAt: Date.now(), rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: 'Imported from .env' })
        }
      }
      log(d, {
        object: `Imported ${rows.length} key${rows.length === 1 ? '' : 's'} from .env into Local store`,
        detail: replaced.length ? [['Values replaced', replaced.join(', ')]] : undefined,
      })
    })
  },
  addOpenBao(s: Omit<SecretStore, 'id' | 'orgId' | 'type' | 'health' | 'checkedAt'>, keyNames: string[]) {
    const id = uid('st')
    update((d) => {
      if (!adminIn(d, d.currentOrgId)) return
      d.stores.push({ ...s, id, orgId: d.currentOrgId, type: 'openbao', health: 'healthy', checkedAt: Date.now() })
      for (const n of keyNames)
        d.keys.push({ id: uid('k'), orgId: d.currentOrgId, name: n, storeId: id, length: 40, createdAt: Date.now(), rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: '' })
      log(d, {
        object: `Connected OpenBao store ${s.name}`,
        severity: s.skipVerify ? 'warn' : 'info',
        result: s.skipVerify ? 'Healthy · TLS not verified' : 'Healthy',
        detail: [['Certificate', s.skipVerify ? 'Not verified — certificate verification skipped' : (s.certName ?? '—')]],
      })
    })
    return id
  },
  /** Saves edits made while re-testing a store, with a before → after for each change. */
  updateOpenBao(id: string, patch: Partial<SecretStore>, changes: [string, string][]) {
    update((d) => {
      const s = storeById(d, id)
      if (!s || !adminIn(d, s.orgId)) return
      Object.assign(s, patch)
      s.health = 'healthy'
      s.checkedAt = Date.now()
      if (changes.length)
        log(d, { object: `Updated OpenBao store ${s.name}`, severity: s.skipVerify ? 'warn' : 'info', result: s.skipVerify ? 'Healthy · TLS not verified' : 'Healthy', detail: changes })
    })
  },
  removeStore(id: string) {
    update((d) => {
      const s = storeById(d, id)
      if (!s || !adminIn(d, s.orgId)) return
      d.stores = d.stores.filter((x) => x.id !== id)
      for (const k of d.keys) if (k.storeId === id) k.sourceRemoved = true
      // Keys nobody uses just go away with their store.
      d.keys = d.keys.filter((k) => k.storeId !== id || keyUsage(d, k.id).length || d.workspaces.some((w) => w.keyIds.includes(k.id)))
      log(d, { object: `Removed store ${s.name}` })
    })
  },
  resolveKey(id: string, replacementId: string | null) {
    update((d) => {
      const k = keyById(d, id)
      if (!k || !adminIn(d, k.orgId) || (replacementId && keyById(d, replacementId)?.orgId !== k.orgId)) return
      const swap = (ids: string[]) => (replacementId ? ids.map((x) => (x === id ? replacementId : x)) : ids.filter((x) => x !== id))
      for (const w of d.workspaces) {
        w.keyIds = Array.from(new Set(swap(w.keyIds)))
        for (const t of w.tools) for (const s in t.slotMap) if (t.slotMap[s] === id) t.slotMap[s] = replacementId
      }
      d.keys = d.keys.filter((x) => x.id !== id)
      const r = replacementId ? keyById(d, replacementId) : null
      log(d, { object: r ? `Replaced ${k.name} with ${r.name}` : `Removed ${k.name} (source removed)` })
    })
  },

  /* Tools */
  installCatalog(catalogId: 'stripe' | 'github' | 'slack') {
    const c = CATALOG.find((x) => x.id === catalogId)!
    const id = uid('t')
    update((d) => {
      if (!adminIn(d, d.currentOrgId)) return
      d.tools.push({ ...structuredClone(c.tool), id, orgId: d.currentOrgId, version: 1, status: 'draft', published: null, updatedAt: Date.now(), catalog: catalogId })
      log(d, { object: `Installed ${c.tool.displayName} from the starter catalog` })
    })
    return id
  },
  createTool(snap: ToolSnapshot) {
    const id = uid('t')
    update((d) => {
      if (!adminIn(d, d.currentOrgId)) return
      d.tools.push({ ...snap, id, orgId: d.currentOrgId, version: 1, status: 'draft', published: null, updatedAt: Date.now() })
      log(d, { object: `Created tool ${snap.displayName}` })
    })
    return id
  },
  editTool(id: string, patch: Partial<ToolSnapshot>) {
    update((d) => {
      const t = toolById(d, id)
      if (!t || !adminIn(d, t.orgId)) return
      if (t.status === 'published') {
        // Editing a published tool opens a draft on top of it.
        t.status = 'draft'
        t.version += 1
      }
      Object.assign(t, patch)
      t.updatedAt = Date.now()
    })
  },
  discardDraft(id: string) {
    update((d) => {
      const t = toolById(d, id)
      if (!t?.published || !adminIn(d, t.orgId)) return
      if (t.status === 'draft') log(d, { object: `Discarded draft v${t.version} of ${t.displayName}`, detail: [['Version', `draft v${t.version} → v${t.published.version}`]] })
      const { version, ...snap } = t.published
      Object.assign(t, structuredClone(snap))
      t.version = version
      t.status = 'published'
      t.updatedAt = Date.now()
    })
  },
  publishTool(id: string, changes: string[] = []) {
    update((d) => {
      const t = toolById(d, id)
      if (!t || !adminIn(d, t.orgId)) return
      // Slots keep their id across versions: a renamed slot keeps the key each workspace picked for it.
      const before = t.published
      const renames = before ? t.slots.map((s) => [before.slots.find((o) => o.id === s.id)?.name, s.name] as const).filter(([o, n]) => o && o !== n) : []
      const carry = (m: Record<string, string | null>) => {
        for (const [o, n] of renames)
          if (m[n] == null && m[o!] != null) {
            m[n] = m[o!]
            delete m[o!]
          }
      }
      for (const w of d.workspaces) for (const wt of w.tools) if (wt.toolId === id) carry(wt.slotMap)
      for (const c of d.cabinets) for (const ct of c.tools) if (ct.toolId === id) carry(ct.slotMap)
      t.status = 'published'
      const { internalName, displayName, description, baseUrl, actions: acts, slots, perMinute, timeoutMs } = t
      t.published = structuredClone({ internalName, displayName, description, baseUrl, actions: acts, slots, perMinute, timeoutMs, version: t.version })
      t.updatedAt = Date.now()
      log(d, {
        object: `Published ${t.displayName} v${t.version}`,
        detail: [['Version', before ? `v${before.version} → v${t.version}` : `v${t.version} (first version)`], ...(changes.length ? [['Changes', changes.join('; ')] as [string, string]] : [])],
      })
    })
  },
  deleteTool(id: string) {
    update((d) => {
      const t = toolById(d, id)
      if (!t || !adminIn(d, t.orgId)) return
      d.tools = d.tools.filter((x) => x.id !== id)
      for (const w of d.workspaces) w.tools = w.tools.filter((x) => x.toolId !== id)
      for (const c of d.cabinets) c.tools = c.tools.filter((x) => x.toolId !== id)
      log(d, { object: `Deleted tool ${t.displayName}` })
    })
  },

  /* Workspaces */
  createWorkspace(w: { name: string; keyIds: string[]; userIds: string[]; agentIds: string[] }) {
    const id = uid('ws')
    update((d) => {
      if (!adminIn(d, d.currentOrgId)) return
      d.workspaces.push({ id, orgId: d.currentOrgId, slug: 'ws_' + id.slice(-4), name: w.name, https: false, mcp: false, keyIds: w.keyIds, userIds: w.userIds, adminIds: [], agentIds: w.agentIds, tools: [], createdAt: Date.now() })
      for (const a of d.agents) if (w.agentIds.includes(a.id) && !a.workspaceIds.includes(id)) a.workspaceIds.push(id)
      log(d, { shared: true, object: `Created workspace ${w.name}` })
    })
    return id
  },
  exposeKey(wsId: string, keyId: string) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || !wsAdminIn(d, w) || keyById(d, keyId)?.orgId !== w.orgId || w.keyIds.includes(keyId)) return
      w.keyIds.push(keyId)
      log(d, { object: `Exposed ${keyById(d, keyId)?.name} in ${w.name}`, workspaceId: wsId })
    })
  },
  setWorkspaceKeys(wsId: string, keyIds: string[]) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || !wsAdminIn(d, w) || keyIds.some((id) => keyById(d, id)?.orgId !== w.orgId)) return
      const { removed, slots, cabinets } = unexposeImpact(d, wsId, keyIds)
      const added = keyIds.filter((id) => !w.keyIds.includes(id))
      w.keyIds = keyIds
      // Un-exposing a key cuts it off everywhere in the workspace: slots go back to Missing.
      const clear = (m: Record<string, string | null>) => {
        for (const s in m) if (m[s] && removed.includes(m[s]!)) m[s] = null
      }
      for (const wt of w.tools) clear(wt.slotMap)
      for (const c of d.cabinets.filter((x) => x.workspaceId === wsId)) {
        c.keyIds = c.keyIds.filter((k) => !removed.includes(k))
        for (const t of c.tools) clear(t.slotMap)
      }
      const names = (ids: string[]) => ids.map((id) => keyById(d, id)?.name ?? id).join(', ')
      if (added.length) log(d, { object: `Exposed ${names(added)} in ${w.name}`, workspaceId: wsId })
      if (removed.length) {
        const detail: [string, string][] = [
          ...slots.map((x): [string, string] => [`${x.tool} · ${x.slot}`, `${x.key} → Missing`]),
          ...cabinets.map((c): [string, string] => [`Cabinet ${c.name}`, `Loses ${c.keys.join(', ')}`]),
        ]
        log(d, { object: `Stopped exposing ${names(removed)} in ${w.name}`, workspaceId: wsId, detail: detail.length ? detail : undefined })
      }
    })
  },
  grantTool(wsId: string, toolId: string, slotMap: Record<string, string | null>) {
    update((d) => {
      const w = wsById(d, wsId)
      const t = toolById(d, toolId)
      if (!w || !t || t.orgId !== w.orgId || !wsAdminIn(d, w)) return
      const existing = w.tools.find((x) => x.toolId === toolId)
      const keyName = (id: string | null | undefined) => (id ? (keyById(d, id)?.name ?? id) : 'Missing')
      const detail: [string, string][] = [
        ['Access', existing ? 'Granted (unchanged)' : 'Not granted → granted'],
        ['Version', `v${(liveTool(t) ?? t).version}`],
        ...Object.keys(slotMap).map((s): [string, string] => [s, existing ? `${keyName(existing.slotMap[s])} → ${keyName(slotMap[s])}` : `→ ${keyName(slotMap[s])}`]),
      ]
      if (existing) existing.slotMap = slotMap
      else w.tools.push({ toolId, slotMap, enabled: true, perMinute: (liveTool(t) ?? t).perMinute })
      log(d, { object: `Granted ${t.displayName} to ${w.name}`, workspaceId: wsId, detail })
    })
  },
  updateWorkspaceTool(wsId: string, toolId: string, patch: Partial<{ enabled: boolean; perMinute: number; slotMap: Record<string, string | null> }>) {
    update((d) => {
      const w = wsById(d, wsId)
      const wt = w?.tools.find((x) => x.toolId === toolId)
      if (!w || !wt || !wsAdminIn(d, w)) return
      const tool = toolById(d, toolId)?.displayName ?? 'Tool'
      const keyName = (id: string | null | undefined) => (id ? (keyById(d, id)?.name ?? id) : 'Missing')
      // Every change to who can reach which key is logged with its before → after.
      if (patch.slotMap) {
        const changed = Object.keys({ ...wt.slotMap, ...patch.slotMap }).filter((s) => (wt.slotMap[s] ?? null) !== (patch.slotMap![s] ?? null))
        if (changed.length)
          log(d, {
            object: `Changed ${tool} key slot${changed.length === 1 ? '' : 's'} in ${w.name}`,
            workspaceId: wsId,
            detail: changed.map((s): [string, string] => [s, `${keyName(wt.slotMap[s])} → ${keyName(patch.slotMap![s])}`]),
          })
      }
      if (patch.enabled !== undefined && patch.enabled !== wt.enabled) log(d, { object: `Turned ${tool} ${patch.enabled ? 'on' : 'off'} in ${w.name}`, workspaceId: wsId })
      if (patch.perMinute !== undefined && patch.perMinute !== wt.perMinute)
        log(d, { object: `Set ${tool} limit in ${w.name} to ${patch.perMinute}/min`, workspaceId: wsId, detail: [['Calls per minute', `${wt.perMinute} → ${patch.perMinute}`]] })
      Object.assign(wt, patch)
    })
  },
  removeWorkspaceTool(wsId: string, toolId: string) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || !wsAdminIn(d, w)) return
      w.tools = w.tools.filter((x) => x.toolId !== toolId)
      log(d, { object: `Removed ${toolById(d, toolId)?.displayName} from ${w.name}`, workspaceId: wsId })
    })
  },
  setConnection(wsId: string, kind: 'https' | 'mcp', on: boolean) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || !wsAdminIn(d, w)) return
      if (w[kind] === on) return
      w[kind] = on
      log(d, { object: `Turned ${kind.toUpperCase()} ${on ? 'on' : 'off'} for ${w.name}`, workspaceId: wsId, detail: [[kind.toUpperCase(), on ? 'Off → On' : 'On → Off']] })
    })
  },
  /** The workspace's Members tab: people with their workspace role, and agents. */
  setWorkspaceMembers(wsId: string, members: { userId: string; role: 'member' | 'admin' }[], agentIds: string[]) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || !wsAdminIn(d, w)) return
      const want = new Map(members.map((m) => [m.userId, m.role]))
      for (const id of new Set([...w.userIds, ...want.keys()])) applyMembership(d, w, id, want.get(id) ?? null)
      // Only agents of the workspace's own organization can be added to it.
      agentIds = agentIds.filter((id) => agentById(d, id)?.orgId === w.orgId)
      for (const id of new Set([...w.agentIds, ...agentIds])) applyAgentMembership(d, w, id, agentIds.includes(id))
    })
  },
  deleteWorkspace(wsId: string) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || !adminIn(d, w.orgId)) return
      d.workspaces = d.workspaces.filter((x) => x.id !== wsId)
      d.cabinets = d.cabinets.filter((c) => c.workspaceId !== wsId)
      for (const a of d.agents) a.workspaceIds = a.workspaceIds.filter((x) => x !== wsId)
      // Connectors are enrolled into a workspace, so they're revoked with it; stores routed through them lose their route.
      const conns = d.connectors.filter((c) => c.workspaceId === wsId)
      const routed = d.stores.filter((s) => conns.some((c) => c.id === s.route))
      d.connectors = d.connectors.filter((c) => c.workspaceId !== wsId)
      for (const s of routed) s.health = 'unreachable'
      const detail: [string, string][] = []
      const vaults = conns.filter((c) => c.kind === 'vault')
      const sidecars = conns.filter((c) => c.kind === 'sidecar')
      if (vaults.length) detail.push(['Vault connectors revoked', vaults.map((c) => c.name).join(', ')])
      if (sidecars.length) detail.push(['Sidecars revoked', sidecars.map((c) => c.name).join(', ')])
      if (routed.length) detail.push(['Stores now unreachable', routed.map((s) => s.name).join(', ')])
      log(d, { shared: true, object: `Deleted workspace ${w.name}`, detail: detail.length ? detail : undefined })
    })
  },

  /* Agents */
  createAgent(a: { label: string; workspaceIds: string[]; expiryDays: number | null; rateLimit: number | null }) {
    // Checked before a token is minted, so a refused call never shows one.
    if (!adminIn(db, db.currentOrgId)) return null
    const token = newToken()
    const id = uid('ag')
    sessionTokens.set(id, token)
    update((d) => {
      const agent: Agent = {
        id,
        orgId: d.currentOrgId,
        label: a.label,
        tokenLast4: token.slice(-4),
        status: 'active',
        createdAt: Date.now(),
        createdBy: me(d).name,
        expiresAt: a.expiryDays ? Date.now() + a.expiryDays * DAY : null,
        lastUsedAt: null,
        workspaceIds: a.workspaceIds.filter((id) => wsById(d, id)?.orgId === d.currentOrgId),
        rateLimit: a.rateLimit,
      }
      d.agents.push(agent)
      for (const w of d.workspaces) if (a.workspaceIds.includes(w.id) && !w.agentIds.includes(id)) w.agentIds.push(id)
      log(d, { shared: true, object: `Created agent ${a.label}`, detail: [['Workspaces', a.workspaceIds.map((id) => wsById(d, id)?.name).join(', ') || 'None']] })
    })
    return { id, token }
  },
  rotateAgent(id: string) {
    if (!adminIn(db, agentById(db, id)?.orgId)) return null
    const token = newToken()
    sessionTokens.set(id, token)
    update((d) => {
      const a = agentById(d, id)
      if (!a) return
      a.tokenLast4 = token.slice(-4)
      a.rotatedAt = Date.now()
      log(d, { object: `Rotated token for ${a.label}` })
    })
    return token
  },
  renameAgent(id: string, label: string) {
    update((d) => {
      const a = agentById(d, id)
      if (!a || !adminIn(d, a.orgId) || !label.trim() || label.trim() === a.label) return
      log(d, { shared: true, object: `Renamed agent ${a.label} → ${label.trim()}` })
      a.label = label.trim()
    })
  },
  setAgentStatus(id: string, status: 'active' | 'suspended') {
    update((d) => {
      const a = agentById(d, id)
      if (!a || !adminIn(d, a.orgId) || a.status === 'revoked' || a.status === status) return
      log(d, { object: `${status === 'suspended' ? 'Suspended' : 'Resumed'} ${a.label}`, detail: [['Status', `${a.status} → ${status}`]] })
      a.status = status
    })
  },
  revokeAgent(id: string) {
    if (!adminIn(db, agentById(db, id)?.orgId)) return
    update((d) => {
      const a = agentById(d, id)
      if (!a || a.status === 'revoked') return
      log(d, { object: `Revoked token for ${a.label}`, detail: [['Status', `${a.status} → revoked`], ['Token', maskToken(a.tokenLast4)]] })
      a.status = 'revoked'
    })
    // The agent doesn't know yet — its next attempt shows up blocked.
    setTimeout(() => {
      update((d) => {
        const a = agentById(d, id)
        if (!a) return
        const ws = wsById(d, a.workspaceIds[0])
        log(d, {
          // Written to the agent's own organization, whichever one is open by then.
          orgId: a.orgId,
          type: 'blocked',
          severity: 'blocked',
          actor: a.label,
          actorKind: 'agent',
          actorId: a.id,
          workspaceId: ws?.id,
          object: `${ws?.name ?? 'Workspace'} · ${ws?.mcp ? 'MCP' : 'HTTPS'} connect`,
          result: 'Token revoked',
          reason: `Blocked: the token for ${a.label} was revoked. Create a new agent to reconnect.`,
          detail: [['Token', `kh_live_••••${a.tokenLast4}`], ['Revoked', 'Moments ago']],
        })
      })
    }, 4000)
  },

  /* People */
  invite(i: { email: string; role: Role; workspaceIds: string[]; adminWorkspaceIds?: string[] }) {
    update((d) => {
      if (!adminIn(d, d.currentOrgId)) return
      const id = uid('inv')
      d.invites.push({ id, orgId: d.currentOrgId, email: i.email, role: i.role, workspaceIds: i.workspaceIds, adminWorkspaceIds: i.adminWorkspaceIds?.filter((id) => i.workspaceIds.includes(id)), invitedBy: me(d).name, invitedAt: Date.now(), expiresAt: Date.now() + 7 * DAY })
      let u = d.users.find((x) => x.email === i.email)
      if (!u) {
        u = { id: uid('u'), name: i.email.split('@')[0].replace(/^./, (c) => c.toUpperCase()), email: i.email, roles: {}, status: 'invited', locked: false, lastActive: null, sessions: [] }
        d.users.push(u)
      }
      ;(d.incomingInvites[u.id] ??= []).push({ id, orgName: org(d)!.name, orgId: d.currentOrgId, invitedBy: me(d).name, role: i.role, expiresAt: Date.now() + 7 * DAY })
      log(d, { object: `Invited ${i.email} as ${i.role}`, shared: true, detail: i.workspaceIds.length ? [['Workspaces', i.workspaceIds.map((id) => `${wsById(d, id)?.name}${i.adminWorkspaceIds?.includes(id) ? ' (admin)' : ''}`).join(', ')]] : undefined })
    })
  },
  resendInvite(inviteId: string) {
    update((d) => {
      const inv = d.invites.find((x) => x.id === inviteId)
      if (!inv || !adminIn(d, inv.orgId)) return
      inv.expiresAt = Date.now() + 7 * DAY
      log(d, { shared: true, object: `Resent invite to ${inv.email}`, orgId: inv.orgId })
    })
  },
  revokeInvite(inviteId: string) {
    update((d) => {
      const inv = d.invites.find((x) => x.id === inviteId)
      if (!inv || !adminIn(d, inv.orgId)) return
      d.invites = d.invites.filter((x) => x.id !== inviteId)
      for (const k in d.incomingInvites) d.incomingInvites[k] = d.incomingInvites[k].filter((x) => x.id !== inviteId)
      // The person's record stays: accounts are never deleted here, so "Created by" and history keep resolving.
      log(d, { shared: true, object: `Revoked invite for ${inv.email}` })
    })
  },
  acceptInvite(inviteId: string) {
    update((d) => {
      const u = me(d)
      const inc = d.incomingInvites[u.id]?.find((x) => x.id === inviteId)
      const inv = d.invites.find((x) => x.id === inviteId)
      if (!inc?.orgId) return
      u.roles[inc.orgId] = inc.role
      u.status = 'active'
      u.lastActive = Date.now()
      for (const w of d.workspaces)
        if (inv?.workspaceIds.includes(w.id)) {
          if (!w.userIds.includes(u.id)) w.userIds.push(u.id)
          if (inv.adminWorkspaceIds?.includes(w.id) && !w.adminIds.includes(u.id)) w.adminIds.push(u.id)
        }
      d.invites = d.invites.filter((x) => x.id !== inviteId)
      d.incomingInvites[u.id] = d.incomingInvites[u.id].filter((x) => x.id !== inviteId)
      d.currentOrgId = inc.orgId
      log(d, { shared: true, object: `${u.name} joined as ${inc.role}`, orgId: inc.orgId })
    })
  },
  declineInvite(inviteId: string) {
    update((d) => {
      const u = me(d)
      d.incomingInvites[u.id] = (d.incomingInvites[u.id] ?? []).filter((x) => x.id !== inviteId)
      const inv = d.invites.find((x) => x.id === inviteId)
      d.invites = d.invites.filter((x) => x.id !== inviteId)
      if (inv) log(d, { shared: true, object: `${u.email} declined the invite`, orgId: inv.orgId })
    })
  },
  changeRole(userId: string, role: Role) {
    update((d) => {
      const u = userById(d, userId)
      // An Owner may also step down themselves, as long as another Owner remains.
      const selfOwner = userId === d.currentUserId && myRole(d) === 'Owner'
      if (!u || (!canManageMember(d, userId) && !selfOwner)) return
      const before = u.roles[d.currentOrgId]
      if (before === role) return
      // Only Owners make Owners, and the last Owner can't be demoted (transfer ownership instead).
      if ((role === 'Owner' && myRole(d) !== 'Owner') || (before === 'Owner' && isLastOwner(d, userId))) return
      u.roles[d.currentOrgId] = role
      const released = settleCabinetManagement(d)
      log(d, { shared: true, object: `Changed ${u.name}'s role to ${role}`, detail: [['Role', `${before} → ${role}`], ...(released.length ? [['Cabinets now managed by admins', released.join(', ')] as [string, string]] : [])] })
    })
  },
  setUserSuspended(userId: string, suspended: boolean) {
    update((d) => {
      const u = userById(d, userId)
      if (!u || !canManageMember(d, userId) || (suspended && isLastOwner(d, userId))) return
      // Only this membership changes: the person's other organizations, and their logs, are untouched.
      if (isSuspended(u, d.currentOrgId) === suspended) return
      const label = (s: boolean) => (s ? 'suspended' : 'active')
      log(d, { shared: true, object: `${suspended ? 'Suspended' : 'Reactivated'} ${u.name}`, detail: [['Status in this organization', `${label(!suspended)} → ${label(suspended)}`]] })
      if (suspended) u.suspended = { ...u.suspended, [d.currentOrgId]: true }
      else if (u.suspended) delete u.suspended[d.currentOrgId]
    })
  },
  /** The current Owner hands ownership to another member and becomes a userAdmin. */
  transferOwnership(toUserId: string) {
    update((d) => {
      const from = me(d)
      const to = userById(d, toUserId)
      const before = to?.roles[d.currentOrgId]
      if (!from || myRole(d) !== 'Owner' || !inMyOrg(d, d.currentOrgId) || !to || !before || before === 'Owner' || to.id === from.id || !isActive(to, d.currentOrgId)) return
      to.roles[d.currentOrgId] = 'Owner'
      from.roles[d.currentOrgId] = 'userAdmin'
      log(d, {
        shared: true, object: `Transferred ownership of ${org(d)?.name} to ${to.name}`,
        detail: [
          [to.name, `${before} → Owner`],
          [from.name, 'Owner → userAdmin'],
        ],
      })
    })
  },
  /** Players › Assign workspaces: one person's role in each workspace (absent = not a member). One log row per workspace. */
  setUserWorkspaces(userId: string, roles: Record<string, 'member' | 'admin'>) {
    update((d) => {
      if (!adminIn(d, d.currentOrgId) || !userById(d, userId)?.roles[d.currentOrgId]) return
      for (const w of d.workspaces.filter((x) => x.orgId === d.currentOrgId)) applyMembership(d, w, userId, roles[w.id] ?? null)
    })
  },
  /** Players › Agents › Assign workspaces. */
  setAgentWorkspaces(agentId: string, wsIds: string[]) {
    update((d) => {
      const a = agentById(d, agentId)
      if (!a || !adminIn(d, a.orgId)) return
      for (const w of d.workspaces.filter((x) => x.orgId === a.orgId)) applyAgentMembership(d, w, a.id, wsIds.includes(w.id))
    })
  },
  /**
   * Removing someone only stops what they can do next (rule 4). Nothing they created or did changes:
   * agents keep working, grants and keys stay, cabinets keep their keys, tools and lock list (admins
   * take over managing them), and the audit log keeps their name. Their Keyhole account stays too.
   */
  removeUser(userId: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u || !canManageMember(d, userId) || isLastOwner(d, userId)) return
      const role = u.roles[d.currentOrgId]
      const lost = d.workspaces.filter((w) => w.orgId === d.currentOrgId && w.userIds.includes(userId)).map((w) => w.name)
      delete u.roles[d.currentOrgId]
      // Suspension belongs to the membership, so it goes with it.
      if (u.suspended) delete u.suspended[d.currentOrgId]
      for (const w of d.workspaces)
        if (w.orgId === d.currentOrgId) {
          w.userIds = w.userIds.filter((x) => x !== userId)
          w.adminIds = w.adminIds.filter((x) => x !== userId)
        }
      const released = settleCabinetManagement(d)
      log(d, {
        shared: true, object: `Removed ${u.name} from the organization`,
        detail: [
          ['Role', `${role} → removed`],
          ['Workspaces', isAdminRole(role) ? 'All (as an org admin)' : lost.join(', ') || 'None'],
          ['Agents they created', 'Unaffected'],
          ['Cabinets they managed', released.length ? `${released.join(', ')} — keep working; admins manage them` : 'None'],
        ],
      })
    })
  },

  /* Cabinets */
  createCabinet(c: { workspaceId: string; name: string; newKeys: { name: string; length: number }[]; pickedKeyIds: string[]; tools: Cabinet['tools']; access: Cabinet['access'] }) {
    const id = uid('cb')
    update((d) => {
      const ws = wsById(d, c.workspaceId)
      if (!ws || !inMyOrg(d, ws.orgId) || !hasWorkspaceAccess(d, d.currentUserId, ws.id)) return
      // Picked keys must already be exposed in the workspace; anything else is dropped.
      const exposed = wsById(d, c.workspaceId)?.keyIds ?? []
      const keyIds = c.pickedKeyIds.filter((k) => exposed.includes(k))
      const nameToId: Record<string, string> = {}
      for (const nk of c.newKeys) {
        const kid = uid('k')
        nameToId[nk.name] = kid
        keyIds.push(kid)
        d.keys.push({ id: kid, orgId: d.currentOrgId, name: nk.name, storeId: 'st_local', length: nk.length, createdAt: Date.now(), rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: 'Cabinet key', cabinetId: id })
      }
      const fill = (v: string | null) => (!v ? null : v.startsWith('new:') ? (nameToId[v.slice(4)] ?? null) : keyIds.includes(v) ? v : null)
      const tools = c.tools.filter((t) => liveTool(toolById(d, t.toolId))).map((t) => ({ ...t, slotMap: Object.fromEntries(Object.entries(t.slotMap).map(([s, v]) => [s, fill(v)])) }))
      d.cabinets.push({ id, workspaceId: c.workspaceId, name: c.name, createdBy: d.currentUserId, managedBy: d.currentUserId, keyIds, tools, access: c.access, createdAt: Date.now() })
      log(d, { object: `Created cabinet ${c.name}${c.access === 'everyone' ? '' : ' (locked)'}`, workspaceId: c.workspaceId })
    })
    return id
  },
  setCabinetAccess(id: string, access: 'everyone' | PlayerRef[]) {
    update((d) => {
      const c = d.cabinets.find((x) => x.id === id)
      if (!c || !canManageCabinet(d, c)) return
      const label = (a: Cabinet['access']) => (a === 'everyone' ? 'Everyone in the workspace' : `Only ${plural(a.length, 'player')}`)
      const detail: [string, string][] = [['Lock', `${label(c.access)} → ${label(access)}`], ...playerDiff(d, c.access === 'everyone' ? [] : c.access, access === 'everyone' ? [] : access).detail]
      c.access = access
      log(d, { object: `Changed lock on ${c.name}`, workspaceId: c.workspaceId, detail })
    })
  },
  /** Hands management to someone else. "Created by" never changes. */
  reassignCabinet(id: string, managerId: string) {
    update((d) => {
      const c = d.cabinets.find((x) => x.id === id)
      if (!c || !wsAdminIn(d, wsById(d, c.workspaceId)) || !hasWorkspaceAccess(d, managerId, c.workspaceId)) return
      const before = userById(d, c.managedBy)?.name ?? 'Org admins'
      c.managedBy = managerId
      log(d, { object: `${userById(d, managerId)?.name} now manages ${c.name}`, workspaceId: c.workspaceId, detail: [['Managed by', `${before} → ${userById(d, managerId)?.name}`]] })
    })
  },
  deleteCabinet(id: string) {
    update((d) => {
      const c = d.cabinets.find((x) => x.id === id)
      if (!c || !canManageCabinet(d, c)) return
      d.cabinets = d.cabinets.filter((x) => x.id !== id)
      d.keys = d.keys.filter((k) => k.cabinetId !== id)
      log(d, { object: `Deleted cabinet ${c.name}`, workspaceId: c.workspaceId })
    })
  },

  /* Connectors */
  enrollConnector(c: { name: string; workspaceId: string }) {
    const id = uid('cn')
    update((d) => {
      if (!wsAdminIn(d, wsById(d, c.workspaceId))) return
      d.connectors.push({ id, orgId: d.currentOrgId, kind: 'vault', name: c.name, workspaceId: c.workspaceId, version: '1.4.2', health: 'healthy', lastSeen: Date.now(), ip: `10.2.14.${20 + Math.floor(Math.random() * 60)}`, enrolledBy: me(d).name.replace(/(\w+) (\w).*/, '$1 $2.'), enrolledAt: Date.now() })
      // The person who issued the enrollment token is attributed; the heartbeat is the connector's own.
      log(d, { object: `Enrolled vault connector ${c.name} in ${wsById(d, c.workspaceId)?.name ?? 'a workspace'}`, workspaceId: c.workspaceId })
      log(d, { type: 'connection', severity: 'ok', actor: c.name, actorKind: 'connector', object: 'Vault connector enrolled', workspaceId: c.workspaceId, result: 'First heartbeat' })
    })
    return id
  },
  /** A sidecar next to an app or agent harness, acting as one agent of its workspace. */
  enrollSidecar(c: { name: string; workspaceId: string; agentId: string; protocols: SidecarProtocol[]; listen: string }) {
    const id = uid('sc')
    update((d) => {
      const w = wsById(d, c.workspaceId)
      const a = agentById(d, c.agentId)
      if (!w || !wsAdminIn(d, w) || !a || a.orgId !== w.orgId || !w.agentIds.includes(a.id) || !c.protocols.length) return
      const host = `${c.name.replace(/[^a-z0-9-]/gi, '').toLowerCase() || 'app'}-${Math.random().toString(16).slice(2, 6)}.${w.name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}.internal`
      d.connectors.push({ id, orgId: w.orgId, kind: 'sidecar', name: c.name, workspaceId: w.id, agentId: a.id, protocols: c.protocols, listen: c.listen, host, requestsBase: 0, version: '1.5.0', health: 'healthy', lastSeen: Date.now(), ip: `10.2.30.${20 + Math.floor(Math.random() * 60)}`, enrolledBy: me(d).name.replace(/(\w+) (\w).*/, '$1 $2.'), enrolledAt: Date.now() })
      log(d, { object: `Enrolled sidecar ${c.name} in ${w.name} for ${a.label}`, workspaceId: w.id, detail: [['Agent', a.label], ['Protocols', c.protocols.map((p) => p.toUpperCase()).join(', ')], ['Listens on', c.listen]] })
      log(d, { type: 'connection', severity: 'ok', actor: c.name, actorKind: 'connector', object: 'Sidecar enrolled', workspaceId: w.id, result: 'First heartbeat', detail: [['Host', host]] })
    })
    return id
  },
  /** Point a sidecar at another agent of its workspace; that agent's permissions apply from the next request. */
  rebindSidecar(id: string, agentId: string) {
    update((d) => {
      const c = d.connectors.find((x) => x.id === id)
      const w = c && wsById(d, c.workspaceId)
      const a = agentById(d, agentId)
      if (!c || c.kind !== 'sidecar' || !w || !wsAdminIn(d, w) || !a || a.status !== 'active' || !w.agentIds.includes(a.id) || c.agentId === a.id) return
      const before = agentById(d, c.agentId ?? '')?.label ?? '—'
      c.agentId = a.id
      log(d, { object: `Rebound sidecar ${c.name} to ${a.label}`, workspaceId: w.id, detail: [['Agent', `${before} → ${a.label}`]] })
    })
  },
  /** New credential for a running connector. Checked before minting; the token is returned once and never stored. */
  rotateConnector(id: string) {
    const c = db.connectors.find((x) => x.id === id)
    if (!c || !wsAdminIn(db, wsById(db, c.workspaceId))) return null
    const token = newConnectorToken()
    update((d) => {
      const x = d.connectors.find((y) => y.id === id)
      if (!x) return
      x.rotatedAt = Date.now()
      log(d, { object: `Rotated token for ${connectorKind(x)} ${x.name}`, workspaceId: x.workspaceId, detail: [['Credential', 'New token issued · the old one works 10 more minutes']] })
    })
    return token
  },
  renameConnector(id: string, name: string) {
    update((d) => {
      const c = d.connectors.find((x) => x.id === id)
      if (!c || !wsAdminIn(d, wsById(d, c.workspaceId)) || !name.trim() || name.trim() === c.name) return
      log(d, { object: `Renamed ${connectorKind(c)} ${c.name} → ${name.trim()}`, workspaceId: c.workspaceId })
      c.name = name.trim()
    })
  },
  revokeConnector(id: string) {
    update((d) => {
      const c = d.connectors.find((x) => x.id === id)
      if (!c || !wsAdminIn(d, wsById(d, c.workspaceId))) return
      d.connectors = d.connectors.filter((x) => x.id !== id)
      for (const s of d.stores) if (s.route === id) s.health = 'unreachable'
      log(d, { object: `Revoked ${connectorKind(c)} ${c.name}`, workspaceId: c.workspaceId })
    })
  },

  /* Verify + first call */
  landFirstCall(wsId: string, agentId: string | null, sidecarId?: string) {
    const trk = trackingCode()
    const ms = 140 + Math.floor(Math.random() * 90)
    update((d) => {
      const w = wsById(d, wsId)
      if (!w) return
      const a = agentId ? agentById(d, agentId) : null
      const sc = sidecarId ? d.connectors.find((x) => x.id === sidecarId) : undefined
      const via = sc ? { via: sc.name, viaId: sc.id } : {}
      const wt = w?.tools.find((t) => t.enabled && liveTool(toolById(d, t.toolId)) && !missingSlots(d, w, t).length)
      const t = wt ? liveTool(toolById(d, wt.toolId)) : null
      const act = t?.actions[0]
      if (a) a.lastUsedAt = Date.now()
      const detail: [string, string][] = [['Action', act ? `${act.method} ${act.path}` : '—'], ['Workspace', w?.name ?? '—'], sc ? ['Sidecar', sc.name] : ['Route', 'Direct']]
      log(d, { type: 'request', severity: 'ok', actor: a?.label ?? 'agent', actorKind: 'agent', actorId: a?.id, ...via, object: t?.displayName ?? 'MCP tools/list', destination: t ? new URL(t.baseUrl).host : undefined, orgId: w.orgId, workspaceId: wsId, result: `200 · ${ms} ms`, trk, detail })
      log(d, { type: 'verification', severity: 'ok', actor: a?.label ?? 'agent', actorKind: 'agent', actorId: a?.id, ...via, object: 'First request verified', orgId: w.orgId, workspaceId: wsId, result: `200 · ${ms} ms`, trk: trackingCode() })
      if (!d.firstCallAt) d.firstCallAt = Date.now()
    })
    return { trk, ms }
  },

  /* Support */
  /** Support acts on an account only with a reason or ticket ID, which every affected organization sees. */
  setLocked(userId: string, locked: boolean, reason: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u || d.currentUserId !== 'support') return
      u.locked = locked
      u.lockedAt = locked ? Date.now() : undefined
      u.lockReason = locked ? reason : undefined
      u.unlockRequest = undefined
      if (locked) u.sessions = []
      for (const orgId of Object.keys(u.roles))
        log(d, { shared: true, orgId, type: 'support', actor: 'Keyhole support', actorKind: 'support', actorId: 'support', object: `${locked ? 'Locked' : 'Unlocked'} account ${u.email}`, detail: [['Reason or ticket', reason]] })
    })
  },
  signOutEverywhere(userId: string, reason?: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u || d.currentUserId !== 'support') return
      const was = u.sessions.length
      u.sessions = []
      if (d.currentUserId === 'support')
        for (const orgId of Object.keys(u.roles))
          log(d, {
            orgId,
            type: 'support',
            actor: 'Keyhole support',
            actorKind: 'support',
            actorId: 'support',
            shared: true, object: `Signed ${u.email} out everywhere (${was} session${was === 1 ? '' : 's'})`,
            detail: reason ? [['Reason or ticket', reason]] : undefined,
          })
    })
  },
  /** Only support can unlock; an admin can ask for it, and support sees the request. */
  requestUnlock(userId: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u?.locked || !adminIn(d, d.currentOrgId) || !u.roles[d.currentOrgId]) return
      u.unlockRequest = { by: `${me(d).name} · ${org(d)?.name}`, at: Date.now() }
      log(d, { object: `Asked Keyhole support to unlock ${u.email}` })
    })
  },
  supportResendInvite(userId: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u || d.currentUserId !== 'support') return
      for (const inv of d.invites.filter((x) => x.email === u.email)) {
        inv.expiresAt = Date.now() + 7 * DAY
        log(d, { shared: true, orgId: inv.orgId, type: 'support', actor: 'Keyhole support', actorKind: 'support', actorId: 'support', object: `Resent invite to ${u.email}` })
      }
    })
  },
  signOutSession(index: number) {
    update((d) => {
      me(d).sessions.splice(index, 1)
    })
  },

  /* Settings */
  toggleNotification(key: string) {
    update((d) => void (d.notifications[key] = !d.notifications[key]))
  },
  setWebhook(url: string) {
    update((d) => void (d.webhook = url))
  },
  renameMe(name: string) {
    update((d) => void (me(d).name = name))
  },
  renameOrg(name: string) {
    update((d) => {
      const o = org(d)
      if (!o || !name.trim() || myRole(d) !== 'Owner' || !inMyOrg(d, o.id)) return
      log(d, { shared: true, object: `Renamed organization ${o.name} → ${name.trim()}` })
      o.name = name.trim()
    })
  },
  deleteOrg() {
    update((d) => {
      const id = d.currentOrgId
      if (myRole(d) !== 'Owner' || !inMyOrg(d, id)) return
      d.orgs = d.orgs.filter((o) => o.id !== id)
      for (const u of d.users) delete u.roles[id]
      d.workspaces = d.workspaces.filter((w) => w.orgId !== id)
      d.stores = d.stores.filter((s) => s.orgId !== id)
      d.keys = d.keys.filter((k) => k.orgId !== id)
      d.tools = d.tools.filter((t) => t.orgId !== id)
      d.agents = d.agents.filter((a) => a.orgId !== id)
      d.connectors = d.connectors.filter((c) => c.orgId !== id)
      d.events = d.events.filter((e) => e.orgId !== id)
      d.currentOrgId = Object.keys(me(d).roles)[0] ?? ''
    })
  },
}

export type { User }
