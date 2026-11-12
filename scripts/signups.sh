#!/usr/bin/env bash
# Where new accounts came from (internal/attribution): sign-ups by source over the last DAYS days, and the most recent ones.
#
#   scripts/signups.sh [DAYS]      default 30
#
# Run on the server as the deploy user; it reads the database with docker exec. "(not recorded)" is a sign-up whose browser had analytics off,
# or one from before attribution was recorded. "verified" counts confirmed email addresses, "paid" accounts whose own organization is on a paid plan.
set -euo pipefail
days="${1:-30}"
case "$days" in ''|*[!0-9]*) echo "usage: $0 [DAYS]" >&2; exit 2 ;; esac
DB="${DB_CONTAINER:-hawkeye-db-1}"

docker exec -i "$DB" psql -U hawkeye -d monitoring -v ON_ERROR_STOP=1 -v days="$days" -X -q <<'SQL'
\echo 'Sign-ups by source, last' :days 'days'
WITH s AS (
  SELECT u.email_verified_at, a.source, a.medium, a.campaign, a.referrer_host, a.landing_path,
         EXISTS (SELECT 1 FROM organization_members m JOIN subscriptions sub ON sub.organization_id = m.organization_id
                 JOIN plans p ON p.id = sub.plan_id WHERE m.user_id = u.id AND m.role = 'owner' AND p.code <> 'free') AS paid
  FROM users u LEFT JOIN signup_attributions a ON a.user_id = u.id
  WHERE u.created_at > now() - make_interval(days => :days)
)
SELECT COALESCE(source, CASE WHEN referrer_host IS NOT NULL THEN 'linked from ' || referrer_host
                             WHEN landing_path IS NOT NULL THEN '(direct)' ELSE '(not recorded)' END) AS source,
       COALESCE(medium, '') AS medium, COALESCE(campaign, '') AS campaign,
       count(*) AS signups, count(email_verified_at) AS verified, count(*) FILTER (WHERE paid) AS paid
FROM s GROUP BY 1, 2, 3 ORDER BY signups DESC, 1;

\echo 'Most recent sign-ups'
SELECT to_char(u.created_at, 'YYYY-MM-DD HH24:MI') AS signed_up, COALESCE(a.source, '') AS source, COALESCE(a.medium, '') AS medium,
       COALESCE(a.campaign, '') AS campaign, COALESCE(a.click_id, '') AS ad_click, COALESCE(a.referrer_host, '') AS linked_from,
       COALESCE(a.landing_path, '') AS landed_on, u.email_verified_at IS NOT NULL AS verified
FROM users u LEFT JOIN signup_attributions a ON a.user_id = u.id
WHERE u.created_at > now() - make_interval(days => :days)
ORDER BY u.created_at DESC LIMIT 20;
SQL
