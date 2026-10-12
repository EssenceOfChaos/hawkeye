package checks

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
func (c *Checker) Check(ctx context.Context, t Target) (Result, error) {
	res := Result{CheckedAt: c.now()}
	finish := func() (Result, error) {
		res.FinishedAt = c.now()
		return res, nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, t.Method, t.URL, nil)
	if err != nil {
		res.ErrorCategory, res.ErrorSummary = CatOther, "Invalid request"
		return finish()
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "*/*")

	resp, err := safehttp.New(c.cfg, t.Timeout).Do(req)
	elapsed := int(c.now().Sub(res.CheckedAt) / time.Millisecond)
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err() // caller shut down; the outcome says nothing about the site
		}
		res.ErrorCategory, res.ErrorSummary = Classify(err)
		return finish()
	}
	defer resp.Body.Close()
	_, _ = io.CopyN(io.Discard, resp.Body, maxDrainBytes) // never keep or interpret the body

	code := resp.StatusCode
	res.StatusCode, res.ResponseTimeMs = &code, &elapsed
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
	}
	return finish()
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
