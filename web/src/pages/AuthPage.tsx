import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, googleLoginURL } from '../api'
import { useAuth } from '../auth'
import { Field } from '../components/Field'

// Messages for the ?error= codes the API uses when redirecting back from Google.
const googleErrors: Record<string, string> = {
  google_cancelled: 'Google sign-in was cancelled.',
  google_expired: 'The Google sign-in took too long or cookies are blocked. Please try again.',
  google_state: 'That sign-in link was invalid or already used. Please try again.',
  google_failed: "Couldn't sign in with Google. Please try again.",
  google_unavailable: 'Google sign-in is not available right now.',
  google_email_exists:
    'An account with this Google email already exists. Log in with your email and password instead.',
}

// AuthPage is both the login and the sign-up page; `mode` picks which.
export function AuthPage({ mode }: { mode: 'login' | 'register' }) {
  const { login, register } = useAuth()
  const navigate = useNavigate()
  // ?next=/history/7 is where to go after signing in; ?error=... comes from
  // the API when a Google sign-in fails (see googleErrors above).
  const [params] = useSearchParams()
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState(googleErrors[params.get('error') ?? ''] ?? '')
  const [busy, setBusy] = useState(false)
  // null while we ask the server whether Google sign-in is configured.
  const [google, setGoogle] = useState<boolean | null>(null)
  const [emailOpen, setEmailOpen] = useState(false)

  // Ask the server once whether Google sign-in is configured.
  useEffect(() => {
    api.authConfig().then((c) => setGoogle(c.google), () => setGoogle(false))
  }, [])

  const isLogin = mode === 'login'
  // Google is the primary option; the email form is tucked behind a button.
  // If Google isn't available, email is the only way in, so show it directly.
  const showEmailForm = emailOpen || google === false

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    if (!isLogin && password.length < 8) return setError('Password must be at least 8 characters.')
    setBusy(true)
    try {
      await (isLogin ? login : register)(email.trim(), password)
      // replace: true keeps the login page out of the back-button history.
      navigate(params.get('next') ?? '/', { replace: true })
    } catch (err) {
      setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <div className="auth">
      <div className="card auth__card">
        <h1>{isLogin ? 'Welcome back' : 'Create your account'}</h1>
        <p className="muted">
          {isLogin ? 'Log in to see your tracked products.' : 'Track prices and get an email when they drop.'}
        </p>

        {/* Shown up here (not inside the form) so Google errors are visible
            even while the email form is collapsed. */}
        {error && <div className="callout callout--error">{error}</div>}

        {google ? (
          // A plain link (full page navigation), because sign-in happens on Google's site.
          <a className="btn btn--google btn--large" href={googleLoginURL(params.get('next'))}>
            <GoogleLogo />
            Continue with Google
          </a>
        ) : (
          <>
            <button className="btn btn--google btn--large" disabled>
              <GoogleLogo />
              Continue with Google
            </button>
            {google === false && (
              <p className="field__hint auth__note">
                Google sign-in isn't set up on this server yet. Use email below for now.
              </p>
            )}
          </>
        )}

        <div className="auth__divider">
          <span>or</span>
        </div>

        {!showEmailForm ? (
          <button className="btn btn--large" onClick={() => setEmailOpen(true)}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden>
              <rect x="3" y="5" width="18" height="14" rx="2" />
              <path d="m3 7 9 6 9-6" />
            </svg>
            Continue with email
          </button>
        ) : (
          <form className="form" onSubmit={onSubmit} noValidate>
            <Field label="Email" htmlFor="email">
              <input
                id="email"
                className="input"
                type="email"
                autoComplete="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                autoFocus
                required
              />
            </Field>
            <Field label="Password" htmlFor="password" hint={isLogin ? undefined : 'At least 8 characters.'}>
              <input
                id="password"
                className="input"
                type="password"
                // Lets password managers save/fill correctly.
                autoComplete={isLogin ? 'current-password' : 'new-password'}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </Field>
            <button className="btn btn--primary" type="submit" disabled={busy}>
              {busy ? 'Please wait…' : isLogin ? 'Log in with email' : 'Create account with email'}
            </button>
          </form>
        )}

        <p className="muted auth__switch">
          Just checking a price? <Link to="/history">See price history without an account</Link>
        </p>
        <p className="muted auth__switch">
          {isLogin ? (
            <>
              New to PricePulse? <Link to="/register">Create an account</Link>
            </>
          ) : (
            <>
              Already have an account? <Link to="/login">Log in</Link>
            </>
          )}
        </p>
      </div>
    </div>
  )
}

// GoogleLogo is Google's multicolor "G", as their branding guidelines require
// on sign-in buttons.
function GoogleLogo() {
  return (
    <svg width="18" height="18" viewBox="0 0 48 48" aria-hidden>
      <path fill="#EA4335" d="M24 9.5c3.5 0 6.6 1.2 9.1 3.6l6.8-6.8C35.8 2.4 30.3 0 24 0 14.6 0 6.6 5.4 2.7 13.3l7.9 6.1C12.5 13.6 17.8 9.5 24 9.5z" />
      <path fill="#4285F4" d="M46.1 24.5c0-1.6-.1-3.1-.4-4.5H24v9h12.4c-.5 2.9-2.2 5.3-4.6 6.9l7.4 5.7c4.3-4 6.9-9.9 6.9-17.1z" />
      <path fill="#FBBC05" d="M10.6 28.6c-.5-1.4-.8-3-.8-4.6s.3-3.2.8-4.6l-7.9-6.1C1 16.6 0 20.2 0 24s1 7.4 2.7 10.7l7.9-6.1z" />
      <path fill="#34A853" d="M24 48c6.5 0 11.9-2.1 15.9-5.8l-7.4-5.7c-2.1 1.4-4.8 2.3-8.5 2.3-6.2 0-11.5-4.1-13.4-9.9l-7.9 6.1C6.6 42.6 14.6 48 24 48z" />
    </svg>
  )
}
