import { lazy, type ComponentType } from 'react'

const KEY = 'hawkeye:reloaded-for-new-build-at'
const COOLDOWN_MS = 30_000

/**
 * Every deploy renames the app's script files (their names contain a content hash) and removes the old ones. A tab opened before a deploy still asks for
 * the old names, gets a 404, and the page it was loading never appears. Reloading fetches the new index.html and so the new names.
 *
 * Reloads at most once per 30 seconds: if the files are still missing after a reload, something else is wrong and looping would only hide it. Returns
 * whether a reload was started. Without session storage there is no way to tell a first failure from a repeat, so it never reloads then.
 */
export function reloadForNewBuild(now: () => number = Date.now): boolean {
  try {
    if (now() - Number(sessionStorage.getItem(KEY) ?? 0) < COOLDOWN_MS) return false
    sessionStorage.setItem(KEY, String(now()))
  } catch {
    return false
  }
  window.location.reload()
  return true
}

/** React.lazy that survives a deploy: if the file cannot be fetched, reload once to pick up the new build; if that does not help, fail loudly (ErrorBoundary). */
export function lazyWithReload(factory: () => Promise<{ default: ComponentType }>) {
  return lazy(async () => {
    try {
      return await factory()
    } catch (err) {
      if (reloadForNewBuild()) return new Promise<never>(() => {}) // the page is reloading; keep showing "Loading…" until it does
      throw err
    }
  })
}
