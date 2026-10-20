import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, ApiError, type Organization, type User } from './api'
import { trackEvent } from './analytics'

interface AuthState {
  status: 'loading' | 'anonymous' | 'authenticated'
  user: User | null
  organizations: Organization[]
  login(email: string, password: string): Promise<void>
  register(email: string, password: string): Promise<void>
  logout(): Promise<void>
  refresh(): Promise<void>
}

const Ctx = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthState['status']>('loading')
  const [user, setUser] = useState<User | null>(null)
  const [organizations, setOrganizations] = useState<Organization[]>([])

  const refresh = useCallback(async () => {
    try {
      const r = await api<{ user: User; organizations: Organization[] }>('GET', '/auth/me')
      setUser(r.user)
      setOrganizations(r.organizations)
      setStatus('authenticated')
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 401) console.error(e)
      setUser(null)
      setOrganizations([])
      setStatus('anonymous')
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  const value = useMemo<AuthState>(
    () => ({
      status,
      user,
      organizations,
      refresh,
      login: async (email, password) => {
        await api('POST', '/auth/login', { email, password })
        await refresh()
      },
      register: async (email, password) => {
        await api('POST', '/auth/register', { email, password })
        trackEvent('sign_up', { method: 'email' })
        await refresh()
      },
      logout: async () => {
        await api('POST', '/auth/logout')
        setUser(null)
        setOrganizations([])
        setStatus('anonymous')
      },
    }),
    [status, user, organizations, refresh],
  )
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth must be used inside AuthProvider')
  return v
}
