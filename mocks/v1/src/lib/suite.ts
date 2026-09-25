/**
 * VBCDX suite — the shared seed.
 *
 * Keyhole and Dispatch seed the same organizations, people, workspaces, workspace memberships and
 * agent records, with the same IDs, names and emails (see README › Shared with Dispatch). Keep this
 * file identical in both repos. Each app layers its own content on top (Keyhole: stores, keys,
 * tools, connectors, tokens; Dispatch: messages, context, webhooks, tokens).
 */
import type { Role } from './types'

export type SuiteWorkspaceRole = 'member' | 'admin'

export const SUITE_ORGS = [
  { id: 'org_acme', name: 'Acme Corp' },
  { id: 'org_nw', name: 'Northwind Labs' },
] as const

export const SUITE_USERS: {
  id: string
  name: string
  email: string
  roles: Record<string, Role>
  /** Organizations that suspended this membership. */
  suspended?: Record<string, true>
}[] = [
  { id: 'u_dana', name: 'Dana Keller', email: 'dana@acme.com', roles: { org_acme: 'Owner' } },
  { id: 'u_ravi', name: 'Ravi Mehta', email: 'ravi@acme.com', roles: { org_acme: 'userAdmin' } },
  { id: 'u_mia', name: 'Mia Chen', email: 'mia@acme.com', roles: { org_acme: 'user', org_nw: 'user' } },
  // Keyhole seeds Sam as a pending invite to Acme, for its invitation flow.
  { id: 'u_sam', name: 'Sam Ortiz', email: 'sam@acme.com', roles: { org_acme: 'user' } },
  { id: 'u_jo', name: 'Jo Reyes', email: 'jo@acme.com', roles: { org_acme: 'user' }, suspended: { org_acme: true } },
  { id: 'u_leo', name: 'Leo Park', email: 'leo@northwind.dev', roles: { org_nw: 'Owner' } },
  { id: 'u_noor', name: 'Noor Haddad', email: 'noor@northwind.dev', roles: { org_nw: 'userAdmin' } },
]

/**
 * Workspaces and who is in them. `role: 'admin'` is an explicit workspace admin; when a workspace has
 * none, the org's active Owners and userAdmins are its admins by default (permission model, rule 2).
 */
export const SUITE_WORKSPACES: {
  id: string
  orgId: string
  name: string
  members: { userId: string; role: SuiteWorkspaceRole }[]
  agentIds: string[]
}[] = [
  {
    id: 'ws_prod',
    orgId: 'org_acme',
    name: 'Production',
    members: [
      { userId: 'u_dana', role: 'member' },
      { userId: 'u_ravi', role: 'member' },
      { userId: 'u_mia', role: 'member' },
    ],
    // Dispatch's former "Release train" agents (planner, builder, reviewer, deployer) live here.
    agentIds: ['ag_billing', 'ag_ops', 'ag_docs', 'ag_report', 'ag_planner', 'ag_builder', 'ag_reviewer', 'ag_deployer'],
  },
  {
    id: 'ws_staging',
    orgId: 'org_acme',
    name: 'Staging',
    members: [
      { userId: 'u_ravi', role: 'member' },
      { userId: 'u_jo', role: 'member' },
    ],
    agentIds: ['ag_docs'],
  },
  { id: 'ws_sandbox', orgId: 'org_acme', name: 'Sandbox', members: [{ userId: 'u_ravi', role: 'member' }], agentIds: ['ag_oldci'] },
  {
    id: 'ws_incidents',
    orgId: 'org_acme',
    name: 'Incidents',
    members: [
      { userId: 'u_ravi', role: 'member' },
      { userId: 'u_mia', role: 'admin' },
    ],
    agentIds: ['ag_triage', 'ag_scraper'],
  },
  {
    id: 'ws_docs',
    orgId: 'org_nw',
    name: 'Docs site',
    members: [
      { userId: 'u_leo', role: 'member' },
      { userId: 'u_noor', role: 'member' },
      { userId: 'u_mia', role: 'member' },
    ],
    agentIds: ['ag_docsbot'],
  },
]

/** One agent record across the suite. Credentials stay per app (kh_live_… in Keyhole, dsp_agent_… in Dispatch). */
export const SUITE_AGENTS: { id: string; orgId: string; label: string; createdBy: string }[] = [
  { id: 'ag_billing', orgId: 'org_acme', label: 'billing-agent', createdBy: 'Dana Keller' },
  { id: 'ag_ops', orgId: 'org_acme', label: 'ops-agent', createdBy: 'Ravi Mehta' },
  { id: 'ag_docs', orgId: 'org_acme', label: 'docs-agent', createdBy: 'Ravi Mehta' },
  { id: 'ag_planner', orgId: 'org_acme', label: 'planner', createdBy: 'Ravi Mehta' },
  { id: 'ag_builder', orgId: 'org_acme', label: 'builder', createdBy: 'Ravi Mehta' },
  { id: 'ag_reviewer', orgId: 'org_acme', label: 'reviewer', createdBy: 'Ravi Mehta' },
  { id: 'ag_deployer', orgId: 'org_acme', label: 'deployer', createdBy: 'Dana Keller' },
  { id: 'ag_scraper', orgId: 'org_acme', label: 'web-scraper', createdBy: 'Ravi Mehta' },
  { id: 'ag_triage', orgId: 'org_acme', label: 'triage-bot', createdBy: 'Dana Keller' },
  { id: 'ag_report', orgId: 'org_acme', label: 'reporting-bot', createdBy: 'Dana Keller' },
  { id: 'ag_oldci', orgId: 'org_acme', label: 'old-ci', createdBy: 'Ravi Mehta' },
  { id: 'ag_docsbot', orgId: 'org_nw', label: 'docs-bot', createdBy: 'Leo Park' },
]
