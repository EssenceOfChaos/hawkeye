import { readSpec, type SpecResult } from './openapi'

/**
 * Reads a spec in a Web Worker and gives up after timeoutMs, stopping the worker, so a file that takes too long can't hang the page. Where workers
 * aren't available (tests, very old browsers) it reads on the page instead; readSpec has its own size and alias limits.
 */
export function readSpecInBackground(text: string, timeoutMs = 20_000): Promise<SpecResult> {
  if (typeof Worker === 'undefined') return Promise.resolve(readSpec(text))
  return new Promise((resolve) => {
    let worker: Worker
    try {
      worker = new Worker(new URL('./openapi.worker.ts', import.meta.url), { type: 'module' })
    } catch {
      resolve(readSpec(text))
      return
    }
    const done = (r: SpecResult) => {
      clearTimeout(timer)
      worker.terminate()
      resolve(r)
    }
    const timer = setTimeout(() => done({ ok: false, error: 'Reading the spec took too long. Try a smaller file.' }), timeoutMs)
    worker.onmessage = (e: MessageEvent<SpecResult>) => done(e.data)
    worker.onerror = () => done({ ok: false, error: 'The spec couldn’t be read. Check that it’s a complete OpenAPI or Swagger file.' })
    worker.postMessage(text)
  })
}
