import { Link } from 'react-router-dom'

// NotFound is shown for unknown routes and for product IDs the API doesn't know.
export function NotFound({ what = 'page' }: { what?: string }) {
  return (
    <div className="empty">
      <h1>404</h1>
      <p className="muted">That {what} doesn't exist. It may have been deleted.</p>
      <Link to="/" className="btn btn--primary">
        Go to my bucket list
      </Link>
    </div>
  )
}
