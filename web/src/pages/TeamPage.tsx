import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { api, ApiError, type Invitation, type Member, type Usage } from '../api'
import { Alert } from '../components/Alert'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { PageIcon } from '../components/Brand'
import { Field } from '../components/Field'

const day = (iso: string) => new Date(iso).toLocaleDateString([], { dateStyle: 'medium' })

export function TeamPage() {
  const { user, organization, organizations, refresh } = useAuth()
  const isOwner = organization?.role === 'owner'
  const orgId = organization?.id
  const verified = Boolean(user?.email_verified_at)

  const [members, setMembers] = useState<Member[] | null>(null)
  const [invitations, setInvitations] = useState<Invitation[]>([])
  const [usage, setUsage] = useState<Usage | null>(null)
  const [error, setError] = useState<ApiError | string | null>(null)
  const [fieldError, setFieldError] = useState<string | undefined>()
  const [notice, setNotice] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [address, setAddress] = useState('')

  const load = useCallback(async () => {
    if (!orgId) return
    try {
      const [m, u, i] = await Promise.all([
        api<{ members: Member[] }>('GET', `/organizations/${orgId}/members`),
        api<{ usage: Usage }>('GET', '/usage'),
        isOwner ? api<{ invitations: Invitation[] }>('GET', `/organizations/${orgId}/invitations`) : Promise.resolve({ invitations: [] as Invitation[] }),
      ])
      setMembers(m.members)
      setUsage(u.usage)
      setInvitations(i.invitations)
      setError(null)
    } catch (e) {
      setError(e instanceof ApiError ? e.message : 'Could not load your team.')
    }
  }, [orgId, isOwner])

  useEffect(() => {
    void load()
  }, [load])

  // Runs one change, then reloads. Plan-limit and other API errors are shown in full; anything else gets a generic line.
  async function run(action: () => Promise<unknown>, done?: string) {
    setError(null)
    setNotice(null)
    try {
      await action()
      if (done) setNotice(done)
      await load()
    } catch (e) {
      setError(e instanceof ApiError ? e : 'That did not work. Try again.')
    }
  }

  async function invite(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    setNotice(null)
    setFieldError(undefined)
    try {
      await api('POST', `/organizations/${orgId}/invitations`, { email: address })
      setNotice(`Invitation sent to ${address.trim().toLowerCase()}.`)
      setAddress('')
      await load()
    } catch (err) {
      if (err instanceof ApiError && err.fields.email) setFieldError(err.fields.email)
      else setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    } finally {
      setBusy(false)
    }
  }

  function remove(m: Member) {
    if (!window.confirm(`Remove ${m.email} from ${organization?.name}? They lose access straight away. Their own account is not affected.`)) return
    void run(() => api('DELETE', `/organizations/${orgId}/members/${m.user_id}`), `${m.email} was removed.`)
  }

  async function leave() {
    if (!window.confirm(`Leave ${organization?.name}? You will no longer see its websites. You can only come back if the owner invites you again.`)) return
    try {
      await api('POST', `/organizations/${orgId}/leave`)
      await refresh() // the list no longer has it, so the app falls back to the person's own organization
    } catch (err) {
      setError(err instanceof ApiError ? err : 'That did not work. Try again.')
    }
  }

  const seats = usage?.plan.max_team_members ?? 1
  const used = (members?.length ?? 0) + invitations.length
  const soloPlan = seats <= 1
  const full = used >= seats
  const upgrade = error instanceof ApiError && error.code === 'PLAN_LIMIT_REACHED'

  return (
    <>
      <div className="row">
        <PageIcon src={brand.icons.settings} />
        <h1>Team</h1>
      </div>
      <p>
        People you invite see this organization’s websites, incidents and uptime, and can add or change monitors. Only you, as the owner, manage billing, alert recipients, the
        status page and who is on the team.
      </p>

      {error && (
        <Alert>
          {typeof error === 'string' ? error : error.message} {upgrade && <Link to="/billing">See plans</Link>}
        </Alert>
      )}
      {notice && <Alert tone="success" role="status">{notice}</Alert>}
      {members === null && !error && <p role="status"><span className="d-loading d-loading-spinner d-loading-sm" aria-hidden="true" /> Loading…</p>}

      {members && (
        <>
          <p className="hint" role="status">
            {used} of {seats} seat{seats === 1 ? '' : 's'} used{usage ? ` on the ${usage.plan.name} plan` : ''}.
            {invitations.length > 0 && ' An invitation holds a seat until it is accepted or cancelled.'}
          </p>
          <div className="table-wrap">
            <table>
              <caption className="sr-only">People on this team</caption>
              <thead>
                <tr><th>Email</th><th>Role</th><th>Joined</th>{isOwner && <th>Actions</th>}</tr>
              </thead>
              <tbody>
                {members.map((m) => (
                  <tr key={m.user_id}>
                    <td>
                      {m.email}
                      {m.user_id === user?.id && <span className="hint"> (you)</span>}
                    </td>
                    <td>{m.role === 'owner' ? 'Owner' : 'Member'}</td>
                    <td>{day(m.created_at)}</td>
                    {isOwner && (
                      <td className="actions">
                        {m.role !== 'owner' && (
                          <button type="button" className="danger" onClick={() => remove(m)} aria-label={`Remove ${m.email}`}>Remove</button>
                        )}
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </>
      )}

      {isOwner && invitations.length > 0 && (
        <section aria-labelledby="pending-h">
          <h2 id="pending-h">Waiting to join</h2>
          <div className="table-wrap">
            <table>
              <caption className="sr-only">Invitations that have not been accepted yet</caption>
              <thead>
                <tr><th>Email</th><th>Sent</th><th>Link works until</th><th>Actions</th></tr>
              </thead>
              <tbody>
                {invitations.map((i) => (
                  <tr key={i.id}>
                    <td>{i.email}</td>
                    <td>{day(i.created_at)}</td>
                    <td>{day(i.expires_at)}</td>
                    <td className="actions">
                      <button type="button" onClick={() => void run(() => api('POST', `/organizations/${orgId}/invitations/${i.id}/resend`), `A new invitation was sent to ${i.email}.`)}>
                        Resend
                      </button>
                      <button type="button" className="danger" aria-label={`Cancel the invitation to ${i.email}`}
                        onClick={() => void run(() => api('DELETE', `/organizations/${orgId}/invitations/${i.id}`), `The invitation to ${i.email} was cancelled.`)}>
                        Cancel
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}

      {isOwner ? (
        soloPlan ? (
          <Alert tone="info" role="note">
            Your {usage?.plan.name ?? 'current'} plan is for one person. <Link to="/billing">Upgrade</Link> to invite your team.
          </Alert>
        ) : (
          <section className="card narrow-left" aria-labelledby="invite-h">
            <h2 id="invite-h">Invite someone</h2>
            <form onSubmit={invite}>
              <Field label="Email address" type="email" required value={address} onChange={(e) => setAddress(e.target.value)} error={fieldError}
                disabled={!verified || full}
                hint="They get an email with a link that works for 7 days. They sign in, or create a free account, with this address to join." />
              <button type="submit" disabled={busy || !verified || full}>Send invitation</button>
              {!verified && <p className="hint">Verify your own email address first, so invitations can be sent from your account.</p>}
              {verified && full && <p className="hint">All {seats} seats are taken. Remove someone or cancel an invitation to free one.</p>}
            </form>
          </section>
        )
      ) : (
        <>
          <p className="hint">You are a member of {organization?.name}. Only its owner can invite or remove people.</p>
          {organizations.length > 1 && <button type="button" className="danger" onClick={() => void leave()}>Leave {organization?.name}</button>}
        </>
      )}
    </>
  )
}
