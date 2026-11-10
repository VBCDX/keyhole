import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AnalyticsParams } from './analytics'

// gtag queues its calls in dataLayer; each entry is the call's `arguments`.
function gtagCalls(): unknown[][] {
  return (window.dataLayer ?? []).map((entry) => Array.from(entry as ArrayLike<unknown>))
}

beforeEach(() => {
  vi.resetModules() // the module keeps enabled/initialized state; start fresh
  delete window.dataLayer
  delete window.gtag
  for (const s of document.head.querySelectorAll('script[src*="googletagmanager"]')) s.remove()
  window.history.replaceState({}, '', '/')
})

async function load() {
  return import('./analytics')
}

describe('page_location override (privacy invariant)', () => {
  // Real seeded shapes: the demo fixtures have suffixes as short as one
  // character (internal/store/seed/demo.json).
  const seeded = [
    { path: '/orgs/org_acme/stores/st_local', sanitized: '/orgs/:id/stores/:id' },
    { path: '/orgs/org_nw/stores/st_bao', sanitized: '/orgs/:id/stores/:id' },
    { path: '/players/users/u_jo', sanitized: '/players/users/:id' },
    { path: '/cabinets/cb_mia', sanitized: '/cabinets/:id' },
  ]

  it('never emits a raw tenant/store id, including GA4 auto-collected fields', async () => {
    const { initAnalytics, trackEvent, trackPageView } = await load()
    window.history.replaceState({}, '', seeded[0].path)
    expect(initAnalytics('G-TEST1234')).toBe(true)
    // The config call carries the sanitized defaults too, so a hit GA4 emits
    // on its own starts from an id-free URL.
    const config = gtagCalls().find(([kind]) => kind === 'config')
    expect(config?.[2]).toMatchObject({
      page_location: `${window.location.origin}${seeded[0].sanitized}`,
      page_referrer: '',
    })

    for (const { path, sanitized } of seeded) {
      window.history.replaceState({}, '', path)
      window.dataLayer = []
      trackEvent('login', { method: 'local' })
      trackPageView(window.location.pathname, document.title)

      const calls = gtagCalls()
      for (const call of calls.filter(([kind]) => kind === 'event')) {
        expect(call[2]).toMatchObject({
          page_location: `${window.location.origin}${sanitized}`,
          page_referrer: '',
        })
      }
      // No dataLayer entry may contain any raw id segment of this URL — the
      // short seeded ids (org_nw, st_bao, u_jo, cb_mia) included.
      const serialized = JSON.stringify(calls)
      for (const raw of path.split('/').filter((segment) => segment.includes('_'))) {
        expect(serialized).not.toContain(raw)
      }
    }
  })

  it('a caller cannot smuggle a raw page_location or page_referrer (not caller parameters)', async () => {
    const { initAnalytics, trackEvent } = await load()
    initAnalytics('G-TEST1234')
    const smuggled = {
      method: 'local',
      page_location: 'https://keyhole.vbcdx.com/orgs/org_acme',
      page_referrer: 'https://idp.example/authorize?code_challenge=x',
    } as unknown as AnalyticsParams
    trackEvent('login', smuggled)
    const [call] = gtagCalls().filter(([kind]) => kind === 'event')
    expect(call[2]).toMatchObject({ page_location: `${window.location.origin}/`, page_referrer: '' })
    expect(JSON.stringify(call)).not.toContain('org_acme')
    expect(JSON.stringify(call)).not.toContain('code_challenge')
  })
})

