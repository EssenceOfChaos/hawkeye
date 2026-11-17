import { BODY_METHODS, HTTP_METHODS, type AuthType, type HttpMethod, type RequestHeader } from './api'
import { RESERVED_HEADERS } from './reservedHeaders'

/**
 * Reads a pasted cURL command into the fields of an API check, entirely in the browser: nothing pasted is sent anywhere until the person saves.
 *
 * It understands the shell syntax people paste (quotes, $'…', line continuations, Chrome's "Copy as cURL (cmd)" with ^ escapes, curl.exe) and the
 * options that say what request to send. Options that only change curl's output are ignored, and everything that was moved, left out or ignored is
 * reported, so the form never silently differs from the command. See docs/plan-api-import.md.
 */

export interface CurlAuth {
  type: AuthType
  username?: string
  headerName?: string
  secret?: string
}

export interface CurlNote {
  /** What it is about: an option such as --compressed, or a header name. */
  subject: string
  text: string
}

export interface CurlRequest {
  url: string
  method: HttpMethod
  /** Headers to send as typed. */
  headers: RequestHeader[]
  body: string
  auth: CurlAuth
  /** From --max-time, in whole seconds within what HawkEye allows (1 to 30). */
  timeoutSeconds?: number
  /** Headers left out by default that the person may put back: browser extras, cookies and conditional requests. */
  omitted: RequestHeader[]
  /** Things that would stop the check from working as pasted (a private address, a missing upload). The form is still filled in. */
  warnings: CurlNote[]
  /** Everything else worth knowing: moved credentials, reserved headers, ignored options. */
  notes: CurlNote[]
}

export type CurlResult = { ok: true; request: CurlRequest } | { ok: false; error: string }

const RESERVED = new Set(RESERVED_HEADERS.map((h) => h.toLowerCase()))

/** Headers a browser adds to "Copy as cURL" that an API check doesn't need, and ones that would change the answer (a 304 from If-None-Match). */
function isBrowserExtra(name: string): boolean {
  const n = name.toLowerCase()
  return n.startsWith('sec-') || n.startsWith('if-') || ['accept-language', 'priority', 'pragma', 'cache-control', 'dnt', 'referer', 'origin',
    'upgrade-insecure-requests', 'cookie', 'x-requested-with'].includes(n)
}

/** Header names that usually carry a credential, which belongs in the encrypted credentials field rather than a plain header. */
const CREDENTIAL_NAME = /(token|secret|api[-_]?key|apikey|subscription-key|password|passwd|auth)/i

/** Options that take a value. Anything else that starts with - is a flag. */
const WITH_VALUE = new Set([
  '-X', '--request', '-H', '--header', '-d', '--data', '--data-raw', '--data-binary', '--data-ascii', '--data-urlencode', '--json', '-u', '--user',
  '-m', '--max-time', '-b', '--cookie', '-e', '--referer', '-A', '--user-agent', '--url', '-F', '--form', '--form-string', '-o', '--output',
  '--connect-timeout', '-w', '--write-out', '--retry', '--retry-delay', '--retry-max-time', '-x', '--proxy', '-U', '--proxy-user', '-T', '--upload-file',
  '--cacert', '--capath', '-E', '--cert', '--key', '--cert-type', '--key-type', '--resolve', '--connect-to', '-K', '--config', '--oauth2-bearer',
  '--aws-sigv4', '-r', '--range', '--limit-rate', '--max-redirs', '-c', '--cookie-jar', '-D', '--dump-header', '--interface', '--variable',
  '--expect100-timeout', '--keepalive-time', '--max-filesize', '--ciphers', '--tls-max', '--trace', '--trace-ascii', '--stderr', '-Y', '--speed-limit',
  '-y', '--speed-time', '-z', '--time-cond', '--dns-servers', '--doh-url', '--preproxy', '--unix-socket', '--abstract-unix-socket', '-P', '--ftp-port',
  '-Q', '--quote', '--mail-from', '--mail-rcpt', '-t', '--telnet-option', '--happy-eyeballs-timeout-ms', '--noproxy', '--request-target',
])

