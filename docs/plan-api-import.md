# Plan: create API checks from a cURL command or an OpenAPI spec

Status: **built** (2026-11-16): step 1 (`web/src/curl.ts`, `components/CurlImport.tsx`) and step 2 (`web/src/openapi.ts`, `pages/OpenApiImportPage.tsx`
at `/monitors/import`), and Postman v2.x collections on the same screen (`web/src/postman.ts`, added on request), with the recommendations below as decided. Fetching a spec from a URL and the items under *Not in this plan* are still open.

Developers already have their requests written down: as a cURL command (from their docs, a README, a terminal, or a browser's
"Copy as cURL") or as an OpenAPI/Swagger file. Retyping the method, headers, token and JSON body into a form is slow and error-prone. This plan
lets them paste the command, or drop in the spec, and get API checks they review and save.

## Should we build it?

Yes, in two steps: cURL first, then OpenAPI.

**Why yes.**

- **Nothing new is needed underneath.** API checks already send any method with headers, a body and encrypted credentials, and check the status
  and the JSON (up to 10 assertions, `docs/api.md`, *API checks*). Multi-step workflows exist too. Import is a faster way to fill in fields that
  already exist, so every rule that protects customers and us (URL and SSRF rules, the reserved headers, the 5-minute rule for writes, plan limits)
  applies unchanged.
- **It removes the slowest part of setting up an API check.** "Set up from a domain" (`POST /monitors/suggestions`) did the same for websites:
  propose, let the person tick, create through the normal endpoint.
- **It reaches developers where they already are.** "Paste your cURL" is a short, concrete line for the landing page and the docs.

**What "deep" should mean.** A spec can't tell us what a healthy response *contains*, only its shape. Generated checks will test the status
code and that the fields the spec marks as required are present (JSON path `exists`). That is honestly deeper than "the URL answers", but copy
should say "checks the status and the response's shape", not promise business-level checks. Customers can then add `equals` assertions or turn
a check into a multi-step workflow.

**Why cURL first.** It's about a week, it fits into the form people already use (and each workflow step), and it has no open questions. OpenAPI
is about two weeks and has the decisions below.

## Decisions for you

| Decision | Recommendation |
|---|---|
| Where the command or spec is read | **In the browser.** Nothing pasted or uploaded is sent to HawkEye until the person saves the checks, and then only the fields of those checks. No new endpoint, nothing new stored, no new privacy wording |
| Which plans | **Every plan.** It creates ordinary monitors, so the existing monitor, workflow and interval limits apply. Free users who import a spec see quickly why Pro allows 50 monitors |
| Write operations in a spec (POST, PUT, PATCH, DELETE) | **Listed but unticked**, with a warning that HawkEye will repeat them every 5 minutes or more (the existing rule). The person can still choose them |
| Credentials found in a command | **Moved into the encrypted credentials field by default** (`auth`), with a switch to keep a header as typed. Plain headers are returned by the API; `auth` never is |
| Browser noise in "Copy as cURL" (`sec-ch-*`, `accept-language`, `priority`, cookies) | **Left out by default**, with "show N headers we left out" to put any back. A pasted session cookie works until it expires and then the check fails, so it's off unless chosen |
| Tag monitors made from a spec | **Yes, by default**: a tag named after the API's title (tags exist), so they can be filtered, paused or deleted together. The person can untick it |
| Fetch a spec from a URL | **Not in the first version.** A browser fetch fails on most hosts (CORS), and fetching it from our servers is a new outbound path. Upload a file or paste the text. Revisit if people ask |
| MCP | **No new tool.** Claude already reads cURL and specs, and `create_monitor` / `create_workflow` exist. Mention this in `/docs/claude` |

## Step 1: paste a cURL command

**Where.** A "Paste a cURL command" button on the API check form (`web/src/pages/MonitorFormPage.tsx`) and on each step of the workflow editor
(`web/src/components/WorkflowEditor.tsx`). It opens a text box; reading it fills the form, and a short list says what was used, moved or left out.
Nothing is saved until the person presses Save as usual.

**Reading the command** (`web/src/curl.ts`, a small parser of our own, no new dependency):

