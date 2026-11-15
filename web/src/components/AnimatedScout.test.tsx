import { act, fireEvent, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { brand } from '../brand'
import { AnimatedScout } from './AnimatedScout'
import { FLAP_MS, HELLO_DELAY_MS } from './scoutSteps'

function motion(reduced: boolean) {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: reduced && q.includes('reduce'), media: q, addEventListener() {}, removeEventListener() {} }))
}

/** Image whose decode() resolves, and which records what was loaded. */
const loaded: string[] = []
class FakeImage {
  src = ''
  decode() { loaded.push(this.src); return Promise.resolve() }
}

beforeEach(() => {
  loaded.length = 0
  vi.stubGlobal('Image', FakeImage)
  vi.useFakeTimers()
  vi.spyOn(Math, 'random').mockReturnValue(0.5) // single blinks, 4.25 s apart
})
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); vi.restoreAllMocks() })

const frame = (c: HTMLElement) => c.querySelector('.scout-frames')?.getAttribute('data-frame')
const advance = (ms: number) => act(() => { vi.advanceTimersByTime(ms) })

describe('AnimatedScout', () => {
  it('keeps the still for people who prefer reduced motion, and never loads the frames', async () => {
    motion(true)
    const { container } = render(<AnimatedScout />)
    await act(async () => {})
    advance(10_000)
    expect(container.querySelector('img')).toHaveAttribute('src', brand.scoutMotion.rest)
    expect(container.querySelector('.scout-frames')).toBeNull()
    expect(loaded).toEqual([])
  })

  it('is decoration, hidden from assistive technology', () => {
    motion(true)
    const { container } = render(<AnimatedScout />)
    expect(container.firstElementChild).toHaveAttribute('aria-hidden', 'true')
    expect(container.querySelector('img')).toHaveAttribute('alt', '')
  })

  it('waves hello once the frames load, gives a thumbs-up, rests, then blinks', async () => {
    motion(false)
    const { container } = render(<AnimatedScout />)
    await act(async () => {}) // decode resolves
    expect(loaded).toEqual([brand.scoutMotion.frames])
    expect(frame(container)).toBe('0')
    advance(HELLO_DELAY_MS)
    expect(frame(container)).toBe('2') // the wave's first step
    advance(1100)
    expect(frame(container)).toBe('5') // thumbs-up, straight after the wave
    advance(1800)
    expect(frame(container)).toBe('0') // back at rest
    advance(4250)
    expect(frame(container)).toBe('1') // a blink
    advance(130)
    expect(frame(container)).toBe('0')
  })

  it('gives a thumbs-up every fourth idle moment instead of blinking', async () => {
    motion(false)
    const { container } = render(<AnimatedScout />)
    await act(async () => {})
    advance(HELLO_DELAY_MS + 1040 + 1800) // hello and the first thumbs-up are over
    const seen: string[] = []
    for (let i = 0; i < 4; i++) {
      advance(4250) // idle moments are 4.25 s apart with Math.random at 0.5
      seen.push(frame(container)!)
    }
    expect(seen).toEqual(['1', '1', '1', '5'])
  })

  it('flaps and hops when tapped, through every wing step, but not in the middle of another move', async () => {
    motion(false)
    const { container } = render(<AnimatedScout />)
    await act(async () => {})
    const scout = container.firstElementChild as HTMLElement
    fireEvent.click(scout)
    expect(scout).toHaveClass('is-flapping')
    const steps = [frame(container)]
    for (let t = 10; t < FLAP_MS; t += 10) {
      advance(10)
      if (steps.at(-1) !== frame(container)) steps.push(frame(container))
    }
    // Up through all five steps, down to the second, up again, and down to rest: the wings never skip a step.
    expect(steps).toEqual(['6', '7', '8', '9', '10', '9', '8', '7', '8', '9', '10', '9', '8', '7', '6'])
    advance(10)
    expect(frame(container)).toBe('0')
    expect(scout).not.toHaveClass('is-flapping')
    // The hello wave is due now; a tap during it changes nothing.
    advance(HELLO_DELAY_MS - FLAP_MS)
    fireEvent.click(scout)
    expect(scout).not.toHaveClass('is-flapping')
    expect(frame(container)).toBe('2')
  })

  it('waits for a flap to finish before waving hello, and still goes on to blink', async () => {
    motion(false)
    const { container } = render(<AnimatedScout />)
    await act(async () => {})
    advance(HELLO_DELAY_MS - 200)
    fireEvent.click(container.firstElementChild as HTMLElement) // a flap that runs past the moment the hello is due
    advance(FLAP_MS + 150)
    expect(frame(container)).toBe('2') // the hello, a little late
    advance(1040 + 1800 + 4250)
    expect(frame(container)).toBe('1') // and the blinking has started
  })
})
