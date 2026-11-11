# Plan: multi-step API checks (synthetic API monitoring)

Status: **built** (2026-11-11). Plan limits as recommended (Free 1 workflow of up to 3 steps, Pro 5 of 10, Agency 50 of 10; each counts as a monitor).
The API is in `docs/api.md` (*Multi-step API checks*), security in `docs/security.md`, and the user guide at `/docs/api-workflows`. Browser checks and
the "Validating demand" steps are still open.

A multi-step API check (a "workflow") runs a short sequence of HTTP requests in order, carrying values from one response into the next request,
and fails when any step fails: for example, sign in → fetch the account → check the plan is active. It answers "does the thing my customers do
still work?" rather than "does the URL answer?". This plan covers that, and only that. Browser checks (a real browser clicking through a site) are
a separate, later decision; see *Not in this plan*.

## Should we build it?

Yes, as the next significant feature, with three changes to the advice it came from.

**Why yes.**

- HawkEye already has most of the parts. API checks send any method with headers, a body and stored credentials (encrypted), and check the
  status and the body: up to 10 assertions, including JSON paths (`docs/api.md`, *API checks*). The SSRF-safe client, regions with quorum, the
  failure thresholds, incidents, alert routing and status pages all work per monitor. A workflow is mostly a loop over requests HawkEye can
  already make, plus a way to pass values between them.
- It gives agencies a reason to pick Agency beyond more monitors: "check that each client's login and checkout API work", not only that the
  home page loads.
- It costs us little to run: a workflow is a few ordinary HTTP requests, not a browser.

**What to change from the advice.**

1. **Its first priority, response and JSON assertions, already exists.** Start at multi-step workflows with variables and authentication.
2. **Don't meter API runs.** HawkEye's plans limit counts (monitors, check interval, history), and billing has no usage metering. API runs are
   cheap, so limit the *number of workflows*, *steps per workflow* and the *fastest interval* per plan instead. Metered runs and add-on packs
   need Stripe usage billing, and are only worth building for browser checks, where a run really costs something.
