import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { brand } from '../brand'
import { prefersReducedMotion } from '../motion'
import { ALL_GOOD, BLINK_ONCE, BLINK_TWICE, FLAP, HELLO, HELLO_DELAY_MS, REST, THUMBS_UP_EVERY, type Steps } from './scoutSteps'

/**
 * Scout, animated: he waves hello, gives a thumbs-up, blinks now and then, and flaps his wings when pointed at or tapped. The still rest frame shows first;
 * the frames load only after the page has, and never for people who prefer reduced motion, who keep the still. Decoration only: hidden
 * from assistive technology.
 */
export function AnimatedScout({ size = 160, className = '' }: { size?: number; className?: string }) {
  const [ready, setReady] = useState(false)
  const [frame, setFrame] = useState(REST)
  const [flapping, setFlapping] = useState(false)
  const timers = useRef<number[]>([])
  const busy = useRef(false)

  const later = useCallback((fn: () => void, ms: number) => {
    const id = window.setTimeout(() => { timers.current = timers.current.filter((t) => t !== id); fn() }, ms)
    timers.current.push(id)
  }, [])

  /** Plays steps unless another sequence is playing; returns whether it started. */
  const play = useCallback((steps: Steps, done?: () => void) => {
    if (busy.current) return false
    busy.current = true
    let at = 0
    for (const [f, ms] of steps) {
      if (at === 0) setFrame(f) // the first step shows at once
      else later(() => setFrame(f), at)
      at += ms
    }
    later(() => { setFrame(REST); busy.current = false; done?.() }, at)
    return true
  }, [later])

  // Load the frames once the page has loaded, so they never compete with what the visitor sees first.
  useEffect(() => {
    if (prefersReducedMotion()) return
    let cancelled = false
    const load = () => {
      const img = new Image()
      img.src = brand.scoutMotion.frames
      img.decode().then(() => { if (!cancelled) setReady(true) }, () => {}) // if it fails, he stays still
    }
    if (document.readyState === 'complete') load()
    else window.addEventListener('load', load, { once: true })
    return () => { cancelled = true; window.removeEventListener('load', load) }
  }, [])

  // Once loaded: wave hello and give a thumbs-up, then blink every few seconds, with a thumbs-up every few blinks (skipped while the tab is
  // hidden).
  useEffect(() => {
    if (!ready) return
    let moment = 0
    const idle = () => {
      later(() => {
        moment += 1
        if (!document.hidden) {
          if (moment % THUMBS_UP_EVERY === 0) play(ALL_GOOD)
          else play(Math.random() < 0.2 ? BLINK_TWICE : BLINK_ONCE)
        }
        idle()
      }, 2500 + Math.random() * 3500)
    }
    // If he is mid-flap when the hello is due, it waits for the flap to end rather than being skipped (the blinking starts after it).
    const hello = () => { if (!play(HELLO, () => play(ALL_GOOD, idle))) later(hello, 150) }
    later(hello, HELLO_DELAY_MS)
    return () => { timers.current.forEach(clearTimeout); timers.current = []; busy.current = false }
  }, [ready, play, later])

  const flap = () => {
    if (!ready || !play(FLAP, () => setFlapping(false))) return
    setFlapping(true)
  }

  return (
    <span className={`scout scout-animated ${flapping ? 'is-flapping' : ''} ${className}`.replace(/\s+/g, ' ').trim()}
      style={{ '--scout-size': `${size}px` } as CSSProperties} aria-hidden="true"
      onPointerEnter={(e) => { if (e.pointerType === 'mouse') flap() }} onClick={flap}>
      <span className="scout-figure">
        <img src={brand.scoutMotion.rest} alt="" width={size} height={size} decoding="async" style={ready ? { visibility: 'hidden' } : undefined} />
        {ready && (
          <span className="scout-frames" data-frame={frame}
            style={{ backgroundImage: `url(${brand.scoutMotion.frames})`, '--frame': frame, '--frames': brand.scoutMotion.count } as CSSProperties} />
        )}
      </span>
    </span>
  )
}
