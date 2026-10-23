package sslcheck

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hawkeye/internal/safehttp"
	"hawkeye/internal/toolguard"
)

var fixedNow = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

type authority struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newAuthority(t *testing.T) authority {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root CA", Organization: []string{"Test Trust"}},
		NotBefore: fixedNow.Add(-5 * 365 * 24 * time.Hour), NotAfter: fixedNow.Add(5 * 365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return authority{cert, key, pool}
}

type leafSpec struct {
	names     []string
	notBefore time.Time
	notAfter  time.Time
	sigAlg    x509.SignatureAlgorithm
	rsaBits   int
	selfSign  bool
}

// issue returns a tls.Certificate for the spec, signed by ca (or by itself when selfSign).
func (ca authority) issue(t *testing.T, spec leafSpec) tls.Certificate {
	t.Helper()
	var priv any
	var pub any
	if spec.rsaBits > 0 {
		k, err := rsa.GenerateKey(rand.Reader, spec.rsaBits)
		if err != nil {
			t.Fatal(err)
		}
		priv, pub = k, &k.PublicKey
	} else {
		k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		priv, pub = k, &k.PublicKey
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: spec.names[0], Organization: []string{"Example Org"}},
		DNSNames: spec.names, NotBefore: spec.notBefore, NotAfter: spec.notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: spec.sigAlg,
	}
	parent, signer := ca.cert, any(ca.key)
	if spec.selfSign {
		parent, signer = tpl, priv
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, pub, signer)
	if err != nil {
		t.Fatal(err)
	}
	chain := [][]byte{der}
	if !spec.selfSign {
		chain = append(chain, ca.cert.Raw)
	}
	return tls.Certificate{Certificate: chain, PrivateKey: priv}
}

func good(names ...string) leafSpec {
	return leafSpec{names: names, notBefore: fixedNow.Add(-30 * 24 * time.Hour), notAfter: fixedNow.Add(60 * 24 * time.Hour)}
}

// serve starts a TLS server presenting cert and returns a Service whose dialer connects to it. Resolution, the address policy and the port
// policy are the production ones: only the final connection is redirected to the local server.
func serve(t *testing.T, ca authority, cert tls.Certificate, maxVersion uint16) *Service {
	t.Helper()
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}}
	if maxVersion != 0 {
		cfg.MaxVersion = maxVersion
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _ = c.(*tls.Conn).Handshake(); c.Close() }()
		}
	}()
	return newTestService(t, ca, safehttp.Config{
		Resolver: fakeResolver{ips: []string{"93.184.216.34"}},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, ln.Addr().String())
		},
	})
}

type fakeResolver struct {
	ips []string
	err error
}

func (f fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []netip.Addr
	for _, s := range f.ips {
		out = append(out, netip.MustParseAddr(s))
	}
	return out, nil
}

func newTestService(t *testing.T, ca authority, cfg safehttp.Config) *Service {
	t.Helper()
	cfg.RootCAs = ca.pool
	s := New(cfg)
	s.now = func() time.Time { return fixedNow }
	s.guard = toolguard.New[Report](toolguard.Options{Timeout: 3 * time.Second})
	return s
}

func codes(r Report) map[string]string {
	m := map[string]string{}
	for _, p := range r.Problems {
		m[p.Code] = p.Severity
	}
	return m
}

func TestValidCertificate(t *testing.T) {
	ca := newAuthority(t)
	s := serve(t, ca, ca.issue(t, good("example.com", "www.example.com")), 0)
	r, err := s.Inspect(context.Background(), "https://Example.com/some/path")
	if err != nil {
		t.Fatal(err)
	}
	if r.Host != "example.com" || r.Status != "valid" || !r.Trusted || !r.HostnameMatch || len(r.Problems) != 0 {
		t.Fatalf("%+v", r)
	}
	if r.DaysRemaining != 60 {
		t.Errorf("days remaining = %d", r.DaysRemaining)
	}
	c := r.Certificate
	if c.SubjectName != "example.com" || c.SubjectOrg != "Example Org" || c.IssuerName != "Test Root CA" || c.KeyAlgorithm != "ECDSA" || c.KeyBits != 256 ||
		len(c.DNSNames) != 2 || len(c.SHA256Fingerprint) != 64 || c.SerialNumber == "" {
		t.Errorf("certificate = %+v", c)
	}
	if len(r.Chain) != 2 || r.Chain[1].SubjectName != "Test Root CA" {
		t.Errorf("chain = %+v", r.Chain)
	}
	if !strings.HasPrefix(r.TLSVersion, "TLS 1.") || r.CipherSuite == "" {
		t.Errorf("connection = %q %q", r.TLSVersion, r.CipherSuite)
	}
}

