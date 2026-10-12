// Brand artwork. Sources and the cleanup applied to them: scripts/prepare_brand_assets.py.
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
import logo from './assets/brand/logo.png'
import websiteMagnifier from './assets/brand/website-magnifier.png'

export const brand = {
  /** Emblem + wordmark. The wordmark is dark navy: only place it on light backgrounds. */
  logo,
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
