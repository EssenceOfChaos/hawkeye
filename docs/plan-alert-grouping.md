# Plan: alert grouping and flap suppression

Status: **built** on branch `feat/alert-grouping` (2026-11-24), not yet released. Roadmap 2, feature #10 (Q3, about 3 build weeks).

Two ways HawkEye sends fewer alerts without hiding any problem:

- **Grouping.** When many monitors fail together (one server hosting 20 client sites), each destination gets one message listing them instead of
  twenty.
- **Flap suppression.** When a monitor goes down and up repeatedly, it gets one "flapping" alert instead of an outage and a recovery alert every few
  minutes. If it then stays down, it is alerted as an ordinary outage.

The roadmap's warnings were that grouping must never hide the first alert, and that the "nobody gets told" case must be tested, as with routing.
Both shaped the design.

## Grouping

### The rule

Per destination and per kind of alert (`outage`, `recovery`, `flapping`, `flapping_ended`):

1. The **first** alert goes out as soon as it is due, exactly as before.
2. An alert that falls due within `ALERT_GROUP_WINDOW` (2 minutes) of the last one of its kind sent to that destination is **held** until that
   window ends (`notification_deliveries.group_held`).
3. When it is due, it is sent with **every other due alert of that kind for that destination** as one message.
4. Alerts that fall due at the same moment (several held by the same quiet hours, or a dispatcher catching up) are sent together, without waiting.

So no alert waits longer than the window, a destination gets at most one message of a kind per window during a storm, and the first message is never
late. A 20-site server failing over a few minutes sends about three messages: the first site at once, then one or two lists.

### Why in the dispatcher, not when alerts are queued

The alerts of a storm are queued seconds or minutes apart: each monitor fails on its own schedule. Grouping when queuing would mean guessing whether
more are coming. The dispatcher instead looks back ("was something of this kind sent here in the last two minutes?"), which needs no guess, and
merges whatever is due at once, which also covers quiet hours ending at 7:00 with twenty alerts held.

### Making "never hide the first alert" true

