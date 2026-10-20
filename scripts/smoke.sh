#!/usr/bin/env bash
# Post-deployment smoke test. Safe to run against production: it only reads, except in --full mode, which registers a throwaway
# account and deletes it again (that sends real emails to the address you give).
#
#   scripts/smoke.sh https://hawkeye.example.org
#   scripts/smoke.sh https://localhost:8443 --insecure                      # self-signed/internal CA (local stacks)
#   scripts/smoke.sh https://hawkeye.example.org --full smoke+$(date +%s)@example.org [--mail-api http://localhost:8026]
set -u
BASE="${1:?usage: smoke.sh BASE_URL [--insecure] [--full EMAIL [--mail-api URL]]}"; shift
BASE="${BASE%/}"
CURL=(curl -sS --max-time 15)
FULL_EMAIL="" ; MAIL_API=""
while [ $# -gt 0 ]; do
  case "$1" in
    --insecure) CURL+=(-k) ;;
    --full) FULL_EMAIL="${2:?--full needs an email address}"; shift ;;
    --mail-api) MAIL_API="${2:?}"; shift ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac; shift
done

pass=0; fail=0
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=$((fail+1)); }
check() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }
status() { "${CURL[@]}" -o /dev/null -w '%{http_code}' "$@"; }
hdr()    { "${CURL[@]}" -D - -o /dev/null "$1" | tr -d '\r'; }
jq_() { python3 -c "import sys,json; d=json.load(sys.stdin); print($1)"; }

echo "Smoke test against $BASE"
echo "== availability"
check "liveness /healthz is 200"            '[ "$(status "$BASE/healthz")" = 200 ]'
check "readiness /readyz is 200 (database reachable)" '[ "$(status "$BASE/readyz")" = 200 ]'
check "readiness reports the database check"  '"${CURL[@]}" "$BASE/readyz" | grep -q "\"database\":\"ok\""'
check "landing page is served"              '"${CURL[@]}" "$BASE/" | grep -qi "hawkeye"'
check "SPA deep link falls back to the app (/login)" '[ "$(status "$BASE/login")" = 200 ]'
check "the /monitoring campaign page is served as a page, not a 404" '[ "$(status "$BASE/monitoring")" = 200 ]'
check "privacy, terms and contact pages exist" '[ "$(status "$BASE/privacy")" = 200 ] && [ "$(status "$BASE/terms")" = 200 ] && [ "$(status "$BASE/contact")" = 200 ]'
check "the email logo is served as an image" '[ "$(status "$BASE/email/hawkeye-logo.png")" = 200 ] && "${CURL[@]}" -I "$BASE/email/hawkeye-logo.png" | grep -qi "^content-type: image/png"'
check "contact topics are public"            '[ "$("${CURL[@]}" "$BASE/api/v1/contact/categories" | jq_ "len(d[\"categories\"])")" -ge 5 ]'

echo "== API basics"
check "public plan list has three plans"    '[ "$("${CURL[@]}" "$BASE/api/v1/plans" | jq_ "len(d[\"plans\"])")" = 3 ]'
check "unauthenticated /auth/me is 401 with the error envelope" '[ "$(status "$BASE/api/v1/auth/me")" = 401 ] && "${CURL[@]}" "$BASE/api/v1/auth/me" | grep -q UNAUTHENTICATED'
check "CSRF protection is active (login without token is 403)" '[ "$(status -X POST -H "Content-Type: application/json" -d "{}" "$BASE/api/v1/auth/login")" = 403 ]'
check "unknown API route is a JSON 404"     '[ "$(status "$BASE/api/v1/nope")" = 404 ]'
check "Stripe webhook rejects an unsigned request (400, or 503 if billing is off)" 'c=$(status -X POST -d "{}" "$BASE/api/v1/webhooks/stripe"); [ "$c" = 400 ] || [ "$c" = 503 ]'

echo "== security headers"
H="$(hdr "$BASE/")"
check "Content-Security-Policy is set and forbids framing" 'echo "$H" | grep -qi "^content-security-policy:.*frame-ancestors .none"'
check "CSP allows no inline scripts (script-src has no unsafe-inline)" '! echo "$H" | grep -i "^content-security-policy:" | grep -qE "script-src[^;]*unsafe-inline"'
check "CSP allows the Google Analytics script and data endpoints" 'echo "$H" | grep -i "^content-security-policy:" | grep -q "script-src[^;]*googletagmanager.com" && echo "$H" | grep -i "^content-security-policy:" | grep -q "connect-src[^;]*google-analytics.com"'
check "Google Analytics is not hard-coded in the page (the app loads it unless the visitor opted out)" '! "${CURL[@]}" "$BASE/" | grep -qi "googletagmanager"'
check "X-Content-Type-Options: nosniff"      'echo "$H" | grep -qi "^x-content-type-options: nosniff"'
check "X-Frame-Options: DENY"                'echo "$H" | grep -qi "^x-frame-options: DENY"'
check "no Server header leaks the software"  '! echo "$H" | grep -qi "^server:"'
case "$BASE" in https://*) check "HSTS is sent over HTTPS" 'echo "$H" | grep -qi "^strict-transport-security:.*max-age"' ;; esac
AH="$(hdr "$BASE/api/v1/plans")"
check "API responses are not cacheable"      'echo "$AH" | grep -qi "^cache-control:.*no-store"'
check "API sends no CORS headers (same-origin only)" '! hdr "$BASE/api/v1/plans" | grep -qi "^access-control-allow"'