describe('normalizePath', () => {
  it('redacts opaque ids — prefixed schema ids, uuids, long hex and numbers', async () => {
    const { normalizePath } = await load()
    expect(normalizePath('/orgs/org_acme/stores/st_local')).toBe('/orgs/:id/stores/:id')
    // Short seeded suffixes must redact too (demo.json: org_nw, st_bao).
    expect(normalizePath('/orgs/org_nw/stores/st_bao')).toBe('/orgs/:id/stores/:id')
    expect(normalizePath('/players/users/u_jo')).toBe('/players/users/:id')
    expect(normalizePath('/cabinets/cb_mia')).toBe('/cabinets/:id')
    expect(normalizePath('/orgs/org_3fK9aBcDeFgHiJkL')).toBe('/orgs/:id')
    expect(normalizePath('/orgs/3f2a9c1d-0000-1111-2222-333344445555/stores/12')).toBe('/orgs/:id/stores/:id')
    expect(normalizePath('/stores/12?tab=keys#top')).toBe('/stores/:id')
  })

  it('redacts every id the schema mints: multiple underscores, long prefixes, "@"', async () => {
    const { normalizePath } = await load()
    // internal/store/seed/demo.json: om_acme_dana, wm_prod_docs, gr_prod_github,
    // inv_nw_old; plus ctest_bao_1, t_github@1 and a prefix longer than six.
    for (const id of ['om_acme_dana', 'wm_prod_docs', 'gr_prod_github', 'inv_nw_old', 'ctest_bao_1', 't_github@1', 'workspace_availability']) {
      expect(normalizePath(`/x/${id}`)).toBe('/x/:id')
      expect(normalizePath(`/x/${encodeURIComponent(id)}/y`)).toBe('/x/:id/y')
    }
  })

  it('never lets a multi-underscore id reach the dataLayer', async () => {
    const { initAnalytics, trackEvent, trackPageView } = await load()
    window.history.replaceState({}, '', '/orgs/org_acme/members/om_acme_dana')
    expect(initAnalytics('G-TEST1234')).toBe(true)
    trackEvent('login', { method: 'local' })
    trackPageView(window.location.pathname, document.title)
    const serialized = JSON.stringify(gtagCalls())
    expect(serialized).not.toContain('om_acme_dana')
    expect(serialized).not.toContain('org_acme')
    expect(serialized).toContain('/orgs/:id/members/:id')
  })

  it('leaves ordinary route segments alone', async () => {
    const { normalizePath } = await load()
    expect(normalizePath('/settings/me')).toBe('/settings/me')
    expect(normalizePath('/players/agents')).toBe('/players/agents')
    expect(normalizePath('')).toBe('/')
  })
})

describe('analytics off by default', () => {
  it('does not touch the page for a missing or malformed id', async () => {
    const { initAnalytics, trackEvent } = await load()
    expect(initAnalytics('')).toBe(false)
    expect(initAnalytics('UA-123')).toBe(false)
    trackEvent('login', { method: 'local' })
    expect(window.dataLayer).toBeUndefined()
    expect(document.head.querySelector('script[src*="googletagmanager"]')).toBeNull()
  })
})

describe('parameter allowlist', () => {
  it('sends only documented keys with primitive values', async () => {
    const { initAnalytics, trackEvent } = await load()
    expect(initAnalytics('G-TEST1234')).toBe(true)
    const smuggled: Record<string, unknown> = {
      method: 'local',
      email: 'person@example.com',
      secret: 'x',
      page_path: '/',
      intent: false,
      page_title: undefined,
      nested: { value: 1 },
    }
    trackEvent('login', smuggled as AnalyticsParams)
    const events = gtagCalls().filter((call) => call[0] === 'event')
    expect(events).toHaveLength(1)
    expect(events[0]).toEqual([
      'event',
      'login',
      { method: 'local', page_path: '/', page_location: `${window.location.origin}/`, page_referrer: '' },
    ])
  })

  it('normalizes a caller-supplied page_path so trackEvent cannot bypass redaction', async () => {
    const { initAnalytics, trackEvent } = await load()
    initAnalytics('G-TEST1234')
    // A future caller may emit a page_view through trackEvent instead of
    // trackPageView; the id-free invariant must still hold for them.
    trackEvent('page_view', { page_path: '/orgs/org_acme/stores/st_local' })
    const [call] = gtagCalls().filter(([kind]) => kind === 'event')
    expect(call[2]).toMatchObject({ page_path: '/orgs/:id/stores/:id' })
    expect(JSON.stringify(call)).not.toContain('org_acme')
    expect(JSON.stringify(call)).not.toContain('st_local')
  })

  it('drops malformed event names', async () => {
    const { initAnalytics, trackEvent } = await load()
    initAnalytics('G-TEST1234')
    trackEvent('Login', { method: 'local' })
    trackEvent('a', { method: 'local' })
    expect(gtagCalls().filter((call) => call[0] === 'event')).toHaveLength(0)
  })

  it('reports already-on on a repeat init, and no longer exports analyticsEnabled', async () => {
    const mod = await load()
    expect(mod.initAnalytics('G-TEST1234')).toBe(true)
    expect(mod.initAnalytics('G-OTHER123')).toBe(true)
    expect('analyticsEnabled' in mod).toBe(false)
  })
})
