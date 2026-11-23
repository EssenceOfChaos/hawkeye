# Plan: DNS change alerts (drift and hijack detection)

Status: **built** on branch `feat/dns-change-alerts` (2026-11-22), not yet released. Roadmap 2, feature #5 (Q1, about 2 build weeks).

HawkEye will notice **when a DNS record changes**, without the customer having to type what it should be: a new mail server, a different
address for the website, a name server that isn't theirs, an SPF or DMARC record that was edited. The alert shows the record **before and
after**, and one click on **"This change was intended"** accepts the new answer.

Why customers care: a changed record is how a domain gets hijacked, how mail quietly starts going elsewhere, and how a "small DNS tweak" by
a colleague or a hosting provider takes a site down hours later. Today a DNS monitor only fails when the answer stops matching a value the
customer typed in, and most people don't know what to type.

## How DNS monitors work today

- A DNS monitor looks up one record type (A, AAAA, MX, NS or TXT) for a host on a schedule (`checks.CheckDNS`). It fails, and opens an
  incident, when there's no answer, or when an **expected value** is set and no answer matches it.
- **The answers are never stored.** Only whether the lookup succeeded and matched is recorded, and the summary names the customer's own expected
  value, never the answers. The privacy policy says so: "We do not store the answers we receive."
- A and AAAA answers that are private addresses are refused (`ErrBlocked`), so a monitor can't map this server's network.
- **CNAME isn't offered.** Go's resolver only reports where a chain of CNAMEs ends, so a CDN adding a hop of its own would look like a change.
- With several check locations, each location looks the record up with its own resolver, and most locations must agree on an outage.
- "Set up from your domain" (`internal/discovery`) suggests an MX monitor for the domain.

## The design

### What counts as a change

Each DNS monitor gets a **baseline**: the set of answers it accepts as normal, kept **per check location**. GeoDNS legitimately answers
differently in Amsterdam and Los Angeles.

- **The first successful lookup sets the baseline silently.** No alert, nothing to accept.
- **A change has to be confirmed.** A different set of answers must be seen in **2 checks in a row** at a location. With several locations, a
  **majority** of them must have confirmed it. A one-off odd answer, or one resolver's bad cache, never alerts. This mirrors how outages are
  confirmed.
- **Comparison is by set:** order doesn't matter, and host names are compared lower-case without the trailing dot, as today.
- **Address rotation (A and AAAA):** CDNs and load balancers hand out a changing subset of a pool of addresses. By default, an A or AAAA
  monitor compares against the **pool of answers seen in the last 7 days** at that location, so it only alerts when an address appears that
  hasn't been seen in that time (the hijack case) or when every known address is gone. A per-monitor **"Alert on any change"** setting makes
  it strict, for a site with one fixed address. MX, NS and TXT records are always compared exactly: they don't rotate.

### What happens on a change

A confirmed change is **not an outage**. The site may well be up; something about it is different. It's handled like a slowdown:

- A **DNS change** is opened (a new `dns_changes` row) with the before and after answers, and **one alert** goes to the monitor's alert
  destinations: email, chat, webhook, and PagerDuty as a *warning*, as slowdowns do. Alert routing, quiet hours and maintenance windows apply
  as for other alerts.
- **The monitor stays up**, and uptime isn't affected. If the expected value no longer matches, that's still an outage through the existing
  rule. The two work side by side.
- The monitor page shows a banner, "MX record changed on Nov 22, 3:14 PM: mail.example.com → mx.attacker.net", with **"This change was
  intended"**. Accepting makes the new answers the baseline, closes the change (resolving the PagerDuty alert), and is recorded in the security
  log with who accepted it.
- **If the record changes back** to the baseline before anyone accepts, the change closes by itself as "reverted", with one follow-up alert.
- Until a change is accepted or reverted, **further changes don't alert again**, but the banner shows the latest answers.
- Past changes are listed on the monitor page (date, before, after, accepted by whom or reverted) and kept as long as the plan's history.

### Customer choice

- **New setting `dns_change_alerts`** (boolean, DNS monitors only), **on by default**. Changes alert without any setup, and the customer can
  turn it off per monitor. Existing DNS monitors get it on as well (decision 2): their first lookup after release sets the baseline silently,
  so nobody gets an alert from the upgrade itself.
- **`dns_change_strict`** (A/AAAA only, off by default): alert on any change instead of only on never-seen addresses.
- **The expected value stays optional,** and both can be used together: change alerts for "something changed", the expected value for "it must
  be exactly this".
