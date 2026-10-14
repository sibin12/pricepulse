import { useEffect, useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, ApiError } from '../api'
import { useAuth } from '../auth'
import { HistoryChart } from '../components/HistoryChart'
import { PriceDrops } from '../components/PriceDrops'
import { ProductFacts, ProductImage } from '../components/ProductMedia'
import { SkeletonDetail } from '../components/Skeleton'
import { PriceStats } from '../components/PriceStats'
import { formatDate, formatMoney, hostOf, parsePrice, priceStats, relativeTime } from '../format'
import type { LookupResult } from '../types'
import { useAsync } from '../useAsync'
import { NotFound } from './NotFound'

// Public price-history pages, like pricehistory sites: paste a product link,
// see how its price changed, the lowest price (and when), and whether now is a
// good time to buy. No account needed; logging in adds "Track this product".
//
//   /history       search box
//   /history/:id   one product's history (shareable link)

// LookupForm takes a product URL and opens its history page.
function LookupForm({ autoFocus }: { autoFocus?: boolean }) {
  const navigate = useNavigate()
  const [url, setUrl] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError('')
    setBusy(true)
    try {
      // The server fetches products it hasn't seen before, which can take a
      // few seconds; known products return immediately.
      const res = await api.lookup(url.trim())
      setUrl('')
      navigate(`/history/${res.listing.id}`)
    } catch (err) {
      setError((err as Error).message)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="lookup" onSubmit={onSubmit}>
      <input
        className="input lookup__input"
        type="url"
        inputMode="url"
        placeholder="Paste a product link, e.g. https://www.boat-lifestyle.com/products/…"
        value={url}
        onChange={(e) => setUrl(e.target.value)}
        aria-label="Product link"
        autoFocus={autoFocus}
        required
      />
      <button className="btn btn--primary" type="submit" disabled={busy || !url.trim()}>
        {busy ? 'Checking store…' : 'Check price history'}
      </button>
      {error && <div className="callout callout--error lookup__error">{error}</div>}
    </form>
  )
}

export function PriceLookup() {
  return (
    <div className="lookup-page">
      <section className="lookup-hero">
        <h1>Price history for any product</h1>
        <p className="muted">
          Paste a product link to see its price history, the lowest price and when it happened, and whether now is a
          good time to buy.
        </p>
        <LookupForm autoFocus />
        <p className="muted small">
          Works with stores that publish product data, such as Flipkart, boAt, Allbirds and many Shopify stores.
          Amazon blocks automated price checks. History starts from the first time anyone looks a product up here.
        </p>
      </section>
    </div>
  )
}

// ListingPage is the shareable page for one product: /history/:id.
export function ListingPage() {
  const id = Number(useParams().id)
  const { data, error, loading, reload } = useAsync(() => api.listing(id), [id])

  // 404: unknown id. 401: an admin turned public lookups off, so visitors must
  // log in first (logged-in users never get a 401 here).

  if (!Number.isInteger(id) || id <= 0 || (error instanceof ApiError && error.status === 404)) {
    return <NotFound what="product" />
  }
  if (error instanceof ApiError && error.status === 401) {
    return (
      <div className="empty">
        <h2>Log in to see price history</h2>
        <p className="muted">Public lookups are turned off on this site.</p>
        <Link to={`/login?next=${encodeURIComponent(`/history/${id}`)}`} className="btn btn--primary">
          Log in
        </Link>
      </div>
    )
  }
  if (loading && !data) {
    return (
      <>
        <LookupForm />
        <SkeletonDetail />
      </>
    )
  }
  if (error || !data) return <div className="callout callout--error">{error?.message}</div>

  return (
    <>
      <LookupForm />
      <ListingView result={data} onTracked={reload} />
    </>
  )
}

