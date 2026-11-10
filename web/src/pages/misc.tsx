// Placeholders and gates around the screens that exist so far.
import type { ReactNode } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { KeyholeIcon } from '../components/keyhole'
import { Button, PageTitle } from '../components/ui'
import { useSession } from '../session'

/** A route whose screen isn't built yet. */
export function ComingSoon({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="max-w-[760px]">
      <PageTitle>{title}</PageTitle>
      <div className="mt-5 rounded-[10px] border border-edge bg-panel p-10 text-center">
        <div className="text-[13px] font-semibold text-zinc-200">Coming soon</div>
        <div className="mt-1 text-[13px] text-zinc-400">This screen isn’t built yet.</div>
        {children && <div className="mt-4 text-[13px]">{children}</div>}
      </div>
    </div>
  )
}

/** Wraps a control that will work in a later release: disabled, with a tooltip. */
export function Soon({ children }: { children: ReactNode }) {
  return (
    <span title="Coming soon" className="inline-flex">
      {children}
    </span>
  )
}

export function NotFound() {
  return (
    <div className="text-sm text-zinc-400">
      Nothing here. <Link to="/">Go home</Link>
    </div>
  )
}

export function NoAccess({ what, to, back }: { what: string; to: string; back: string }) {
  return (
    <div className="text-sm text-zinc-400">
      You don’t have access to this {what}. <Link to={to}>{back}</Link>
    </div>
  )
}

function SignOutButton() {
  const { signOut } = useSession()
  return <Button onClick={() => void signOut().catch(() => window.location.assign('/'))}>Sign out</Button>
}

/** Signed in, but not a member of any organization yet. */
export function NoOrgShell() {
  const { me } = useSession()
  return (
    <div className="flex h-full flex-col bg-page text-zinc-100">
      <div className="flex items-center gap-2 border-b border-line bg-rail px-6 py-4">
        <KeyholeIcon size={16} />
        <span className="text-sm font-semibold">Keyhole</span>
      </div>
      <main className="flex-1 overflow-y-auto px-10 py-8">
        <div className="max-w-[640px]">
          <PageTitle>You’re not in an organization yet</PageTitle>
          <div className="mt-3 text-sm2 leading-relaxed text-zinc-400">
            You’re signed in as <span className="text-zinc-200">{me.user.email}</span>. Ask an admin of your organization to invite you.
          </div>
          <div className="mt-5">
            <SignOutButton />
          </div>
        </div>
      </main>
    </div>
  )
}

/**
 * From the mock's App.tsx: suspension only applies in the organization that
 * suspended you, so your other organizations stay one click away.
 */
export function Suspended() {
  const { orgs, current, setOrg } = useSession()
  const nav = useNavigate()
  const others = orgs.filter((o) => o.status === 'active')
  return (
    <div className="flex h-full items-center justify-center bg-page text-zinc-100">
      <div className="flex w-[420px] flex-col items-center gap-3 text-center">
        <KeyholeIcon size={28} state="error" />
        <div className="text-[15px] font-semibold">You’re suspended in {current?.name}</div>
        <div className="text-sm2 leading-relaxed text-zinc-400">An admin of {current?.name} suspended your membership. Ask them to reactivate it — everything you set up there is still in place.</div>
        <div className="mt-2 flex flex-col items-center gap-2">
          {others.length > 0 && <div className="text-xs text-zinc-500">Your other organizations aren’t affected:</div>}
          {others.map((o) => (
            <Button
              key={o.id}
              onClick={() => {
                setOrg(o.id)
                nav('/')
              }}
            >
              Open {o.name}
            </Button>
          ))}
          <SignOutButton />
        </div>
      </div>
    </div>
  )
}
