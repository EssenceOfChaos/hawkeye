// Brand artwork. The logo, emblem and icons come from scripts/prepare_glossy_logos.py; the rest from scripts/prepare_brand_assets.py.
import alertBell from './assets/brand/alert-bell.png'
import appIcon from './assets/brand/app-icon.png'
import cloudSecurity from './assets/brand/cloud-security.png'
import dashboardLaptop from './assets/brand/dashboard-laptop.png'
import iconAnalytics from './assets/brand/icon-analytics.png'
import iconEmail from './assets/brand/icon-email.png'
import iconHistory from './assets/brand/icon-history.png'
import iconSettings from './assets/brand/icon-settings.png'
import iconShield from './assets/brand/icon-shield.png'
import iconWebsites from './assets/brand/icon-websites.png'
import emblem from './assets/brand/emblem.png'
import logo from './assets/brand/logo.png'
import logoWhite from './assets/brand/logo-white.png'
import websiteMagnifier from './assets/brand/website-magnifier.png'

export const brand = {
  /** Hawk + wordmark. The wordmark is dark navy: only place it on light backgrounds. */
  logo,
  /** Hawk + wordmark with a white "Hawk", for dark backgrounds. */
  logoWhite,
  /** The hawk head alone (transparent, works on any background). */
  emblem,
  /** Square app icon with its own navy background; safe on any theme. */
  appIcon,
  dashboardLaptop,
  cloudSecurity,
  websiteMagnifier,
  alertBell,
  icons: {
    websites: iconWebsites,
    email: iconEmail,
    analytics: iconAnalytics,
    history: iconHistory,
    shield: iconShield,
    settings: iconSettings,
  },
}
