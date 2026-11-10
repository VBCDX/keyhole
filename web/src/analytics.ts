/*
 * Minimal GA4 instrumentation for Keyhole.
 *
 * Fail-safe: the tag only loads when the server config serves a measurement id
 * (GA_MEASUREMENT_ID, see docs/analytics.md). With none configured nothing
 * happens — no script, no dataLayer, no cookies, no network calls; trackEvent
 * is a no-op. The CSP on the server side is extended only in the same case.
 *
 * Only the documented event names and parameter keys are ever sent (an
 * allowlist, enforced here): no emails, names, account ids or page content.
 * Page paths are normalized so opaque ids become ":id", and every hit carries
 * an explicitly sanitized `page_location` and `page_referrer`, because GA4
 * auto-collects the raw URL/referrer otherwise (a query string can carry an
 * OIDC code/state or an invite token).
 */

declare global {
  interface Window {
    dataLayer?: unknown[]
    gtag?: (...args: unknown[]) => void
  }
}

// GA4 measurement ids look like G-XXXXXXXXXX (letters/digits after "G-").
const MEASUREMENT_ID_RE = /^G-[A-Z0-9]{4,20}$/

// The complete set of parameter keys instrumentation is allowed to send.
// Anything else is dropped before an event leaves the page. See
// docs/analytics.md — the same contract is auditable from the event names.
// (`page_location`/`page_referrer` are not caller parameters: trackEvent fills
// them with sanitized values, see safePageLocation/safePageReferrer.)
const ALLOWED_PARAM_KEYS = new Set(['method', 'page_path', 'page_title'])

// A path segment that is an opaque identifier. Two rules, either redacts:
//  1. Any segment containing an underscore. The schema mints prefixed ids with
//     one or more underscores and prefixes of any length ("org_acme",
//     "om_acme_dana", "wm_prod_docs", "gr_prod_github", "inv_nw_old",
//     "ctest_bao_1", "t_github@1"), and ordinary route segments in this app
//     never contain an underscore, so redacting on the underscore is
//     fail-closed without depending on the id alphabet.
//  2. Bare uuid, long hex or numeric segments (ID_SEGMENT_RE).
const ID_SEGMENT_RE = /^(?:[0-9a-f]{8,}(?:-[0-9a-f]{4,})+|[0-9a-f]{16,}|\d+)$/i

function isOpaqueSegment(decoded: string): boolean {
  return decoded.includes('_') || ID_SEGMENT_RE.test(decoded)
}

export type AnalyticsParams = Record<string, string | number | boolean | undefined>

let enabled = false
let initialized = false

/**
 * Loads the GA4 tag when `id` is a well-formed measurement id. Returns
 * whether analytics is on after the call: true when a valid id initialized
 * the tag (a repeat call returns true as well), false when analytics is off
 * (missing or malformed id). Calling it again after a success is a no-op.
 */
export function initAnalytics(id: string | null | undefined): boolean {
  if (initialized) return enabled
  const measurementId = typeof id === 'string' ? id.trim() : ''
  if (!MEASUREMENT_ID_RE.test(measurementId)) return false

  initialized = true
  enabled = true

  window.dataLayer = window.dataLayer || []
  // The standard gtag shim: every call is queued until gtag.js loads.
  window.gtag = function gtag() {
    window.dataLayer!.push(arguments as unknown as unknown[])
  }
  window.gtag('js', new Date())
  // send_page_view is off because this is a single-page app: the page_view
  // event is sent explicitly so it fires exactly once per app load.
  // Signals and ad personalization stay off: aggregate product analytics only.
  // The sanitized location/referrer are set here too: any hit that did not go
  // through trackEvent still cannot carry the raw URL or referrer.
  window.gtag('config', measurementId, {
    send_page_view: false,
    allow_google_signals: false,
    allow_ad_personalization_signals: false,
    page_location: safePageLocation(),
    page_referrer: safePageReferrer(),
  })

  const script = document.createElement('script')
  script.async = true
  script.src = `https://www.googletagmanager.com/gtag/js?id=${encodeURIComponent(measurementId)}`
  document.head.appendChild(script)
  return true
}

/**
 * The current location, sanitized: origin + normalized path, no query and no
 * hash. GA4 otherwise auto-collects the full URL on every hit, which can carry
 * an OIDC code/state or an invite token.
 */
function safePageLocation(): string {
  const { origin, pathname } = window.location
  return origin + normalizePath(pathname)
}

/**
 * The referrer, sanitized to its origin only (and to "" when there is none).
 * GA4 otherwise auto-collects the full referrer URL, which can be an
 * identity-provider URL carrying state/code_challenge.
 */
function safePageReferrer(): string {
  const referrer = document.referrer
  if (!referrer) return ''
  try {
    return new URL(referrer).origin
  } catch {
    return ''
  }
}

/**
 * Sends one event. No-op before initAnalytics or with an invalid event name.
 * Parameters are filtered to the allowlist and to primitive values; a
 * caller-supplied `page_path` is normalized (opaque ids become ":id"), and the
 * GA4 auto-collected location/referrer are always overridden with sanitized
 * values, so nothing beyond the documented metadata leaves the page.
 */
export function trackEvent(name: string, params: AnalyticsParams = {}): void {
  if (!enabled || !/^[a-z][a-z0-9_]{1,39}$/.test(name)) return
  const safe: Record<string, string | number | boolean> = {}
  for (const [key, value] of Object.entries(params)) {
    if (!ALLOWED_PARAM_KEYS.has(key) || value == null) continue
    const type = typeof value
    if (type === 'string' || type === 'number' || type === 'boolean') {
      // Enforce the id-free path invariant on every entry point, not only
      // trackPageView: a caller-supplied page_path is normalized here so a
      // future trackEvent caller cannot bypass the redaction.
      safe[key] = key === 'page_path' && type === 'string' ? normalizePath(value as string) : value
    }
  }
  window.gtag!('event', name, {
    ...safe,
    page_location: safePageLocation(),
    page_referrer: safePageReferrer(),
  })
}

/** One page_view. `pathname` is normalized so opaque ids become ":id". */
export function trackPageView(pathname: string, title?: string): void {
  trackEvent('page_view', {
    page_path: normalizePath(pathname),
    page_title: typeof title === 'string' ? title : undefined,
  })
}

/** "/orgs/3f2a.../stores/12" -> "/orgs/:id/stores/:id"; query/hash dropped. */
export function normalizePath(pathname: string): string {
  const path = String(pathname || '/').split('?')[0].split('#')[0] || '/'
  return path
    .split('/')
    .map((segment) => {
      let decoded = segment
      try {
        decoded = decodeURIComponent(segment)
      } catch {
        /* malformed escape: test the raw segment */
      }
      return isOpaqueSegment(decoded) ? ':id' : segment
    })
    .join('/')
}
