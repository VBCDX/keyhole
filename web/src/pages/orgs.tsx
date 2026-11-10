// Ported from mocks/v1/src/pages/orgs.tsx: the organizations list and the
// organization header with its tabs. Only the Stores tab is built so far.
import { useEffect } from 'react'
import { Navigate, Outlet, useLocation, useNavigate, useParams } from 'react-router-dom'
import { ago, ms } from '../lib/format'
import { isAdmin, useSession } from '../session'
import { Breadcrumb, Button, PageTitle, Pill, Row, Table, Tabs } from '../components/ui'
import { NoAccess, Soon } from './misc'

const ROLE: Record<string, string> = { Owner: 'Owner', userAdmin: 'Admin', user: 'Member' }
export const roleName = (r: string) => ROLE[r] ?? r

const ORG_COLS = '2fr 1fr 1fr'

export function OrgsList() {
  const { orgs, setOrg } = useSession()
  const nav = useNavigate()
  return (
    <div>
      <PageTitle>Organizations</PageTitle>
      <Table cols={ORG_COLS} head={['Name', 'Your role', 'Joined']} className="mt-5 max-w-[1000px]">
        {orgs.map((o) => (
          <Row
            key={o.id}
            cols={ORG_COLS}
            onClick={() => {
              setOrg(o.id)
              nav(`/orgs/${o.id}/overview`)
            }}
          >
            <div className="flex items-center gap-2 font-medium">
              {o.name}
              {o.status === 'suspended' && <Pill tone="amber">Suspended</Pill>}
            </div>
            <div className="text-zinc-400">{roleName(o.role)}</div>
            <div className="text-zinc-500">{ago(ms(o.joined_at))}</div>
          </Row>
        ))}
      </Table>
    </div>
  )
}

export function OrgDetail() {
  const { orgs, current, setOrg } = useSession()
  const { orgId } = useParams()
  const { pathname } = useLocation()
  const o = orgs.find((x) => x.id === orgId)
  // A link to an organization you belong to switches to it, so its
  // suspension gate applies; any other organization is "no access".
  const switchTo = o && o.id !== current?.id ? o.id : null
  useEffect(() => {
    if (switchTo) setOrg(switchTo)
  }, [switchTo]) // eslint-disable-line react-hooks/exhaustive-deps
  if (!o) return <NoAccess what="organization" to="/orgs" back="Back to your organizations" />
  if (switchTo) return null
  const base = `/orgs/${o.id}`
  const onStoresList = pathname.replace(/\/$/, '').endsWith('/stores')
  return (
    <div>
      <Breadcrumb items={[{ label: 'Organizations', to: '/orgs' }, { label: o.name }]} />
      <PageTitle
        actions={
          onStoresList && isAdmin(o) ? (
            <Soon>
              <Button variant="primary" disabled>
                Add store
              </Button>
            </Soon>
          ) : undefined
        }
      >
        {o.name}
      </PageTitle>
      <Tabs
        tabs={[
          { to: `${base}/overview`, label: 'Overview' },
          { to: `${base}/stores`, label: 'Stores' },
          { to: `${base}/members`, label: 'Members' },
          { to: `${base}/agents`, label: 'Agents' },
          { to: `${base}/tools`, label: 'Tools' },
          { to: `${base}/workspaces`, label: 'Workspaces' },
          { to: `${base}/audit`, label: 'Audit' },
        ]}
      />
      <Outlet />
    </div>
  )
}

/** A tab of the organization page that isn't built yet. */
export function OrgTabSoon() {
  return (
    <div className="mt-5 max-w-[760px] rounded-[10px] border border-edge bg-panel p-10 text-center">
      <div className="text-[13px] font-semibold text-zinc-200">Coming soon</div>
      <div className="mt-1 text-[13px] text-zinc-400">This tab isn’t built yet.</div>
    </div>
  )
}

export function OrgIndex() {
  return <Navigate to="overview" replace />
}
