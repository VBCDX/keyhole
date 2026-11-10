// Thin client for the Keyhole JSON API. Requests are same-origin and carry
// the session cookie; state-changing calls send the session's CSRF token.

/** Another app of the VBCDX suite, from SUITE_APPS. `current` is this one. */
export type SuiteApp = { name: string; url: string; current: boolean }

export type AuthConfig = {
  local: boolean
  zitadel: boolean
  /** Try /auth/oidc/login?prompt=none once before showing the sign-in page. */
  silent_sign_in: boolean
  suite_apps: SuiteApp[]
  /** GA4 measurement id; empty means analytics is off. Public config. */
  ga_measurement_id: string
}

export type Me = {
  user: { id: string; email: string; name: string }
  method: 'local' | 'zitadel'
  session_started_at: string
  session_expires_at: string
  csrf_token: string
}

// The caller's membership of an organization.
export type Org = {
  id: string
  name: string
  role: 'Owner' | 'userAdmin' | 'user'
  status: 'active' | 'invited' | 'suspended'
  joined_at: string | null
}

export type StoreHealthStatus = 'healthy' | 'degraded' | 'sealed' | 'denied' | 'unreachable' | 'unknown'

export type Store = {
  id: string
  name: string
  provider: 'local' | 'openbao'
  status: string
  route_mode: 'direct' | 'connector' | 'internal'
  route_connector_id?: string
  /** Null when the store doesn't route through a connector, or its connector is gone. */
  route_connector: { id: string; name: string; status: 'active' | 'revoked'; health_status: 'healthy' | 'degraded' | 'offline' } | null
  network_exposure: string
  health: { status: StoreHealthStatus; code: string; message: string; checked_at: string }
  skip_tls_verify: boolean
  key_count: number
  created_at: string
  updated_at: string
  removed_at: string | null
}

export type Key = {
  id: string
  name: string
  status: 'active' | 'source_removed'
  cabinet: { id: string; name: string } | null
  pinned: boolean
  used_by: number
  created_at: string
  rotated_at: string | null
  expires_at: string | null
}

export type ConnectionTest = {
  id: string
  trigger: string
  started_at: string
  finished_at: string | null
  ok: boolean
  health_code: string
}

export type StoreDetail = Store & {
  address: string
  namespace: string
  tls_server_name: string
  ca_bundle_name: string
  kv_mount: string
  kv_path_prefix: string
  auth_method: string
  cache_default_ttl_seconds: number
  keys: Key[]
  connection_tests: ConnectionTest[]
}

export class ApiError extends Error {
  status: number
  code: string
  retryAfter: number | null

  constructor(status: number, code: string, message: string, retryAfter: number | null) {
    super(message)
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

async function call<T>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await fetch(path, { credentials: 'same-origin', ...init })
  if (!res.ok) {
    const body = await res.json().catch(() => ({}))
    const retry = res.headers.get('Retry-After')
    throw new ApiError(res.status, body.error ?? 'error', body.message ?? res.statusText, retry ? Number(retry) : null)
  }
  return res.json() as Promise<T>
}

export const api = {
  authConfig: () => call<AuthConfig>('/api/auth/config'),

  me: async (): Promise<Me | null> => {
    try {
      return await call<Me>('/api/me')
    } catch (e) {
      if (e instanceof ApiError && e.status === 401) return null
      throw e
    }
  },

  login: (email: string, password: string) =>
    call<Me>('/api/auth/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    }),

  logout: (csrfToken: string) =>
    call<{ redirect_url: string }>('/api/auth/logout', {
      method: 'POST',
      headers: { 'X-CSRF-Token': csrfToken },
    }),

  orgs: () => call<{ orgs: Org[] }>('/api/v1/orgs').then((r) => r.orgs),

  stores: (orgId: string) => call<{ stores: Store[]; source_removed_keys: Key[] }>(`/api/v1/orgs/${encodeURIComponent(orgId)}/stores`),

  store: (orgId: string, storeId: string) => call<StoreDetail>(`/api/v1/orgs/${encodeURIComponent(orgId)}/stores/${encodeURIComponent(storeId)}`),
}
