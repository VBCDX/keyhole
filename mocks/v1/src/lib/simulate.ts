import { trackingCode } from './format'
import { agentById, liveTool, log, missingSlots, toolById, update } from './store'
import type { DB } from './types'

/**
 * One tick of simulated live traffic for the current organization.
 * The misconfigured ops-agent keeps trying GET /v1/payouts; it turns green
 * once a published version of the Stripe tool allows that path.
 */
export function liveTick() {
  update((d: DB) => {
    const workspaces = d.workspaces.filter((w) => w.orgId === d.currentOrgId)
    const candidates: { agentId: string; wsId: string; toolId: string }[] = []
    for (const w of workspaces)
      // A tool with an empty key slot can't make calls, so it produces no traffic.
      for (const wt of w.tools.filter((x) => x.enabled && liveTool(toolById(d, x.toolId)) && !missingSlots(d, w, x).length))
        for (const aid of w.agentIds) {
          const a = agentById(d, aid)
          if (a && a.status === 'active') candidates.push({ agentId: aid, wsId: w.id, toolId: wt.toolId })
        }
    if (!candidates.length) return

    const ops = candidates.find((c) => agentById(d, c.agentId)?.label === 'ops-agent' && toolById(d, c.toolId)?.catalog === 'stripe')
    const pick = ops && Math.random() < 0.55 ? ops : candidates[Math.floor(Math.random() * candidates.length)]
    const a = agentById(d, pick.agentId)!
    const t = toolById(d, pick.toolId)!
    const ws = workspaces.find((w) => w.id === pick.wsId)!
    const eff = liveTool(t)!
    const host = new URL(eff.baseUrl).host
    a.lastUsedAt = Date.now()

    if (pick === ops) {
      const allowed = eff.actions.some((x) => x.path === '/v1/payouts')
      if (!allowed) {
        log(d, {
          type: 'blocked',
          severity: 'blocked',
          actor: a.label,
          actorKind: 'agent',
          actorId: a.id,
          object: t.displayName,
          destination: host,
          workspaceId: ws.id,
          result: 'Blocked',
          reason: `Blocked: path /v1/payouts isn't allowed for the tool "${t.displayName}".`,
          detail: [
            ['Attempted', 'GET /v1/payouts'],
            ['Allowed paths', eff.actions.map((x) => x.path).join(' · ')],
            ['Fix', 'Add the action to the tool, or point the agent at an allowed path.'],
          ],
          fix: { label: 'Open the tool’s actions', to: `/tools/${t.id}?section=actions` },
        })
        return
      }
      log(d, {
        type: 'request',
        severity: 'ok',
        actor: a.label,
        actorKind: 'agent',
        actorId: a.id,
        object: t.displayName,
        destination: host,
        workspaceId: ws.id,
        result: `200 · ${120 + Math.floor(Math.random() * 120)} ms`,
        detail: [['Action', 'GET /v1/payouts'], ['Workspace', ws.name], ['Tool version', `v${eff.version}`]],
      })
      return
    }

    const act = eff.actions[Math.floor(Math.random() * eff.actions.length)]
    const status = act.method === 'POST' ? 201 : 200
    log(d, {
      type: 'request',
      severity: 'ok',
      actor: a.label,
      actorKind: 'agent',
      actorId: a.id,
      object: t.displayName,
      destination: host,
      workspaceId: ws.id,
      result: `${status} · ${110 + Math.floor(Math.random() * 260)} ms`,
      trk: trackingCode(),
      detail: [['Action', `${act.method} ${act.path}`], ['Workspace', ws.name], ['Route', 'Direct']],
    })
  })
}

/**
 * A harmless test call from the tool editor or workspace Tools tab.
 * Workspaces test what agents get (the published version); the editor tests its draft.
 */
export function testCall(opts: { toolId: string; wsId: string; actionId?: string; missingSlot?: string | null; draft?: boolean }) {
  const trk = trackingCode()
  const ms = 90 + Math.floor(Math.random() * 180)
  let result = { ok: true, status: 200, ms, trk, message: '' }
  update((d) => {
    const tool = toolById(d, opts.toolId)
    const ws = d.workspaces.find((w) => w.id === opts.wsId)
    if (!tool || !ws) return
    const t = opts.draft ? tool : liveTool(tool)
    if (!t) {
      result = { ok: false, status: 0, ms: 0, trk, message: `${tool.displayName} isn’t published yet, so agents can’t call it.` }
      log(d, { type: 'error', severity: 'warn', object: `Test call · ${tool.displayName}`, workspaceId: ws.id, result: 'Not published', trk, reason: result.message })
      return
    }
    const act = t.actions.find((x) => x.id === opts.actionId) ?? t.actions[0]
    if (opts.missingSlot) {
      result = { ok: false, status: 0, ms: 0, trk, message: `Slot ${opts.missingSlot} has no key in ${ws.name}.` }
      log(d, { type: 'error', severity: 'warn', object: `Test call · ${t.displayName}`, workspaceId: ws.id, result: 'Missing key slot', trk, reason: result.message })
      return
    }
    log(d, {
      type: 'request',
      severity: 'ok',
      object: `Test call · ${t.displayName}`,
      destination: new URL(t.baseUrl).host,
      workspaceId: ws.id,
      result: `200 · ${ms} ms`,
      trk,
      detail: [['Action', act ? `${act.method} ${act.path}` : '—'], ['Workspace', ws.name], ['Tool version', opts.draft && tool.status === 'draft' ? `draft v${t.version}` : `v${t.version}`], ['Source', 'Test call']],
    })
  })
  return result
}