- Shell syntax: single and double quotes, `$'…'`, backslash line continuations, and the Windows forms people paste (`^` in cmd, a backtick in
  PowerShell, and `curl.exe`).
- **Used:** the URL (positional or `--url`); `-X/--request`; `-H/--header`; `-d/--data`, `--data-raw`, `--data-binary`, `--data-ascii` (joined
  with `&` like cURL does, method becomes POST when none is given); `--json` (body plus JSON `Content-Type` and `Accept`); `-u/--user` (basic
  credentials); `-I/--head` (HEAD); `-G/--get` (data moves into the query string); `-m/--max-time` (timeout, capped at 30 s); `-b/--cookie`
  (a `Cookie` header, off by default as above); `-e/--referer` and `-A/--user-agent` (left out: HawkEye sets its own User-Agent).
- **Ignored with a note:** `-L`, `--compressed`, `-s`, `-v`, `-o`, `-i`, `--http2` and other output or transport flags that don't change what
  is checked. `-k/--insecure`: noted that HawkEye always verifies certificates (an invalid certificate is a failure, which is usually what
  people want to hear about).
- **Refused with a reason:** `-F/--form` (multipart uploads aren't supported), `-d @file` (we can't read the file), more than one URL, and anything
  the form itself would refuse: `localhost` or a private address ("HawkEye checks from the internet; use your public address"), and ports
  other than 80 and 443.

**Credentials.**

- `Authorization: Bearer …` → bearer credentials. `Authorization: Basic …` → decoded into basic credentials. Any other `Authorization`
  value → header credentials named `Authorization`, which the API already allows.
- Headers whose names look like credentials (`X-API-Key`, `Api-Key`, `X-Auth-Token`, `X-Access-Token`, and names containing `token`,
  `secret` or `key`) → header credentials. A check has one credential, so if there are several, the first is moved and the others stay as
  headers, with a note.
- `-u user:password` → basic credentials.

**Reserved headers** (`Host`, `Content-Length`, `User-Agent`, `Accept-Encoding`, `Connection` and the rest of `reservedHeaders` in
`internal/monitors/monitors.go`) are left out with a note, rather than failing on save. The parser reads that list from one shared file
generated from Go, or a test keeps the two in step, so they can't drift.

**Writes.** A command that writes (POST, PUT, PATCH, DELETE) fills the form as usual, and the form's existing note about the 5-minute minimum
and the verified address applies.

## Step 2: import an OpenAPI or Swagger spec

**Where.** "Import from OpenAPI" next to "Set up from a domain" on the add-monitor screen (`web/src/pages/SetupPage.tsx`). The person uploads or
pastes a spec, sees a checklist, sets credentials once, and creates the ticked checks.

**Reading the spec** (`web/src/openapi.ts`):

