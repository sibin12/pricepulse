import { useState } from 'react'
import { formatDate, formatMoney, priceDrops } from '../format'
import type { PricePoint } from '../types'

// PriceDrops lists recent price drops ("₹12,999 → ₹9,999, 23% drop").
export function PriceDrops({ points, currency }: { points: PricePoint[]; currency?: string }) {
  const [all, setAll] = useState(false) // "Show all" expands beyond the first four
  const drops = priceDrops(points)
  const shown = all ? drops : drops.slice(0, 4)
  return (
    <section className="card">
      <h2>Recent price drops</h2>
      {drops.length === 0 ? (
        <p className="muted">No price drops recorded yet. We'll list them here as the price changes.</p>
      ) : (
        <>
          <div className="drops">
            {shown.map((d) => (
              <div className="drop" key={d.at}>
                <span className="muted small">{formatDate(d.at)}</span>
                <span className="drop__prices">
                  <span className="text-bad">{formatMoney(d.from, currency)}</span> →{' '}
                  <span className="text-good">{formatMoney(d.to, currency)}</span>
                </span>
                <span className="small">{d.pct.toFixed(1)}% drop</span>
              </div>
            ))}
          </div>
          {drops.length > shown.length && (
            <button className="btn btn--small" onClick={() => setAll(true)}>
              Show all {drops.length} drops
            </button>
          )}
        </>
      )}
    </section>
  )
}
