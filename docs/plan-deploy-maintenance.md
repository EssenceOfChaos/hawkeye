# Plan: one-line deploy maintenance and pipeline checks

Status: **built** on branch `feat/deploy-maintenance` (2026-11-22), not yet released. Roadmap 2, feature #4 (Q1, about 2 build weeks).

**This is for HawkEye's customers, when they deploy their own websites and APIs.** It doesn't involve how HawkEye itself is released. A
customer adds two lines to their own CI pipeline:

```sh
# before deploying: quiet the alerts for these monitors (ends by itself after 15 minutes)
curl -fsS -X POST https://hawkeye.observer/api/v1/deploys -H "Authorization: Bearer $HAWKEYE_DEPLOY_KEY" -d tag=production

# after deploying: end the quiet period, check the monitors now, and fail the pipeline if one fails
curl -fsS -X POST https://hawkeye.observer/api/v1/deploys/latest/finish -H "Authorization: Bearer $HAWKEYE_DEPLOY_KEY"
```

The first line stops a deploy's restart from paging anyone. The second checks the site straight away and makes the pipeline fail (so it can
roll back) if the site isn't healthy. There's also a GitHub Action and a GitLab CI snippet that wrap the same two calls.

Why customers care: today they either get paged during every deploy, or schedule a maintenance window by hand, or ignore alerts around
deploys, which is how a broken deploy goes unnoticed. Basic uptime tools don't tie into the deploy at all.

## What exists today

- **Maintenance windows** (`internal/maintenance`): one-off, daily or weekly, for all monitors or a list. During a window, checks still run and
  are recorded, but raise no incidents or alerts, and the time doesn't count against uptime (`in_maintenance()` in SQL, migration `000013`).
  Paid plans only (`plans.maintenance_enabled`); only the organization's owner can create one; at most 50 per organization.
- **Status pages** show a monitor as "Maintenance" while a window covers it.
- **API keys** (`internal/apitokens`): `read` or `write`, acting as the person who created them. A write key can do everything that person can
  through the API, which is far too much to paste into a CI configuration.
- **On-demand checks:** only `POST /monitors/test-workflow` (session only, for drafts) and MCP `test_workflow`. Nothing runs a saved monitor's
  check on request and reports pass or fail.

## The design

### A deploy key

A third API key scope, **`deploy`**, made for CI secrets. It can do exactly two things: start and finish deploys in its organization. It
can't read monitors, change anything else or use MCP. Owners create deploy keys on the API keys page (they open maintenance, which only an
owner can). Like other keys it expires after 90 days by default, with "no expiry" offered, and the owner gets the usual "a key was created"
email. If the key leaks, the worst anyone can do is quiet the alerts for up to the maximum window and trigger checks, both rate-limited and
both in the security log.

### Starting a deploy

`POST /api/v1/deploys` with any of:

