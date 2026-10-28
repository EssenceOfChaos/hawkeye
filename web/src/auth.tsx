import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, ApiError, ORGANIZATION_LOST_EVENT, setApiOrganization, type Organization, type User } from './api'
import { trackEvent } from './analytics'

interface AuthState {
  status: 'loading' | 'anonymous' | 'authenticated'
  user: User | null
  /** Every organization the person belongs to: their own, and any team they were invited to. Oldest first. */
  organizations: Organization[]
  /** The one the app is showing right now, with the person's role in it. */
  organization: Organization | null
  /** Switch to another of `organizations`. Pages reload their data for it. */
  selectOrganization(id: string): void
  login(email: string, password: string): Promise<void>
  register(email: string, password: string): Promise<void>
  logout(): Promise<void>
  /** Reloads who is signed in and their organizations. `select` makes that organization the active one (one just joined is not in the list yet). */
  refresh(select?: string): Promise<void>
}

const Ctx = createContext<AuthState | null>(null)

const STORED_ORGANIZATION = 'hawkeye_organization'

// Browser storage can be missing or refuse writes (private windows, blocked site data): the choice is then simply not remembered.
function storedOrganization(): string | null {
  try {
    return window.localStorage.getItem(STORED_ORGANIZATION)
  } catch {
    return null
  }
}

function storeOrganization(id: string | null): void {
  try {
    if (id) window.localStorage.setItem(STORED_ORGANIZATION, id)
    else window.localStorage.removeItem(STORED_ORGANIZATION)
  } catch {
    // not remembered
  }
}

/** What the server acts for when no organization is named: the one the person owns (their own), else the oldest. Keep in step with resolveOrg. */
function serverDefault(list: Organization[]): Organization | null {
  return list.find((o) => o.role === 'owner') ?? list[0] ?? null
}

/** The remembered organization if the person still belongs to it, else the server's default. */
function pickOrganization(list: Organization[], wanted: string | null): Organization | null {
  return list.find((o) => o.id === wanted) ?? serverDefault(list)
}

/** Makes requests act for `org`. The server's default needs no header. */
function actFor(list: Organization[], org: Organization | null): void {
  setApiOrganization(org && org.id !== serverDefault(list)?.id ? org.id : null)
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<AuthState['status']>('loading')
  const [user, setUser] = useState<User | null>(null)
  const [organizations, setOrganizations] = useState<Organization[]>([])
  const [activeId, setActiveId] = useState<string | null>(storedOrganization)

  const refresh = useCallback(async (select?: string) => {
    if (select) storeOrganization(select)
    try {
      const r = await api<{ user: User; organizations: Organization[] }>('GET', '/auth/me')
      // The header is set before anything is rendered for the new state, so no page ever fetches for the wrong organization.
      const active = pickOrganization(r.organizations, storedOrganization())
      actFor(r.organizations, active)
      setUser(r.user)
      setOrganizations(r.organizations)
      setActiveId(active?.id ?? null)
      setStatus('authenticated')
    } catch (e) {
      if (!(e instanceof ApiError) || e.status !== 401) console.error(e)
      actFor([], null)
      setUser(null)
      setOrganizations([])
      setStatus('anonymous')
    }
  }, [])

  useEffect(() => {
    void refresh()
  }, [refresh])

  // Someone removed from a team while looking at it: the server stops answering for it, so reload the list and fall back to another organization.
  useEffect(() => {
    const lost = () => {
      storeOrganization(null)
      void refresh()
    }
    window.addEventListener(ORGANIZATION_LOST_EVENT, lost)
    return () => window.removeEventListener(ORGANIZATION_LOST_EVENT, lost)
  }, [refresh])

  const organization = useMemo(() => pickOrganization(organizations, activeId), [organizations, activeId])

  const value = useMemo<AuthState>(
    () => ({
      status,
      user,
      organizations,
      organization,
      refresh,
      selectOrganization: (id) => {
        const next = organizations.find((o) => o.id === id)
        if (!next) return
        storeOrganization(next.id)
        actFor(organizations, next)
        setActiveId(next.id)
      },
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
        storeOrganization(null)
        actFor([], null)
        setUser(null)
        setOrganizations([])
        setActiveId(null)
        setStatus('anonymous')
      },
    }),
    [status, user, organizations, organization, refresh],
  )
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export function useAuth(): AuthState {
  const v = useContext(Ctx)
  if (!v) throw new Error('useAuth must be used inside AuthProvider')
  return v
}
