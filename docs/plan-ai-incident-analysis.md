# Plan: AI incident analysis

Status: **phase 1 built** on branch `feat/ai-incident-analysis` (2026-11-27), not yet released. Where the build differs from the first draft of
this plan, the text below says what was built (see *Changes while building*). Builds on failure snapshots (`docs/plan-failure-snapshots.md`) and
reuses the setup of AI-drafted incident updates (`internal/incidentdraft`, migration `000042`).

When an outage of a website, API or multi-step monitor is confirmed, HawkEye asks Claude to read the incident's failure snapshot and write a
**short, labeled "likely cause"**: what probably broke, which facts point to it, and what to check first. For example:

> **Likely cause: the origin server behind Cloudflare.** Cloudflare answered 502 from every location, and the server took 2.4 s to answer
> (usually 180 ms). Check the origin's health and logs; DNS and the certificate are unchanged.

## Why

Checkly sells the same idea as "Rocky AI" root cause analysis (researched 2026-11-27 from their product page, docs and pricing page). It is
automatic for each new error group, appears in alerts and their MCP server, and is metered at 10 / 50 / 150 "invocations" a billing period. That
count is shared with their AI chat and script repairs. It runs on OpenAI by default. Much of their pitch rests on browser artifacts (screenshots,
Playwright traces) that HawkEye does not have, but for HTTP and API failures the snapshot already holds the evidence. What is missing is turning
it into one sentence a person can act on.

## Cost and allowances

Haiku 5.5 is $0.10 / $0.50 per million input / output tokens (2026-11). One analysis is about 1,500 input tokens (the instructions plus the
snapshot) and 200 to 500 output tokens, so roughly **$0.0003 to $0.0004**. That is an estimate from the request size, to be replaced by measured
`usage` once built (see *Metrics*).

| Plan | Analyses a month | Most it can cost HawkEye a month |
|---|---|---|
| Free | 25 | about 1¢ |
| Pro | 250 | about 10¢ |
| Agency | 1,000 | about 40¢ |

These are 2.5× to 6× Checkly's allowances, all of them for analyses. Most organizations will use far fewer: there is one analysis per confirmed
incident, and flapping is already grouped. The cap exists to bound abuse (a monitor pointed at something that fails on purpose) and to give the
plans a difference, not to save money. Free is included on purpose: it costs almost nothing and shows the feature off. AI drafts stay paid-only.

## The design

### When it runs

- **Automatically**, once per incident, when the outage is confirmed and the incident has a snapshot (website, API and multi-step monitors;
  heartbeat and DNS incidents have no snapshot and are not analyzed). Only for organizations that turned it on (*Opt-in*).
- **On demand**, from the incident page ("Analyze again"), for example after the owner has edited the monitor. A re-run counts against the
  allowance, like Checkly's. It replaces the stored analysis.
- **Never** when the month's allowance is used up. The incident page then says so and names the next plan, like the other plan limits.

`incidents.Engine.Opened` already writes the snapshot in the incident's transaction. In that same transaction it inserts a `pending`
`incident_analyses` row, which is the job: the analysis never delays the outage alert and survives a restart. The worker claims the row, calls
the model and stores the result in it. Outages that a flapping monitor's "flapping" alert covers are not analyzed.

### What is sent to the model

Only the facts already in the snapshot, plus what the monitor expects:

- the monitor's **kind** (website, API, multi-step API check), its check locations, and for a multi-step check the failing step's name and number;
- HawkEye's failure **category** and **summary**;
- the snapshot `facts` (status code, content type, size, allowlisted headers, certificate issuer and expiry, timings), the `changes` list and
  each location's result;
- what the check asserts: the expected status, and **that** a content or JSON check exists and which kind. The expected text and values are not sent.

**Never sent:** the monitor's address (`final_url` and `server_addr` are left out; "the server address moved to another network" survives
as a change), the monitor's name, request headers, request body, credentials, workflow secrets and saved values, and the **body excerpt**. The
excerpt stays where the snapshot plan put it: in the app only, never in alerts, webhooks, MCP or reports. Sending it to a model is a separate
decision, see *Open questions*.

The prompt treats all of it as data, since headers and summaries come from the monitored site.

### What comes back

A structured output (`output_config.format`), so the shape is checked by the API rather than parsed out of prose:

