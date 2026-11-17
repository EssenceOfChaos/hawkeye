import { describe, expect, it } from 'vitest'
import { parseCurl, shellWords, suggestName, type CurlRequest } from './curl'
import { secretNameFor, type SecretDraft } from './workflowDrafts'

function ok(cmd: string, opts = {}): CurlRequest {
  const r = parseCurl(cmd, opts)
  if (!r.ok) throw new Error(`expected ${cmd} to parse, got: ${r.error}`)
  return r.request
}

function failure(cmd: string): string {
  const r = parseCurl(cmd)
  if (r.ok) throw new Error(`expected ${cmd} to be refused`)
  return r.error
}

const subjects = (notes: { subject: string }[]) => notes.map((n) => n.subject)

describe('shellWords', () => {
  it('reads quotes, escapes, ANSI-C strings and line continuations', () => {
    expect(shellWords(`curl 'a b' "c \\"d\\"" e\\ f $'g\\nh\\x41\\u00e9' \\\n  i`)).toEqual(['curl', 'a b', 'c "d"', 'e f', 'g\nhAé', 'i'])
  })

  it('refuses an unclosed quote', () => {
    expect(shellWords(`curl 'https://x.example`)).toBeNull()
    expect(shellWords(`curl "https://x.example`)).toBeNull()
  })

  it('reads Chrome’s Windows (cmd) form', () => {
    expect(shellWords('curl ^"https://api.example.com/v1^" ^\n  -H ^"accept: */*^" ^\n  --data-raw ^"^{^\\^"a^\\^":1^}^"')).toEqual([
      'curl', 'https://api.example.com/v1', '-H', 'accept: */*', '--data-raw', '{"a":1}',
    ])
  })

  it('reads PowerShell line continuations', () => {
    expect(shellWords('curl.exe `\n  -H "X-Thing: 1" `\n  https://api.example.com')).toEqual(['curl.exe', '-H', 'X-Thing: 1', 'https://api.example.com'])
  })
})

