import type { CSSProperties } from 'react'
import { brand } from '../brand'

export type ScoutPose = keyof typeof brand.scout

/**
 * Scout, HawkEye's mascot. He is decoration: by default the image is hidden from assistive technology, so pass `alt` only where he
 * adds meaning the surrounding text does not already say. A soft gold glow sits behind him so his navy feathers stand out from the navy
 * page. Keep him out of the dense working screens and out of real outage alerts (docs/branding.md).
 */
export function Scout({ pose, size = 160, alt = '', className = '', eager = false }: { pose: ScoutPose; size?: number; alt?: string; className?: string; eager?: boolean }) {
  return (
    <span className={`scout ${className}`.trim()} style={{ '--scout-size': `${size}px` } as CSSProperties}>
      <img src={brand.scout[pose]} alt={alt} width={size} height={size} loading={eager ? 'eager' : 'lazy'} decoding="async" />
    </span>
  )
}