```json
{"category": "origin_server", "likely_cause": "…", "evidence": ["…", "…"], "next_step": "…", "confidence": "medium"}
```

- `category`: one of `origin_server`, `cdn_or_proxy`, `dns`, `tls_certificate`, `network`, `slow_response`, `application_error`,
  `content_changed`, `authentication`, `rate_limited`, `unknown`.
- `likely_cause`: at most 200 characters. `evidence`: up to 3 short items, each naming a fact from the snapshot. `next_step`: at most 200
  characters. `confidence`: `low`, `medium` or `high`.
- Lengths are checked again on our side, and anything over is cut at a word.
- The model is told to answer `unknown` with low confidence rather than guess, and never to claim it saw anything outside the facts.

Model: **Claude Haiku 5.5** (`claude-haiku-5-5`), effort `low`, the same as the drafts. A `refusal` stop has no server-side fallback for Haiku,
so a declined or failed analysis is stored with its status and does **not** count against the allowance.

### Storage

Migration `0000NN_incident_analyses`:

```sql
CREATE TABLE incident_analyses (
    incident_id     uuid PRIMARY KEY REFERENCES incidents (id) ON DELETE CASCADE,
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    status          text NOT NULL CHECK (status IN ('done', 'declined', 'failed')),
    category        text, likely_cause text, evidence jsonb, next_step text, confidence text,
    model           text NOT NULL,
    input_tokens    integer, output_tokens integer,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE ai_analysis_usage (          -- one row per analysis that counted, so re-runs count and the monthly total is a simple count
    organization_id uuid NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    incident_id     uuid,                 -- not a foreign key: usage outlives the incident within the month
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON ai_analysis_usage (organization_id, created_at);
ALTER TABLE plans ADD COLUMN max_ai_analyses integer NOT NULL DEFAULT 0 CHECK (max_ai_analyses >= 0);
UPDATE plans SET max_ai_analyses = 25 WHERE code = 'free';
UPDATE plans SET max_ai_analyses = 250 WHERE code = 'pro';
UPDATE plans SET max_ai_analyses = 1000 WHERE code = 'agency';
ALTER TABLE organizations ADD COLUMN ai_analysis_enabled boolean NOT NULL DEFAULT false;
```

- The allowance is per **calendar month in UTC**, counted from `ai_analysis_usage`. Free has no billing period, so one rule for everyone. The
  count and the insert happen in one transaction with a per-organization advisory lock, so two jobs cannot both take the last analysis.
- Analyses go with their incident (retention, deleting the monitor or the account) and are in the account export. Usage rows older than
  two months are deleted by the retention job.

### Opt-in

- Off by default for every organization, existing and new. The owner turns it on on the Incidents page, next to the AI drafts switch (`AIDraftsSetting`), with one
  sentence on what is sent to Anthropic and a link to the Privacy Policy section. Turning it on or off is recorded in the security log.
- With no `ANTHROPIC_API_KEY`, the setting says it is unavailable, as the drafts do. A separate `AI_ANALYSIS_ENABLED=false` switch turns off
  only this feature, for an incident with the model provider.
- A downgrade takes effect at once: the plan's allowance is checked when each job runs, not when it is enqueued.

### Where it appears

- **Incident page:** a "Likely cause" panel above the snapshot, labeled "AI analysis · may be wrong", with the evidence, the next step, the
  confidence, "Analyze again" (owner and members) and the month's usage ("12 of 250 analyses used this month").
- **Incident API and MCP:** `GET /incidents/:id/analysis` → `{analysis}` or 404, open to API keys. `has_analysis` on the incident.
  `get_incident` in MCP includes it. Re-running is `POST /incidents/:id/analysis`, session only, like drafts.
- **Recovery alert:** the analysis is included when it exists (by then it always has had time to finish).
- **Outage alert:** not in the first version (see *Phases*), because waiting for the model would delay the alert.
- **AI drafts:** when an analysis is `done` with medium or high confidence, the draft is sent its category as a fixed phrase in a customer's words
  (`incidentdraft.CauseKind`, such as "a domain name (DNS) problem"), never its text. The prompt treats it as a guess: not mentioned in an
  "investigating" update, and never stated more firmly than the team's own updates.

### Limits, metrics and safety

