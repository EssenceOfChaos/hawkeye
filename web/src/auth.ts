import { createContext, useContext } from 'react'
import type { Organization, TwoFactorStatus, User } from './api'

export interface AuthState {
  status: 'loading' | 'anonymous' | 'authenticated'
  user: User | null
  /** Every organization the person belongs to: their own, and any team they were invited to. Oldest first. */
  organizations: Organization[]
  /** The one the app is showing right now, with the person's role in it. */
  organization: Organization | null
  /** Switch to another of `organizations`. Pages reload their data for it. */
  selectOrganization(id: string): void
  /** Whether the person uses two-factor sign-in (null while unknown, or when the server doesn't offer it). */
  twoFactor: TwoFactorStatus | null
  /** Signs in with a password. For an account with two-factor sign-in it returns the challenge to pass to completeTwoFactor; no session exists yet. */
  login(email: string, password: string): Promise<{ challenge?: string }>
  /** Finishes a two-factor sign-in with a code or a recovery code. Reports whether a recovery code was used. */
  completeTwoFactor(challenge: string, code: string): Promise<{ usedRecoveryCode: boolean }>
  register(email: string, password: string): Promise<void>
  logout(): Promise<void>
  /** Reloads who is signed in and their organizations. `select` makes that organization the active one (one just joined is not in the list yet). */
  refresh(select?: string): Promise<void>
}

/** Provided by AuthProvider (AuthProvider.tsx), which is kept in its own file so this module and that one each refresh cleanly in development. */
export const AuthContext = createContext<AuthState | null>(null)

export function useAuth(): AuthState {
  const v = useContext(AuthContext)
  if (!v) throw new Error('useAuth must be used inside AuthProvider')
  return v
}