func TestProblems(t *testing.T) {
	ca := newAuthority(t)
	other := newAuthority(t)
	cases := []struct {
		name   string
		cert   tls.Certificate
		status string
		want   map[string]string // problem code -> severity, exactly
	}{
		{"expired", ca.issue(t, leafSpec{names: []string{"example.com"}, notBefore: fixedNow.Add(-400 * 24 * time.Hour), notAfter: fixedNow.Add(-10 * 24 * time.Hour)}),
			"expired", map[string]string{"expired": "error"}},
		{"not yet valid", ca.issue(t, leafSpec{names: []string{"example.com"}, notBefore: fixedNow.Add(24 * time.Hour), notAfter: fixedNow.Add(90 * 24 * time.Hour)}),
			"invalid", map[string]string{"not_yet_valid": "error"}},
		{"expiring", ca.issue(t, leafSpec{names: []string{"example.com"}, notBefore: fixedNow.Add(-80 * 24 * time.Hour), notAfter: fixedNow.Add(9 * 24 * time.Hour)}),
			"expiring", map[string]string{"expiring": "warning"}},
		{"wrong host", ca.issue(t, good("other.example.org")), "invalid", map[string]string{"hostname_mismatch": "error"}},
		{"self-signed", ca.issue(t, leafSpec{names: []string{"example.com"}, notBefore: good("x").notBefore, notAfter: good("x").notAfter, selfSign: true}),
			"invalid", map[string]string{"self_signed": "error"}},
		{"unknown authority", other.issue(t, good("example.com")), "invalid", map[string]string{"untrusted_chain": "error"}},
		{"expired and wrong host", ca.issue(t, leafSpec{names: []string{"other.example.org"}, notBefore: fixedNow.Add(-400 * 24 * time.Hour), notAfter: fixedNow.Add(-10 * 24 * time.Hour)}),
			"expired", map[string]string{"expired": "error", "hostname_mismatch": "error"}},
		{"weak rsa key", ca.issue(t, leafSpec{names: []string{"example.com"}, notBefore: good("x").notBefore, notAfter: good("x").notAfter, rsaBits: 1024}),
			"valid", map[string]string{"weak_key": "warning"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := serve(t, ca, tc.cert, 0)
			r, err := s.Inspect(context.Background(), "example.com")
			if err != nil {
				t.Fatal(err)
			}
			if r.Status != tc.status {
				t.Errorf("status = %q, want %q (%v)", r.Status, tc.status, r.Problems)
			}
			got := codes(r)
			if len(got) != len(tc.want) {
				t.Fatalf("problems = %v, want %v", got, tc.want)
			}
			for code, sev := range tc.want {
				if got[code] != sev {
					t.Errorf("problems = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestErrorsComeFirstAndExpiredLeafIsNotAlsoUntrusted(t *testing.T) {
	ca := newAuthority(t)
	s := serve(t, ca, ca.issue(t, leafSpec{names: []string{"example.com"}, notBefore: fixedNow.Add(-400 * 24 * time.Hour), notAfter: fixedNow.Add(-10 * 24 * time.Hour), rsaBits: 1024}), 0)
	r, err := s.Inspect(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Trusted {
		t.Error("an expired leaf from a trusted authority is expired, not untrusted")
	}
	if len(r.Problems) < 2 || r.Problems[0].Severity != SeverityError || r.Problems[len(r.Problems)-1].Severity != SeverityWarning {
		t.Errorf("problems must list errors before warnings: %+v", r.Problems)
	}
}

func TestAServerThatOnlySpeaksOldTLSCannotBeChecked(t *testing.T) {
	ca := newAuthority(t)
	s := serve(t, ca, ca.issue(t, good("example.com")), tls.VersionTLS11)
	if _, err := s.Inspect(context.Background(), "example.com"); !errors.Is(err, ErrNoTLS) {
		t.Errorf("err = %v, want ErrNoTLS: TLS 1.2 is the oldest version this checker negotiates", err)
	}
}

func TestAVerifiedConnectionReportsItsProtocolAndAnUnverifiedOneDoesNot(t *testing.T) {
	ca := newAuthority(t)
	r, err := serve(t, ca, ca.issue(t, good("example.com")), 0).Inspect(context.Background(), "example.com")
	if err != nil || r.TLSVersion == "" || r.CipherSuite == "" {
		t.Fatalf("%v %+v", err, r)
	}
	r, err = serve(t, ca, ca.issue(t, good("other.example.org")), 0).Inspect(context.Background(), "example.com")
	if err != nil || r.TLSVersion != "" || r.CipherSuite != "" || r.Status != "invalid" {
		t.Errorf("a certificate that does not verify fails the handshake, so there is no protocol to report: %v %+v", err, r)
	}
}

// The checker looks at certificates that are wrong; it must never get there by turning verification off or by accepting old protocols.
func TestTheHandshakeKeepsCertificateVerificationAndModernTLSOn(t *testing.T) {
	src, err := os.ReadFile("sslcheck.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"InsecureSkipVerify", "VersionTLS10", "VersionTLS11", "VersionSSL30"} {
		if strings.Contains(string(src), banned) {
			t.Errorf("sslcheck.go mentions %s", banned)
		}
	}
	if !strings.Contains(string(src), "MinVersion: tls.VersionTLS12") {
		t.Error("the handshake must set MinVersion to TLS 1.2")
	}
}

func TestManyNamesAreTruncated(t *testing.T) {
	ca := newAuthority(t)
	var names []string
	for i := 0; i < 60; i++ {
		names = append(names, "h"+strings.Repeat("x", i%5)+string(rune('a'+i%26))+".example.com")
	}
	names[0] = "example.com"
	s := serve(t, ca, ca.issue(t, good(names...)), 0)
	r, err := s.Inspect(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Certificate.DNSNames) != maxDNSNames || !r.Certificate.DNSNamesTruncated {
		t.Errorf("%d names, truncated=%v", len(r.Certificate.DNSNames), r.Certificate.DNSNamesTruncated)
	}
}

// --- failure modes and the safety rules ---

func TestRefusesHostsThatResolveToNonPublicAddresses(t *testing.T) {
	ca := newAuthority(t)
	var dialed atomic.Int32
	for _, ips := range [][]string{{"127.0.0.1"}, {"10.0.0.5"}, {"169.254.169.254"}, {"192.168.1.1"}, {"::1"}, {"fd00:ec2::254"}, {"93.184.216.34", "10.0.0.5"}} {
		s := newTestService(t, ca, safehttp.Config{
			Resolver: fakeResolver{ips: ips},
			Dial: func(context.Context, string, string) (net.Conn, error) {
				dialed.Add(1)
				return nil, errors.New("must not dial")
			},
		})
		if _, err := s.Inspect(context.Background(), "looks-public.example.com"); !errors.Is(err, ErrBlocked) {
			t.Errorf("%v: err = %v, want ErrBlocked", ips, err)
		}
	}
	if dialed.Load() != 0 {
		t.Errorf("a connection was attempted to a blocked address %d times", dialed.Load())
	}
}

func TestOnlyPort443IsDialed(t *testing.T) {
	ca := newAuthority(t)
	var addr string
	s := newTestService(t, ca, safehttp.Config{
		Resolver: fakeResolver{ips: []string{"93.184.216.34"}},
		Dial:     func(_ context.Context, _, a string) (net.Conn, error) { addr = a; return nil, errors.New("stop") },
	})
	_, _ = s.Inspect(context.Background(), "https://example.com:443/")
	if addr != "93.184.216.34:443" {
		t.Errorf("dialed %q", addr)
	}
	if _, err := s.Inspect(context.Background(), "example.com:8443"); err == nil || addr != "93.184.216.34:443" {
		t.Errorf("a non-443 port must be refused before any dial: %v %q", err, addr)
	}
}

func TestErrorClassification(t *testing.T) {
	ca := newAuthority(t)
	cases := []struct {
		name string
		cfg  safehttp.Config
		want error
	}{
		{"no such host", safehttp.Config{Resolver: fakeResolver{err: &net.DNSError{Err: "no such host", Name: "x", IsNotFound: true}}}, ErrNotFound},
		{"resolver failure", safehttp.Config{Resolver: fakeResolver{err: &net.DNSError{Err: "server misbehaving", Name: "x"}}}, ErrNotFound},
		{"refused", safehttp.Config{Resolver: fakeResolver{ips: []string{"93.184.216.34"}}, Dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("connection refused") }}, ErrConnect},
		{"dial hangs", safehttp.Config{Resolver: fakeResolver{ips: []string{"93.184.216.34"}}, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }}, ErrTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService(t, ca, tc.cfg)
			s.guard = toolguard.New[Report](toolguard.Options{Timeout: 200 * time.Millisecond})
			if _, err := s.Inspect(context.Background(), "example.com"); !errors.Is(err, tc.want) {
				t.Errorf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestServerThatDoesNotSpeakTLS(t *testing.T) {
	ca := newAuthority(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\nplain text, not TLS"))
			c.Close()
		}
	}()
	s := newTestService(t, ca, safehttp.Config{
		Resolver: fakeResolver{ips: []string{"93.184.216.34"}},
		Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, n, ln.Addr().String())
		},
	})
	if _, err := s.Inspect(context.Background(), "example.com"); !errors.Is(err, ErrNoTLS) {
		t.Errorf("err = %v, want ErrNoTLS", err)
	}
}

func TestServerThatNeverAnswersTheHandshakeTimesOut(t *testing.T) {
	ca := newAuthority(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() }) // holds the connection open and says nothing
		}
	}()
	s := newTestService(t, ca, safehttp.Config{
		Resolver: fakeResolver{ips: []string{"93.184.216.34"}},
		Dial: func(ctx context.Context, n, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, n, ln.Addr().String())
		},
	})
	s.guard = toolguard.New[Report](toolguard.Options{Timeout: 300 * time.Millisecond})
	start := time.Now()
	if _, err := s.Inspect(context.Background(), "example.com"); !errors.Is(err, ErrTimeout) {
		t.Errorf("err = %v, want ErrTimeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestInvalidInputNeverReachesTheNetwork(t *testing.T) {
	ca := newAuthority(t)
	s := newTestService(t, ca, safehttp.Config{
		Resolver: fakeResolver{err: errors.New("must not resolve")},
		Dial:     func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("must not dial") },
	})
	for _, in := range []string{"", "127.0.0.1", "localhost", "10.0.0.1", "example.com:22", "user@example.com"} {
		var ih *InvalidHostError
		if _, err := s.Inspect(context.Background(), in); !errors.As(err, &ih) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
}

// --- abuse limits ---

func TestRepeatedRequestsForAHostMakeOneConnection(t *testing.T) {
	ca := newAuthority(t)
	var dials atomic.Int32
	s := serve(t, ca, ca.issue(t, good("example.com")), 0)
	inner := s.dial
	s.dial = func(ctx context.Context, n, a string) (net.Conn, error) { dials.Add(1); return inner(ctx, n, a) }
	for i := 0; i < 30; i++ {
		if _, err := s.Inspect(context.Background(), "example.com"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if dials.Load() != 1 {
		t.Errorf("%d connections for 30 requests", dials.Load())
	}
}

func TestTheHostBudgetAppliesThroughTheService(t *testing.T) {
	ca := newAuthority(t)
	s := newTestService(t, ca, safehttp.Config{
		Resolver: fakeResolver{ips: []string{"93.184.216.34"}},
		Dial:     func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("refused") },
	})
	s.guard = toolguard.New[Report](toolguard.Options{TargetMax: 2})
	for i := 0; i < 2; i++ {
		if _, err := s.Inspect(context.Background(), "example.com"); !errors.Is(err, ErrConnect) {
			t.Fatalf("check %d: %v", i, err)
		}
	}
	var tl *TargetLimitedError
	if _, err := s.Inspect(context.Background(), "https://EXAMPLE.com/other/path"); !errors.As(err, &tl) {
		t.Errorf("the same host in another spelling must share the budget: %v", err)
	}
}

func TestPortsOtherThan443AreRefused(t *testing.T) {
	for _, in := range []string{"example.com:80", "http://example.com:80", "example.com:8443", "example.com:22"} {
		var ih *InvalidHostError
		if _, err := Normalize(in); !errors.As(err, &ih) {
			t.Errorf("%q: err = %v", in, err)
		}
	}
	for in, want := range map[string]string{"example.com": "example.com", "https://Example.com:443/x": "example.com", "http://example.com": "example.com"} {
		if got, err := Normalize(in); err != nil || got != want {
			t.Errorf("%q: %q, %v", in, got, err)
		}
	}
}
