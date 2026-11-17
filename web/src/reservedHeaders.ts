/**
 * Request headers HawkEye sets itself, so a check may not set them (reservedHeaders in internal/monitors/monitors.go, without Authorization, which
 * goes through credentials). TestReservedHeadersMatchTheWebApp keeps this list and the server's the same.
 */
export const RESERVED_HEADERS: readonly string[] = [
  'Host', 'Content-Length', 'Transfer-Encoding', 'Connection', 'Upgrade', 'Te', 'Trailer', 'Keep-Alive', 'Proxy-Authorization', 'Proxy-Connection',
  'Accept-Encoding', 'User-Agent', 'Cookie2', 'Expect',
]
