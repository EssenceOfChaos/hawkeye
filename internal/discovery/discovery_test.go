package discovery

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"hawkeye/internal/safehttp"
)

// testCert is a certificate for example.com and its subdomains, trusted by pool.
func testCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "example.com"},
		DNSNames:  []string{"example.com", "*.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// world serves fake hosts. Each host name resolves to its own public address; https (443) and http (80) on that address reach the host's handlers.
// A name that is not listed does not exist.
type world struct {
	t       *testing.T
	cert    tls.Certificate
	pool    *x509.CertPool
	ips     map[string]netip.Addr
	servers map[string]string // "ip:port" -> listener address
	mu      sync.Mutex
	seen    []string // "METHOD host path?query" of every request received
}

func newWorld(t *testing.T) *world {
	cert, pool := testCert(t)
	return &world{t: t, cert: cert, pool: pool, ips: map[string]netip.Addr{}, servers: map[string]string{}}
}

// host adds a name. https and plain may be nil (nothing listens on that port). ip overrides the address it resolves to.
func (w *world) host(name string, https, plain http.Handler, ip ...string) {
	addr := netip.AddrFrom4([4]byte{93, 184, 216, byte(10 + len(w.ips))})
	if len(ip) > 0 {
		addr = netip.MustParseAddr(ip[0])
	}
	w.ips[name] = addr
	record := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			w.mu.Lock()
			w.seen = append(w.seen, r.Method+" "+r.Host+" "+r.URL.RequestURI())
			w.mu.Unlock()
			h.ServeHTTP(rw, r)
		})
	}
	if https != nil {
		srv := httptest.NewUnstartedServer(record(https))
		srv.TLS = &tls.Config{Certificates: []tls.Certificate{w.cert}}
		srv.StartTLS()
		w.t.Cleanup(srv.Close)
		w.servers[net.JoinHostPort(addr.String(), "443")] = srv.Listener.Addr().String()
	}
	if plain != nil {
		srv := httptest.NewServer(record(plain))
		w.t.Cleanup(srv.Close)
		w.servers[net.JoinHostPort(addr.String(), "80")] = srv.Listener.Addr().String()
	}
}

