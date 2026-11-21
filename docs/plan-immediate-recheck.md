# Plan: immediate re-check before alerting

Status: **built** on branch `feat/immediate-recheck` (2026-11-21), not yet released. Roadmap 2, feature #2 (Q1, about 1 build week). Feature #1 (Amsterdam and Los Angeles) has shipped, so
multi-location monitors exist in production.

When a healthy monitor fails a check, HawkEye checks it again about 15 seconds later instead of waiting for the next scheduled check. A second
failure confirms the outage and alerts right away. A success clears the failure, as it does today. Customers get **faster alerts** (a
5-minute Free monitor alerts in about 15 seconds instead of up to 5 minutes) and **fewer false alarms** (two unrelated blips in consecutive
intervals no longer add up to an outage).

## How it works today

- The scheduler enqueues one `check_jobs` row per monitor and location each interval (`internal/scheduler`). `UNIQUE (monitor_id, region)`
  means a monitor never has two jobs queued for one location.
- The worker runs the job, records the result (`checks.Store.Record`), then deletes the job (`internal/worker`).
- `checks.Apply` marks a monitor down after `FAILURE_THRESHOLD` (2) failures in a row. With several locations, each location keeps its own
  streak in `monitor_region_state`, and the monitor is down once a majority of locations are down (`applyRegional`).
- A failure that may be caused by our own network is already discarded when the canary says this server can't reach public sites either.

So the second, confirming failure always waits a full interval.

## The design

### When a re-check runs

`RecordTx` sets a new `Outcome.Recheck` when **all** of these hold:

