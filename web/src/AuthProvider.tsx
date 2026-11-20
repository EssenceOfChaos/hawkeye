import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { api, ApiError, ORGANIZATION_LOST_EVENT, setApiOrganization, type Organization, type TwoFactorStatus, type User } from './api'
import { analyticsEnabled, trackEvent, trackRegistration } from './analytics'
import { attributionForSignup, forgetArrival } from './attribution'
import { AuthContext, type AuthState } from './auth'

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
  const [twoFactor, setTwoFactor] = useState<TwoFactorStatus | null>(null)

  const refresh = useCallback(async (select?: string) => {
    if (select) storeOrganization(select)
    try {
      const r = await api<{ user: User; organizations: Organization[]; two_factor?: TwoFactorStatus | null }>('GET', '/auth/me')
      // The header is set before anything is rendered for the new state, so no page ever fetches for the wrong organization.
      const active = pickOrganization(r.organizations, storedOrganization())
      actFor(r.organizations, active)
      setUser(r.user)
      setTwoFactor(r.two_factor ?? null)
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
    // oxlint-disable-next-line react/set-state-in-effect -- refresh() sets state only after its request returns, never synchronously
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
      twoFactor,
      login: async (email, password) => {
        const r = await api<{ two_factor_required?: boolean; challenge?: string }>('POST', '/auth/login', { email, password })
        if (r?.two_factor_required && r.challenge) return { challenge: r.challenge }
        await refresh()
        return {}
      },
      completeTwoFactor: async (challenge, code) => {
        const r = await api<{ used_recovery_code?: boolean }>('POST', '/auth/login/two-factor', { challenge, code })
        await refresh()
        return { usedRecoveryCode: Boolean(r?.used_recovery_code) }
      },
      register: async (email, password) => {
        // How this browser first found HawkEye, recorded with the account (nothing while analytics is off).
        const r = await api<{ conversion_event_id?: string }>('POST', '/auth/register', { email, password, attribution: attributionForSignup(analyticsEnabled()) })
        forgetArrival()
        trackEvent('sign_up', { method: 'email' })
        trackRegistration(r?.conversion_event_id)
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
    [status, user, organizations, organization, twoFactor, refresh],
  )
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}
