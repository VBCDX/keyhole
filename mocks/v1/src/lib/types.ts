export type Role = 'Owner' | 'userAdmin' | 'user'
export type UserStatus = 'active' | 'suspended' | 'invited'

export interface Org {
  id: string
  name: string
  createdAt: number
}

export interface User {
  id: string
  name: string
  email: string
  /** Role per organization id. A person can belong to several organizations. */
  roles: Record<string, Role>
  status: UserStatus
  /** Set by Keyhole support; blocks sign-in. */
  locked: boolean
  lockedAt?: number
  lastActive: number | null
  sessions: { device: string; place: string; at: number }[]
}

export interface Invite {
  id: string
  orgId: string
  email: string
  role: Role
  workspaceIds: string[]
  invitedBy: string
  invitedAt: number
  expiresAt: number
}

export type StoreHealth = 'healthy' | 'sealed' | 'denied' | 'unreachable' | 'degraded'

export interface SecretStore {
  id: string
  orgId: string
  type: 'local' | 'openbao'
  name: string
  health: StoreHealth
  checkedAt: number
  address?: string
  /** 'public' or a connector id */
  route?: string
  auth?: 'approle' | 'kubernetes' | 'token'
  path?: string
  cacheSeconds?: number
}

export interface Key {
  id: string
  orgId: string
  name: string
  storeId: string
  /** Only the length is kept in the mock; the value is never held or shown. */
  length: number
  createdAt: number
  rotatedAt: number | null
  expiresAt: number | null
  rotationReminderDays: number | null
  notes: string
  sourceRemoved?: boolean
  /** Cabinet-owned Local key */
  cabinetId?: string
}

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export interface ToolInput {
  id: string
  name: string
  type: 'string' | 'integer' | 'number' | 'boolean'
  required: boolean
  description: string
}

export interface ToolAction {
  id: string
  method: HttpMethod
  path: string
  summary: string
  inputs: ToolInput[]
}

export interface KeySlot {
  id: string
  name: string
  location: 'header' | 'query'
  /** Header name or query parameter */
  field: string
  scheme: 'Bearer' | 'Basic' | 'None'
}

export interface ToolSnapshot {
  internalName: string
  displayName: string
  description: string
  baseUrl: string
  actions: ToolAction[]
  slots: KeySlot[]
  perMinute: number
  timeoutMs: number
}

export interface Tool extends ToolSnapshot {
  id: string
  orgId: string
  version: number
  status: 'draft' | 'published'
  /** The last published snapshot, when a draft is open on top of it. */
  published: (ToolSnapshot & { version: number }) | null
  updatedAt: number
  catalog?: 'stripe' | 'github' | 'slack'
}

export interface WorkspaceTool {
  toolId: string
  slotMap: Record<string, string | null>
  enabled: boolean
  perMinute: number
}

export interface Workspace {
  id: string
  orgId: string
  slug: string
  name: string
  https: boolean
  mcp: boolean
  keyIds: string[]
  userIds: string[]
  agentIds: string[]
  tools: WorkspaceTool[]
  createdAt: number
}

export type AgentStatus = 'active' | 'suspended' | 'revoked'

export interface Agent {
  id: string
  orgId: string
  label: string
  tokenLast4: string
  status: AgentStatus
  createdAt: number
  createdBy: string
  expiresAt: number | null
  lastUsedAt: number | null
  workspaceIds: string[]
  rateLimit: number | null
  rotatedAt?: number
}

export type PlayerRef = { kind: 'user' | 'agent'; id: string }

export interface Cabinet {
  id: string
  workspaceId: string
  name: string
  ownerId: string | null
  keyIds: string[]
  tools: { toolId: string; slotMap: Record<string, string | null> }[]
  access: 'everyone' | PlayerRef[]
  createdAt: number
}

export type ConnectorHealth = 'healthy' | 'degraded' | 'offline'

export interface Connector {
  id: string
  orgId: string
  name: string
  workspaceId: string
  version: string
  health: ConnectorHealth
  lastSeen: number
  ip: string | null
  enrolledBy: string
  enrolledAt: number
}

export type EventType =
  | 'request'
  | 'blocked'
  | 'error'
  | 'admin'
  | 'support'
  | 'connection'
  | 'verification'

export interface AuditEvent {
  id: string
  at: number
  orgId: string
  workspaceId?: string
  type: EventType
  severity: 'ok' | 'blocked' | 'warn' | 'info'
  actor: string
  actorKind: 'user' | 'agent' | 'support' | 'system' | 'connector'
  actorId?: string
  object: string
  destination?: string
  result: string
  trk: string
  reason?: string
  detail?: [string, string][]
  /** Link target for the "fix" in an expanded blocked row */
  fix?: { label: string; to: string }
}

export interface IncomingInvite {
  id: string
  orgName: string
  orgId: string | null
  invitedBy: string
  role: Role
  expiresAt: number
}

export interface DB {
  scenario: 'fresh' | 'populated'
  currentUserId: string
  currentOrgId: string
  checklistDismissed: boolean
  /** When someone first downloaded a connection config (checklist step). */
  configDownloadedAt: number | null
  firstCallAt: number | null
  orgs: Org[]
  users: User[]
  invites: Invite[]
  incomingInvites: Record<string, IncomingInvite[]>
  stores: SecretStore[]
  keys: Key[]
  tools: Tool[]
  workspaces: Workspace[]
  agents: Agent[]
  cabinets: Cabinet[]
  connectors: Connector[]
  events: AuditEvent[]
  notifications: Record<string, boolean>
  webhook: string
  /** Demo-only override to show list loading/error states. */
  listState: 'normal' | 'loading' | 'error'
  /** Pre-existing traffic volume that isn't modelled as individual rows. */
  statsBase: { requests: number; blocked: number }
}
