import { useEffect, useRef, useState } from 'react'
import { newEnrollmentToken } from '../lib/format'
import { actions, orgConnectors, orgWorkspaces, useDB } from '../lib/store'
import { CopyChip, KeyholeIcon } from './keyhole'
import { Button, Field, Select } from './ui'

const HEARTBEAT_AFTER_MS = 6000

function useCountdown(until: number | null) {
  const [now, setNow] = useState(Date.now())
  useEffect(() => {
    if (!until) return
    const t = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(t)
  }, [until])
  if (!until) return null
  const s = Math.max(0, Math.round((until - now) / 1000))
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/**
 * The command-and-token panel, shared by Connectors → Enroll, the OpenBao
 * wizard, and the Connect tab's Connector route.
 */
export function EnrollPanel({
  workspaceId,
  onEnrolled,
  showWait = true,
  showChecks = true,
  intro = 'Run this on a machine inside your network:',
}: {
  workspaceId?: string
  onEnrolled?: (connectorId: string) => void
  showWait?: boolean
  showChecks?: boolean
  intro?: string
}) {
  const d = useDB()
  const workspaces = orgWorkspaces(d)
  const [wsId, setWsId] = useState(workspaceId ?? workspaces[0]?.id ?? '')
  const ws = workspaces.find((w) => w.id === (workspaceId ?? wsId))
  const [token, setToken] = useState<string | null>(null)
  const [expires, setExpires] = useState<number | null>(null)
  const [enrolled, setEnrolled] = useState<string | null>(null)
  const timer = useRef<number | undefined>(undefined)
  const left = useCountdown(expires)
  useEffect(() => () => window.clearTimeout(timer.current), [])

  const generate = () => {
    setToken(newEnrollmentToken())
    setExpires(Date.now() + 15 * 60_000)
    setEnrolled(null)
    window.clearTimeout(timer.current)
    // In the prototype, the connector "runs" and reports in a few seconds later.
    timer.current = window.setTimeout(() => {
      const n = orgConnectors(d).length + 1
      const id = actions.enrollConnector({ name: `edge-${String(n).padStart(2, '0')}`, workspaceId: ws?.id ?? '' })
      setEnrolled(id)
      setToken(null)
      setExpires(null)
      onEnrolled?.(id)
    }, HEARTBEAT_AFTER_MS)
  }

  const cmd = `npx keyholed@latest connect --workspace ${ws?.slug ?? 'ws_…'} --token ${token ?? '<enrollment-token>'}`
  const conn = enrolled ? d.connectors.find((c) => c.id === enrolled) : null

  return (
    <div className="flex flex-col gap-4">
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
      <div className="text-sm2 leading-relaxed text-zinc-400">{intro}</div>
      <CopyChip variant="command" value={cmd} />
      <div className="flex items-center gap-2.5">
        <Button size="sm" onClick={generate} disabled={!ws}>
          {token ? 'Generate a new token' : 'Generate enrollment token'}
        </Button>
        <span className="text-xs2 text-zinc-500">{left ? `Single-use · expires in ${left}` : 'Single-use · expires in 15 minutes'}</span>
      </div>
      {showChecks && (
        <Field label="Then check it">
          <div className="rounded-lg border border-edge bg-rail px-3.5 py-3 font-mono text-xs leading-[1.8] text-zinc-400">
            keyholed status
            <br />
            keyholed validate
          </div>
        </Field>
      )}
      {showWait &&
        (conn ? (
          <div className="flex items-center gap-3.5 rounded-[10px] border border-green-500/30 bg-green-500/[0.06] p-[18px]">
            <KeyholeIcon state="closed" turn />
            <div>
              <div className="text-[13px] font-semibold text-green-400">{conn.name} reported in</div>
              <div className="mt-0.5 font-mono text-xs text-zinc-400">
                keyholed {conn.version} · {conn.ip} · {ws?.name}
              </div>
            </div>
          </div>
        ) : (
          <div className="flex items-center gap-3.5 rounded-[10px] border border-edge bg-rail p-[18px]">
            <KeyholeIcon pulse />
            <div>
              <div className="text-[13px] font-semibold">Waiting for the first heartbeat…</div>
              <div className="mt-0.5 text-xs text-zinc-500">This turns green the moment the vault connector reports in.</div>
            </div>
          </div>
        ))}
    </div>
  )
}
