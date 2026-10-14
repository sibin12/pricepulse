import { useId, useLayoutEffect, useMemo, useRef, useState, type MouseEvent } from 'react'
import { formatDateTime, formatMoney, formatShortDate, formatTime } from '../format'
import type { PricePoint } from '../types'

// Fixed chart height; the width follows the container.
const HEIGHT = 260
// Space around the plot for axis labels (left is wide for prices like ₹11,499.00).
const PAD = { top: 20, right: 20, bottom: 32, left: 72 }

interface Props {
  points: PricePoint[]
  target?: number // dashed "your target" line; omitted on public pages
  lowest?: number | null // all-time lowest: points at this price get a green marker
  currency?: string // ISO code for axis/tooltip formatting
}

// PriceChart draws price history as an SVG line chart, with an optional dashed
// target line, green markers on all-time-low points, and hollow markers for
// prices the user typed in by hand.
//
// It is hand-rolled (no chart library) to keep the bundle small; the x axis is
// proportional to time so uneven gaps between checks are shown honestly.
//
// Performance notes:
//   - All geometry (scales, paths, tick positions) is computed once per data
//     change with useMemo, so hovering only re-renders the marker and tooltip.
//   - Mouse moves are throttled to one update per animation frame.
//   - The width is measured before the first paint (useLayoutEffect), so the
//     chart never flashes at a default size and then jumps.
export function PriceChart({ points, target, lowest, currency }: Props) {
  const wrapRef = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0) // 0 = not measured yet (renders nothing)
  const [hover, setHover] = useState<number | null>(null)
  const gradientId = 'area-' + useId().replace(/:/g, '')
  const frame = useRef(0) // pending requestAnimationFrame id for hover updates

  // Measure the container synchronously before paint, then keep following
  // resizes (scaling the SVG instead would stretch the text).
  useLayoutEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const apply = (w: number) => setWidth(Math.max(280, Math.floor(w)))
    apply(el.getBoundingClientRect().width)
    const ro = new ResizeObserver(([entry]) => apply(entry.contentRect.width))
    ro.observe(el)
    return () => {
      ro.disconnect()
      cancelAnimationFrame(frame.current)
    }
  }, [])

  // Everything that depends only on the data and the width.
  const geo = useMemo(() => {
    if (points.length === 0 || width === 0) return null

    const innerW = width - PAD.left - PAD.right
    const innerH = HEIGHT - PAD.top - PAD.bottom

    // Y range covers every price and the target, padded and snapped to a "nice"
    // step (1, 2, 5 × 10ⁿ) so axis labels read 300, 320, 340… instead of 312.47.
    const prices = points.map((p) => p.price)
    const withTarget = target === undefined ? prices : [...prices, target]
    const min = Math.min(...withTarget)
    const max = Math.max(...withTarget)
    const span = max - min || max * 0.1 || 1
    const step = niceStep((span * 1.3) / 4)
    const lo = Math.max(0, Math.floor((min - span * 0.15) / step) * step)
    const hi = Math.ceil((max + span * 0.15) / step) * step

    // X positions are proportional to time between the first and last check.
    const times = points.map((p) => new Date(p.recorded_at).getTime())
    const t0 = times[0]
    const t1 = times[times.length - 1]
    const xs = times.map((t, i) => {
      if (points.length === 1) return PAD.left + innerW / 2
      if (t1 === t0) return PAD.left + (innerW * i) / (points.length - 1)
      return PAD.left + (innerW * (t - t0)) / (t1 - t0)
    })
    const y = (v: number) => PAD.top + innerH * (1 - (v - lo) / (hi - lo))
    const ys = prices.map(y)

    const line = points.map((_, i) => `${i ? 'L' : 'M'}${xs[i]},${ys[i]}`).join(' ')
    const baseline = PAD.top + innerH
    const area = `${line} L${xs[xs.length - 1]},${baseline} L${xs[0]},${baseline} Z`
    const yTicks: number[] = []
    for (let v = lo; v <= hi + step / 2; v += step) yTicks.push(v)

    // Within a single day, dates would repeat on both ends; show times instead.
    const sameDay = new Date(t0).toDateString() === new Date(t1).toDateString()
    const xLabel = (iso: string) => (sameDay ? formatTime(iso) : formatShortDate(iso))

    return { xs, ys, y, line, area, baseline, yTicks, xLabel, targetY: target === undefined ? null : y(target) }
  }, [points, width, target])

  // Snap the hover marker to the nearest point horizontally, at most once per
  // frame: mousemove can fire far more often than the screen can update.
  const onMove = (e: MouseEvent<SVGSVGElement>) => {
    if (!geo) return
    const mx = e.clientX - e.currentTarget.getBoundingClientRect().left
    cancelAnimationFrame(frame.current)
    frame.current = requestAnimationFrame(() => {
      let best = 0
      for (let i = 1; i < geo.xs.length; i++) {
        if (Math.abs(geo.xs[i] - mx) < Math.abs(geo.xs[best] - mx)) best = i
      }
      setHover((prev) => (prev === best ? prev : best)) // no re-render if unchanged
    })
  }

  if (points.length === 0) {
    return <div className="chart-empty">No price history yet. Log a price to start the chart.</div>
  }

  const hp = hover === null ? null : points[hover]
  const isLow = (p: PricePoint) => lowest != null && p.price === lowest && p.source !== 'manual'

  return (
    <div className="chart" ref={wrapRef} style={{ minHeight: HEIGHT }}>
      {geo && (
        <svg
          width={width}
          height={HEIGHT}
          role="img"
          aria-label={`Price history with ${points.length} data points`}
          onMouseMove={onMove}
          onMouseLeave={() => setHover(null)}
        >
          <defs>
            <linearGradient id={gradientId} x1="0" x2="0" y1="0" y2="1">
              <stop offset="0%" stopColor="var(--accent)" stopOpacity="0.25" />
              <stop offset="100%" stopColor="var(--accent)" stopOpacity="0" />
            </linearGradient>
          </defs>

          {/* Horizontal grid lines with price labels. */}
          {geo.yTicks.map((v) => (
            <g key={v}>
              <line className="chart__grid" x1={PAD.left} x2={width - PAD.right} y1={geo.y(v)} y2={geo.y(v)} />
              <text className="chart__label" x={PAD.left - 10} y={geo.y(v)} textAnchor="end" dominantBaseline="middle">
                {formatMoney(v, currency)}
              </text>
            </g>
          ))}

          {/* First and last check on the x axis. */}
          <text className="chart__label" x={PAD.left} y={HEIGHT - 8} textAnchor="start">
            {geo.xLabel(points[0].recorded_at)}
          </text>
          {points.length > 1 && (
            <text className="chart__label" x={width - PAD.right} y={HEIGHT - 8} textAnchor="end">
              {geo.xLabel(points[points.length - 1].recorded_at)}
            </text>
          )}

          {/* The user's target price, as a dashed line. */}
          {geo.targetY !== null && target !== undefined && (
            <>
              <line className="chart__target" x1={PAD.left} x2={width - PAD.right} y1={geo.targetY} y2={geo.targetY} />
              <text className="chart__target-label" x={width - PAD.right - 4} y={geo.targetY - 6} textAnchor="end">
                Target {formatMoney(target, currency)}
              </text>
            </>
          )}

          {/* Shaded area under the line, then the line, then one dot per check. */}
          <path d={geo.area} fill={`url(#${gradientId})`} />
          <path className="chart__line" d={geo.line} />
          {points.map((p, i) => (
            <circle
              key={p.id}
              className={`chart__dot ${p.source === 'manual' ? 'chart__dot--manual' : ''} ${isLow(p) ? 'chart__dot--low' : ''}`}
              cx={geo.xs[i]}
              cy={geo.ys[i]}
              r={hover === i ? 5 : isLow(p) ? 4 : 3}
            />
          ))}

          {/* Vertical cursor line at the hovered point. */}
          {hover !== null && (
            <line className="chart__cursor" x1={geo.xs[hover]} x2={geo.xs[hover]} y1={PAD.top} y2={geo.baseline} />
          )}
        </svg>
      )}

      {/* Tooltip is HTML (not SVG) so it can wrap and sit above the chart. */}
      {geo && hp && hover !== null && (
        <div
          className="chart__tooltip"
          style={{ left: Math.min(Math.max(geo.xs[hover], 90), width - 90), top: geo.ys[hover] - 12 }}
        >
          <strong>{formatMoney(hp.price, currency)}</strong>
          <span>{formatDateTime(hp.recorded_at)}</span>
          {hp.source === 'manual' && <span>Your entry</span>}
          {isLow(hp) && <span>Lowest price</span>}
        </div>
      )}
    </div>
  )
}

// niceStep rounds a raw step up to 1, 2 or 5 times a power of ten, so axis
// ticks land on round numbers.
function niceStep(raw: number): number {
  const pow = 10 ** Math.floor(Math.log10(raw))
  const f = raw / pow
  return (f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10) * pow
}
