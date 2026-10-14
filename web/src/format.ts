// Formatting and small calculations shared by every page: money, dates,
// relative times, and the price maths behind "is this a good deal?".
// Keeping them here means the bucket list, product page and public history
// page always agree on what "lowest price" or "target hit" means.
import type { PricePoint, Product } from './types'

// Fallback currency for products whose store didn't state one. Override with VITE_CURRENCY.
const defaultCurrency = import.meta.env.VITE_CURRENCY ?? 'USD'
const formatters = new Map<string, Intl.NumberFormat>()

// formatMoney formats n in the product's own currency (as detected by the
// scraper), falling back to the default. Formatters are cached because
// creating Intl objects is relatively expensive.
export function formatMoney(n: number, currency?: string): string {
  const code = currency || defaultCurrency
  let f = formatters.get(code)
  if (!f) {
    try {
      f = new Intl.NumberFormat(undefined, { style: 'currency', currency: code })
    } catch {
      // Unknown code: Intl throws a RangeError, so fall back to the default.
      f = new Intl.NumberFormat(undefined, { style: 'currency', currency: defaultCurrency })
    }
    formatters.set(code, f)
  }
  return f.format(n)
}

// Date formatters are created once (they're expensive) and reused. `undefined`
// as the locale means "the user's browser language", so dates look local.
const dateTime = new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }) // 14 Oct 2026, 21:10
const shortDate = new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric' }) // 14 Oct
const timeOnly = new Intl.DateTimeFormat(undefined, { timeStyle: 'short' }) // 21:10

export const formatDateTime = (iso: string) => dateTime.format(new Date(iso))
export const formatShortDate = (iso: string) => shortDate.format(new Date(iso))
export const formatTime = (iso: string) => timeOnly.format(new Date(iso))

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

// relativeTime turns a timestamp into "5 minutes ago", "yesterday", etc.
export function relativeTime(iso: string): string {
  const seconds = (new Date(iso).getTime() - Date.now()) / 1000
  const units: [Intl.RelativeTimeFormatUnit, number][] = [
    ['year', 31536000],
    ['month', 2592000],
    ['day', 86400],
    ['hour', 3600],
    ['minute', 60],
  ]
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) return rtf.format(Math.round(seconds / size), unit)
  }
  return 'just now'
}

// hostOf shows a compact store name: "https://www.example.com/x" -> "example.com".
export function hostOf(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return url
  }
}

// A product's status relative to the user's target price:
//   unchecked = no price known yet, hit = at/below target, above = still waiting.
export type StatusKind = 'unchecked' | 'hit' | 'above'

export interface PriceStatus {
  kind: StatusKind
  label: string
  gapPct: number // how far above target, in %; 0 when at/below target
  progress: number // 0..100, how close the price is to the target
}

// priceStatus is the single source of truth for "is this a deal yet?".
export function priceStatus(p: Product): PriceStatus {
  if (p.current_price <= 0) {
    return { kind: 'unchecked', label: 'Awaiting price', gapPct: 0, progress: 0 }
  }
  if (p.current_price <= p.target_price) {
    return { kind: 'hit', label: 'Target hit', gapPct: 0, progress: 100 }
  }
  const gapPct = ((p.current_price - p.target_price) / p.target_price) * 100
  return {
    kind: 'above',
    label: `${gapPct.toFixed(gapPct < 10 ? 1 : 0)}% above target`,
    gapPct,
    progress: (p.target_price / p.current_price) * 100,
  }
}

// parsePrice reads a form input as a positive number, or returns null.
export function parsePrice(raw: string): number | null {
  const n = Number(raw.trim())
  return raw.trim() !== '' && Number.isFinite(n) && n > 0 ? Math.round(n * 100) / 100 : null
}

// formatInterval turns minutes into "every 30 minutes", "every 6 hours", "every day".
export function formatInterval(minutes: number): string {
  if (minutes % 1440 === 0) return minutes === 1440 ? 'every day' : `every ${minutes / 1440} days`
  if (minutes % 60 === 0) return minutes === 60 ? 'every hour' : `every ${minutes / 60} hours`
  return `every ${minutes} minutes`
}

const dateOnly = new Intl.DateTimeFormat(undefined, { day: 'numeric', month: 'short', year: 'numeric' }) // 14 Oct 2026
export const formatDate = (iso: string) => dateOnly.format(new Date(iso))

export interface PriceStats {
  lowest: number
  lowestDates: string[] // first sighting on each day the lowest price was seen
  highest: number
  average: number
  count: number // number of store price checks
  since: string // first check
}

