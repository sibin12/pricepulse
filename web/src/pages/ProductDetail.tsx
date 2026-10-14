import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { Field } from '../components/Field'
import { HistoryChart } from '../components/HistoryChart'
import { PriceDrops } from '../components/PriceDrops'
import { ProductFacts, ProductImage } from '../components/ProductMedia'
import { SkeletonDetail } from '../components/Skeleton'
import { PriceStats } from '../components/PriceStats'
import { StatusBadge, TargetProgress } from '../components/StatusBadge'
import { formatDate, formatDateTime, formatMoney, hostOf, parsePrice, priceStats, priceStatus, relativeTime, savings } from '../format'
import type { PricePoint, Product } from '../types'
import { useAsync } from '../useAsync'
import { NotFound } from './NotFound'

// ProductDetail is one wish from the bucket list. Top to bottom:
//   hero (photo, name, store facts, note)  ->  current price vs my price  ->
//   buy meter + lowest/highest/average     ->  chart | got-it / alert / log / edit
//   recent price drops                     ->  every check  ->  remove from list
export function ProductDetail() {
  const id = Number(useParams().id)
  // Product and history are fetched in parallel and reloaded together after edits.
  const { data, error, loading, reload } = useAsync(() => Promise.all([api.getProduct(id), api.history(id)]), [id])

  // A bad id in the URL, or someone else's product (the API answers 404 for
  // those on purpose), both show the not-found page.
  if (!Number.isInteger(id) || id <= 0 || (error instanceof ApiError && error.status === 404)) {
    return <NotFound what="product" />
  }
  if (loading && !data) return <SkeletonDetail />
  if (error || !data) {
    return (
      <div className="callout callout--error">
        <p>{error?.message}</p>
        <button className="btn" onClick={reload}>
          Try again
        </button>
      </div>
    )
  }

  const [product, history] = data
  const status = priceStatus(product) // unchecked / hit / above
  // Lowest/highest are the ones saved on the shared listing (store prices);
  // your manual entries show on the chart but don't change them.
  const stats = priceStats(history, {
    lowest: product.lowest_price,
    dates: product.lowest_price_dates,
    highest: product.highest_price,
  })

  return (
    <>
      <Link to="/" className="back">
        ← Back to my bucket list
      </Link>

      <div className="product-hero">
        <ProductImage src={product.image_url} alt={product.title} size="lg" />
        <div className="detail-title">
          <h1>{product.title}</h1>
          <p className="muted">
            <a href={product.url} target="_blank" rel="noopener noreferrer" className="link">
              {hostOf(product.url)} ↗
            </a>{' '}
            · <Link to={`/history/${product.listing_id}`} className="link">Public price history page</Link>
          </p>
          <ProductFacts d={product} price={product.current_price} currency={product.currency} />
          {product.note && <p className="wish__note">“{product.note}”</p>}
        </div>
        <div className="detail-actions">
          {product.bought_at ? <span className="badge badge--low">Got it ✓</span> : <StatusBadge status={status} />}
          <CheckNowButton product={product} onDone={reload} />
        </div>
      </div>

      {product.last_error && (
        <div className="callout callout--warn">
          <p>
            <strong>Couldn't read the price</strong>
            {product.last_attempt_at && ` (${relativeTime(product.last_attempt_at)})`}: {product.last_error} You can still log prices
            manually below.
          </p>
        </div>
      )}

      <section className="stats stats--two">
        <div className="stat">
          <span className="stat__value">{status.kind === 'unchecked' ? '—' : formatMoney(product.current_price, product.currency)}</span>
          <span className="stat__label">
            Current price{status.kind !== 'unchecked' && ` · ${relativeTime(product.last_checked_at)}`}
          </span>
          <TargetProgress status={status} />
        </div>
        <div className="stat">
          <span className="stat__value">{formatMoney(product.target_price, product.currency)}</span>
          <span className="stat__label">My price</span>
        </div>
      </section>

      {status.kind !== 'unchecked' && <PriceStats current={product.current_price} stats={stats} currency={product.currency} />}

      <div className="detail-grid">
        <section className="card">
          <h2>Price history</h2>
          <HistoryChart points={history} target={product.target_price} lowest={product.lowest_price} currency={product.currency} />
        </section>

        <aside className="detail-side">
          <GotItCard product={product} onSaved={reload} />
          {!product.bought_at && <AlertCard product={product} />}
          <LogPriceForm productId={product.id} onSaved={reload} />
          <EditForm product={product} onSaved={reload} />
        </aside>
      </div>

      <PriceDrops points={history} currency={product.currency} />

      <section className="card">
        <h2>All checks</h2>
        <HistoryTable history={history} target={product.target_price} currency={product.currency} />
      </section>

      <DangerZone product={product} />
    </>
  )
}

