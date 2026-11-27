package checks

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"hawkeye/internal/safehttp"
)

// Error categories stored with failed checks.
const (
	CatDNS        = "dns"
	CatConnect    = "connect"
	CatTLS        = "tls"
	CatTimeout    = "timeout"
	CatRedirect   = "redirect"
	CatBlocked    = "blocked"
	CatHTTPStatus = "http_status"
	CatAssertion  = "assertion"
	CatOther      = "other"
)

const (
	userAgent     = "Hawkeye-Monitor/1.0"
	maxDrainBytes = 64 << 10
	maxSummaryLen = 200
)

// Target describes one check.
type Target struct {
	URL            string
	Method         string
	ExpectedStatus *int
	Timeout        time.Duration
	Headers        []Header
	Body           string
	Auth           Auth
	Assertions     []Assertion
	// UserAgent replaces the monitoring engine's default. The free website checker names itself so site owners can tell it apart.
	UserAgent string
	// TraceContext sends a W3C traceparent header with a new random trace ID, sampled, so the request shows up in the site's own tracing. The ID
	// is returned in Result.TraceID.
	TraceContext bool
	// CaptureBody keeps the start of a failed response's body in Result.Excerpt (the monitor opted in; off by default). Successful checks
	// read no more of the body than without it.
	CaptureBody bool
}

// Result is the outcome of one completed check. ResponseTimeMs is the time until response headers arrived.
type Result struct {
	CheckedAt      time.Time
	FinishedAt     time.Time
	Success        bool
	StatusCode     *int
	ResponseTimeMs *int
	ErrorCategory  string
	ErrorSummary   string
	// CertExpiresAt is when the monitored host's TLS certificate expires, when the check reached one (HTTPS monitors only). It is also set
	// when the handshake failed because that certificate had already expired.
	CertExpiresAt *time.Time
	// Region is where the check ran; the worker sets it. Empty means the primary region.
	Region string
	// DNSAnswers are a DNS lookup's answers (normalized), for change alerts only: never stored with the result or put in a summary.
	DNSAnswers []string
	// Timings is where the time went (website and API checks that opened a connection; nil otherwise). A failed check keeps the phases it
	// got through, and the one it was stuck in counts up to the failure.
	Timings *Timings
	// Recheck marks the immediate re-check after a failure (the worker sets it). It still decides the monitor's health, but it is left out of
	// uptime, response times and slow-response streaks, so those stay one sample per interval.
	Recheck bool
	// FinalURL is where the request ended up after redirects, when a response arrived. Credentials, query and fragment are not included.
	FinalURL string
	// TraceID is the trace ID sent in the traceparent header (32 lower-case hex digits), when the target asked for trace context.
	TraceID string
	// ContentType is the response's media type (lower case, without parameters such as charset), when a response arrived. Not stored.
	ContentType string
	// Steps is set for multi-step API checks: what each step that ran did (workflow.go). Stored with the result; never response content.
	Steps []StepOutcome
	// Facts is what the check saw of the response, for failure snapshots (snapshot.go): set when a connection was attempted. For a workflow,
	// the failing step's (FailedStep), or nil when every step passed (each step's are on Steps). Never stored with the result.
	Facts *ResponseFacts
	// FailedStep is the index of a workflow's failing step, or -1.
	FailedStep int
	// Excerpt is the start of a failed response's body, when the target asked for it (CaptureBody). Never stored with the result.
	Excerpt *Excerpt
}

// Timings splits a check's time into phases, each summed over every connection the check made (one per redirect). Their sum is close to
// ResponseTimeMs; the small rest is the client's own work.
type Timings struct {
	DNSMs     int `json:"dns_ms"`     // looking up the host name (0 for an address)
	ConnectMs int `json:"connect_ms"` // opening the TCP connection
	TLSMs     int `json:"tls_ms"`     // the TLS handshake (0 for http://)
	WaitMs    int `json:"wait_ms"`    // server wait: from sending the request to the first byte of the response
	Redirects int `json:"redirects"`
}

// timingsFrom turns measured phases into Timings, or nil when no connection was attempted. Values are capped to fit their smallint columns
// (a check's timeout is at most 30 s, so the cap is never reached in practice).
func timingsFrom(p safehttp.PhaseTimes) *Timings {
	if p.Connections == 0 {
		return nil
	}
	ms := func(d time.Duration) int { return min(int(d/time.Millisecond), 32767) }
	return &Timings{DNSMs: ms(p.DNS), ConnectMs: ms(p.Connect), TLSMs: ms(p.TLS), WaitMs: ms(p.Wait), Redirects: min(p.Connections-1, 32767)}
}