/** Flags that don't change what is checked (output, progress, protocol version), so ignoring them loses nothing. Reported only as a count. */
const HARMLESS = new Set([
  '-s', '--silent', '-S', '--show-error', '-L', '--location', '-v', '--verbose', '-i', '--include', '--compressed', '-f', '--fail', '--fail-with-body',
  '-N', '--no-buffer', '--http1.1', '--http2', '--http2-prior-knowledge', '--http3', '-4', '--ipv4', '-6', '--ipv6', '-#', '--progress-bar', '-g',
  '--globoff', '--location-trusted', '-O', '--remote-name', '--raw', '--path-as-is', '--no-progress-meter', '--tr-encoding', '--ssl-no-revoke',
  '--tlsv1', '--tlsv1.0', '--tlsv1.1', '--tlsv1.2', '--tlsv1.3', '-Z', '--parallel', '-q', '--disable', '-o', '--output', '-w', '--write-out',
  '-D', '--dump-header', '-c', '--cookie-jar', '--connect-timeout', '--retry', '--retry-delay', '--retry-max-time', '--trace', '--trace-ascii',
  '--stderr', '-l', '--list-only', '-0', '--http1.0', '--max-redirs', '--keepalive-time', '--expect100-timeout', '--happy-eyeballs-timeout-ms',
  '--styled-output', '--no-styled-output', '--create-dirs', '-R', '--remote-time', '-J', '--remote-header-name',
])

/** Reads one shell word list. Returns null when a quote is never closed. */
export function shellWords(input: string): string[] | null {
  let s = input.replace(/\r\n?/g, '\n')
  // Chrome's "Copy as cURL (cmd)" escapes with ^ (^" for a quote, ^\^" for a quote inside one) and continues lines with ^.
  if (/\^"/.test(s)) s = s.replace(/\^\n/g, ' ').replace(/\^(.)/gs, '$1')
  // Line continuations: \ (bash and zsh), ` (PowerShell).
  s = s.replace(/\\\n/g, ' ').replace(/`\n/g, ' ')

  const words: string[] = []
  let cur = ''
  let inWord = false
  let i = 0
  const n = s.length
  while (i < n) {
    const c = s[i]
    if (c === ' ' || c === '\t' || c === '\n') {
      if (inWord) words.push(cur)
      cur = ''
      inWord = false
      i++
      continue
    }
    inWord = true
    if (c === "'") {
      const end = s.indexOf("'", i + 1)
      if (end < 0) return null
      cur += s.slice(i + 1, end)
      i = end + 1
    } else if (c === '$' && s[i + 1] === "'") {
      i += 2
      let closed = false
      while (i < n) {
        const d = s[i]
        if (d === "'") { closed = true; i++; break }
        if (d === '\\' && i + 1 < n) {
          const e = s[i + 1]
          const simple: Record<string, string> = { n: '\n', t: '\t', r: '\r', '\\': '\\', "'": "'", '"': '"', a: '\x07', b: '\b', e: '\x1b', f: '\f', v: '\v', '0': '\0' }
          if (e in simple) { cur += simple[e]; i += 2; continue }
          const hex = e === 'x' ? /^[0-9a-fA-F]{1,2}/.exec(s.slice(i + 2)) : e === 'u' ? /^[0-9a-fA-F]{1,4}/.exec(s.slice(i + 2)) : null
          if (hex) { cur += String.fromCodePoint(parseInt(hex[0], 16)); i += 2 + hex[0].length; continue }
          cur += '\\' + e
          i += 2
          continue
        }
        cur += d
        i++
      }
      if (!closed) return null
    } else if (c === '"') {
      i++
      let closed = false
      while (i < n) {
        const d = s[i]
        if (d === '"') { closed = true; i++; break }
        if (d === '\\' && i + 1 < n && '"\\$`\n'.includes(s[i + 1])) { cur += s[i + 1]; i += 2; continue }
        if (d === '`' && i + 1 < n && s[i + 1] === '"') { cur += '"'; i += 2; continue } // PowerShell's escaped quote
        cur += d
        i++
      }
      if (!closed) return null
    } else if (c === '\\' && i + 1 < n) {
      cur += s[i + 1]
      i += 2
    } else {
      cur += c
      i++
    }
  }
  if (inWord) words.push(cur)
  return words
}

