import { brand } from '../brand'

/** The official HawkEye logo (hawk, white "Hawk", gold "Eye"). The app is always on the dark navy theme, so the white version is used throughout. */
export function BrandMark({ size = 34 }: { size?: number }) {
  return (
    <span className="brandmark">
      <img src={brand.logoWhite} alt="HawkEye" width={Math.round(size * 5.14)} height={size} />
    </span>
  )
}

/** A feature icon shown beside a page heading; decorative. */
export function PageIcon({ src }: { src: string }) {
  return <img className="page-icon" src={src} alt="" width={44} height={44} />
}
