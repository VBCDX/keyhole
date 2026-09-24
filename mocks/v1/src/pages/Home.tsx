import { Link } from 'react-router-dom'
import { expiringSoon, plural } from '../lib/format'
import { actions, myRole, orgAgents, orgConnectors, orgKeys, orgStores, orgTools, orgWorkspaces, useDB, useNow, visibleEvents } from '../lib/store'
import { KeyholeIcon } from '../components/keyhole'
import { LogFeed } from '../components/shared'
import { Card, CloseX, Dot, cx } from '../components/ui'

function Stat({ label, children, tone, className }: { label: string; children: React.ReactNode; tone?: 'red'; className?: string }) {
  return (
    <div className={cx('flex-1 rounded-[10px] border bg-panel p-4', tone === 'red' ? 'border-red-500/35' : 'border-edge', className)}>
      <div className="text-xs text-zinc-500">{label}</div>
      {children}
    </div>
  )
}

export function Checklist() {
  const d = useDB()
  const ws = orgWorkspaces(d)
  const wsWithTool = ws.find((w) => w.tools.length)
  const connected = ws.find((w) => w.mcp || w.https)
  const target = connected ?? wsWithTool ?? ws[0]
  const tools = orgTools(d)
  const toPublish = tools.find((t) => !t.published) ?? tools[0]
  const steps = [
    { t: 'Add your first key', sub: 'The Local store is ready', done: orgKeys(d).some((k) => !k.cabinetId), to: `/orgs/${d.currentOrgId}/stores/st_local?add=1` },
    { t: 'Create a tool', sub: 'Import an API description or pick from the catalog', done: tools.length > 0, to: '/tools' },
    { t: 'Publish the tool', sub: 'New tools start as drafts. Agents can only use a published version', done: tools.some((t) => t.published), to: toPublish ? `/tools/${toPublish.id}?section=publish` : '/tools' },
    { t: 'Create a workspace and attach the tool', sub: '', done: !!wsWithTool, to: ws[0] ? `/workspaces/${ws[0].id}/tools` : '/workspaces?new=1' },
    { t: 'Create an agent', sub: '', done: orgAgents(d).length > 0, to: '/players/agents?new=1' },
    { t: 'Turn on MCP or HTTPS and download the config', sub: '', done: !!connected && !!d.configDownloadedAt, to: target ? `/workspaces/${target.id}/connect` : '/workspaces' },
    { t: 'Make your first call', sub: 'We wait here — this checks off when the first request lands', done: !!d.firstCallAt, to: target ? `/workspaces/${target.id}/connect` : '/workspaces' },
  ]
  const waitStep = steps.length - 1
  const doneCount = steps.filter((s) => s.done).length
  const allDone = doneCount === steps.length
  const nextIndex = steps.findIndex((s) => !s.done)

  return (
    <div className="relative mt-5 max-w-[720px] rounded-xl border border-edge bg-panel p-6">
      <div className="absolute top-4 right-4">
        <CloseX onClick={actions.dismissChecklist} />
      </div>
      {allDone ? (
        <div className="flex items-center gap-4">
          <KeyholeIcon state="closed" size={26} turn />
          <div>
            <div className="text-sm font-semibold text-green-400">Your first guarded call landed</div>
            <div className="mt-0.5 text-sm2 text-zinc-400">The key never left Keyhole. Every use from here on is in the audit log.</div>
          </div>
        </div>
      ) : (
        <>
          <div className="text-sm font-semibold">Get set up</div>
          <div className="mt-0.5 text-sm2 text-zinc-400">Seven steps to your first guarded call. Each checks off on its own.</div>
        </>
      )}
      <div className="mt-4 flex flex-col">
        {steps.map((s, i) => (
          <div key={s.t} className="flex items-start gap-3 border-t border-line py-2.5">
            <div
              className={cx(
                'mt-px flex size-[22px] min-w-[22px] items-center justify-center rounded-full border text-2xs font-semibold',
                s.done ? 'border-green-500/40 bg-green-500/[0.12] text-green-400' : i === nextIndex ? 'border-zinc-500 text-zinc-200' : 'border-zinc-700 text-zinc-400',
              )}
            >
              {s.done ? '✓' : i + 1}
            </div>
            <div className="min-w-0">
              <Link to={s.to} className={cx('text-md font-medium', s.done ? 'text-zinc-500 line-through hover:text-zinc-400' : 'text-zinc-200 hover:text-zinc-50')}>
                {s.t}
              </Link>
              {s.sub && !s.done && (
                <div className="mt-0.5 flex items-center gap-2 text-xs text-zinc-500">
                  {i === waitStep && i === nextIndex && <KeyholeIcon size={12} pulse />}
                  {s.sub}
                </div>
              )}
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function Home() {
  const d = useDB()
  const now = useNow()
  // The `user` role only sees its own workspaces, so the org-wide traffic baseline doesn't apply.
  const scoped = myRole(d) === 'user'
  const events = visibleEvents(d)
  const base = scoped ? { requests: 0, blocked: 0 } : d.statsBase
  const day = events.filter((e) => now - e.at < 86_400_000)
  const requests = base.requests + day.filter((e) => e.type === 'request').length
  const blocked = base.blocked + day.filter((e) => e.severity === 'blocked').length
  const myWs = new Set(orgWorkspaces(d).map((w) => w.id))
  const expiring = orgAgents(d).filter((a) => a.status !== 'revoked' && expiringSoon(a.expiresAt, now) && (!scoped || a.workspaceIds.some((id) => myWs.has(id)))).length
  const stores = orgStores(d)
  const unhealthyStores = stores.filter((s) => s.health !== 'healthy')
  const unverified = stores.filter((s) => s.health === 'healthy' && s.skipVerify)
  const connectors = orgConnectors(d)
  const badConn = connectors.filter((c) => c.health !== 'healthy')
  const traffic = events.filter((e) => ['request', 'blocked', 'error', 'verification'].includes(e.type))
  const showChecklist = !d.checklistDismissed

  return (
    <div>
      <h1 className="m-0 text-lg font-semibold tracking-[-0.01em]">Home</h1>
      {showChecklist && <Checklist />}

      <div className={cx('flex gap-4', showChecklist ? 'mt-6 max-w-[960px]' : 'mt-5 max-w-[1080px]')}>
        <Stat label={scoped ? 'Requests · 24 h · your workspaces' : 'Requests · 24 h'} className="flex-[1.4]">
          <div className="flex items-end justify-between">
            <div className="mt-1.5 text-[22px] font-semibold">{requests.toLocaleString()}</div>
            {requests > 0 && (
              <svg width="120" height="32" viewBox="0 0 120 32" aria-hidden>
                <polyline points="0,26 15,22 30,24 45,16 60,18 75,10 90,13 105,6 120,8" fill="none" stroke="#3f3f46" strokeWidth="2" />
              </svg>
            )}
          </div>
        </Stat>
        <Stat label="Blocked" tone={blocked > 0 ? 'red' : undefined}>
          <Link to="/audit" className={cx('mt-1.5 block text-[22px] font-semibold', blocked > 0 ? 'text-red-400 hover:text-red-300' : 'text-zinc-100 hover:text-zinc-100')}>
            {blocked}
          </Link>
        </Stat>
        <Stat label="Tokens expiring · 14 d">
          <Link to="/players/agents" className={cx('mt-1.5 block text-[22px] font-semibold', expiring > 0 ? 'text-amber-400 hover:text-amber-300' : 'text-zinc-100 hover:text-zinc-100')}>
            {expiring}
          </Link>
        </Stat>
        <Stat label={stores.length > 1 ? 'Stores' : 'Store health'}>
          <Link to={`/orgs/${d.currentOrgId}/stores`} className="mt-3 flex items-center gap-2">
            <Dot health={unhealthyStores.length ? 'error' : unverified.length ? 'degraded' : 'healthy'} />
            <span className={cx('text-[13px]', unhealthyStores.length ? 'text-red-400' : unverified.length ? 'text-amber-400' : 'text-zinc-300')}>
              {unhealthyStores.length ? `${unhealthyStores.length} unhealthy` : unverified.length ? `${unverified.length} with unverified TLS` : stores.length === 1 ? 'Local store healthy' : `${stores.length} healthy`}
            </span>
          </Link>
        </Stat>
        {connectors.length > 0 && (
          <Stat label="Connectors">
            <Link to="/connectors" className="mt-3 flex items-center gap-2">
              <Dot health={badConn.some((c) => c.health === 'offline') && !badConn.some((c) => c.health === 'degraded') ? 'offline' : badConn.length ? 'degraded' : 'healthy'} />
              <span className={cx('text-[13px]', badConn.length ? 'text-amber-400' : 'text-zinc-300')}>
                {badConn.filter((c) => c.health === 'degraded').length ? `${badConn.filter((c) => c.health === 'degraded').length} degraded` : badConn.length ? `${plural(badConn.length, 'offline', 'offline')}` : `${connectors.length} healthy`}
              </span>
            </Link>
          </Stat>
        )}
      </div>

      {traffic.length === 0 ? (
        <Card className="mt-6 max-w-[960px] p-8 text-center">
          <div className="text-[13px] text-zinc-400">No activity yet. Requests appear here the moment your first call lands.</div>
        </Card>
      ) : (
        <div className="mt-7 max-w-[1080px]">
          <div className="flex items-center justify-between">
            <div className="eyebrow">Recent activity</div>
            <Link to="/audit" className="text-sm2">
              Open Audit
            </Link>
          </div>
          <div className="mt-2.5">
            <LogFeed events={events.slice(0, 10)} />
          </div>
        </div>
      )}
    </div>
  )
}