// Checker runs checks through the SSRF-safe HTTP client.
type Checker struct {
	cfg safehttp.Config
	now func() time.Time
	dns DNSResolver // used by DNS monitors; website checks resolve through the safehttp client
}

func NewChecker(cfg safehttp.Config) *Checker {
	return &Checker{cfg: cfg, now: time.Now, dns: net.DefaultResolver}
}

// Check performs a check. A check that fails because the target is down, slow or prohibited returns a
// Result with Success=false. The error is non-nil only when ctx itself was canceled, in which case the
// check did not complete and must not be recorded.
//
// Success rule: with an expected status code, the final status (after redirects) must equal it;
// otherwise any final status below 400 is up. Transport failures (DNS, connect, TLS, timeout) always fail.
// When the status passes, every assertion must hold on the response body (CatAssertion otherwise).
func (c *Checker) Check(ctx context.Context, t Target) (Result, error) {
	res, _, err := c.run(ctx, t, nil, false)
	return res, err
}

// MaxPreviewBytes bounds the response body a test request shows. Larger bodies are still checked (up to MaxBodyBytes), just not shown.
const MaxPreviewBytes = 256 << 10

// Preview is what a test request shows of the response, so rules can be written from it. It goes back to the person who asked and is never
// stored or logged.
type Preview struct {
	Body      string // the body as text, up to MaxPreviewBytes; empty when Binary or there was no response
	Truncated bool   // the body was longer than what is shown
	Binary    bool   // the body is not UTF-8 text, so it isn't shown
	// Checks has one entry per assertion of the target, in order: "" when it holds, otherwise why not. Nil when no response arrived.
	Checks []string
}

// TryRequest runs one check like Check and also returns the response for the person testing it, with every assertion's outcome (Check stops
// at the first failure). Assertions are evaluated even when the status check fails, so a test of a broken endpoint still shows each rule.
func (c *Checker) TryRequest(ctx context.Context, t Target) (Result, Preview, error) {
	res, kept, err := c.run(ctx, t, nil, true)
	if err != nil || kept == nil {
		return res, Preview{}, err
	}
	p := Preview{Checks: EvaluateEach(t.Assertions, kept.body, kept.truncated)}
	if shown := kept.body[:validPrefix(kept.body, MaxPreviewBytes)]; utf8.Valid(shown) {
		p.Body, p.Truncated = string(shown), len(shown) < len(kept.body) || kept.truncated
	} else {
		p.Binary = true
	}
	return res, p, nil
}

// validPrefix is the length of b cut to at most n bytes without splitting a UTF-8 character.
func validPrefix(b []byte, n int) int {
	if len(b) <= n {
		return len(b)
	}
	for n > 0 && !utf8.RuneStart(b[n]) {
		n--
	}
	return n
}

// response is what a workflow step keeps of a response for extracting values: never stored, and gone when the run ends.
type response struct {
	header    http.Header
	cookies   []*http.Cookie
	body      []byte // read only when asked for, up to MaxBodyBytes
	truncated bool
}

