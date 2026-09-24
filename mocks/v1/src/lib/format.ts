export const MIN = 60_000
export const HOUR = 60 * MIN
export const DAY = 24 * HOUR

const ALNUM = 'abcdefghijkmnpqrstuvwxyz23456789'
const B62 = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789'

function rand(alphabet: string, n: number) {
  let s = ''
  const buf = new Uint32Array(n)
  crypto.getRandomValues(buf)
  for (let i = 0; i < n; i++) s += alphabet[buf[i] % alphabet.length]
  return s
}

export const uid = (prefix = 'id') => `${prefix}_${rand(ALNUM, 8)}`
export const trackingCode = () => `trk_${rand(ALNUM, 8)}`
export const newToken = () => `kh_live_${rand(B62, 24)}`
export const newEnrollmentToken = () => `kh_enr_${rand(B62, 20)}`

/** The one mask style: prefix + last four. */
export const maskToken = (last4: string) => `kh_live_••••${last4}`

export function ago(t: number | null, now = Date.now()): string {
  if (t == null) return 'Never'
  const d = now - t
  if (d < 45_000) return 'Just now'
  if (d < HOUR) {
    const m = Math.max(1, Math.round(d / MIN))
    return `${m} minute${m === 1 ? '' : 's'} ago`
  }
  if (d < DAY) {
    const h = Math.round(d / HOUR)
    return `${h} h ago`
  }
  const days = Math.round(d / DAY)
  if (days < 14) return `${days} day${days === 1 ? '' : 's'} ago`
  const w = Math.round(days / 7)
  if (days < 60) return `${w} weeks ago`
  return new Date(t).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })
}

export function until(t: number | null, now = Date.now()): string {
  if (t == null) return 'No expiry'
  const d = t - now
  if (d <= 0) return 'Expired'
  const days = Math.round(d / DAY)
  if (days <= 1) return 'In 1 day'
  return `In ${days} days`
}

export const expiringSoon = (t: number | null, now = Date.now()) =>
  t != null && t - now > 0 && t - now <= 14 * DAY

export function clock(t: number) {
  return new Date(t).toLocaleTimeString('en-GB', { hour12: false })
}

export function initials(name: string) {
  const parts = name.replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean)
  return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase() || '?'
}

export function plural(n: number, one: string, many = one + 's') {
  return `${n} ${n === 1 ? one : many}`
}

export function joinNames(names: string[], empty = '—') {
  return names.length ? names.join(', ') : empty
}
