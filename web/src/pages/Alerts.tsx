import { useState } from 'react'
import { api } from '../api'
import { useAsync } from '../useAsync'

// Alerts shows whether email alerts are configured, lets the user send a test
// email, and explains how to configure SMTP. Settings live in server env vars
// (not in the UI) so the SMTP password never passes through the browser.
export function Alerts() {
  const { data: status, error, loading } = useAsync(api.alertStatus, [])
  const [sending, setSending] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null)

  // sendTest asks the API to email the logged-in user a sample alert, which
  // proves the server's SMTP settings work end to end.
  async function sendTest() {
    setSending(true)
    setResult(null)
    try {
      const res = await api.sendTestAlert()
      setResult({ ok: true, text: `Test email sent to ${res.sent_to.join(', ')}. Check your inbox (and spam folder).` })
    } catch (err) {
      setResult({ ok: false, text: (err as Error).message })
    } finally {
      setSending(false)
    }
  }

  if (loading) return <p className="muted">Loading…</p>
  if (error || !status) return <div className="callout callout--error">{error?.message}</div>

  return (
    <div className="narrow">
      <h1>Email alerts</h1>
      <p className="muted">
        PricePulse emails your account address once when a product drops to your target price. If the price rises
        above the target and drops again, you get another email.
      </p>

      <section className="card form">
        <div className="alert-status">
          <span className={`badge ${status.enabled ? 'badge--hit' : 'badge--unchecked'}`}>
            {status.enabled ? 'Enabled' : 'Not configured'}
          </span>
          {status.enabled && <span>Sending to {status.recipients.join(', ')}</span>}
        </div>
        {status.enabled && (
          <div>
            <button className="btn btn--primary" onClick={sendTest} disabled={sending}>
              {sending ? 'Sending…' : 'Send test email'}
            </button>
          </div>
        )}
        {result && <div className={`callout ${result.ok ? 'callout--ok' : 'callout--error'}`}>{result.text}</div>}
      </section>

      <section className="card">
        <h2>{status.enabled ? 'Server mail settings' : 'How to turn alerts on'}</h2>
        <p className="muted">
          Email is sent through one mail account configured on the server (this is the app's sender, not your inbox).
          Put these in the <code>.env</code> file in the project folder and restart the API. For Gmail, create an{' '}
          <a href="https://myaccount.google.com/apppasswords" target="_blank" rel="noopener noreferrer">
            app password
          </a>{' '}
          and use it as <code>SMTP_PASSWORD</code>. Your normal password won't work.
        </p>
        <pre className="code">{`SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_USERNAME=you@gmail.com
SMTP_PASSWORD=your-app-password
# optional
SMTP_FROM="PricePulse <you@gmail.com>"
APP_URL=http://localhost:3000`}</pre>
      </section>
    </div>
  )
}
