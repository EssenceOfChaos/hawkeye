import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Flag, LocationName, flagCountries } from './components/Flag'

// The check locations' one source (scripts/regions.py, infra/workers.tf read it too).
import regionsFile from '../../infra/regions.json?raw'

const regions = JSON.parse(regionsFile) as {
  primary: { country?: string }
  workers: Record<string, { country?: string }>
}

describe('location flags', () => {
  it('has a flag for every country a check location names', () => {
    const countries = [regions.primary, ...Object.values(regions.workers)].map((r) => r.country).filter(Boolean)
    expect(countries.length).toBeGreaterThan(0)
    for (const c of countries) expect(flagCountries(), `add web/src/assets/flags/${c}.svg`).toContain(c)
  })

  it('shows the flag as decoration beside the name, and nothing for an unknown country', () => {
    const { container } = render(<LocationName name="Amsterdam" country="nl" />)
    const img = container.querySelector('img.flag')!
    expect(img.getAttribute('alt')).toBe('') // the name says it in text
    expect(img.getAttribute('src')).toMatch(/NL\.svg|A2001D/i) // a file URL, or inlined by Vite with the Dutch red
    expect(container.textContent).toBe('Amsterdam')
    expect(render(<Flag country="ZZ" />).container.innerHTML).toBe('')
    expect(render(<Flag />).container.innerHTML).toBe('')
  })
})
