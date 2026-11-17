# Plan: checks under a minute (Agency)

Status: **in progress** (2026-11-16). Load tested; `WORKER_CONCURRENCY` raised to 30 and a queue-wait alert added; hourly result rollups built; sub-minute monitors keep
individual results for 30 days (decided 2026-11-16). Next: the interval change itself.

Agency customers will be able to check a monitor every 30 seconds. **New monitors keep the 60-second default**; a shorter interval is a choice
an Agency customer makes per monitor. Free and Pro are unchanged.

## How it was measured

`cmd/loadtest` creates its own scratch database, applies the 30-second schema change there only, and runs HawkEye's real code:

- **live** runs the scheduler, worker and result store with production settings against a local fake site (5% of sites slow, 1% never answering),
  and reports the gaps between checks, queue waits, scheduler lag and database writes.
- **read** fills in history at an interval and times the queries that read it: dashboard uptime, status pages, charts, history and monthly reports.

PostgreSQL 17 ran in a container capped at 2 CPUs and 2 GB, on 2026-11-16. That was meant to match production, but production turned out to be
`vc2-1c-2gb` (1 vCPU / 2 GB for everything, 55 GB disk; found 2026-11-17): resize it before 30-second checks are sold at volume
(`docs/plan-high-availability.md`, step 1).

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

**With hourly rollups** (`internal/rollups`), the same queries on the same data:

| 50 monitors, 90 days (median of 5) | at 60 s | at 30 s |
|---|---|---|
| Dashboard uptime, all monitors, 30 days | 16 ms | 14 ms |
| Dashboard uptime, all monitors, 90 days | 52 ms | 48 ms |
| Public status page, 50 monitors, 90 days (uncached) | 83 ms | 93 ms |
| One monitor's uptime, chart or monthly report | under 10 ms | under 10 ms |

The interval no longer matters to these. The totals take about 21 MB here (1.5% of the raw results at 60 s), about 340 MB for a full Agency
account's year whatever the interval, and were built from 13 million results in 24 seconds.

## Order of work

1. **Done:** `WORKER_CONCURRENCY` default 30 (the worker's pool follows it: 34 connections), the `HawkEyeChecksDelayed` alert, and `cmd/loadtest`.
   Production takes the value from Infisical, so it changes there.
2. **Done: rollups.** Hourly totals per monitor (checks, successes, response-time sum, count and maximum), added up by a job 15 minutes after
   each hour, read by uptime, status pages, dashboards, badges, charts and monthly and weekly reports together with raw results for the rest
   (same answers, checked by an equivalence test). This also makes today's 60-second accounts faster.
3. **Done: shorter raw retention for sub-minute monitors** (decided 2026-11-16). Monitors checked more often than once a minute keep individual
   results for 30 days (`rollups.RawRetention`) and their hourly totals for the plan's whole history, so uptime, status pages, charts and reports
   keep 365 days and a full Agency account's year stays near 4 GB instead of 44 GB. Retention deletes whole hours, only hours the totals already
   cover, and the rollup job never replaces totals whose results are gone. The rule follows a monitor's current interval. A range reaching back
   past 30 days for such a monitor loses only the partial hour at its start (which comes from raw results); step 4 tells the person, on the check
   history list, that individual checks go back 30 days.
4. **Done: the interval change** (migration `000048`). Websites, APIs and DNS records may run every 30 s on Agency; heartbeats and multi-step API
   checks keep 60 s (a multi-step run may take 60 s). New monitors default to 60 s whatever the plan allows (`monitors.DefaultIntervalSeconds`),
   and a timeout is at most half the interval, so a 30-second check gives up after 15 s; bulk `set_interval` follows the same rules. The form
   has a "Check every 30 seconds" switch, shown only when the plan allows it and the request does not change data. Was: migration (monitors may run every 30 s; Agency's minimum 30 s), new monitors default to 60 s whatever the plan's
   minimum, timeout below the interval, then the form, API, MCP, pricing card, docs and the Terms' plan list.
