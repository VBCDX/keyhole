// Time and count wording, from the mock (mocks/v1/src/lib/format.ts).
export const MIN = 60_000
export const HOUR = 60 * MIN
export const DAY = 24 * HOUR

/** Milliseconds for an API timestamp; null stays null. */
export const ms = (iso: string | null | undefined) => (iso ? Date.parse(iso) : null)

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

export function initials(name: string) {
  const parts = name.replace(/@.*/, '').split(/[\s._-]+/).filter(Boolean)
  return ((parts[0]?.[0] ?? '') + (parts[1]?.[0] ?? '')).toUpperCase() || '?'
}

export function plural(n: number, one: string, many = one + 's') {
  return `${n} ${n === 1 ? one : many}`
}
