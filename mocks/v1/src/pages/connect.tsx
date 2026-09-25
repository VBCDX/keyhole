import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { ago } from '../lib/format'
import { actions, agentById, getDB, isAdmin, liveTool, log, orgEvents, protocolList, sessionTokenFor, toolById, update, useDB, useNow } from '../lib/store'
import type { AuditEvent, Workspace } from '../lib/types'
import { SidecarEnrollPanel } from '../components/SidecarEnrollPanel'
import { sidecarAppConfig } from '../lib/enroll'
import { ImpactDialog } from '../components/shared'
import { CopyChip, KeyholeIcon } from '../components/keyhole'
import { Button, Card, Field, Segmented, Select, SlideOver, Toggle, Dot } from '../components/ui'
import { useWorkspace } from './workspaces'

type Kind = 'mcp' | 'https'
type Client = 'desktop' | 'code' | 'cursor'
const FIRST_REQUEST_AFTER_MS = 4000

const host = (w: Workspace) => `https://${w.slug.replace('_', '-')}.keyhole.dev`
const serverName = (w: Workspace) => `keyhole-${w.name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`

function configFor(kind: Kind, client: Client, w: Workspace, token: string) {
  const url = `${host(w)}/mcp`
  if (kind === 'https') {
    return {
      filename: 'keyhole.env',
      text: `# Keyhole · ${w.name}\nKEYHOLE_BASE_URL=${host(w)}\nKEYHOLE_TOKEN=${token}\n`,
    }
  }
  const headers = { Authorization: `Bearer ${token}` }
  const server = client === 'code' ? { type: 'http', url, headers } : { url, headers }
  const text = JSON.stringify({ mcpServers: { [serverName(w)]: server } }, null, 2) + '\n'
  return { filename: client === 'desktop' ? 'claude_desktop_config.json' : client === 'code' ? '.mcp.json' : 'mcp.json', text }
}

/** Renders a config with the token masked and brass-highlighted. */
function ConfigPreview({ text, masked }: { text: string; masked: string }) {
  const parts = text.split(masked)
  return (
    <pre className="m-0 overflow-x-auto rounded-lg border border-edge bg-rail px-4 py-3.5 font-mono text-xs2 leading-[1.7] text-zinc-400">
      {parts.map((p, i) => (
        <span key={i}>
          {p}
          {i < parts.length - 1 && <span className="text-brass-light">{masked}</span>}
        </span>
      ))}
    </pre>
  )
}

function download(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: 'text/plain' }))
  const a = document.createElement('a')
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

type Verify = { state: 'idle' | 'waiting' | 'success' | 'failed'; event?: AuditEvent }

