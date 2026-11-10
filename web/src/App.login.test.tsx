import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { AuthConfig, Me } from './api'

vi.mock('./api', async (importOriginal) => {
  const mod = await importOriginal<typeof import('./api')>()
  return {
    ...mod,
    api: { authConfig: vi.fn(), me: vi.fn(), login: vi.fn(), orgs: vi.fn() },
  }
})

import { api } from './api'
import { App } from './App'

const config: AuthConfig = {
  local: true,
  zitadel: true,
  silent_sign_in: true,
  suite_apps: [],
  ga_measurement_id: 'G-TEST1234',
}

const me: Me = {
  user: { id: 'u_1', email: 'person@example.com', name: 'Person' },
  method: 'local',
  session_started_at: '2026-10-09T00:00:00Z',
  session_expires_at: '2026-10-10T00:00:00Z',
  csrf_token: 'csrf',
}

function loginEvents(): unknown[][] {
  return (window.dataLayer ?? [])
    .map((entry) => Array.from(entry as ArrayLike<unknown>))
    .filter((call) => call[0] === 'event' && call[1] === 'login')
}

beforeEach(() => {
  sessionStorage.clear()
  delete window.dataLayer
  delete window.gtag
  vi.clearAllMocks()
  vi.mocked(api.authConfig).mockResolvedValue(config)
  vi.mocked(api.me).mockResolvedValue(null)
  vi.mocked(api.orgs).mockResolvedValue([])
  vi.mocked(api.login).mockResolvedValue(me)
})

// Regression: a silent SSO attempt fails and leaves its
// pending marker; the browser then signs in with the local form, which
// reports the login itself. A later reload must not count that login again.
describe('sign-in accounting', () => {
  it('does not double-count after an orphaned silent marker and a local sign-in', async () => {
    // The state a failed silent SSO attempt leaves: a pending marker plus the
    // once-a-minute guard, and no session.
    sessionStorage.setItem('keyhole.signin.pending', '1')
    sessionStorage.setItem('keyhole.silent_sign_in_at', String(Date.now()))

    // The return load: no session, the guard stops another attempt, the form shows.
    render(<App />)
    const email = await screen.findByLabelText('Email')
    fireEvent.change(email, { target: { value: 'person@example.com' } })
    fireEvent.change(screen.getByLabelText('Password'), { target: { value: 'a long password' } })
    fireEvent.submit(email.closest('form')!)

    await waitFor(() => expect(api.login).toHaveBeenCalledTimes(1))
    // The local completion must have discarded the orphaned marker.
    await waitFor(() => expect(sessionStorage.getItem('keyhole.signin.pending')).toBeNull())
    expect(loginEvents()).toHaveLength(1) // the local sign-in, reported once
    cleanup()

    // A later reload with the session live counts nothing further.
    vi.mocked(api.me).mockResolvedValue(me)
    render(<App />)
    await waitFor(() => expect(api.orgs).toHaveBeenCalledTimes(2))
    expect(loginEvents()).toHaveLength(1)
  })
})