describe('parseCurl', () => {
  it('reads a plain GET', () => {
    const r = ok('curl https://api.example.com/v1/status')
    expect(r).toMatchObject({ url: 'https://api.example.com/v1/status', method: 'GET', headers: [], body: '', auth: { type: 'none' }, warnings: [] })
  })

  it('ignores a copied shell prompt and a path to curl', () => {
    expect(ok('$ /usr/bin/curl -s https://api.example.com').url).toBe('https://api.example.com/')
  })

  it('reads a Postman export: POST with a JSON body over several lines and a bearer token', () => {
    const r = ok(`curl --location --request POST 'https://api.example.com/v1/orders' \\
--header 'Content-Type: application/json' \\
--header 'Authorization: Bearer sk_test_123' \\
--data-raw '{
    "sku": "A-1",
    "qty": 2
}'`)
    expect(r.method).toBe('POST')
    expect(r.headers).toEqual([{ name: 'Content-Type', value: 'application/json' }])
    expect(r.body).toBe('{\n    "sku": "A-1",\n    "qty": 2\n}')
    expect(r.auth).toEqual({ type: 'bearer', secret: 'sk_test_123' })
    expect(subjects(r.notes)).toContain('Authorization')
  })

  it('reads Chrome’s "Copy as cURL" and leaves the browser’s extra headers out, but lets them be put back', () => {
    const r = ok(`curl 'https://app.example.com/api/me' \\
  -H 'accept: application/json' \\
  -H 'accept-language: en-US,en;q=0.9' \\
  -H 'cookie: session=abc123; theme=dark' \\
  -H 'if-none-match: W/"42"' \\
  -H 'priority: u=1, i' \\
  -H 'referer: https://app.example.com/' \\
  -H 'sec-ch-ua: "Chromium";v="131"' \\
  -H 'sec-fetch-mode: cors' \\
  -H 'user-agent: Mozilla/5.0' \\
  --data-raw $'{"name":"O\\'Brien"}'`)
    expect(r.method).toBe('POST')
    expect(r.body).toBe(`{"name":"O'Brien"}`)
    expect(r.headers).toEqual([{ name: 'accept', value: 'application/json' }])
    expect(r.omitted.map((h) => h.name)).toEqual(['accept-language', 'cookie', 'if-none-match', 'priority', 'referer', 'sec-ch-ua', 'sec-fetch-mode'])
    expect(r.notes.find((n) => n.subject === 'headers')?.text).toMatch(/7 headers .* including a cookie/)
    expect(subjects(r.notes)).toContain('user-agent')
  })

  it('reads combined short options and values attached to them', () => {
    const r = ok(`curl -sSL -XPUT -H'Content-Type: text/plain' -dhello https://api.example.com/x`)
    expect(r).toMatchObject({ method: 'PUT', body: 'hello', headers: [{ name: 'Content-Type', value: 'text/plain' }] })
  })

  it('joins several -d values with & and makes the request a POST', () => {
    expect(ok(`curl https://api.example.com/f -d a=1 -d b=2`)).toMatchObject({ method: 'POST', body: 'a=1&b=2' })
  })

  it('URL-encodes --data-urlencode', () => {
    expect(ok(`curl https://api.example.com/f --data-urlencode 'q=a b&c'`).body).toBe('q=a%20b%26c')
  })

  it('moves the data into the query string with -G', () => {
    const r = ok(`curl -G https://api.example.com/search?lang=en -d q=hawk --data-urlencode 'tag=a b'`)
    expect(r).toMatchObject({ method: 'GET', body: '', url: 'https://api.example.com/search?lang=en&q=hawk&tag=a%20b' })
  })

  it('reads --json as a JSON body with JSON headers', () => {
    const r = ok(`curl --json '{"ok":true}' https://api.example.com/x`)
    expect(r).toMatchObject({ method: 'POST', body: '{"ok":true}' })
    expect(r.headers).toEqual([{ name: 'Content-Type', value: 'application/json' }, { name: 'Accept', value: 'application/json' }])
  })

  it('uses HEAD for -I', () => {
    expect(ok('curl -I https://example.com').method).toBe('HEAD')
  })

  it('leaves a body out of a GET', () => {
    const r = ok(`curl -X GET https://api.example.com/x -d '{"a":1}'`)
    expect(r).toMatchObject({ method: 'GET', body: '' })
    expect(subjects(r.notes)).toContain('body')
  })

  describe('credentials', () => {
    it('reads -u as basic credentials', () => {
      expect(ok('curl -u alice:s3cret https://api.example.com').auth).toEqual({ type: 'basic', username: 'alice', secret: 's3cret' })
    })

    it('asks for the password when -u has none', () => {
      const r = ok('curl -u alice https://api.example.com')
      expect(r.auth).toEqual({ type: 'basic', username: 'alice', secret: '' })
      expect(subjects(r.warnings)).toEqual(['-u'])
    })

    it('decodes an Authorization: Basic header', () => {
      expect(ok(`curl -H 'Authorization: Basic ${btoa('bob:pa:ss')}' https://api.example.com`).auth)
        .toEqual({ type: 'basic', username: 'bob', secret: 'pa:ss' })
    })

    it('keeps any other Authorization scheme as an Authorization header credential', () => {
      expect(ok(`curl -H 'Authorization: Token abc' https://api.example.com`).auth)
        .toEqual({ type: 'header', headerName: 'Authorization', secret: 'Token abc' })
    })

    it('moves the first key-like header and keeps the rest as headers, saying so', () => {
      const r = ok(`curl -H 'X-API-Key: k1' -H 'X-Auth-Token: t1' -H 'X-Idempotency-Key: i1' https://api.example.com`)
      expect(r.auth).toEqual({ type: 'header', headerName: 'X-API-Key', secret: 'k1' })
      expect(r.headers).toEqual([{ name: 'X-Auth-Token', value: 't1' }, { name: 'X-Idempotency-Key', value: 'i1' }])
      expect(r.notes.find((n) => n.subject === 'X-Auth-Token')?.text).toMatch(/plain header/)
    })

    it('prefers the Authorization header to -u, as curl does', () => {
      expect(ok(`curl -u a:b -H 'Authorization: Bearer t' https://api.example.com`).auth).toEqual({ type: 'bearer', secret: 't' })
    })

    it('reads --oauth2-bearer', () => {
      expect(ok('curl --oauth2-bearer t0k https://api.example.com').auth).toEqual({ type: 'bearer', secret: 't0k' })
    })

    it('moves credentials out of the address', () => {
      const r = ok('curl https://ann:p%40ss@api.example.com/x')
      expect(r.url).toBe('https://api.example.com/x')
      expect(r.auth).toEqual({ type: 'basic', username: 'ann', secret: 'p@ss' })
    })
  })

  it('leaves out the headers HawkEye sets itself', () => {
    const r = ok(`curl -H 'Host: other.example' -H 'Accept-Encoding: gzip' -H 'Content-Length: 3' https://api.example.com`)
    expect(r.headers).toEqual([])
    expect(subjects(r.notes)).toEqual(expect.arrayContaining(['Host', 'Accept-Encoding', 'Content-Length']))
  })

  it('adds https:// to an address without one', () => {
    const r = ok('curl api.example.com/health')
    expect(r.url).toBe('https://api.example.com/health')
    expect(subjects(r.notes)).toContain('address')
  })

  it('uses --max-time only where the form has a timeout', () => {
    expect(ok('curl -m 45 https://api.example.com', { timeout: true }).timeoutSeconds).toBe(30)
    expect(ok('curl --max-time 2.5 https://api.example.com', { timeout: true }).timeoutSeconds).toBe(3)
    const r = ok('curl -m 5 https://api.example.com')
    expect(r.timeoutSeconds).toBeUndefined()
    expect(subjects(r.notes)).toContain('--max-time')
  })

  it('fills the form but warns about addresses HawkEye can’t reach', () => {
    expect(ok('curl http://localhost:3000/api').warnings.map((w) => w.text).join(' ')).toMatch(/port 3000.*only reachable on your own machine/s)
    expect(ok('curl http://192.168.1.10/api').warnings[0].text).toMatch(/private address/)
    expect(ok('curl http://printer/status').warnings[0].text).toMatch(/private network/)
    expect(ok('curl https://api.example.com:443/x').warnings).toEqual([])
  })

  it('warns about parts it can’t send', () => {
    expect(subjects(ok(`curl -F file=@a.png https://api.example.com/up`).warnings)).toEqual(['-F'])
    expect(subjects(ok(`curl -d @body.json https://api.example.com/up`).warnings)).toEqual(['-d'])
  })

  it('mentions -k and options it doesn’t know, and counts output options', () => {
    const r = ok('curl -k --frobnicate -sS -o out.txt https://api.example.com')
    expect(subjects(r.notes)).toEqual(expect.arrayContaining(['-k', '--frobnicate', 'options']))
    expect(r.notes.find((n) => n.subject === 'options')?.text).toMatch(/^3 options/)
  })

  it('refuses what it can’t read', () => {
    expect(failure('')).toMatch(/Paste/)
    expect(failure('wget https://example.com')).toMatch(/start with curl/)
    expect(failure(`curl 'https://example.com`)).toMatch(/quote/)
    expect(failure('curl -s')).toMatch(/no address/)
    expect(failure('curl https://a.example https://b.example')).toMatch(/more than one address/)
    expect(failure('curl -X TRACE https://a.example')).toMatch(/can’t send TRACE/)
    expect(failure('curl ftp://a.example/file')).toMatch(/http and https/)
    expect(failure('curl https://a.example -H')).toMatch(/-H needs a value/)
  })
})

describe('suggestName', () => {
  it('names the check after the method, host and path', () => {
    expect(suggestName(ok('curl https://api.example.com/v1/status'))).toBe('api.example.com/v1/status')
    expect(suggestName(ok('curl -X DELETE https://api.example.com/'))).toBe('DELETE api.example.com')
  })
})

describe('secretNameFor', () => {
  const secret = (name: string, removed = false): SecretDraft => ({ key: 0, name, value: '', stored: false, pending: false, removed })

  it('names the secret after the kind of credential, unique in the workflow', () => {
    expect(secretNameFor({ type: 'bearer' }, [])).toBe('token')
    expect(secretNameFor({ type: 'bearer' }, [secret('token'), secret('token_2')])).toBe('token_3')
    expect(secretNameFor({ type: 'bearer' }, [secret('token', true)])).toBe('token')
    expect(secretNameFor({ type: 'basic' }, [])).toBe('password')
    expect(secretNameFor({ type: 'header', headerName: 'X-API-Key' }, [])).toBe('x_api_key')
    expect(secretNameFor({ type: 'header', headerName: '2fa-Code' }, [])).toBe('_2fa_code')
  })
})