function VerifyCard({ v, w, last }: { v: Verify; w: Workspace; last?: AuditEvent }) {
  const d = useDB()
  const now = useNow()
  if (v.state === 'success' && v.event) {
    const e = v.event
    const req = orgEvents(d).find((x) => x.type === 'request' && x.workspaceId === w.id && x.actorId === e.actorId && Math.abs(x.at - e.at) < 2000)
    const action = req?.detail?.find(([k]) => k === 'Action')?.[1]
    return (
      <div className="flex items-start gap-4 rounded-[10px] border border-green-500/30 bg-green-500/[0.06] p-5">
        <KeyholeIcon state="closed" size={24} turn className="mt-0.5" />
        <div className="flex-1">
          <div className="text-sm font-semibold text-green-400">First request verified</div>
          <div className="mt-3 grid grid-cols-[110px_1fr] gap-x-3 gap-y-1.5 text-sm2">
            <span className="text-zinc-500">Agent</span>
            {/* The name at the time of the call: history isn't rewritten by later renames or revocations. */}
            <span>{e.actor}</span>
            {e.via && (
              <>
                <span className="text-zinc-500">Through</span>
                <span>Sidecar {e.via}</span>
              </>
            )}
            <span className="text-zinc-500">Tool</span>
            <span>
              {req?.object ?? '—'}
              {action ? ` · ${action}` : ''}
            </span>
            <span className="text-zinc-500">Response</span>
            <span className="text-green-400">{e.result.replace('200', '200 OK')}</span>
            <span className="text-zinc-500">Tracking code</span>
            <span>
              <CopyChip value={req?.trk ?? e.trk} />
            </span>
            <span className="text-zinc-500">When</span>
            <span className="text-zinc-400">{ago(e.at, now)}</span>
          </div>
          <div className="mt-3 text-xs text-zinc-500">
            This event is also in the workspace's <Link to={`/workspaces/${w.id}/audit`}>audit log</Link>.
          </div>
        </div>
      </div>
    )
  }
  if (v.state === 'failed' && v.event) {
    return (
      <div className="flex items-start gap-4 rounded-[10px] border border-red-500/35 bg-red-500/[0.06] p-5">
        <KeyholeIcon state="error" size={24} className="mt-0.5" />
        <div className="flex-1">
          <div className="text-sm font-semibold text-red-400">First request failed</div>
          <div className="mt-1 text-sm2 text-zinc-300">{v.event.reason}</div>
          <div className="mt-3 flex items-center gap-3 text-sm2">
            <span className="text-zinc-500">Tracking code</span>
            <CopyChip value={v.event.trk} />
            <Link to={`/workspaces/${w.id}/audit?event=${v.event.id}`} className="ml-auto">
              Open in the log →
            </Link>
          </div>
        </div>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-3.5 rounded-[10px] border border-edge bg-rail p-[18px]">
      <KeyholeIcon pulse />
      <div>
        <div className="text-[13px] font-semibold">Waiting for the first request…</div>
        <div className="mt-0.5 text-xs text-zinc-500">Run your agent — its first call through this workspace shows here.</div>
        {last && (
          <div className="mt-1 text-xs2 text-zinc-600">
            Last verified here: {last.actor}, {ago(last.at, now).toLowerCase()}
          </div>
        )}
      </div>
    </div>
  )
}

function ConnectPanel({ kind, w, open, onClose }: { kind: Kind; w: Workspace; open: boolean; onClose: () => void }) {
  const d = useDB()
  const [tab, setTab] = useState<'direct' | 'sidecar'>('direct')
  const [enrollingSidecar, setEnrollingSidecar] = useState(false)
  const sidecars = d.connectors.filter((c) => c.kind === 'sidecar' && c.workspaceId === w.id)
  const admin = isAdmin(d)
  const [client, setClient] = useState<Client>('desktop')
  const agents = w.agentIds.map((id) => agentById(d, id)).filter((a) => a && a.status !== 'revoked') as NonNullable<ReturnType<typeof agentById>>[]
  const [agentId, setAgentId] = useState('')
  const timer = useRef<number | undefined>(undefined)
  const lastVerified = orgEvents(d).find((e) => e.type === 'verification' && e.workspaceId === w.id)
  const [verify, setVerify] = useState<Verify>({ state: 'idle' })

  useEffect(() => {
    if (open) {
      const fresh = agents.find((a) => sessionTokenFor(a.id))
      setAgentId((fresh ?? agents[0])?.id ?? '')
      // Never show an earlier success as if it were this agent's: start from Waiting.
      setVerify({ state: 'waiting' })
    }
    return () => window.clearTimeout(timer.current)
  }, [open]) // eslint-disable-line

  const agent = agents.find((a) => a.id === agentId)
  const token = agent ? sessionTokenFor(agent.id) : null
  const masked = agent ? `kh_live_••••${agent.tokenLast4}` : '<PASTE_AGENT_TOKEN>'
  const preview = configFor(kind, client, w, token ? masked : '<PASTE_AGENT_TOKEN>')

  /** Direct: the agent picked above. Sidecar: the agent the sidecar acts as, with the call going through it. */
  const expectFirstRequest = (source: 'download' | 'sidecar', sidecarId?: string) => {
    setVerify({ state: 'waiting' })
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => {
      const sc = sidecarId ? getDB().connectors.find((c) => c.id === sidecarId) : undefined
      const a = sc ? agentById(getDB(), sc.agentId ?? '') : agent ? agentById(getDB(), agent.id) : null
      if (!a || a.status !== 'active') {
        let ev: AuditEvent | undefined
        update((dd) => {
          ev = log(dd, {
            type: 'blocked',
            severity: 'blocked',
            actor: a?.label ?? 'unknown agent',
            actorKind: 'agent',
            actorId: a?.id,
            workspaceId: w.id,
            ...(sc ? { via: sc.name, viaId: sc.id } : {}),
            object: `${w.name} · ${sc ? 'sidecar' : kind.toUpperCase()} connect`,
            result: a?.status === 'suspended' ? 'Agent suspended' : 'Token revoked',
            reason: a?.status === 'suspended' ? `Blocked: ${a.label} is suspended. Resume it on the Agents page, then run it again.` : 'Blocked: this token isn’t valid. Create a new agent and download a fresh config.',
          })
        })
        setVerify({ state: 'failed', event: ev })
        return
      }
      actions.landFirstCall(w.id, a.id, sc?.id)
      const ev = orgEvents(getDB()).find((e) => e.type === 'verification' && e.workspaceId === w.id)
      setVerify({ state: 'success', event: ev })
    }, source === 'sidecar' ? FIRST_REQUEST_AFTER_MS / 2 : FIRST_REQUEST_AFTER_MS)
  }

  const onDownload = () => {
    const cfg = configFor(kind, client, w, token ?? '<PASTE_AGENT_TOKEN>')
    download(cfg.filename, cfg.text)
    actions.markConfigDownloaded()
    expectFirstRequest('download')
  }

  const firstTool = w.tools.map((t) => liveTool(toolById(d, t.toolId))).find(Boolean)
  const curl = `curl ${host(w)}/${firstTool?.internalName ?? 'tool'}${firstTool?.actions[0]?.path.replace(/\{.*?\}/g, 'ID') ?? '/'} \\\n  -H "Authorization: Bearer $KEYHOLE_TOKEN"`

  return (
    <SlideOver open={open} onClose={onClose} width={560} title={`Connect · ${kind === 'mcp' ? 'MCP' : 'HTTPS'}`}>
      <Segmented
        value={tab}
        onChange={setTab}
        options={[
          { value: 'direct', label: 'Direct' },
          { value: 'sidecar', label: 'Sidecar' },
        ]}
        className="-mt-0.5 [&>button]:font-semibold"
      />
      {tab === 'direct' ? (
        <div className="flex flex-col gap-4">
          <Field label="Workspace address">
            <CopyChip variant="block" value={kind === 'mcp' ? `${host(w)}/mcp` : host(w)} />
          </Field>
          {agents.length === 0 ? (
            <Card className="p-5 text-center">
              <div className="text-[13px] text-zinc-400">No agents on this workspace yet.</div>
              <Link to="/players/agents?new=1" className="mt-1 inline-block text-sm2">
                Create one on the Agents page
              </Link>
            </Card>
          ) : (
            <>
              <Field label="Agent">
                <Select
                  value={agentId}
                  onChange={(e) => {
                    setAgentId(e.target.value)
                    window.clearTimeout(timer.current)
                    setVerify({ state: 'waiting' })
                  }}
                  className="bg-rail"
                >
                  {agents.map((a) => (
                    <option key={a.id} value={a.id}>
                      {a.label}
                      {a.status === 'suspended' ? ' (suspended)' : ''}
                    </option>
                  ))}
                </Select>
              </Field>
              <div className="flex flex-col gap-2">
                <div className="flex items-center justify-between">
                  {kind === 'mcp' ? (
                    <Segmented
                      size="sm"
                      value={client}
                      onChange={setClient}
                      options={[
                        { value: 'desktop', label: 'Claude Desktop' },
                        { value: 'code', label: 'Claude Code' },
                        { value: 'cursor', label: 'Cursor' },
                      ]}
                    />
                  ) : (
                    <span className="text-xs font-medium text-zinc-300">Settings block</span>
                  )}
                  <Button variant="primary" size="sm" onClick={onDownload}>
                    Download config
                  </Button>
                </div>
                <ConfigPreview text={preview.text} masked={masked} />
                {kind === 'https' && (
                  <Field label="Example request">
                    <CopyChip variant="command" value={curl.replace('\\\n  ', '')} display={<span className="whitespace-pre-wrap">{curl}</span>} neutral />
                  </Field>
                )}
                <div className="text-xs2 text-zinc-500">
                  {token
                    ? 'The downloaded file has the full token filled in this session only — on screen it stays masked. Tokens are never re-shown.'
                    : 'This token isn’t available anymore, so the file contains <PASTE_AGENT_TOKEN> — paste the token you stored when the agent was created. Tokens are never re-shown.'}
                </div>
              </div>
            </>
          )}
        </div>
      ) : (
        <div className="flex flex-col gap-4">
          <div className="text-sm2 leading-relaxed text-zinc-400">
            A sidecar runs next to your app or agent harness and acts as one agent of this workspace. The app calls the sidecar as if it were calling the service; the sidecar fetches the key for the
            tool slot and forwards the call with it. Keys never enter the app’s memory or disk, and the app holds no Keyhole token.
          </div>
          {sidecars.length > 0 && (
            <div className="flex flex-col gap-2">
              {sidecars.map((sc) => {
                const a = agentById(d, sc.agentId ?? '')
                return (
                  <div key={sc.id} className="flex items-center gap-3 rounded-[10px] border border-edge bg-rail px-4 py-3">
                    <Dot health={sc.health} />
                    <div className="min-w-0 flex-1">
                      <div className="text-[13px] font-medium">
                        {sc.name} <span className="font-normal text-zinc-500">· as {a?.label ?? '—'}</span>
                      </div>
                      <div className="truncate text-xs text-zinc-500">
                        {protocolList(sc)} · <span className="font-mono">{sc.listen}</span>
                      </div>
                    </div>
                    <Button
                      size="sm"
                      onClick={() => {
                        const cfg = sidecarAppConfig(sc.listen ?? '127.0.0.1:8787', sc.protocols ?? [])
                        download(`${sc.name}.env`, cfg.env + (cfg.mcp ? `\n# MCP clients:\n# ${cfg.mcp.replace(/\n/g, '\n# ')}` : ''))
                        actions.markConfigDownloaded()
                        expectFirstRequest('sidecar', sc.id)
                      }}
                    >
                      Download app config
                    </Button>
                  </div>
                )
              })}
            </div>
          )}
          {!sidecars.length && !enrollingSidecar && <Card className="p-5 text-center text-[13px] text-zinc-400">No sidecars on this workspace yet.</Card>}
          {admin &&
            (enrollingSidecar ? (
              <SidecarEnrollPanel workspaceId={w.id} onEnrolled={(id) => expectFirstRequest('sidecar', id)} />
            ) : (
              <Button className="self-start" onClick={() => setEnrollingSidecar(true)}>
                Enroll a sidecar
              </Button>
            ))}
          {!admin && <div className="text-xs text-zinc-500">Workspace admins enroll sidecars.</div>}
          <div className="text-xs2 text-zinc-500">
            Vault connectors, which reach vaults behind your firewall, are managed on the <Link to="/connectors">Connectors</Link> page.
          </div>
        </div>
      )}
      <div className="mt-auto border-t border-line pt-[18px]">
        <VerifyCard v={verify} w={w} last={lastVerified} />
      </div>
    </SlideOver>
  )
}

