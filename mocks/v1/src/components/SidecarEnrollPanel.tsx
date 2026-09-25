import { useEffect, useRef, useState } from 'react'
import { newEnrollmentToken } from '../lib/format'
import { actions, agentById, orgConnectors, orgWorkspaces, useDB } from '../lib/store'
import type { SidecarProtocol } from '../lib/types'
import { HEARTBEAT_AFTER_MS, enrollmentExpiry, sidecarAppConfig, useCountdown } from '../lib/enroll'
import { CopyChip, KeyholeIcon } from './keyhole'
import { Button, Checkbox, Field, Input, Select } from './ui'

const PROTOCOLS: { id: SidecarProtocol; label: string }[] = [
  { id: 'http', label: 'HTTP' },
  { id: 'https', label: 'HTTPS' },
  { id: 'sse', label: 'SSE' },
  { id: 'mcp', label: 'MCP' },
]

/**
 * Enroll a sidecar: pick the workspace and the one agent it acts as, the protocols it exposes locally,
 * then run the command. The enrollment token is single-use and expires in 15 minutes.
 */
export function SidecarEnrollPanel({ workspaceId, onEnrolled }: { workspaceId?: string; onEnrolled?: (id: string) => void }) {
  const d = useDB()
  const workspaces = orgWorkspaces(d)
  const [wsId, setWsId] = useState(workspaceId ?? workspaces[0]?.id ?? '')
  const ws = workspaces.find((w) => w.id === (workspaceId ?? wsId))
  const agents = (ws?.agentIds ?? []).map((id) => agentById(d, id)).filter((a) => a?.status === 'active') as NonNullable<ReturnType<typeof agentById>>[]
  const [agentId, setAgentId] = useState(agents[0]?.id ?? '')
  const agent = agents.find((a) => a.id === agentId) ?? agents[0]
  const [protocols, setProtocols] = useState<SidecarProtocol[]>(['http', 'mcp'])
  const [listen, setListen] = useState('127.0.0.1:8787')
  const [name, setName] = useState(() => `sidecar-${String(orgConnectors(d).filter((c) => c.kind === 'sidecar').length + 1).padStart(2, '0')}`)
  const [token, setToken] = useState<string | null>(null)
  const [expires, setExpires] = useState<number | null>(null)
  const [enrolled, setEnrolled] = useState<string | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const left = useCountdown(expires)
  useEffect(() => () => window.clearTimeout(timer.current), [])

  const ready = !!ws && !!agent && protocols.length > 0 && /^[\w.-]+:\d+$/.test(listen) && !!name.trim()
  const generate = () => {
    if (!ready) return
    setToken(newEnrollmentToken())
    setExpires(enrollmentExpiry())
    setEnrolled(null)
    window.clearTimeout(timer.current)
    // In the prototype, the sidecar "runs" and reports in a few seconds later.
    timer.current = window.setTimeout(() => {
      const id = actions.enrollSidecar({ name: name.trim(), workspaceId: ws!.id, agentId: agent!.id, protocols, listen })
      setEnrolled(id)
      setToken(null)
      setExpires(null)
      onEnrolled?.(id)
    }, HEARTBEAT_AFTER_MS)
  }

  const cmd = `npx keyholed@latest sidecar --workspace ${ws?.id ?? 'ws_…'} --agent ${agent?.id ?? 'ag_…'} --token ${token ?? '<enrollment-token>'} --listen ${listen} --protocols ${protocols.join(',') || '…'}`
  const cfg = sidecarAppConfig(listen, protocols)
  const conn = enrolled ? d.connectors.find((c) => c.id === enrolled) : null

  return (
    <div className="flex flex-col gap-4">
      <div className="text-sm2 leading-relaxed text-zinc-400">
        The sidecar runs next to your app or agent harness. Your app calls it as if it were calling the service directly; the sidecar signs in to Keyhole as the agent you pick, fetches the key for the
        tool slot and forwards the call with it. The secret never enters your app’s memory or disk.
      </div>
      {!workspaceId && (
        <Field label="Workspace">
          <Select value={wsId} onChange={(e) => setWsId(e.target.value)}>
            {workspaces.map((w) => (
              <option key={w.id} value={w.id}>
                {w.name}
              </option>
            ))}
          </Select>
        </Field>
      )}
      <Field label="Acts as agent" hint="That agent’s tool grants, rate limit, expiry and status apply to everything through this sidecar.">
        {agents.length ? (
          <Select value={agent?.id ?? ''} onChange={(e) => setAgentId(e.target.value)}>
            {agents.map((a) => (
              <option key={a.id} value={a.id}>
                {a.label}
              </option>
            ))}
          </Select>
        ) : (
          <div className="text-xs text-amber-400">No active agents on {ws?.name ?? 'this workspace'}. Add one on the workspace’s Members tab first.</div>
        )}
      </Field>
      <div className="flex gap-4">
        <div className="flex-1">
          <Field label="Name">
            <Input mono value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
        </div>
        <div className="flex-1">
          <Field label="Listens on" hint="Keep it on localhost.">
            <Input mono value={listen} onChange={(e) => setListen(e.target.value)} />
          </Field>
        </div>
      </div>
      <fieldset className="flex flex-col gap-1.5">
        <legend className="mb-1.5 text-xs font-medium text-zinc-300">Protocols to expose locally</legend>
        <div className="flex gap-4">
          {PROTOCOLS.map((p) => (
            <Checkbox key={p.id} checked={protocols.includes(p.id)} onChange={(v) => setProtocols(v ? [...protocols, p.id] : protocols.filter((x) => x !== p.id))} label={p.label} />
          ))}
        </div>
      </fieldset>
      <div className="text-sm2 text-zinc-400">Run this next to your app:</div>
      <CopyChip variant="command" value={cmd} />
      <div className="flex items-center gap-2.5">
        <Button size="sm" onClick={generate} disabled={!ready}>
          {token ? 'Generate a new token' : 'Generate enrollment token'}
        </Button>
        <span className="text-xs2 text-zinc-500">{left ? `Single-use · expires in ${left}` : 'Single-use · expires in 15 minutes'}</span>
      </div>
      <Field label="Then point your app at it">
        <pre className="m-0 overflow-x-auto rounded-lg border border-edge bg-rail px-4 py-3 font-mono text-xs2 leading-[1.7] text-zinc-400">{cfg.env}</pre>
      </Field>
      {cfg.mcp && (
        <Field label="MCP clients">
          <pre className="m-0 overflow-x-auto rounded-lg border border-edge bg-rail px-4 py-3 font-mono text-xs2 leading-[1.7] text-zinc-400">{cfg.mcp}</pre>
        </Field>
      )}
      {conn ? (
        <div className="flex items-center gap-3.5 rounded-[10px] border border-green-500/30 bg-green-500/[0.06] p-[18px]">
          <KeyholeIcon state="closed" turn />
          <div>
            <div className="text-[13px] font-semibold text-green-400">{conn.name} reported in</div>
            <div className="mt-0.5 font-mono text-xs text-zinc-400">
              keyholed {conn.version} · {conn.host} · as {agentById(d, conn.agentId ?? '')?.label}
            </div>
          </div>
        </div>
      ) : (
        <div className="flex items-center gap-3.5 rounded-[10px] border border-edge bg-rail p-[18px]">
          <KeyholeIcon pulse />
          <div>
            <div className="text-[13px] font-semibold">Waiting for the first heartbeat…</div>
            <div className="mt-0.5 text-xs text-zinc-500">This turns green the moment the sidecar reports in.</div>
          </div>
        </div>
      )}
    </div>
  )
}
