import type {
  AdminStats,
  AdminUser,
  AlertStatus,
  AppSettings,
  Limits,
  Me,
  NewProduct,
  Overrides,
  PricePoint,
  Product,
  LookupResult,
  ProductPatch,
  Role,
} from './types'

// Base URL of the Go API. Override with VITE_API_URL in .env.local.
export const API_URL = (import.meta.env.VITE_API_URL ?? 'http://localhost:8088').replace(/\/$/, '')

// ApiError carries the HTTP status so pages can react to e.g. 404 differently.
// status 0 means the request never reached the server (it's down, or CORS failed).
export class ApiError extends Error {
  constructor(message: string, public status: number) {
    super(message)
  }
}

// onUnauthorized is called whenever the API answers 401 (not logged in, or
// the session expired), so the app can send the user to the login page.
let onUnauthorized: () => void = () => {}
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

// request is the single place that talks to the network: it sets JSON headers,
// sends the session cookie, turns the API's {"error": "..."} bodies into
// ApiError, and handles 204.
async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(API_URL + path, {
      ...init,
      // The API is on another port (origin), so cookies are only sent if we
      // ask for it explicitly; the session cookie is how the API knows who we are.
      credentials: 'include',
      headers: { 'Content-Type': 'application/json', ...init.headers },
    })
  } catch {
    throw new ApiError(`Can't reach the API at ${API_URL}. Is the Go server running?`, 0)
  }

  if (res.status === 204) return undefined as T

  const body = await res.json().catch(() => null)
  if (!res.ok) {
    // /auth/me returning 401 just means "not logged in yet"; don't treat that
    // as an expired session.
    if (res.status === 401 && path !== '/auth/me') onUnauthorized()
    throw new ApiError(body?.error ?? `Request failed (${res.status})`, res.status)
  }
  return body as T
}

const json = (method: string, data: unknown): RequestInit => ({ method, body: JSON.stringify(data) })

// Where the "Continue with Google" button points: a full-page navigation (not
// fetch), because the user has to visit Google's sign-in page. `next` is the
// app page to return to afterwards.
export const googleLoginURL = (next?: string | null) =>
  `${API_URL}/auth/google/start` + (next ? `?next=${encodeURIComponent(next)}` : '')

export const api = {
  health: () => request<{ status: string; database: string }>('/health'),
  authConfig: () => request<{ google: boolean }>('/auth/config'),
  me: () => request<Me>('/auth/me'),
  login: (email: string, password: string) => request<Me>('/auth/login', json('POST', { email, password })),
  register: (email: string, password: string) => request<Me>('/auth/register', json('POST', { email, password })),
  logout: () => request<void>('/auth/logout', { method: 'POST' }),
  listProducts: () => request<Product[]>('/products'),
  getProduct: (id: number) => request<Product>(`/products/${id}`),
  createProduct: (input: NewProduct) => request<Product>('/products', json('POST', input)),
  updateProduct: (id: number, patch: ProductPatch) => request<Product>(`/products/${id}`, json('PATCH', patch)),
  deleteProduct: (id: number) => request<void>(`/products/${id}`, { method: 'DELETE' }),
  history: (id: number) => request<PricePoint[]>(`/products/${id}/history`),
  recordPrice: (id: number, price: number) => request<PricePoint>(`/products/${id}/prices`, json('POST', { price })),
  // Scrapes the store page now; resolves with the updated product even when the
  // scrape fails (the reason is in product.last_error).
  checkNow: (id: number) => request<Product>(`/products/${id}/check`, { method: 'POST' }),
  // Bucket list: check an item off (price defaults to the current price) or undo.
  markBought: (id: number, price?: number) =>
    request<Product>(`/products/${id}/bought`, json('POST', price ? { price } : {})),
  unmarkBought: (id: number) => request<Product>(`/products/${id}/bought`, { method: 'DELETE' }),
  alertStatus: () => request<AlertStatus>('/alerts'),

  // Public price history (works logged out, unless an admin disabled it).
  lookup: (url: string) => request<LookupResult>('/lookup', json('POST', { url })),
  listing: (id: number) => request<LookupResult>(`/listings/${id}`),

  // Admin dashboard (admins only; everyone else gets 403).
  adminStats: () => request<AdminStats>('/admin/stats'),
  adminSettings: () => request<AppSettings>('/admin/settings'),
  adminUpdateSettings: (s: {
    defaults: Limits
    signups_open: boolean
    public_lookups: boolean
    public_check_interval_minutes: number
  }) =>
    request<AppSettings>('/admin/settings', json('PUT', s)),
  adminUsers: () => request<AdminUser[]>('/admin/users'),
  adminUser: (id: number) => request<{ user: AdminUser; products: Product[] }>(`/admin/users/${id}`),
  adminUpdateUser: (id: number, u: { role: Role; disabled: boolean; overrides: Overrides }) =>
    request<AdminUser>(`/admin/users/${id}`, json('PUT', u)),
  adminDeleteUser: (id: number) => request<void>(`/admin/users/${id}`, { method: 'DELETE' }),
  sendTestAlert: () => request<{ sent_to: string[] }>('/alerts/test', { method: 'POST' }),
}