- Per organization: the monthly allowance, plus at most 30 analyses an hour so a burst of incidents cannot use it up in minutes.
- Per IP on the "Analyze again" endpoint, like drafts.
- Metrics: `hawkeye_ai_analyses_total{status}`, `hawkeye_ai_analysis_tokens_total{direction}`, and the job's duration. Alert
  `HawkEyeAIAnalysisFailing` when more than half fail over 15 minutes.
- The worker logs neither the prompt nor the answer, only the incident ID, status and token counts.

## Sync checklist

- **Pricing:** "N AI root cause analyses a month" with the other limits on each pricing card (`PlanDetails.tsx`), and an "AI root cause
  analyses per month" row in the comparison table (`PlanComparison.tsx`), both from `plans.max_ai_analyses`. It differs on every plan, so it is a
  limit, not a line in a card's "plus" list. Not in the taglines.
- **Landing page:** an FAQ answer ("Can HawkEye tell me why a site went down?") and a line in the features section. Optionally a value-strip
  badge.
- **Public docs:** an "AI incident analysis" section on the incidents doc page, and the Claude page (MCP `get_incident` returns it).
- **Privacy Policy** (`LegalPages.tsx`): Anthropic already processes drafts. Add what an analysis sends, that it is opt-in, and that the body
  excerpt is never sent. Have it reviewed before release, as with the body excerpt.
- **Internal docs:** `api.md` (endpoints, errors, `has_analysis`), `architecture.md` (the job), `security.md` (what is sent and never sent),
  `operations.md` (the metric, the alert, the switch), `billing.md` (the plan column).
- `README.md`, `web/public/llms.txt`, `internal/notifications/render.go` (recovery alert).

## Phases

1. **Analysis on the incident page, API and MCP** (about 1.5 build weeks): migration, `internal/incidentanalysis`, the job, the settings switch,
   the panel, the allowance, metrics, docs and pricing row.
2. **In the outage alert** (about 1 week, after measuring phase 1's latency with `hawkeye_ai_analysis_model_seconds` and
   `hawkeye_ai_analysis_latency_seconds`, `docs/operations.md#ai-incident-analysis`): for organizations with analysis on, the dispatcher waits up to a
   few seconds for the analysis before sending the outage alert, then sends without it. Measure first. If p95 is under about 3 s, this is worth
   it, since alerts are where Checkly shows theirs. The job polls for work every 2 to 3 seconds, so phase 2 should wake it with a `NOTIFY` from
   the incident's transaction instead; the latency histogram shows how much that wait costs.
3. **Later, if asked for:** bring your own Anthropic key (no allowance), and sending the body excerpt for organizations that keep it.

## Changes while building

- **The job lives in its own table.** `internal/queue` holds only monitor checks (`check_jobs`, one per monitor and region), so the
  `incident_analyses` row is the job itself: claimed with `FOR UPDATE SKIP LOCKED` and a 2-minute lease, retried up to 3 times with a growing
  pause. `ai_analysis_usage` is keyed by `(incident_id, requested_at)`, so a run taken over after a crash is not counted twice.
- **Statuses** are `pending`, `running`, `done`, `declined`, `failed`, `over_limit` and `off` (turned off, or no longer in the plan, before the
  run started). Runs over the hourly limit wait 5 minutes and are not counted.
- **Flapping monitors' suppressed outages are not analyzed**, so one unsteady site can't use up the month.
- **Any member can ask for an analysis** in the app (session only); API keys and MCP can read analyses but never spend them.
- **Recovery alerts** carry the analysis in email (two facts), chat (one line) and webhooks (`incident.ai_analysis`). PagerDuty does not.
- **The settings switch** is on the Incidents page next to the AI drafts switch.
- **Facts left out besides addresses:** the `Location` header's value, and assertion values (only their kinds are sent).

## Open questions

- **Body excerpt to the model.** It would make `application_error` and `content_changed` answers much better, such as reading a "database
  connection failed" page. It also sends response content to a third party. Keep it out of phase 1, and if added, make it a second opt-in
  inside the body-excerpt setting.
- **Outage alert wait** (phase 2): how long is acceptable? 3 s is the starting proposal.
- **Free plan:** included at 25 here, on the view that showing the feature sells upgrades. Revisit if Free abuse appears.
