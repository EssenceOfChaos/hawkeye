import { useEffect, useRef, useState } from 'react'
import { api, ApiError } from './api'

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
