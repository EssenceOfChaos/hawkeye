import { Component, type ReactNode } from 'react'
import { BrandMark } from './Brand'

interface Props {
  children: ReactNode
  /** When this changes (the route, for example) a shown error is cleared, so moving to another page gets a fresh try. */
  resetKey?: string
}

/** A crash while rendering shows this instead of a blank page. Reloading is the one thing that helps most (a new deploy, a stale file, a one-off glitch). */
export class ErrorBoundary extends Component<Props, { failed: boolean; key?: string }> {
  state = { failed: false, key: this.props.resetKey }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  // A new resetKey (the visitor went to another page) clears a shown error.
  static getDerivedStateFromProps(props: Props, state: { failed: boolean; key?: string }) {
    return props.resetKey !== state.key ? { failed: false, key: props.resetKey } : null
  }

  render() {
    if (!this.state.failed) return this.props.children
    return (
      <main className="auth">
        <section className="card narrow" role="alert">
          <BrandMark />
          <h1>Something went wrong</h1>
          <p>
            HawkEye may have just been updated, or the page hit a snag. Reloading usually fixes it, and your monitors are not affected: they keep being
            checked either way.
          </p>
          <button type="button" onClick={() => window.location.reload()}>Reload the page</button>
        </section>
      </main>
    )
  }
}
