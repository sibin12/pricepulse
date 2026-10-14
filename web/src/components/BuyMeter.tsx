import { buyMeter, type PriceStats, type Verdict } from '../format'

const ZONES = [
  { key: 'skip', label: 'Skip' },
  { key: 'wait', label: 'Wait' },
  { key: 'okay', label: 'Okay' },
  { key: 'yes', label: 'Yes' },
] as const

// BuyMeter is the "Should you buy at this price?" gauge: the marker sits
// between the highest price seen (Skip) and the lowest (Yes).
export function BuyMeter({ current, stats, verdict }: { current: number; stats: PriceStats | null; verdict: Verdict }) {
  // m is null until there are at least three checks with some price movement;
  // the bar is then shown grey with the marker hidden.
  const m = buyMeter(current, stats)
  return (
    <section className={`card meter-card ${m ? '' : 'meter-card--empty'}`}>
      <div className="meter-head">
        <h2>Should you buy at this price?</h2>
        <span className={`verdict-pill verdict-pill--${verdict.kind}`}>{verdict.label}</span>
      </div>
      <div className="meter" role="img" aria-label={m ? `Buy meter: ${m.zone}` : 'Not enough history for the buy meter'}>
        <div className="meter__bar" />
        {m && <div className="meter__marker" style={{ left: `${m.position * 100}%` }} />}
        <div className="meter__labels">
          {ZONES.map((z) => (
            <span key={z.key} className={m?.zone === z.key ? 'meter__label--active' : ''}>
              {z.label}
            </span>
          ))}
        </div>
      </div>
      <p className="muted">{verdict.detail}</p>
    </section>
  )
}
