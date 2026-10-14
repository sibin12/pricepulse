import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, ApiError, setUnauthorizedHandler } from './api'
import type { Me } from './types'

// AuthState is what every page can read through useAuth().
interface AuthState {
  user: Me | null | undefined // undefined = still checking the session; null = logged out
  refresh: () => Promise<void> // re-load limits/product count (e.g. after adding a product)
  login: (email: string, password: string) => Promise<void>
  register: (email: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

// AuthProvider loads the current session once at startup and shares the user
// with every page. The session itself lives in an HttpOnly cookie that
// JavaScript can't read; we only learn who we are by asking GET /auth/me.
export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<Me | null | undefined>(undefined)

  useEffect(() => {
    // Any 401 from the API (e.g. the session expired) logs the UI out.
    setUnauthorizedHandler(() => setUser(null))
    api.me().then(setUser, (err) => {
      // 401 = simply not logged in. For other errors (API down) we also show
      // the login page; its requests will surface the real error.
      if (!(err instanceof ApiError) || err.status === 401 || err.status === 0) setUser(null)
    })
  }, [])

  // useCallback keeps these functions stable between renders, so components
  // that list them as effect dependencies don't re-run needlessly.
  const login = useCallback(async (email: string, password: string) => setUser(await api.login(email, password)), [])
  const register = useCallback(async (email: string, password: string) => setUser(await api.register(email, password)), [])
  const refresh = useCallback(async () => {
    try {
      setUser(await api.me())
    } catch {
      // a 401 already logged us out via the unauthorized handler
    }
  }, [])
  const logout = useCallback(async () => {
    try {
      await api.logout()
    } finally {
      setUser(null)
    }
  }, [])

  // Memoize the context value: a fresh object on every render would make
  // every component that calls useAuth() re-render each time, even when
  // nothing about the user changed.
  const value = useMemo(() => ({ user, refresh, login, register, logout }), [user, refresh, login, register, logout])

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

// useAuth gives any component the logged-in user and the auth actions.
export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside <AuthProvider>')
  return ctx
}
