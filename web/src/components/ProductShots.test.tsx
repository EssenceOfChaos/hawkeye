import { render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ProductShots } from './ProductShots'

afterEach(() => vi.unstubAllGlobals())

function stubMotion({ reduce }: { reduce: boolean }) {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: query.includes('prefers-reduced-motion') ? reduce : false, // no hover-capable pointer either, so no tilt listeners
    media: query, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false, onchange: null,
  }))
  vi.stubGlobal('IntersectionObserver', class { observe() {} unobserve() {} disconnect() {} takeRecords() { return [] } })
}

describe('ProductShots', () => {
  it('always shows the three screens, labelled as sample data, whatever the motion setting', () => {
    const { container } = render(<ProductShots />)
    expect(screen.getByRole('heading', { name: 'See it in action.' })).toBeInTheDocument()
    expect(screen.getByText(/shown with sample data/)).toBeInTheDocument()
    const imgs = [...container.querySelectorAll('.shot img')]
    expect(imgs).toHaveLength(3)
    imgs.forEach((img) => expect(img.getAttribute('alt')).toMatch(/sample data/i))
    expect(container.querySelectorAll('figcaption')).toHaveLength(3)
  })

  it('decorative motion pieces are hidden from assistive technology', () => {
    const { container } = render(<ProductShots />)
    container.querySelectorAll('.shot-sheen, .shot-ping').forEach((el) => expect(el).toHaveAttribute('aria-hidden', 'true'))
  })

  it('stays completely still when the visitor prefers reduced motion: nothing hidden, no motion classes, no ping', () => {
    stubMotion({ reduce: true })
    const { container } = render(<ProductShots />)
    const group = container.querySelector('.shots')!
    expect(group.classList.contains('shots-animated')).toBe(false)
    expect(group.classList.contains('reveal-armed')).toBe(false) // the class that hides figures until they animate in
  })

  it('arms the motion otherwise, and puts everything back when it unmounts', () => {
    stubMotion({ reduce: false })
    const { container, unmount } = render(<ProductShots />)
    const group = container.querySelector('.shots')!
    expect(group.classList.contains('shots-animated')).toBe(true)
    expect(group.classList.contains('reveal-armed')).toBe(true)
    unmount()
    expect(group.classList.contains('shots-animated')).toBe(false)
    expect(group.classList.contains('reveal-armed')).toBe(false)
  })
})
