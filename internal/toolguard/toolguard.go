// Package toolguard holds the abuse limits shared by the free tools that connect out to a host a visitor names (the SSL checker and the
// website status checker). Each tool wraps its work in Guard.Do, which gives it, in this order:
//   - a short cache, so the same host asked about repeatedly costs one connection (and does not spend its budget);
//   - a cap on checks running at once, so a burst cannot tie up the server (the visitor is told it is busy, and nothing is queued);
//   - a budget per host (a few fresh checks per window, whoever asks), so the tool cannot be pointed repeatedly at one server;
//   - a deadline for the work itself.
//
// The limits are per process. Per-visitor limits are applied by the API in front of the tool.
package toolguard

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrBusy means every slot is in use (or the table of recently checked hosts is full). Nothing was attempted.
var ErrBusy = errors.New("too many checks in progress")

// TargetLimitedError means this host was checked too often recently. RetryAfter is when it may be checked again.
type TargetLimitedError struct{ RetryAfter time.Duration }

func (e *TargetLimitedError) Error() string { return "host checked too often" }

// Options sets the limits. A zero field takes the default shown.
type Options struct {
	Timeout     time.Duration // 8 s: the deadline given to the work
	Concurrency int           // 8: checks running at once
	CacheTTL    time.Duration // 60 s: how long a successful result is reused
	TargetMax   int           // 5: fresh checks of one host per TargetSpan
	TargetSpan  time.Duration // 10 min
}

const (
	maxCacheEntries  = 2000
	maxTargetEntries = 20000
)

type entry[T any] struct {
	value   T
	expires time.Time
}

// Guard applies the limits to work that returns a T. The key is the host name, already normalised by the caller.
type Guard[T any] struct {
	timeout    time.Duration
	cacheTTL   time.Duration
	targetMax  int
	targetSpan time.Duration
	sem        chan struct{}
	now        func() time.Time

	mu     sync.Mutex
	cache  map[string]entry[T]
	recent map[string][]time.Time
}

func New[T any](o Options) *Guard[T] {
	if o.Timeout <= 0 {
		o.Timeout = 8 * time.Second
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 8
	}
	if o.CacheTTL <= 0 {
		o.CacheTTL = 60 * time.Second
	}
	if o.TargetMax <= 0 {
		o.TargetMax = 5
	}
	if o.TargetSpan <= 0 {
		o.TargetSpan = 10 * time.Minute
	}
	return &Guard[T]{
		timeout: o.Timeout, cacheTTL: o.CacheTTL, targetMax: o.TargetMax, targetSpan: o.TargetSpan,
		sem: make(chan struct{}, o.Concurrency), now: time.Now,
		cache: map[string]entry[T]{}, recent: map[string][]time.Time{},
	}
}

// Do returns the cached result for key if there is one; otherwise it takes a slot, spends one unit of the key's budget and runs fn with the
// deadline. Only a successful result is cached: a failure is not remembered, but it did spend budget, so a host that keeps failing is
// not hammered either.
func (g *Guard[T]) Do(ctx context.Context, key string, fn func(context.Context) (T, error)) (T, error) {
	var zero T
	if v, ok := g.cached(key); ok {
		return v, nil
	}
	// Capacity is checked first so that a busy moment does not also spend the host's budget.
	select {
	case g.sem <- struct{}{}:
		defer func() { <-g.sem }()
	default:
		return zero, ErrBusy
	}
	if err := g.allowTarget(key); err != nil {
		return zero, err
	}
	ctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	v, err := fn(ctx)
	if err != nil {
		return zero, err
	}
	g.store(key, v)
	return v, nil
}

func (g *Guard[T]) cached(key string) (T, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	e, ok := g.cache[key]
	if !ok || !g.now().Before(e.expires) {
		delete(g.cache, key)
		var zero T
		return zero, false
	}
	return e.value, true
}

func (g *Guard[T]) store(key string, v T) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.cache) >= maxCacheEntries {
		for k, e := range g.cache {
			if !g.now().Before(e.expires) {
				delete(g.cache, k)
			}
		}
		if len(g.cache) >= maxCacheEntries {
			return // full of fresh entries: skip caching rather than grow
		}
	}
	g.cache[key] = entry[T]{value: v, expires: g.now().Add(g.cacheTTL)}
}

// allowTarget counts one fresh check of key and refuses it when the key has had targetMax of them within targetSpan.
func (g *Guard[T]) allowTarget(key string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if len(g.recent) >= maxTargetEntries {
		for k, ts := range g.recent {
			if len(ts) == 0 || now.Sub(ts[len(ts)-1]) >= g.targetSpan {
				delete(g.recent, k)
			}
		}
		if len(g.recent) >= maxTargetEntries {
			return ErrBusy
		}
	}
	var kept []time.Time
	for _, t := range g.recent[key] {
		if now.Sub(t) < g.targetSpan {
			kept = append(kept, t)
		}
	}
	if len(kept) >= g.targetMax {
		g.recent[key] = kept
		return &TargetLimitedError{RetryAfter: g.targetSpan - now.Sub(kept[0])}
	}
	g.recent[key] = append(kept, now)
	return nil
}
