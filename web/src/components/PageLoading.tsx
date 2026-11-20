/**
 * Shown in place of a whole page while it loads (a lazy page's code, or the sign-in check before a page decides where to go): a large gold
 * daisyUI ring. The words stay for screen readers. It fades in after a moment, so a fast load never flashes it.
 * Sections inside a page use the small spinner with "Loading…" beside it instead.
 */
export function PageLoading() {
  return (
    <div role="status" className="page-loading">
      <span className="d-loading d-loading-ring d-loading-xl" aria-hidden="true" />
      <span className="sr-only">Loading…</span>
    </div>
  )
}