// AlertCard explains what will happen (or has happened) with the email alert
// for this product, based on the server's alert configuration.
function AlertCard({ product }: { product: Product }) {
  const { data: status } = useAsync(api.alertStatus, []) // is SMTP configured, and for whom
  const alertsAllowed = useAuth().user?.limits.alerts_enabled ?? true // admin per-user switch
  if (!status) return null

  const to = status.recipients.join(', ')
  const target = formatMoney(product.target_price, product.currency)
  let body
  if (!alertsAllowed) {
    body = <p className="muted">Email alerts are turned off for your account by an administrator.</p>
  } else if (!status.enabled) {
    body = (
      <p className="muted">
        Email alerts are off. <Link to="/alerts">Set them up</Link> to get an email when this drops to {target}.
      </p>
    )
  } else if (product.alert_sent_at) {
    body = (
      <p>
        <span className="text-good">✓ Alert sent {relativeTime(product.alert_sent_at)}</span> to {to}.{' '}
        <span className="muted">You'll get another if the price goes above {target} and drops back.</span>
      </p>
    )
  } else if (product.current_price > 0 && product.current_price <= product.target_price) {
    body = <p>Target hit, sending an email to {to}…</p>
  } else {
    body = (
      <p className="muted">
        You'll get an email at {to} when the price drops to {target} or lower.
      </p>
    )
  }

  return (
    <section className="card">
      <h2>Email alert</h2>
      {body}
    </section>
  )
}

// LogPriceForm records a price by hand: a fallback for stores the scraper
// can't read, and for corrections.
function LogPriceForm({ productId, onSaved }: { productId: number; onSaved: () => void }) {
  const [price, setPrice] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    const value = parsePrice(price)
    if (value === null) return setError('Enter a price greater than 0')
    setSaving(true)
    setError('')
    try {
      await api.recordPrice(productId, value)
      setPrice('')
      onSaved()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="card form" onSubmit={onSubmit} noValidate>
      <h2>Log a price manually</h2>
      <Field label="Price seen now" htmlFor="log-price" error={error} hint="For stores that can't be read automatically.">
        <input
          id="log-price"
          className="input"
          type="number"
          inputMode="decimal"
          min="0.01"
          step="0.01"
          placeholder="0.00"
          value={price}
          onChange={(e) => setPrice(e.target.value)}
        />
      </Field>
      <button className="btn btn--primary" type="submit" disabled={saving}>
        {saving ? 'Saving…' : 'Add price'}
      </button>
    </form>
  )
}

// CheckNowButton asks the API to scrape the store page immediately. For a
// product that has never been checked it fires once automatically, so a newly
// added product gets its first price without waiting for the background worker.
function CheckNowButton({ product, onDone }: { product: Product; onDone: () => void }) {
  // Admins can switch manual checks off per user; the worker still checks
  // automatically, so the button (and the auto-check) simply disappear.
  const allowed = useAuth().user?.limits.manual_checks_enabled ?? true
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  // Remembers which product the automatic first check already ran for, so a
  // re-render (or React's dev-mode double effects) can't fire it twice.
  const autoTried = useRef<number | null>(null)

  // check asks the server to scrape the store now; it can take several
  // seconds, during which the button reads "Checking store…".
  async function check() {
    setChecking(true)
    setError('')
    try {
      await api.checkNow(product.id)
      onDone()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setChecking(false)
    }
  }

  useEffect(() => {
    if (allowed && product.last_attempt_at === null && autoTried.current !== product.id) {
      autoTried.current = product.id
      check()
    }
    // Runs only when the product (or its attempt state) changes, not on every render.
  }, [product.id, product.last_attempt_at, allowed])

  if (!allowed) return null

  return (
    <div className="check-now">
      <button className="btn" onClick={check} disabled={checking}>
        {checking ? 'Checking store…' : '↻ Check price now'}
      </button>
      {error && <span className="field__error">{error}</span>}
    </div>
  )
}

// EditForm updates name and target price via PATCH (only changed fields are sent).
function EditForm({ product, onSaved }: { product: Product; onSaved: () => void }) {
  const [title, setTitle] = useState(product.title)
  const [note, setNote] = useState(product.note)
  const [target, setTarget] = useState(String(product.target_price))
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [saved, setSaved] = useState(false)

  // Re-sync the inputs if the product is reloaded with new values.
  useEffect(() => {
    setTitle(product.title)
    setNote(product.note)
    setTarget(String(product.target_price))
  }, [product.title, product.note, product.target_price])

  const targetValue = parsePrice(target)
  // Enable Save only when a field differs from what the server has.
  const dirty = title.trim() !== product.title || note.trim() !== product.note || targetValue !== product.target_price

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    if (!title.trim()) return setError('Name cannot be empty')
    if (targetValue === null) return setError('Target must be greater than 0')
    setSaving(true)
    setError('')
    try {
      await api.updateProduct(product.id, {
        ...(title.trim() !== product.title && { title: title.trim() }),
        ...(note.trim() !== product.note && { note: note.trim() }),
        ...(targetValue !== product.target_price && { target_price: targetValue }),
      })
      setSaved(true)
      setTimeout(() => setSaved(false), 2000)
      onSaved()
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="card form" onSubmit={onSubmit} noValidate>
      <h2>Settings</h2>
      <Field label="Name" htmlFor="edit-title">
        <input id="edit-title" className="input" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} />
      </Field>
      <Field label="Why I want it" htmlFor="edit-note">
        <input id="edit-note" className="input" value={note} onChange={(e) => setNote(e.target.value)} maxLength={280} />
      </Field>
      <Field label="My price" htmlFor="edit-target" error={error}>
        <input
          id="edit-target"
          className="input"
          type="number"
          inputMode="decimal"
          min="0.01"
          step="0.01"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
        />
      </Field>
      <button className="btn" type="submit" disabled={!dirty || saving}>
        {saving ? 'Saving…' : saved ? 'Saved ✓' : 'Save changes'}
      </button>
    </form>
  )
}

