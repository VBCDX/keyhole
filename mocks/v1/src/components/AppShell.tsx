import type { ReactNode } from 'react'
import { Link, Outlet } from 'react-router-dom'
import { useDB } from '../lib/store'
import { Sidebar } from './Sidebar'

export function InviteBanner() {
  const d = useDB()
  const pending = (d.incomingInvites[d.currentUserId] ?? []).filter((i) => i.expiresAt > Date.now())
  if (!pending.length) return null
  const first = pending[0]
  return (
    <div className="mb-6 flex max-w-[1080px] items-center justify-between gap-4 rounded-[10px] border border-brass/30 bg-brass/[0.06] px-[18px] py-3.5 text-[13px] text-brass-light">
      <span>
        You've been invited to <strong>{first.orgName}</strong> as {first.role === 'user' ? 'a member' : `a ${first.role}`}.
        {pending.length > 1 && ` (${pending.length} pending invitations)`}
      </span>
      <Link to="/invitations" className="shrink-0 text-sm2">
        View invitations →
      </Link>
    </div>
  )
}

export function Page({ children, wide }: { children: ReactNode; wide?: boolean }) {
  return <div className={wide ? '' : 'max-w-[1120px]'}>{children}</div>
}

export function AppShell() {
  return (
    <div className="flex h-full bg-page text-zinc-100">
      <Sidebar />
      <main className="min-w-0 flex-1 overflow-y-auto px-10 py-8">
        <InviteBanner />
        <Outlet />
      </main>
    </div>
  )
}
