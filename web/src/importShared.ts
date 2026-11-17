import type { HttpMethod } from './api'
import { RESERVED_HEADERS } from './reservedHeaders'

/** Helpers shared by the OpenAPI reader (openapi.ts) and the Postman reader (postman.ts). */

export type Obj = Record<string, unknown>
export const isObj = (v: unknown): v is Obj => typeof v === 'object' && v !== null && !Array.isArray(v)
export const str = (v: unknown): string | undefined => (typeof v === 'string' ? v : undefined)

export const METHODS: readonly HttpMethod[] = ['GET', 'HEAD', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS']
export const WRITES = new Set<HttpMethod>(['POST', 'PUT', 'PATCH', 'DELETE'])
/** Paths that look like health or status endpoints, which are offered first. */
export const HEALTH = /(^|\/)(health|healthz|healthcheck|health-check|status|ping|ready|readyz|readiness|live|livez|liveness|alive|heartbeat)(\/|$|\?)/i
/** Headers an imported check can't set: the ones HawkEye sets itself, and Authorization, which goes through credentials. */
export const RESERVED = new Set([...RESERVED_HEADERS.map((h) => h.toLowerCase()), 'authorization'])

/** Turns an example value into the text a form field holds. */
export function asText(v: unknown): string | undefined {
  if (v === undefined || v === null) return undefined
  if (typeof v === 'string') return v
  if (typeof v === 'number' || typeof v === 'boolean') return String(v)
  return undefined
}