1. The result counts (not in maintenance, and from one of the monitor's current locations).
2. It is a failure, and it isn't itself a re-check.
3. The location's failure streak has just become 1, and neither the location nor the monitor is down.
4. `FAILURE_THRESHOLD` is at least 2 (with 1 there's nothing to confirm).
5. The monitor's `recheck` setting allows it (see *Customer choice*): `auto` when every request is read-only, `always`, or never with `off`.
6. The next scheduled check is more than twice the re-check delay away. This rules out 30-second Agency checks, whose next check is about
   as soon as a re-check would be.

That gives the cap the roadmap asks for: **at most one re-check per failure streak, and none while a monitor is down or recovering.** A site
that is really down is checked at its normal rate, and recovery timing doesn't change. The worst case is a site that fails one check in every
interval and passes the re-check each time, which doubles that one monitor's checks. That is still cheap, and flap suppression (#10) will
handle it.

With `FAILURE_THRESHOLD` above 2, the re-check still runs only once, and any further confirmations wait for their intervals as today.

### Which location re-checks

**The location that failed re-checks itself.** The roadmap said "from another location when the monitor has one", but on a closer look the
same location is the right one:

- **Multi-location monitors** already get a second opinion. Every location is scheduled in the same tick, and the majority vote decides. If
  the site is down everywhere, every location fails, re-checks itself about 15 seconds later, and the majority forms right away. If one
  location has a local problem, its re-check may fail too, but one location alone never makes a majority, so nobody is alerted.
- **Single-location monitors** can't safely let a different location decide. If a success from Amsterdam cleared a failure from New York, a
  site that is down only for US visitors (a regional CDN outage, or a geo-block by mistake) would never alert: every New York failure would be
  cleared. Using another location's failure to *confirm* would also give Free monitors a location their plan doesn't include.

A "second opinion from another location", shown in the alert but not deciding anything, fits better with rich failure snapshots (#8). It's
left out of this version.

### Queue mechanics

The re-check **reuses the job that just ran** instead of inserting a new one. The job row still exists while the worker records the result
(it's deleted afterward), and inserting a second job for the same location would hit `UNIQUE (monitor_id, region)`.

1. `queue.Recheck(ctx, db, jobID, delay)`: `UPDATE check_jobs SET status = 'queued', locked_until = NULL, run_at = now() + delay,
   attempts = 0, recheck = true WHERE id = $1`.
2. The worker calls `queue.Recheck` instead of `queue.Complete` when `Outcome.Recheck` is set.
3. `ClaimRegion` returns `Job.Recheck`. The worker copies it onto the result (`Result.Recheck`), and `RecordTx` stores it and never asks for
   another re-check from it.

Properties:

- **No extra queue rows.** Queue depth, per-location backpressure and the scheduler's `ON CONFLICT DO NOTHING` behave as they do today. If the
  scheduler comes around while a re-check is queued, it skips that location, which is already counted as `Skipped`.
- **Crash-safe.** If the worker dies between recording the result and requeuing the job, the lease expires and the job runs again as an
  ordinary check. The only cost is that the result isn't labeled as a re-check.
- `Release` (retries) leaves `recheck` as it is, so a re-check that hits a transient error is retried as a re-check.
- A monitor that is paused or deleted before the re-check runs is handled by the worker's existing check (`enabled`, `deleted_at`).

**Delay: 15 seconds**, set with `RECHECK_DELAY` (worker config, listed in `docs/operations.md`). It's long enough to get past a container
restart or a load balancer reshuffle, and short enough that alerts stay fast. It's deliberately not a per-monitor setting.

### Uptime, charts and history

Uptime is successes ÷ checks, so counting re-checks would distort it: an outage would gain one extra failure, and a blip would be diluted by
its successful re-check. **Re-checks are kept in the check history (labeled "Re-check") but left out of uptime, response-time averages,
rollups and slow-response streaks.** Uptime math stays per interval, exactly as today.

- `internal/rollups`: `WHERE NOT recheck` in the hourly insert (`job.go`) and in the live union over raw results (`rollups.go`).
- Any query that reads `check_results` directly for uptime or averages (status pages, badges, monthly report, weekly summary, MCP
  `get_uptime`) gets the same filter. Grep for `FROM check_results` and go through each one.
- `recordSpeed` is skipped for re-checks.
- Incidents: check whether `started_at` is the confirming check's time or the first failure's, and keep it the first failure's. Re-checks
  make the confirming check much closer to the real start anyway.

### Customer choice

Following the "best practice as a default the customer controls" rule. **A re-check never repeats a request that may change something
unless the customer asks for it.** The scheduled checks already repeat every request each interval, but a re-check would add an extra run
exactly when something is failing. For example, a workflow whose "create order" step succeeded before a later step failed would create a
second order.

- New column `monitors.recheck text NOT NULL DEFAULT 'auto' CHECK (recheck IN ('auto', 'always', 'off'))`:
  - `auto` (the default): re-check only when **every request is read-only**, meaning GET, HEAD or OPTIONS for an HTTP or keyword monitor and
    for every step of a workflow. DNS monitors are always read-only. A monitor or workflow with any POST, PUT, PATCH or DELETE isn't
    re-checked, and alerts as it does today.
  - `always`: re-check whatever the methods are. This is for a POST that is safe to repeat (a search, or a GraphQL query).
  - `off`: never re-check.
- The decision is made **when the check fails**, from the monitor's current methods, not stored when the monitor is created. Editing a GET
  monitor into a POST therefore stops `auto` re-checks with no further step. A helper (`checks.ReadOnly(method)` and `monitors.AllReadOnly`
  for a workflow's steps) has one list of safe methods, used by both the rule and the form.
- Heartbeat monitors have no checks, so the setting doesn't apply to them, and the form hides it for them.
- Monitor form, under Alert timing: one switch, **"Re-check right away before alerting"**, showing whether the monitor is re-checked.
  - Read-only monitors: on by default (`auto`). Turning it off stores `off`.
  - Monitors with a write request: off by default, with the hint *"Off for this monitor: it sends a request that may change data, and a
    re-check would send it again."* Turning it on stores `always`, and the hint then says that the request will be sent again. (This
    replaced the first draft's second checkbox.)
  - 30-second checks: the hint says they aren't re-checked.
- The public API and `create_monitor` in MCP take `recheck` (`auto`, `always` or `off`), defaulting to `auto`. cURL, OpenAPI and Postman
  import leave it at `auto`, so an imported POST endpoint is never re-checked without the customer asking.
- Available on every plan. Free has the longest intervals and gains the most.

## Schema (one migration)

```sql
ALTER TABLE monitors      ADD COLUMN recheck text NOT NULL DEFAULT 'auto' CHECK (recheck IN ('auto', 'always', 'off'));
ALTER TABLE check_jobs    ADD COLUMN recheck boolean NOT NULL DEFAULT false;
ALTER TABLE check_results ADD COLUMN recheck boolean NOT NULL DEFAULT false;
```

A constant default is a metadata-only change in PostgreSQL 11 and later, so the busiest table isn't rewritten. Rollups don't need a new
column: they just leave re-checks out.

## Alert and incident wording

- The incident summary and the alert in every channel (`internal/notifications/render.go`, `internal/email/content.go`) say how the outage was
  confirmed: *"Confirmed by a re-check 15 seconds later."* With several locations, this goes next to the existing "offline from 2 of 3
  locations".
- The monitor page's check history labels re-check rows.
- `MonitoringFlow.tsx`: the "Outage confirmed" step changes from "Failed checks in a row" to *"Failed again on an immediate re-check"*.

## Metrics

- `hawkeye_rechecks_total{result="failed"|"passed"}`: "passed" counts the false alarms avoided, which is a number worth putting on the
  site later.
- Re-checks also appear in `ChecksTotal` as usual.

## Files touched

| Area | Files |
|---|---|
| State and recording | `internal/checks/store.go` (`Outcome.Recheck`, the rule, skip speed, store the flag), `internal/checks/checker.go` (`Result.Recheck`) |
| Queue and worker | `internal/queue/queue.go` (`Recheck`, `Job.Recheck`), `internal/worker/worker.go`, `internal/config/config.go` (`RECHECK_DELAY`), `cmd/worker/main.go` |
| Uptime | `internal/rollups/*.go` and every direct uptime query over `check_results` |
| Monitors | `internal/monitors` (field, validation), `internal/httpapi`, `internal/mcpserver`, `migrations/` |
| Web | `MonitorFormPage.tsx`, `MonitorDetailPage.tsx` (history label), `MonitoringFlow.tsx`, `api.ts` |
| Wording | `internal/notifications/render.go`, `internal/email/content.go` |

## Feature sync

- **Public docs** (`docsArticles.tsx`, "how alerts work"): explain the re-check and the toggle. `seo.ts` only if a new page is added (none is
  planned).
- **Landing page:** the false-alarm FAQ answer, if there is one, or a feature card line. **Value strip / `PlanDetails.tsx`:** "Re-checked
  before every alert" on all plans. Buyers do compare false-alarm handling, so this one belongs on the cards.
- `README.md`, `web/public/llms.txt`.
- `docs/api.md` (the field), `docs/architecture.md` (the queue reuse), `docs/operations.md` (`RECHECK_DELAY`, the metric, and line 119's
  "two failures in a row" if the status beat changes. It doesn't: `internal/statusbeat` is separate and stays as it is).
- Legal: no new data is collected, so nothing changes.
- Copy sync tests: `PlanCardsInSync.test.tsx`, landing page tests.

## Tests

- **`state`/`store` unit and integration:** first failure → re-check requested; re-check failure → `WentDown` with the incident opened;
  re-check success → streak cleared and no incident; no re-check while down, while recovering, during maintenance, from a dropped location,
  with `recheck` set to `off`, with `FAILURE_THRESHOLD=1`, or when the next check is due soon (30-second monitors).
- **Read-only rule:** `auto` re-checks GET, HEAD and OPTIONS monitors, DNS monitors and all-GET workflows. It doesn't re-check a POST, PUT,
  PATCH or DELETE monitor, or a workflow with any such step, and `always` does. A monitor edited from GET to POST stops being re-checked.
- **Multi-location:** all three locations fail → every one re-checks → the majority forms after the re-checks, not an interval later; one
  location fails and fails its re-check → no incident.
- **Queue:** `Recheck` requeues the same row with `attempts = 0` and `run_at` in the future; the scheduler skips the location while it's
  queued; a lease expiry mid-way runs it again; `Release` keeps the flag.
- **Worker end to end** (`worker_integration_test.go`, with a fake site that fails once, or fails every time): alert timing and the number of
  checks.
- **Uptime:** re-check rows are excluded from rollups and from every direct uptime query (a table-driven test over the query functions, so a
  new query can't forget the filter).
- **Web:** the form toggle, hidden for heartbeats, with the workflow hint; the history label.

## Order of work (about 5 days)

1. Migration, `Result`/`Job` flags, `queue.Recheck`, the rule in `RecordTx`, the worker change, config. Unit and integration tests.
2. Uptime exclusions across rollups and direct queries, with the table-driven test.
3. API, MCP and form toggle; history label; alert and incident wording.
4. Feature sync (docs, site, cards, README, llms.txt), then a local run with `run-hawkeye` against a site that fails once.
5. Ship behind the per-monitor default. Watch `hawkeye_rechecks_total` and queue wait (`HawkEyeChecksDelayed`) for a few days.

## Found while building

A monitor with several locations could not record a location's very first result if it was a failure: the location's state started from an
empty status, which `monitor_region_state` rejects, so the whole result rolled back and the job was retried until dropped. A new multi-location
monitor for a site that was already down therefore never alerted. Fixed in `applyRegional` (a location now starts from `unknown`), with a
regression test (`TestAFirstFailureInALocationIsRecorded`).

## Decisions (2026-11-21)

1. **Existing monitors get `auto` too** (the migration default). Read-only monitors get faster alerts and fewer false alarms, and monitors
   that send POST, PUT, PATCH or DELETE behave exactly as before.
2. **Requests that may change something are never repeated by default**, for workflows and plain monitors alike. The customer can choose
   `always` per monitor. This replaced the first draft's "workflows on, with a hint".
3. **The delay is 15 seconds** (`RECHECK_DELAY`), and it can be tuned without a release.
