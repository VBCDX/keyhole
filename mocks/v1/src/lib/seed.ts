import { CATALOG } from './catalog'
import { DAY, HOUR, MIN } from './format'
import type { AuditEvent, DB, Tool } from './types'

const clone = <T,>(v: T): T => JSON.parse(JSON.stringify(v))

const DEFAULT_NOTIFICATIONS = {
  connectorOffline: true,
  storeUnhealthy: true,
  verificationFailed: true,
  tokenExpiring: true,
  blockedSpike: false,
  memberJoined: false,
}

const dana = (now: number) => ({
  id: 'u_dana',
  name: 'Dana Keller',
  email: 'dana@acme.com',
  roles: { org_acme: 'Owner' as const },
  status: 'active' as const,
  locked: false,
  lastActive: now,
  sessions: [
    { device: 'MacBook Pro', place: 'San Francisco', at: now },
    { device: 'iPhone', place: 'San Francisco', at: now - 2 * HOUR },
  ],
})

export function freshDB(): DB {
  const now = Date.now()
  return {
    scenario: 'fresh',
    currentUserId: 'u_dana',
    currentOrgId: 'org_acme',
    checklistDismissed: false,
    configDownloadedAt: null,
    firstCallAt: null,
    orgs: [{ id: 'org_acme', name: 'Acme Corp', createdAt: now - 2 * MIN }],
    users: [dana(now)],
    invites: [],
    incomingInvites: {},
    stores: [{ id: 'st_local', orgId: 'org_acme', type: 'local', name: 'Local store', health: 'healthy', checkedAt: now }],
    keys: [],
    tools: [],
    workspaces: [],
    agents: [],
    cabinets: [],
    connectors: [],
    events: [
      {
        id: 'ev_0',
        at: now - 2 * MIN,
        orgId: 'org_acme',
        type: 'admin',
        severity: 'info',
        actor: 'Dana Keller',
        actorKind: 'user',
        actorId: 'u_dana',
        object: 'Created organization Acme Corp',
        result: 'Done',
        trk: 'trk_0rg1n1t0',
      },
    ],
    notifications: { ...DEFAULT_NOTIFICATIONS },
    webhook: '',
    listState: 'normal',
    statsBase: { requests: 0, blocked: 0 },
  }
}

