/**
 * The status page as served on a customer's own domain (status.clientbrand.com; docs/plan-status-page-domains.md). A separate entry (status.html)
 * with nothing of the app in it: no router, no sign-in, no analytics, no cookies. The server finds the page from the address the visitor typed, so
 * this script never names one.
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/inter'
import '@fontsource-variable/sora'
import './daisy.css'
import './index.css'
import type { PublicStatus } from './api'
import { StatusView } from './pages/StatusView'
import { StatusNotFound } from './statusLoad'

async function loadDomainStatus(): Promise<PublicStatus> {
  const res = await fetch('/api/v1/public/domain-status', { credentials: 'omit', headers: { Accept: 'application/json' } })
  if (res.status === 404) throw new StatusNotFound()
  if (!res.ok) throw new Error(`status ${res.status}`)
  return (await res.json()) as PublicStatus
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <StatusView load={loadDomainStatus} />
  </StrictMode>,
)
