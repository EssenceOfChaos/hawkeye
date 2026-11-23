import { Link } from 'react-router'
import { useAuth } from '../auth'
import { brand, STATUS_URL } from '../brand'
import { Reveal } from '../components/Reveal'
import { Scout } from '../components/Scout'
import { SiteFooter } from '../components/SiteFooter'
import goLogo from '../assets/brand/tech/go.svg'
import postgresLogo from '../assets/brand/tech/postgresql.svg'
import reactLogo from '../assets/brand/tech/react.svg'

// The About page (/about): who builds HawkEye and why. Like the home page it may only say what the product does today: the setup and import
// panels describe internal/discovery and the OpenAPI/Postman import as they work, and the stack is the one in docs/architecture.md (no Redis:
// the job queue is in PostgreSQL). No team photos, customer quotes, statistics or certifications. Its title and description come from src/seo.ts.

const PRINCIPLES = [
  {
    icon: brand.icons.simple,
    title: 'Simple',
    lead: 'Powerful technology should feel effortless.',
    text: 'We believe in minimalist interfaces, intuitive workflows, and eliminating unnecessary complexity. From the moment you create an account to the everyday monitoring experience, everything should feel natural and straightforward.',
  },
  {
    icon: brand.icons.analytics,
    title: 'Effective',
    lead: 'Simple doesn’t mean sacrificing capability.',
    text: 'We believe developers deserve meaningful health checks, accurate monitoring, actionable insights, and timely alerts. Our goal is to provide deep visibility into your services without requiring a complicated setup.',
  },
  {
    icon: brand.icons.shield,
    title: 'Secure',
    lead: 'Trust is fundamental to everything we do.',
    text: 'We take a security-conscious approach to software development, infrastructure, and data handling. We believe security should be considered from the beginning, not added as an afterthought.',
  },
]

// What "set up from your domain" suggests (internal/discovery).
const DISCOVERY = [
  { title: 'Your website', text: 'A monitor for your home page, the address your visitors actually use.' },
  { title: 'Health and API endpoints', text: 'Health checks such as /healthz on your site, and an API on api.yourdomain.com. They can report problems the home page hides.' },
  { title: 'SSL certificates and domains', text: 'Warnings before your certificate or domain registration expires, so a missed renewal never causes security warnings or downtime.' },
  { title: 'Mail (MX) records', text: 'An alert if your mail records disappear, which stops email arriving without any sign on your website.' },
]

const STACK = [
  {
    logo: goLogo,
    title: 'Go',
    text: 'Efficient, high-performance backend services built with Go. We value its simplicity, concurrency model, and suitability for building reliable systems.',
  },
  {
    logo: postgresLogo,
    title: 'PostgreSQL',
    text: 'Reliable relational data storage powered by PostgreSQL, a proven foundation for the data behind every check. It also runs our job queue, so scheduled checks survive a restart.',
  },
  {
    logo: reactLogo,
    title: 'React',
    text: 'A fast, responsive dashboard built with React and TypeScript, so the health of every service is clear at a glance.',
  },
]

const COMMITMENTS = [
  { title: 'Developer-first design', text: 'Creating tools that developers genuinely enjoy using.' },
  { title: 'Continuous innovation', text: 'Consistently improving the platform and introducing useful new capabilities.' },
  { title: 'Reliability', text: 'Building dependable monitoring and alerting experiences.' },
  { title: 'Transparency', text: 'Making infrastructure health and monitoring results easy to understand, including our own.' },
  { title: 'Accessibility', text: 'Providing powerful monitoring without unnecessary enterprise complexity.' },
  { title: 'Listening to our users', text: 'Taking feedback seriously and allowing our customers to help shape the future of HawkEye.' },
]

