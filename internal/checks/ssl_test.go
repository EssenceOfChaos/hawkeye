package checks

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"hawkeye/internal/safehttp"
)

// loopbackResolver resolves every name to 127.0.0.1 (the system resolver also returns ::1, which the strict policy refuses).
type loopbackResolver struct{}

func (loopbackResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
}

func TestSSLBuckets(t *testing.T) {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	h := func(hours float64) time.Time { return now.Add(time.Duration(hours * float64(time.Hour))) }
	cases := []struct {
		name   string
		expiry time.Time
		bucket int
		warn   bool
		days   int
		status string
	}{
		{"far away", h(24 * 90), 0, false, 90, SSLValid},
		{"just outside 30 days", h(24*30 + 1), 0, false, 30, SSLValid},
		{"inside 30 days", h(24 * 29), 30, true, 29, SSLExpiring},
		{"exactly 30 days", h(24 * 30), 30, true, 30, SSLExpiring},
		{"inside 14 days", h(24 * 13), 14, true, 13, SSLExpiring},
		{"inside 7 days", h(24*6 + 5), 7, true, 6, SSLExpiring},
		{"inside 1 day", h(20), 1, true, 0, SSLExpiring},
		{"exactly now", h(0), 0, true, 0, SSLExpired},
		{"expired hours ago", h(-5), 0, true, -1, SSLExpired},
		{"expired 3 days ago", h(-24 * 3), 0, true, -3, SSLExpired},
	}
	for _, c := range cases {
		b, ok := SSLBucket(c.expiry, now)
		if ok != c.warn || (ok && b != c.bucket) {
			t.Errorf("%s: bucket=%d ok=%v, want %d %v", c.name, b, ok, c.bucket, c.warn)
		}
		if d := SSLDaysRemaining(c.expiry, now); d != c.days {
			t.Errorf("%s: days=%d, want %d", c.name, d, c.days)
		}
		if s := SSLStatus(c.expiry, now); s != c.status {
			t.Errorf("%s: status=%s, want %s", c.name, s, c.status)
		}
	}
}

// tlsServer serves HTTPS on 127.0.0.1 with a certificate for "localhost" valid between notBefore and notAfter, signed by a throwaway CA
// that the returned config trusts. The URL uses the name "localhost" so the certificate matches.
func tlsServer(t *testing.T, notBefore, notAfter time.Time, h http.Handler) (url string, cfg safehttp.Config) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test ca"},
		NotBefore: time.Now().Add(-100 * 24 * time.Hour), NotAfter: time.Now().Add(100 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign}
	caDER, _ := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	ca, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"}, DNSNames: []string{"localhost"},
		NotBefore: notBefore, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	cfg = loopbackCfg()
	cfg.RootCAs = pool
	cfg.Resolver = loopbackResolver{}
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	return "https://localhost:" + port + "/", cfg
}

func TestCheckRecordsCertificateExpiry(t *testing.T) {
	expiry := time.Now().Add(20 * 24 * time.Hour).Truncate(time.Second)
	url, cfg := tlsServer(t, time.Now().Add(-time.Hour), expiry, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	r := check(t, cfg, Target{URL: url})
	if !r.Success || r.CertExpiresAt == nil || !r.CertExpiresAt.Equal(expiry) {
		t.Fatalf("success=%v expiry=%v want %v (%s)", r.Success, r.CertExpiresAt, expiry, r.ErrorSummary)
	}
}

func TestExpiredCertificateIsRecordedEvenThoughTheCheckFails(t *testing.T) {
	expiry := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	url, cfg := tlsServer(t, time.Now().Add(-90*24*time.Hour), expiry, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	r := check(t, cfg, Target{URL: url})
	if r.Success || r.ErrorCategory != CatTLS {
		t.Fatalf("an expired certificate must fail the check as a TLS error: %+v", r)
	}
	if r.CertExpiresAt == nil || !r.CertExpiresAt.Equal(expiry) {
		t.Fatalf("expiry of the expired certificate not captured: %v want %v", r.CertExpiresAt, expiry)
	}
}

func TestCertificateOfAnotherHostIsNotRecorded(t *testing.T) {
	// The monitored host is plain HTTP and redirects to an HTTPS host: the certificate belongs to the other host, so nothing is recorded.
	tlsURL, cfg := tlsServer(t, time.Now().Add(-time.Hour), time.Now().Add(10*24*time.Hour), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, tlsURL, http.StatusFound) }))
	defer plain.Close()
	r := check(t, cfg, Target{URL: plain.URL})
	if !r.Success {
		t.Fatalf("%+v", r)
	}
	if r.CertExpiresAt != nil {
		t.Fatalf("recorded the redirect target's certificate (%v) for a plain HTTP monitor", r.CertExpiresAt)
	}
}

func TestPlainHTTPMonitorsHaveNoCertificate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	if r := check(t, loopbackCfg(), Target{URL: srv.URL}); r.CertExpiresAt != nil {
		t.Fatalf("%v", r.CertExpiresAt)
	}
}

func TestUntrustedCertificateDoesNotRecordAnExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	cfg := loopbackCfg()
	cfg.Resolver = loopbackResolver{}
	r := check(t, cfg, Target{URL: strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)})
	if r.Success || r.CertExpiresAt != nil {
		t.Fatalf("untrusted certificate: success=%v expiry=%v", r.Success, r.CertExpiresAt)
	}
}
