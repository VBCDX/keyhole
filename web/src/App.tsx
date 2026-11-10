import { useEffect, useState, type FormEvent } from 'react'
import { BrowserRouter, Link, Route, Routes } from 'react-router-dom'
import { api, ApiError, type AuthConfig, type Me, type Org, type SuiteApp } from './api'
import { initAnalytics, trackEvent, trackPageView } from './analytics'
import { consumeSignIn, clearSignIn, markSignIn } from './signin'
import { Button, ButtonLink, Card, KeyholeMark, Field, Input } from './ui'
import { AppShell } from './shell/AppShell'
import { ComingSoon, NoOrgShell, NotFound, Suspended } from './pages/misc'
import { OrgDetail, OrgIndex, OrgsList, OrgTabSoon } from './pages/orgs'
import { StoreDetail, StoresTab } from './pages/stores'
import { SessionProvider, useSession } from './session'

// Messages for the ?error= codes the server redirects back with.
const REDIRECT_ERRORS: Record<string, string> = {
  signin_failed: 'Sign-in with VBCDX did not complete. Please try again.',
  provider_unavailable: 'The VBCDX sign-in service is not reachable right now. Please try again shortly.',
  zitadel_not_enabled: 'Sign-in with VBCDX is not enabled on this server.',
}

type State =
  | { kind: 'loading' }
  | { kind: 'failed'; message: string }
  | { kind: 'signed-out'; config: AuthConfig }
  | { kind: 'signed-in'; me: Me; apps: SuiteApp[] }

// Silent sign-in (signed in to another VBCDX app, so no password needed).
// The server allows one attempt a minute and none after a sign-out; this
// per-tab guard also stops a loop in a browser that refuses cookies.
const SILENT_KEY = 'keyhole.silent_sign_in_at'
const SILENT_GUARD_MS = 60_000

function trySilentSignIn(config: AuthConfig, hadError: boolean): boolean {
  if (!config.zitadel || !config.silent_sign_in || hadError) return false
  const last = Number(sessionStorage.getItem(SILENT_KEY) ?? 0)
  if (Date.now() - last < SILENT_GUARD_MS) return false
  sessionStorage.setItem(SILENT_KEY, String(Date.now()))
  markSignIn() // the silent SSO attempt is still a sign-in
  window.location.assign('/auth/oidc/login?prompt=none')
  return true
}

export function App() {
  const [state, setState] = useState<State>({ kind: 'loading' })
  const [notice, setNotice] = useState<string | null>(null)

  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get('error')
    if (code) {
      setNotice(REDIRECT_ERRORS[code] ?? 'Sign-in failed. Please try again.')
      window.history.replaceState(null, '', window.location.pathname)
    }
    Promise.all([api.authConfig(), api.me()])
      .then(([config, me]) => {
        // Analytics is server-configured: load the tag as soon as the
        // config says so; with no id this is a no-op.
        initAnalytics(config.ga_measurement_id)
        trackPageView(window.location.pathname, document.title)
        // A sign-in this browser started (Zitadel redirect or silent SSO)
        // just completed: one aggregate event, method from the session.
        if (me && consumeSignIn()) trackEvent('login', { method: me.method })
        if (me) setState({ kind: 'signed-in', me, apps: config.suite_apps })
        else if (!trySilentSignIn(config, code !== null)) setState({ kind: 'signed-out', config })
      })
      .catch(() => setState({ kind: 'failed', message: 'Keyhole is not responding. Reload to try again.' }))
  }, [])

  switch (state.kind) {
    case 'loading':
      return <Card><div className="h-24 skeleton" aria-busy="true" aria-label="Loading" /></Card>
    case 'failed':
      return <Card><p role="alert" className="text-sm2 text-red-400">{state.message}</p></Card>
    case 'signed-out':
      return <SignIn config={state.config} notice={notice} onSignedIn={(me) => setState({ kind: 'signed-in', me, apps: state.config.suite_apps })} />
    case 'signed-in':
      return <SignedIn me={state.me} apps={state.apps} />
  }
}

