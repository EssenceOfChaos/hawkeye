import { Link } from 'react-router'
import { useAuth } from '../auth'
import { brand } from '../brand'
import { CodeBlock } from '../components/CodeBlock'
import { Reveal } from '../components/Reveal'
import { SiteFooter } from '../components/SiteFooter'

// The Elixir page (/elixir): the open-source hawk_eye package, which reports Oban jobs to heartbeat monitors. Like the home page it may only say
// what works today: keep it in step with the package's README (github.com/EssenceOfChaos/hawk_eye) and with heartbeat monitors in docs/api.md.
// The error text of a failure is kept in the monitor's ping log; alerts say only that the job reported a failure.

export const HAWK_EYE_GITHUB = 'https://github.com/EssenceOfChaos/hawk_eye'
export const HAWK_EYE_HEXDOCS = 'https://hexdocs.pm/hawk_eye'

const INSTALL = `def deps do
  [
    {:hawk_eye, "~> 0.1"}
  ]
end`

const CONFIG = `# config/runtime.exs
if config_env() == :prod do
  config :hawk_eye,
    oban: [
      workers: %{
        MyApp.Workers.NightlyReport => System.get_env("HAWKEYE_NIGHTLY_REPORT"),
        MyApp.Workers.SyncInvoices => System.get_env("HAWKEYE_SYNC_INVOICES")
      }
    ]
end`

const TRACK = `HawkEye.track(System.get_env("HAWKEYE_CLEANUP"), fn ->
  MyApp.Cleanup.run()
end)`

const OUTCOMES = [
  { outcome: 'Succeeded', sent: 'A success ping, with how long the job took' },
  { outcome: 'Failed, and Oban will retry it', sent: 'Nothing: a transient error wakes nobody' },
  { outcome: 'Failed on its last attempt', sent: 'A failure, with the error message' },
  { outcome: 'Discarded by the worker', sent: 'A failure' },
  { outcome: 'Snoozed or canceled', sent: 'Nothing (canceled jobs are configurable)' },
  { outcome: 'Never ran at all', sent: 'Nothing, so HawkEye alerts when the deadline passes' },
]

const FEATURES = [
  { title: 'Catches jobs that never run', text: 'HawkEye checks the deadline from outside your app, so a stopped scheduler or a crashed node still alerts you.' },
  { title: 'Ignores retries', text: 'Only a job’s last attempt reports a failure, so errors that fix themselves alert nobody.' },
  { title: 'The error, kept for you', text: 'A failure carries its error message, shown in the monitor’s ping log. Job arguments are never sent.' },
  { title: 'Never in the way', text: 'Pings are sent in the background and retried. They never raise, block a job or stop your app from starting.' },
  { title: 'No extra HTTP client', text: 'It uses Erlang’s built-in client with certificate checks. Its only dependency is telemetry.' },
  { title: 'Open source', text: 'MIT licensed. Issues and pull requests are welcome on GitHub.' },
]

