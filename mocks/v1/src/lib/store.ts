import { useEffect, useState, useSyncExternalStore } from 'react'
import { CATALOG } from './catalog'
import { DAY, newToken, plural, trackingCode, uid } from './format'
import { freshDB, populatedDB } from './seed'
import type {
  Agent,
  AuditEvent,
  Cabinet,
  DB,
  Key,
  PlayerRef,
  Role,
  SecretStore,
  Tool,
  ToolSnapshot,
  User,
  Workspace,
  WorkspaceTool,
} from './types'

const LS_KEY = 'keyhole-mocks-v1'
const VERSION = 4

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
  return r === 'Owner' || r === 'userAdmin'
}
export const org = (d: DB, id = d.currentOrgId) => d.orgs.find((o) => o.id === id)
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
 * cabinets), what they did themselves, and their own agents' activity.
 */
export function visibleEvents(d: DB) {
  const all = orgEvents(d)
  if (myRole(d) !== 'user') return all
  const mine = new Set(orgWorkspaces(d).map((w) => w.id))
  const myName = me(d)?.name
  const myAgents = new Set(d.agents.filter((a) => a.orgId === d.currentOrgId && a.createdBy === myName).map((a) => a.id))
  return all.filter((e) => (e.workspaceId && mine.has(e.workspaceId)) || e.actorId === d.currentUserId || (e.actorId && myAgents.has(e.actorId)))
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

/** Agents a person created that still work. They belong to the organization, not to the person. */
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
      if (!k) return
      k.length = length
      k.rotatedAt = Date.now()
      log(d, { object: `Replaced value of ${k.name}` })
    })
  },
  deleteKey(id: string) {
    update((d) => {
      const k = keyById(d, id)
      if (!k) return
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
      if (!isAdmin(d)) return
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
      if (!s || !isAdmin(d)) return
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
      if (!s) return
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
      if (!k) return
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
      d.tools.push({ ...structuredClone(c.tool), id, orgId: d.currentOrgId, version: 1, status: 'draft', published: null, updatedAt: Date.now(), catalog: catalogId })
      log(d, { object: `Installed ${c.tool.displayName} from the starter catalog` })
    })
    return id
  },
  createTool(snap: ToolSnapshot) {
    const id = uid('t')
    update((d) => {
      d.tools.push({ ...snap, id, orgId: d.currentOrgId, version: 1, status: 'draft', published: null, updatedAt: Date.now() })
      log(d, { object: `Created tool ${snap.displayName}` })
    })
    return id
  },
  editTool(id: string, patch: Partial<ToolSnapshot>) {
    update((d) => {
      const t = toolById(d, id)
      if (!t) return
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
      if (!t?.published) return
      const { version, ...snap } = t.published
      Object.assign(t, structuredClone(snap))
      t.version = version
      t.status = 'published'
      t.updatedAt = Date.now()
    })
  },
  publishTool(id: string) {
    update((d) => {
      const t = toolById(d, id)
      if (!t) return
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
      log(d, { object: `Published ${t.displayName} v${t.version}` })
    })
  },
  deleteTool(id: string) {
    update((d) => {
      const t = toolById(d, id)
      if (!t) return
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
      d.workspaces.push({ id, orgId: d.currentOrgId, slug: 'ws_' + id.slice(-4), name: w.name, https: false, mcp: false, keyIds: w.keyIds, userIds: w.userIds, agentIds: w.agentIds, tools: [], createdAt: Date.now() })
      for (const a of d.agents) if (w.agentIds.includes(a.id) && !a.workspaceIds.includes(id)) a.workspaceIds.push(id)
      log(d, { object: `Created workspace ${w.name}` })
    })
    return id
  },
  exposeKey(wsId: string, keyId: string) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w || w.keyIds.includes(keyId)) return
      w.keyIds.push(keyId)
      log(d, { object: `Exposed ${keyById(d, keyId)?.name} in ${w.name}`, workspaceId: wsId })
    })
  },
  setWorkspaceKeys(wsId: string, keyIds: string[]) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w) return
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
      if (!w || !t) return
      const existing = w.tools.find((x) => x.toolId === toolId)
      if (existing) existing.slotMap = slotMap
      else w.tools.push({ toolId, slotMap, enabled: true, perMinute: (liveTool(t) ?? t).perMinute })
      log(d, { object: `Granted ${t.displayName} to ${w.name}`, workspaceId: wsId })
    })
  },
  updateWorkspaceTool(wsId: string, toolId: string, patch: Partial<{ enabled: boolean; perMinute: number; slotMap: Record<string, string | null> }>) {
    update((d) => {
      const w = wsById(d, wsId)
      const wt = w?.tools.find((x) => x.toolId === toolId)
      if (!w || !wt) return
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
      if (!w) return
      w.tools = w.tools.filter((x) => x.toolId !== toolId)
      log(d, { object: `Removed ${toolById(d, toolId)?.displayName} from ${w.name}`, workspaceId: wsId })
    })
  },
  setConnection(wsId: string, kind: 'https' | 'mcp', on: boolean) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w) return
      w[kind] = on
      log(d, { object: `Turned ${kind.toUpperCase()} ${on ? 'on' : 'off'} for ${w.name}`, workspaceId: wsId })
    })
  },
  setWorkspacePlayers(wsId: string, userIds: string[], agentIds: string[]) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w) return
      const refs = (u: string[], a: string[]): PlayerRef[] => [...u.map((id) => ({ kind: 'user' as const, id })), ...a.map((id) => ({ kind: 'agent' as const, id }))]
      const diff = playerDiff(d, refs(w.userIds, w.agentIds), refs(userIds, agentIds))
      if (diff.detail.length) {
        const one = diff.added.length + diff.removed.length === 1
        const object = one ? (diff.added.length ? `Added ${diff.added[0]} to ${w.name}` : `Removed ${diff.removed[0]} from ${w.name}`) : `Changed who can use ${w.name}`
        log(d, { object, workspaceId: wsId, detail: diff.detail })
      }
      w.userIds = userIds
      w.agentIds = agentIds
      for (const a of d.agents) {
        const has = a.workspaceIds.includes(wsId)
        if (agentIds.includes(a.id) && !has) a.workspaceIds.push(wsId)
        if (!agentIds.includes(a.id) && has) a.workspaceIds = a.workspaceIds.filter((x) => x !== wsId)
      }
    })
  },
  deleteWorkspace(wsId: string) {
    update((d) => {
      const w = wsById(d, wsId)
      if (!w) return
      d.workspaces = d.workspaces.filter((x) => x.id !== wsId)
      d.cabinets = d.cabinets.filter((c) => c.workspaceId !== wsId)
      for (const a of d.agents) a.workspaceIds = a.workspaceIds.filter((x) => x !== wsId)
      // Connectors are enrolled into a workspace, so they're revoked with it; stores routed through them lose their route.
      const conns = d.connectors.filter((c) => c.workspaceId === wsId)
      const routed = d.stores.filter((s) => conns.some((c) => c.id === s.route))
      d.connectors = d.connectors.filter((c) => c.workspaceId !== wsId)
      for (const s of routed) s.health = 'unreachable'
      const detail: [string, string][] = []
      if (conns.length) detail.push(['Connectors revoked', conns.map((c) => c.name).join(', ')])
      if (routed.length) detail.push(['Stores now unreachable', routed.map((s) => s.name).join(', ')])
      log(d, { object: `Deleted workspace ${w.name}`, detail: detail.length ? detail : undefined })
    })
  },

  /* Agents */
  createAgent(a: { label: string; workspaceIds: string[]; expiryDays: number | null; rateLimit: number | null }) {
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
        workspaceIds: a.workspaceIds,
        rateLimit: a.rateLimit,
      }
      d.agents.push(agent)
      for (const w of d.workspaces) if (a.workspaceIds.includes(w.id) && !w.agentIds.includes(id)) w.agentIds.push(id)
      log(d, { object: `Created agent ${a.label}` })
    })
    return { id, token }
  },
  rotateAgent(id: string) {
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
      if (!a || !label.trim() || label.trim() === a.label) return
      log(d, { object: `Renamed agent ${a.label} → ${label.trim()}` })
      a.label = label.trim()
    })
  },
  setAgentStatus(id: string, status: 'active' | 'suspended') {
    update((d) => {
      const a = agentById(d, id)
      if (!a || a.status === 'revoked') return
      a.status = status
      log(d, { object: `${status === 'suspended' ? 'Suspended' : 'Resumed'} ${a.label}` })
    })
  },
  revokeAgent(id: string) {
    update((d) => {
      const a = agentById(d, id)
      if (!a) return
      a.status = 'revoked'
      log(d, { object: `Revoked token for ${a.label}` })
    })
    // The agent doesn't know yet — its next attempt shows up blocked.
    setTimeout(() => {
      update((d) => {
        const a = agentById(d, id)
        if (!a) return
        const ws = wsById(d, a.workspaceIds[0])
        log(d, {
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
  invite(i: { email: string; role: Role; workspaceIds: string[] }) {
    update((d) => {
      const id = uid('inv')
      d.invites.push({ id, orgId: d.currentOrgId, email: i.email, role: i.role, workspaceIds: i.workspaceIds, invitedBy: me(d).name, invitedAt: Date.now(), expiresAt: Date.now() + 7 * DAY })
      let u = d.users.find((x) => x.email === i.email)
      if (!u) {
        u = { id: uid('u'), name: i.email.split('@')[0].replace(/^./, (c) => c.toUpperCase()), email: i.email, roles: {}, status: 'invited', locked: false, lastActive: null, sessions: [] }
        d.users.push(u)
      }
      ;(d.incomingInvites[u.id] ??= []).push({ id, orgName: org(d)!.name, orgId: d.currentOrgId, invitedBy: me(d).name, role: i.role, expiresAt: Date.now() + 7 * DAY })
      log(d, { object: `Invited ${i.email} as ${i.role}` })
    })
  },
  resendInvite(inviteId: string) {
    update((d) => {
      const inv = d.invites.find((x) => x.id === inviteId)
      if (!inv) return
      inv.expiresAt = Date.now() + 7 * DAY
      log(d, { object: `Resent invite to ${inv.email}`, orgId: inv.orgId })
    })
  },
  revokeInvite(inviteId: string) {
    update((d) => {
      const inv = d.invites.find((x) => x.id === inviteId)
      if (!inv) return
      d.invites = d.invites.filter((x) => x.id !== inviteId)
      for (const k in d.incomingInvites) d.incomingInvites[k] = d.incomingInvites[k].filter((x) => x.id !== inviteId)
      const u = d.users.find((x) => x.email === inv.email)
      if (u && !Object.keys(u.roles).length) d.users = d.users.filter((x) => x.id !== u.id)
      log(d, { object: `Revoked invite for ${inv.email}` })
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
      for (const w of d.workspaces) if (inv?.workspaceIds.includes(w.id) && !w.userIds.includes(u.id)) w.userIds.push(u.id)
      d.invites = d.invites.filter((x) => x.id !== inviteId)
      d.incomingInvites[u.id] = d.incomingInvites[u.id].filter((x) => x.id !== inviteId)
      d.currentOrgId = inc.orgId
      log(d, { object: `${u.name} joined as ${inc.role}`, orgId: inc.orgId })
    })
  },
  declineInvite(inviteId: string) {
    update((d) => {
      const u = me(d)
      d.incomingInvites[u.id] = (d.incomingInvites[u.id] ?? []).filter((x) => x.id !== inviteId)
      const inv = d.invites.find((x) => x.id === inviteId)
      d.invites = d.invites.filter((x) => x.id !== inviteId)
      if (inv) log(d, { object: `${u.email} declined the invite`, orgId: inv.orgId })
    })
  },
  changeRole(userId: string, role: Role) {
    update((d) => {
      const u = userById(d, userId)
      if (!u) return
      const before = u.roles[d.currentOrgId]
      if (before === role) return
      u.roles[d.currentOrgId] = role
      log(d, { object: `Changed ${u.name}'s role to ${role}`, detail: [['Role', `${before} → ${role}`]] })
    })
  },
  setUserSuspended(userId: string, suspended: boolean) {
    update((d) => {
      const u = userById(d, userId)
      if (!u) return
      u.status = suspended ? 'suspended' : 'active'
      log(d, { object: `${suspended ? 'Suspended' : 'Reactivated'} ${u.name}` })
    })
  },
  setUserWorkspaces(userId: string, wsIds: string[]) {
    update((d) => {
      const name = userById(d, userId)?.name
      // One entry per workspace, so each workspace's own log shows who joined or left it.
      for (const w of d.workspaces.filter((x) => x.orgId === d.currentOrgId)) {
        const has = w.userIds.includes(userId)
        if (wsIds.includes(w.id) && !has) {
          w.userIds.push(userId)
          log(d, { object: `Added ${name} to ${w.name}`, workspaceId: w.id })
        }
        if (!wsIds.includes(w.id) && has) {
          w.userIds = w.userIds.filter((x) => x !== userId)
          log(d, { object: `Removed ${name} from ${w.name}`, workspaceId: w.id })
        }
      }
    })
  },
  removeUser(userId: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u) return
      delete u.roles[d.currentOrgId]
      for (const w of d.workspaces) w.userIds = w.userIds.filter((x) => x !== userId)
      for (const c of d.cabinets) {
        if (c.ownerId === userId) c.ownerId = null
        if (Array.isArray(c.access)) c.access = c.access.filter((p) => !(p.kind === 'user' && p.id === userId))
      }
      if (!Object.keys(u.roles).length) d.users = d.users.filter((x) => x.id !== userId)
      log(d, { object: `Removed ${u.name} from the organization` })
    })
  },

  /* Cabinets */
  createCabinet(c: { workspaceId: string; name: string; newKeys: { name: string; length: number }[]; pickedKeyIds: string[]; tools: Cabinet['tools']; access: Cabinet['access'] }) {
    const id = uid('cb')
    update((d) => {
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
      d.cabinets.push({ id, workspaceId: c.workspaceId, name: c.name, ownerId: d.currentUserId, keyIds, tools, access: c.access, createdAt: Date.now() })
      log(d, { object: `Created cabinet ${c.name}${c.access === 'everyone' ? '' : ' (locked)'}`, workspaceId: c.workspaceId })
    })
    return id
  },
  setCabinetAccess(id: string, access: 'everyone' | PlayerRef[]) {
    update((d) => {
      const c = d.cabinets.find((x) => x.id === id)
      if (!c) return
      const label = (a: Cabinet['access']) => (a === 'everyone' ? 'Everyone in the workspace' : `Only ${plural(a.length, 'player')}`)
      const detail: [string, string][] = [['Lock', `${label(c.access)} → ${label(access)}`], ...playerDiff(d, c.access === 'everyone' ? [] : c.access, access === 'everyone' ? [] : access).detail]
      c.access = access
      log(d, { object: `Changed lock on ${c.name}`, workspaceId: c.workspaceId, detail })
    })
  },
  reassignCabinet(id: string, ownerId: string) {
    update((d) => {
      const c = d.cabinets.find((x) => x.id === id)
      if (!c) return
      c.ownerId = ownerId
      log(d, { object: `Reassigned ${c.name} to ${userById(d, ownerId)?.name}`, workspaceId: c.workspaceId })
    })
  },
  deleteCabinet(id: string) {
    update((d) => {
      const c = d.cabinets.find((x) => x.id === id)
      if (!c) return
      d.cabinets = d.cabinets.filter((x) => x.id !== id)
      d.keys = d.keys.filter((k) => k.cabinetId !== id)
      log(d, { object: `Deleted cabinet ${c.name}`, workspaceId: c.workspaceId })
    })
  },

  /* Connectors */
  enrollConnector(c: { name: string; workspaceId: string }) {
    const id = uid('cn')
    update((d) => {
      d.connectors.push({ id, orgId: d.currentOrgId, name: c.name, workspaceId: c.workspaceId, version: '1.4.2', health: 'healthy', lastSeen: Date.now(), ip: `10.2.14.${20 + Math.floor(Math.random() * 60)}`, enrolledBy: me(d).name.replace(/(\w+) (\w).*/, '$1 $2.'), enrolledAt: Date.now() })
      log(d, { type: 'connection', severity: 'ok', actor: c.name, actorKind: 'connector', object: 'Connector enrolled', workspaceId: c.workspaceId, result: 'First heartbeat' })
    })
    return id
  },
  renameConnector(id: string, name: string) {
    update((d) => {
      const c = d.connectors.find((x) => x.id === id)
      if (c && name.trim()) c.name = name.trim()
    })
  },
  revokeConnector(id: string) {
    update((d) => {
      const c = d.connectors.find((x) => x.id === id)
      if (!c) return
      d.connectors = d.connectors.filter((x) => x.id !== id)
      for (const s of d.stores) if (s.route === id) s.health = 'unreachable'
      log(d, { object: `Revoked connector ${c.name}` })
    })
  },

  /* Verify + first call */
  landFirstCall(wsId: string, agentId: string | null) {
    const trk = trackingCode()
    const ms = 140 + Math.floor(Math.random() * 90)
    update((d) => {
      const w = wsById(d, wsId)
      const a = agentId ? agentById(d, agentId) : null
      const wt = w?.tools.find((t) => t.enabled && liveTool(toolById(d, t.toolId)) && !missingSlots(d, w, t).length)
      const t = wt ? liveTool(toolById(d, wt.toolId)) : null
      const act = t?.actions[0]
      if (a) a.lastUsedAt = Date.now()
      const detail: [string, string][] = [['Action', act ? `${act.method} ${act.path}` : '—'], ['Workspace', w?.name ?? '—'], ['Route', 'Direct']]
      log(d, { type: 'request', severity: 'ok', actor: a?.label ?? 'agent', actorKind: 'agent', actorId: a?.id, object: t?.displayName ?? 'MCP tools/list', destination: t ? new URL(t.baseUrl).host : undefined, workspaceId: wsId, result: `200 · ${ms} ms`, trk, detail })
      log(d, { type: 'verification', severity: 'ok', actor: a?.label ?? 'agent', actorKind: 'agent', actorId: a?.id, object: 'First request verified', workspaceId: wsId, result: `200 · ${ms} ms`, trk: trackingCode() })
      if (!d.firstCallAt) d.firstCallAt = Date.now()
    })
    return { trk, ms }
  },

  /* Support */
  /** Support acts on an account only with a reason or ticket ID, which every affected organization sees. */
  setLocked(userId: string, locked: boolean, reason: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u) return
      u.locked = locked
      u.lockedAt = locked ? Date.now() : undefined
      u.lockReason = locked ? reason : undefined
      u.unlockRequest = undefined
      if (locked) u.sessions = []
      for (const orgId of Object.keys(u.roles))
        log(d, { orgId, type: 'support', actor: 'Keyhole support', actorKind: 'support', actorId: 'support', object: `${locked ? 'Locked' : 'Unlocked'} account ${u.email}`, detail: [['Reason or ticket', reason]] })
    })
  },
  signOutEverywhere(userId: string, reason?: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u) return
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
            object: `Signed ${u.email} out everywhere (${was} session${was === 1 ? '' : 's'})`,
            detail: reason ? [['Reason or ticket', reason]] : undefined,
          })
    })
  },
  /** Only support can unlock; an admin can ask for it, and support sees the request. */
  requestUnlock(userId: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u?.locked) return
      u.unlockRequest = { by: `${me(d).name} · ${org(d)?.name}`, at: Date.now() }
      log(d, { object: `Asked Keyhole support to unlock ${u.email}` })
    })
  },
  supportResendInvite(userId: string) {
    update((d) => {
      const u = userById(d, userId)
      if (!u) return
      for (const inv of d.invites.filter((x) => x.email === u.email)) {
        inv.expiresAt = Date.now() + 7 * DAY
        log(d, { orgId: inv.orgId, type: 'support', actor: 'Keyhole support', actorKind: 'support', actorId: 'support', object: `Resent invite to ${u.email}` })
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
      if (!o || !name.trim()) return
      log(d, { object: `Renamed organization ${o.name} → ${name.trim()}` })
      o.name = name.trim()
    })
  },
  deleteOrg() {
    update((d) => {
      const id = d.currentOrgId
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
