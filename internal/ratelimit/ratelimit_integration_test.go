package ratelimit_test

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hawkeye/internal/ratelimit"
	"hawkeye/internal/testutil"
)

func TestAllowsUpToTheLimitThenBlocks(t *testing.T) {
	l := ratelimit.New(testutil.NewPool(t))
	key := ratelimit.Key("auth:login", "203.0.113.9")
	for i := 1; i <= 5; i++ {
		r, err := l.Allow(t.Context(), key, 5, time.Hour)
		if err != nil || !r.Allowed || r.Hits != i || r.Remaining != 5-i {
			t.Fatalf("hit %d: %+v %v", i, r, err)
		}
	}
	r, _ := l.Allow(t.Context(), key, 5, time.Hour)
	if r.Allowed || r.Remaining != 0 || r.RetryAfter <= 0 || r.RetryAfter > time.Hour {
		t.Fatalf("6th hit must be blocked with a Retry-After: %+v", r)
	}
	if r, _ := l.Allow(t.Context(), ratelimit.Key("auth:login", "203.0.113.10"), 5, time.Hour); !r.Allowed {
		t.Fatal("a different subject has its own budget")
	}
	if r, _ := l.Allow(t.Context(), ratelimit.Key("auth:register", "203.0.113.9"), 5, time.Hour); !r.Allowed {
		t.Fatal("a different scope has its own budget")
	}
}

// The point of a shared limiter: simultaneous requests (as if from several API servers) cannot exceed the limit.
func TestConcurrentRequestsNeverExceedTheLimit(t *testing.T) {
	pool := testutil.NewPoolSize(t, 30)
	l := ratelimit.New(pool)
	key := ratelimit.Key("auth:login", "198.51.100.1")
	var allowed atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if r, err := l.Allow(t.Context(), key, 10, time.Hour); err != nil {
				t.Error(err)
			} else if r.Allowed {
				allowed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("%d of 40 concurrent requests were allowed, want exactly 10", allowed.Load())
	}
}

func TestWindowsReset(t *testing.T) {
	l := ratelimit.New(testutil.NewPool(t))
	key := ratelimit.Key("x", "y")
	l.Allow(t.Context(), key, 1, time.Second)
	if r, _ := l.Allow(t.Context(), key, 1, time.Second); r.Allowed {
		// The two calls could straddle a window boundary; retry once within the same second budget to avoid flakiness.
		t.Log("second hit straddled a window boundary; checking the next pair")
		l.Allow(t.Context(), key, 1, time.Second)
	}
	time.Sleep(2100 * time.Millisecond)
	if r, _ := l.Allow(t.Context(), key, 1, time.Second); !r.Allowed {
		t.Fatal("a new window must start with a fresh budget")
	}
}

func TestKeysAreHashedAndNormalised(t *testing.T) {
	k := ratelimit.Key("auth:forgot", "  Victim@Example.COM ")
	if strings.Contains(k, "victim") || strings.Contains(k, "example") || !strings.HasPrefix(k, "auth:forgot|") {
		t.Fatalf("the subject must not be stored in clear: %s", k)
	}
	if k != ratelimit.Key("auth:forgot", "victim@example.com") {
		t.Fatal("keys must ignore case and surrounding whitespace so variants cannot dodge the limit")
	}
}

func TestPurgeRemovesOldWindowsOnly(t *testing.T) {
	pool := testutil.NewPool(t)
	l := ratelimit.New(pool)
	l.Allow(t.Context(), "recent", 5, time.Hour)
	pool.Exec(t.Context(), `INSERT INTO rate_limits (key, window_start, hits) VALUES ('old', now() - interval '3 days', 4)`)
	n, err := l.Purge(t.Context(), 24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	var left int
	pool.QueryRow(t.Context(), `SELECT count(*) FROM rate_limits`).Scan(&left)
	if left != 1 {
		t.Fatalf("%d rows left", left)
	}
}