echo "== internals are not exposed"
check "/metrics does not serve Prometheus data" '! "${CURL[@]}" "$BASE/metrics" | grep -q "go_goroutines\|hawkeye_http"'
check "/api/v1/metrics is not an API route"     '[ "$(status "$BASE/api/v1/metrics")" = 404 ]'
case "$BASE" in https://*)
  HTTP_BASE="http://${BASE#https://}"
  if [ -n "${SMOKE_HTTP_BASE:-}" ]; then HTTP_BASE="$SMOKE_HTTP_BASE"; fi
  check "plain HTTP redirects to HTTPS" 'loc=$("${CURL[@]}" -o /dev/null -w "%{http_code} %{redirect_url}" "$HTTP_BASE/"); echo "$loc" | grep -Eq "^30[1278] https://"' ;;
esac

if [ -n "$FULL_EMAIL" ]; then
  echo "== full account lifecycle (throwaway account $FULL_EMAIL)"
  JAR="$(mktemp)"; trap 'rm -f "$JAR"' EXIT
  PW="Smoke-$(openssl rand -hex 8)-Aa1"
  "${CURL[@]}" -c "$JAR" -b "$JAR" -o /dev/null "$BASE/api/v1/auth/me"
  csrf() { awk '$6=="csrf_token"{print $7}' "$JAR" | tail -1; }
  post() { "${CURL[@]}" -c "$JAR" -b "$JAR" -X POST -H "Content-Type: application/json" -H "X-CSRF-Token: $(csrf)" -d "$2" -w '\n%{http_code}' "$BASE/api/v1$1"; }
  R="$(post /auth/register "{\"email\":\"$FULL_EMAIL\",\"password\":\"$PW\"}")"
  check "register returns 201"                    '[ "$(echo "$R" | tail -1)" = 201 ]'
  check "session works (/auth/me returns the user)" '"${CURL[@]}" -b "$JAR" "$BASE/api/v1/auth/me" | grep -q "$FULL_EMAIL"'
  check "the session cookie is HttpOnly and Secure" 'grep -q "#HttpOnly_.*hawkeye_session" "$JAR" && awk "/hawkeye_session/{print \$4}" "$JAR" | grep -q TRUE || case "$BASE" in http://*) true;; *) false;; esac'
  if [ -n "$MAIL_API" ]; then
    sleep 2
    check "the verification email was delivered over SMTP" '"${CURL[@]}" "$MAIL_API/api/v1/messages" | grep -q "Verify your HawkEye"'
    ID="$("${CURL[@]}" "$MAIL_API/api/v1/messages" | jq_ "d['messages'][0]['ID']")"
    TOKEN="$("${CURL[@]}" "$MAIL_API/api/v1/message/$ID" | python3 -c "import sys,json,re; print(re.search(r'token=([A-Za-z0-9_-]+)', json.load(sys.stdin)['Text']).group(1))")"
    R="$(post /auth/verify-email "{\"token\":\"$TOKEN\"}")"
    check "the emailed link verifies the address (204)" '[ "$(echo "$R" | tail -1)" = 204 ]'
  fi
  if [ -n "$MAIL_API" ]; then
    R="$(post /contact "{\"category\":\"technical\",\"name\":\"Smoke Test\",\"email\":\"$FULL_EMAIL\",\"subject\":\"Smoke test message\",\"message\":\"This is an automated smoke test of the contact form.\"}")"
    check "the contact form is accepted (202)"      '[ "$(echo "$R" | tail -1)" = 202 ]'
    sleep 2
    check "support received the message, tagged, with Reply-To and branded HTML" '"${CURL[@]}" "$MAIL_API/api/v1/messages" | grep -q "\[Something isn.t working\] Smoke test message" && ID=$("${CURL[@]}" "$MAIL_API/api/v1/search?query=subject:%22Smoke+test+message%22" | jq_ "d[\"messages\"][0][\"ID\"]") && M=$("${CURL[@]}" "$MAIL_API/api/v1/message/$ID") && echo "$M" | grep -q "support@hawkeye.test" && echo "$M" | grep -q "hawkeye-logo.png"'
  fi
  EXPECT_TOTAL=1
  if [ -n "$MAIL_API" ]; then # POST monitors need a verified address, which only this mode can complete
    EXPECT_TOTAL=2
    R="$(post /monitors '{"name":"Smoke API monitor","url":"https://example.com","method":"POST","check_interval_seconds":600,"body":"{\"ping\":true}","auth":{"type":"bearer","secret":"smoke-secret-value"},"assertions":[{"type":"json_path","path":"ok","operator":"equals","value":"true"}]}')"
    check "an API monitor with a credential and a POST method can be created (201)" '[ "$(echo "$R" | tail -1)" = 201 ]'
    check "the credential is never returned, only that it is set" '! echo "$R" | grep -q smoke-secret-value && echo "$R" | grep -q "\"secret_set\":true"'
  fi
  R="$(post /monitors '{"name":"Smoke monitor","url":"https://example.com"}')"
  check "a monitor can be created (201)"          '[ "$(echo "$R" | tail -1)" = 201 ]'
  check "the dashboard summary works"             '"${CURL[@]}" -b "$JAR" "$BASE/api/v1/dashboard/summary" | grep -q "\"total\":$EXPECT_TOTAL"'
  R="$(post /account/delete "{\"password\":\"$PW\",\"confirm_email\":\"$FULL_EMAIL\"}")"
  check "account deletion returns 204"            '[ "$(echo "$R" | tail -1)" = 204 ]'
  R="$(post /auth/login "{\"email\":\"$FULL_EMAIL\",\"password\":\"$PW\"}")"
  check "the deleted account can no longer sign in" '[ "$(echo "$R" | tail -1)" = 401 ]'
fi

echo
echo "Result: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
