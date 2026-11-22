import { useState, type SubmitEvent } from 'react'
import { api, ApiError, type Organization } from '../api'
import { useAuth } from '../auth'
import { Field } from './Field'

/** A new organization is named after the sign-up address ("freddy's organization", internal/auth); this tells whether it still is. */
function isDefaultName(name: string, email: string | undefined): boolean {
  const local = (email ?? '').split('@')[0]
  return local !== '' && name === `${local}'s organization`
}

/**
 * The organization's name, on the Team page. The owner can change it (PATCH /organizations/:id, recorded in the security log); members see it.
 * Teammates see the name in invitations and in the organization menu, so a new organization, still named after its owner's address, gets a nudge
 * to name it after the company or team.
 */
export function OrganizationName() {
  const { organization } = useAuth()
  // The form starts from the organization's name, so it waits until that's known (and starts again for another organization).
  return organization ? <NameForm key={organization.id} organization={organization} /> : null
}

function NameForm({ organization }: { organization: Organization }) {
  const { user, refresh } = useAuth()
  const [name, setName] = useState(organization.name)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState(false)
  const [busy, setBusy] = useState(false)
  const isOwner = organization.role === 'owner'
  const changed = name.trim() !== organization.name

  async function save(e: SubmitEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setSaved(false)
    try {
      await api('PATCH', `/organizations/${organization.id}`, { name })
      await refresh() // the organization menu and every page show the new name
      setSaved(true)
    } catch (err) {
      setError(err instanceof ApiError ? (err.fields?.name ?? err.message) : 'Could not reach the server. Try again.')
    } finally {
      setBusy(false)
    }
  }

  if (!isOwner) {
    return <p>You’re a member of <strong>{organization.name}</strong>. Its owner can change the name.</p>
  }
  return (
    <section className="card narrow-left" aria-labelledby="org-name-h">
      <h2 id="org-name-h">Organization name</h2>
      <form onSubmit={save}>
        <Field label="Name" required maxLength={100} value={name} error={error ?? undefined}
          onChange={(e) => { setName(e.target.value); setSaved(false) }}
          hint={isDefaultName(organization.name, user?.email)
            ? 'Name it after your company or team: teammates see it in their invitation and in the organization menu.'
            : 'Teammates see it in their invitation and in the organization menu.'} />
        <div className="row">
          <button type="submit" disabled={busy || !changed || name.trim() === ''}>Save name</button>
          {saved && <span className="hint" role="status">Saved.</span>}
        </div>
      </form>
    </section>
  )
}