export function populatedDB(): DB {
  const now = Date.now()
  const stripe = CATALOG[0].tool
  const github = CATALOG[1].tool
  const slack = CATALOG[2].tool

  const tools: Tool[] = [
    {
      ...clone(stripe),
      id: 't_stripe',
      orgId: 'org_acme',
      version: 2,
      status: 'published',
      published: { ...clone(stripe), version: 2 },
      updatedAt: now - 3 * DAY,
      catalog: 'stripe',
    },
    {
      ...clone(github),
      id: 't_github',
      orgId: 'org_acme',
      version: 1,
      status: 'published',
      published: { ...clone(github), version: 1 },
      updatedAt: now - 9 * DAY,
      catalog: 'github',
    },
    {
      ...clone(slack),
      id: 't_slack',
      orgId: 'org_acme',
      version: 1,
      status: 'draft',
      published: null,
      updatedAt: now - 1 * DAY,
      catalog: 'slack',
    },
  ]

  const ev = (
    id: string,
    ago: number,
    e: Omit<AuditEvent, 'id' | 'at' | 'orgId'>,
  ): AuditEvent => ({ id, at: now - ago, orgId: 'org_acme', ...e })

  const stripeReq = {
    type: 'request' as const,
    severity: 'ok' as const,
    actorKind: 'agent' as const,
    object: 'Stripe charges',
    destination: 'api.stripe.com',
    workspaceId: 'ws_prod',
  }

  const events: AuditEvent[] = [
    ev('ev_1', 2 * MIN, { ...stripeReq, actor: 'billing-agent', actorId: 'ag_billing', result: '200 · 164 ms', trk: 'trk_2b8xm4c1', detail: [['Action', 'GET /v1/charges'], ['Workspace', 'Production'], ['Route', 'Direct']] }),
    ev('ev_2', 2 * MIN + 27_000, {
      type: 'blocked',
      severity: 'blocked',
      actorKind: 'agent',
      actor: 'ops-agent',
      actorId: 'ag_ops',
      object: 'Stripe charges',
      destination: 'api.stripe.com',
      workspaceId: 'ws_prod',
      result: 'Blocked',
      trk: 'trk_9k2fp7d3',
      reason: 'Blocked: path /v1/payouts isn\'t allowed for the tool "Stripe charges".',
      detail: [['Attempted', 'GET /v1/payouts'], ['Allowed paths', '/v1/charges · /v1/charges/{id}'], ['Fix', 'Add the action to the tool, or point the agent at an allowed path.']],
      fix: { label: 'Open the tool’s actions', to: '/tools/t_stripe?section=actions' },
    }),
    ev('ev_3', 3 * MIN, { type: 'request', severity: 'ok', actorKind: 'agent', actor: 'docs-agent', actorId: 'ag_docs', object: 'GitHub issues', destination: 'api.github.com', workspaceId: 'ws_prod', result: '201 · 340 ms', trk: 'trk_6d9sk3m2', detail: [['Action', 'POST /repos/{owner}/{repo}/issues'], ['Workspace', 'Production'], ['Route', 'Direct']] }),
    ev('ev_4', 18 * MIN, { type: 'connection', severity: 'warn', actorKind: 'connector', actor: 'edge-02', object: 'Heartbeat late — 2 of the last 5 missed', workspaceId: 'ws_staging', result: 'Degraded', trk: 'trk_4h8cz2p1' }),
    ev('ev_5', 24 * MIN, { ...stripeReq, actor: 'billing-agent', actorId: 'ag_billing', result: '200 · 182 ms', trk: 'trk_8f2ka91x', detail: [['Action', 'GET /v1/charges'], ['Workspace', 'Production'], ['Route', 'Direct']] }),
    ev('ev_6', 47 * MIN, { type: 'request', severity: 'ok', actorKind: 'agent', actor: 'docs-agent', actorId: 'ag_docs', object: 'GitHub issues', destination: 'api.github.com', workspaceId: 'ws_staging', result: '200 · 212 ms', trk: 'trk_1q7vm3x8', detail: [['Action', 'GET /repos/{owner}/{repo}/issues'], ['Workspace', 'Staging'], ['Route', 'Connector edge-02']] }),
    ev('ev_7', 1 * HOUR + 5 * MIN, { type: 'error', severity: 'warn', actorKind: 'agent', actor: 'docs-agent', actorId: 'ag_docs', object: 'GitHub issues', destination: 'api.github.com', workspaceId: 'ws_staging', result: '502 · upstream timeout', trk: 'trk_5r2kd9w4', reason: 'The destination didn’t answer within 10 s. Keyhole did not retry.' }),
    ev('ev_8', 2 * HOUR, { type: 'admin', severity: 'info', actorKind: 'user', actor: 'Ravi Mehta', actorId: 'u_ravi', object: 'Granted GitHub issues to Staging', result: 'Done', trk: 'trk_3m6pt8k2' }),
    ev('ev_9', 3 * HOUR, { type: 'admin', severity: 'info', actorKind: 'user', actor: 'Mia Chen', actorId: 'u_mia', workspaceId: 'ws_prod', object: "Created cabinet Mia's billing set (locked)", result: 'Done', trk: 'trk_7x2nb4q9' }),
    ev('ev_10', 2 * DAY, { type: 'admin', severity: 'info', actorKind: 'user', actor: 'Dana Keller', actorId: 'u_dana', object: 'Invited sam@acme.com as user', result: 'Done', trk: 'trk_9b3ws6h1' }),
    ev('ev_11', 3 * DAY, { type: 'verification', severity: 'ok', actorKind: 'agent', actor: 'billing-agent', actorId: 'ag_billing', object: 'First request verified', workspaceId: 'ws_prod', result: '200 · 182 ms', trk: 'trk_1v8rf5k3' }),
    ev('ev_12', 3 * DAY + HOUR, { type: 'connection', severity: 'ok', actorKind: 'connector', actor: 'edge-01', object: 'Connector enrolled', workspaceId: 'ws_prod', result: 'First heartbeat', trk: 'trk_2e7yl4c6' }),
  ]

  return {
    scenario: 'populated',
    currentUserId: 'u_dana',
    currentOrgId: 'org_acme',
    checklistDismissed: true,
    configDownloadedAt: now - 3 * DAY,
    firstCallAt: now - 3 * DAY,
    orgs: [
      { id: 'org_acme', name: 'Acme Corp', createdAt: now - 60 * DAY },
      { id: 'org_nw', name: 'Northwind Labs', createdAt: now - 200 * DAY },
    ],
    users: [
      dana(now),
      { id: 'u_ravi', name: 'Ravi Mehta', email: 'ravi@acme.com', roles: { org_acme: 'userAdmin' }, status: 'active', locked: false, lastActive: now - 2 * HOUR, sessions: [{ device: 'ThinkPad', place: 'Austin', at: now - 2 * HOUR }] },
      { id: 'u_mia', name: 'Mia Chen', email: 'mia@acme.com', roles: { org_acme: 'user', org_nw: 'user' }, status: 'active', locked: false, lastActive: now - 3 * HOUR, sessions: [{ device: 'MacBook Air', place: 'Seattle', at: now - 3 * HOUR }, { device: 'Pixel 8', place: 'Seattle', at: now - 5 * HOUR }] },
      { id: 'u_jo', name: 'Jo Reyes', email: 'jo@acme.com', roles: { org_acme: 'user' }, status: 'suspended', locked: false, lastActive: now - 6 * DAY, sessions: [] },
      { id: 'u_sam', name: 'Sam Ortiz', email: 'sam@acme.com', roles: {}, status: 'invited', locked: false, lastActive: null, sessions: [{ device: 'Chromebook', place: 'Denver', at: now - 10 * MIN }] },
    ],
    invites: [
      { id: 'inv_sam', orgId: 'org_acme', email: 'sam@acme.com', role: 'user', workspaceIds: ['ws_prod'], invitedBy: 'Dana Keller', invitedAt: now - 2 * DAY, expiresAt: now + 5 * DAY },
    ],
    incomingInvites: {
      u_sam: [
        { id: 'inv_sam', orgName: 'Acme Corp', orgId: 'org_acme', invitedBy: 'Dana Keller', role: 'user', expiresAt: now + 5 * DAY },
        { id: 'inv_nw_old', orgName: 'Northwind Labs', orgId: null, invitedBy: 'Sam Ortiz', role: 'user', expiresAt: now - 3 * DAY },
      ],
    },
    stores: [
      { id: 'st_local', orgId: 'org_acme', type: 'local', name: 'Local store', health: 'healthy', checkedAt: now - 40_000 },
      { id: 'st_bao', orgId: 'org_acme', type: 'openbao', name: 'Payments vault', health: 'healthy', checkedAt: now - 4 * MIN, address: 'https://bao.acme.internal:8200', route: 'cn_edge01', auth: 'approle', path: 'secret/data/payments/', cacheSeconds: 60, certName: 'vault-ca.pem', skipVerify: false },
    ],
    keys: [
      { id: 'k_stripe', orgId: 'org_acme', name: 'stripe_secret', storeId: 'st_local', length: 32, createdAt: now - 20 * DAY, rotatedAt: now - 6 * DAY, expiresAt: null, rotationReminderDays: 90, notes: 'Restricted key — charges read only' },
      { id: 'k_slack', orgId: 'org_acme', name: 'SLACK_BOT_TOKEN', storeId: 'st_local', length: 57, createdAt: now - 12 * DAY, rotatedAt: null, expiresAt: now + 10 * DAY, rotationReminderDays: null, notes: '' },
      { id: 'k_gh', orgId: 'org_acme', name: 'github_token', storeId: 'st_local', length: 40, createdAt: now - 30 * DAY, rotatedAt: null, expiresAt: now + 80 * DAY, rotationReminderDays: 30, notes: 'Fine-grained PAT, issues only' },
      { id: 'k_mia1', orgId: 'org_acme', name: 'mia_stripe_test', storeId: 'st_local', length: 32, createdAt: now - 3 * HOUR, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: 'Cabinet key', cabinetId: 'cb_mia' },
      { id: 'k_mia2', orgId: 'org_acme', name: 'mia_webhook', storeId: 'st_local', length: 38, createdAt: now - 3 * HOUR, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: 'Cabinet key', cabinetId: 'cb_mia' },
      { id: 'k_legacy_c', orgId: 'org_acme', name: 'old_mailgun', storeId: 'st_local', length: 36, createdAt: now - 90 * DAY, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: 'Cabinet key', cabinetId: 'cb_legacy' },
      { id: 'k_bao1', orgId: 'org_acme', name: 'payments/api_key', storeId: 'st_bao', length: 48, createdAt: now - 14 * DAY, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: '' },
      { id: 'k_bao2', orgId: 'org_acme', name: 'payments/webhook_secret', storeId: 'st_bao', length: 44, createdAt: now - 14 * DAY, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: '' },
      { id: 'k_bao3', orgId: 'org_acme', name: 'payments/refund_key', storeId: 'st_bao', length: 48, createdAt: now - 14 * DAY, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: '' },
      { id: 'k_bao4', orgId: 'org_acme', name: 'payments/ledger_token', storeId: 'st_bao', length: 36, createdAt: now - 14 * DAY, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: '' },
      { id: 'k_old', orgId: 'org_acme', name: 'billing/api_key', storeId: 'st_removed', length: 40, createdAt: now - 70 * DAY, rotatedAt: null, expiresAt: null, rotationReminderDays: null, notes: 'From the retired "Old vault" store', sourceRemoved: true },
    ],
    tools,
    workspaces: [
      {
        id: 'ws_prod', orgId: 'org_acme', slug: 'ws_1a2b', name: 'Production', https: false, mcp: true,
        keyIds: ['k_stripe', 'k_gh', 'k_bao1', 'k_old'],
        userIds: ['u_dana', 'u_ravi', 'u_mia'],
        agentIds: ['ag_billing', 'ag_ops', 'ag_docs', 'ag_report'],
        tools: [
          { toolId: 't_stripe', slotMap: { stripe_secret: 'k_stripe' }, enabled: true, perMinute: 60 },
          { toolId: 't_github', slotMap: { github_token: 'k_gh' }, enabled: true, perMinute: 30 },
        ],
        createdAt: now - 50 * DAY,
      },
      {
        id: 'ws_staging', orgId: 'org_acme', slug: 'ws_7c3d', name: 'Staging', https: true, mcp: false,
        keyIds: ['k_slack', 'k_gh'],
        userIds: ['u_ravi', 'u_jo'],
        agentIds: ['ag_docs'],
        tools: [{ toolId: 't_github', slotMap: { github_token: 'k_gh' }, enabled: true, perMinute: 30 }],
        createdAt: now - 40 * DAY,
      },
      {
        id: 'ws_sandbox', orgId: 'org_acme', slug: 'ws_5e9f', name: 'Sandbox', https: false, mcp: false,
        keyIds: [], userIds: ['u_ravi'], agentIds: ['ag_oldci'], tools: [], createdAt: now - 20 * DAY,
      },
    ],
    agents: [
      { id: 'ag_billing', orgId: 'org_acme', label: 'billing-agent', tokenLast4: '6TpE', status: 'active', createdAt: now - 3 * DAY, createdBy: 'Dana Keller', expiresAt: now + 87 * DAY, lastUsedAt: now - 2 * MIN, workspaceIds: ['ws_prod'], rateLimit: 60 },
      { id: 'ag_ops', orgId: 'org_acme', label: 'ops-agent', tokenLast4: 'r7Qm', status: 'active', createdAt: now - 81 * DAY, createdBy: 'Ravi Mehta', expiresAt: now + 9 * DAY, lastUsedAt: now - 2 * MIN - 27_000, workspaceIds: ['ws_prod'], rateLimit: 30 },
      { id: 'ag_docs', orgId: 'org_acme', label: 'docs-agent', tokenLast4: 'X2dk', status: 'active', createdAt: now - 25 * DAY, createdBy: 'Ravi Mehta', expiresAt: null, lastUsedAt: now - 3 * MIN, workspaceIds: ['ws_prod', 'ws_staging'], rateLimit: null },
      { id: 'ag_report', orgId: 'org_acme', label: 'reporting-bot', tokenLast4: 'hN4s', status: 'suspended', createdAt: now - 45 * DAY, createdBy: 'Dana Keller', expiresAt: now + 200 * DAY, lastUsedAt: now - 8 * DAY, workspaceIds: ['ws_prod'], rateLimit: 10 },
      { id: 'ag_oldci', orgId: 'org_acme', label: 'old-ci', tokenLast4: 'Pw9a', status: 'revoked', createdAt: now - 120 * DAY, createdBy: 'Ravi Mehta', expiresAt: null, lastUsedAt: now - 30 * DAY, workspaceIds: ['ws_sandbox'], rateLimit: null },
    ],
    cabinets: [
      { id: 'cb_mia', workspaceId: 'ws_prod', name: "Mia's billing set", createdBy: 'u_mia', managedBy: 'u_mia', keyIds: ['k_mia1', 'k_mia2'], tools: [{ toolId: 't_stripe', slotMap: { stripe_secret: 'k_mia1' } }], access: [{ kind: 'user', id: 'u_mia' }, { kind: 'agent', id: 'ag_billing' }], createdAt: now - 3 * HOUR },
      { id: 'cb_team', workspaceId: 'ws_prod', name: 'Team sandbox', createdBy: 'u_ravi', managedBy: 'u_ravi', keyIds: ['k_gh'], tools: [{ toolId: 't_github', slotMap: { github_token: 'k_gh' } }, { toolId: 't_stripe', slotMap: { stripe_secret: null } }], access: 'everyone', createdAt: now - 10 * DAY },
      { id: 'cb_legacy', workspaceId: 'ws_prod', name: 'Legacy imports', createdBy: null, managedBy: null, keyIds: ['k_legacy_c'], tools: [], access: 'everyone', createdAt: now - 90 * DAY },
    ],
    connectors: [
      { id: 'cn_edge01', orgId: 'org_acme', name: 'edge-01', workspaceId: 'ws_prod', version: '1.4.2', health: 'healthy', lastSeen: now - 20_000, ip: '10.2.14.7', enrolledBy: 'Dana K.', enrolledAt: now - 3 * DAY },
      { id: 'cn_edge02', orgId: 'org_acme', name: 'edge-02', workspaceId: 'ws_staging', version: '1.4.0', health: 'degraded', lastSeen: now - 90_000, ip: '10.2.14.9', enrolledBy: 'Dana K.', enrolledAt: now - 3 * DAY },
      { id: 'cn_lab', orgId: 'org_acme', name: 'lab-runner', workspaceId: 'ws_sandbox', version: '1.3.9', health: 'offline', lastSeen: now - 2 * HOUR, ip: null, enrolledBy: 'Ravi M.', enrolledAt: now - 14 * DAY },
    ],
    events,
    notifications: { ...DEFAULT_NOTIFICATIONS },
    webhook: '',
    listState: 'normal',
    statsBase: { requests: 4176, blocked: 6 },
  }
}