3. **Keep browser checks out until demand is shown.** They are a different product to run: headless Chromium workers with hundreds of MB of
   memory per run, strict isolation (a browser loads whatever the page asks for, so `safehttp` can't police it; only the worker network's
   egress firewall can), screenshot storage, and per-run costs. The roadmap already scores them lowest for ease (#29). Put a waitlist on the
   pricing page first (see *Validating demand*).

**Naming.** "User journey monitoring" is a strong name for browser checks later. For this feature, "multi-step API checks" (or "API workflows")
says what it is. The landing page line "Your website is online. But is it working?" fits both.

**Caveat.** As with the roadmap, there is no customer data behind the demand estimate. A preview list and a question in the onboarding emails
would cost almost nothing and would make the browser decision much easier.

## Decisions for you

| Decision | Recommendation |
|---|---|
| Which plans get workflows | Free: 1 workflow of up to 3 steps (a taste that sells the upgrade). Pro: 5 workflows, 10 steps. Agency: 50 workflows, 10 steps |
| Do workflows count toward the monitor limit | Yes: each workflow is one monitor, and the workflow limit applies on top |
| Fastest interval | The plan's usual minimum, raised to 5 minutes when any step uses POST, PUT, PATCH or DELETE (the existing rule for API checks) |
| Price changes | None now. Revisit with browser checks |

## What a workflow is

- **Steps** (up to the plan's limit, at most 10): each has a name, method, URL, headers, body, optional authentication, an expected status, the
  existing assertions, and **extractions**. An extraction saves a value from the response for later steps: a JSON path (`data.token`), a response
  header (`Location`), or a cookie.
- **Secrets**: named values the workflow uses, such as an API key or a test account's password. They're stored encrypted like monitor credentials,
  never returned by the API, and never shown in results or alerts.
- **Variables**: later steps use `{{name}}` in the URL, headers and body, for an extracted value or a secret (`{{secret.password}}`). Plain
  substitution only, with no expressions or scripts: that keeps workflows safe to run on our workers and easy to understand. Inside a JSON
  body, substituted values are JSON-escaped.
- **Cookies**: optional per workflow. A cookie jar lives for one run and is thrown away.
- **Timing**: each step has its own timeout, and a run has an overall limit of 60 seconds.

A run stops at the first failing step. The monitor's result is that step's result, with its name: "Step 2 'Get account': expected status 200,
got 401".

## Design

### Data model (migration `000036_workflows`)

- `monitors.kind` gains `workflow`. A workflow monitor's own request fields (URL, method, headers and so on) are unused, like a heartbeat's.
- `monitor_steps`: `monitor_id`, `position`, `name`, `method`, `url`, `headers jsonb`, `body`, `auth_type`, `auth_username`, `auth_header_name`,
  `auth_secret` (sealed), `expected_status_code`, `assertions jsonb`, `extractions jsonb`, `timeout_seconds`. Deleted with the monitor.
- `monitor_secrets`: `monitor_id`, `name`, `value` (sealed, bound to the monitor and name). Erased at once when the monitor is deleted, like
  credentials today.
- `check_results.steps jsonb`: per step, its name, status code, time and whether it passed. Never response bodies, headers or variable values.
  It follows the plan's history retention like every result.
- `plans.max_workflows`, `plans.max_workflow_steps`, with an entitlement check (`plans.CanCreateWorkflow`) in the same transaction as the insert.

### Engine (`internal/checks/workflow.go`)

`RunWorkflow(ctx, steps, secrets)` runs the steps in order with the existing `safehttp` client, returning one `Result` plus the per-step
summaries.

- Every request goes through `safehttp`, so each step's address, and each redirect, must resolve to a public address on an allowed port. That
  holds **after** substitution: a variable can't point a step at an internal host.
- Substitution happens just before each request. A reference to a value that doesn't exist fails the run as a configuration problem.
- Responses are read only when an assertion or an extraction needs them, up to the existing 1 MiB. Extracted values are capped at 4 KB each,
  and live only in memory for the run.
- **Redaction:** before a summary is stored or alerted on, every secret and extracted value is replaced with `[hidden]`, so a token echoed in
  an error never reaches the database or an email.
- **Failure categories:** application failures use the existing ones (`http_status`, `assertion`, `connect`, `timeout` and so on), so failure
  confirmation, regions, incidents and alerts work unchanged. A broken workflow, such as a missing variable or an extraction path that isn't in
  the response, gets a new category, `workflow_config`. That way "your test is broken" can be told apart from "your API is broken" in the
  summary and on the monitor page.

### Worker and scheduler

No new infrastructure. A check job for a workflow monitor calls `RunWorkflow` instead of `Check`. Regions, quorum, maintenance windows, slow
alerts (on the run's total time) and incident creation are unchanged.

### API

- `POST /monitors` with `type: "workflow"`, `steps: [...]` and `secrets: {name: value}`. `PATCH` replaces the steps; secrets work like `auth`
  (write-only, and kept when omitted). `GET` returns steps and `secrets: [{name, set: true}]`, never values.
- `POST /monitors/test-workflow`: runs an unsaved workflow once and returns each step's outcome, without storing anything. It is essential for
  building one. It gets the same safety rules, a verified email, and a tight rate limit: it makes our servers send requests on demand.
- The API key and MCP rules apply as for other monitors: creating one through a key needs write access on a paid plan. MCP's `get_monitor`
  lists the step names; MCP doesn't create workflows in the first version.

### App

- **Workflow editor** on the monitor form: a list of steps (each like today's API check form), "Add step", and reordering. Each step has
  "extract a value" rows, and the form has a secrets section and a variable picker.
- **"Run test"** shows each step's status, time and, for a failure, the reason, before saving.
- On the monitor page, the latest run's steps (✓ or ✗ and time), and the failing step's name on incidents.

### Security

- SSRF: every request, after substitution and on every redirect, goes through `safehttp`, as above.
- Secrets: sealed at rest, write-only through the API, and redacted from everything stored or sent.
- Abuse: a workflow can send up to 10 requests a run, so limits are per workflow (steps, total time) as well as per plan. Write methods keep the
  verified-email and 5-minute rules. `test-workflow` is rate-limited per account and per IP.
- Nothing from the response is stored except status codes and timings.
- Update `docs/security.md` and `docs/security-review.md`. Counsel should glance at the privacy policy: test-account passwords that customers
  store are a new kind of secret, though covered by how credentials are handled today.

## Build order and estimate

| Step | Work | Estimate |
|---|---|---|
| 1 | Engine: substitution, extraction, redaction, the overall timeout, `workflow_config`; unit tests against a local test server | 1.5 weeks |
| 2 | Migration, monitor validation and storage, plan limits, API and `test-workflow`, worker wiring; integration tests (including SSRF after substitution and redaction) | 1 week |
| 3 | App: workflow editor, Run test, monitor page and incident details | 1.5 weeks |
| 4 | Docs and feature sync: `/docs/api-workflows`, pricing cards, landing page, FAQ, `llms.txt`, API, security and privacy docs, MCP `get_monitor` | 0.5 weeks |

About **4–5 weeks** for one developer. Each step can merge on its own behind the plan flag (`max_workflows = 0` hides it), so it can ship
gradually.

## Validating demand

- Add "Browser checks (coming soon): join the waitlist" to the pricing page and the workflow editor, stored like a contact-form topic, and count
  sign-ups.
- Ask in the onboarding and weekly summary emails which workflows people would want checked.
- After a month with workflows live: how many paying organizations create one, how many steps they use, and whether Pro → Agency upgrades move.

## Not in this plan

- **Browser checks** (Playwright and Chromium workers, screenshots, run metering and add-ons). Decide after the waitlist and workflow numbers.
  If yes, start with a cost test: one worker, measured CPU, memory and seconds per run, to set run allowances and a price.
- No-code recorders, visual regression, AI-generated tests, scripting in workflows, file uploads, and GraphQL- or gRPC-specific steps.