export function ConnectTab() {
  const d = useDB()
  const now = useNow()
  const w = useWorkspace()
  const admin = isAdmin(d)
  const [panel, setPanel] = useState<Kind | null>(null)
  const [turningOff, setTurningOff] = useState<Kind | null>(null)
  const active = w.agentIds.map((id) => agentById(d, id)).filter((a) => a?.status === 'active').map((a) => a!.label)
  const lastRequest = orgEvents(d).find((e) => e.workspaceId === w.id && e.type === 'request' && e.actorKind === 'agent')
  const row = (kind: Kind, title: string, sub: string) => {
    const on = w[kind]
    return (
      <div className="flex items-center justify-between rounded-[10px] border border-edge bg-panel px-[18px] py-4">
        <div>
          <div className="text-md font-semibold">{title}</div>
          <div className="mt-0.5 text-xs text-zinc-500">{sub}</div>
        </div>
        <div className="flex items-center gap-3">
          {on && (
            <button type="button" className="text-sm2 text-brass hover:text-brass-light" onClick={() => setPanel(kind)}>
              Connection instructions
            </button>
          )}
          <Toggle
            on={on}
            label={`Turn ${title} ${on ? 'off' : 'on'}`}
            disabled={!admin}
            onChange={(v) => {
              // Turning a method on is harmless; turning it off disconnects agents, so it's previewed first.
              if (!v) return setTurningOff(kind)
              actions.setConnection(w.id, kind, true)
              setPanel(kind)
            }}
          />
        </div>
      </div>
    )
  }
  return (
    <div className="mt-5 flex max-w-[720px] flex-col gap-3">
      {row('https', 'HTTPS', "Apps send their API requests through this workspace's address")}
      {row('mcp', 'MCP', "AI assistants connect here and see this workspace's tools")}
      {!admin && <div className="text-xs text-zinc-500">Only admins can turn connection methods on or off.</div>}
      <ConnectPanel kind={panel ?? 'mcp'} w={w} open={!!panel} onClose={() => setPanel(null)} />
      <ImpactDialog
        open={!!turningOff}
        onClose={() => setTurningOff(null)}
        title={`Turn ${turningOff?.toUpperCase()} off for ${w.name}?`}
        rows={[
          ['Active agents on this workspace', active.join(', ') || 'None', active.length ? 'amber' : undefined],
          ['Last agent request', lastRequest ? `${lastRequest.actor} · ${ago(lastRequest.at, now).toLowerCase()}` : 'Never'],
          ['Other method', turningOff && w[turningOff === 'mcp' ? 'https' : 'mcp'] ? `${turningOff === 'mcp' ? 'HTTPS' : 'MCP'} stays on` : 'Off — nothing can connect'],
        ]}
        body={`Agents connected over ${turningOff?.toUpperCase()} are refused on their next request. Turning it back on restores them with the same tokens.`}
        confirmLabel={`Turn ${turningOff?.toUpperCase()} off`}
        onConfirm={() => turningOff && actions.setConnection(w.id, turningOff, false)}
      />
    </div>
  )
}
