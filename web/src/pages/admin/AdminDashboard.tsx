import { useMemo, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../../api'
import { formatInterval, relativeTime } from '../../format'
import type { AdminUser, AppSettings, Limits } from '../../types'
import { useAsync } from '../../useAsync'
import { IntervalSelect, Toggle } from './controls'

// AdminDashboard: platform stats, the global settings that apply to every
// user without a personal override, and the list of users.
export function AdminDashboard() {
  // Three independent requests, started in parallel; each section renders as
  // soon as its own data arrives.
  const stats = useAsync(api.adminStats, [])
  const users = useAsync(api.adminUsers, [])
  const settings = useAsync(api.adminSettings, [])

  return (
    <>
      <div className="page-head">
        <div>
          <h1>Admin</h1>
          <p className="muted">Manage users, limits and how often prices are checked.</p>
        </div>
      </div>

      {stats.data && (
        <section className="stats stats--admin">
          <Stat label="Users" value={stats.data.users} hint={`${stats.data.admins} admin${stats.data.admins === 1 ? '' : 's'}`} />
          <Stat label="Products tracked" value={stats.data.products} hint={`${stats.data.listings} unique`} />
          <Stat label="Price checks (24h)" value={stats.data.checks_24h} />
          <Stat label="Alerts sent (7 days)" value={stats.data.alerts_7d} />
          <Stat label="Failing products" value={stats.data.failing_products} tone={stats.data.failing_products ? 'warn' : undefined} />
          <Stat label="Suspended users" value={stats.data.disabled_users} tone={stats.data.disabled_users ? 'warn' : undefined} />
        </section>
      )}

      {/* Saving the defaults changes every user's effective limits, so the
          user table is reloaded afterwards. */}
      {settings.data && <GlobalSettings initial={settings.data} onSaved={users.reload} />}
      {settings.error && <div className="callout callout--error">{settings.error.message}</div>}

      <UsersTable users={users.data} error={users.error} />
    </>
  )
}

// Stat is one number tile; tone="warn" colours it orange (e.g. failing products).
function Stat({ label, value, hint, tone }: { label: string; value: number; hint?: string; tone?: 'warn' }) {
  return (
    <div className={`stat ${tone ? `stat--${tone}` : ''}`}>
      <span className="stat__value">{value}</span>
      <span className="stat__label">
        {label}
        {hint && <span className="muted"> · {hint}</span>}
      </span>
    </div>
  )
}

// GlobalSettings edits the defaults for everyone. Users with a personal
// override keep it; everyone else picks up the change immediately.
function GlobalSettings({ initial, onSaved }: { initial: AppSettings; onSaved: () => void }) {
  const [d, setD] = useState<Limits>(initial.defaults)
  const [signupsOpen, setSignupsOpen] = useState(initial.signups_open)
  const [publicLookups, setPublicLookups] = useState(initial.public_lookups)
  const [publicInterval, setPublicInterval] = useState(initial.public_check_interval_minutes)
  const [saved, setSaved] = useState(initial) // last values the server confirmed
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  // The Save button is only enabled when something differs from the saved
  // values. JSON.stringify is a simple deep comparison for these small objects.
  const dirty =
    JSON.stringify({ d, signupsOpen, publicLookups, publicInterval }) !==
    JSON.stringify({
      d: saved.defaults,
      signupsOpen: saved.signups_open,
      publicLookups: saved.public_lookups,
      publicInterval: saved.public_check_interval_minutes,
    })
  // set('max_products', 30) updates one field of the defaults object.
  const set = <K extends keyof Limits>(k: K, v: Limits[K]) => setD((prev) => ({ ...prev, [k]: v }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setMsg(null)
    try {
      const s = await api.adminUpdateSettings({
        defaults: d,
        signups_open: signupsOpen,
        public_lookups: publicLookups,
        public_check_interval_minutes: publicInterval,
      })
      setSaved(s)
      setMsg({ ok: true, text: 'Saved. Changes apply right away.' })
      onSaved() // effective limits in the user table changed
    } catch (err) {
      setMsg({ ok: false, text: (err as Error).message })
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="card form" onSubmit={onSubmit}>
      <div>
        <h2>Default settings for all users</h2>
        <p className="muted">Applies to every user unless you set something different on their own page.</p>
      </div>
      <div className="settings-grid">
        <div className="field">
          <label htmlFor="g-max">Max products per user</label>
          <input
            id="g-max"
            className="input"
            type="number"
            min={0}
            max={10000}
            value={d.max_products}
            onChange={(e) => set('max_products', Number(e.target.value))}
          />
        </div>
        <div className="field">
          <label htmlFor="g-interval">Re-check prices</label>
          <IntervalSelect id="g-interval" value={d.check_interval_minutes} onChange={(v) => set('check_interval_minutes', v)} />
        </div>
      </div>
      <div className="toggles">
        <Toggle id="g-alerts" checked={d.alerts_enabled} onChange={(v) => set('alerts_enabled', v)} label="Email alerts" />
        <Toggle
          id="g-manual"
          checked={d.manual_checks_enabled}
          onChange={(v) => set('manual_checks_enabled', v)}
          label={'"Check price now" button'}
        />
        <Toggle id="g-signups" checked={signupsOpen} onChange={setSignupsOpen} label="Allow new sign-ups" />
      </div>

      <div className="form__section">
        <h2>Public price history</h2>
        <p className="muted">
          Anyone can paste a product link at /history and see its price history without an account. Products that are
          only looked up (not tracked) keep being checked for 30 days after someone last viewed them.
        </p>
      </div>
      <div className="settings-grid">
        <div className="field">
          <span className="field__label">Public lookups</span>
          <Toggle id="g-public" checked={publicLookups} onChange={setPublicLookups} label="Allow without logging in" />
        </div>
        <div className="field">
          <label htmlFor="g-public-interval">Re-check looked-up products</label>
          <IntervalSelect id="g-public-interval" value={publicInterval} onChange={setPublicInterval} />
        </div>
      </div>
      {msg && <div className={`callout ${msg.ok ? 'callout--ok' : 'callout--error'}`}>{msg.text}</div>}
      <div>
        <button className="btn btn--primary" type="submit" disabled={!dirty || busy}>
          {busy ? 'Saving…' : 'Save defaults'}
        </button>
      </div>
    </form>
  )
}

// UsersTable lists every account with a search box; clicking a row opens the
// per-user edit page.
function UsersTable({ users, error }: { users?: AdminUser[]; error?: Error }) {
  const [query, setQuery] = useState('')
  // Filtering is memoized so typing doesn't re-filter unless users or the
  // query actually changed.
  const visible = useMemo(
    () => (users ?? []).filter((u) => u.email.includes(query.trim().toLowerCase())),
    [users, query],
  )

  return (
    <section className="card">
      <div className="card-head">
        <h2>Users {users && <span className="muted">({users.length})</span>}</h2>
        <input
          className="input card-head__search"
          type="search"
          placeholder="Search by email"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          aria-label="Search users"
        />
      </div>
      {error && <div className="callout callout--error">{error.message}</div>}
      {!users && !error && <p className="muted">Loading…</p>}
      {users && (
        <div className="table-wrap">
          <table className="table table--users">
            <thead>
              <tr>
                <th>User</th>
                <th>Sign-in</th>
                <th className="num">Products</th>
                <th>Re-check</th>
                <th>Last login</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {visible.map((u) => (
                <tr key={u.id} className={u.disabled ? 'row--disabled' : ''}>
                  <td>
                    <Link to={`/admin/users/${u.id}`} className="user-link">
                      {u.email}
                    </Link>
                    <div className="row-badges">
                      {u.role === 'admin' && <span className="badge badge--admin">Admin</span>}
                      {u.disabled && <span className="badge badge--danger">Suspended</span>}
                      {hasOverrides(u) && <span className="badge badge--unchecked">Custom limits</span>}
                    </div>
                  </td>
                  <td className="muted">{signInMethods(u)}</td>
                  <td className="num">
                    {u.product_count} / {u.effective.max_products}
                  </td>
                  <td className="muted">{formatInterval(u.effective.check_interval_minutes)}</td>
                  <td className="muted">{u.last_login_at ? relativeTime(u.last_login_at) : 'Never'}</td>
                  <td className="num">
                    <Link to={`/admin/users/${u.id}`} className="btn btn--small">
                      Edit
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {visible.length === 0 && <p className="muted center">No users match.</p>}
        </div>
      )}
    </section>
  )
}

// signInMethods describes how an account can log in: "Google", "Password",
// "Google + Password", or "—" for a broken account with neither.
export function signInMethods(u: AdminUser): string {
  return [u.google_linked && 'Google', u.has_password && 'Password'].filter(Boolean).join(' + ') || '—'
}

// hasOverrides: does this user have any limit set to something other than the default?
const hasOverrides = (u: AdminUser) => Object.values(u.overrides).some((v) => v !== null)