- **A race between dispatchers.** Two dispatchers claiming two alerts for one destination at the same moment could both see the other's claim and
  both hold back, so nothing would go out. The claim is therefore a transaction that locks the destination's `notification_settings` row
  (`FOR NO KEY UPDATE`, so queuing new deliveries for it isn't blocked) before deciding. One dispatcher decides at a time; the other then sees the
  first one's attempt and holds its alert, or the first takes both. Tested with two dispatchers racing (`TestConcurrentDispatchersNeverHoldTheFirstAlert`).
- **A held alert is always sent.** `cancelHeld` (the alert delay and quiet hours rule that drops an alert whose problem ended before it was due) skips
  `group_held` rows. Grouping changes how alerts are packaged, never whether they are sent (`TestAHeldAlertIsSentEvenIfItsMonitorRecoversFirst`).
  The grouped message says when a listed monitor is already back online.
- **Held once.** An alert that has been held, or attempted, is never held again, so a steady trickle can't postpone one forever.
- **Routing first.** Grouping happens per destination, after routing, so a client's channel only ever lists that client's monitors.

### What isn't grouped

- **Webhooks and PagerDuty** always get one event per alert. Integrations expect one event per incident, PagerDuty pairs each trigger with its resolve
  by dedup key, and PagerDuty has its own grouping.
- **Slow-response, certificate, domain and DNS change alerts.** They don't come in storms from one cause; easy to add to `groupable` if that changes.

### Messages

`notifications.Group` turns the alerts into one `Alert` with `Group` set. Email: "HawkEye alert: 3 monitors may be offline", one fact per monitor
(address and latest result), the first 25 named and the rest counted. Chat (Slack, Discord, Teams, Telegram): one line per monitor, 10 named, each
line at most 240 characters, so every app's size limit holds however many monitors fail (tested per app). Recoveries, flapping and "stopped
flapping" have their own wording.

### Customer choice

On by default for every organization, existing ones included (`organizations.alert_grouping`). The owner turns it off on the Alerts page (`PUT
/notification-settings/grouping`, in the security log); members see which way it is set. `ALERT_GROUP_WINDOW=0` turns it off for the installation.

## Flap suppression

### When a monitor is flapping

When an outage is confirmed (`incidents.Engine.Opened`), HawkEye counts the monitor's incidents that started in the last hour. The **third** opens a
flap episode (`flap_episodes`, one open per monitor) and sends one `flapping` alert instead of the outage alert. The first two outages were alerted
normally: a team hears "down, up, down, up, flapping", then nothing per outage.

While the episode is open, each new outage is still an incident and still counts in uptime and on status pages, but it carries `alerts_paused` and
sends no outage or recovery alert. The Incidents page labels them "no alert (flapping)", and the monitor shows **Flapping**.

The `flapping` alert goes out outside quiet hours but without the alert delay, like slow-response alerts: three outages in an hour are already a
confirmed pattern.

### How it ends (`incidents.FlapJob`, every 30 seconds)

| End | When | Alert |
|---|---|---|
| `settled` | no open incident, and the last one ended 30 minutes ago | one `flapping_ended` ("has stopped flapping") |
| `stayed_down` | an outage of the episode has lasted 15 minutes, or 4 check intervals if longer | that outage's ordinary `outage` alert, saying it had been flapping; its recovery is then alerted as usual |
| `ended` | flap suppression turned off, or the monitor paused | an outage still open is alerted at once; otherwise one `flapping_ended` ("alerts are back on") |

**The "nobody gets told" case** is `stayed_down`: a flapping monitor that goes down for good is never kept quiet for long. The minimum of four
intervals stops a 5-minute Free monitor, whose every outage lasts about 10 minutes, from being "alerted anyway" on every outage. Tested:
`TestAFlappingMonitorThatStaysDownIsAlerted`, `TestTurningFlapSuppressionOffMidOutageAlertsIt`, `TestAShortOutageOfAFlappingMonitorIsNotAlertedEarly`.

The job decides nothing during a maintenance window. It locks the monitor row first, as recording a check result does, so an outage can't join an
episode while it is being closed, and several workers can run it.

### PagerDuty

One dedup key per episode, `hawkeye:flapping:<episode>`: `flapping` triggers it (severity `error`), the outage of an episode that stayed down triggers
it again (`critical`, updating the open alert), and `flapping_ended` or that outage's recovery resolve it. So PagerDuty shows one alert for the whole
episode, and it always closes.

### Customer choice

`monitors.flap_suppression`, on by default for every monitor, existing ones included. Turned off with **Hold back alerts while it's flapping** under
Alert timing, the API (`flap_suppression`) or MCP `create_monitor`. Turning it off mid-outage alerts that outage at once.

## Schema (migration `000057`)

- `organizations.alert_grouping boolean DEFAULT true`; `notification_deliveries.group_held boolean DEFAULT false`, and an index on
  `(notification_setting_id, event_type, last_attempt_at)` for "what was sent here recently".
- `monitors.flap_suppression boolean DEFAULT true`; `flap_episodes`; `incidents.flap_episode_id` and `incidents.alerts_paused`.
- `notification_deliveries.flap_episode_id` with the `flapping` and `flapping_ended` event types in the shape constraint, idempotent per episode,
  recipient and event.

Constant defaults are metadata-only changes, so no table is rewritten.

## Metrics and operations

`hawkeye_notification_groups_total{event}` counts messages that combined several alerts. A jump means many monitors failed together: a shared host,
or our own network (check the canary). `ALERT_GROUP_WINDOW` and the queries for "why didn't this outage alert?" are in `docs/operations.md`.

## Decisions (2026-11-24, defaults to confirm)

1. **Both are on by default, for existing customers too.** Grouping only changes packaging, and flap suppression never hides an outage that lasts.
   Owners can turn grouping off; each monitor can turn flap suppression off.
2. **Window 2 minutes; flapping at 3 outages in an hour; settled after 30 minutes up; "stayed down" after 15 minutes or 4 intervals.** Constants in
   `internal/notifications/dispatcher.go` and `internal/incidents/flap.go`; the window is also `ALERT_GROUP_WINDOW`.
3. **Webhooks and PagerDuty are never grouped.**
4. **The flapping alert ignores the alert delay** (it respects quiet hours). A customer who set a 10-minute delay to ignore blips still hears once
   if a site blips three times in an hour.
