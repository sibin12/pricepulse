import type { ReactNode } from 'react'
import { BrowserRouter, Link, Navigate, NavLink, Route, Routes, useLocation } from 'react-router-dom'
import { AuthProvider, useAuth } from './auth'
import { ApiStatus } from './components/ApiStatus'
import { AddProduct } from './pages/AddProduct'
import { AdminDashboard } from './pages/admin/AdminDashboard'
import { AdminUserPage } from './pages/admin/AdminUserPage'
import { Alerts } from './pages/Alerts'
import { AuthPage } from './pages/AuthPage'
import { ListingPage, PriceLookup } from './pages/PriceHistory'
import { BucketList } from './pages/BucketList'
import { NotFound } from './pages/NotFound'
import { ProductDetail } from './pages/ProductDetail'

// App defines the page shell (header + main) and the client-side routes:
//   /login, /register  public sign-in / sign-up pages
//   /history           public price-history lookup (no login needed)
//   /history/:id       one product's public price history (shareable)
//   /                  the user's bucket list (tracked products)
//   /products/new      add a wish
//   /products/:id      one product: chart, history, edit, delete
//   /alerts            email alert setup and test
//   /admin             admin dashboard: stats, global settings, users (admins only)
//   /admin/users/:id   edit one user's role, status and limits (admins only)
export default function App() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Shell />
      </AuthProvider>
    </BrowserRouter>
  )
}

function Shell() {
  const { user } = useAuth()
  // undefined = the session check (GET /auth/me) hasn't answered yet. The
  // header is drawn straight away with a progress line, and the page body
  // waits: a logged-in user must not see the login page flash on reload.
  const checking = user === undefined

  return (
    <>
      <header className="topbar">
        {checking && <div className="topbar__loading" aria-hidden />}
        <div className="topbar__inner">
          <Link to="/" className="brand">
            <svg viewBox="0 0 32 32" width="28" height="28" aria-hidden>
              <rect width="32" height="32" rx="8" fill="var(--accent)" />
              <path d="M5 18h5l3-8 5 14 3-6h6" fill="none" stroke="#fff" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" />
            </svg>
            PricePulse
          </Link>
          {user ? (
            <>
              <nav className="nav">
                <NavLink to="/" end>
                  Bucket list
                </NavLink>
                <NavLink to="/products/new">Add a wish</NavLink>
                <NavLink to="/history">Price history</NavLink>
                <NavLink to="/alerts">Alerts</NavLink>
                {user.role === 'admin' && <NavLink to="/admin">Admin</NavLink>}
              </nav>
              <ApiStatus />
              <UserMenu />
            </>
          ) : (
            <>
              <nav className="nav">
                <NavLink to="/history">Price history</NavLink>
              </nav>
              <ApiStatus />
              <Link to="/login" className="btn btn--small">
                Log in
              </Link>
            </>
          )}
        </div>
      </header>

      <main className="page">
        {checking ? null : (
        <Routes>
          <Route path="/login" element={<PublicOnly><AuthPage mode="login" /></PublicOnly>} />
          <Route path="/register" element={<PublicOnly><AuthPage mode="register" /></PublicOnly>} />
          <Route path="/history" element={<PriceLookup />} />
          <Route path="/history/:id" element={<ListingPage />} />
          <Route path="/" element={<RequireAuth><BucketList /></RequireAuth>} />
          <Route path="/products/new" element={<RequireAuth><AddProduct /></RequireAuth>} />
          <Route path="/products/:id" element={<RequireAuth><ProductDetail /></RequireAuth>} />
          <Route path="/alerts" element={<RequireAuth><Alerts /></RequireAuth>} />
          <Route path="/admin" element={<RequireAdmin><AdminDashboard /></RequireAdmin>} />
          <Route path="/admin/users/:id" element={<RequireAdmin><AdminUserPage /></RequireAdmin>} />
          <Route path="*" element={<NotFound />} />
        </Routes>
        )}
      </main>
    </>
  )
}

// RequireAuth sends logged-out visitors to /login, remembering where they were
// going (?next=) so they land there after logging in.
function RequireAuth({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const location = useLocation()
  if (!user) {
    const next = location.pathname + location.search
    // Logged-out visitors to the home page get the public price-history page.
    return <Navigate to={next === '/' ? '/history' : `/login?next=${encodeURIComponent(next)}`} replace />
  }
  return <>{children}</>
}

// RequireAdmin is RequireAuth plus a role check. This only hides the pages;
// the API independently rejects admin requests from non-admins (403).
function RequireAdmin({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  if (user && user.role !== 'admin') return <NotFound />
  return <RequireAuth>{children}</RequireAuth>
}

// PublicOnly keeps logged-in users away from the login/sign-up pages (sending
// them where they were headed, if a ?next= was given).
function PublicOnly({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const next = new URLSearchParams(useLocation().search).get('next')
  return user ? <Navigate to={next && next.startsWith('/') && !next.startsWith('//') ? next : '/'} replace /> : <>{children}</>
}

function UserMenu() {
  const { user, logout } = useAuth()
  return (
    <div className="user-menu">
      <span className="user-menu__email" title={user?.email}>
        {user?.email}
      </span>
      <button className="btn btn--small" onClick={logout}>
        Log out
      </button>
    </div>
  )
}
