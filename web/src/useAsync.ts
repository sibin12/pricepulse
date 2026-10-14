import { useCallback, useEffect, useState } from 'react'

interface AsyncState<T> {
  data?: T
  error?: Error
  loading: boolean
}

// useAsync runs `fn` when `deps` change and tracks loading/error/data.
// `reload()` re-runs it, e.g. after a mutation. Results from stale runs
// (the component unmounted or deps changed) are ignored.
export function useAsync<T>(fn: () => Promise<T>, deps: unknown[]) {
  const [state, setState] = useState<AsyncState<T>>({ loading: true })
  const [tick, setTick] = useState(0)

  useEffect(() => {
    let cancelled = false
    setState((s) => ({ ...s, loading: true }))
    fn().then(
      (data) => !cancelled && setState({ data, loading: false }),
      (error: Error) => !cancelled && setState({ error, loading: false }),
    )
    return () => {
      cancelled = true
    }
    // `fn` is intentionally not a dependency: callers pass a new arrow function
    // every render, so we re-run only when the declared deps (or tick) change.
  }, [...deps, tick])

  const reload = useCallback(() => setTick((t) => t + 1), [])
  return { ...state, reload }
}