// GotItCard checks the wish off the bucket list with the price actually paid
// (pre-filled with today's price), or puts it back on the list.
function GotItCard({ product, onSaved }: { product: Product; onSaved: () => void }) {
  const { refresh } = useAuth()
  const [price, setPrice] = useState(product.current_price > 0 ? String(product.current_price) : '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const saved = savings(product) // how much below MRP (or highest seen) they paid

  // run wraps either action (mark bought / put back) with the shared
  // busy/error handling and the reloads both need.
  async function run(action: () => Promise<unknown>) {
    setBusy(true)
    setError('')
    try {
      await action()
      onSaved()
      refresh() // the item frees (or takes) a slot
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  if (product.bought_at) {
    return (
      <section className="card form got-card">
        <h2>Got it ✓</h2>
        <p>
          Bought for <strong>{formatMoney(product.bought_price ?? 0, product.currency)}</strong> on {formatDate(product.bought_at)}.
        </p>
        {saved && (
          <p className="text-good">
            You saved {formatMoney(saved.amount, product.currency)} vs the {saved.versus}.
          </p>
        )}
        {error && <span className="field__error">{error}</span>}
        <button className="btn" disabled={busy} onClick={() => run(() => api.unmarkBought(product.id))}>
          Put back on my list
        </button>
      </section>
    )
  }

  function onSubmit(e: FormEvent) {
    e.preventDefault()
    const value = parsePrice(price)
    if (value === null) return setError('Enter the price you paid')
    run(() => api.markBought(product.id, value))
  }

  return (
    <form className="card form" onSubmit={onSubmit} noValidate>
      <h2>Got it?</h2>
      <p className="muted">Check it off your bucket list. It stops alerts and frees a slot.</p>
      <Field label="I paid" htmlFor="paid" error={error}>
        <input
          id="paid"
          className="input"
          type="number"
          inputMode="decimal"
          min="0.01"
          step="0.01"
          value={price}
          onChange={(e) => setPrice(e.target.value)}
        />
      </Field>
      <button className="btn btn--primary" type="submit" disabled={busy}>
        {busy ? 'Saving…' : '✓ Mark as bought'}
      </button>
    </form>
  )
}

// HistoryTable lists every check, newest first, with the change from the previous one.
function HistoryTable({ history, target, currency }: { history: PricePoint[]; target: number; currency: string }) {
  if (history.length === 0) return <p className="muted">No checks recorded yet.</p>

  // Pair each check with the change from the previous one, then show newest first.
  const rows = history.map((h, i) => ({ ...h, change: i === 0 ? null : h.price - history[i - 1].price })).reverse()

  return (
    <div className="table-wrap">
      <table className="table">
        <thead>
          <tr>
            <th>Checked</th>
            <th className="num">Price</th>
            <th className="num">Change</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              <td>
                {formatDateTime(r.recorded_at)}
                {r.source === 'manual' && <span className="badge badge--unchecked tag">Your entry</span>}
              </td>
              <td className={`num ${r.price <= target ? 'text-good' : ''}`}>{formatMoney(r.price, currency)}</td>
              <td className="num">
                {r.change === null || r.change === 0 ? (
                  <span className="muted">—</span>
                ) : (
                  // A price drop is good news, so it gets the "good" color.
                  <span className={r.change < 0 ? 'text-good' : 'text-bad'}>
                    {r.change < 0 ? '▼' : '▲'} {formatMoney(Math.abs(r.change), currency)}
                  </span>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// DangerZone removes the wish from the user's list. It needs two clicks so a
// stray click can't destroy data; the confirm state resets itself after a few
// seconds. The shared listing and its public price history are kept (other
// people may track it); only this user's manual price entries go with it.
function DangerZone({ product }: { product: Product }) {
  const navigate = useNavigate()
  const { refresh } = useAuth()
  const [confirming, setConfirming] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!confirming) return
    const t = setTimeout(() => setConfirming(false), 4000)
    return () => clearTimeout(t)
  }, [confirming])

  async function onDelete() {
    if (!confirming) return setConfirming(true)
    try {
      await api.deleteProduct(product.id)
      await refresh() // frees a product slot
      navigate('/')
    } catch (err) {
      setError((err as Error).message)
    }
  }

  return (
    <section className="card danger">
      <div>
        <h2>Remove from my list</h2>
        <p className="muted">
          Removes this wish, your note and any prices you logged by hand. The product's public price history stays.
        </p>
        {error && <p className="field__error">{error}</p>}
      </div>
      <button className="btn btn--danger" onClick={onDelete}>
        {confirming ? 'Click again to confirm' : 'Remove wish'}
      </button>
    </section>
  )
}
