import { render, screen } from '@testing-library/react'
import { Suspense } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ErrorBoundary } from './components/ErrorBoundary'
import { lazyWithReload, reloadForNewBuild } from './chunkReload'

const reload = vi.fn()
beforeEach(() => {
  sessionStorage.clear()
  reload.mockClear()
  vi.stubGlobal('location', { ...window.location, reload })
})
afterEach(() => vi.unstubAllGlobals())

describe('reloading for a new build', () => {
  it('reloads once, and not again within 30 seconds', () => {
    let t = 1_000_000
    expect(reloadForNewBuild(() => t)).toBe(true)
    t += 10_000
    expect(reloadForNewBuild(() => t)).toBe(false)
    expect(reload).toHaveBeenCalledTimes(1)
    t += 25_000 // 35 s after the first
    expect(reloadForNewBuild(() => t)).toBe(true)
    expect(reload).toHaveBeenCalledTimes(2)
  })

  it('never reloads when it cannot remember that it already did (no endless loop)', () => {
    const blocked = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {
      throw new Error('storage blocked')
    })
    expect(reloadForNewBuild()).toBe(false)
    expect(reload).not.toHaveBeenCalled()
    blocked.mockRestore()
  })

  it('a lazy page whose file is gone triggers one reload and keeps showing "Loading…"', async () => {
    const Gone = lazyWithReload(() => Promise.reject(new TypeError('Failed to fetch dynamically imported module')))
    render(<Suspense fallback={<p>Loading…</p>}><Gone /></Suspense>)
    expect(screen.getByText('Loading…')).toBeInTheDocument()
    await vi.waitFor(() => expect(reload).toHaveBeenCalledTimes(1))
    expect(screen.getByText('Loading…')).toBeInTheDocument()
  })

  it('a lazy page that fails again right after a reload reaches the error screen instead of looping', async () => {
    sessionStorage.setItem('hawkeye:reloaded-for-new-build-at', String(Date.now()))
    const Gone = lazyWithReload(() => Promise.reject(new TypeError('Failed to fetch dynamically imported module')))
    vi.spyOn(console, 'error').mockImplementation(() => {})
    render(<ErrorBoundary><Suspense fallback={<p>Loading…</p>}><Gone /></Suspense></ErrorBoundary>)
    expect(await screen.findByRole('alert')).toHaveTextContent('Something went wrong')
    expect(reload).not.toHaveBeenCalled()
  })

  it('a lazy page that loads is rendered normally', async () => {
    const Fine = lazyWithReload(() => Promise.resolve({ default: () => <p>dashboard</p> }))
    render(<Suspense fallback={<p>Loading…</p>}><Fine /></Suspense>)
    expect(await screen.findByText('dashboard')).toBeInTheDocument()
    expect(reload).not.toHaveBeenCalled()
  })
})

describe('error boundary', () => {
  function Boom(): never {
    throw new Error('boom')
  }

  it('shows a reload screen instead of a blank page, and clears when the page changes', () => {
    vi.spyOn(console, 'error').mockImplementation(() => {})
    const { rerender } = render(<ErrorBoundary resetKey="/a"><Boom /></ErrorBoundary>)
    expect(screen.getByRole('alert')).toHaveTextContent('Something went wrong')
    expect(screen.getByRole('button', { name: 'Reload the page' })).toBeInTheDocument()
    rerender(<ErrorBoundary resetKey="/b"><p>fine now</p></ErrorBoundary>)
    expect(screen.getByText('fine now')).toBeInTheDocument()
  })
})
