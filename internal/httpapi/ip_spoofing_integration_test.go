package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

// callWith sends a protected request with extra headers, as a client that forges its address would.
func (c *client) callWith(headers map[string]string, method, path string, body any) resp {
	c.t.Helper()
	if c.csrf() == "" {
		c.do(http.MethodGet, "/api/v1/auth/me", nil, false)
	}
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, c.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", c.csrf())
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(res.Body).Decode(&m)
	return resp{res.StatusCode, m}
}

// The per-IP limits key on the address of the connection, not on headers the client sends. Echo's own default (no IPExtractor) would believe
// X-Forwarded-For and X-Real-IP, so a client could rotate them to get a fresh allowance on every request (the issue behind GitHub advisories
// GHSA-246p-cpwv-v3jq and GHSA-99jh-6h7p-pp36, which only affect an app that leaves IPExtractor unset). This pins that the API sets it.
func TestForgedForwardingHeadersDoNotBuyAFreshRateLimitAllowance(t *testing.T) {
	for name, opts := range map[string]envOpts{
		"in-memory limiter":         {},
		"shared (database) limiter": {shared: true},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnvOpts(t, 3, opts) // 3 login attempts a minute per address
			c := e.newClient(t)
			statuses := []int{}
			for i := 0; i < 6; i++ {
				r := c.callWith(map[string]string{
					"X-Forwarded-For": "203.0.113." + string(rune('1'+i)) + ", 198.51.100.7", // a different "client" each time
					"X-Real-IP":       "192.0.2." + string(rune('1'+i)),
				}, "POST", "/api/v1/auth/login", map[string]string{"email": "nobody@example.com", "password": "not-the-password"})
				statuses = append(statuses, r.Status)
			}
			limited := 0
			for _, s := range statuses {
				if s == http.StatusTooManyRequests {
					limited++
				}
			}
			if limited == 0 {
				t.Fatalf("rotating X-Forwarded-For / X-Real-IP evaded the per-address limit: statuses %v", statuses)
			}
			if statuses[0] != http.StatusUnauthorized {
				t.Fatalf("the first attempts should simply fail to log in: %v", statuses)
			}
		})
	}
}
