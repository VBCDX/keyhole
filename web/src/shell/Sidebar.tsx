// Ported from mocks/v1/src/components/Sidebar.tsx, reading the session
// instead of the mock's in-browser database. The user menu at the foot is
// new: the mock has no sign-out, and neither has the app switcher beside the
// logo, which links to the other VBCDX apps (SUITE_APPS).
import { useEffect, useRef, useState, type RefObject } from 'react'
import { Link, NavLink, useNavigate } from 'react-router-dom'
import { initials } from '../lib/format'
import { useSession } from '../session'
import { KeyholeIcon } from '../components/keyhole'
import { Avatar, cx } from '../components/ui'

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

/** Closes a popover on a click outside it. */
function useOutsideClose(ref: RefObject<HTMLElement | null>, open: boolean, close: () => void) {
  useEffect(() => {
    if (!open) return
    const h = (e: MouseEvent) => !ref.current?.contains(e.target as Node) && close()
    document.addEventListener('mousedown', h)
    return () => document.removeEventListener('mousedown', h)
  }, [open, ref, close])
}

function OrgSwitcher() {
  const { orgs, current, setOrg } = useSession()
  const nav = useNavigate()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useOutsideClose(ref, open, () => setOpen(false))
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
                setOrg(o.id)
                setOpen(false)
                nav('/')
              }}
              className="flex w-full items-center justify-between rounded-md px-2.5 py-1.5 text-left text-[13px] text-zinc-300 hover:bg-line"
            >
              <span className="truncate">{o.name}</span>
              {o.status === 'suspended' ? (
                <span className="text-2xs text-amber-400">suspended</span>
              ) : (
                <span className="text-2xs text-zinc-500">{o.id === current?.id ? '✓' : o.role}</span>
              )}
            </button>
          ))}
          <div className="my-1 border-t border-line" />
          <Link to="/invitations" onClick={() => setOpen(false)} className="flex items-center justify-between rounded-md px-2.5 py-1.5 text-[13px] text-zinc-300 hover:bg-line hover:text-zinc-100">
            Pending invitations
          </Link>
        </div>
      )}
    </div>
  )
}

/** Links to the other VBCDX apps. Hidden when SUITE_APPS is unset. */
function AppSwitcher() {
  const { apps } = useSession()
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  useOutsideClose(ref, open, () => setOpen(false))
  if (apps.length === 0) return null
  return (
    <div ref={ref} className="relative ml-auto">
      <button
        type="button"
        aria-expanded={open}
        aria-label="Switch VBCDX app"
        title="VBCDX apps"
        onClick={() => setOpen(!open)}
        className="flex size-6 items-center justify-center rounded-md text-zinc-500 hover:bg-line hover:text-zinc-200"
      >
        <svg width="12" height="12" viewBox="0 0 12 12" fill="currentColor" aria-hidden="true">
          {[1, 6, 11].flatMap((x) => [1, 6, 11].map((y) => <circle key={`${x}-${y}`} cx={x} cy={y} r="1.1" />))}
        </svg>
      </button>
      {open && (
        <div className="absolute top-full right-0 z-30 mt-1 w-48 rounded-lg border border-edge bg-panel p-1 shadow-xl">
          <div className="px-2.5 pt-1.5 pb-1 text-[10px] font-semibold tracking-[0.08em] text-zinc-600 uppercase">VBCDX apps</div>
          {apps.map((a) =>
            a.current ? (
              <div key={a.url} aria-current="true" className="flex items-center justify-between rounded-md px-2.5 py-1.5 text-[13px] text-zinc-100">
                <span className="truncate">{a.name}</span>
                <span className="text-2xs text-zinc-500">✓</span>
              </div>
            ) : (
              <a key={a.url} href={a.url} className="block truncate rounded-md px-2.5 py-1.5 text-[13px] text-zinc-300 hover:bg-line hover:text-zinc-100">
                {a.name}
              </a>
            ),
          )}
        </div>
      )}
    </div>
  )
}

function UserMenu() {
  const { me, signOut } = useSession()
  const [open, setOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const ref = useRef<HTMLDivElement>(null)
  useOutsideClose(ref, open, () => setOpen(false))
  const name = me.user.name || me.user.email
  return (
    <div ref={ref} className="relative mt-auto border-t border-line p-2">
      {open && (
        <div className="absolute right-2 bottom-full left-2 z-30 mb-1 rounded-lg border border-edge bg-panel p-1 shadow-xl">
          <div className="truncate px-2.5 pt-1.5 pb-1 text-xs text-zinc-500">{me.user.email}</div>
          <Link to="/settings/me" onClick={() => setOpen(false)} className="block rounded-md px-2.5 py-1.5 text-[13px] text-zinc-300 hover:bg-line hover:text-zinc-100">
            My Settings
          </Link>
          <button
            type="button"
            disabled={busy}
            onClick={async () => {
              setBusy(true)
              setError(null)
              try {
                await signOut()
              } catch {
                setError('Sign-out failed. Please try again.')
                setBusy(false)
              }
            }}
            className="block w-full rounded-md px-2.5 py-1.5 text-left text-[13px] text-zinc-300 hover:bg-line hover:text-zinc-100 disabled:opacity-40"
          >
            {busy ? 'Signing out…' : 'Sign out'}
          </button>
          {error && (
            <div role="alert" className="px-2.5 pb-1.5 text-xs text-red-400">
              {error}
            </div>
          )}
        </div>
      )}
      <button type="button" aria-expanded={open} aria-label="Account menu" onClick={() => setOpen(!open)} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-white/[0.02]">
        <Avatar initials={initials(name)} />
        <span className="min-w-0 truncate text-[13px] text-zinc-300">{name}</span>
        <span className="ml-auto text-[10px] text-zinc-600">▴</span>
      </button>
    </div>
  )
}

export function Sidebar() {
  return (
    <aside className="flex h-full w-60 min-w-60 flex-col border-r border-line bg-rail">
      <div className="flex items-center pt-4 pr-3 pb-1.5 pl-4">
        <Link to="/" className="flex items-center gap-2">
          {/* The nav logo is the one standing exception to brass-only-near-credentials. */}
          <KeyholeIcon size={16} />
          <span className="text-sm font-semibold tracking-[-0.01em] text-zinc-100">Keyhole</span>
        </Link>
        <AppSwitcher />
      </div>
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
      <UserMenu />
    </aside>
  )
}