- **Available on every plan.** It's part of DNS monitors, which every plan has.

### Answers are stored now

Detecting a change and showing before and after means **keeping the answers**: the baseline per location, the pending candidate, the 7-day
address pool, and the before and after of each change. These are public DNS records of the customer's own domain, not personal data, but
it's a change to what the privacy policy promises (decision 1):

- Stored only for DNS monitors with change alerts on. Turning them off deletes the monitor's baseline and pool.
- Private addresses are still never stored or compared (they are refused, as today).
- Changes are kept for the plan's history period and removed with the monitor.
- TXT answers are capped (each answer up to 1,000 characters, at most 20 per set) so an unusual record can't bloat the table.
- The privacy policy's DNS line changes from "We do not store the answers we receive" to say that, for DNS change alerts, the answers are kept
  to compare them, for the plan's history period.

### CNAME records (decision 3)

Left out of this version. Watching a CNAME properly needs asking for the CNAME record itself rather than following the chain, which needs a
DNS client library (for example `miekg/dns`) and egress rules for talking to resolvers directly. An A or AAAA monitor on the same name already
notices when where it points changes. A later step can add CNAME once the library question is settled.

### "Set up from your domain" suggests change alerts

The domain setup (`internal/discovery`) adds DNS change monitors for the records that matter most, with change alerts on:

- **NS** for the domain (who controls its DNS: the hijack case),
- **MX** (already suggested; now with change alerts),
- **TXT** for SPF on the domain, and **TXT** on `_dmarc.<domain>` (email authentication).

They count toward the plan's monitor limit like any monitor. On Free (5 monitors), only NS and MX are ticked by default, so the website
monitors still fit; the customer can tick the others.

## Data model (migration `000054`)

```sql
ALTER TABLE monitors
    ADD COLUMN dns_change_alerts boolean NOT NULL DEFAULT true,   -- used by DNS monitors only
    ADD COLUMN dns_change_strict boolean NOT NULL DEFAULT false;  -- A/AAAA: alert on any change, not only on unseen addresses

CREATE TABLE dns_baselines (            -- per monitor and check location
    monitor_id    uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    region        text NOT NULL,
    answers       text[] NOT NULL,      -- the accepted set, sorted
    pending       text[],               -- a different set seen at the last check, not yet confirmed
    pending_count smallint NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL,
    PRIMARY KEY (monitor_id, region)
);
CREATE TABLE dns_seen (                 -- the 7-day address pool for A/AAAA
    monitor_id uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    region     text NOT NULL,
    answer     text NOT NULL,
    last_seen  timestamptz NOT NULL,
    PRIMARY KEY (monitor_id, region, answer)
);
CREATE TABLE dns_changes (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    monitor_id  uuid NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    record_type text NOT NULL,
    before      text[] NOT NULL,
    after       text[] NOT NULL,
    detected_at timestamptz NOT NULL,
    status      text NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'accepted', 'reverted')),
    closed_at   timestamptz,
    accepted_by uuid REFERENCES users (id) ON DELETE SET NULL
);
-- At most one open change per monitor.
CREATE UNIQUE INDEX dns_changes_open_idx ON dns_changes (monitor_id) WHERE status = 'open';
```

Notifications get a `dns_change_id` and two event types, `dns_change` and `dns_change_closed`, like slowdowns. `dns_seen` rows older than 7
days are pruned by retention.

## Where it happens

`checks.CheckDNS` returns the answers (`Result.DNSAnswers`, sorted and capped) alongside today's outcome; they're never written to
`check_results` and never put in an error summary. `Store.RecordTx`, in the same transaction that records the result, folds them into the
location's baseline and, once a change is confirmed by the rule above, calls a new `Incidents.DNSChanged` (and `DNSChangeClosed` on a
revert). So the change, its alert and the result commit together, as outages do. Maintenance windows and deploys hold the alert like any other.

## Files touched

| Area | Files |
|---|---|
| Checking | `internal/checks/dns.go` (answers on the result), `internal/checks/dnschange.go` (baseline, pool and confirmation rules, unit-tested on their own), `internal/checks/store.go` |
| Changes and alerts | `internal/incidents` (open, revert, accept), `internal/notifications` (two events, email and chat wording, PagerDuty warning, webhook payload) |
| Monitors and API | `internal/monitors` (two settings), `internal/httpapi` (`GET /monitors/:id/dns-changes`, `POST /monitors/:id/dns-changes/:changeID/accept`), `internal/mcpserver` (changes in `get_monitor`) |
| Setup from a domain | `internal/discovery` (NS, SPF and DMARC suggestions) |
| Retention | `internal/retention` (`dns_seen` after 7 days, `dns_changes` with history) |
| Web | DNS monitor form (two toggles), monitor page (banner, accept, history), domain setup suggestions |

