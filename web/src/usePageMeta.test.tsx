import { render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { PAGES } from './seo'
import { findPage, usePageMeta } from './usePageMeta'

const page = (path: string) => PAGES.find((p) => p.path === path)!
const robots = () => document.head.querySelector('meta[name="robots"]')
const canonical = () => document.head.querySelector('link[rel="canonical"]')
const description = () => document.head.querySelector('meta[name="description"]')?.getAttribute('content')

function Probe({ path }: { path: string }) {
  usePageMeta(findPage(path))
  return null
}

beforeEach(() => {
  document.head.innerHTML = '<title>shell</title><meta name="description" content="shell description" />'
})
afterEach(() => {
  document.head.innerHTML = ''
})

describe('findPage', () => {
  it('finds a page by its address, ignoring a trailing slash and letter case', () => {
    expect(findPage('/contact')?.title).toBe(page('/contact').title)
    expect(findPage('/contact/')?.title).toBe(page('/contact').title)
    expect(findPage('/Contact')?.title).toBe(page('/contact').title)
    expect(findPage('/')?.path).toBe('/')
  })

  it('matches monitor pages with an id, and leaves status pages and unknown addresses to name themselves', () => {
    expect(findPage('/monitors/abc-123')?.path).toBe('/monitors/:monitorID')
    expect(findPage('/monitors/abc-123/edit')?.path).toBe('/monitors/:monitorID/edit')
    expect(findPage('/monitors/new')?.path).toBe('/monitors/new')
    expect(findPage('/status/acme')).toBeUndefined()
    expect(findPage('/nope')).toBeUndefined()
  })
})

describe('usePageMeta', () => {
  it('sets title, description and canonical for an indexable page', () => {
    render(<Probe path="/contact" />)
    expect(document.title).toBe(page('/contact').title)
    expect(description()).toBe(page('/contact').description)
    expect(canonical()?.getAttribute('href')).toBe('https://hawkeye.observer/contact')
    expect(robots()).toBeNull()
  })

  it('asks search engines to skip a page that is not for them', () => {
    render(<Probe path="/monitoring" />)
    expect(document.title).toBe('HawkEye: Know Before Your Customers Do')
    expect(robots()?.getAttribute('content')).toBe('noindex')
    expect(canonical()).toBeNull()
  })

  it('does not stack tags when the page changes, and removes them when it is left', () => {
    const { rerender, unmount } = render(<Probe path="/monitoring" />)
    rerender(<Probe path="/contact" />)
    expect(document.title).toBe(page('/contact').title)
    expect(robots()).toBeNull()
    expect(document.head.querySelectorAll('meta[name="description"]')).toHaveLength(1)
    expect(document.head.querySelectorAll('link[rel="canonical"]')).toHaveLength(1)
    unmount()
    expect(canonical()).toBeNull()
    expect(robots()).toBeNull()
  })

  it('takes over the tags a prerendered page already has instead of adding a second set', () => {
    document.head.insertAdjacentHTML('beforeend', '<link rel="canonical" href="https://hawkeye.observer/stale" /><meta name="robots" content="noindex" />')
    render(<Probe path="/privacy" />)
    expect(document.head.querySelectorAll('link[rel="canonical"]')).toHaveLength(1)
    expect(canonical()?.getAttribute('href')).toBe('https://hawkeye.observer/privacy')
    expect(robots()).toBeNull() // an indexable page must not keep a noindex left from the page before it
  })

  it('leaves the head alone for an address that names itself', () => {
    render(<Probe path="/status/acme" />)
    expect(document.title).toBe('shell')
    expect(description()).toBe('shell description')
  })
})
