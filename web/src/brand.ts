// Brand artwork: final production assets. They were made once (ChatGPT renders, cut out and resized outside this repository) and the originals are not kept; docs/branding.md lists what each file is and the specs to match if one is replaced.
import appIcon from './assets/brand/app-icon.png'
import dashboardLaptop from './assets/brand/dashboard-laptop.webp'
import iconAnalytics from './assets/brand/icon-analytics.png'
import iconDashboard from './assets/brand/icon-dashboard.png'
import iconDevelopers from './assets/brand/icon-developers.png'
import iconEmail from './assets/brand/icon-email.png'
import iconHistory from './assets/brand/icon-history.png'
import iconMaintenance from './assets/brand/icon-maintenance.png'
import iconNotifications from './assets/brand/icon-notifications.png'
import iconSettings from './assets/brand/icon-settings.png'
import iconBilling from './assets/brand/icon-billing.png'
import iconTeam from './assets/brand/icon-team.png'
import iconStatusPage from './assets/brand/icon-status-page.png'
import iconReports from './assets/brand/icon-reports.png'
import iconMonitors from './assets/brand/icon-monitors.png'
import iconShield from './assets/brand/icon-shield.png'
import iconSsl from './assets/brand/icon-ssl.png'
import iconWebsites from './assets/brand/icon-websites.png'
import iconCron from './assets/brand/icon-cron.png'
import iconIncident from './assets/brand/icon-incident.png'
import iconLocations from './assets/brand/icon-locations.png'
import iconPdfReports from './assets/brand/icon-pdf-reports.png'
import iconResponseTime from './assets/brand/icon-response-time.png'
import iconWarning from './assets/brand/icon-warning.png'
import iconSimple from './assets/brand/icon-simple.png'
import emblem from './assets/brand/emblem.png'
import screenAlertEmail from './assets/brand/screens/alert-email.webp'
import screenDashboard from './assets/brand/screens/dashboard.webp'
import screenIncidents from './assets/brand/screens/incidents.webp'
import monitoringEarth from './assets/brand/monitoring-earth-network.webp'
import monitoringHawkEye from './assets/brand/monitoring-hawk-eye.webp'
import audienceFreelancer from './assets/brand/audience-freelancer.png'
import audienceAgency from './assets/brand/audience-agency.png'
import audienceBusiness from './assets/brand/audience-business.png'
import audienceDeveloper from './assets/brand/audience-developer.png'
import scoutWave from './assets/brand/scout-wave.webp'
import scoutCheck from './assets/brand/scout-check.webp'
import scoutAlert from './assets/brand/scout-alert.webp'
import scoutStand from './assets/brand/scout-stand.webp'
import scoutRest from './assets/brand/scout-rest.webp'
import scoutFrames from './assets/brand/scout-frames.webp'
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
  /**
   * Scout, the mascot: a navy and gold hawk, full body, on transparent 640x640 squares with his feet on the bottom edge. Render him with
   * the Scout component. wave: greeting (onboarding, empty states); check: thumbs-up with a green check (things are fine); stand: neutral,
   * gesturing with one wing; alert: holding a red warning beacon (use sparingly, never in a real outage alert).
   */
  scout: { wave: scoutWave, check: scoutCheck, stand: scoutStand, alert: scoutAlert },
  /**
   * Scout in motion, for the AnimatedScout component. rest: him standing, wings folded (400x400, shown first and to people who prefer
   * reduced motion). frames: eleven 400x400 frames side by side, his body lined up across all of them: rest, blink, three steps of a wave,
   * a thumbs-up with a green check, and five steps of raising his wings.
   */
  scoutMotion: { rest: scoutRest, frames: scoutFrames, count: 11 },
  /** Background art for the /monitoring page: a gold hawk's eye on navy, and the night Earth with connection arcs. No text in either. */
  art: { hawkEye: monitoringHawkEye, earth: monitoringEarth },
  /** Real screens of the app with sample data (scripts/screenshots): the dashboard, the incidents list and the alert email. */
  screens: { dashboard: screenDashboard, incidents: screenIncidents, alertEmail: screenAlertEmail },
  /** Who it is for: one person, a group and a storefront, as free-standing gold-edged glyphs on transparent backgrounds. */
  audience: { freelancer: audienceFreelancer, agency: audienceAgency, business: audienceBusiness, developer: audienceDeveloper },
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
    developers: iconDevelopers,
    maintenance: iconMaintenance,
    billing: iconBilling,
    team: iconTeam,
    statusPage: iconStatusPage,
    reports: iconReports,
    monitors: iconMonitors,
    cron: iconCron,
    incident: iconIncident,
    locations: iconLocations,
    pdfReports: iconPdfReports,
    responseTime: iconResponseTime,
    warning: iconWarning, // the Incidents page (the bell is for notifications)
    simple: iconSimple, // the About page's "Simple" principle
  },
}

/** HawkEye's own status page. It runs on Cloudflare, separately from HawkEye (status/), so it answers when HawkEye doesn't. */
export const STATUS_URL = 'https://status.hawkeye.observer'
