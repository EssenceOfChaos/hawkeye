import { render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const revealOnScroll = vi.fn()
vi.mock('../motion', () => ({
  prefersReducedMotion: () => false,
  inScope: (_root: HTMLElement, setup: () => void) => {
    setup()
    return () => {}
  },
  revealOnScroll: (group: HTMLElement) => revealOnScroll(Array.from(group.querySelectorAll('[data-reveal]')).map((e) => e.textContent)),
}))

import { Reveal } from './Reveal'

function Cards({ extra }: { extra: boolean }) {
  return (
    <Reveal as="ul">
      <li data-reveal>one</li>
      {extra && <li data-reveal>late</li>}
    </Reveal>
  )
}

describe('Reveal', () => {
  beforeEach(() => revealOnScroll.mockClear())
  afterEach(() => vi.clearAllMocks())

  it('animates the items it has when it mounts', () => {
    render(<Cards extra={false} />)
    expect(revealOnScroll).toHaveBeenCalledWith(['one'])
  })

  // The multi-location card arrives with the plans, after the section mounted; it stayed at opacity 0 until this was handled.
  it('sets the fade-in up again for an item that arrives later, so it does not stay hidden', async () => {
    const { rerender, container } = render(<Cards extra={false} />)
    rerender(<Cards extra />)
    await waitFor(() => expect(revealOnScroll).toHaveBeenLastCalledWith(['one', 'late']))
    expect(container.querySelector('ul')).toHaveClass('reveal-armed')
  })

  it('leaves the animation alone when the content changes but no item is added', async () => {
    const { rerender } = render(<Cards extra={false} />)
    rerender(<Cards extra={false} />)
    await new Promise((r) => setTimeout(r, 0))
    expect(revealOnScroll).toHaveBeenCalledTimes(1)
  })
})
