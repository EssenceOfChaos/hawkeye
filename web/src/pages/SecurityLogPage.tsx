import { Link } from 'react-router'
import { useAuth } from '../auth'
import { SecurityLog } from '../components/SecurityLog'

/** The organization's security log, for its owner: who changed what, when, from where, and with which key or connected app. */
export function SecurityLogPage() {
  const { organization } = useAuth()
  if (!organization) return null // still loading who is signed in
  if (organization.role !== 'owner') {
    return (
      <>
        <h1>Security log</h1>
        <p>Only the owner of {organization.name} can see its security log. Your own sign-ins and account changes are on your <Link to="/account#activity-h">account page</Link>.</p>
      </>
    )
  }
  return (
    <>
      <h1>Security log</h1>
      <p>
        Every change to {organization.name}’s monitors, status pages, maintenance windows, incident updates, alert recipients, API keys,
        connected apps and team: who made it, when, and from where. Changes made with an API key or through Claude name the key or app that was used.
      </p>
      <p className="hint">
        Sign-ins and password changes are personal: each person sees their own on their <Link to="/account#activity-h">account page</Link>.
      </p>
      <SecurityLog path="/security-log" caption={`Security log for ${organization.name}`} />
    </>
  )
}
