/**
 * Features that are coming soon, each with "Get notified": a waitlist whose sign-up counts show which to build first (internal/waitlist). Keep the
 * keys in step with waitlist.Features and the migration's CHECK. These are not plan features: they never appear on the pricing cards until built.
 */
export const COMING_SOON = [
  {
    key: 'status_page_domains',
    title: 'Status pages on your own domain',
    text: 'Serve each status page at an address like status.yourbrand.com, with a certificate HawkEye looks after, so clients see your brand, not ours.',
  },
  {
    key: 'status_page_subscribers',
    title: 'Email updates for status page visitors',
    text: 'Let customers subscribe to a status page and get an email when something goes down and when it’s fixed, without you sending a thing.',
  },
] as const
