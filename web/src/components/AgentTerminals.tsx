import type { ReactNode } from 'react'
import { Link } from 'react-router'

/** One line of a terminal: a prompt or answer marker (shown in the gutter, not read aloud) and its text. */
type Line = { prefix?: string; text: ReactNode; tone?: 'prompt' | 'muted' }

// Terminal coloring, as Codex and Claude Code show it: highlighted figures and names, links in green, list bullets in teal.
const Em = ({ children }: { children: ReactNode }) => <span className="t-em">{children}</span>
const Url = ({ children }: { children: ReactNode }) => <span className="t-url">{children}</span>
const Bullet = () => <span className="t-bullet">•</span>

function Terminal({ label, lines }: { label: string; lines: Line[] }) {
  return (
    <figure className="agent-term">
      <figcaption>{label}</figcaption>
      <div className="d-mockup-code agent-code">
        {lines.map((l, i) => (
          <pre key={i} data-prefix={l.prefix ?? ' '} className={l.tone ? `term-${l.tone}` : undefined}><code>{l.text}</code></pre>
        ))}
      </div>
    </figure>
  )
}

// A real exchange in Codex with HawkEye's plugin, against an account with sample data (2026-11-20).
const CODEX: Line[] = [
  { prefix: '›', text: 'What’s the state of my HawkEye monitors?', tone: 'prompt' },
  { text: '' },
  { prefix: '•', text: 'Your HawkEye monitors look healthy overall:' },
  { text: <>  <Bullet /> <Em>6 up</Em>, <Em>0 down</Em>, <Em>1 paused</Em> out of 7</> },
  { text: <>  <Bullet /> <Em>No open incidents</Em></> },
  { text: <>  <Bullet /> <Em>24-hour uptime: 100%</Em></> },
  { text: <>  <Bullet /> No certificates or domains expiring within 30 days</> },
  { text: '' },
  { text: <>The paused monitor is <Em>Old Acme landing page</Em> (<Url>https://example.net</Url>). All other monitors are up.</> },
  { text: '' },
  { text: 'Worked for 9s', tone: 'muted' },
]

// What Claude can answer from HawkEye's tools: the monitor's open slowdown (get_monitor) and its timing breakdown for the last day and the
// past week (get_uptime's avg_timings). Sample data.
const CLAUDE: Line[] = [
  { prefix: '>', text: 'Why is my website running slow?', tone: 'prompt' },
  { text: '' },
  { prefix: '⏺', text: <><Em>Checkout API</Em> is slower than usual. Its checks averaged <Em>2,310 ms</Em> over the last 24 hours, against 420 ms over the past week.</> },
  { text: '' },
  { text: <>  <Bullet /> <Em>Server wait: 2,080 ms</Em> (usually 190 ms)</> },
  { text: <>  <Bullet /> DNS lookup, connect and TLS: 95 ms together, as usual</> },
  { text: '' },
  { text: 'The extra time is your application, not the network. Look at today’s deploys or slow database queries. HawkEye confirmed the slowdown at 2:14 PM and will tell you when it’s over.' },
]

/**
 * The landing page's "works with your AI assistant" section: an exchange in Codex and one in Claude Code, side by side from tablet width and
 * stacked on phones. Both are labeled as sample data, like the product screenshots.
 */
export function AgentTerminals() {
  return (
    <section aria-labelledby="agents-h" className="section agents">
      <p className="eyebrow">Works with your AI assistant</p>
      <h2 id="agents-h">Ask Claude or Codex how your sites are doing.</h2>
      <p>
        HawkEye connects to Claude and Codex. Ask about uptime, incidents and slowdowns in plain words, or let your assistant set up monitoring straight
        from your code.
      </p>
      <div className="agent-terms">
        <Terminal label="Codex" lines={CODEX} />
        <Terminal label="Claude Code" lines={CLAUDE} />
      </div>
      <p className="hint agent-note">Shown with sample data.</p>
      <p className="row agent-links">
        <Link to="/docs/claude">Set up with Claude</Link>
        <Link to="/docs/codex">Set up with Codex</Link>
      </p>
    </section>
  )
}
