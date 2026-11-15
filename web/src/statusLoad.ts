/** A status page load that found nothing to show (an unknown, unpublished or no longer served page): the view says "not found", not "try again". */
export class StatusNotFound extends Error {}

/** Leaves for a status page's own domain when its owner chose that. An object, so tests can replace it. */
export const goTo = { url: (url: string) => window.location.replace(url) }
