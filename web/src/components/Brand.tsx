import { brand } from '../brand'

/** App icon plus a text wordmark, so it stays readable in light and dark themes. */
export function BrandMark({ size = 32 }: { size?: number }) {
  return (
    <span className="brandmark">
      <img src={brand.appIcon} alt="" width={size} height={size} />
      <span className="wordmark">
        Hawk<span>Eye</span>
      </span>
    </span>
  )
}

/** A feature icon shown beside a page heading; decorative. */
export function PageIcon({ src }: { src: string }) {
  return <img className="page-icon" src={src} alt="" width={44} height={44} />
}
