import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { brand } from '../brand'
import { Scout } from './Scout'

describe('Scout', () => {
  it('is decorative by default, so screen readers skip him', () => {
    const { container } = render(<Scout pose="wave" />)
    const img = container.querySelector('img')!
    expect(img).toHaveAttribute('alt', '')
    expect(img).toHaveAttribute('src', brand.scout.wave)
    expect(img).toHaveAttribute('loading', 'lazy')
    expect(screen.queryByRole('img')).toBeNull() // alt="" gives the image the presentation role
  })

  it('takes alt text where he carries meaning, and loads eagerly above the fold', () => {
    render(<Scout pose="check" alt="Scout giving a thumbs-up" eager size={120} />)
    const img = screen.getByRole('img', { name: 'Scout giving a thumbs-up' })
    expect(img).toHaveAttribute('loading', 'eager')
    expect(img).toHaveAttribute('width', '120')
    expect(img.parentElement).toHaveStyle({ '--scout-size': '120px' })
  })

  it('has every pose', () => {
    expect(Object.keys(brand.scout).sort()).toEqual(['alert', 'check', 'stand', 'wave'])
  })
})
