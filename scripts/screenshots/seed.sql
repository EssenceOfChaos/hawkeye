-- Sample data for the landing page screenshots (docs/branding.md, "Screenshots"). Run against a THROWAWAY local database after registering owner@example.com:
--   psql ... -v org=<organization id> -f scripts/screenshots/seed.sql
-- It makes a Pro-plan account with five monitors, 24 hours of one-minute checks with believable response times, one 14-minute outage that was confirmed and
-- recovered a few hours ago, and a few older incidents. Names and addresses are obviously fictional (example.com). The worker must NOT be running while you capture.
\set ON_ERROR_STOP on
SELECT setseed(0.42); -- the same "random" jitter every time
DELETE FROM monitors WHERE organization_id = :'org'; -- re-runnable: starts from this account's monitors being empty

UPDATE users SET email_verified_at = now() WHERE email = 'owner@example.com';
UPDATE subscriptions SET plan_id = (SELECT id FROM plans WHERE code = 'pro') WHERE organization_id = :'org';

CREATE TEMP TABLE sample (name text, url text, base int, amp int, jitter int, ssl_days int, phase numeric); -- phase: each monitor has its own daily rhythm
INSERT INTO sample VALUES
  ('Marketing site',  'https://www.example.com/',      180, 35, 30, 74, 1.2),
  ('Customer portal', 'https://app.example.com/',      330, 60, 55, 38, 2.9),
  ('Public API',      'https://api.example.com/health', 95, 20, 18, 91, 4.4),
  ('Checkout',        'https://shop.example.com/checkout', 410, 70, 70, 27, 0.4),
  ('Documentation',   'https://docs.example.com/',     140, 25, 25, 52, 5.6);

INSERT INTO monitors (organization_id, name, url, check_interval_seconds, timeout_seconds, current_status, ssl_expires_at, ssl_checked_at, created_at, next_check_at)
SELECT :'org', name, url, 60, 10, 'up', now() + make_interval(days => ssl_days), now() - interval '1 minute', now() - interval '40 days', now() + interval '1 minute' FROM sample;

-- 1440 checks per monitor (every minute for 24 h). Checkout is unreachable from 3h29m to 3h16m ago (14 failed checks).
INSERT INTO check_results (monitor_id, checked_at, finished_at, success, http_status_code, response_time_ms, error_category, error_summary, region)
SELECT m.id, s.ts, s.ts + make_interval(secs => CASE WHEN s.ok THEN s.rt / 1000.0 ELSE 10 END), s.ok,
       CASE WHEN s.ok THEN 200 END, CASE WHEN s.ok THEN s.rt END,
       CASE WHEN s.ok THEN NULL ELSE 'timeout' END, CASE WHEN s.ok THEN NULL ELSE 'Request timed out' END, 'primary'
FROM monitors m
JOIN sample p ON p.name = m.name
CROSS JOIN LATERAL (
  SELECT now() - make_interval(mins => g) AS ts,
         NOT (m.name = 'Checkout' AND g BETWEEN 196 AND 209) AS ok,
         GREATEST(40, (p.base + p.amp * sin(2 * pi() * g / 1440.0 + p.phase) + (random() - 0.5) * 2 * p.jitter
                       * CASE WHEN random() < 0.03 THEN 4 ELSE 1 END))::int AS rt
  FROM generate_series(0, 1439) g
) s
WHERE m.organization_id = :'org';

UPDATE monitors m SET last_checked_at = now() - interval '1 minute', last_success_at = now() - interval '1 minute',
       consecutive_failures = 0, consecutive_successes = CASE WHEN m.name = 'Checkout' THEN 195 ELSE 1440 END
WHERE m.organization_id = :'org';

-- Incidents: today's Checkout outage (matches the failed checks above) and older, shorter ones.
INSERT INTO incidents (monitor_id, started_at, resolved_at, status, cause_category, summary)
SELECT m.id, now() - i.started, now() - i.started + i.lasted, 'resolved', i.cause, i.summary
FROM monitors m JOIN (VALUES
  ('Checkout',        interval '209 minutes', interval '14 minutes', 'timeout',     'Request timed out'),
  ('Marketing site',  interval '2 days 4 hours', interval '9 minutes',  'http_status', 'Received HTTP 503'),
  ('Documentation',   interval '6 days 9 hours', interval '31 minutes', 'dns',         'DNS lookup failed'),
  ('Customer portal', interval '11 days 2 hours', interval '5 minutes', 'connect',     'Could not connect to the server')
) AS i(name, started, lasted, cause, summary) ON i.name = m.name
WHERE m.organization_id = :'org';
