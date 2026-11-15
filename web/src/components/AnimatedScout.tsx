import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react'
import { brand } from '../brand'
import { prefersReducedMotion } from '../motion'

/** Frames in brand.scoutMotion.frames, left to right. */
const REST = 0
const BLINK = 1
const WINGS_UP = 2
const WINGS_DOWN = 3
const WAVE = [4, 5, 6]
const THUMBS_UP = 7

/** A sequence of [frame, milliseconds]; he returns to rest after the last one. */
type Steps = [number, number][]

const HELLO: Steps = [[WAVE[0], 140], [WAVE[1], 140], [WAVE[2], 170], [WAVE[1], 140], [WAVE[2], 170], [WAVE[1], 140], [WAVE[0], 140]]
const FLAP: Steps = [[WINGS_DOWN, 90], [WINGS_UP, 110], [WINGS_DOWN, 90], [WINGS_UP, 110], [WINGS_DOWN, 100]]
const BLINK_ONCE: Steps = [[BLINK, 130]]
const BLINK_TWICE: Steps = [[BLINK, 110], [REST, 120], [BLINK, 110]]
const ALL_GOOD: Steps = [[THUMBS_UP, 1800]]
/** Every this many idle moments, he gives a thumbs-up instead of blinking. */
const THUMBS_UP_EVERY = 4

/** Wait before the hello wave: long enough for the hero's rise-in to finish (index.css, .hero-scout). */
export const HELLO_DELAY_MS = 1100

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
    later(() => play(HELLO, () => play(ALL_GOOD, idle)), HELLO_DELAY_MS)
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
