import { useState } from 'react'
import { discountPct, formatMoney } from '../format'
import type { ListingDetails } from '../types'

// ProductImage shows the store's product photo, or a neutral placeholder when
// there is none or it fails to load. referrerPolicy keeps our page URL private
// from the store's image server.
export function ProductImage({ src, alt, size = 'md' }: { src: string; alt: string; size?: 'sm' | 'md' | 'lg' }) {
  const [failed, setFailed] = useState(false)
  if (!src || failed) {
    return (
      <div className={`product-img product-img--${size} product-img--empty`} aria-hidden>
        <svg viewBox="0 0 24 24" width="40%" height="40%" fill="none" stroke="currentColor" strokeWidth="1.5">
          <path d="M6 7h12l-1 13H7L6 7Z" />
          <path d="M9 7a3 3 0 0 1 6 0" />
        </svg>
      </div>
    )
  }
  return (
    <div className={`product-img product-img--${size}`}>
      {/* loading="lazy": off-screen images aren't fetched until scrolled near.
          decoding="async": large store photos (often 1500×1500) are decoded off
          the main thread, so scrolling doesn't pause while they appear. */}
      <img src={src} alt={alt} loading="lazy" decoding="async" referrerPolicy="no-referrer" onError={() => setFailed(true)} />
    </div>
  )
}

// ProductFacts is the line of store details: brand, rating, stock, MRP and discount.
export function ProductFacts({ d, price, currency }: { d: ListingDetails; price: number; currency?: string }) {
  const off = discountPct(price, d.mrp)
  return (
    <div className="facts">
      {d.brand && <span className="fact">{d.brand}</span>}
      {d.rating != null && (
        <span className="fact" title={`${d.rating} out of ${d.best_rating ?? 5}`}>
          <span className="fact__star">★</span> {d.rating.toFixed(1)}
          {d.rating_count != null && <span className="muted"> ({d.rating_count.toLocaleString()})</span>}
        </span>
      )}
      {d.in_stock != null && (
        <span className={`fact ${d.in_stock ? 'text-good' : 'text-bad'}`}>{d.in_stock ? 'In stock' : 'Out of stock'}</span>
      )}
      {off != null && d.mrp != null && (
        <span className="fact">
          <s className="muted">{formatMoney(d.mrp, currency)}</s> <span className="badge badge--low">{off}% off</span>
        </span>
      )}
    </div>
  )
}