| Field | Default | Meaning |
|---|---|---|
| `tag` or `monitor_ids` | every monitor | which monitors the deploy affects (a tag name, so CI configs don't hold IDs) |
| `minutes` | 15 | when the quiet period ends by itself if `finish` is never called (1 to 120) |
| `note` | none | a commit SHA, version or pipeline URL, shown in the app and the security log (up to 200 characters) |

It creates a maintenance window of a new **kind, `deploy`**, starting now. The monitors are resolved when the deploy starts, so a monitor
tagged later isn't included by surprise. It returns `{deploy: {id, ends_at, monitors}}`. Form-encoded and JSON bodies both work, so the
one-liner needs no quoting.

### Finishing a deploy

`POST /api/v1/deploys/:id/finish` (`:id` can be `latest`: the most recent unfinished deploy started with this key, so the second CI line
doesn't need to pass an ID along):

1. **Ends the quiet period now** (sets `ended_at`), so the next scheduled checks alert normally again.
2. **Checks the deploy's monitors now**, from the primary location, through the same SSRF-safe checker the worker uses. A site that has
   just been deployed often needs a moment, so a failing monitor is tried again every 10 seconds until it passes or `verify_seconds` (default
   60, at most 120) runs out. These results are **recorded** like scheduled checks (labeled "Deploy check" in the history), so a failure
   counts toward an outage right away, and a pass shows that the deploy was verified.
3. **Answers with the outcome:** 200 with `{passed: true, monitors: [...]}` when every monitor passed, or **422** with `passed: false` and
   each failing monitor's error. `curl -f` (and the Action) fails the pipeline on the 422.

`verify=false` skips step 2 (just end the quiet period). Heartbeat monitors are skipped (nothing to check); a deploy limited to heartbeats
only ends the window. Up to 20 monitors are checked per finish, 5 at a time; more than that returns 400 asking for a narrower tag.

### Safety rails

- **A bad deploy must not be hidden.** The quiet period ends at `finish` or after `minutes`, whichever comes first, and the finish check
  records its results, so an outage started by the deploy is alerted as soon as the window ends. The app shows an open deploy on the
  dashboard and the monitor page ("Deploy in progress, alerts quiet until 3:45 PM").
- **Uptime:** like other maintenance, the deploy's minutes don't count against uptime. A deploy of at most 2 hours, ended early by
  `finish`, keeps that honest. A monthly report could list deploys later.
- **Status pages:** a deploy doesn't show as "Maintenance" on public status pages; it's an internal quiet period, not an announcement. See
  decision 2.
- **Limits:** at most 1 open deploy per tag (starting another one extends the existing one instead of stacking windows), 30 deploys an hour
  per organization, and finish checks share the per-organization budget that test runs use. Deploy windows don't count toward the 50
  maintenance windows, and ended ones are removed after 30 days by retention (the security log keeps the record).

### Plans

Deploy windows are maintenance, which is a paid feature, so **starting a deploy needs a paid plan**. On Free, `POST /deploys` answers 403
`PLAN_LIMIT_REACHED` with the usual upgrade message. The **pipeline check** part (finish with verify) could stay open to Free; see
decision 1.

### CI integrations

- **The plain `curl` lines** above, in the docs, work in any CI.
- **A GitHub Action**, `uses: EssenceOfChaos/hawkeye/actions/deploy@v1` (a composite action in this repository, released with a tag like
  the plugins are): `start` and `finish` steps, the key from a secret, inputs for `tag`, `minutes`, `note` (defaulting to the commit SHA) and
  `verify_seconds`, and the failing monitors printed in the job summary.
- **A GitLab CI snippet** in the docs, using `curl` in `before_script` and `after_script`.

### In the app

- **API keys page:** "Deploy" as a third access option, explained ("Only starts and finishes deploys. Safe to store in CI.").
- **Maintenance page:** deploys listed separately ("Recent deploys": note, monitors, started, ended, result), not mixed into scheduled
  windows.
- **Check history:** "Deploy check" label on results recorded by a finish.
- **Dashboard and monitor page:** the open-deploy notice above.
- **Security log:** deploy started, extended, finished (with the outcome).

## Data model (migration `000053`)

```sql
ALTER TABLE maintenance_windows
    ADD COLUMN kind text NOT NULL DEFAULT 'scheduled' CHECK (kind IN ('scheduled', 'deploy')),
    ADD COLUMN ended_at timestamptz,            -- a deploy finished early
    ADD COLUMN note text CHECK (char_length(note) <= 200),
    ADD COLUMN started_by_token uuid REFERENCES api_tokens (id) ON DELETE SET NULL,
    ADD COLUMN verified boolean;                -- the finish check's outcome; NULL until finished (or finished without one)
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scope_check,
    ADD CONSTRAINT api_tokens_scope_check CHECK (scope IN ('read', 'write', 'deploy'));
ALTER TABLE check_results ADD COLUMN deploy_check boolean NOT NULL DEFAULT false;
```

`maintenance_window_active()` stops counting a window at `ended_at`. The status page query treats `kind = 'deploy'` windows as not
maintenance for display (decision 2). The OAuth scope constraint stays `read`/`write`: deploy keys are API keys only.

## Files touched

| Area | Files |
|---|---|
| Keys | `internal/apitokens` (scope, `CanDeploy`), `internal/httpapi` (a deploy-only route group: every other route refuses a deploy key) |
| Deploys | new `internal/deploys` (start, extend, finish, verify with retries), `internal/maintenance` (kind, ended_at, listing), `internal/checks` (a `DeployCheck` flag on results) |
| API | `internal/httpapi/handlers_deploys.go`, routes, rate limits, audit events |
| Status pages, retention | `internal/statuspage` (decision 2), `internal/retention` (ended deploy windows after 30 days) |
| Web | API keys page, maintenance page (recent deploys), history label, dashboard and monitor notices |
| CI | `actions/deploy/action.yml` (composite), its README, a release tag |
| Docs | a new public page `/docs/deploys` (route in `App.tsx`, `seo.ts`, Caddy `@app` allowlist), `docs/api.md`, `docs/security.md` |

## Feature sync

- **Pricing cards:** "Deploy maintenance and pipeline checks from CI" on paid plans (next to maintenance windows).
- **Landing page:** a sentence in a feature card, and the FAQ if visitors would ask "can I stop alerts during deploys?" (likely yes).
- **Public docs:** the new deploys page, linked from the API docs and the alerts article.
- `README.md`, `web/public/llms.txt`; internal docs `api.md`, `architecture.md`, `security.md` (the new scope), `operations.md` (limits).
- **Legal:** nothing new is collected; a commit note is customer text like a monitor name.
- **Copy sync tests:** plan cards, SEO and prerender tests for the new page.

## Tests

- **Keys:** a deploy key can start and finish deploys and gets 403 on every other route (a table test over the route list, so a new route
  can't forget it). Read and write keys can't use deploy routes unless they're the owner's write key (decision 3).
- **Windows:** a deploy quiets alerts for its monitors only; `finish` ends it at once; it ends by itself after `minutes`; a second start on
  the same tag extends instead of stacking; ended deploys don't count toward the 50.
- **Finish check:** a passing site gives 200; a site that fails for 20 s and then passes gives 200 after retries; a site that keeps failing
  gives 422 with its error, records the failures (so the outage alert follows), and marks the deploy unverified.
- **Plans and limits:** Free gets 403 on start; the hourly limit and the 20-monitor cap answer clearly.
- **Status page:** a deploy doesn't show monitors as "Maintenance" (decision 2).
- **Action:** a test workflow in CI runs the composite action against a mocked API.
- **Web:** the key scope option, the recent deploys list, the history label and the notices.

## Order of work (about 2 weeks)

1. Migration, the deploy scope and the route guard (with the table test).
2. `internal/deploys`: start, extend, finish, the timeout ending, and the finish check with retries.
3. API endpoints, limits, the security log, status pages and retention.
4. Web: keys page, recent deploys, labels and notices.
5. The GitHub Action and the GitLab snippet; the docs page.
6. Feature sync, then a local run: start a deploy, take a test site down, finish, and see the 422 and the alert.

## Changed while building (2026-11-22)

- **The check after a deploy fits in one API request** (the API's 30-second limit): at most 10 monitors (not 20), all at once, a 12-second cap per
  monitor and about 20 seconds in all, with one retry of a failing monitor after 4 seconds. `verify_seconds` is gone: callers wait longer by
  retrying, which `finish` allows (the docs' `curl --retry`, the GitHub Action's `wait-seconds`).
- **Its results are not recorded.** With retries, two failures 10 seconds apart during a warm-up would have confirmed an outage and paged someone,
  which is what this feature should prevent. The window ends at `finish`, so the scheduled checks alert as usual right after. So there is no
  `check_results.deploy_check` column and no "Deploy check" label; the outcome is on the deploy (`verified`) and in the security log.
- **A check on its own** is `POST /deploys/check` (every plan), rather than a finish without a deploy.
- **The app** lists recent deploys under Maintenance and offers "Deploys only" keys; the dashboard notice was left out (monitors already show as in
  maintenance while a deploy covers them).
- **The GitHub Action** is a composite action around `actions/deploy/hawkeye-deploy.sh`, which also runs on its own; it was tested against a local API.
  Releasing it needs the tag `deploy-action-v1` on `main` after merging.
- **Found while building:** on phones, every docs page was wider than the screen (a long code line widened the page's grid column). Fixed with
  `minmax(0, 1fr)`.

## Decisions (2026-11-22): the recommendations below were chosen

1. **Plans.** Recommended: starting a deploy (the quiet period) is paid, like maintenance windows, but **finish with verify works on every
   plan**, so a Free customer can still use HawkEye as a post-deploy check in CI. The alternative is the whole feature on paid plans.
2. **Status pages during a deploy.** Recommended: don't show "Maintenance" publicly for a deploy (it's a few minutes of quiet alerts, not
   an announcement), while scheduled windows keep showing as today. The alternative is showing deploys like any maintenance, or a per-page
   setting.
3. **Who may use the deploy endpoints.** Recommended: deploy keys, plus the owner's own write keys (so an owner can try it from a terminal),
   but not other members' write keys, matching who can create maintenance windows today.
4. **Where the GitHub Action lives.** Recommended: in this repository (`actions/deploy`), released with a tag, as the plugins are. A
   separate repository would be needed only to list it on the GitHub Marketplace, which can come later.