func (w *world) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if ip, ok := w.ips[host]; ok {
		return []netip.Addr{ip}, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func (w *world) service(mx MXResolver) *Service {
	s := New(safehttp.Config{
		RootCAs:  w.pool,
		Resolver: w,
		Dial: func(ctx context.Context, n, addr string) (net.Conn, error) {
			if to, ok := w.servers[addr]; ok {
				return (&net.Dialer{}).DialContext(ctx, n, to)
			}
			return nil, &net.OpError{Op: "dial", Net: n, Err: errors.New("connection refused")}
		},
	})
	s.probe = func() string { return "/hawkeye-setup-probe" }
	if mx == nil {
		mx = mxTable{}
	}
	return s.WithMXResolver(mx)
}

type mxTable map[string][]*net.MX

func (m mxTable) LookupMX(_ context.Context, name string) ([]*net.MX, error) {
	if r, ok := m[name]; ok {
		return r, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}

// routes answers the listed paths with a status and content type; any other path is 404.
func routes(m map[string][2]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := m[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", a[1])
		switch a[0] {
		case "200":
			_, _ = w.Write([]byte("ok"))
		case "403":
			w.WriteHeader(http.StatusForbidden)
		case "503":
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
}

const (
	html = "text/html; charset=utf-8"
	json = "application/json"
)

func kinds(r Report) string {
	var k []string
	for _, s := range r.Suggestions {
		k = append(k, s.Kind+"="+s.Monitor.URL)
	}
	return strings.Join(k, " ")
}

func TestSuggestsHomeHealthAPIAndMail(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", routes(map[string][2]string{"/": {"200", html}, "/healthz": {"200", json}, "/health": {"200", json}}), nil)
	w.host("api.example.com", routes(map[string][2]string{"/": {"404", json}, "/api/health": {"200", "text/plain"}}), nil)
	mx := mxTable{"example.com": {{Host: "aspmx.l.google.com.", Pref: 1}}}

	r, err := w.service(mx).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := "website=https://example.com/ health=https://example.com/healthz api=https://api.example.com/api/health mail=example.com"
	if got := kinds(r); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	for _, s := range r.Suggestions {
		if !s.Recommended || s.Reason == "" || s.Monitor.Name == "" {
			t.Errorf("%+v", s)
		}
	}
	if m := r.Suggestions[3].Monitor; m.Type != "dns" || m.DNSRecordType == nil || *m.DNSRecordType != "MX" || !strings.Contains(r.Suggestions[3].Reason, "aspmx.l.google.com") {
		t.Errorf("mail: %+v %s", m, r.Suggestions[3].Reason)
	}
	if !strings.Contains(r.Suggestions[0].Reason, "SSL certificate") {
		t.Errorf("an https site's monitor also watches its certificate: %s", r.Suggestions[0].Reason)
	}
}

func TestPrefersReadinessAndSendsOnlyFixedGETs(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", routes(map[string][2]string{"/": {"200", html}, "/readyz": {"200", json}, "/healthz": {"200", json}}), nil)

	r, err := w.service(nil).Suggest(context.Background(), "https://example.com/account/settings?token=SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(r); got != "website=https://example.com/ health=https://example.com/readyz" {
		t.Fatal(got)
	}
	allowed := map[string]bool{"/": true, "/hawkeye-setup-probe": true}
	for _, p := range healthPaths {
		allowed[p] = true
	}
	for _, s := range w.seen {
		f := strings.Fields(s)
		if f[0] != http.MethodGet || !allowed[f[2]] || strings.Contains(s, "SECRET") {
			t.Errorf("unexpected request %q: only GETs to fixed paths may be sent", s)
		}
	}
}

func TestCatchAllSiteHasNoHealthEndpoint(t *testing.T) {
	w := newWorld(t)
	// Every path answers 200, as a single-page app or a catch-all route does: a 200 from /healthz proves nothing.
	w.host("example.com", http.HandlerFunc(func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "text/plain")
		_, _ = rw.Write([]byte("hi"))
	}), nil)

	r, err := w.service(nil).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(r); got != "website=https://example.com/" {
		t.Fatal(got)
	}
}

func TestWebPageAtHealthPathIsNotAnEndpoint(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", routes(map[string][2]string{"/": {"200", html}, "/health": {"200", html}}), nil)

	r, err := w.service(nil).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(r); got != "website=https://example.com/" {
		t.Fatal(got)
	}
}

func TestRefusedHomePageIsNotPreselected(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", routes(map[string][2]string{"/": {"403", html}}), nil)

	r, err := w.service(nil).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Suggestions) != 1 || r.Suggestions[0].Recommended || !strings.Contains(r.Suggestions[0].Reason, "403") {
		t.Fatalf("%+v", r.Suggestions)
	}
}

func TestDownSiteIsStillSuggested(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", routes(map[string][2]string{"/": {"503", html}}), nil)

	r, err := w.service(nil).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Suggestions) != 1 || !r.Suggestions[0].Recommended || !strings.Contains(r.Suggestions[0].Reason, "503") {
		t.Fatalf("a site that is down right now is the one most worth monitoring: %+v", r.Suggestions)
	}
}

func TestFallsBackToPlainHTTP(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", nil, routes(map[string][2]string{"/": {"200", html}}))

	r, err := w.service(nil).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(r); got != "website=http://example.com/" || !strings.Contains(r.Suggestions[0].Reason, "plain http") {
		t.Fatalf("%s: %s", got, r.Suggestions[0].Reason)
	}
}

func TestRedirectIsExplained(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", http.RedirectHandler("https://www.example.com/", http.StatusMovedPermanently), nil)
	w.host("www.example.com", routes(map[string][2]string{"/": {"200", html}}), nil)

	r, err := w.service(nil).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Suggestions[0].Reason, "redirects to www.example.com") {
		t.Fatal(r.Suggestions[0].Reason)
	}
}

func TestNullMXAndMissingAPIAreLeftOut(t *testing.T) {
	w := newWorld(t)
	w.host("example.com", routes(map[string][2]string{"/": {"200", html}}), nil)
	mx := mxTable{"example.com": {{Host: ".", Pref: 0}}}

	r, err := w.service(mx).Suggest(context.Background(), "www.example.com")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("www.example.com does not exist in this world: %v %+v", err, r)
	}
	r, err = w.service(mx).Suggest(context.Background(), "example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got := kinds(r); got != "website=https://example.com/" {
		t.Fatal(got)
	}
}

func TestErrors(t *testing.T) {
	w := newWorld(t)
	w.host("internal.example.com", routes(map[string][2]string{"/": {"200", html}}), nil, "10.0.0.5")
	s := w.service(nil)

	if _, err := s.Suggest(context.Background(), "nope.example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown name: %v", err)
	}
	if _, err := s.Suggest(context.Background(), "internal.example.com"); !errors.Is(err, ErrBlocked) {
		t.Errorf("private address: %v", err)
	}
	var invalid *InvalidHostError
	if _, err := s.Suggest(context.Background(), "localhost"); !errors.As(err, &invalid) {
		t.Errorf("not a public name: %v", err)
	}
}
