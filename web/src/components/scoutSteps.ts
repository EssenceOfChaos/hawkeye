/** What AnimatedScout plays: the frames of brand.scoutMotion.frames, and the sequences made from them. */

/** Frames in brand.scoutMotion.frames, left to right. */
export const REST = 0
export const BLINK = 1
export const WAVE = [2, 3, 4]
export const THUMBS_UP = 5
/** Five steps of raising his wings, from just lifting off his body to raised high. */
export const WING = [6, 7, 8, 9, 10]

/** A sequence of [frame, milliseconds]; he returns to rest after the last one. */
export type Steps = [number, number][]

export const HELLO: Steps = [[WAVE[0], 140], [WAVE[1], 140], [WAVE[2], 170], [WAVE[1], 140], [WAVE[2], 170], [WAVE[1], 140], [WAVE[0], 140]]
/** One and a half wingbeats through every step, so the wings never jump: up a little slower, a beat at the top, down quicker. */
export const FLAP: Steps = [
  [WING[0], 60], [WING[1], 60], [WING[2], 60], [WING[3], 70], [WING[4], 120],
  [WING[3], 50], [WING[2], 50], [WING[1], 90],
  [WING[2], 60], [WING[3], 70], [WING[4], 120],
  [WING[3], 50], [WING[2], 50], [WING[1], 60], [WING[0], 70],
]
/** How long a flap lasts, in milliseconds. */
export const FLAP_MS = FLAP.reduce((sum, [, ms]) => sum + ms, 0)
export const BLINK_ONCE: Steps = [[BLINK, 130]]
export const BLINK_TWICE: Steps = [[BLINK, 110], [REST, 120], [BLINK, 110]]
export const ALL_GOOD: Steps = [[THUMBS_UP, 1800]]
/** Every this many idle moments, he gives a thumbs-up instead of blinking. */
export const THUMBS_UP_EVERY = 4

/** Wait before the hello wave: long enough for the hero's rise-in to finish (index.css, .hero-scout). */
export const HELLO_DELAY_MS = 1100
