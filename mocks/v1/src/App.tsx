import { useEffect } from 'react'
import { HashRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { actions, isSuspended, me, myOrgs, org, useDB } from './lib/store'
import { Button } from './components/ui'
import { AppShell } from './components/AppShell'
import { DemoPanel } from './components/DemoPanel'
import { KeyholeIcon } from './components/keyhole'
import { Connectors } from './pages/Connectors'
import { Home } from './pages/Home'
import { AccountSettings, AuditPage, InvitationsPage, MySettings, NoOrgShell, NotFound, SupportConsole } from './pages/misc'
import { OpenBaoWizard } from './pages/OpenBaoWizard'
import { OrgAgents, OrgAudit, OrgDetail, OrgMembers, OrgOverview, OrgsList, OrgTools, OrgWorkspaces } from './pages/orgs'
import { AgentDetail, AgentsPage, UserDetail, UsersPage } from './pages/players'
import { StoreDetail, StoresTab } from './pages/stores'
import { ToolEditor } from './pages/ToolEditor'
import { ToolsPage } from './pages/tools'
import { WorkspaceDetail, WorkspacesList, WsAudit, WsCabinets, WsConnect, WsSummary, WsTools } from './pages/workspaces'

function Locked() {
  const u = me(useDB())
  return (
    <div className="flex h-full items-center justify-center bg-page text-zinc-100">
      <div className="flex w-[420px] flex-col items-center gap-3 text-center">
        <KeyholeIcon size={28} state="error" />
        <div className="text-[15px] font-semibold">This account is locked</div>
        <div className="text-sm2 leading-relaxed text-zinc-400">
          Keyhole support locked it and ended every session. Only Keyhole support can unlock it — contact <span className="text-zinc-200">support@keyhole.dev</span>
          {u.lockReason ? ' and quote the case below.' : '.'}
        </div>
        {u.lockReason && (
          <div className="rounded-lg border border-edge bg-panel px-3.5 py-2.5 text-sm2">
            <span className="text-zinc-500">Case · </span>
            <span className="text-zinc-200">{u.lockReason}</span>
          </div>
        )}
        {u.unlockRequest && <div className="text-xs text-zinc-500">An administrator of your organization has already asked support to unlock it.</div>}
      </div>
    </div>
  )
}

/**
 * Suspension takes effect immediately (rule 5), but only in the organization that suspended you:
 * your other organizations stay one click away.
 */
function Suspended() {
  const d = useDB()
  const u = me(d)
  const here = org(d)
  const others = myOrgs(d).filter((o) => !isSuspended(u, o.id))
  // A link into another of their organizations (#/orgs/<id>/…) opens it rather than stopping here.
  const linked = useLocation().pathname.match(/^\/orgs\/([^/]+)/)?.[1]
  const target = others.find((o) => o.id === linked)
  useEffect(() => {
    if (target) actions.setOrg(target.id)
  }, [target])
  return (
    <div className="flex h-full items-center justify-center bg-page text-zinc-100">
      <div className="flex w-[420px] flex-col items-center gap-3 text-center">
        <KeyholeIcon size={28} state="error" />
        <div className="text-[15px] font-semibold">You’re suspended in {here?.name}</div>
        <div className="text-sm2 leading-relaxed text-zinc-400">An admin of {here?.name} suspended your membership. Ask them to reactivate it — everything you set up there is still in place.</div>
        {others.length > 0 && (
          <div className="mt-2 flex flex-col items-center gap-2">
            <div className="text-xs text-zinc-500">Your other organizations aren’t affected:</div>
            {others.map((o) => (
              <Button key={o.id} onClick={() => actions.setOrg(o.id)}>
                Open {o.name}
              </Button>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function Routed() {
  const d = useDB()
  if (d.currentUserId === 'support')
    return (
      <Routes>
        <Route path="*" element={<SupportConsole />} />
      </Routes>
    )
  const u = me(d)
  if (u?.locked) return <Locked />
  if (u && isSuspended(u, d.currentOrgId)) return <Suspended />
  if (!u || !Object.keys(u.roles).length || !d.orgs.some((o) => o.id === d.currentOrgId)) return <NoOrgShell />
  return (
    <Routes>
      <Route path="/tools/:toolId" element={<ToolEditor />} />
      <Route element={<AppShell />}>
        <Route index element={<Home />} />
        <Route path="invitations" element={<InvitationsPage />} />
        <Route path="orgs" element={<OrgsList />} />
        <Route path="orgs/:orgId" element={<OrgDetail />}>
          <Route index element={<Navigate to="overview" replace />} />
          <Route path="overview" element={<OrgOverview />} />
          <Route path="stores" element={<StoresTab />} />
          <Route path="stores/connect-openbao" element={<OpenBaoWizard />} />
          <Route path="stores/:storeId" element={<StoreDetail />} />
          <Route path="members" element={<OrgMembers />} />
          <Route path="agents" element={<OrgAgents />} />
          <Route path="tools" element={<OrgTools />} />
          <Route path="workspaces" element={<OrgWorkspaces />} />
          <Route path="audit" element={<OrgAudit />} />
        </Route>
        <Route path="workspaces" element={<WorkspacesList />} />
        <Route path="workspaces/:wsId" element={<WorkspaceDetail />}>
          <Route index element={<Navigate to="summary" replace />} />
          <Route path="summary" element={<WsSummary />} />
          <Route path="tools" element={<WsTools />} />
          <Route path="cabinets" element={<WsCabinets />} />
          <Route path="connect" element={<WsConnect />} />
          <Route path="audit" element={<WsAudit />} />
        </Route>
        <Route path="tools" element={<ToolsPage />} />
        <Route path="players/users" element={<UsersPage />} />
        <Route path="players/users/:userId" element={<UserDetail />} />
        <Route path="players/agents" element={<AgentsPage />} />
        <Route path="players/agents/:agentId" element={<AgentDetail />} />
        <Route path="connectors" element={<Connectors />} />
        <Route path="audit" element={<AuditPage />} />
        <Route path="settings/me" element={<MySettings />} />
        <Route path="settings/account" element={<AccountSettings />} />
        <Route path="support" element={<Navigate to="/" replace />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}

export default function App() {
  return (
    <HashRouter>
      <Routed />
      <DemoPanel />
    </HashRouter>
  )
}
