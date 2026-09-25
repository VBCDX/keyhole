import { useEffect, useState } from 'react'
import type { SidecarProtocol } from './types'

/** Shared by the vault connector and sidecar enroll panels. */
export const HEARTBEAT_AFTER_MS = 6000

export function useCountdown(until: number | null) {
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

/** Enrollment tokens are single-use and expire after 15 minutes. */
export const enrollmentExpiry = () => Date.now() + 15 * 60_000

/** What the app next to the sidecar points at. No Keyhole token here: the sidecar holds its own credential. */
export function sidecarAppConfig(listen: string, protocols: SidecarProtocol[]) {
  const base = `http://${listen}`
  const lines = ['# App side — no secrets and no Keyhole token here', `HTTP_PROXY=${base}`, `KEYHOLE_BASE_URL=${base}`]
  if (protocols.includes('https')) lines.push(`# HTTPS: https://${listen} (trusts the sidecar's local CA)`)
  if (protocols.includes('sse')) lines.push(`# SSE streams: ${base}/sse`)
  const env = lines.join('\n') + '\n'
  const mcp = protocols.includes('mcp') ? JSON.stringify({ mcpServers: { 'keyhole-sidecar': { url: `${base}/mcp` } } }, null, 2) + '\n' : null
  return { env, mcp }
}
