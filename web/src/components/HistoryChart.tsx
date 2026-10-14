import { useMemo, useState } from 'react'
import type { PricePoint } from '../types'
import { PriceChart } from './PriceChart'

const RANGES = [
  { key: '1m', label: '1M', days: 30 },
  { key: '3m', label: '3M', days: 90 },
  { key: '6m', label: '6M', days: 182 },
  { key: '1y', label: '1Y', days: 365 },
  { key: 'all', label: 'All', days: Infinity },
] as const

type RangeKey = (typeof RANGES)[number]['key']

// HistoryChart is PriceChart plus range buttons (1M / 3M / 6M / 1Y / All),
// like price-history sites have. Filtering happens here, on the client.
export function HistoryChart(props: { points: PricePoint[]; target?: number; lowest?: number | null; currency?: string }) {
  const [range, setRange] = useState<RangeKey>('all')

  // Points inside the chosen range. Memoized so hovering the chart (which
  // re-renders this component's child) doesn't re-filter every time.
  const visible = useMemo(() => {
    const days = RANGES.find((r) => r.key === range)!.days
    if (!Number.isFinite(days)) return props.points
    const since = Date.now() - days * 86400000
    return props.points.filter((p) => new Date(p.recorded_at).getTime() >= since)
  }, [props.points, range])

  return (
    <div className="history-chart">
      <div className="chips chips--small" role="group" aria-label="Time range">
        {RANGES.map((r) => (
          <button
            key={r.key}
            className={`chip ${range === r.key ? 'chip--active' : ''}`}
            aria-pressed={range === r.key}
            onClick={() => setRange(r.key)}
          >
            {r.label}
          </button>
        ))}
      </div>
      {visible.length === 0 && props.points.length > 0 ? (
        <div className="chart-empty">No prices recorded in this period.</div>
      ) : (
        <PriceChart points={visible} target={props.target} lowest={props.lowest} currency={props.currency} />
      )}
      <div className="chart-legend muted">
        <span>
          <i className="legend-dot legend-dot--low" /> Lowest price
        </span>
        {props.points.some((p) => p.source === 'manual') && (
          <span>
            <i className="legend-dot legend-dot--manual" /> Your manual entries (only you see these)
          </span>
        )}
      </div>
    </div>
  )
}