// run performs one request as Check describes. jar, when set, sends and keeps cookies across a workflow's steps. With keep, the response's
// headers, cookies and body are returned for extractions (the body is read even without assertions).
func (c *Checker) run(ctx context.Context, t Target, jar http.CookieJar, keep bool) (Result, *response, error) {
	res := Result{CheckedAt: c.now(), FailedStep: -1}
	var kept *response
	finish := func() (Result, *response, error) {
		res.FinishedAt = c.now()
		return res, kept, nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	var body io.Reader
	if t.Body != "" {
		body = strings.NewReader(t.Body)
	}
	req, err := http.NewRequestWithContext(reqCtx, t.Method, t.URL, body)
	if err != nil {
		res.ErrorCategory, res.ErrorSummary = CatOther, "Invalid request"
		return finish()
	}
	req.Header.Set("Accept", "*/*")
	if t.Body != "" {
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		if json.Valid([]byte(t.Body)) {
			req.Header.Set("Content-Type", "application/json")
		}
	}
	for _, h := range t.Headers {
		req.Header.Set(h.Name, h.Value)
	}
	req.Header.Set("User-Agent", userAgent)
	if t.UserAgent != "" {
		req.Header.Set("User-Agent", t.UserAgent)
	}
	if t.TraceContext {
		// W3C Trace Context (version 00, sampled): a new trace for each check, with this request as its root span.
		traceID, spanID := randomHex(16), randomHex(8)
		req.Header.Set("Traceparent", "00-"+traceID+"-"+spanID+"-01")
		res.TraceID = traceID
	}

	// Watch the TLS handshake with the monitored host (not hosts it redirects to) to learn when its certificate expires, and time each phase
	// of the request. The client measures DNS and connect itself (it resolves and dials on its own); httptrace gives the TLS handshake and
	// the server's wait.
	cert := &certWatch{host: req.URL.Hostname()}
	phases := &safehttp.Phases{}
	req = req.WithContext(httptrace.WithClientTrace(safehttp.WithPhases(req.Context(), phases), &httptrace.ClientTrace{
		TLSHandshakeStart: phases.TLSStart,
		TLSHandshakeDone: func(cs tls.ConnectionState, err error) {
			phases.TLSDone()
			cert.handshakeDone(cs, err)
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { phases.WroteRequest() },
		GotFirstResponseByte: phases.GotFirstByte,
	}))

	client := safehttp.New(c.cfg, t.Timeout)
	client.Jar = jar
	if secretHeader := applyAuth(req, t.Auth); secretHeader != "" {
		// Credentials only go to the monitored host. Go already drops Authorization when a redirect leaves
		// the domain, but keeps it for subdomains and never knows about a custom API-key header.
		next := client.CheckRedirect
		client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
			if err := next(r, via); err != nil {
				return err
			}
			if !strings.EqualFold(r.URL.Host, via[0].URL.Host) {
				r.Header.Del(secretHeader)
			}
			return nil
		}
	}

	resp, err := client.Do(req)
	elapsed := int(c.now().Sub(res.CheckedAt) / time.Millisecond)
	pt := phases.Snapshot()
	res.Timings = timingsFrom(pt)
	res.CertExpiresAt = cert.expiry()
	if pt.Connections > 0 {
		res.Facts = &ResponseFacts{SizeBytes: -1, Timings: res.Timings, CertExpiresAt: res.CertExpiresAt, CertIssuer: cert.issuerName()}
		if pt.Addr.IsValid() {
			res.Facts.ServerAddr = pt.Addr.Unmap().String()
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, nil, ctx.Err() // caller shut down; the outcome says nothing about the site
		}
		if res.CertExpiresAt == nil {
			res.CertExpiresAt = expiredCertificate(err, c.now())
			if res.Facts != nil {
				res.Facts.CertExpiresAt = res.CertExpiresAt
			}
		}
		res.ErrorCategory, res.ErrorSummary = Classify(err)
		return finish()
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	res.StatusCode, res.ResponseTimeMs = &code, &elapsed
	res.FinalURL = displayURL(resp.Request.URL)
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil {
		res.ContentType = mt
	}
	if res.Facts == nil { // a response always comes over a connection; this only guards a custom transport in tests
		res.Facts = &ResponseFacts{SizeBytes: -1, Timings: res.Timings}
	}
	res.Facts.StatusCode, res.Facts.FinalURL, res.Facts.ContentType = code, res.FinalURL, res.ContentType
	res.Facts.Headers = keepHeaders(resp.Header)
	// The body is only read when assertions need it, and then only up to MaxBodyBytes; it is never stored. Otherwise it is drained, and with
	// CaptureBody the drain keeps its first MaxExcerptBytes in case the check fails.
	var respBody, head []byte
	truncated := false
	var bodyRead int64
	if len(t.Assertions) > 0 || keep {
		respBody, err = io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, nil, ctx.Err()
			}
			res.ErrorCategory, res.ErrorSummary = Classify(err)
			return finish()
		}
		if len(respBody) > MaxBodyBytes {
			respBody, truncated = respBody[:MaxBodyBytes], true
		}
		bodyRead = int64(len(respBody))
	} else {
		if t.CaptureBody {
			head, _ = io.ReadAll(io.LimitReader(resp.Body, MaxExcerptBytes+1))
		}
		n, _ := io.CopyN(io.Discard, resp.Body, maxDrainBytes-int64(len(head)))
		bodyRead = int64(len(head)) + n
		truncated = bodyRead >= maxDrainBytes
	}
	if resp.ContentLength >= 0 {
		res.Facts.SizeBytes = resp.ContentLength
	} else {
		res.Facts.SizeBytes, res.Facts.SizeAtLeast = bodyRead, truncated
	}
	excerpt := func() {
		if !t.CaptureBody {
			return
		}
		b, more := head, len(head) > MaxExcerptBytes || truncated
		if respBody != nil {
			b, more = respBody, truncated || len(respBody) > MaxExcerptBytes
		}
		res.Excerpt = excerptOf(b, more, credentialRedactor(t))
	}
	if keep {
		kept = &response{header: resp.Header, cookies: resp.Cookies(), body: respBody, truncated: truncated}
	}

	if t.ExpectedStatus != nil {
		res.Success = code == *t.ExpectedStatus
	} else {
		res.Success = code < 400
	}
	if !res.Success {
		res.ErrorCategory = CatHTTPStatus
		res.ErrorSummary = fmt.Sprintf("Received HTTP %d", code)
		if t.ExpectedStatus != nil {
			res.ErrorSummary = fmt.Sprintf("Received HTTP %d, expected %d", code, *t.ExpectedStatus)
		}
		excerpt()
		return finish()
	}
	if msg := evaluate(t.Assertions, respBody, truncated); msg != "" {
		res.Success, res.ErrorCategory, res.ErrorSummary = false, CatAssertion, truncate(msg, maxSummaryLen)
		excerpt()
	}
	return finish()
}

