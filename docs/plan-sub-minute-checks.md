# Plan: checks under a minute (Agency)

Status: **in progress** (2026-11-16). Load tested; `WORKER_CONCURRENCY` raised to 30 and a queue-wait alert added. Next: result rollups. The
interval change itself comes last.

Agency customers will be able to check a monitor every 30 seconds. **New monitors keep the 60-second default**; a shorter interval is a choice
an Agency customer makes per monitor. Free and Pro are unchanged.

## How it was measured

`cmd/loadtest` creates its own scratch database, applies the 30-second schema change there only, and runs HawkEye's real code:

- **live** runs the scheduler, worker and result store with production settings against a local fake site (5% of sites slow, 1% never answering),
  and reports the gaps between checks, queue waits, scheduler lag and database writes.
- **read** fills in history at an interval and times the queries that read it: dashboard uptime, status pages, charts, history and monthly reports.

PostgreSQL 17 ran in a container capped at 2 CPUs and 2 GB, like the production server (`vc2-2c-4gb`, 80 GB disk), on 2026-11-16.

```sh
go run ./cmd/loadtest -db 'postgres://…' live -interval 30 -orgs 5 -base 400 -concurrency 30 -duration 5m
go run ./cmd/loadtest -db 'postgres://…' read -interval 30 -monitors 50 -days 90
```

## Results

**The checking engine** keeps up once enough checks run at once. Checks mostly wait on the network, so worker slots fill long before the database
is busy.

| Load | Checks at once | Result |
|---|---|---|
| 600 monitors at 60 s | 10 | on time (worst gap 67 s) |
| 600 monitors at 30 s | 10 | on time (worst gap 36 s) |
| 1,000 at 30 s + 400 at 60 s | 10 | **96% of 30 s checks late** (really every ~53 s), **32% of the 60 s checks late too**; database CPU ~25% |
| 1,000 at 30 s + 400 at 60 s | 30 | on time (worst gaps 34 s and 67 s); database CPU averaged 24%, peaked at 53% of 2 cores |

Saturation degrades everyone, not only the customers on fast checks, and none of the existing alerts noticed: the oldest queued job waited 48 s
(alerts fired at 180 s and 300 s) and `missed_checks` stayed at 0, because it only counts lateness at scheduling time. Hence
`WORKER_CONCURRENCY=30` by default and the `HawkEyeChecksDelayed` alert (oldest queued check over 30 s for 5 minutes).

A 30-second timeout on a 30-second check skips a check now and then when the site stops answering (the previous one is still running): the
worst gap was 61 s. The interval change will require the timeout to be shorter than the interval.

**Reading history** is the real limit, and it already is at 60 seconds: uptime, status pages and dashboards add up raw results.

| 50 monitors, 90 days of results (median of 5) | at 60 s | at 30 s |
|---|---|---|
| Dashboard uptime, all monitors, 30 days | 0.65 s | 2.8 s |
| Dashboard uptime, all monitors, 90 days | 4.1 s | 24 s |
| Public status page, 50 monitors, 90 days (uncached) | 3.9 s | 15.7 s |
| One monitor's uptime, chart, history page or monthly report | under 0.2 s | under 0.2 s |

A full Agency account has four times these monitors, and the dashboard also offers 365 days. Each result takes 225 bytes with its indexes, so a
full Agency account (200 monitors, 365 days) is **22 GB a year at 60 s and 44 GB at 30 s**.

## Order of work

1. **Done:** `WORKER_CONCURRENCY` default 30 (the worker's pool follows it: 34 connections), the `HawkEyeChecksDelayed` alert, and `cmd/loadtest`.
   Production takes the value from Infisical, so it changes there.
2. **Rollups:** hourly and daily totals per monitor (checks, successes, response-time sum and maximum), kept up to date as results are recorded,
   and read by uptime, status pages, dashboards, badges and reports instead of raw results. This also makes today's 60-second accounts faster.
3. **Shorter raw retention for sub-minute monitors** (decision pending): keep their individual results for 30 days and their rollups for the plan's
   full history, so uptime and charts keep 365 days while storage stays a few GB.
4. **The interval change:** migration (monitors may run every 30 s; Agency's minimum 30 s), new monitors default to 60 s whatever the plan's
   minimum, timeout below the interval, then the form, API, MCP, pricing card, docs and the Terms' plan list.
