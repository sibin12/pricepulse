// These mirror the JSON shapes produced by internal/domain/product.go.
// Keep them in sync when the Go structs change.

// Optional facts from the store page (null/'' = the store didn't publish it).
export interface ListingDetails {
  image_url: string
  brand: string
  rating: number | null
  best_rating: number | null
  rating_count: number | null
  mrp: number | null
  in_stock: boolean | null
}

export interface Product extends ListingDetails {
  id: number
  url: string
  title: string
  current_price: number // 0 means "no price recorded yet"
  target_price: number
  currency: string // ISO 4217 code detected by the scraper ("INR"); "" if unknown
  last_checked_at: string // ISO 8601 timestamp of the last recorded price
  created_at: string
  last_attempt_at: string | null // last scrape attempt (success or failure); null = never
  last_error: string // why the last scrape failed; "" if it succeeded
  alert_sent_at: string | null // when the "target hit" email went out; null = not (yet) sent
  // Bucket list:
  note: string // "why I want it"
  bought_at: string | null // null = still wishing
  bought_price: number | null
  // From the shared listing (store prices only):
  listing_id: number
  lowest_price: number | null
  lowest_price_dates: string[] // every day the lowest price was seen
  highest_price: number | null
}

export interface AlertStatus {
  enabled: boolean // false when the server has no SMTP settings
  recipients: string[]
}

export interface PricePoint {
  id: number
  price: number
  recorded_at: string
  source: 'scraper' | 'manual' // manual = typed in by you; private to you
}

// Listing is one product page, shared by everyone who tracks or looks it up.
export interface Listing extends ListingDetails {
  id: number
  url: string
  title: string
  currency: string
  current_price: number
  lowest_price: number | null
  lowest_price_dates: string[]
  highest_price: number | null
  last_checked_at: string | null
  last_attempt_at: string | null
  last_error: string
  trackers: number
  created_at: string
}

// LookupResult is what the public price-history endpoints return.
export interface LookupResult {
  listing: Listing
  history: PricePoint[]
  my_product_id: number | null // set when you already track it
}

export interface NewProduct {
  url: string
  title?: string
  note?: string
  target_price: number
  current_price?: number
}

export interface ProductPatch {
  title?: string
  note?: string
  target_price?: number
}

export type Role = 'user' | 'admin'

export interface User {
  id: number
  email: string
  role: Role
  created_at: string
}

// Limits mirror domain.Limits: what one user may do (defaults + overrides).
export interface Limits {
  max_products: number
  check_interval_minutes: number
  alerts_enabled: boolean
  manual_checks_enabled: boolean
}

// Me is GET /auth/me: the logged-in user plus their effective limits (global
// defaults with any admin overrides applied) and how many active wishes they have.
export interface Me extends User {
  limits: Limits
  product_count: number
}

// Overrides: null = "use the global default".
export interface Overrides {
  max_products: number | null
  check_interval_minutes: number | null
  alerts_enabled: boolean | null
  manual_checks_enabled: boolean | null
}

export interface AppSettings {
  defaults: Limits
  signups_open: boolean
  public_lookups: boolean
  public_check_interval_minutes: number
  updated_at: string
}

export interface AdminUser extends User {
  disabled: boolean
  has_password: boolean
  google_linked: boolean
  last_login_at: string | null
  product_count: number
  overrides: Overrides
  effective: Limits
}

export interface AdminStats {
  users: number
  admins: number
  disabled_users: number
  products: number
  listings: number
  failing_products: number
  checks_24h: number
  alerts_7d: number
}
