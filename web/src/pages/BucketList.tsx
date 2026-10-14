import { memo, useCallback, useMemo, useState, type MouseEvent } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../api'
import { useAuth } from '../auth'
import { ProductImage } from '../components/ProductMedia'
import { SkeletonCards } from '../components/Skeleton'
import { discountPct, formatDate, formatInterval, formatMoney, hostOf, savings } from '../format'
import type { Product } from '../types'
import { useAsync } from '../useAsync'

// The bucket list: everything the user wants to buy, at the price they want.
// Items move from "Waiting" to "Ready to buy" when the price hits the target,
// and get checked off as "Got it" once bought (showing how much was saved).

type Stage = 'waiting' | 'ready' | 'got'
type Tab = 'all' | Stage
type Sort = 'newest' | 'closest' | 'price' | 'name'

export function stageOf(p: Product): Stage {
  if (p.bought_at) return 'got'
  if (p.current_price > 0 && p.current_price <= p.target_price) return 'ready'
  return 'waiting'
}

const tabs: { value: Tab; label: string }[] = [
  { value: 'all', label: 'Everything' },
  { value: 'waiting', label: 'Waiting' },
  { value: 'ready', label: 'Ready to buy' },
  { value: 'got', label: 'Got it' },
]

export function BucketList() {
  const { data: products, error, loading, reload } = useAsync(api.listProducts, [])
  const { user, refresh } = useAuth()
  const [tab, setTab] = useState<Tab>('all')
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<Sort>('newest')
  const [actionError, setActionError] = useState('')

  // How many wishes are in each stage; drives the headline, tiles and tab counts.
  const counts = useMemo(() => {
    const c = { all: 0, waiting: 0, ready: 0, got: 0 }
    for (const p of products ?? []) {
      c.all++
      c[stageOf(p)]++
    }
    return c
  }, [products])

  // Total saved on bought items, per currency (adding ₹ and $ makes no sense).
  const saved = useMemo(() => {
    const byCurrency = new Map<string, number>()
    for (const p of products ?? []) {
      const s = savings(p)
      if (s) byCurrency.set(p.currency, (byCurrency.get(p.currency) ?? 0) + s.amount)
    }
    return [...byCurrency].map(([cur, amount]) => formatMoney(amount, cur)).join(' + ')
  }, [products])

  // The cards to show: filtered by tab and search text, then sorted.
  const visible = useMemo(() => {
    const q = query.trim().toLowerCase()
    return (products ?? [])
      .filter((p) => (tab === 'all' || stageOf(p) === tab) && (!q || `${p.title} ${p.note} ${p.url}`.toLowerCase().includes(q)))
      .sort(compareBy(sort))
  }, [products, tab, query, sort])

  // Only active wishes (not bought ones) count towards the user's limit.
  const limits = user?.limits
  const active = counts.waiting + counts.ready
  const full = !!limits && active >= limits.max_products

  // The round check button on each card: check off (bought at today's price)
  // or put back on the list. useCallback keeps the function identity stable so
  // memo()'d cards don't all re-render whenever this page does.
  const toggleBought = useCallback(
    async (p: Product) => {
      setActionError('')
      try {
        if (p.bought_at) await api.unmarkBought(p.id)
        else await api.markBought(p.id)
        reload()
        refresh() // a slot was freed or used
      } catch (err) {
        setActionError((err as Error).message)
      }
    },
    [reload, refresh],
  )

  // First load: show card-shaped placeholders so the grid doesn't jump in.
  // Later reloads (after checking an item off) keep the current cards on screen.
  if (loading && !products) return <SkeletonCards />
  if (error) {
    return (
      <div className="callout callout--error">
        <p>{error.message}</p>
        <button className="btn" onClick={reload}>
          Try again
        </button>
      </div>
    )
  }

  return (
    <>
      <section className="bucket-hero">
        <div>
          <p className="eyebrow">My bucket list</p>
          <h1>{headline(counts)}</h1>
          <p className="muted">Add what you want and the price you'd happily pay. We watch the price and tell you when it's time.</p>
        </div>
        {full ? (
          <span className="btn btn--primary" aria-disabled title="Your bucket list is full">
            List is full
          </span>
        ) : (
          <Link to="/products/new" className="btn btn--primary">
            + Add a wish
          </Link>
        )}
      </section>

      <section className="bucket-stats">
        <Tile label="Waiting" value={counts.waiting} icon="⏳" />
        <Tile label="Ready to buy" value={counts.ready} icon="🎉" tone={counts.ready ? 'good' : undefined} />
        <Tile label="Got it" value={counts.got} icon="✓" />
        <Tile label="Saved so far" value={saved || '—'} icon="💰" />
      </section>

      {limits && (
        <p className="muted small usage-line">
          {active} of {limits.max_products} wishes used · prices checked {formatInterval(limits.check_interval_minutes)}
          {counts.got > 0 && ' · bought items don’t use a slot'}
        </p>
      )}

      {actionError && <div className="callout callout--error">{actionError}</div>}

      {counts.all === 0 ? (
        <div className="empty bucket-empty">
          <div className="bucket-empty__icon" aria-hidden>
            🪣
          </div>
          <h2>Your bucket list is empty</h2>
          <p className="muted">That gadget, those shoes, the thing you keep looking at: add it with the price you'd pay.</p>
          <div className="bucket-empty__actions">
            <Link to="/products/new" className="btn btn--primary">
              Add your first wish
            </Link>
            <Link to="/history" className="btn">
              Look up a price first
            </Link>
          </div>
        </div>
      ) : (
        <>
          <div className="toolbar">
            <div className="chips" role="group" aria-label="Show">
              {tabs.map((t) => (
                <button
                  key={t.value}
                  className={`chip ${tab === t.value ? 'chip--active' : ''}`}
                  aria-pressed={tab === t.value}
                  onClick={() => setTab(t.value)}
                >
                  {t.label} <span className="chip__count">{counts[t.value]}</span>
                </button>
              ))}
            </div>
            <input
              className="input toolbar__search"
              type="search"
              placeholder="Search your list"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              aria-label="Search your bucket list"
            />
            <select className="input toolbar__sort" value={sort} onChange={(e) => setSort(e.target.value as Sort)} aria-label="Sort">
              <option value="newest">Newest first</option>
              <option value="closest">Closest to my price</option>
              <option value="price">Lowest price</option>
              <option value="name">Name A–Z</option>
            </select>
          </div>

          {visible.length === 0 ? (
            <p className="muted center">{tab === 'got' ? 'Nothing checked off yet. Soon!' : 'Nothing here right now.'}</p>
          ) : (
            <ul className="bucket-grid">
              {visible.map((p) => (
                <WishCard key={p.id} product={p} onToggle={toggleBought} />
              ))}
            </ul>
          )}
        </>
      )}
    </>
  )
}

