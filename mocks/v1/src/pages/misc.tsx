import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { ago, initials, until } from '../lib/format'
import { actions, agentsCreatedBy, isAdmin, me, myRole, org, useDB, useNow, visibleEvents } from '../lib/store'
import { AuditLog } from '../components/shared'
import { KeyholeIcon } from '../components/keyhole'
import { Avatar, Button, Card, Field, Footer, Input, Modal, PageTitle, Toggle } from '../components/ui'
import { DeleteOrgDialog } from './orgs'

/* ------------------------------------------------------------------ */
/* Audit                                                               */
/* ------------------------------------------------------------------ */
export function AuditPage() {
  const d = useDB()
  const [params] = useSearchParams()
  const events = visibleEvents(d)
  return (
    <div className="max-w-[1080px]">
      <PageTitle>Audit</PageTitle>
      <div className="mt-4">
        <AuditLog events={events} initialExpand={params.get('event') ?? undefined} />
      </div>
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Invitations                                                         */
/* ------------------------------------------------------------------ */
export function InvitationsPage() {
  const d = useDB()
  const nav = useNavigate()
  const now = useNow()
  const list = d.incomingInvites[d.currentUserId] ?? []
  const [asked, setAsked] = useState<string[]>([])
  return (
    <div className="max-w-[760px]">
      <PageTitle>Invitations</PageTitle>
      {list.length === 0 ? (
        <div className="mt-5 rounded-[10px] border border-edge bg-panel p-10 text-center text-[13px] text-zinc-400">No pending invitations. When someone invites you to an organization, it shows up here.</div>
      ) : (
        <div className="mt-4 overflow-hidden rounded-[10px] border border-edge bg-panel">
          {list.map((i) => {
            const expired = i.expiresAt < now
            return (
              <div key={i.id} className={`flex items-center gap-4 border-b border-line p-4 last:border-b-0 ${expired ? 'opacity-55' : ''}`}>
                <div className="flex-1">
                  <div className="text-md font-semibold">{i.orgName}</div>
                  <div className="mt-0.5 text-xs text-zinc-500">
                    Invited by {i.invitedBy} · role: {i.role} · {expired ? <span className="text-amber-400">expired</span> : `expires ${until(i.expiresAt, now).toLowerCase()}`}
                  </div>
                </div>
                {expired ? (
                  asked.includes(i.id) ? (
                    <span className="text-sm2 text-zinc-500">Request sent to {i.invitedBy}</span>
                  ) : (
                    <button className="text-sm2 text-brass hover:text-brass-light" onClick={() => setAsked([...asked, i.id])}>
                      Ask for a new invite
                    </button>
                  )
                ) : (
                  <>
                    <Button onClick={() => actions.declineInvite(i.id)}>Decline</Button>
                    <Button
                      variant="primary"
                      onClick={() => {
                        actions.acceptInvite(i.id)
                        nav('/')
                      }}
                    >
                      Accept
                    </Button>
                  </>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}

/** Shell for someone who's signed in but belongs to no organization yet. */
export function NoOrgShell() {
  return (
    <div className="flex h-full flex-col bg-page text-zinc-100">
      <div className="flex items-center gap-2 border-b border-line bg-rail px-6 py-4">
        <KeyholeIcon size={16} />
        <span className="text-sm font-semibold">Keyhole</span>
      </div>
      <main className="flex-1 overflow-y-auto px-10 py-8">
        <InviteBannerLite />
        <InvitationsPage />
      </main>
    </div>
  )
}
function InviteBannerLite() {
  const d = useDB()
  const first = (d.incomingInvites[d.currentUserId] ?? []).find((i) => i.expiresAt > Date.now())
  if (!first) return null
  return (
    <div className="mb-7 max-w-[640px] rounded-[10px] border border-brass/30 bg-brass/[0.06] px-[18px] py-3.5 text-[13px] text-brass-light">
      You've been invited to <strong>{first.orgName}</strong> as {first.role === 'user' ? 'a member' : `a ${first.role}`}.
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Settings                                                            */
/* ------------------------------------------------------------------ */
const NOTIFS: [string, string][] = [
  ['connectorOffline', 'Connector offline'],
  ['storeUnhealthy', 'Store unhealthy'],
  ['verificationFailed', 'Verification failed'],
  ['tokenExpiring', 'Token expiring'],
  ['blockedSpike', 'Spike in blocked requests'],
  ['memberJoined', 'New member joined'],
]

export function MySettings() {
  const d = useDB()
  const now = useNow()
  const u = me(d)
  const [name, setName] = useState(u.name)
  const [webhook, setWebhook] = useState(d.webhook)
  const hookOk = !webhook || /^https:\/\/\S+$/.test(webhook)
  return (
    <div className="max-w-[920px]">
      <PageTitle>My Settings</PageTitle>
      <div className="mt-5 grid grid-cols-2 items-start gap-6">
        <Card className="flex flex-col gap-3.5 rounded-xl p-6">
          <div className="eyebrow-sm">Profile</div>
          <div className="flex items-center gap-3">
            <Avatar initials={initials(u.name)} size={40} />
            <div>
              <div className="text-md font-semibold">{u.name}</div>
              <div className="text-xs text-zinc-500">Photo from Google</div>
            </div>
          </div>
          <Field label="Name">
            <div className="flex gap-2">
              <Input value={name} onChange={(e) => setName(e.target.value)} className="bg-rail" />
              <Button disabled={!name.trim() || name === u.name} onClick={() => actions.renameMe(name.trim())}>
                Save
              </Button>
            </div>
          </Field>
          <Field label="Email" optional="read-only" hint="You sign in with Google — there’s no password to change.">
            <div className="rounded-lg border border-edge bg-rail px-3 py-[9px] text-[13px] text-zinc-500">{u.email}</div>
          </Field>
          <div className="eyebrow-sm mt-1">Active sessions</div>
          {u.sessions.length ? (
            u.sessions.map((s, i) => (
              <div key={i} className={`flex items-center justify-between text-sm2 ${i ? 'text-zinc-400' : ''}`}>
                <span>
                  {s.device} · {s.place} · {i === 0 ? 'now' : ago(s.at, now)}
                </span>
                <button className="text-xs text-brass hover:text-brass-light" onClick={() => actions.signOutSession(i)}>
                  Sign out
                </button>
              </div>
            ))
          ) : (
            <div className="text-sm2 text-zinc-500">No other sessions.</div>
          )}
        </Card>
        <Card className="flex flex-col gap-3 rounded-xl p-6">
          <div className="eyebrow-sm">Notifications</div>
          {NOTIFS.map(([k, l]) => (
            <div key={k} className="flex items-center justify-between">
              <span className="text-[13px]">{l}</span>
              <Toggle on={!!d.notifications[k]} onChange={() => actions.toggleNotification(k)} label={l} />
            </div>
          ))}
          <div className="mt-1 flex flex-col gap-1.5 border-t border-line pt-3">
            <label className="text-xs font-medium text-zinc-300">Channel</label>
            <div className="flex items-center gap-2">
              <span className="text-sm2 text-zinc-300">Email</span>
              <span className="text-zinc-600">+</span>
              <input
                value={webhook}
                onChange={(e) => setWebhook(e.target.value)}
                onBlur={() => hookOk && actions.setWebhook(webhook)}
                placeholder="https:// webhook — optional"
                aria-label="Webhook URL"
                className="flex-1 rounded-[7px] border border-edge bg-rail px-3 py-[7px] font-mono text-xs2 text-zinc-300 outline-none placeholder:text-zinc-600 focus:border-zinc-600"
              />
            </div>
            {!hookOk && <div className="text-xs2 text-red-400">Webhook addresses must start with https://</div>}
          </div>
        </Card>
      </div>
    </div>
  )
}

export function AccountSettings() {
  const d = useDB()
  const nav = useNavigate()
  const o = org(d)!
  const owner = myRole(d) === 'Owner'
  const [name, setName] = useState(o.name)
  const [deleting, setDeleting] = useState(false)
  const [typed, setTyped] = useState('')
  const [renamed, setRenamed] = useState(false)
  return (
    <div className="max-w-[480px]">
      <PageTitle>Account</PageTitle>
      <Card className="mt-5 flex flex-col gap-3.5 rounded-xl p-6">
        <Field label="Organization name">
          <div className="flex gap-2">
            <Input value={name} onChange={(e) => setName(e.target.value)} disabled={!isAdmin(d)} className="bg-rail" />
            <Button
              disabled={!owner || !name.trim() || name === o.name}
              onClick={() => {
                actions.renameOrg(name)
                setRenamed(true)
              }}
            >
              Rename
            </Button>
          </div>
        </Field>
        {renamed && <div className="-mt-2 text-xs text-green-400">Renamed.</div>}
        <div className="rounded-lg border border-edge bg-rail px-3.5 py-3 text-sm2 text-zinc-400">Billing — placeholder</div>
        <div className="rounded-lg border border-edge bg-rail px-3.5 py-3 text-sm2 text-zinc-400">Sign-in provider — Google · SAML/OIDC coming soon</div>
        <div className="flex flex-col gap-2 rounded-lg border border-red-500/30 bg-red-500/[0.04] p-3.5">
          <div className="text-sm2 font-semibold text-red-400">Delete organization</div>
          <div className="text-xs text-zinc-400">Owner only. Type the name to confirm. Everything inside is listed first.</div>
          <input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            disabled={!owner}
            placeholder={`Type "${o.name}" to confirm`}
            aria-label="Type the organization name to confirm"
            className="rounded-[7px] border border-zinc-700 bg-rail px-3 py-2 text-sm2 text-zinc-200 outline-none placeholder:text-zinc-600 disabled:opacity-50"
          />
          <Button variant="danger" size="sm" className="self-start" disabled={!owner || typed !== o.name} onClick={() => setDeleting(true)}>
            Delete organization
          </Button>
          {!owner && <div className="text-xs2 text-zinc-500">Only an Owner can delete the organization.</div>}
        </div>
      </Card>
      <DeleteOrgDialog open={deleting} onClose={() => setDeleting(false)} onDeleted={() => nav('/')} />
    </div>
  )
}

/* ------------------------------------------------------------------ */
/* Support console — superAdmin only, deliberately sparse.            */
/* ------------------------------------------------------------------ */
export function SupportConsole() {
  const d = useDB()
  const now = useNow()
  const [q, setQ] = useState('')
  const [picked, setPicked] = useState<string | null>(null)
  const [confirm, setConfirm] = useState<null | 'lock' | 'unlock' | 'signout'>(null)
  const [reason, setReason] = useState('')
  const matches = q.trim().length >= 2 ? d.users.filter((u) => u.email.toLowerCase().includes(q.trim().toLowerCase())) : []
  const u = d.users.find((x) => x.id === picked)
  const orgNames = u ? Object.keys(u.roles).map((id) => d.orgs.find((o) => o.id === id)?.name).filter(Boolean) : []
  const pendingInvite = u ? d.invites.find((i) => i.email === u.email) : null
  // Support sees membership, never contents: a count of agents, not their names.
  const agentCount = u ? agentsCreatedBy(d, u, Object.keys(u.roles)).length : 0
  const ask = (c: NonNullable<typeof confirm>) => {
    setReason('')
    setConfirm(c)
  }
  const reasonOk = reason.trim().length >= 3

  const btnLight = 'rounded-lg bg-slate-200 px-3.5 py-2 text-sm2 font-semibold text-slate-900 hover:bg-white disabled:opacity-40'
  const btnLine = 'rounded-lg border border-support-edge px-3.5 py-2 text-sm2 text-slate-300 hover:bg-white/[0.03] disabled:opacity-40'

  return (
    <div className="flex h-full flex-col bg-support-bg font-sans text-slate-200">
      <div className="flex items-center gap-3 border-b border-support-line bg-support-rail px-8 py-4">
        <span className="size-2 rounded-[2px] bg-sky-400" />
        <span className="text-sm font-semibold">Support console</span>
        <span className="ml-auto text-xs text-slate-500">support@keyhole.dev</span>
      </div>
      <div className="flex flex-1 flex-col items-center gap-6 overflow-y-auto px-4 pt-16">
        <div className="relative w-[560px] max-w-full">
          <input
            value={q}
            onChange={(e) => {
              setQ(e.target.value)
              setPicked(null)
            }}
            placeholder="Search by email…"
            aria-label="Search by email"
            className="w-full rounded-[10px] border border-support-edge bg-support-rail px-4 py-[11px] text-md text-slate-200 outline-none placeholder:text-slate-500 focus:border-sky-500/50"
          />
          {!picked && matches.length > 0 && (
            <div className="absolute top-full right-0 left-0 z-10 mt-1 overflow-hidden rounded-[10px] border border-support-edge bg-support-rail">
              {matches.map((m) => (
                <button key={m.id} onClick={() => setPicked(m.id)} className="block w-full px-4 py-2.5 text-left text-[13px] text-slate-300 hover:bg-white/[0.04]">
                  {m.email}
                </button>
              ))}
            </div>
          )}
          {q.trim().length >= 2 && !matches.length && <div className="mt-2 text-xs text-slate-500">No account with that email.</div>}
        </div>

        {u && (
          <div className="flex w-[560px] max-w-full flex-col gap-4 rounded-xl border border-support-line bg-support-rail p-6">
            <div className="flex items-center gap-3">
              <span className="inline-flex size-[34px] items-center justify-center rounded-full bg-support-line text-xs font-semibold text-slate-400">{initials(u.name)}</span>
              <div>
                <div className="text-md font-semibold">{u.email}</div>
                <div className={`text-xs ${u.locked ? 'text-amber-500' : 'text-slate-400'}`}>
                  {u.locked ? `Locked · ${new Date(u.lockedAt!).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' })} today` : u.status === 'invited' ? 'Invited · hasn’t signed in' : 'Active'}
                </div>
              </div>
            </div>
            <div className="grid grid-cols-[130px_1fr] gap-x-3 gap-y-1.5 text-sm2">
              <span className="text-slate-500">Organizations</span>
              <span>{orgNames.join(' · ') || '—'}</span>
              <span className="text-slate-500">Sessions</span>
              <span>{u.sessions.length} active</span>
              <span className="text-slate-500">Invite</span>
              <span>{pendingInvite ? `Pending · expires ${until(pendingInvite.expiresAt, now).toLowerCase()}` : 'Accepted'}</span>
              {u.locked && (
                <>
                  <span className="text-slate-500">Lock reason</span>
                  <span>{u.lockReason ?? '—'}</span>
                </>
              )}
              {u.unlockRequest && (
                <>
                  <span className="text-slate-500">Unlock request</span>
                  <span className="text-amber-500">
                    {u.unlockRequest.by} · {ago(u.unlockRequest.at, now).toLowerCase()}
                  </span>
                </>
              )}
            </div>
            <div className="flex gap-2 border-t border-support-line pt-4">
              <button className={btnLight} onClick={() => ask(u.locked ? 'unlock' : 'lock')}>
                {u.locked ? 'Unlock account' : 'Lock account'}
              </button>
              <button className={btnLine} disabled={!u.sessions.length} onClick={() => ask('signout')}>
                Sign out everywhere
              </button>
              <button className={btnLine} disabled={!pendingInvite} onClick={() => actions.supportResendInvite(u.id)}>
                Resend invite
              </button>
            </div>
          </div>
        )}
        <div className="max-w-[560px] text-center text-xs text-slate-500">Support sees which organizations an account belongs to — never what’s inside them: no keys, secrets, tools, or traffic.</div>
      </div>

      <Modal
        open={!!confirm}
        onClose={() => setConfirm(null)}
        width={460}
        title={confirm === 'lock' ? `Lock ${u?.email}?` : confirm === 'unlock' ? `Unlock ${u?.email}?` : `Sign ${u?.email} out everywhere?`}
      >
        {confirm !== 'unlock' && u && (
          <div className="flex flex-col gap-2.5 rounded-[10px] border border-edge bg-rail p-4 text-sm2">
            <div className="flex justify-between gap-6">
              <span className="text-zinc-500">Sessions ended</span>
              <span>{u.sessions.length}</span>
            </div>
            <div className="flex justify-between gap-6">
              <span className="text-zinc-500">Organizations notified</span>
              <span className="text-right">{orgNames.join(', ') || '—'}</span>
            </div>
            {confirm === 'lock' && (
              <div className="flex justify-between gap-6">
                <span className="text-zinc-500">Agents they created</span>
                <span className="text-right">{agentCount ? `${agentCount} · keep working (they belong to the organization)` : 'None'}</span>
              </div>
            )}
          </div>
        )}
        <div className="text-sm2 leading-relaxed text-zinc-400">
          {confirm === 'lock'
            ? 'They can’t sign in until unlocked. Their organizations see this action and your reason in their audit log; they see the reason on the sign-in screen.'
            : 'They can sign in again right away. Their organizations see this action and your reason in their audit log.'}
        </div>
        <Field label="Reason or ticket ID" hint="Required. Keep it factual — the customer reads it.">
          <Input value={reason} onChange={(e) => setReason(e.target.value)} placeholder="SUP-4821 · suspected session theft" autoFocus />
        </Field>
        <Footer>
          <Button size="lg" onClick={() => setConfirm(null)}>
            Cancel
          </Button>
          <Button
            size="lg"
            variant="primary"
            disabled={!reasonOk}
            onClick={() => {
              if (u && confirm === 'lock') actions.setLocked(u.id, true, reason.trim())
              if (u && confirm === 'unlock') actions.setLocked(u.id, false, reason.trim())
              if (u && confirm === 'signout') actions.signOutEverywhere(u.id, reason.trim())
              setConfirm(null)
            }}
          >
            {confirm === 'lock' ? 'Lock account' : confirm === 'unlock' ? 'Unlock account' : 'Sign out everywhere'}
          </Button>
        </Footer>
      </Modal>
    </div>
  )
}

export function NotFound() {
  return (
    <div className="text-sm text-zinc-400">
      Nothing here. <Link to="/">Go home</Link>
    </div>
  )
}
