import { useEffect, useState, type FormEvent, type ReactNode } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../../api'
import { useAuth } from '../../auth'
import { StatusBadge } from '../../components/StatusBadge'
import { formatDateTime, formatInterval, formatMoney, priceStatus, relativeTime } from '../../format'
import type { AdminUser, Overrides, Role } from '../../types'
import { useAsync } from '../../useAsync'
import { NotFound } from '../NotFound'
import { signInMethods } from './AdminDashboard'
import { IntervalSelect, onOff } from './controls'

// AdminUserPage edits one user: role, suspension, and personal overrides of
// the global defaults (each one can be switched back to "use default").
export function AdminUserPage() {
  const id = Number(useParams().id)
  // The user (with their products) and the global defaults, so the form can
  // show "Default (20)" next to each override.
  const { data, error, reload } = useAsync(() => api.adminUser(id), [id])
  const defaults = useAsync(api.adminSettings, [])

  if (error instanceof ApiError && error.status === 404) return <NotFound what="user" />
  if (error) return <div className="callout callout--error">{error.message}</div>
  if (!data || !defaults.data) return <p className="muted">Loading…</p>

  const { user, products } = data
  return (
    <>
      <Link to="/admin" className="back">
        ← Back to admin
      </Link>
      <div className="page-head">
        <div className="detail-title">
          <h1>{user.email}</h1>
          <p className="muted">
            Joined {formatDateTime(user.created_at)} · signs in with {signInMethods(user)} · last login{' '}
            {user.last_login_at ? relativeTime(user.last_login_at) : 'never'}
          </p>
        </div>
        <div className="row-badges">
          {user.role === 'admin' && <span className="badge badge--admin">Admin</span>}
          {user.disabled && <span className="badge badge--danger">Suspended</span>}
        </div>
      </div>

      <div className="detail-grid">
        <EditUser user={user} defaults={defaults.data.defaults} onSaved={reload} />
        <aside className="detail-side">
          <section className="card">
            <h2>Effective limits</h2>
            <dl className="kv">
              <dt>Products</dt>
              <dd>
                {user.product_count} of {user.effective.max_products}
              </dd>
              <dt>Re-check</dt>
              <dd>{formatInterval(user.effective.check_interval_minutes)}</dd>
              <dt>Email alerts</dt>
              <dd>{onOff(user.effective.alerts_enabled)}</dd>
              <dt>Check now</dt>
              <dd>{onOff(user.effective.manual_checks_enabled)}</dd>
            </dl>
          </section>
          <DeleteUser user={user} />
        </aside>
      </div>

      <section className="card">
        <h2>Products ({products.length})</h2>
        {products.length === 0 ? (
          <p className="muted">This user isn't tracking anything yet.</p>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th>Product</th>
                  <th className="num">Price</th>
                  <th className="num">Target</th>
                  <th>Status</th>
                </tr>
              </thead>
              <tbody>
                {products.map((p) => (
                  <tr key={p.id}>
                    <td>
                      <a href={p.url} target="_blank" rel="noopener noreferrer" className="user-link">
                        {p.title}
                      </a>
                      {p.last_error && <div className="text-bad small">⚠ {p.last_error}</div>}
                    </td>
                    <td className="num">{p.current_price > 0 ? formatMoney(p.current_price, p.currency) : '—'}</td>
                    <td className="num">{formatMoney(p.target_price, p.currency)}</td>
                    <td>
                      <StatusBadge status={priceStatus(p)} />
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </>
  )
}

type Defaults = AdminUser['effective']

// EditUser is the form for role, suspension and per-user overrides. Overrides
// are sent in full on save: null means "use the global default".
function EditUser({ user, defaults, onSaved }: { user: AdminUser; defaults: Defaults; onSaved: () => void }) {
  const me = useAuth().user
  // The API refuses to let admins demote or suspend themselves; the form
  // disables those controls so the refusal is never hit.
  const isSelf = me?.id === user.id
  const [role, setRole] = useState<Role>(user.role)
  const [disabled, setDisabled] = useState(user.disabled)
  const [o, setO] = useState<Overrides>(user.overrides)
  const [busy, setBusy] = useState(false)
  const [msg, setMsg] = useState<{ ok: boolean; text: string } | null>(null)

  // After a save the page reloads the user; copy the confirmed values back
  // into the inputs so "dirty" becomes false again.
  useEffect(() => {
    setRole(user.role)
    setDisabled(user.disabled)
    setO(user.overrides)
  }, [user])

  // Enable Save only when something changed.
  const dirty =
    role !== user.role || disabled !== user.disabled || JSON.stringify(o) !== JSON.stringify(user.overrides)
  const set = <K extends keyof Overrides>(k: K, v: Overrides[K]) => setO((prev) => ({ ...prev, [k]: v }))

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setMsg(null)
    try {
      await api.adminUpdateUser(user.id, { role, disabled, overrides: o })
      setMsg({ ok: true, text: disabled && !user.disabled ? 'Saved. The user has been logged out.' : 'Saved.' })
      onSaved()
    } catch (err) {
      setMsg({ ok: false, text: (err as Error).message })
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="card form" onSubmit={onSubmit}>
      <h2>Account</h2>
      <div className="settings-grid">
        <div className="field">
          <label htmlFor="u-role">Role</label>
          <select id="u-role" className="input" value={role} onChange={(e) => setRole(e.target.value as Role)} disabled={isSelf}>
            <option value="user">User</option>
            <option value="admin">Admin: can open this dashboard</option>
          </select>
        </div>
        <div className="field">
          <label htmlFor="u-status">Status</label>
          <select
            id="u-status"
            className="input"
            value={disabled ? 'suspended' : 'active'}
            onChange={(e) => setDisabled(e.target.value === 'suspended')}
            disabled={isSelf}
          >
            <option value="active">Active</option>
            <option value="suspended">Suspended: can't log in, checks paused</option>
          </select>
        </div>
      </div>
      {isSelf && <p className="field__hint">You can't change your own role or suspend yourself.</p>}

      <h2 className="form__section">Limits for this user</h2>
      <p className="muted">Leave a setting on "Default" to follow the global settings.</p>

      <Override
        label="Max products"
        isCustom={o.max_products !== null}
        defaultText={String(defaults.max_products)}
        onToggle={(custom) => set('max_products', custom ? defaults.max_products : null)}
      >
        <input
          className="input"
          type="number"
          min={0}
          max={10000}
          aria-label="Max products"
          value={o.max_products ?? ''}
          onChange={(e) => set('max_products', Number(e.target.value))}
        />
      </Override>

      <Override
        label="Re-check prices"
        isCustom={o.check_interval_minutes !== null}
        defaultText={formatInterval(defaults.check_interval_minutes)}
        onToggle={(custom) => set('check_interval_minutes', custom ? defaults.check_interval_minutes : null)}
      >
        <IntervalSelect
          id="u-interval"
          value={o.check_interval_minutes ?? defaults.check_interval_minutes}
          onChange={(v) => set('check_interval_minutes', v)}
        />
      </Override>

      <TriState
        id="u-alerts"
        label="Email alerts"
        value={o.alerts_enabled}
        defaultValue={defaults.alerts_enabled}
        onChange={(v) => set('alerts_enabled', v)}
      />
      <TriState
        id="u-manual"
        label={'"Check price now" button'}
        value={o.manual_checks_enabled}
        defaultValue={defaults.manual_checks_enabled}
        onChange={(v) => set('manual_checks_enabled', v)}
      />

      {msg && <div className={`callout ${msg.ok ? 'callout--ok' : 'callout--error'}`}>{msg.text}</div>}
      <div>
        <button className="btn btn--primary" type="submit" disabled={!dirty || busy}>
          {busy ? 'Saving…' : 'Save changes'}
        </button>
      </div>
    </form>
  )
}

// Override shows "Default (X)" or a custom input for a numeric setting.
// Switching to Custom pre-fills the input with the default so the admin can
// adjust from a sensible starting point.
function Override(props: {
  label: string
  isCustom: boolean
  defaultText: string
  onToggle: (custom: boolean) => void
  children: ReactNode
}) {
  return (
    <div className="override">
      <span className="override__label">{props.label}</span>
      <select
        className="input override__mode"
        aria-label={`${props.label}: default or custom`}
        value={props.isCustom ? 'custom' : 'default'}
        onChange={(e) => props.onToggle(e.target.value === 'custom')}
      >
        <option value="default">Default ({props.defaultText})</option>
        <option value="custom">Custom</option>
      </select>
      <div className="override__value">{props.isCustom ? props.children : null}</div>
    </div>
  )
}

// TriState is Default / On / Off for a boolean setting (null = default).
function TriState(props: {
  id: string
  label: string
  value: boolean | null
  defaultValue: boolean
  onChange: (v: boolean | null) => void
}) {
  const current = props.value === null ? 'default' : props.value ? 'on' : 'off'
  return (
    <div className="override">
      <label className="override__label" htmlFor={props.id}>
        {props.label}
      </label>
      <select
        id={props.id}
        className="input override__mode"
        value={current}
        onChange={(e) => props.onChange(e.target.value === 'default' ? null : e.target.value === 'on')}
      >
        <option value="default">Default ({onOff(props.defaultValue)})</option>
        <option value="on">On</option>
        <option value="off">Off</option>
      </select>
      <div className="override__value" />
    </div>
  )
}

// DeleteUser needs two clicks so a stray click can't destroy an account; the
// confirmation resets itself after a few seconds. Admins can't delete themselves.
function DeleteUser({ user }: { user: AdminUser }) {
  const navigate = useNavigate()
  const isSelf = useAuth().user?.id === user.id
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!confirming) return
    const t = setTimeout(() => setConfirming(false), 4000)
    return () => clearTimeout(t)
  }, [confirming])

  if (isSelf) return null

  async function onDelete() {
    if (!confirming) return setConfirming(true)
    try {
      await api.adminDeleteUser(user.id)
      navigate('/admin')
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <section className="card danger danger--stacked">
      <div>
        <h2>Delete user</h2>
        <p className="muted">
          Permanently deletes this account and its {user.product_count} product{user.product_count === 1 ? '' : 's'}. To
          block someone temporarily, suspend them instead.
        </p>
        {error && <p className="field__error">{error}</p>}
      </div>
      <button className="btn btn--danger" onClick={onDelete}>
        {confirming ? 'Click again to confirm' : 'Delete user'}
      </button>
    </section>
  )
}