// priceStats summarises store prices (manual entries are ignored, since they
// are one person's observation). `saved` lets callers use the lowest price and
// dates stored on the server, which is the source of truth.
export function priceStats(
  points: PricePoint[],
  saved?: { lowest: number | null; dates: string[]; highest: number | null },
): PriceStats | null {
  const store = points.filter((p) => p.source !== 'manual')
  if (store.length === 0) return null
  const prices = store.map((p) => p.price)
  const lowest = saved?.lowest ?? Math.min(...prices)
  // Prefer the server's list of lowest-price days; otherwise work it out from
  // the points (one entry per day the lowest price was seen).
  let lowestDates = saved?.lowest != null ? saved.dates : []
  if (lowestDates.length === 0) {
    const seen = new Set<string>()
    for (const p of store) {
      const day = new Date(p.recorded_at).toDateString()
      if (p.price === lowest && !seen.has(day)) {
        seen.add(day)
        lowestDates.push(p.recorded_at)
      }
    }
  }
  return {
    lowest,
    lowestDates,
    highest: saved?.highest ?? Math.max(...prices),
    average: prices.reduce((a, b) => a + b, 0) / prices.length,
    count: store.length,
    since: store[0].recorded_at,
  }
}

export type VerdictKind = 'best' | 'good' | 'fair' | 'high' | 'unknown'

export interface Verdict {
  kind: VerdictKind
  label: string
  detail: string
}

// dealVerdict answers "is now a good time to buy?" by comparing the current
// price with the lowest and the average store price.
export function dealVerdict(current: number, stats: PriceStats | null, currency?: string): Verdict {
  if (!stats || current <= 0 || stats.count < 3) {
    return {
      kind: 'unknown',
      label: 'Not enough history yet',
      detail: 'A few more price checks are needed to judge this price.',
    }
  }
  // How far the current price sits from the average, in percent (negative = cheaper).
  const vsAvg = ((current - stats.average) / stats.average) * 100
  const avg = formatMoney(stats.average, currency)
  if (current <= stats.lowest) {
    return { kind: 'best', label: 'Lowest price ever', detail: `The best price seen since ${formatDate(stats.since)}.` }
  }
  if (vsAvg <= -3) {
    return { kind: 'good', label: 'Good price', detail: `${Math.abs(vsAvg).toFixed(0)}% below the average of ${avg}.` }
  }
  if (vsAvg >= 3) {
    return { kind: 'high', label: 'Consider waiting', detail: `${vsAvg.toFixed(0)}% above the average of ${avg}.` }
  }
  return { kind: 'fair', label: 'Usual price', detail: `Close to the average of ${avg}.` }
}

// discountPct is how far below the MRP the price is, e.g. 41 for 41% off.
export function discountPct(price: number, mrp: number | null): number | null {
  if (!mrp || price <= 0 || mrp <= price) return null
  return Math.round(((mrp - price) / mrp) * 100)
}

export interface PriceDrop {
  at: string // when the lower price was recorded
  from: number // price before
  to: number // price after
  pct: number // size of the drop, in percent of the old price
}

// priceDrops lists every time the store price went down, newest first.
export function priceDrops(points: PricePoint[]): PriceDrop[] {
  const store = points.filter((p) => p.source !== 'manual')
  const drops: PriceDrop[] = []
  for (let i = 1; i < store.length; i++) {
    const from = store[i - 1].price
    const to = store[i].price
    if (to < from) drops.push({ at: store[i].recorded_at, from, to, pct: ((from - to) / from) * 100 })
  }
  return drops.reverse()
}

export type MeterZone = 'skip' | 'wait' | 'okay' | 'yes'

// buyMeter places the current price between the highest (0 = skip) and the
// lowest (1 = yes) store price seen, like the "Should you buy?" gauge on
// price-history sites. null when there isn't enough history to judge.
export function buyMeter(current: number, stats: PriceStats | null): { position: number; zone: MeterZone } | null {
  if (!stats || stats.count < 3 || stats.highest <= stats.lowest || current <= 0) return null
  const position = Math.min(1, Math.max(0, (stats.highest - current) / (stats.highest - stats.lowest)))
  const zone: MeterZone = position >= 0.75 ? 'yes' : position >= 0.5 ? 'okay' : position >= 0.25 ? 'wait' : 'skip'
  return { position, zone }
}

// savings is how much a bought item cost below its MRP (or, without an MRP,
// below the highest price we saw). null if we can't tell.
export function savings(p: Product): { amount: number; versus: 'MRP' | 'highest price' } | null {
  if (p.bought_price == null) return null
  if (p.mrp != null && p.mrp > p.bought_price) return { amount: p.mrp - p.bought_price, versus: 'MRP' }
  if (p.highest_price != null && p.highest_price > p.bought_price) {
    return { amount: p.highest_price - p.bought_price, versus: 'highest price' }
  }
  return null
}
