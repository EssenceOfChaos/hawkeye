import { readSpec } from './openapi'

/** Reads a spec off the page's main thread (openapiRead.ts), so a huge or hostile file can't freeze the tab. */
self.onmessage = (e: MessageEvent<string>) => {
  ;(self as unknown as { postMessage(m: unknown): void }).postMessage(readSpec(e.data))
}
