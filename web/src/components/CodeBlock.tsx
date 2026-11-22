import { CheckIcon, ClipboardDocumentIcon } from '@heroicons/react/24/outline'
import { useEffect, useState } from 'react'

/**
 * A block of code to copy into a terminal or a config file, with a copy button in its top-right corner. The button sits on the wrapper, not the
 * scrolling <pre>, so it stays put when a long line scrolls sideways. After copying it shows a check mark for two seconds (an icon only, so it
 * never covers the code), and says so to screen readers.
 */
export function CodeBlock({ children }: { children: string }) {
  const [copied, setCopied] = useState(false)
  useEffect(() => {
    if (!copied) return
    const t = setTimeout(() => setCopied(false), 2000)
    return () => clearTimeout(t)
  }, [copied])
  const copy = () => {
    // The clipboard is missing on plain http and in some embedded browsers: then the button does nothing, and the code can still be selected.
    void navigator.clipboard?.writeText(children).then(() => setCopied(true), () => undefined)
  }
  return (
    <div className="doc-code-wrap">
      <pre className="doc-code"><code>{children}</code></pre>
      <button type="button" className="code-copy" onClick={copy} aria-label={copied ? 'Copied' : 'Copy code'} title={copied ? 'Copied' : 'Copy'}>
        {copied ? <CheckIcon aria-hidden="true" /> : <ClipboardDocumentIcon aria-hidden="true" />}
      </button>
      <span className="sr-only" aria-live="polite">{copied ? 'Copied to the clipboard' : ''}</span>
    </div>
  )
}
