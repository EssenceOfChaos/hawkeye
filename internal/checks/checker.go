package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

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
}

// Checker runs checks through the SSRF-safe HTTP client.
type Checker struct {
	cfg safehttp.Config
	now func() time.Time
}

func NewChecker(cfg safehttp.Config) *Checker { return &Checker{cfg: cfg, now: time.Now} }

// Check performs a check. A check that fails because the target is down, slow or prohibited returns a
// Result with Success=false. The error is non-nil only when ctx itself was cancelled, in which case the
// check did not complete and must not be recorded.
//
// Success rule: with an expected status code, the final status (after redirects) must equal it;
// otherwise any final status below 400 is up. Transport failures (DNS, connect, TLS, timeout) always fail.
// When the status passes, every assertion must hold on the response body (CatAssertion otherwise).
func (c *Checker) Check(ctx context.Context, t Target) (Result, error) {
	res := Result{CheckedAt: c.now()}
	finish := func() (Result, error) {
		res.FinishedAt = c.now()
		return res, nil
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

	client := safehttp.New(c.cfg, t.Timeout)
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
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err() // caller shut down; the outcome says nothing about the site
		}
		res.ErrorCategory, res.ErrorSummary = Classify(err)
		return finish()
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	res.StatusCode, res.ResponseTimeMs = &code, &elapsed
	// The body is only read when assertions need it, and then only up to MaxBodyBytes; it is never stored.
	var respBody []byte
	truncated := false
	if len(t.Assertions) > 0 {
		respBody, err = io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			res.ErrorCategory, res.ErrorSummary = Classify(err)
			return finish()
		}
		if len(respBody) > MaxBodyBytes {
			respBody, truncated = respBody[:MaxBodyBytes], true
		}
	} else {
		_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes)
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
		return finish()
	}
	if msg := evaluate(t.Assertions, respBody, truncated); msg != "" {
		res.Success, res.ErrorCategory, res.ErrorSummary = false, CatAssertion, truncate(msg, maxSummaryLen)
	}
	return finish()
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

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Classify maps a transport error to a stable category and a fixed, sanitised summary. Raw error
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
