import { beforeEach, describe, expect, it } from 'vitest'
import { clearSignIn, consumeSignIn, markSignIn } from './signin'

beforeEach(() => {
  sessionStorage.clear()
})

describe('sign-in marker', () => {
  it('is consumed exactly once per marked sign-in', () => {
    markSignIn()
    expect(consumeSignIn()).toBe(true)
    expect(consumeSignIn()).toBe(false)
  })

  it('clearSignIn discards a pending marker without reporting anything', () => {
    markSignIn()
    clearSignIn()
    expect(consumeSignIn()).toBe(false)
  })

  // Regression: a silent-SSO attempt fails and leaves its
  // marker; the user then signs in with the local form, which reports the
  // login itself. The local completion must discard the orphan, or the next
  // reload redeems it into a second `login`.
  it('discards an orphaned silent-attempt marker when local sign-in completes', () => {
    markSignIn() // silent SSO attempt starts
    // ... the attempt fails; no session resolves on the return load ...
    clearSignIn() // the local form signed in and reported the login itself
    expect(consumeSignIn()).toBe(false) // a later reload does not double-count
  })
})
