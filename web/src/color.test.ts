import { describe, expect, it } from 'vitest'
import { contrast, readableTextOn } from './color'

describe('readableTextOn', () => {
  it('picks the text colour that reads best', () => {
    expect(readableTextOn('#ffffff')).toBe('#000000')
    expect(readableTextOn('#000000')).toBe('#ffffff')
    expect(readableTextOn('#ffeb3b')).toBe('#000000') // pale yellow
    expect(readableTextOn('#1a237e')).toBe('#ffffff') // navy
  })

  it('always reaches the WCAG AA contrast for normal text, whatever the accent colour', () => {
    for (let v = 0; v < 0x1000000; v += 0x010307) {
      const bg = `#${v.toString(16).padStart(6, '0')}`
      expect(contrast(bg, readableTextOn(bg)), bg).toBeGreaterThanOrEqual(4.5)
    }
  })
})
