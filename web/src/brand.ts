// Brand artwork: final production assets. They were made once (ChatGPT renders, cut out and resized outside this repository) and the originals are not kept; docs/branding.md lists what each file is and the specs to match if one is replaced.
import appIcon from './assets/brand/app-icon.png'
import dashboardLaptop from './assets/brand/dashboard-laptop.webp'
import iconAnalytics from './assets/brand/icon-analytics.png'
import iconDashboard from './assets/brand/icon-dashboard.png'
import iconEmail from './assets/brand/icon-email.png'
import iconHistory from './assets/brand/icon-history.png'
import iconMaintenance from './assets/brand/icon-maintenance.png'
import iconNotifications from './assets/brand/icon-notifications.png'
import iconSettings from './assets/brand/icon-settings.png'
import iconShield from './assets/brand/icon-shield.png'
import iconSsl from './assets/brand/icon-ssl.png'
import iconWebsites from './assets/brand/icon-websites.png'
import emblem from './assets/brand/emblem.png'
import screenAlertEmail from './assets/brand/screens/alert-email.webp'
import screenDashboard from './assets/brand/screens/dashboard.webp'
import screenIncidents from './assets/brand/screens/incidents.webp'
import monitoringEarth from './assets/brand/monitoring-earth-network.webp'
import monitoringHawkEye from './assets/brand/monitoring-hawk-eye.webp'
import audienceFreelancer from './assets/brand/audience-freelancer.png'
import audienceAgency from './assets/brand/audience-agency.png'
import audienceBusiness from './assets/brand/audience-business.png'
import logo from './assets/brand/logo.png'
import logoWhite from './assets/brand/logo-white.png'

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
  /** Background art for the /monitoring page: a gold hawk's eye on navy, and the night Earth with connection arcs. No text in either. */
  art: { hawkEye: monitoringHawkEye, earth: monitoringEarth },
  /** Real screens of the app with sample data (scripts/screenshots): the dashboard, the incidents list and the alert email. */
  screens: { dashboard: screenDashboard, incidents: screenIncidents, alertEmail: screenAlertEmail },
  /** Who it is for: one person, a group and a storefront, as free-standing gold-edged glyphs on transparent backgrounds. */
  audience: { freelancer: audienceFreelancer, agency: audienceAgency, business: audienceBusiness },
  /** Glossy gold-ringed round badges: each carries its own navy, so they work on any background. */
  icons: {
    websites: iconWebsites,
    email: iconEmail,
    analytics: iconAnalytics,
    history: iconHistory,
    shield: iconShield,
    settings: iconSettings,
    notifications: iconNotifications,
    ssl: iconSsl,
    dashboard: iconDashboard,
    maintenance: iconMaintenance,
  },
}
