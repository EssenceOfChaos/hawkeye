import { Link } from 'react-router'
import { brand } from '../brand'

/**
 * The official HawkEye logo (hawk, white "Hawk", gold "Eye"). The app is always on the dark navy theme, so the white version is used throughout.
 * `linkHome` makes it a link to the home page (the sign-in, sign-up and other account pages). Leave it off where the logo already sits inside a
 * link (the top bar) or may render outside the router (the error screen).
 */
export function BrandMark({ size = 34, linkHome = false }: { size?: number; linkHome?: boolean }) {
  const img = <img src={brand.logoWhite} alt={linkHome ? '' : 'HawkEye'} width={Math.round(size * 5.14)} height={size} />
  if (linkHome) {
    return <Link to="/" className="brandmark" aria-label="HawkEye home">{img}</Link>
  }
  return <span className="brandmark">{img}</span>
}

/** A feature icon shown beside a page heading; decorative. */
export function PageIcon({ src }: { src: string }) {
  return <img className="page-icon" src={src} alt="" width={44} height={44} />
}