- OpenAPI 3.0 and 3.1, and Swagger 2.0. JSON or YAML; YAML through the `yaml` npm package (no dependencies of its own; alias expansion is capped,
  so a hostile file can't blow up the page). Files up to 5 MB.
- `$ref`s inside the file are followed, with a guard against loops. References to other files or URLs are not followed: those parts are shown
  as unknown, which only means fewer generated assertions.
- **Base URL:** the first `servers` entry (3.x, with its variables' defaults) or `schemes`, `host` and `basePath` (2.0). When there is none,
  it's relative, or it's `localhost` (common in specs), the person types the public base URL. A dropdown picks between several servers.

**The checklist.** One row per operation, grouped by the spec's tags, showing the method, path and summary.

- **Ticked by default:** GET operations that need no values the person must supply, with health-like paths first (`/health`, `/healthz`,
  `/status`, `/ping`, `/ready`). At most 10 are ticked by default, and never more than the plan has room for. The screen shows "N of M monitors
  left on your plan".
- **Path and query parameters:** filled from the spec's `example`, `examples` or `default` when present. Otherwise the row shows the parameter,
  and the person types a value before it can be ticked (for example `/users/{id}` needs a real ID).
- **Writes:** listed, unticked, with the warning above.
- **Header parameters** marked required with an example become headers.

**What each check gets.**

- Name: the operation's `summary`, else `operationId`, else `GET /path`.
- Expected status: the lowest 2xx response the spec defines. When it gives only `2XX` or `default`, none is set, so any response below 400 counts
  as up (setting 200 would fail an endpoint that answers 204).
- Assertions: for a JSON response schema, a JSON path `exists` for each required top-level property (up to 5), and `equals` when the property is a
  `const` or a single-value `enum` (for example `status: "ok"`). Nothing is assumed when there's no schema.
- Body for writes: the request body's `example`, when there is one; otherwise the row needs a body before it can be ticked.
- Interval and timeout: the form's defaults. The 5-minute rule applies to writes.

**Credentials.** The spec's `securitySchemes` decide what the credentials step asks for:

| Scheme | Becomes |
|---|---|
| `http` `bearer` | bearer credentials |
| `http` `basic` | basic credentials |
| `apiKey` in a header | header credentials with that header name |
| `apiKey` in the query string or a cookie | not supported: the query string isn't stored encrypted. The person can still add the value as a header if the API accepts one |
| `oauth2`, `openIdConnect` | not filled in. The screen suggests a multi-step workflow whose first step gets a token, and links to its guide |

The person types the secret once, and it's saved, encrypted as today, on each check that uses it. Operations whose `security` is `[]` get none.

**Creating.** The ticked checks are created one by one through `POST /monitors`, the same way "Set up from a domain" does it, so each one gets
the server's validation and plan checks. The screen shows per-row results ("created", or the server's error on that field), keeps rows that
failed so they can be fixed, and adds the tag at the end.

## Security and privacy

- Nothing new reaches the server. The command or spec stays in the page; only the fields of saved checks are sent, through the existing
  endpoint, with its validation and the checker's SSRF-safe client unchanged.
- Credentials are moved out of plain headers by default, which is safer than how many people would type them by hand.
- Analytics events record counts only (`import_curl` with what was used, moved or ignored; `import_openapi` with the spec version and how
  many checks were offered and created). Never URLs, headers, bodies or spec content.
- The YAML parser runs with alias limits and a size cap. The page parses in a Web Worker so a huge or hostile file can't freeze the tab.
- `docs/legal-placeholders.md`: no new personal data, so nothing changes. Record that in the PR.

## Everything else that changes

Following the feature checklist:

- **Public docs** (`web/src/pages/docs/DocsPages.tsx`): a section "Create a check from cURL or OpenAPI" in the API checks guide, with what's
  supported and refused; a line in `/docs/claude` saying Claude can do the same from a chat. SEO entries in `web/src/seo.ts` if it becomes its own page.
- **Landing page:** one line in the API checks feature card ("Paste a cURL command or import your OpenAPI spec"). Not on the pricing cards: it's
  available on every plan, and people shop for the checks, not how they're entered.
- `web/public/llms.txt`, `README.md`, `docs/architecture.md` (the two parsers are browser-only), `docs/security.md` (the points above).
- No API, MCP, email, migration or plan change.

## Build order and estimate

1. **cURL parser and tests** (2 days): table-driven tests from real commands (from popular API docs, Chrome's, Firefox's and Postman's
   "Copy as cURL", and Windows variants).
2. **cURL in the form and workflow steps** (2 days), with the summary of what was used and left out; docs and landing copy (1 day). Ship.
3. **OpenAPI reader and tests** (4 days): 2.0, 3.0 and 3.1 fixtures, including large public specs (Stripe's, GitHub's) for speed and
   hostile ones for the limits.
4. **Import screen** (4 days): checklist, parameters, credentials, creation with per-row results, tag.
5. **Docs, copy and a browser test** of the whole flow (2 days). Ship.

About **3 weeks** in all, released as two pull requests.

## Not in this plan

- Fetching a spec from a URL, and keeping checks in sync when the spec changes. Both need a server-side reader; worth it only if people ask.
- HAR files. They'd follow the same pattern as Postman collections if requested.
- Generating multi-step workflows from a spec's `links` or OAuth flows.
- A REST or MCP endpoint that imports. API users can already create checks directly, and Claude can read the spec itself.
