import { animate, createScope, onScroll, scrambleText, stagger } from 'animejs'

/** True when animation should be skipped: the user asked for reduced motion, or the environment cannot report it (tests, very old browsers). */
export function prefersReducedMotion(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return true
  return window.matchMedia('(prefers-reduced-motion: reduce)').matches
}

/**
 * Rotates the text of `el` through `phrases`, scrambling each change. Call inside a scope; returns a cleanup function.
 * The caller keeps a static, accessible version of the content; this only changes what sighted users see.
 */
export function rotateScramble(el: HTMLElement, phrases: string[], { hold = 2600 }: { hold?: number } = {}): () => void {
  let i = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let stopped = false
  const next = () => {
    if (stopped) return
    i = (i + 1) % phrases.length
    animate(el, {
      innerHTML: scrambleText({ text: phrases[i], chars: 'a-z0-9_#%', cursor: '_', revealRate: 45, settleDuration: 260 }),
      duration: 1100,
    }).then(() => {
      if (!stopped) timer = setTimeout(next, hold)
    })
  }
  timer = setTimeout(next, hold)
  return () => {
    stopped = true
    if (timer) clearTimeout(timer)
  }
}

/** Moves `el` slightly slower than the page while `trigger` is on screen. */
export function parallax(el: HTMLElement, trigger: HTMLElement) {
  animate(el, {
    translateY: [0, 48],
    ease: 'linear',
    autoplay: onScroll({ target: trigger, enter: 'top top', leave: 'bottom top', sync: true }),
  })
}

/** Fades and lifts the `[data-reveal]` children of `group` in a stagger when the group scrolls into view. Call inside a scope. */
export function revealOnScroll(group: HTMLElement) {
  const items = Array.from(group.querySelectorAll<HTMLElement>('[data-reveal]'))
  if (items.length === 0) return
  animate(items, {
    opacity: [0, 1],
    translateY: [28, 0],
    duration: 700,
    ease: 'outCubic',
    delay: stagger(90),
    // 'play' (not the default 'play pause') so a fast scroll past the group never freezes it half-visible.
    autoplay: onScroll({ target: group, enter: 'bottom-=15% top', sync: 'play', repeat: false }),
  })
}

/**
 * Runs `setup` in an anime.js scope rooted at `root` (the pattern from the anime.js React guide): everything it creates is reverted by the
 * returned cleanup, so React effects and StrictMode double-mounts cannot leak animations. If setup throws, the page is left un-animated.
 */
export function inScope(root: HTMLElement, setup: (add: (cleanup: () => void) => void) => void): () => void {
  const cleanups: Array<() => void> = []
  const scope = createScope({ root })
  try {
    scope.add(() => setup((c) => cleanups.push(c)))
  } catch {
    scope.revert()
    return () => {}
  }
  return () => {
    cleanups.forEach((c) => c())
    scope.revert()
  }
}

/**
 * Tilts `frame` a few degrees toward the pointer while it hovers, and settles it back on leave. Mouse-like pointers only: on touch screens there is no hover,
 * and a tilt that sticks after a tap would look broken. Returns its cleanup.
 */
export function tiltOnHover(frame: HTMLElement, maxDeg = 3): () => void {
  if (typeof window.matchMedia !== 'function' || !window.matchMedia('(hover: hover) and (pointer: fine)').matches) return () => {}
  let raf = 0
  const onMove = (e: PointerEvent) => {
    cancelAnimationFrame(raf)
    raf = requestAnimationFrame(() => {
      const r = frame.getBoundingClientRect()
      const x = (e.clientX - r.left) / r.width - 0.5
      const y = (e.clientY - r.top) / r.height - 0.5
      animate(frame, { rotateY: x * maxDeg * 2, rotateX: -y * maxDeg * 2, translateY: -4, duration: 240, ease: 'outQuad' })
    })
  }
  const onLeave = () => {
    cancelAnimationFrame(raf)
    animate(frame, { rotateX: 0, rotateY: 0, translateY: 0, duration: 600, ease: 'outElastic(1, .6)' })
  }
  frame.addEventListener('pointermove', onMove)
  frame.addEventListener('pointerleave', onLeave)
  return () => {
    cancelAnimationFrame(raf)
    frame.removeEventListener('pointermove', onMove)
    frame.removeEventListener('pointerleave', onLeave)
  }
}

/** A looping pulse (expanding rings) that only runs while `host` is on screen, so it costs nothing off-screen. Returns its cleanup. */
export function pingWhileVisible(host: HTMLElement): () => void {
  const rings = Array.from(host.querySelectorAll<HTMLElement>('.ring'))
  if (rings.length === 0 || typeof IntersectionObserver === 'undefined') return () => {}
  const pulse = animate(rings, {
    scale: [1, 3],
    opacity: [0.55, 0],
    duration: 2000,
    ease: 'outCubic',
    delay: stagger(700),
    loop: true,
    autoplay: false,
  })
  const io = new IntersectionObserver((entries) => entries.forEach((e) => (e.isIntersecting ? pulse.play() : pulse.pause())), { threshold: 0.3 })
  io.observe(host)
  return () => {
    io.disconnect()
    pulse.revert()
  }
}

/**
 * The motion for the product screenshots, run inside a scope: each `[data-reveal]` figure fades up in a stagger as the group scrolls into view, a gold sheen
 * sweeps once across every screenshot after it appears, screenshots tilt toward the pointer on hover, and the alert email's dot pings while visible.
 */
export function animateShots(group: HTMLElement, add: (cleanup: () => void) => void) {
  revealOnScroll(group)
  const sheens = Array.from(group.querySelectorAll<HTMLElement>('.shot-sheen'))
  if (sheens.length > 0) {
    animate(sheens, {
      translateX: ['-130%', '260%'],
      duration: 1600,
      ease: 'inOutSine',
      delay: stagger(280, { start: 900 }), // after the figures have faded in
      autoplay: onScroll({ target: group, enter: 'bottom-=15% top', sync: 'play', repeat: false }),
    })
  }
  group.querySelectorAll<HTMLElement>('.shot-frame').forEach((f) => add(tiltOnHover(f)))
  group.querySelectorAll<HTMLElement>('.shot-ping').forEach((p) => add(pingWhileVisible(p)))
}
