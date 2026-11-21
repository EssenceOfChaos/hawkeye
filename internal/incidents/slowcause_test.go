package incidents

import (
	"strings"
	"testing"

	"hawkeye/internal/checks"
)

func TestSlowCause(t *testing.T) {
	usual := &Baseline{DNSMs: 10, ConnectMs: 20, TLSMs: 30, WaitMs: 180, Checks: 500}
	cases := map[string]struct {
		slow  *checks.Timings
		usual *Baseline
		want  string
	}{
		"server wait": {&checks.Timings{DNSMs: 12, ConnectMs: 22, TLSMs: 31, WaitMs: 2100}, usual,
			"Most of the extra time is the server's wait for a first byte: 2,100 ms, against 180 ms usually."},
		"DNS": {&checks.Timings{DNSMs: 1500, ConnectMs: 20, TLSMs: 30, WaitMs: 200}, usual, "Most of the extra time is the DNS lookup"},
		// 400, 300 and 300 ms more in connect, TLS and wait: none is half of the extra 1,000 ms.
		"split evenly":              {&checks.Timings{DNSMs: 10, ConnectMs: 420, TLSMs: 330, WaitMs: 480}, usual, "spread across the connection"},
		"no breakdown":              {nil, usual, ""},
		"no history":                {&checks.Timings{WaitMs: 5000}, nil, ""},
		"too little history":        {&checks.Timings{WaitMs: 5000}, &Baseline{WaitMs: 100, Checks: baselineChecks - 1}, ""},
		"nothing slower than usual": {&checks.Timings{DNSMs: 5, ConnectMs: 10, TLSMs: 20, WaitMs: 100}, usual, ""},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := SlowCause(c.slow, c.usual)
			if c.want == "" && got != "" || c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("SlowCause = %q, want %q", got, c.want)
			}
		})
	}
}
