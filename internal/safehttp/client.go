package safehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"syscall"
	"time"
)

var (
	// ErrBlocked means the destination (address, port or redirect target) is not permitted.
	ErrBlocked = errors.New("destination not permitted")
	// ErrTooManyRedirects means the redirect limit was exceeded.
	ErrTooManyRedirects = errors.New("too many redirects")
)

// Resolver looks up host addresses. *net.Resolver satisfies it.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// DialFunc opens a connection to an already-validated "ip:port" address.
type DialFunc func(ctx context.Context, network, address string) (net.Conn, error)

// Config customises the client. The zero value is the strict production policy; the hooks exist so
// tests can use fake DNS and local servers while keeping every other control active.
type Config struct {
	Resolver     Resolver
	Dial         DialFunc
	AllowIP      func(netip.Addr) bool
	AllowPort    func(int) bool
	MaxRedirects int
	DialTimeout  time.Duration
}

func (c Config) withDefaults() Config {
	if c.Resolver == nil {
		c.Resolver = net.DefaultResolver
	}
	if c.AllowIP == nil {
		c.AllowIP = IsPublicIP
	}
	if c.AllowPort == nil {
		c.AllowPort = IsAllowedPort
	}
	if c.MaxRedirects == 0 {
		c.MaxRedirects = 5
	}
	if c.DialTimeout == 0 {
		c.DialTimeout = 5 * time.Second
	}
	if c.Dial == nil {
		allowIP, allowPort := c.AllowIP, c.AllowPort
		d := &net.Dialer{
			Timeout: c.DialTimeout,
			// Control runs on the exact address about to be connected, after resolution. It is a second,
			// independent check: even if the logic in dialContext were bypassed, a connection to a
			// prohibited address cannot be established.
			Control: func(_, address string, _ syscall.RawConn) error {
				host, port, err := net.SplitHostPort(address)
				if err != nil {
					return ErrBlocked
				}
				ip, err := netip.ParseAddr(host)
				p, perr := strconv.Atoi(port)
				if err != nil || perr != nil || !allowIP(ip) || !allowPort(p) {
					return ErrBlocked
				}
				return nil
			},
		}
		c.Dial = d.DialContext
	}
	return c
}

// New returns an HTTP client with a hard overall timeout. Safety properties:
//   - environment proxies are ignored (a proxy would dial on our behalf and bypass the checks);
//   - every hostname is resolved by us, ALL answers must be public, and we dial the validated IP
//     literal, so a second DNS answer (DNS rebinding) is never used;
//   - the same dialer serves redirects, so each redirect target is revalidated;
//   - only ports 80/443, only http/https, no credentials in redirect URLs, bounded redirects.
func New(cfg Config, timeout time.Duration) *http.Client {
	cfg = cfg.withDefaults()
	t := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialContext(ctx, cfg, network, addr)
		},
		DisableKeepAlives:      true,
		DisableCompression:     true,
		ForceAttemptHTTP2:      false,
		TLSHandshakeTimeout:    cfg.DialTimeout,
		ResponseHeaderTimeout:  timeout,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &http.Client{
		Transport: t,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > cfg.MaxRedirects {
				return ErrTooManyRedirects
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("%w: redirect scheme %q", ErrBlocked, req.URL.Scheme)
			}
			if req.URL.User != nil {
				return fmt.Errorf("%w: redirect with credentials", ErrBlocked)
			}
			return nil // host and port are validated when the transport dials
		},
	}
}

func dialContext(ctx context.Context, cfg Config, network, addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: bad address", ErrBlocked)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || !cfg.AllowPort(port) {
		return nil, fmt.Errorf("%w: port %s", ErrBlocked, portStr)
	}

	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip}
	} else {
		ips, err = cfg.Resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err // *net.DNSError; classified as a DNS failure by the caller
		}
		if len(ips) == 0 {
			return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
		}
	}
	// Strict: one prohibited answer blocks the host, so mixed public/private records cannot be used
	// to reach an internal address by luck of round-robin ordering.
	for _, ip := range ips {
		if !cfg.AllowIP(ip) {
			return nil, ErrBlocked
		}
	}

	var lastErr error
	for _, ip := range ips {
		conn, err := cfg.Dial(ctx, network, net.JoinHostPort(ip.String(), portStr))
		if err == nil {
			return conn, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, lastErr
}