function header(raw: string): RequestHeader | null {
  // "Name: value"; "Name;" sends an empty header; "Name:" with nothing after it removes a header in curl, so there is nothing to send.
  const semi = /^([^:;\s]+);\s*$/.exec(raw)
  if (semi) return { name: semi[1], value: '' }
  const at = raw.indexOf(':')
  if (at <= 0) return null
  const name = raw.slice(0, at).trim()
  const value = raw.slice(at + 1).trim()
  if (value === '') return null
  return { name, value }
}

function decodeBasic(token: string): { username: string; password: string } | null {
  try {
    const plain = atob(token.trim())
    const at = plain.indexOf(':')
    if (at < 0) return null
    return { username: plain.slice(0, at), password: plain.slice(at + 1) }
  } catch {
    return null
  }
}

function urlencodeArg(v: string): string {
  // --data-urlencode: "name=content" encodes content; "=content" and "content" encode all of it. @file forms are handled by the caller.
  const eq = v.indexOf('=')
  if (eq > 0) return `${v.slice(0, eq)}=${encodeURIComponent(v.slice(eq + 1))}`
  return encodeURIComponent(eq === 0 ? v.slice(1) : v)
}

/** Addresses HawkEye can't reach from the internet, matching the server's early URL checks (the checker enforces the real rule). */
export function privateHostReason(u: URL): string | null {
  const host = u.hostname.replace(/^\[|\]$/g, '').toLowerCase()
  if (host === 'localhost' || host.endsWith('.localhost') || host.endsWith('.local') || host.endsWith('.internal') || host.endsWith('.home.arpa')) {
    return `${host} is only reachable on your own machine or network.`
  }
  if (!host.includes('.') && !host.includes(':')) return `${host} is a name on a private network.`
  const v4 = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(host)
  if (v4) {
    const [a, b] = [Number(v4[1]), Number(v4[2])]
    if (a === 127 || a === 10 || a === 0 || (a === 172 && b >= 16 && b <= 31) || (a === 192 && b === 168) || (a === 169 && b === 254) || (a === 100 && b >= 64 && b <= 127)) {
      return `${host} is a private address.`
    }
  }
  if (host === '::1' || host.startsWith('fc') || host.startsWith('fd') || host.startsWith('fe80')) return `${host} is a private address.`
  return null
}

export interface ParseOptions {
  /** Whether the form being filled has a timeout field (workflow steps do; website monitors use the default). */
  timeout?: boolean
}