## Feature sync

- **Pricing cards:** "Cron job and DNS monitoring" becomes "Cron job and DNS monitoring, with DNS change alerts" (every plan).
- **Landing page:** the DNS feature card mentions change and hijack alerts; the FAQ answer "Can HawkEye monitor my DNS records?" says it alerts
  on changes without you entering a value.
- **Public docs:** the DNS monitoring guide gets "Change alerts" (what counts, rotation, accepting, turning it off).
- `README.md`, `web/public/llms.txt`; internal docs `api.md`, `architecture.md`, `security.md` (stored answers).
- **Legal:** the privacy policy's DNS line (decision 1) and its "Last updated" date.
- **Copy sync tests:** plan cards and landing page tests.

## Tests

- **Rules (unit):**
  - The first answer sets the baseline silently.
  - A different set needs 2 checks in a row; a one-off is forgotten.
  - Order and case don't matter.
  - A/AAAA rotation within the 7-day pool doesn't alert, but a never-seen address or the pool emptying does.
  - Strict mode alerts on any change.
  - MX, NS and TXT are compared exactly.
  - Sets are capped.
- **Store and incidents:**
  - One alert per change, and nothing further while it's open.
  - A revert closes it with one follow-up.
  - Accepting sets the new baseline and resolves the PagerDuty warning.
  - With several locations, a change confirmed at one location only doesn't alert, and GeoDNS differences between locations never do.
  - Maintenance holds the alert.
  - Turning the setting off deletes the baseline.
  - The answers never appear in `check_results` or in summaries.
- **Existing monitors:** after the migration, the first lookup sets a baseline with no alert.
- **Discovery:** the new suggestions, and which ones are ticked on Free.
- **Web:** the toggles, the banner with before and after, accepting, and the history.

## Order of work (about 2 weeks)

1. The rules in `dnschange.go` with their unit tests; the answers on `Result`.
2. Migration, the store integration (baseline, pool, confirmation), and incidents (open, revert, accept).
3. Notifications: events, wording on every channel, PagerDuty and webhooks.
4. API, MCP, monitor settings and retention.
5. Web: form toggles, the monitor page banner and history, and discovery suggestions.
6. Feature sync (including the privacy policy), then a local run against a name whose record I change (a test zone, or a fake resolver in the
   worker).

## Changed while building

- **The closing event is `dns_change_closed`,** covering both a change accepted as intended and one that changed back, so accepting also resolves
  the PagerDuty warning and tells the team who accepted it.
- **`dns_baselines` also keeps `last` and `changed`** (the latest answers, and whether the location has a confirmed change), which accepting and
  the multi-location rule need.
- **The changes endpoint also returns `current`,** the latest answers, shown as "Current answer" on the monitor page.

## In the app

- **DNS monitor form:** a "Change alerts" section: "Alert me when this record changes" (on), and for A/AAAA "Alert on any address change"
  (off). The expected value is labeled optional.
- **Monitor page:** while a change is open, a warning banner (like the slowdown one) with before, after and **This change was intended**; then
  a "DNS changes" section with the current answer and past changes (accepted by whom, or changed back). Any member can accept, as members
  can edit monitors; the security log records who.
- **Monitors list:** a "Changed" badge beside the status while a change is open.
- **Domain setup:** the new suggestions, each with a line on why it matters.
- **Alerts:** "DNS record changed for example.com", before and after, and a "Review the change" button.

## Decisions (2026-11-22): the recommendations below were chosen

1. **Storing DNS answers.** Recommended: yes, for monitors with change alerts on. There's no other way to show "before → after", and they
   are public records of the customer's own domain. It changes a promise in the privacy policy, so the new wording needs your review.
2. **On by default, existing monitors included.** Recommended: yes. The first lookup after release sets the baseline silently, so the upgrade
   itself never alerts, and anyone can turn it off per monitor. The alternative is on for new monitors only.
3. **CNAME.** Recommended: leave it for a later step. It needs a DNS client library and direct access to resolvers, and an A/AAAA monitor on
   the same name already catches where it points.
4. **Domain setup suggestions on Free.** Recommended: suggest NS, MX, SPF and DMARC monitors, ticking only NS and MX on Free so the website
   monitors still fit in 5.
