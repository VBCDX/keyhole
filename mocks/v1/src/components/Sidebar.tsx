import { useEffect, useRef, useState } from 'react'
import { Link, NavLink, useNavigate } from 'react-router-dom'
import { actions, isSuspended, me, myOrgs, org, useDB } from '../lib/store'
import { KeyholeIcon } from './keyhole'
import { cx } from './ui'

type Item = { label: string; to?: string; indent?: boolean; group?: boolean; end?: boolean }
const NAV: Item[] = [
  { label: 'Home', to: '/', end: true },
  { label: 'Organizations', to: '/orgs' },
  { label: 'Workspaces', to: '/workspaces' },
  { label: 'Resources', group: true },
  { label: 'Tools', to: '/tools', indent: true },
  { label: 'Players', group: true },
  { label: 'Users', to: '/players/users', indent: true },
  { label: 'Agents', to: '/players/agents', indent: true },
  { label: 'Connectors', to: '/connectors' },
  { label: 'Audit', to: '/audit' },
  { label: 'Settings', group: true },
  { label: 'My Settings', to: '/settings/me', indent: true },
  { label: 'Account', to: '/settings/account', indent: true },
]

function OrgSwitcher() {
  const d = useDB()
  const nav = useNavigate()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const current = org(d)
  const orgs = myOrgs(d)
  const pending = (d.incomingInvites[d.currentUserId] ?? []).filter((i) => i.expiresAt > Date.now()).length
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && setOpen(false)
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open])
  return (
    <div ref={ref} className="relative mx-3 my-2.5">
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        className="flex w-full items-center justify-between rounded-lg border border-edge bg-panel px-3 py-[9px] hover:border-zinc-700"
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="flex size-5 items-center justify-center rounded-[5px] border border-chip bg-line text-2xs font-semibold text-zinc-400">{current?.name.charAt(0) ?? '—'}</span>
          <span className="truncate text-[13px] font-medium text-zinc-200">{current?.name ?? 'No organization'}</span>
        </span>
        <span className="text-[10px] text-zinc-600">▾</span>
      </button>
      {open && (
        <div className="absolute top-full right-0 left-0 z-30 mt-1 rounded-lg border border-edge bg-panel p-1 shadow-xl">
          <div className="px-2.5 pt-1.5 pb-1 text-[10px] font-semibold tracking-[0.08em] text-zinc-600 uppercase">Organizations</div>
          {orgs.map((o) => (
            <button
              key={o.id}
              type="button"
              onClick={() => {
                actions.setOrg(o.id)
                setOpen(false)
                nav('/')
              }}
              className="flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-[13px] text-zinc-300 hover:bg-line"
            >
              <span className="truncate">{o.name}</span>
              {isSuspended(me(d), o.id) ? (
                <span className="text-2xs text-amber-400">suspended</span>
              ) : (
                <span className="text-2xs text-zinc-500">{o.id === d.currentOrgId ? '✓' : me(d).roles[o.id]}</span>
              )}
            </button>
          ))}
          <div className="my-1 border-t border-line" />
          <Link to="/invitations" onClick={() => setOpen(false)} className="flex items-center justify-between rounded-md px-2.5 py-1.5 text-[13px] text-zinc-300 hover:bg-line hover:text-zinc-100">
            Pending invitations
            {pending > 0 && <span className="rounded-full bg-brass/15 px-1.5 text-2xs font-semibold text-brass-light">{pending}</span>}
          </Link>
        </div>
      )}
    </div>
  )
}

export function Sidebar() {
  return (
    <aside className="flex h-full w-60 min-w-60 flex-col border-r border-line bg-rail">
      <Link to="/" className="flex items-center gap-2 px-4 pt-4 pb-1.5">
        {/* The nav logo is the one standing exception to brass-only-near-credentials. */}
        <KeyholeIcon size={16} />
        <span className="text-sm font-semibold tracking-[-0.01em] text-zinc-100">Keyhole</span>
      </Link>
      <OrgSwitcher />
      <nav className="flex flex-col gap-px overflow-y-auto px-2 py-1">
        {NAV.map((it) =>
          it.group ? (
            <div key={it.label} className="mt-3.5 px-2 py-1.5 text-[10px] font-semibold tracking-[0.08em] text-[#5b5b64] uppercase">
              {it.label}
            </div>
          ) : (
            <NavLink
              key={it.label}
              to={it.to!}
              end={it.end}
              className={({ isActive }) =>
                cx('rounded-md py-1.5 pr-2 text-[13px]', it.indent ? 'pl-[22px]' : 'pl-2', isActive ? 'bg-line font-semibold text-zinc-100 hover:text-zinc-100' : 'text-zinc-400 hover:bg-white/[0.02] hover:text-zinc-200')
              }
            >
              {it.label}
            </NavLink>
          ),
        )}
      </nav>
    </aside>
  )
}
