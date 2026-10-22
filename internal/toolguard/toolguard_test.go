package toolguard

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var t0 = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func newGuard(o Options) (*Guard[int], *time.Time) {
	g := New[int](o)
	now := t0
	g.now = func() time.Time { return now }
	return g, &now
}

func TestResultsAreCachedAndDoNotSpendTheKeyBudget(t *testing.T) {
	g, _ := newGuard(Options{})
	var runs atomic.Int32
	for i := 0; i < 30; i++ {
		v, err := g.Do(context.Background(), "example.com", func(context.Context) (int, error) { runs.Add(1); return 7, nil })
		if err != nil || v != 7 {
			t.Fatalf("request %d: %v %v", i, v, err)
		}
	}
	if runs.Load() != 1 {
		t.Errorf("work ran %d times for 30 requests", runs.Load())
	}
}

func TestCacheExpires(t *testing.T) {
	g, now := newGuard(Options{CacheTTL: time.Minute})
	var runs atomic.Int32
	work := func(context.Context) (int, error) { return int(runs.Add(1)), nil }
	a, _ := g.Do(context.Background(), "k", work)
	*now = now.Add(61 * time.Second)
	b, _ := g.Do(context.Background(), "k", work)
	if a != 1 || b != 2 {
		t.Errorf("results %d, %d; want a fresh one after the TTL", a, b)
	}
}

func TestFailuresAreNotCachedButSpendBudget(t *testing.T) {
	g, now := newGuard(Options{TargetMax: 3, TargetSpan: 10 * time.Minute})
	var runs atomic.Int32
	boom := errors.New("boom")
	fail := func(context.Context) (int, error) { runs.Add(1); return 0, boom }
	for i := 0; i < 3; i++ {
		if _, err := g.Do(context.Background(), "k", fail); !errors.Is(err, boom) {
			t.Fatalf("check %d: %v", i, err)
		}
	}
	var tl *TargetLimitedError
	_, err := g.Do(context.Background(), "k", fail)
	if !errors.As(err, &tl) || tl.RetryAfter <= 0 || tl.RetryAfter > 10*time.Minute {
		t.Fatalf("err = %v", err)
	}
	if runs.Load() != 3 {
		t.Errorf("a limited request still ran the work: %d", runs.Load())
	}
	if _, err := g.Do(context.Background(), "other", fail); !errors.Is(err, boom) {
		t.Errorf("another key must not be affected: %v", err)
	}
	*now = now.Add(11 * time.Minute)
	if _, err := g.Do(context.Background(), "k", fail); !errors.Is(err, boom) {
		t.Errorf("the budget must come back after the window: %v", err)
	}
}

func TestConcurrencyIsCappedAndABusyRefusalSpendsNoBudget(t *testing.T) {
	g, _ := newGuard(Options{Concurrency: 3})
	release := make(chan struct{})
	var running atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = g.Do(context.Background(), string(rune('a'+i)), func(context.Context) (int, error) { running.Add(1); <-release; return 0, nil })
		}(i)
	}
	for running.Load() < 3 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := g.Do(context.Background(), "late", func(context.Context) (int, error) { return 1, nil }); !errors.Is(err, ErrBusy) {
		t.Errorf("err = %v, want ErrBusy", err)
	}
	g.mu.Lock()
	_, spent := g.recent["late"]
	g.mu.Unlock()
	if spent {
		t.Error("a busy refusal spent the key's budget")
	}
	close(release)
	wg.Wait()
}

func TestWorkGetsADeadline(t *testing.T) {
	g, _ := newGuard(Options{Timeout: 50 * time.Millisecond})
	start := time.Now()
	_, err := g.Do(context.Background(), "k", func(ctx context.Context) (int, error) { <-ctx.Done(); return 0, ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
}

func TestTablesStayBounded(t *testing.T) {
	g, _ := newGuard(Options{TargetMax: 1})
	for i := 0; i < maxTargetEntries+10; i++ {
		_, _ = g.Do(context.Background(), "host"+itoa(i), func(context.Context) (int, error) { return 0, errors.New("x") })
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.recent) > maxTargetEntries {
		t.Errorf("%d tracked hosts", len(g.recent))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}
