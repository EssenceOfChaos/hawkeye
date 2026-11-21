package safehttp

import (
	"context"
	"sync"
	"time"
)

// Phases accumulates where a request's time went. The client resolves host names and dials connections itself (so that only checked
// addresses are ever used), which means Go's httptrace never sees those two steps: dialContext records them here instead. The caller adds
// the TLS handshake and the server's wait from httptrace. Every value is summed over all the connections a request makes (one per redirect,
// since keep-alives are off). Safe for concurrent use.
type Phases struct {
	mu                       sync.Mutex
	dns, connect, tls, wait  time.Duration
	connections              int
	tlsStart, wroteRequestAt time.Time
}

type phasesKey struct{}

// WithPhases returns a context whose requests record their phases into p. Without it, nothing is measured and nothing changes.
func WithPhases(ctx context.Context, p *Phases) context.Context {
	return context.WithValue(ctx, phasesKey{}, p)
}

func phasesFrom(ctx context.Context) *Phases {
	p, _ := ctx.Value(phasesKey{}).(*Phases)
	return p
}

func (p *Phases) add(field *time.Duration, d time.Duration) {
	p.mu.Lock()
	*field += d
	p.mu.Unlock()
}

// TLSStart and TLSDone bracket a TLS handshake (httptrace's TLSHandshakeStart and TLSHandshakeDone).
func (p *Phases) TLSStart() {
	p.mu.Lock()
	p.tlsStart = time.Now()
	p.mu.Unlock()
}

func (p *Phases) TLSDone() {
	p.mu.Lock()
	if !p.tlsStart.IsZero() {
		p.tls += time.Since(p.tlsStart)
		p.tlsStart = time.Time{}
	}
	p.mu.Unlock()
}

// WroteRequest and GotFirstByte bracket the server's wait (httptrace's WroteRequest and GotFirstResponseByte).
func (p *Phases) WroteRequest() {
	p.mu.Lock()
	p.wroteRequestAt = time.Now()
	p.mu.Unlock()
}

func (p *Phases) GotFirstByte() {
	p.mu.Lock()
	if !p.wroteRequestAt.IsZero() {
		p.wait += time.Since(p.wroteRequestAt)
		p.wroteRequestAt = time.Time{}
	}
	p.mu.Unlock()
}

// PhaseTimes is a snapshot of Phases.
type PhaseTimes struct {
	DNS, Connect, TLS, Wait time.Duration
	// Connections is how many connections were opened: 1 plus the number of redirects followed.
	Connections int
}

// Snapshot returns the times so far. A phase still in progress (a handshake or a wait cut short by a timeout) counts up to now, so a
// request that failed partway still shows where it was stuck.
func (p *Phases) Snapshot() PhaseTimes {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := PhaseTimes{DNS: p.dns, Connect: p.connect, TLS: p.tls, Wait: p.wait, Connections: p.connections}
	if !p.tlsStart.IsZero() {
		t.TLS += time.Since(p.tlsStart)
	}
	if !p.wroteRequestAt.IsZero() {
		t.Wait += time.Since(p.wroteRequestAt)
	}
	return t
}