function ListingView({ result, onTracked }: { result: LookupResult; onTracked: () => void }) {
  const { listing, history } = result
  const stats = priceStats(history, {
    lowest: listing.lowest_price,
    dates: listing.lowest_price_dates,
    highest: listing.highest_price,
  })
  const title = listing.title || hostOf(listing.url)
  const neverPriced = listing.current_price <= 0

  return (
    <>
      <div className="product-hero">
        <ProductImage src={listing.image_url} alt={title} size="lg" />
        <div className="detail-title">
          <h1>{title}</h1>
          <p className="muted">
            <a href={listing.url} target="_blank" rel="noopener noreferrer" className="link">
              {hostOf(listing.url)} ↗
            </a>
            {listing.last_checked_at && <> · checked {relativeTime(listing.last_checked_at)}</>}
            {listing.trackers > 0 && (
              <>
                {' '}
                · on {listing.trackers} {listing.trackers === 1 ? 'bucket list' : 'bucket lists'}
              </>
            )}
          </p>
          <ProductFacts d={listing} price={listing.current_price} currency={listing.currency} />
        </div>
        <div className="listing-price">
          <span className="listing-price__value">{neverPriced ? '—' : formatMoney(listing.current_price, listing.currency)}</span>
          <span className="muted small">Current price</span>
        </div>
      </div>

      {listing.last_error && (
        <div className="callout callout--warn">
          <p>
            <strong>{neverPriced ? "We couldn't read a price from this store." : 'The last check failed.'}</strong>{' '}
            {listing.last_error}
          </p>
        </div>
      )}

      {!neverPriced && <PriceStats current={listing.current_price} stats={stats} currency={listing.currency} />}

      {/* We can only record prices from the moment we first see a product, so
          new listings have short histories. Say so instead of looking broken. */}
      {!neverPriced && Date.now() - new Date(listing.created_at).getTime() < 30 * 86400000 && (
        <div className="callout callout--info">
          <p>
            PricePulse started recording this product on <strong>{formatDate(listing.created_at)}</strong>, so its
            history begins there and grows with every check.{' '}
            {result.my_product_id ? 'It is checked regularly because you track it.' : 'Track it to have it checked more often.'}
          </p>
        </div>
      )}

      <div className="detail-grid">
        <section className="card">
          <div className="card-head">
            <h2>Price history</h2>
            <ShareButton />
          </div>
          <HistoryChart points={history} lowest={listing.lowest_price} currency={listing.currency} />
        </section>
        <aside className="detail-side">
          <TrackCard result={result} onTracked={onTracked} />
        </aside>
      </div>

      {!neverPriced && <PriceDrops points={history} currency={listing.currency} />}
    </>
  )
}

// TrackCard is the call to action: track (logged in), open your product
// (already tracking), or log in to track (logged out).
function TrackCard({ result, onTracked }: { result: LookupResult; onTracked: () => void }) {
  const { user, refresh } = useAuth()
  const navigate = useNavigate()
  const { listing } = result
  // Pre-fill "my price" with 10% below the current price as a starting point.
  const suggested = listing.current_price > 0 ? Math.floor(listing.current_price * 0.9) : ''
  const [target, setTarget] = useState(String(suggested))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => setTarget(String(suggested)), [suggested])

  if (result.my_product_id) {
    return (
      <section className="card form">
        <h2>It's on your bucket list</h2>
        <p className="muted">You'll get an email when it drops to your target price.</p>
        <Link to={`/products/${result.my_product_id}`} className="btn btn--primary">
          Open in my bucket list
        </Link>
      </section>
    )
  }

  if (!user) {
    return (
      <section className="card form">
        <h2>Want it? Add it to your bucket list</h2>
        <p className="muted">Log in, set the price you'd pay, and get an email when it drops there.</p>
        <Link to={`/login?next=${encodeURIComponent(`/history/${listing.id}`)}`} className="btn btn--primary">
          Log in to add it
        </Link>
        <Link to={`/register?next=${encodeURIComponent(`/history/${listing.id}`)}`} className="btn">
          Create a free account
        </Link>
      </section>
    )
  }

  async function onTrack(e: FormEvent) {
    e.preventDefault()
    const value = parsePrice(target)
    if (value === null) return setError('Enter a target price greater than 0')
    setBusy(true)
    setError('')
    try {
      const p = await api.createProduct({ url: listing.url, target_price: value })
      await refresh()
      navigate(`/products/${p.id}`)
    } catch (err) {
      // 409 = already tracking (e.g. in another tab): just reload to show it.
      if (err instanceof ApiError && err.status === 409) onTracked()
      else setError((err as Error).message)
      setBusy(false)
    }
  }

  return (
    <form className="card form" onSubmit={onTrack} noValidate>
      <h2>Add to my bucket list</h2>
      <p className="muted">We'll keep checking the price and email you when it drops to your price.</p>
      <div className="field">
        <label htmlFor="track-target">My price</label>
        <input
          id="track-target"
          className="input"
          type="number"
          inputMode="decimal"
          min="0.01"
          step="0.01"
          value={target}
          onChange={(e) => setTarget(e.target.value)}
        />
      </div>
      {error && <div className="callout callout--error">{error}</div>}
      <button className="btn btn--primary" type="submit" disabled={busy}>
        {busy ? 'Adding…' : 'Add to my bucket list'}
      </button>
    </form>
  )
}

// ShareButton copies the page URL; the page needs no login, so anyone can open it.
function ShareButton() {
  const [copied, setCopied] = useState(false)
  async function copy() {
    try {
      await navigator.clipboard.writeText(window.location.href)
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    } catch {
      // Clipboard can be blocked (e.g. insecure context); the URL bar still works.
    }
  }
  return (
    <button className="btn btn--small" onClick={copy}>
      {copied ? 'Link copied ✓' : 'Copy link'}
    </button>
  )
}
