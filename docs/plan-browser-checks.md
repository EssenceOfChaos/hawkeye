# Plan: browser checks (synthetic browser monitoring)

Status: **on the roadmap, not started** (planned 2026-11-17; roadmap #29). Nothing below is built. Start with *Step 0: validate demand*; the rest
waits on its answer.

A browser check loads a page in a real browser (Chrome) instead of only fetching it, and can click through a short user journey: open the sign-in
page, type an email and password, click "Sign in", expect "Your account". It catches what HTTP checks can't: a page that answers 200 but renders
blank because a script failed, a broken checkout button, a third-party script that hangs the page. A failure includes the step that failed, the
browser's console errors and failed requests, and a screenshot.

This plan covers browser checks only. It replaces the "Browser checks" notes in `docs/plan-synthetic-api.md` (*Not in this plan*).

## Not real user monitoring (RUM)

Browser checks are **synthetic**: HawkEye's browser visits on a schedule. Real user monitoring is a different product: a script on the customer's
site reports every real visitor's load times and errors, so it shows what real devices, networks and countries experience. RUM needs a script for
customers' sites, a public collection endpoint, and storage for far more data than checks produce. It is out of scope here, and worth planning only
if customers ask for it.

## Decision: a hosted browser first, our own Chrome later if the numbers justify it

**Run the browser on Cloudflare Browser Run first, behind a small interface, so it can move to our own Chrome on separate servers later without
changing anything customers see.**

| | Cloudflare Browser Run | Our own Chrome |
|---|---|---|
| Cost at low volume | A few dollars a month; no new servers | At least one extra server, about $20 a month, from the start |
| Cost at high volume | $0.09 per browser hour | Roughly 10× cheaper per browser hour, fully used (estimate, see *Costs*) |
| Security | The browser runs on Cloudflare's network and can't reach ours | We must stop Chrome from reaching our own network (see *Security*) |
| Upkeep | None | Chrome updates, memory leaks, stray processes, fonts |
| Check locations | Cloudflare chooses where the browser runs; no choice of location | Any of our regional worker hosts |
| Limits | 200 browsers at once and 3 new ones a second (Workers Paid; can be raised); sessions close when Cloudflare releases a new version | Only what our servers can hold |

**Why not our own Chrome now.**

- **Demand is unknown.** The status page subscribers waitlist has 0 sign-ups after launch, a reminder that a plausible feature isn't proof of
  demand. At low volume Browser Run costs a few dollars a month.
- **It can't share the production server.** Chrome uses 200–500 MB per page, and production is one 2-CPU, 4 GB server that also runs PostgreSQL and
  the checks. Self-hosting means a dedicated browser server from the first day.
- **It is the larger security job** (below).

**When to switch to our own Chrome.** When the Browser Run bill is clearly larger than the dedicated servers plus the time to run them, or when
customers ask to choose where browser checks run from. Because of the interface, switching changes one package.

## What a browser check is

A new monitor type, `browser`, built like multi-step API checks (`docs/plan-synthetic-api.md`): **steps, not code.**

- **Steps** (up to 10): `open` a URL; `click` an element; `type` into a field (text or a secret); `select` an option; `wait_for` an element or
  text; `expect_text` / `expect_no_text`; `expect_url`. Elements are found by visible text, label or role first (as people describe them), with a
  CSS selector as the fallback.
- **Secrets**: a test account's password and similar, encrypted and never shown, exactly as for multi-step API checks (`internal/secrets`).
- **Results**: pass or fail, the failing step, total time and time per step, page load timings (time to first byte, load, largest contentful
  paint), console errors, failed requests, and a screenshot when a check fails.
- **Not customer code.** Uploading Playwright or Puppeteer scripts would mean running strangers' code. Steps cover the common journeys and are much
  safer to run. Revisit only if customers ask for it.

Failures go through the existing rules: consecutive failures before an incident, alert delay, quiet hours, maintenance windows, status pages and
reports. A browser check counts as a monitor, like any monitor.

## Plans and pricing

A browser run costs real money, unlike an HTTP check, so browser checks get their **own allowance and a slower minimum interval**, like
`max_workflows` for multi-step API checks. Suggested starting point, to confirm with the numbers from step 1:

| Plan | Browser checks | Fastest interval |
|---|---|---|
| Free | 0 (shown on the pricing card as a paid feature) | n/a |
| Pro | 5 | 15 minutes |
| Agency | 25 | 5 minutes |

New plan columns: `max_browser_checks`, `minimum_browser_interval_seconds`. A paid add-on for more checks needs Stripe usage or quantity billing,
which HawkEye doesn't have; consider it only if the allowances prove too small.

## Costs

Browser Run, Workers Paid ([pricing](https://developers.cloudflare.com/browser-run/pricing/)): 10 browser hours a month included, then $0.09 an
hour. 10 concurrent browsers included (monthly average of each day's peak), then $2.00 each. A 10-second check costs:

| Interval | Browser time per check per month | Cost per check per month |
|---|---|---|
| Every 5 minutes | ~24 h | ~$2.16 |
| Every 15 minutes | ~8 h | ~$0.72 |

At the suggested allowances, a fully used Agency account (25 checks every 5 minutes) costs about $54 a month and a full Pro account (5 every 15
minutes) about $3.60. Agency is $59 a month. Most accounts won't use their whole allowance, but **these numbers are why the allowance and
interval are the main pricing lever**. Measure real run times in step 1 before choosing them: a heavy page may take 20 seconds, not 10.

Concurrency is not the constraint: 10 browsers running 10-second checks complete about 300 checks every 5 minutes.

Our own Chrome, for comparison (estimate, not measured): a $20-a-month 2-CPU, 4 GB server might keep about 3 pages busy around the clock, about
2,000 browser hours a month, which would cost about $190 on Browser Run.

## How it runs

- **A `browser` package with one interface:** something like `Run(ctx, steps, secrets) (Result, error)`. The first implementation calls Browser
  Run; a later one drives our own Chrome. Nothing else depends on which is used.
- **Connection:** our worker connects to Browser Run from outside Cloudflare over the Chrome DevTools Protocol (CDP) on a WebSocket, so no
  Cloudflare Worker is needed. A Go CDP client (for example `chromedp`'s remote allocator) runs the steps. Confirm in step 1 that this works from
  our server, and compare it with a small Cloudflare Worker running Playwright.
- **Scheduling:** browser checks use the existing durable queue (`check_jobs`) and scheduler, with their own limit on how many run at once (well
  under Browser Run's concurrency) so they never take workers away from HTTP checks.
- **Retries:** Browser Run closes sessions when Cloudflare releases a new version. Retry once on a closed session or a 429 before recording a
  failure; incidents already need consecutive failures.
- **Screenshots:** only on failure, compressed, kept for the plan's history period or 30 days, whichever is shorter. They need storage outside
  PostgreSQL (object storage), which HawkEye doesn't have yet. Logos are in the database today (up to 512 KB each); screenshots at this volume
  should not be.
- **Results:** `check_results` as today, with step outcomes in its existing `steps` column. The hourly totals (`check_rollups`) need no changes.

## Security

- **Browser Run:** the browser runs on Cloudflare's network, so a customer's page can't reach HawkEye's servers, database or cloud metadata. Still
  validate the starting URL with the public-host rules (`internal/publichost`), refuse IP addresses and private names, and don't send our own
  credentials anywhere but Cloudflare's API.
- **Our own Chrome (later):** Chrome makes its own connections, so `safehttp` can't police them. Run it only on dedicated worker hosts behind
  `deploy/egress-filter.sh`, which drops private, loopback, link-local (cloud metadata) and host traffic for the worker network, and verify it with
  `scripts/verify-egress.sh` (not yet run on a real host; see `docs/security-review.md`). Keep Chrome updated, sandboxed, with one fresh browser
  context per check, and a time and memory cap per run.
- **Secrets** never appear in results, screenshots or logs. `type` steps that use a secret are recorded as "typed secret `<name>`". A screenshot
  taken after a password field is filled must not show the password (Chrome masks password fields; other fields holding secrets need masking).
- **Write actions:** a browser check that submits forms changes data on the target on every run, like POST checks. Require a verified email and
  show the same warning as multi-step checks that change data.

## Known limits

- **No choice of location** on Browser Run: browser checks can't use multi-location checks or regional quorum until we run our own Chrome.
- **Bot protection:** sites with strict bot protection may block a cloud-hosted headless browser. Test this in step 1 against a range of real sites
  and tell customers how to allow HawkEye.
- **Terms:** check Cloudflare's terms allow running Browser Run on customers' behalf as part of a paid monitoring product before building on it.
- **Free Cloudflare plan** is 10 browser minutes a day: development needs a Workers Paid account.

## Order of work

0. **Validate demand.** Add `browser_checks` to the "Get notified" waitlist (`internal/waitlist` `Features`, `web/src/comingSoon.ts`) on the
   pricing page and the multi-step API check editor. Ask in the onboarding and weekly summary emails which journeys people would want checked. Go
   ahead when there are confirmed sign-ups from paying customers, not only visitors.
1. **Spike (a few days).** From our server, run 20–30 real sites through Browser Run: run time per check, how many are blocked, whether CDP from
   outside Cloudflare is reliable, and session closures over a week. Check the terms. Set the allowances and intervals from what it measures.
2. **Engine.** The `browser` package (Browser Run implementation), the `browser` monitor type and steps, migration (plan columns, constraint on
   `kind`), worker concurrency limit, results and failure screenshots (with object storage).
3. **App.** Step editor (reusing the multi-step check editor's layout), a run-test button, the failing step and screenshot on the monitor page,
   MCP `create_browser_check`, docs, pricing cards, landing copy, Terms and Privacy Policy (screenshots are new stored data), security docs.
4. **Later, if justified:** our own Chrome on dedicated regional worker hosts behind the egress filter, for cost or locations.
