import { useLayoutEffect, useRef } from 'react'
import { brand } from '../brand'
import { animateShots, inScope, prefersReducedMotion } from '../motion'

/**
 * "See it in action": real screens of the app, shown with sample data (scripts/screenshots). The images are static and fully readable without any of the
 * motion; animation is layered on top and skipped when the visitor prefers reduced motion.
 */
export function ProductShots() {
  const ref = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el || prefersReducedMotion()) return
    el.classList.add('reveal-armed', 'shots-animated')
    const stop = inScope(el, (add) => animateShots(el, add))
    return () => {
      stop()
      el.classList.remove('reveal-armed', 'shots-animated')
    }
  }, [])

  return (
    <section aria-labelledby="demo-h" className="section">
      <h2 id="demo-h">See it in action.</h2>
      <p>Real screens from HawkEye, shown with sample data.</p>
      <div className="shots" ref={ref}>
        <figure className="shot" data-reveal>
          <div className="shot-frame">
            <img
              src={brand.screens.dashboard}
              alt="The HawkEye dashboard with sample data: 99.8% uptime over the last 24 hours, five monitors online, an SSL certificate expiring in 26 days, a response-time chart and a list of recent resolved incidents."
              width={1700}
              height={1554}
              loading="lazy"
              decoding="async"
            />
            <span className="shot-sheen" aria-hidden="true" />
          </div>
          <figcaption>
            <strong>Your whole picture at a glance.</strong> Uptime, response times, certificate expiry and recent incidents in one place.
          </figcaption>
        </figure>
        <div className="shots-side">
          <figure className="shot" data-reveal>
            <div className="shot-frame">
              <img
                src={brand.screens.incidents}
                alt="The HawkEye incidents list with sample data: four resolved incidents, each with the website, start time, duration and cause, such as a request that timed out."
                width={1400}
                height={481}
                loading="lazy"
                decoding="async"
              />
              <span className="shot-sheen" aria-hidden="true" />
            </div>
            <figcaption>
              <strong>Every outage, accounted for.</strong> Confirmed by consecutive failed checks, with start, duration and cause.
            </figcaption>
          </figure>
          <figure className="shot" data-reveal>
            <div className="shot-frame">
              <img
                src={brand.screens.alertEmail}
                alt="A HawkEye alert email with sample data: “Checkout may be offline”, with the website address, when the failures were detected, the latest result and a View incidents button."
                width={960}
                height={975}
                loading="lazy"
                decoding="async"
              />
              <span className="shot-sheen" aria-hidden="true" />
              <span className="shot-ping" aria-hidden="true">
                <span className="ring" />
                <span className="ring" />
                <span className="dot" />
              </span>
            </div>
            <figcaption>
              <strong>The alert you actually receive.</strong> One email when an outage is confirmed and one when it ends. Slack and webhooks too.
            </figcaption>
          </figure>
        </div>
      </div>
    </section>
  )
}