function SignIn({ config, notice, onSignedIn }: { config: AuthConfig; notice: string | null; onSignedIn: (me: Me) => void }) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(notice)

  async function submit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      const me = await api.login(email, password)
      trackEvent('login', { method: 'local' })
      // The local completion reports itself; discard any marker an earlier
      // silent-SSO attempt left behind so a later reload cannot double-count.
      clearSignIn()
      onSignedIn(me)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) setError('Wrong email or password.')
      else if (err instanceof ApiError && err.status === 429) {
        const minutes = Math.max(1, Math.ceil((err.retryAfter ?? 60) / 60))
        setError(`Too many failed sign-ins. Try again in ${minutes} minute${minutes === 1 ? '' : 's'}.`)
      } else setError('Sign-in failed. Please try again.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <Card>
      <div className="flex items-center gap-2.5">
        <KeyholeMark size={20} />
        <h1 className="text-[15px] font-semibold">Sign in to Keyhole</h1>
      </div>

      {error && (
        <p role="alert" className="mt-4 rounded-lg border border-red-900/60 bg-red-950/40 px-3 py-2 text-sm2 text-red-300">
          {error}
        </p>
      )}

      {config.zitadel && (
        <ButtonLink variant="primary" href="/auth/oidc/login" className="mt-5 w-full" onClick={markSignIn}>
          Sign in with VBCDX
        </ButtonLink>
      )}

      {config.zitadel && config.local && (
        <div className="my-5 flex items-center gap-3 text-2xs uppercase tracking-[0.08em] text-zinc-600">
          <span className="h-px flex-1 bg-line" />
          or
          <span className="h-px flex-1 bg-line" />
        </div>
      )}

      {config.local && (
        <form onSubmit={submit} className={config.zitadel ? 'flex flex-col gap-4' : 'mt-5 flex flex-col gap-4'}>
          <Field label="Email" htmlFor="email">
            <Input id="email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
          </Field>
          <Field label="Password" htmlFor="password">
            <Input id="password" type="password" autoComplete="current-password" required value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Button type="submit" variant={config.zitadel ? 'secondary' : 'primary'} disabled={busy}>
            {busy ? 'Signing in…' : 'Sign in'}
          </Button>
        </form>
      )}

      {!config.local && !config.zitadel && <p className="mt-4 text-sm2 text-zinc-400">No sign-in method is enabled.</p>}
    </Card>
  )
}

/** Loads the user's organizations, then hands over to the app. */
function SignedIn({ me, apps }: { me: Me; apps: SuiteApp[] }) {
  const [orgs, setOrgs] = useState<Org[] | null>(null)
  const [failed, setFailed] = useState(false)
  useEffect(() => {
    api.orgs().then(setOrgs, (e) => {
      if (e instanceof ApiError && e.status === 401) window.location.assign('/')
      else setFailed(true)
    })
  }, [])
  if (failed) return <Card><p role="alert" className="text-sm2 text-red-400">Keyhole is not responding. Reload to try again.</p></Card>
  if (!orgs) return <Card><div className="h-24 skeleton" aria-busy="true" aria-label="Loading" /></Card>
  return (
    <SessionProvider me={me} orgs={orgs} apps={apps}>
      <BrowserRouter>
        <Routed />
      </BrowserRouter>
    </SessionProvider>
  )
}

/** The mock's routes (mocks/v1/src/App.tsx). Screens not built yet show "Coming soon". */
function Routed() {
  const { current } = useSession()
  if (!current) return <NoOrgShell />
  if (current.status === 'suspended') return <Suspended />
  const soon = (title: string) => <ComingSoon title={title} />
  return (
    <Routes>
      <Route element={<AppShell />}>
        <Route
          index
          element={
            <ComingSoon title="Home">
              <Link to={`/orgs/${current.id}/stores`}>Open {current.name}’s stores →</Link>
            </ComingSoon>
          }
        />
        <Route path="invitations" element={soon('Invitations')} />
        <Route path="orgs" element={<OrgsList />} />
        <Route path="orgs/:orgId" element={<OrgDetail />}>
          <Route index element={<OrgIndex />} />
          <Route path="stores" element={<StoresTab />} />
          <Route path="stores/:storeId" element={<StoreDetail />} />
          <Route path="*" element={<OrgTabSoon />} />
        </Route>
        <Route path="workspaces/*" element={soon('Workspaces')} />
        <Route path="tools/*" element={soon('Tools')} />
        <Route path="players/users/*" element={soon('Users')} />
        <Route path="players/agents/*" element={soon('Agents')} />
        <Route path="connectors" element={soon('Connectors')} />
        <Route path="audit" element={soon('Audit')} />
        <Route path="settings/me" element={soon('My Settings')} />
        <Route path="settings/account" element={soon('Account')} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  )
}