/** Reads a cURL command. */
export function parseCurl(input: string, opts: ParseOptions = {}): CurlResult {
  const text = input.trim().replace(/^\$\s+/, '') // a copied shell prompt
  if (text === '') return { ok: false, error: 'Paste a cURL command first.' }
  const words = shellWords(text)
  if (!words) return { ok: false, error: 'A quote in the command is never closed. Copy the whole command and try again.' }
  if (words.length === 0 || !/^(.*[/\\])?curl(\.exe)?$/i.test(words[0])) {
    return { ok: false, error: 'That doesn’t look like a cURL command. It should start with curl.' }
  }

  const notes: CurlNote[] = []
  const warnings: CurlNote[] = []
  const urls: string[] = []
  const rawHeaders: RequestHeader[] = []
  const data: string[] = []
  let method: string | undefined
  let head = false
  let get = false
  let json = false
  let user: string | undefined
  let oauthBearer: string | undefined
  let cookie: string | undefined
  let maxTime: number | undefined
  let ignored = 0

  const take = (opt: string, value: string) => {
    switch (opt) {
      case '-X': case '--request': method = value.toUpperCase(); break
      case '-H': case '--header': {
        const h = header(value)
        if (h) rawHeaders.push(h)
        break
      }
      case '-d': case '--data': case '--data-ascii': case '--data-binary': case '--data-raw': case '--json': case '--data-urlencode':
        if (opt === '--json') json = true
        if (value.startsWith('@') && opt !== '--data-raw') {
          warnings.push({ subject: opt, text: `The body comes from a file (${value.slice(1) || 'standard input'}), which HawkEye can’t read. Paste the body into the form instead.` })
        } else {
          data.push(opt === '--data-urlencode' ? urlencodeArg(value) : value)
        }
        break
      case '-u': case '--user': user = value; break
      case '--oauth2-bearer': oauthBearer = value; break
      case '-m': case '--max-time': maxTime = Number(value); break
      case '-b': case '--cookie':
        if (value.includes('=')) cookie = value
        else notes.push({ subject: opt, text: 'Cookies from a file can’t be used.' })
        break
      case '-e': case '--referer': rawHeaders.push({ name: 'Referer', value: value.replace(/;auto$/, '') }); break
      case '-A': case '--user-agent': notes.push({ subject: opt, text: 'Left out: HawkEye always identifies itself with its own User-Agent.' }); break
      case '--url': urls.push(value); break
      case '-F': case '--form': case '--form-string':
        warnings.push({ subject: opt, text: 'Form uploads (multipart) aren’t supported, so this part of the request was left out.' })
        break
      case '-T': case '--upload-file':
        warnings.push({ subject: opt, text: 'File uploads aren’t supported, so the file was left out.' })
        break
      case '-K': case '--config':
        warnings.push({ subject: opt, text: 'Options from a config file can’t be read. Add them to the command and paste it again.' })
        break
      case '-x': case '--proxy': case '-U': case '--proxy-user': case '--preproxy':
        notes.push({ subject: opt, text: 'HawkEye checks directly from its own servers, not through your proxy.' })
        break
      case '--resolve': case '--connect-to':
        notes.push({ subject: opt, text: 'HawkEye looks up the address in public DNS; overrides aren’t used.' })
        break
      case '-E': case '--cert': case '--key': case '--cacert': case '--capath': case '--cert-type': case '--key-type':
        warnings.push({ subject: opt, text: 'Client certificates and custom certificate authorities aren’t supported.' })
        break
      case '--aws-sigv4':
        warnings.push({ subject: opt, text: 'AWS request signing isn’t supported; the request will be sent unsigned.' })
        break
      default:
        ignored++
    }
  }
  const flag = (opt: string) => {
    switch (opt) {
      case '-I': case '--head': head = true; break
      case '-G': case '--get': get = true; break
      case '-k': case '--insecure':
        notes.push({ subject: opt, text: 'HawkEye always verifies the certificate: an invalid one counts as a failure, which is usually worth knowing.' })
        break
      default:
        if (!HARMLESS.has(opt)) notes.push({ subject: opt, text: 'Not something HawkEye can use, so it was ignored.' })
        else ignored++
    }
  }

  for (let i = 1; i < words.length; i++) {
    const w = words[i]
    if (w === '--') {
      urls.push(...words.slice(i + 1))
      break
    }
    if (w.startsWith('--') && w.length > 2) {
      if (WITH_VALUE.has(w)) {
        if (i + 1 >= words.length) return { ok: false, error: `${w} needs a value after it.` }
        take(w, words[++i])
      } else flag(w)
      continue
    }
    if (w.startsWith('-') && w.length > 1) {
      // Short options can be combined (-sSL) and the last one may carry its value (-XPOST, -HAccept:x).
      for (let j = 1; j < w.length; j++) {
        const opt = '-' + w[j]
        if (WITH_VALUE.has(opt)) {
          const rest = w.slice(j + 1)
          if (rest !== '') take(opt, rest)
          else if (i + 1 < words.length) take(opt, words[++i])
          else return { ok: false, error: `${opt} needs a value after it.` }
          break
        }
        flag(opt)
      }
      continue
    }
    urls.push(w)
  }
  if (ignored > 0) notes.push({ subject: 'options', text: `${ignored} option${ignored === 1 ? '' : 's'} that only change curl’s own output ${ignored === 1 ? 'was' : 'were'} ignored.` })

  if (urls.length === 0) return { ok: false, error: 'The command has no address to check.' }
  if (urls.length > 1) return { ok: false, error: `The command has more than one address (${urls.slice(0, 3).join(', ')}). Paste a command for one request.` }

  let rawURL = urls[0]
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(rawURL)) {
    rawURL = 'https://' + rawURL
    notes.push({ subject: 'address', text: 'The address had no http:// or https://, so https:// was added.' })
  }
  let u: URL
  try {
    u = new URL(rawURL)
  } catch {
    return { ok: false, error: `${urls[0]} isn’t a valid address.` }
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return { ok: false, error: 'HawkEye checks http and https addresses only.' }

  // Method: -X wins; then -I; then a body means POST, as in curl.
  const hasBody = data.length > 0 && !get
  let m = method ?? (head ? 'HEAD' : hasBody ? 'POST' : 'GET')
  if (!(HTTP_METHODS as readonly string[]).includes(m)) return { ok: false, error: `HawkEye can’t send ${m} requests. It supports ${HTTP_METHODS.join(', ')}.` }
  if (get && data.length > 0) {
    u.search = [u.search.replace(/^\?/, ''), ...data].filter(Boolean).join('&')
    if (!method) m = 'GET'
  }
  const finalMethod = m as HttpMethod

  let body = ''
  if (hasBody) {
    body = data.join(json ? '' : '&')
    if (!BODY_METHODS.includes(finalMethod)) {
      notes.push({ subject: 'body', text: `HawkEye doesn’t send a body with ${finalMethod} requests, so it was left out.` })
      body = ''
    }
  }

  // Credentials: the Authorization header first, then -u or --oauth2-bearer, then the first header that looks like a key. A check has one.
  let auth: CurlAuth = { type: 'none' }
  const moved = (subject: string, kind: string) =>
    notes.push({ subject, text: `Moved to Authentication (${kind}), where it’s stored encrypted and never shown again.` })
  if (u.username || u.password) {
    auth = { type: 'basic', username: decodeURIComponent(u.username), secret: decodeURIComponent(u.password) }
    u.username = ''
    u.password = ''
    moved('address', 'username and password from the address')
  }

  const headers: RequestHeader[] = []
  const omitted: RequestHeader[] = []
  const seen = new Set<string>()
  if (json) {
    for (const [name, value] of [['Content-Type', 'application/json'], ['Accept', 'application/json']]) {
      if (!rawHeaders.some((h) => h.name.toLowerCase() === name.toLowerCase())) rawHeaders.push({ name, value })
    }
  }
  if (cookie) rawHeaders.push({ name: 'Cookie', value: cookie })
  const credentialCandidates: RequestHeader[] = []
  for (const h of rawHeaders) {
    const lower = h.name.toLowerCase()
    if (lower === 'authorization') {
      if (auth.type !== 'none') {
        notes.push({ subject: h.name, text: 'Replaced the credentials from the address, as curl would.' })
      }
      const [scheme, ...rest] = h.value.split(/\s+/)
      const token = rest.join(' ')
      const basic = /^basic$/i.test(scheme) ? decodeBasic(token) : null
      if (/^bearer$/i.test(scheme) && token) {
        auth = { type: 'bearer', secret: token }
        moved(h.name, 'bearer token')
      } else if (basic) {
        auth = { type: 'basic', username: basic.username, secret: basic.password }
        moved(h.name, 'basic')
      } else {
        auth = { type: 'header', headerName: 'Authorization', secret: h.value }
        moved(h.name, 'API key header')
      }
      continue
    }
    if (RESERVED.has(lower)) {
      notes.push({ subject: h.name, text: 'Left out: HawkEye sets this header itself.' })
      continue
    }
    if (seen.has(lower)) {
      notes.push({ subject: h.name, text: 'Sent more than once in the command; only the first is kept.' })
      continue
    }
    seen.add(lower)
    if (isBrowserExtra(h.name)) {
      omitted.push(h)
      continue
    }
    if (CREDENTIAL_NAME.test(h.name)) credentialCandidates.push(h)
    headers.push(h)
  }
  const explicitAuth = auth.type !== 'none' && rawHeaders.some((h) => h.name.toLowerCase() === 'authorization')
  if (!explicitAuth && oauthBearer) {
    auth = { type: 'bearer', secret: oauthBearer }
    moved('--oauth2-bearer', 'bearer token')
  } else if (!explicitAuth && user !== undefined) {
    const at = user.indexOf(':')
    auth = { type: 'basic', username: at < 0 ? user : user.slice(0, at), secret: at < 0 ? '' : user.slice(at + 1) }
    moved('-u', 'basic')
    if (at < 0) warnings.push({ subject: '-u', text: 'The command has no password (curl would ask for one). Type it under Authentication.' })
  }
  if (auth.type === 'none' && credentialCandidates.length > 0) {
    const h = credentialCandidates[0]
    auth = { type: 'header', headerName: h.name, secret: h.value }
    headers.splice(headers.indexOf(h), 1)
    moved(h.name, 'API key header')
    credentialCandidates.shift()
  }
  for (const h of credentialCandidates) {
    notes.push({ subject: h.name, text: 'Looks like a credential, but a check has one set of credentials, so this stays a plain header that everyone in your organization can see.' })
  }
  if (omitted.length > 0) {
    const cookieOmitted = omitted.some((h) => h.name.toLowerCase() === 'cookie')
    notes.push({
      subject: 'headers',
      text: `${omitted.length} header${omitted.length === 1 ? '' : 's'} that browsers add ${omitted.length === 1 ? 'was' : 'were'} left out${cookieOmitted ? ', including a cookie (a signed-in session expires, and the check would then fail)' : ''}. You can put ${omitted.length === 1 ? 'it' : 'them'} back.`,
    })
  }

  let timeoutSeconds: number | undefined
  if (maxTime !== undefined && Number.isFinite(maxTime) && maxTime > 0) {
    if (opts.timeout) {
      timeoutSeconds = Math.min(30, Math.max(1, Math.ceil(maxTime)))
      if (timeoutSeconds !== maxTime) notes.push({ subject: '--max-time', text: `Timeout set to ${timeoutSeconds} seconds (HawkEye allows 1 to 30).` })
    } else {
      notes.push({ subject: '--max-time', text: 'Website and API checks use a 10-second timeout, so this was ignored.' })
    }
  }

  const port = u.port === '' ? null : Number(u.port)
  if (port !== null && port !== 80 && port !== 443) {
    warnings.push({ subject: 'address', text: `HawkEye checks ports 80 and 443 only, so port ${port} won’t be accepted. Use the address your users reach.` })
  }
  const priv = privateHostReason(u)
  if (priv) warnings.push({ subject: 'address', text: `${priv} HawkEye checks from the internet, so use the public address.` })

  return {
    ok: true,
    request: { url: u.toString(), method: finalMethod, headers, body, auth, timeoutSeconds, omitted, warnings, notes },
  }
}

/** A name for a monitor made from a command: the method (unless GET) and the host and path, within the 100-character limit. */
export function suggestName(r: CurlRequest): string {
  let where = r.url
  try {
    const u = new URL(r.url)
    where = u.host + (u.pathname === '/' ? '' : u.pathname)
  } catch {
    // keep the address as it is
  }
  return ((r.method === 'GET' ? '' : r.method + ' ') + where).slice(0, 100)
}
