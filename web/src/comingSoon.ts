/**
 * Features that are coming soon, each with "Get notified": a waitlist whose sign-up counts show which to build first (internal/waitlist). Keep the
 * keys in step with waitlist.Features and the migration's CHECK. These are not plan features: they never appear on the pricing cards until built.
 */
export const COMING_SOON = [
  {
    key: 'status_page_subscribers',
    title: 'Email updates for status page visitors',
    text: 'Let customers subscribe to a status page and get an email when something goes down and when it’s fixed, without you sending a thing.',
  },
  {
    key: 'browser_checks',
    title: 'Browser checks',
    text: 'HawkEye opens your site in a real browser and goes through it like a customer: signing in, searching, checking out. When a step breaks, you get an alert with a screenshot of what went wrong. Also known as synthetic monitoring.',
  },
] as const
