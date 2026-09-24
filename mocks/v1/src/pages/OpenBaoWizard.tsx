import { Fragment, useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { EGRESS_IPS } from '../lib/catalog'
import { actions, isAdmin, org, orgConnectors, storeById, useDB } from '../lib/store'
import { EnrollPanel } from '../components/EnrollPanel'
import { NoAccess } from '../components/shared'
import { CopyChip, KeyholeIcon, SECRET_LINE, SecretField } from '../components/keyhole'
import { Breadcrumb, Button, Callout, Dot, Field, Footer, Input, OptionCard, Pill, Segmented, Select, SlideOver, Toggle, cx } from '../components/ui'

const STEPS = ['Basics', 'How Keyhole reaches it', 'How Keyhole signs in', 'What Keyhole may read', 'Certificate', 'Test connection']
type Outcome = 'sealed' | 'denied' | 'unreachable' | 'needsConnector' | 'ok'
const AUTH_LABEL = { approle: 'AppRole', kubernetes: 'Kubernetes', token: 'Token' } as const

/** Keyed by the store being edited, so switching between editing and connecting starts fresh. */
export function OpenBaoWizard() {
  const d = useDB()
  const { orgId } = useParams()
  const [params] = useSearchParams()
  // Editing is limited to this organization's own stores.
  const editing = params.get('edit') ? storeById(d, params.get('edit')!) : null
  if (editing && editing.orgId !== d.currentOrgId)
    return (
      <div className="mt-6">
        <NoAccess what="store" to={`/orgs/${d.currentOrgId}/stores`} back="Back to stores" />
      </div>
    )
  // Stores are credential sources: connecting or changing one is admin-only, however the page was reached.
  if (!isAdmin(d))
    return (
      <div>
        <Breadcrumb items={[{ label: 'Organizations', to: '/orgs' }, { label: org(d)?.name ?? '', to: `/orgs/${orgId}/overview` }, { label: 'Stores', to: `/orgs/${orgId}/stores` }]} />
        <div className="mt-6 text-sm text-zinc-400">
          Only admins manage stores. <Link to={`/orgs/${orgId}/stores`}>Back to stores</Link>
        </div>
      </div>
    )
  return <Wizard key={params.get('edit') ?? 'new'} />
}

function Wizard() {
  const d = useDB()
  const nav = useNavigate()
  const { orgId } = useParams()
  const [params] = useSearchParams()
  const editing = params.get('edit') ? storeById(d, params.get('edit')!) : null

  const [step, setStep] = useState(editing ? Math.min(5, Number(params.get('step') ?? 5)) : 0)
  const [maxStep, setMaxStep] = useState(editing ? 5 : 0)
  const [name, setName] = useState(editing?.name ?? 'Payments vault')
  const [address, setAddress] = useState(editing?.address ?? 'https://bao.acme.internal:8200')
  const [route, setRoute] = useState<'public' | 'connector'>(editing?.route && editing.route !== 'public' ? 'connector' : 'public')
  const connectors = orgConnectors(d)
  const [connectorId, setConnectorId] = useState(editing?.route && editing.route !== 'public' ? editing.route : (connectors.find((c) => c.health !== 'offline')?.id ?? ''))
  const [enrolling, setEnrolling] = useState(false)
  const [auth, setAuth] = useState<'approle' | 'kubernetes' | 'token'>(editing?.auth ?? 'approle')
  const [roleIdLen, setRoleIdLen] = useState(editing ? 16 : 0)
  const [secretIdLen, setSecretIdLen] = useState(editing ? 20 : 0)
  const [k8sRole, setK8sRole] = useState('keyhole-reader')
  const [tokenLen, setTokenLen] = useState(0)
  /** In edit mode: new sign-in credentials were pasted (their values are never known here). */
  const [credsReplaced, setCredsReplaced] = useState(false)
  const secretLen = (set: (n: number) => void) => (n: number) => {
    set(n)
    setCredsReplaced(true)
  }
  const [path, setPath] = useState(editing?.path ?? 'secret/data/payments/')
  const [cache, setCache] = useState(String(editing?.cacheSeconds ?? 60))
  const [cert, setCert] = useState<string | null>(editing?.certName ?? null)
  const [showSkip, setShowSkip] = useState(!!editing?.skipVerify)
  const [skipVerify, setSkipVerify] = useState(!!editing?.skipVerify)
  const [attempts, setAttempts] = useState(0)
  const [testing, setTesting] = useState(false)
  const [result, setResult] = useState<{ outcome: Outcome; config: string } | null>(null)
  const [showPolicy, setShowPolicy] = useState(false)

  const go = (n: number) => {
    setStep(n)
    setMaxStep((m) => Math.max(m, n))
  }

  const authOk = auth === 'approle' ? roleIdLen > 0 && secretIdLen > 0 : auth === 'kubernetes' ? !!k8sRole.trim() : tokenLen > 0
  // Kubernetes sign-in uses the connector's service account, so it can't work over the public route.
  const k8sPublic = auth === 'kubernetes' && route === 'public'
  // A test result only counts for the exact settings it was run with; any edit asks for a new test.
  const config = JSON.stringify([name.trim(), address, route, connectorId, auth, k8sRole, path, cache, cert, skipVerify, credsReplaced])
  const outcome = result && result.config === config ? result.outcome : null
  const canContinue = [
    !!name.trim() && /^https?:\/\/\S+$/.test(address),
    route === 'public' ? !(k8sPublic && maxStep >= 2) : !!connectorId,
    authOk && !k8sPublic,
    !!path.trim() && Number(cache) >= 0,
    !!cert || skipVerify,
    outcome === 'ok',
  ][step]
  // The step list lets people jump ahead, so the test and Finish/Save re-check every earlier step.
  const missingInfo: (string | null)[] = [
    !name.trim() || !/^https?:\/\/\S+$/.test(address) ? 'Add a name and a vault address that starts with https://.' : null,
    route === 'connector' && !connectorId ? 'Pick a connector.' : route === 'connector' && !connectors.some((c) => c.id === connectorId) ? 'Its connector was revoked — pick another route.' : null,
    !authOk ? (auth === 'token' ? 'Paste a token.' : auth === 'approle' ? 'Paste the role ID and secret ID.' : 'Enter the Kubernetes role.') : null,
    !path.trim() || !(Number(cache) >= 0) ? 'Add a secrets path and a cache time.' : null,
    !cert && !skipVerify ? 'Upload the vault’s certificate, or skip verification.' : null,
  ]
  const incomplete = missingInfo.findIndex(Boolean)
  const canSave = incomplete === -1 && !k8sPublic && outcome === 'ok'

  const runTest = () => {
    if (incomplete !== -1) return
    setTesting(true)
    setResult(null)
    window.setTimeout(() => {
      const chosen = connectors.find((c) => c.id === connectorId)
      let o: Outcome
      if (k8sPublic) o = 'needsConnector'
      else if (/unreachable|\.invalid/.test(address) || (route === 'connector' && (!chosen || chosen.health === 'offline'))) o = 'unreachable'
      else if (!path.startsWith('secret/')) o = 'denied'
      // Flow 2: the first try finds the vault sealed; after unsealing, it passes.
      else if (attempts === 0 && !editing) o = 'sealed'
      else o = 'ok'
      setAttempts((a) => a + 1)
      setResult({ outcome: o, config })
      setTesting(false)
    }, 1300)
  }

  const seg = path.replace(/\/$/, '').split('/').pop() || 'app'
  const keyNames = [`${seg}/api_key`, `${seg}/webhook_secret`, `${seg}/refund_key`, `${seg}/ledger_token`]

  const settings = { name: name.trim(), address, route: route === 'public' ? 'public' : connectorId, auth, path, cacheSeconds: Number(cache), certName: skipVerify ? null : cert, skipVerify }
  const routeLabel = (r?: string) => (r === 'public' ? 'Internet' : `Connector ${connectors.find((c) => c.id === r)?.name ?? '(revoked)'}`)
  const certLabel = (c?: string | null, skip?: boolean) => (skip ? 'Not verified' : (c ?? '—'))
  // Edit mode: what the tested settings change about the saved store.
  const changes: [string, string][] = !editing
    ? []
    : (
        [
          ['Name', editing.name, settings.name],
          ['Vault address', editing.address, address],
          ['Route', routeLabel(editing.route), routeLabel(settings.route)],
          ['Signs in with', AUTH_LABEL[editing.auth ?? 'approle'], AUTH_LABEL[auth]],
          ['Reads from', editing.path, path],
          ['Cache', `${editing.cacheSeconds} s`, `${settings.cacheSeconds} s`],
          ['Certificate', certLabel(editing.certName, editing.skipVerify), certLabel(settings.certName, skipVerify)],
        ] as [string, string | undefined, string][]
      )
        .filter(([, a, b]) => a !== b)
        .map(([k, a, b]): [string, string] => [k, `${a ?? '—'} → ${b}`])
        .concat(credsReplaced ? [['Sign-in credentials', 'Replaced']] : [])

  const finish = () => {
    if (!canSave) return
    if (editing) {
      actions.updateOpenBao(editing.id, settings, changes)
      nav(`/orgs/${orgId}/stores/${editing.id}`)
      return
    }
    actions.addOpenBao(settings, keyNames)
    nav(`/orgs/${orgId}/stores`)
  }

  const title = <div className="text-[15px] font-semibold">{STEPS[step]}</div>

  return (
    <div>
      <Breadcrumb items={[{ label: 'Organizations', to: '/orgs' }, { label: org(d)?.name ?? '', to: `/orgs/${orgId}/overview` }, { label: 'Stores', to: `/orgs/${orgId}/stores` }, { label: editing ? editing.name : 'Connect OpenBao' }]} />
      <h1 className="m-0 mt-1 text-lg font-semibold tracking-[-0.01em]">{editing ? `Test ${editing.name}` : 'Connect OpenBao'}</h1>
      <div className="mt-7 flex gap-10">
        <ol className="flex w-[200px] min-w-[200px] flex-col gap-0.5">
          {STEPS.map((s, i) => (
            <li key={s}>
              <button
                type="button"
                disabled={i > maxStep}
                onClick={() => setStep(i)}
                className={cx('w-full rounded-md px-3 py-[7px] text-left text-[13px]', i === step ? 'bg-line font-semibold text-zinc-100' : i <= maxStep ? 'text-zinc-400 hover:text-zinc-200' : 'text-zinc-500')}
              >
                {i < maxStep && i !== step ? <span className="text-green-400">✓</span> : i + 1} · {s}
              </button>
            </li>
          ))}
        </ol>

        <div className="flex w-[560px] max-w-full flex-col gap-4">
          {step === 0 && (
            <>
              <Field label="Display name" htmlFor="bao-name">
                <Input id="bao-name" value={name} onChange={(e) => setName(e.target.value)} className="bg-panel" />
              </Field>
              <Field label="Vault address" htmlFor="bao-addr" hint="The URL your OpenBao listens on, including the port." error={address && !/^https?:\/\/\S+$/.test(address) ? 'Start with https:// — for example https://bao.acme.internal:8200' : null}>
                <Input id="bao-addr" mono value={address} onChange={(e) => setAddress(e.target.value)} className="bg-panel" />
              </Field>
            </>
          )}

          {step === 1 && (
            <>
              {title}
              <OptionCard name="route" checked={route === 'public'} onSelect={() => setRoute('public')} title="It's reachable from the internet">
                <div className="mt-0.5 text-xs text-zinc-500">Allow-list Keyhole's fixed outgoing addresses on your firewall:</div>
                {route === 'public' && (
                  <div className="mt-2.5 flex gap-2">
                    {EGRESS_IPS.map((ip) => (
                      <CopyChip key={ip} value={ip} neutral variant="block" className="!py-[5px] !text-xs" />
                    ))}
                  </div>
                )}
              </OptionCard>
              {route === 'public' && k8sPublic && maxStep >= 2 && <Callout tone="amber">Kubernetes sign-in only works through a Keyhole connector. Pick a connector here, or choose another sign-in method on the next step.</Callout>}
              <OptionCard name="route" checked={route === 'connector'} onSelect={() => setRoute('connector')} title="Through a Keyhole connector">
                {route === 'connector' ? (
                  <>
                    <div className="mt-0.5 text-xs text-zinc-500">Traffic to the vault goes through your own network.</div>
                    {connectors.length ? (
                      <div className="mt-2.5">
                        <Select aria-label="Connector" value={connectorId} onChange={(e) => setConnectorId(e.target.value)} className="bg-panel">
                          <option value="">Pick a connector…</option>
                          {connectors.map((c) => (
                            <option key={c.id} value={c.id}>
                              {c.health === 'healthy' ? '●' : c.health === 'degraded' ? '◐' : '○'} {c.name} · {d.workspaces.find((w) => w.id === c.workspaceId)?.name}
                              {c.health !== 'healthy' ? ` (${c.health})` : ''}
                            </option>
                          ))}
                        </Select>
                        {connectors.find((c) => c.id === connectorId)?.health === 'offline' && <div className="mt-1.5 text-xs text-amber-400">This connector is offline — Keyhole won’t reach the vault through it until it reports in.</div>}
                      </div>
                    ) : (
                      <div className="mt-1.5 text-xs text-zinc-500">No connectors enrolled yet.</div>
                    )}
                    <button type="button" onClick={() => setEnrolling(true)} className="mt-2 text-xs text-brass hover:text-brass-light">
                      Enroll a new connector
                    </button>
                  </>
                ) : (
                  <div className="mt-0.5 text-xs text-zinc-500">
                    For vaults behind your firewall. Pick an enrolled connector, or{' '}
                    <button
                      type="button"
                      className="text-brass hover:text-brass-light"
                      onClick={() => {
                        setRoute('connector')
                        setEnrolling(true)
                      }}
                    >
                      enroll one
                    </button>
                    .
                  </div>
                )}
              </OptionCard>
            </>
          )}

          {step === 2 && (
            <>
              {title}
              <Segmented
                value={auth}
                onChange={setAuth}
                options={[
                  { value: 'approle', label: 'AppRole' },
                  { value: 'kubernetes', label: 'Kubernetes' },
                  { value: 'token', label: 'Token' },
                ]}
              />
              {auth === 'approle' && (
                <>
                  <Field label="Role ID">
                    <SecretField onLength={secretLen(setRoleIdLen)} placeholder={roleIdLen ? '••••••••••••••••' : 'Paste the role ID'} />
                  </Field>
                  <Field label="Secret ID" hint={SECRET_LINE}>
                    <SecretField onLength={secretLen(setSecretIdLen)} placeholder={secretIdLen ? '••••••••••••••••••••' : 'Paste the secret ID'} />
                  </Field>
                </>
              )}
              {auth === 'kubernetes' && (
                <>
                  <Field label="Role" hint="The OpenBao Kubernetes auth role Keyhole signs in as.">
                    <Input mono value={k8sRole} onChange={(e) => setK8sRole(e.target.value)} />
                  </Field>
                  {k8sPublic ? (
                    <Callout tone="amber">
                      Keyhole signs in with the connector’s service-account token, so this works only through a Keyhole connector running in your cluster. This store is set to reach the vault over the internet.{' '}
                      <button
                        type="button"
                        className="text-brass hover:text-brass-light"
                        onClick={() => {
                          setRoute('connector')
                          go(1)
                        }}
                      >
                        Use a connector instead
                      </button>
                    </Callout>
                  ) : (
                    <Callout tone="neutral">Keyhole signs in with the connector’s service-account token, so this works only through a Keyhole connector running in your cluster.</Callout>
                  )}
                </>
              )}
              {auth === 'token' && (
                <>
                  <Field label="Token" hint={SECRET_LINE}>
                    <SecretField onLength={secretLen(setTokenLen)} placeholder="Paste the token" />
                  </Field>
                  <Callout tone="amber">Static tokens are discouraged — prefer AppRole.</Callout>
                </>
              )}
            </>
          )}

          {step === 3 && (
            <>
              {title}
              <Field label="Secrets path" hint="Keyhole can read keys under this path only.">
                <Input mono value={path} onChange={(e) => setPath(e.target.value)} />
              </Field>
              <Field label="Cache values for">
                <div className="flex items-center gap-2">
                  <Input type="number" min={0} value={cache} onChange={(e) => setCache(e.target.value)} className="w-20" />
                  <span className="text-sm2 text-zinc-500">seconds</span>
                </div>
              </Field>
            </>
          )}

          {step === 4 && (
            <>
              {title}
              <label className="cursor-pointer rounded-[10px] border border-dashed border-zinc-700 p-[22px] text-center hover:border-zinc-500">
                <input
                  type="file"
                  accept=".pem,.crt,.cer"
                  className="sr-only"
                  onChange={(e) => {
                    if (!e.target.files?.[0]) return
                    setCert(e.target.files[0].name)
                    // A certificate turns verification back on.
                    setSkipVerify(false)
                  }}
                />
                {cert ? (
                  <>
                    <div className="font-mono text-[13px] text-zinc-200">{cert}</div>
                    <div className="mt-1 text-xs text-zinc-500">Certificate added · click to replace</div>
                  </>
                ) : (
                  <>
                    <div className="text-[13px] text-zinc-300">Upload the vault's certificate</div>
                    <div className="mt-1 text-xs text-zinc-500">
                      PEM file · drag here or <span className="text-brass">browse</span>
                    </div>
                  </>
                )}
              </label>
              {!showSkip ? (
                <button type="button" onClick={() => setShowSkip(true)} className="self-start text-xs text-zinc-500 hover:text-zinc-300">
                  I can’t provide a certificate
                </button>
              ) : (
                <div className="flex items-center justify-between gap-4 rounded-lg border border-red-500/30 bg-red-500/[0.06] px-3.5 py-3">
                  <div>
                    <div className="text-sm2 font-semibold text-red-400">Skip certificate verification</div>
                    <div className="mt-0.5 text-xs2 text-zinc-400">Only for testing. Traffic can be intercepted without it.</div>
                  </div>
                  <Toggle on={skipVerify} onChange={setSkipVerify} label="Skip certificate verification" />
                </div>
              )}
            </>
          )}

          {step === 5 && (
            <>
              {title}
              {testing ? (
                <div className="flex items-center gap-3.5 rounded-[10px] border border-edge bg-rail p-[18px]">
                  <KeyholeIcon pulse />
                  <div>
                    <div className="text-[13px] font-semibold">Testing the connection…</div>
                    <div className="mt-0.5 text-xs text-zinc-500">
                      Signing in to {address.replace(/^https?:\/\//, '')} and reading <span className="font-mono">{path}</span>
                    </div>
                  </div>
                </div>
              ) : outcome === 'ok' ? (
                <>
                  <div className="flex items-center gap-3.5 rounded-[10px] border border-green-500/30 bg-green-500/[0.06] p-[18px]">
                    <KeyholeIcon state="closed" turn />
                    <div>
                      <div className="text-md font-semibold text-green-400">Connected</div>
                      <div className="mt-0.5 text-sm2 text-zinc-400">
                        Read {editing ? d.keys.filter((k) => k.storeId === editing.id).length : keyNames.length} keys under <span className="font-mono text-xs2">{path}</span> · 96 ms
                      </div>
                    </div>
                    <Button variant="primary" className="ml-auto" disabled={!canSave} onClick={finish}>
                      {!editing ? 'Finish' : changes.length ? 'Save changes' : 'Done'}
                    </Button>
                  </div>
                  {editing && changes.length > 0 && (
                    <div className="rounded-[10px] border border-edge bg-rail p-4 text-sm2">
                      <div className="eyebrow-sm">Saving changes to {editing.name}</div>
                      <div className="mt-2.5 grid grid-cols-[150px_1fr] gap-x-3 gap-y-1.5">
                        {changes.map(([k, v]) => (
                          <Fragment key={k}>
                            <span className="text-zinc-500">{k}</span>
                            <span className={cx(k === 'Certificate' && skipVerify && 'text-red-400')}>{v}</span>
                          </Fragment>
                        ))}
                      </div>
                      <div className="mt-2.5 text-xs text-zinc-500">Nothing changes until you save. The change is recorded in the audit log.</div>
                    </div>
                  )}
                  <div className="flex items-center gap-2.5 rounded-[10px] border border-edge bg-rail px-4 py-3.5">
                    <Dot health="healthy" />
                    <span className="text-md font-semibold">{name}</span>
                    <Pill>OpenBao</Pill>
                    <span className="text-xs text-zinc-500">{editing ? d.keys.filter((k) => k.storeId === editing.id).length : keyNames.length} keys · checked just now</span>
                  </div>
                </>
              ) : outcome ? (
                <FailureCard outcome={outcome} path={path} showPolicy={showPolicy} onPolicy={() => setShowPolicy(!showPolicy)} onConnector={() => { setRoute('connector'); go(1) }} />
              ) : incomplete !== -1 ? (
                <Callout tone="amber">
                  {STEPS[incomplete]} isn’t finished: {missingInfo[incomplete]}{' '}
                  <button type="button" className="text-brass hover:text-brass-light" onClick={() => go(incomplete)}>
                    Go to step {incomplete + 1}
                  </button>
                </Callout>
              ) : (
                <div className="rounded-[10px] border border-edge bg-rail p-[18px] text-sm2 text-zinc-400">Keyhole signs in with the details you gave and tries to read one key under the path. Nothing is stored until you finish.</div>
              )}
              {outcome !== 'ok' && (
                <Footer className="border-t-0 pt-0">
                  <Button size="lg" onClick={() => go(4)} disabled={testing}>
                    Back
                  </Button>
                  <Button size="lg" variant="primary" onClick={runTest} disabled={testing || incomplete !== -1}>
                    {outcome ? 'Test again' : 'Test connection'}
                  </Button>
                </Footer>
              )}
              {outcome && outcome !== 'ok' && (
                <div className="flex flex-col gap-2 border-t border-line pt-3.5">
                  <div className="eyebrow-sm">Other failure states</div>
                  {(['sealed', 'denied', 'unreachable'] as const)
                    .filter((o) => o !== outcome)
                    .map((o) => (
                      <div key={o} className="rounded-lg border border-edge bg-rail px-3.5 py-2.5 text-xs text-zinc-400">
                        <span className="font-semibold text-red-400">{FAIL[o].title}</span> — {FAIL[o].short}
                      </div>
                    ))}
                </div>
              )}
            </>
          )}

          {step < 5 && (
            <Footer className={cx('mt-2', step === 0 && 'border-t-0 pt-0')}>
              <Button size="lg" onClick={() => (step === 0 ? nav(editing ? `/orgs/${orgId}/stores/${editing.id}` : `/orgs/${orgId}/stores`) : go(step - 1))}>
                {step === 0 ? 'Cancel' : 'Back'}
              </Button>
              <Button size="lg" variant="primary" disabled={!canContinue} onClick={() => go(step + 1)}>
                Continue
              </Button>
            </Footer>
          )}
        </div>
      </div>

      <SlideOver open={enrolling} onClose={() => setEnrolling(false)} width={520} title="Enroll connector">
        <EnrollPanel
          onEnrolled={(id) => {
            setConnectorId(id)
            setRoute('connector')
            window.setTimeout(() => setEnrolling(false), 1600)
          }}
        />
      </SlideOver>
    </div>
  )
}

const FAIL = {
  needsConnector: { title: 'Needs a connector', short: 'Kubernetes sign-in only works through a Keyhole connector in your cluster.' },
  sealed: { title: 'Sealed', short: 'Your OpenBao is sealed — unseal it and try again.' },
  denied: { title: 'Not allowed', short: "This login can't read that path — here's an example policy to fix it." },
  unreachable: { title: "Can't reach", short: 'No route to that address — check it, or connect through a Keyhole connector.' },
}

function FailureCard({ outcome, path, showPolicy, onPolicy, onConnector }: { outcome: Exclude<Outcome, 'ok'>; path: string; showPolicy: boolean; onPolicy: () => void; onConnector: () => void }) {
  const policy = `path "${path.replace(/\/$/, '')}/*" {\n  capabilities = ["read"]\n}`
  return (
    <div className="flex gap-3.5 rounded-[10px] border border-red-500/35 bg-red-500/[0.06] p-[18px]">
      <KeyholeIcon state="error" size={20} className="mt-0.5" />
      <div className="min-w-0 flex-1">
        <div className="text-md font-semibold text-red-400">{FAIL[outcome].title}</div>
        {outcome === 'sealed' && (
          <>
            <div className="mt-1 text-sm2 leading-relaxed text-zinc-300">Your OpenBao is sealed — unseal it and try again.</div>
            <div className="mt-2">
              <CopyChip value="bao operator unseal" neutral variant="block" className="!text-xs2" />
            </div>
          </>
        )}
        {outcome === 'denied' && (
          <>
            <div className="mt-1 text-sm2 leading-relaxed text-zinc-300">
              This login can't read that path —{' '}
              <button type="button" onClick={onPolicy} className="text-brass hover:text-brass-light">
                here's an example policy to fix it
              </button>
              .
            </div>
            {showPolicy && (
              <div className="mt-2.5 flex flex-col gap-2">
                <pre className="m-0 rounded-lg border border-edge bg-rail px-3.5 py-3 font-mono text-xs2 leading-relaxed text-zinc-300">{policy}</pre>
                <CopyChip value={policy} display="Copy policy" neutral variant="block" className="!text-xs" />
              </div>
            )}
          </>
        )}
        {outcome === 'needsConnector' && (
          <div className="mt-1 text-sm2 leading-relaxed text-zinc-300">
            Keyhole signs in with the connector’s service-account token, so Kubernetes sign-in can’t work over the internet.{' '}
            <button type="button" onClick={onConnector} className="text-brass hover:text-brass-light">
              Use a connector instead
            </button>
            , or pick another sign-in method.
          </div>
        )}
        {outcome === 'unreachable' && (
          <div className="mt-1 text-sm2 leading-relaxed text-zinc-300">
            No route to that address — check it, or{' '}
            <button type="button" onClick={onConnector} className="text-brass hover:text-brass-light">
              connect through a Keyhole connector
            </button>
            .
          </div>
        )}
      </div>
    </div>
  )
}