// headline picks the big sentence at the top, most exciting state first.
function headline(c: { waiting: number; ready: number; got: number }): string {
  if (c.ready > 0) return `${c.ready} ${c.ready === 1 ? 'wish is' : 'wishes are'} ready to buy!`
  if (c.waiting > 0) return `${c.waiting} ${c.waiting === 1 ? 'thing' : 'things'} I'm waiting to buy`
  if (c.got > 0) return 'Everything checked off. Time for a new wish?'
  return 'Start your bucket list'
}

// Tile is one of the four summary boxes under the headline.
function Tile({ label, value, icon, tone }: { label: string; value: number | string; icon: string; tone?: 'good' }) {
  return (
    <div className={`stat bucket-tile ${tone ? `stat--${tone}` : ''}`}>
      <span className="bucket-tile__icon" aria-hidden>
        {icon}
      </span>
      <span className="stat__value">{value}</span>
      <span className="stat__label">{label}</span>
    </div>
  )
}

// WishCard is one bucket-list item. memo() skips re-rendering a card whose
// product and handler haven't changed, e.g. while typing in the search box.
const WishCard = memo(function WishCard({ product: p, onToggle }: { product: Product; onToggle: (p: Product) => void }) {
  const stage = stageOf(p)
  const priced = p.current_price > 0 // false until the first successful check
  const toGo = p.current_price - p.target_price // how much the price must still fall
  // Progress bar: the share of the way from the current price down to the target.
  const progress = priced ? Math.min(100, (p.target_price / p.current_price) * 100) : 0
  const saved = savings(p)
  const off = discountPct(p.current_price, p.mrp)
  const atLow = priced && p.lowest_price != null && p.current_price <= p.lowest_price

  function check(e: MouseEvent) {
    // The whole card is a link; the check button must not open the product.
    e.preventDefault()
    e.stopPropagation()
    onToggle(p)
  }

  return (
    <li className={`wish wish--${stage}`}>
      <Link to={`/products/${p.id}`} className="wish__link">
        <div className="wish__media">
          <ProductImage src={p.image_url} alt={p.title} />
          <button
            className={`wish__check ${stage === 'got' ? 'wish__check--done' : ''}`}
            onClick={check}
            aria-label={stage === 'got' ? `Put "${p.title}" back on the list` : `Mark "${p.title}" as bought`}
            title={stage === 'got' ? 'Bought: click to put it back on the list' : 'Got it? Check it off'}
          >
            ✓
          </button>
          {stage === 'ready' && <span className="wish__ribbon">Ready to buy 🎉</span>}
          {stage === 'got' && <span className="wish__ribbon wish__ribbon--got">Got it</span>}
        </div>

        <div className="wish__body">
          <span className="wish__title">{p.title}</span>
          <span className="wish__meta">
            {hostOf(p.url)}
            {p.brand && ` · ${p.brand}`}
            {p.rating != null && ` · ★ ${p.rating.toFixed(1)}`}
          </span>
          {p.note && <span className="wish__note">“{p.note}”</span>}

          {stage === 'got' ? (
            <div className="wish__got">
              <span>
                Got it for <strong>{formatMoney(p.bought_price ?? 0, p.currency)}</strong>
                {p.bought_at && <span className="muted"> · {formatDate(p.bought_at)}</span>}
              </span>
              {saved && (
                <span className="text-good small">
                  Saved {formatMoney(saved.amount, p.currency)} vs {saved.versus}
                </span>
              )}
            </div>
          ) : (
            <>
              <div className="wish__prices">
                <span className="wish__price">{priced ? formatMoney(p.current_price, p.currency) : 'Checking price…'}</span>
                <span className="muted small">my price {formatMoney(p.target_price, p.currency)}</span>
              </div>
              {priced && (
                <>
                  <div className={`progress ${stage === 'ready' ? 'progress--hit' : ''}`} aria-hidden>
                    <div className="progress__fill" style={{ width: `${progress}%` }} />
                  </div>
                  <span className={`small ${stage === 'ready' ? 'text-good' : 'muted'}`}>
                    {stage === 'ready'
                      ? toGo < 0
                        ? `${formatMoney(-toGo, p.currency)} below my price`
                        : 'At my price'
                      : `${formatMoney(toGo, p.currency)} to go`}
                  </span>
                </>
              )}
              <div className="row-badges">
                {atLow && <span className="badge badge--low">All-time low</span>}
                {off != null && <span className="badge badge--unchecked">{off}% off MRP</span>}
                {p.in_stock === false && <span className="badge badge--danger">Out of stock</span>}
                {p.last_error && !priced && <span className="badge badge--danger">Can't read price</span>}
              </div>
            </>
          )}
        </div>
      </Link>
    </li>
  )
})

// compareBy returns the sort comparator for the chosen option.
function compareBy(sort: Sort): (a: Product, b: Product) => number {
  switch (sort) {
    case 'closest':
      // Ready first, then the smallest remaining gap; unpriced last.
      return (a, b) => gap(a) - gap(b)
    case 'price':
      return (a, b) => (a.current_price || Infinity) - (b.current_price || Infinity)
    case 'name':
      return (a, b) => a.title.localeCompare(b.title)
    default:
      return (a, b) => b.created_at.localeCompare(a.created_at)
  }
}

// gap orders products for "closest to my price": smallest remaining gap first;
// products without a price, then bought ones, go to the end.
function gap(p: Product): number {
  if (p.bought_at) return Infinity
  if (p.current_price <= 0) return Infinity - 1
  return (p.current_price - p.target_price) / p.target_price
}
