import { useCallback, useEffect, useState } from 'react'
import { ApiError } from '../api'

export type Loaded<T> =
  | { state: 'loading' }
  | { state: 'error'; retry: () => void }
  | { state: 'not-found' }
  | { state: 'ready'; data: T }

/**
 * Runs load whenever key changes. A 404 is its own state: the record isn't
 * there, or it belongs to an organization the caller isn't in.
 */
export function useLoad<T>(key: string, load: () => Promise<T>): Loaded<T> {
  const [result, setResult] = useState<{ key: string; value: Loaded<T> } | null>(null)
  const [attempt, setAttempt] = useState(0)
  const retry = useCallback(() => setAttempt((n) => n + 1), [])
  useEffect(() => {
    let live = true
    setResult(null)
    load().then(
      (data) => live && setResult({ key, value: { state: 'ready', data } }),
      (e) => {
        if (!live) return
        // The session ended (expired or signed out elsewhere): back to sign-in.
        if (e instanceof ApiError && e.status === 401) return window.location.assign('/')
        setResult({ key, value: e instanceof ApiError && e.status === 404 ? { state: 'not-found' } : { state: 'error', retry } })
      },
    )
    return () => {
      live = false
    }
  }, [key, attempt]) // eslint-disable-line react-hooks/exhaustive-deps
  return result && result.key === key ? result.value : { state: 'loading' }
}
