import { useEffect, useRef, useState } from 'react'
import { api, ApiError, type Plan } from './api'

interface State<T> {
  data: T | null
  loading: boolean
  error: ApiError | null
}

/**
 * GETs `path` whenever it changes. The previous data stays in place while the next request is in flight
 * (`loading` is true), so charts dim instead of flashing to a skeleton. Pass null to skip.
 */
export function useApi<T>(path: string | null): State<T> {
  const [state, setState] = useState<State<T>>({ data: null, loading: path !== null, error: null })
  const seq = useRef(0)

  useEffect(() => {
    if (path === null) return
    const id = ++seq.current
    setState((s) => ({ ...s, loading: true }))
    api<T>('GET', path)
      .then((data) => {
        if (id === seq.current) setState({ data, loading: false, error: null })
      })
      .catch((e: unknown) => {
        if (id !== seq.current) return
        const error = e instanceof ApiError ? e : new ApiError(0, 'NETWORK', 'Could not reach the server.')
        setState((s) => ({ data: s.data, loading: false, error }))
      })
  }, [path])

  return state
}

/** The public plan list (null while loading, [] if it could not be loaded) and whether paid checkout is switched on. Shared by the landing pages. */
export function usePlans() {
  const [plans, setPlans] = useState<Plan[] | null>(null)
  const [billingEnabled, setBillingEnabled] = useState(false)
  useEffect(() => {
    let cancelled = false
    api<{ plans: Plan[]; billing_enabled?: boolean }>('GET', '/plans')
      .then((r) => {
        if (cancelled) return
        setPlans(r.plans)
        setBillingEnabled(Boolean(r.billing_enabled))
      })
      .catch(() => !cancelled && setPlans([]))
    return () => {
      cancelled = true
    }
  }, [])
  return { plans, billingEnabled }
}