// displayURL is u as it may be shown to a visitor: scheme, host and path only.
func displayURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}

// applyAuth adds credentials to req and returns the header that carries them, or "" for none.
func applyAuth(req *http.Request, a Auth) string {
	switch a.Type {
	case AuthBasic:
		req.SetBasicAuth(a.Username, a.Secret)
		return "Authorization"
	case AuthBearer:
		req.Header.Set("Authorization", "Bearer "+a.Secret)
		return "Authorization"
	case AuthHeader:
		req.Header.Set(a.HeaderName, a.Secret)
		return a.HeaderName
	}
	return ""
}

// certWatch records the expiry of the certificate presented by the monitored host. Handshake callbacks can arrive on other goroutines.
type certWatch struct {
	host   string
	mu     sync.Mutex
	at     *time.Time
	issuer string
}

func (w *certWatch) handshakeDone(cs tls.ConnectionState, err error) {
	// Only the monitored host's own certificate counts: a redirect to another host must not overwrite it.
	if err != nil || len(cs.PeerCertificates) == 0 || !strings.EqualFold(cs.ServerName, w.host) {
		return
	}
	na := cs.PeerCertificates[0].NotAfter
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.at == nil {
		w.at, w.issuer = &na, cs.PeerCertificates[0].Issuer.CommonName
	}
}

func (w *certWatch) expiry() *time.Time {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.at
}

func (w *certWatch) issuerName() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.issuer
}

// expiredCertificate returns the certificate's expiry when a handshake failed because the server's own certificate had expired.
func expiredCertificate(err error, now time.Time) *time.Time {
	var cv *tls.CertificateVerificationError
	if !errors.As(err, &cv) || len(cv.UnverifiedCertificates) == 0 {
		return nil
	}
	var inv x509.CertificateInvalidError
	if !errors.As(cv.Err, &inv) || inv.Reason != x509.Expired {
		return nil
	}
	leaf := cv.UnverifiedCertificates[0]
	if !leaf.NotAfter.Before(now) {
		return nil // an intermediate expired, not the site's certificate
	}
	na := leaf.NotAfter
	return &na
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Classify maps a transport error to a stable category and a fixed, sanitized summary. Raw error
// text is never stored because it can contain resolved internal addresses.
func Classify(err error) (category, summary string) {
	var (
		dnsErr   *net.DNSError
		unknown  x509.UnknownAuthorityError
		invalid  x509.CertificateInvalidError
		hostname x509.HostnameError
		verify   *tls.CertificateVerificationError
		record   tls.RecordHeaderError
		netErr   net.Error
		opErr    *net.OpError
	)
	switch {
	case errors.Is(err, safehttp.ErrBlocked):
		return CatBlocked, "Destination is not permitted"
	case errors.Is(err, safehttp.ErrTooManyRedirects):
		return CatRedirect, "Too many redirects"
	case errors.As(err, &dnsErr):
		return CatDNS, "DNS lookup failed"
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &hostname), errors.As(err, &verify), errors.As(err, &record),
		strings.Contains(err.Error(), "tls:"):
		return CatTLS, "TLS handshake or certificate error"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return CatTimeout, "Request timed out"
	case errors.As(err, &opErr):
		return CatConnect, "Could not connect to the server"
	}
	return CatOther, "Request failed"
}

// randomHex returns n random bytes as lower-case hex. Trace Context forbids an all-zero ID; with crypto/rand that can't realistically happen, but
// it is checked anyway.
func randomHex(n int) string {
	b := make([]byte, n)
	for {
		_, _ = rand.Read(b)
		for _, c := range b {
			if c != 0 {
				return hex.EncodeToString(b)
			}
		}
	}
}
