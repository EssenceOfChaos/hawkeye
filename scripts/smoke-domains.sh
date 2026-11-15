#!/usr/bin/env bash
# Smoke test for status pages on customers' own domains, against the LOCAL smoke stack only (make smoke-up; it writes to that stack's database).
# It adds a published Agency status page with a verified domain, then visits the domain through Caddy as a browser would: the certificate is
# issued on the first visit (by Caddy's own CA there, CADDY_EXTRA_GLOBAL=local_certs), nothing but the status page is served on it, an unverified
# name gets no certificate, and plain http redirects. Running it again replaces what it added.
#
#   scripts/smoke-domains.sh [HTTPS_PORT] [HTTP_PORT]      # defaults 8443 and 8088, as in deploy/smoke/env.smoke
set -u
cd "$(dirname "$0")/.."
HTTPS_PORT="${1:-8443}"; HTTP_PORT="${2:-8088}"
DOMAIN=status.smoke-domain.test; OTHER=unverified.smoke-domain.test; SLUG=smoke-domain
CURL=(curl -sS --max-time 15 -k --resolve "$DOMAIN:$HTTPS_PORT:127.0.0.1" --resolve "$OTHER:$HTTPS_PORT:127.0.0.1" --resolve "$DOMAIN:$HTTP_PORT:127.0.0.1")
BASE="https://$DOMAIN:$HTTPS_PORT"

pass=0; fail=0
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
status() { "${CURL[@]}" -o /dev/null -w '%{http_code}' "$@"; }
hdr()    { "${CURL[@]}" -D - -o /dev/null "$1" | tr -d '\r'; }

psql_() {
  env -i PATH="$PATH" HOME="$HOME" docker compose --env-file deploy/smoke/env.smoke -p hawkeye-smoke -f deploy/docker-compose.prod.yml \
    -f deploy/docker-compose.local-db.yml -f deploy/smoke/docker-compose.smoke.yml exec -T db sh -c 'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -v ON_ERROR_STOP=1 -qAt'
}

echo "Custom domain smoke test against $BASE"
echo "== seed"
if psql_ >/dev/null <<SQL
DELETE FROM organizations WHERE name = 'Smoke custom domain';
WITH o AS (INSERT INTO organizations (name) VALUES ('Smoke custom domain') RETURNING id),
s AS (INSERT INTO subscriptions (organization_id, plan_id, status) SELECT o.id, p.id, 'active' FROM o, plans p WHERE p.code = 'agency'),
m AS (INSERT INTO monitors (organization_id, name, url, check_interval_seconds, current_status, enabled)
      SELECT id, 'Website', 'https://example.com', 300, 'up', false FROM o RETURNING id, organization_id),
pg AS (INSERT INTO status_pages (organization_id, slug, title, published) SELECT organization_id, '$SLUG', 'Smoke domain status', true FROM m RETURNING id, organization_id),
pm AS (INSERT INTO status_page_monitors (page_id, monitor_id, position) SELECT pg.id, m.id, 0 FROM pg, m)
INSERT INTO status_page_domains (page_id, organization_id, hostname, verify_token, state, verified_at, next_check_at)
SELECT id, organization_id, '$DOMAIN', 'abcdefghijklmnopqrstuvwxyz234567', 'active', now(), now() + interval '1 day' FROM pg;
SQL
then ok "a published Agency page with a verified domain was added"; else bad "seeding the database failed"; fi

echo "== the domain"
check "the first visit gets a certificate, from the smoke stack's own CA" \
  '"${CURL[@]}" -v -o /dev/null "$BASE/" 2>&1 | grep -q "issuer: CN=Caddy Local Authority"'
check "the status page is served at /, from its own entry (status.html)" \
  '[ "$(status "$BASE/")" = 200 ] && "${CURL[@]}" "$BASE/" | grep -q "/assets/status-"'
check "its data is found from the host name" \
  '"${CURL[@]}" "$BASE/api/v1/public/domain-status" | grep -q "\"title\":\"Smoke domain status\""'
check "its script is served" \
  '[ "$(status "$BASE$("${CURL[@]}" "$BASE/" | grep -o "/assets/status-[^\"]*\.js" | head -1)")" = 200 ]'
check "CSP allows only our own origin (no analytics or other third party)" \
  'hdr "$BASE/" | grep -i "^content-security-policy:" | grep -q "script-src '"'"'self'"'"';" && ! hdr "$BASE/" | grep -i "^content-security-policy:" | grep -qi "google\|openai"'
check "HSTS is sent without includeSubDomains (the customer's other names are not ours)" \
  'hdr "$BASE/" | grep -i "^strict-transport-security:" | grep -qv -i includesubdomains'
check "framing is refused" 'hdr "$BASE/" | grep -qi "^x-frame-options: DENY"'

echo "== nothing else is served on it"
for p in /login /dashboard /status/$SLUG /index.html /api/v1/auth/me /api/v1/plans /api/v1/public/status/$SLUG "/internal/tls/allow?domain=$DOMAIN" /mcp /healthz; do
  check "$p is a 404" '[ "$(status "$BASE$p")" = 404 ]'
done

echo "== other names and plain http"
check "an unverified name gets no certificate (the handshake fails)" '[ "$(status "https://$OTHER:$HTTPS_PORT/" 2>/dev/null)" = 000 ]'
check "plain http on the domain redirects to https" \
  '"${CURL[@]}" -o /dev/null -w "%{http_code} %{redirect_url}" "http://$DOMAIN:$HTTP_PORT/" | grep -q "^301 https://$DOMAIN/"'
check "the page's HawkEye address names the domain" \
  '"${CURL[@]}" "https://localhost:$HTTPS_PORT/api/v1/public/status/$SLUG" | grep -q "\"custom_domain_url\":\"https://$DOMAIN\""'

echo
echo "Result: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
