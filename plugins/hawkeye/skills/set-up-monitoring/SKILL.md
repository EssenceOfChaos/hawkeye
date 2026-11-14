---
name: set-up-monitoring
description: Set up HawkEye monitoring for the project in this repository - heartbeats for its cron jobs and scheduled tasks (with the ping added to each job), and monitors for its public site, health endpoint, API and email records. Use when the user asks to set up, add or check monitoring, uptime or alerting for this project, to monitor their cron jobs, scheduled tasks or backups, or to add HawkEye.
---

# Set up HawkEye monitoring from this repository

HawkEye checks websites, APIs and DNS records from outside, and watches scheduled jobs through heartbeats: each run of a job requests its own
private ping address, and HawkEye alerts when a ping is late or reports a failure. The HawkEye MCP server's tools (`get_account_overview`,
`suggest_monitors`, `create_monitor`, `list_monitors`) do the HawkEye side; you find what to monitor in the code and add the pings to the jobs.

The person stays in charge: **nothing is created in HawkEye and no file is edited until they have approved the plan** (step 4).

## 1. Check the connection

Call `get_account_overview`.

- If the HawkEye tools are missing or the call is refused as unauthenticated, stop and tell the person how to connect: create a key with
  **Read and write** access at https://hawkeye.observer/api-keys, set it as `HAWKEYE_API_KEY` in the environment Claude Code starts from, and
  restart Claude Code. Read and write keys can add monitors on every plan, including Free.
- If `can_add_monitors` is false, the key is read only: you can still make the plan, but say that monitors can't be created until they use a read
  and write key.
- Note `monitor_limit` minus `monitors_used`: the number of monitors there is room for.

Also call `list_monitors` (raise `limit` until you have them all) so you don't propose anything already monitored.

## 2. Survey the repository

Read, don't run. Look for:

**Where it runs in production.** The public domain(s): deploy config (`vercel.json`, `netlify.toml`, `fly.toml`, `render.yaml`, `app.yaml`,
`CNAME`, `Caddyfile`, nginx config, Kubernetes ingress hosts, Terraform), `.env.example`, the README, a `homepage` field. If you can't tell, or
there are several candidates, ask. Never guess a domain to send requests to.

**Scheduled work.** Each job, with its schedule and what it does:

- crontab files, `*.cron`, `/etc/cron.d` entries in Dockerfiles or provisioning, systemd `.timer` units
- GitHub Actions workflows with `on: schedule`, GitLab CI schedules, Kubernetes `CronJob`s
- platform crons: `crons` in `vercel.json`, Render/Fly/Railway/Heroku scheduler config, Cloud Scheduler, EventBridge rules
- in-app schedulers: node-cron, Agenda, BullMQ repeatable jobs, Celery beat, APScheduler, RQ scheduler, Sidekiq-cron, whenever (Rails),
  Laravel's `schedule()`, Spring `@Scheduled`, Quartz, Hangfire, Go `cron` packages, `time.Ticker` loops that do periodic work

Skip jobs that only exist in development or tests.

**Health endpoints** in the code (routes such as `/healthz`, `/readyz`, `/health`, `/api/health`). `suggest_monitors` finds those that answer in
production; one that exists in the code but wasn't found may not be deployed yet, or may be behind authentication.

## 3. Ask HawkEye about the site

For each production domain, call `suggest_monitors` with it. It looks at the live site and returns proposals (home page, health endpoint, api
subdomain, mail records), each with `create_with`: the exact arguments for `create_monitor`. Keep its `reason`s for the plan. Leave out anything
`already_monitored`. A suggestion with `recommended: false` goes in the plan unticked, with its reason.

## 4. Propose the plan and wait for approval

Show one plan, then stop and ask. For example:

| | Monitor | Type | Details |
|---|---|---|---|
| ✓ | example.com | website | home page; also warns before the SSL certificate and domain expire |
| ✓ | example.com health | website | `/readyz` |
| ✓ | Nightly database backup | heartbeat | daily (`0 3 * * *` in `deploy/crontab`), 1 hour grace |
| ✓ | Weekly report email | heartbeat | every 7 days (GitHub Actions `weekly-report.yml`), 2 hours grace |