export function ElixirPage() {
  const { status } = useAuth()
  const signedIn = status === 'authenticated'
  const cta = signedIn ? 'Open dashboard' : 'Create Your Free Account'
  const ctaTo = signedIn ? '/dashboard' : '/register'

  return (
    <div className="landing mon about elixir bleed" data-theme="light">
      <header className="landing-header">
        <Link to="/" aria-label="HawkEye home"><img className="landing-logo" src={brand.logoWhite} alt="HawkEye" width={220} height={43} /></Link>
        <span className="spacer" />
        {!signedIn && <Link to="/login">Sign in</Link>}
        <Link className="button primary" to={ctaTo}>{signedIn ? 'Open dashboard' : 'Sign up free'}</Link>
      </header>

      <main>
        <section className="mon-earth hero-bleed about-hero" aria-labelledby="elixir-h1">
          <img className="mon-earth-bg" src={brand.art.earth} alt="" width={1774} height={887} decoding="async" />
          <div className="mon-earth-copy">
            <p className="eyebrow">For Elixir and Oban</p>
            <h1 id="elixir-h1">Know when an Oban job fails. <span className="gold nowrap">Or never runs.</span></h1>
            <p>
              <code>hawk_eye</code> is an open-source Elixir package that reports your scheduled Oban jobs to HawkEye. A job that fails on its last attempt
              alerts you at once. A job that doesn’t run at all alerts you when its deadline passes, even if your whole application is down.
            </p>
            <div className="row">
              <Link className="button primary large" to={ctaTo}>{cta}</Link>
              <a className="button large" href={HAWK_EYE_GITHUB}>View on GitHub</a>
            </div>
            <p className="hint">MIT licensed · Heartbeat monitors are on every plan, including Free</p>
          </div>
        </section>

        <section className="section" aria-labelledby="setup-h">
          <p className="eyebrow">Set up in minutes</p>
          <h2 id="setup-h">Three steps. No changes to your workers.</h2>
          <ol className="elixir-steps">
            <li className="about-panel">
              <h3>Add the package</h3>
              <CodeBlock>{INSTALL}</CodeBlock>
            </li>
            <li className="about-panel">
              <h3>Create a heartbeat monitor</h3>
              <p>
                Add a heartbeat monitor in HawkEye for each scheduled worker. Set its period to how often the job runs, then copy its ping address.
              </p>
            </li>
            <li className="about-panel">
              <h3>Map each worker to its ping address</h3>
              <CodeBlock>{CONFIG}</CodeBlock>
            </li>
          </ol>
          <p className="about-lede">
            That’s all. The package listens to Oban’s telemetry events, so your workers stay exactly as they are.
          </p>
        </section>

        <section className="section" aria-labelledby="reported-h">
          <p className="eyebrow">Real failures only</p>
          <h2 id="reported-h">Alerts on what matters, not on every retry.</h2>
          <div className="table-wrap about-panel">
            <table>
              <caption className="sr-only">What each Oban job outcome sends to HawkEye</caption>
              <thead>
                <tr><th>Job outcome</th><th>Sent to HawkEye</th></tr>
              </thead>
              <tbody>
                {OUTCOMES.map((o) => (
                  <tr key={o.outcome}><td>{o.outcome}</td><td>{o.sent}</td></tr>
                ))}
              </tbody>
            </table>
          </div>
        </section>

        <section className="section" aria-labelledby="why-h">
          <p className="eyebrow">Built for production</p>
          <h2 id="why-h">Small package. Careful defaults.</h2>
          <Reveal as="ul" className="features about-three">
            {FEATURES.map((f) => (
              <li key={f.title} className="feature" data-reveal>
                <h3>{f.title}</h3>
                <p>{f.text}</p>
              </li>
            ))}
          </Reveal>
        </section>

        <section className="section" aria-labelledby="other-h">
          <p className="eyebrow">Not using Oban?</p>
          <h2 id="other-h">Wrap any scheduled job.</h2>
          <p className="about-lede">
            For Quantum jobs, GenServer timers or release tasks, <code>HawkEye.track/2</code> reports success when the function returns and a failure when it
            raises.
          </p>
          <CodeBlock>{TRACK}</CodeBlock>
          <p className="about-note">
            Read the <a href={HAWK_EYE_HEXDOCS}>package documentation</a> for every option, or the{' '}
            <Link to="/docs/cron-job-monitoring">heartbeat guide</Link> for how deadlines, grace time and alerts work.
          </p>
        </section>

        <section className="mon-final" aria-labelledby="elixir-final-h">
          <img className="mon-hero-bg" src={brand.art.hawkEye} alt="" width={1672} height={941} loading="lazy" decoding="async" />
          <div className="mon-final-inner">
            <h2 id="elixir-final-h">
              <span className="line">Your jobs run while you sleep.</span>
              <span className="line gold">We’ll notice when they don’t.</span>
            </h2>
            <p>The same account monitors your websites, APIs and DNS records, with status pages for your users.</p>
            <div className="row">
              <Link className="button primary large" to={ctaTo}>{cta}</Link>
              <Link className="button large" to="/#pricing-h">Explore Pricing</Link>
            </div>
            <p className="hint">No credit card required · Set up in minutes</p>
          </div>
        </section>
      </main>

      <SiteFooter />
    </div>
  )
}
