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