export function AboutPage() {
  const { status } = useAuth()
  const signedIn = status === 'authenticated'
  const cta = signedIn ? 'Open dashboard' : 'Create Your Free Account'
  const ctaTo = signedIn ? '/dashboard' : '/register'

  return (
    <div className="landing mon about bleed" data-theme="light">
      <header className="landing-header">
        <Link to="/" aria-label="HawkEye home"><img className="landing-logo" src={brand.logoWhite} alt="HawkEye" width={220} height={43} /></Link>
        <span className="spacer" />
        {!signedIn && <Link to="/login">Sign in</Link>}
        <Link className="button primary" to={ctaTo}>{signedIn ? 'Open dashboard' : 'Sign up free'}</Link>
      </header>

      <main>
        <section className="mon-earth hero-bleed about-hero" aria-labelledby="about-h1">
          <img className="mon-earth-bg" src={brand.art.earth} alt="" width={1774} height={887} decoding="async" />
          <div className="mon-earth-copy">
            <p className="eyebrow">Built by developers. For developers.</p>
            <h1 id="about-h1">Monitoring should be powerful. <span className="gold nowrap">Not complicated.</span></h1>
            <p>
              HawkEye was born from a simple idea: monitoring your websites, APIs, and infrastructure shouldn’t require enterprise-level complexity.
            </p>
            <p>
              We’re building a smarter, simpler monitoring platform that gives developers the visibility and confidence they need to keep their services
              running smoothly.
            </p>
            <p className="about-tagline">Powerful health checks. Effortless setup. Actionable insights.</p>
            <div className="row">
              <Link className="button primary large" to={ctaTo}>{cta}</Link>
              <Link className="button large" to="/#features-h">Explore Features</Link>
            </div>
          </div>
        </section>

        <section className="section about-prose" aria-labelledby="mission-h">
          <p className="eyebrow">Why we exist</p>
          <h2 id="mission-h">Great software doesn’t need to be complicated.</h2>
          <p>We believe developers should spend their time building great software, not wrestling with complicated monitoring tools.</p>
          <p>
            Too many monitoring platforms have become unnecessarily complex, overwhelming users with configuration, cluttered interfaces, and
            enterprise-focused features that many teams simply don’t need.
          </p>
          <p>We set out to build something different.</p>
          <p>
            HawkEye combines deep monitoring capabilities with a clean, intuitive experience that makes it easy to understand the health of your
            infrastructure at a glance.
          </p>
          <p>
            Whether you’re an independent developer, a small business, or a growing engineering team, you deserve monitoring tools that are powerful,
            accessible, and genuinely easy to use.
          </p>
        </section>

        <section className="section" aria-labelledby="principles-h">
          <p className="eyebrow">What guides us</p>
          <h2 id="principles-h">Three principles. Everything we build.</h2>
          <Reveal as="ul" className="features about-three">
            {PRINCIPLES.map((p) => (
              <li key={p.title} className="feature" data-reveal>
                <img src={p.icon} alt="" width={64} height={64} loading="lazy" />
                <h3>{p.title}</h3>
                <p><strong>{p.lead}</strong></p>
                <p>{p.text}</p>
              </li>
            ))}
          </Reveal>
        </section>

        <section className="section" aria-labelledby="setup-h">
          <p className="eyebrow">Less configuration. More coverage.</p>
          <h2 id="setup-h">Your monitoring suite. One click away.</h2>
          <p className="about-lede">Getting started with monitoring shouldn’t mean manually configuring dozens of endpoints and checks.</p>
          <Reveal className="about-panels">
            <article className="about-panel" data-reveal aria-labelledby="discover-h">
              <h3 id="discover-h">Set up from your domain</h3>
              <p>
                Simply enter your domain name. HawkEye looks at your site from the outside, the way its checks will, and suggests an initial monitoring
                suite for you.
              </p>
              <div className="about-flow" aria-hidden="true">
                <span className="about-flow-input mono">yourdomain.com</span>
                <span className="about-flow-arrow">↓</span>
                <ul className="about-flow-out">
                  <li><span className="tick">✓</span> Home page</li>
                  <li><span className="tick">✓</span> Health endpoint</li>
                  <li><span className="tick">✓</span> api.yourdomain.com</li>
                  <li><span className="tick">✓</span> Mail (MX) records</li>
                </ul>
              </div>
              <h4>What HawkEye looks for</h4>
              <ul className="about-list">
                {DISCOVERY.map((d) => (
                  <li key={d.title}><strong>{d.title}:</strong> {d.text}</li>
                ))}
              </ul>
              <p>The recommended monitors come ready to go: one click adds them all. Untick any you don’t want, and let HawkEye take it from there.</p>
              <p className="about-tagline">No lengthy configuration. No complicated setup. Just smarter monitoring.</p>
            </article>
            <article className="about-panel" data-reveal aria-labelledby="import-h">
              <p className="eyebrow">Already have API documentation?</p>
              <h3 id="import-h">Turn your API documentation into actionable health checks.</h3>
              <p>
                If you use Swagger/OpenAPI specifications or Postman collections, upload your existing files and HawkEye lists a detailed API health check
                for every endpoint, ready for you to choose from.
              </p>
              <div className="about-flow" aria-hidden="true">
                <span className="about-flow-input mono">openapi.yaml · collection.json</span>
                <span className="about-flow-arrow">↓</span>
                <ul className="about-flow-out mono">
                  <li><span className="tick">✓</span> GET /health</li>
                  <li><span className="tick">✓</span> GET /users</li>
                  <li><span className="tick">✓</span> GET /orders/{'{id}'}</li>
                </ul>
              </div>
              <p>
                Instead of manually configuring individual endpoints, use the API documentation you already maintain to get deeper monitoring coverage in
                moments.
              </p>
            </article>
          </Reveal>
        </section>

        <section className="section" aria-labelledby="stack-h">
          <p className="eyebrow">Engineered with purpose</p>
          <h2 id="stack-h">Built on technology we believe in.</h2>
          <p className="about-lede">
            We believe the foundation of great software matters. HawkEye is built using a modern technology stack selected for performance, reliability,
            maintainability, and scalability.
          </p>
          <Reveal as="ul" className="features about-three">
            {STACK.map((t) => (
              <li key={t.title} className="feature about-tech" data-reveal>
                <img src={t.logo} alt="" width={48} height={48} loading="lazy" />
                <h3>{t.title}</h3>
                <p>{t.text}</p>
              </li>
            ))}
          </Reveal>
          <p className="about-note">We choose technology based on what helps us build better software, not simply what’s trending.</p>
        </section>

        <section className="section split about-team" aria-labelledby="team-h">
          <div>
            <p className="eyebrow">The people behind the product</p>
            <h2 id="team-h">Small team. Big ambitions.</h2>
            <p>
              HawkEye is an independent startup built by a small team of dedicated and experienced software engineers with a shared passion for technology
              and solving real-world problems.
            </p>
            <p>
              We’re developers ourselves. We understand the frustration of unreliable services, complicated dashboards, excessive configuration, and
              monitoring tools that create more work than they eliminate.
            </p>
            <p>That’s why we’re building HawkEye differently.</p>
            <p>
              Being a small, engineering-focused team allows us to stay agile, listen closely to our users, and continuously improve the product. We aren’t
              interested in adding features just to make a longer feature list. We want every improvement to deliver meaningful value.
            </p>
            <p>We’re committed to innovating, listening to our community, and making HawkEye better every day.</p>
            <p className="about-tagline">We’re just getting started.</p>
          </div>
          <Scout pose="wave" size={220} className="about-scout" />
        </section>

        <section className="section" aria-labelledby="commit-h">
          <p className="eyebrow">What you can expect</p>
          <h2 id="commit-h">Always watching. Always improving.</h2>
          <p className="about-lede">Our commitment goes beyond building a monitoring platform. We’re committed to:</p>
          <Reveal as="ul" className="features about-three">
            {COMMITMENTS.map((c) => (
              <li key={c.title} className="feature" data-reveal>
                <h3>{c.title}</h3>
                <p>{c.text}</p>
              </li>
            ))}
          </Reveal>
          <p className="about-note">
            Our own <a href={STATUS_URL}>status page</a> shows how HawkEye itself is doing. Have an idea or a problem?{' '}
            <Link to="/contact">Tell us</Link>.
          </p>
          <p className="about-note">
            Our goal isn’t simply to monitor your infrastructure. It’s to give you the confidence to focus on building and growing your business.
          </p>
        </section>

        <section className="mon-final" aria-labelledby="about-final-h">
          <img className="mon-hero-bg" src={brand.art.hawkEye} alt="" width={1672} height={941} loading="lazy" decoding="async" />
          <div className="mon-final-inner">
            <h2 id="about-final-h">
              <span className="line">You focus on building.</span>
              <span className="line gold">We’ll keep watch.</span>
            </h2>
            <p>
              Powerful monitoring doesn’t have to be complicated. Get started with HawkEye and discover a simpler way to monitor your websites, APIs, and
              infrastructure.
            </p>
            <div className="row">
              <Link className="button primary large" to={ctaTo}>{cta}</Link>
              <Link className="button large" to="/#pricing-h">Explore Pricing</Link>
            </div>
            <p className="hint">No complicated setup. No unnecessary complexity. Just smarter monitoring.</p>
          </div>
        </section>
      </main>

      <SiteFooter />
    </div>
  )
}
