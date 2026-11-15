import { useCallback } from 'react'
import { useParams } from 'react-router'
import { api, ApiError, type PublicStatus } from '../api'
import { goTo, StatusNotFound } from '../statusLoad'
import { StatusView } from './StatusView'

/** Sends visitors to the page's own domain when its owner chose that; otherwise the page is shown here. */
function leaveForDomain(d: PublicStatus): boolean {
  if (d.redirect_to_domain && d.custom_domain_url?.startsWith('https://')) {
    goTo.url(`${d.custom_domain_url}/`)
    return false
  }
  return true
}

/** The public, no-login status page for /status/<slug>. */
export function StatusPage() {
  const { slug = '' } = useParams()
  const load = useCallback(
    () =>
      api<PublicStatus>('GET', `/public/status/${encodeURIComponent(slug)}`).catch((e) => {
        throw e instanceof ApiError && e.status === 404 ? new StatusNotFound() : e
      }),
    [slug],
  )
  return <StatusView load={load} onLoaded={leaveForDomain} />
}
