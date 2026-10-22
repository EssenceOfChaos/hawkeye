const NAMES: Record<number, string> = {
  200: 'OK', 201: 'Created', 202: 'Accepted', 204: 'No Content',
  301: 'Moved Permanently', 302: 'Found', 303: 'See Other', 307: 'Temporary Redirect', 308: 'Permanent Redirect',
  400: 'Bad Request', 401: 'Unauthorized', 403: 'Forbidden', 404: 'Not Found', 405: 'Method Not Allowed', 406: 'Not Acceptable', 408: 'Request Timeout',
  410: 'Gone', 418: "I'm a teapot", 429: 'Too Many Requests', 451: 'Unavailable For Legal Reasons',
  500: 'Internal Server Error', 501: 'Not Implemented', 502: 'Bad Gateway', 503: 'Service Unavailable', 504: 'Gateway Timeout', 520: 'Unknown Error',
  521: 'Web Server Is Down', 522: 'Connection Timed Out', 523: 'Origin Is Unreachable', 524: 'A Timeout Occurred', 525: 'SSL Handshake Failed', 526: 'Invalid SSL Certificate',
}

/** "200 OK", "503 Service Unavailable"; just the number for a code without a well-known name. */
export function statusLabel(code: number): string {
  const name = NAMES[code]
  return name ? `${code} ${name}` : String(code)
}
