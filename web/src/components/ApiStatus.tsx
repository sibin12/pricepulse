import { useEffect, useState } from 'react'
import { api } from '../api'

// The four states the dot in the header can show.
type State = 'checking' | 'online' | 'degraded' | 'offline'

const labels: Record<State, string> = {
  checking: 'Checking API…',
  online: 'API online',
  degraded: 'Database down',
  offline: 'API offline',
}

// How often to ask the server whether it's alive.
const POLL_MS = 20000

// ApiStatus polls GET /health so a stopped server is visible immediately,
// instead of surfacing as confusing errors on every page.
export function ApiStatus() {
  const [state, setState] = useState<State>('checking')

  useEffect(() => {
    let active = true // false after unmount, so late responses are ignored

    const check = () => {
      // Don't poll while the tab is in the background: it wastes requests and
      // the result can't be seen anyway. We re-check as soon as it's visible.
      if (document.hidden) return
      api.health().then(
        () => active && setState('online'),
        // /health returns 503 when Postgres is unreachable but the API is up.
        (err) => active && setState(err.status === 503 ? 'degraded' : 'offline'),
      )
    }

    check()
    const timer = setInterval(check, POLL_MS)
    document.addEventListener('visibilitychange', check)
    return () => {
      active = false
      clearInterval(timer)
      document.removeEventListener('visibilitychange', check)
    }
  }, [])

  return (
    <span className={`api-status api-status--${state}`} role="status">
      <span className="api-status__dot" aria-hidden />
      {labels[state]}
    </span>
  )
}
