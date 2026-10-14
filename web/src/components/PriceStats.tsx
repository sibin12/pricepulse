import { dealVerdict, formatDate, formatMoney, type PriceStats as Stats } from '../format'
import { BuyMeter } from './BuyMeter'

// PriceStats shows the verdict ("good time to buy?") and the key numbers:
// lowest price with every date it was seen, highest, and average.
export function PriceStats({ current, stats, currency }: { current: number; stats: Stats | null; currency?: string }) {
  const verdict = dealVerdict(current, stats, currency)
  return (
    <section className="price-stats">
      <BuyMeter current={current} stats={stats} verdict={verdict} />
      <div className="stats">
        <div className="stat stat--good">
          <span className="stat__value">{stats ? formatMoney(stats.lowest, currency) : '—'}</span>
          <span className="stat__label">Lowest price</span>
          {stats && stats.lowestDates.length > 0 && <LowestDates dates={stats.lowestDates} />}
        </div>
        <div className="stat">
          <span className="stat__value">{stats ? formatMoney(stats.highest, currency) : '—'}</span>
          <span className="stat__label">Highest price</span>
        </div>
        <div className="stat">
          <span className="stat__value">{stats ? formatMoney(stats.average, currency) : '—'}</span>
          <span className="stat__label">Average price</span>
        </div>
        <div className="stat">
          <span className="stat__value">{stats ? stats.count : 0}</span>
          <span className="stat__label">{stats ? `Price checks since ${formatDate(stats.since)}` : 'Price checks'}</span>
        </div>
      </div>
    </section>
  )
}

// LowestDates lists every day the lowest price was seen (newest first),
// collapsing long lists so the card stays small.
function LowestDates({ dates }: { dates: string[] }) {
  // ISO timestamps sort correctly as plain strings.
  const sorted = [...dates].sort((a, b) => b.localeCompare(a))
  const shown = sorted.slice(0, 3)
  return (
    <span className="low-dates" title={sorted.map(formatDate).join(', ')}>
      {sorted.length === 1 ? 'on ' : `on ${sorted.length} days: `}
      {shown.map(formatDate).join(', ')}
      {sorted.length > shown.length && ` +${sorted.length - shown.length} more`}
    </span>
  )
}
