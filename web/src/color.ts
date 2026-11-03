/** Relative luminance of a #rrggbb color (WCAG 2). */
function luminance(hex: string): number {
  const channel = (i: number) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  }
  return 0.2126 * channel(1) + 0.7152 * channel(3) + 0.0722 * channel(5)
}

/** The WCAG contrast ratio between two #rrggbb colors (1 to 21). */
export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

/**
 * Black or white, whichever reads better on the given background. One of the two always reaches at least 4.58:1 against any color (above the
 * WCAG AA minimum of 4.5:1 for normal text), so a customer's accent color can never make a status page header unreadable.
 */
export function readableTextOn(background: string): '#000000' | '#ffffff' {
  return contrast(background, '#000000') >= contrast(background, '#ffffff') ? '#000000' : '#ffffff'
}

export const isHexColor = (s: string) => /^#[0-9a-f]{6}$/i.test(s)
