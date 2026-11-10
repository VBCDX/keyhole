/*
 * Remembers that this browser just started a sign-in (the Zitadel redirect or
 * the silent SSO attempt), so the first load that resolves to a session can
 * report login completion exactly once. Best-effort: without
 * sessionStorage the flow still works, only the completion event is lost.
 */
const KEY = 'keyhole.signin.pending'

export function markSignIn(): void {
  try {
    sessionStorage.setItem(KEY, '1')
  } catch {
    /* storage unavailable */
  }
}

/** True once per marked sign-in: consumes the marker. */
export function consumeSignIn(): boolean {
  try {
    const value = sessionStorage.getItem(KEY)
    if (value !== null) sessionStorage.removeItem(KEY)
    return value !== null
  } catch {
    return false
  }
}

/**
 * Discards a pending marker without reporting anything. Call when a sign-in
 * completes by a path that reports itself (the local form): it clears any
 * orphaned marker left by an earlier silent-SSO attempt, so a later reload
 * cannot redeem it into a second `login`.
 */
export function clearSignIn(): void {
  try {
    sessionStorage.removeItem(KEY)
  } catch {
    /* storage unavailable */
  }
}