Then list each file you will change and how (one line each), and the environment variables or secrets the person will have to set (step 6).

Heartbeat settings:

- `check_interval_minutes` is the expected time between runs. For a cron expression, use the time between consecutive runs; when runs are uneven
  (weekdays only, the 1st of the month), use the **longest** gap, so a quiet weekend never alerts, and say that in the plan. The plan's minimum
  interval applies: a job that runs more often than it pings fine, and HawkEye raises a shorter period to the minimum.
- `grace_minutes` is how late a ping may be: a little more than the job's usual run time, at least a few minutes; 60 when unsure.
- Name each heartbeat after what the job does ("Nightly database backup"), not its file name.

If the plan needs more monitors than there is room for, put the most important first (the home page, then jobs whose silent failure loses data:
backups, billing, data syncs), mark the rest as "needs a larger plan", and say so. Don't create past the limit.

## 5. Create the monitors

Only after the person approves, and only what they approved. For each:

- suggestions: call `create_monitor` with its `create_with` arguments unchanged
- heartbeats: `create_monitor` with `type: "heartbeat"`, `name`, `check_interval_minutes` and `grace_minutes`

Keep each result: its `id`, and for heartbeats `heartbeat_ping_url`. If a call is refused, report the message and carry on with the rest; a plan
limit refusal means stop creating.

## 6. Add the pings to the jobs

**Never write a ping address into the repository.** Anyone who has it can make a broken job look healthy. Read it from an environment variable or a
CI secret, one per job: `HAWKEYE_PING_<JOB>` (for example `HAWKEYE_PING_NIGHTLY_BACKUP`).

How a job reports, by the ping address `P`:

- success: request `P`
- failure: request `P/fail`, or `P/<exit status>` (0 is success, anything else a failure)

The ping must never break or slow the job: a short timeout, a few retries, errors ignored, and it runs after the work, including when the work fails.
If the variable is not set (development, a fork), skip the ping silently.

Shell (crontab, scripts, containers): report the command's exit status.

```sh
0 3 * * * /usr/local/bin/backup.sh; s=$?; [ -n "$HAWKEYE_PING_NIGHTLY_BACKUP" ] && curl -fsS -m 10 --retry 3 -o /dev/null "$HAWKEYE_PING_NIGHTLY_BACKUP/$s"
```

GitHub Actions: a last step that always runs, with the address in a repository secret.

```yaml
      - name: Report to HawkEye
        if: always() && env.HAWKEYE_PING != ''
        env:
          HAWKEYE_PING: ${{ secrets.HAWKEYE_PING_WEEKLY_REPORT }}
        run: curl -fsS -m 10 --retry 3 -o /dev/null "$HAWKEYE_PING/${{ job.status == 'success' && '0' || 'fail' }}"
```

Kubernetes `CronJob`: wrap the container's command in `sh -c` the same way as the shell example, with the address from a `Secret` through
`env.valueFrom.secretKeyRef`.

In-app schedulers: at the end of the task, in the language's `finally` (or equivalent), request the address (or `/fail` when the task raised),
with the project's existing HTTP client and a timeout of a few seconds, catching and logging any error. Don't add a dependency for it.

Platform crons that call an HTTP route (Vercel, Cloud Scheduler): add the ping to the end of that route's handler, as for in-app schedulers.

Change nothing else: not the schedules, the jobs' logic, or unrelated files.

## 7. Report

Finish with:

- the monitors created, each with its link: `https://hawkeye.observer/monitors/<id>`
- every environment variable and CI secret to set, with the ping address that goes in each (from `heartbeat_ping_url`). These addresses are
  private: say to put them in the deploy platform's or CI's secret settings, not in a committed file
- that each heartbeat waits for its first ping and won't alert before it, so a job that never got its secret set shows as "waiting", not "down"
- anything skipped and why (over the plan's limit, a domain that couldn't be checked, a job that couldn't be edited safely)

Alerts go to the account's email address from the start; the person can add Slack, Teams, Discord, Telegram or webhooks at
https://hawkeye.observer/notifications.
