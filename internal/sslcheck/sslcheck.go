// Package sslcheck inspects the TLS certificate a public host presents on port 443, for the free SSL checker on the website.
//
// It is reachable by anyone, so it is built to be a poor tool for abuse:
//   - it connects only to host names (never IP addresses), only to port 443, only through safehttp's dialer, which resolves the name
//     itself, refuses any non-public answer and dials the validated address (the SSRF boundary, shared with the monitoring engine);
//   - it reads the handshake and hangs up: it sends no HTTP request and returns no content from the host, only facts about the certificate;
//   - concurrency is capped, each host can be checked only a few times in a window (so the tool cannot be aimed repeatedly at one
//     server) and results are cached briefly (internal/toolguard). These limits are per process; the per-IP limit is applied by the API in
//     front of it.
package sslcheck

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"hawkeye/internal/checks"
	"hawkeye/internal/publichost"
	"hawkeye/internal/safehttp"
	"hawkeye/internal/toolguard"
)

// Errors a visitor can be told about.
var (
	ErrBlocked  = errors.New("destination not permitted")
	ErrNotFound = errors.New("host not found")
	ErrConnect  = errors.New("could not connect")
	ErrNoTLS    = errors.New("no TLS handshake")
	ErrTimeout  = errors.New("timed out")
	ErrBusy     = toolguard.ErrBusy
)

// TargetLimitedError means this host was checked too often recently (by anyone). RetryAfter is when it may be checked again.
type TargetLimitedError = toolguard.TargetLimitedError

// InvalidHostError means the input is not a host name this tool will look at. Reason is written for the visitor.
type InvalidHostError = publichost.InvalidHostError

// Problem severities.
const (
	SeverityError   = "error"   // browsers would warn or refuse
	SeverityWarning = "warning" // works today, but should be fixed
)

// Statuses reported for a certificate (checks.SSLValid, SSLExpiring, SSLExpired, plus invalid).
const StatusInvalid = "invalid"

type Problem struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

type Certificate struct {
	SubjectName        string    `json:"subject_name"`
	SubjectOrg         string    `json:"subject_org,omitempty"`
	IssuerName         string    `json:"issuer_name"`
	IssuerOrg          string    `json:"issuer_org,omitempty"`
	NotBefore          time.Time `json:"not_before"`
	NotAfter           time.Time `json:"not_after"`
	DNSNames           []string  `json:"dns_names"`
	DNSNamesTruncated  bool      `json:"dns_names_truncated,omitempty"`
	SerialNumber       string    `json:"serial_number"`
	SignatureAlgorithm string    `json:"signature_algorithm"`
	KeyAlgorithm       string    `json:"key_algorithm"`
	KeyBits            int       `json:"key_bits,omitempty"`
	SHA256Fingerprint  string    `json:"sha256_fingerprint"`
}

type ChainLink struct {
	SubjectName string    `json:"subject_name"`
	IssuerName  string    `json:"issuer_name"`
	NotAfter    time.Time `json:"not_after"`
}

// Report is everything the checker says about a host. It is built only from the handshake.
type Report struct {
	Host          string      `json:"host"`
	CheckedAt     time.Time   `json:"checked_at"`
	Status        string      `json:"status"`
	DaysRemaining int         `json:"days_remaining"`
	Trusted       bool        `json:"trusted"`
	HostnameMatch bool        `json:"hostname_match"`
	TLSVersion    string      `json:"tls_version,omitempty"`  // empty when the certificate did not verify (the handshake did not complete)
	CipherSuite   string      `json:"cipher_suite,omitempty"` // likewise
	Problems      []Problem   `json:"problems"`
	Certificate   Certificate `json:"certificate"`
	Chain         []ChainLink `json:"chain"`
}

const (
	maxDNSNames = 20
	maxChain    = 6
)

// Service runs checks. The zero value is not usable; build one with New.
type Service struct {
	dial  safehttp.DialFunc
	roots *x509.CertPool // nil: the system trust store
	now   func() time.Time
	guard *toolguard.Guard[Report]
}

// New builds a Service whose connections follow cfg's destination policy (the zero Config is the strict production policy).
func New(cfg safehttp.Config) *Service {
	return &Service{dial: safehttp.NewDialer(cfg), roots: cfg.RootCAs, now: time.Now, guard: toolguard.New[Report](toolguard.Options{})}
}

// Normalize reads what a visitor typed and returns the host to check. Only port 443 is ever contacted, so another port is refused.
func Normalize(raw string) (string, error) {
	t, err := publichost.Parse(raw)
	if err != nil {
		return "", err
	}
	if t.Port == "80" {
		return "", &InvalidHostError{Reason: "Only the standard HTTPS port (443) can be checked."}
	}
	return t.Host, nil
}

// Inspect checks the certificate of the host a visitor typed. Errors are the Err* values, *InvalidHostError or *TargetLimitedError.
func (s *Service) Inspect(ctx context.Context, input string) (Report, error) {
	host, err := Normalize(input)
	if err != nil {
		return Report{}, err
	}
	return s.guard.Do(ctx, host, func(ctx context.Context) (Report, error) {
		c, err := s.handshake(ctx, host)
		if err != nil {
			return Report{}, err
		}
		return s.report(host, c), nil
	})
}

// connection is what the handshake showed: the certificates the server presented, and the protocol it settled on (zero when the handshake did
// not complete, which is the case for a certificate that does not verify).
type connection struct {
	certs   []*x509.Certificate
	version uint16
	cipher  uint16
}

// handshake connects and negotiates TLS 1.2 or newer with the certificate checked as a browser would (the system trust store, the name asked
// about). Nothing is sent over the connection. A certificate that does not verify (expired, self-signed, for another name, from an unknown
// authority) fails the handshake, and that is exactly what a visitor wants to hear about: the chain the server presented comes with the error,
// and report() explains it. Verification is never turned off and the connection is never used.
func (s *Service) handshake(ctx context.Context, host string) (connection, error) {
	conn, err := s.dial(ctx, "tcp", net.JoinHostPort(host, "443"))
	if err != nil {
		return connection{}, classifyDial(ctx, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	tc := tls.Client(conn, &tls.Config{ServerName: host, RootCAs: s.roots, MinVersion: tls.VersionTLS12, Time: s.now})
	if err := tc.HandshakeContext(ctx); err != nil {
		var verify *tls.CertificateVerificationError
		if errors.As(err, &verify) && len(verify.UnverifiedCertificates) > 0 {
			return connection{certs: verify.UnverifiedCertificates}, nil
		}
		if ctx.Err() != nil || isTimeout(err) {
			return connection{}, ErrTimeout
		}
		return connection{}, ErrNoTLS
	}
	state := tc.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return connection{}, ErrNoTLS
	}
	return connection{certs: state.PeerCertificates, version: state.Version, cipher: state.CipherSuite}, nil
}

func classifyDial(ctx context.Context, err error) error {
	var dns *net.DNSError
	switch {
	case errors.Is(err, safehttp.ErrBlocked):
		return ErrBlocked
	case errors.As(err, &dns) && dns.IsNotFound:
		return ErrNotFound
	case ctx.Err() != nil || isTimeout(err):
		return ErrTimeout
	case dns != nil:
		return ErrNotFound // the name did not resolve (including a failing resolver)
	}
	return ErrConnect
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// report reads the certificate chain and verifies it by hand: whether the chain leads to a trusted root, whether the name matches and
// whether the dates hold are separate questions, and the visitor should hear about each one that fails.
func (s *Service) report(host string, c connection) Report {
	now := s.now()
	leaf := c.certs[0]
	r := Report{
		Host: host, CheckedAt: now.UTC(),
		DaysRemaining: checks.SSLDaysRemaining(leaf.NotAfter, now),
		Problems:      []Problem{}, Certificate: describe(leaf),
	}
	if c.version != 0 {
		r.TLSVersion, r.CipherSuite = tls.VersionName(c.version), tls.CipherSuiteName(c.cipher)
	}
	for i, cert := range c.certs {
		if i == maxChain {
			break
		}
		r.Chain = append(r.Chain, ChainLink{SubjectName: name(cert.Subject), IssuerName: name(cert.Issuer), NotAfter: cert.NotAfter.UTC()})
	}
	add := func(code, severity, format string, args ...any) {
		r.Problems = append(r.Problems, Problem{Code: code, Severity: severity, Message: fmt.Sprintf(format, args...)})
	}

	switch {
	case now.After(leaf.NotAfter):
		add("expired", SeverityError, "The certificate expired on %s (%s).", day(leaf.NotAfter), ago(now.Sub(leaf.NotAfter)))
	case now.Before(leaf.NotBefore):
		add("not_yet_valid", SeverityError, "The certificate is not valid until %s.", day(leaf.NotBefore))
	case checks.SSLStatus(leaf.NotAfter, now) == checks.SSLExpiring:
		add("expiring", SeverityWarning, "The certificate expires on %s, in %s.", day(leaf.NotAfter), plural(r.DaysRemaining, "day"))
	}

	r.HostnameMatch = leaf.VerifyHostname(host) == nil
	if !r.HostnameMatch {
		add("hostname_mismatch", SeverityError, "The certificate is not valid for %s. It is issued for %s.", host, namesSummary(leaf))
	}

	r.Trusted = s.verifyChain(leaf, c.certs[1:], now, add)

	switch {
	case leaf.SignatureAlgorithm == x509.SHA1WithRSA || leaf.SignatureAlgorithm == x509.ECDSAWithSHA1 || leaf.SignatureAlgorithm == x509.MD5WithRSA:
		add("weak_signature", SeverityWarning, "The certificate is signed with %s, which browsers no longer accept as secure.", leaf.SignatureAlgorithm)
	}
	if r.Certificate.KeyAlgorithm == "RSA" && r.Certificate.KeyBits < 2048 {
		add("weak_key", SeverityWarning, "The certificate uses a %d-bit RSA key. 2048 bits is the minimum considered secure.", r.Certificate.KeyBits)
	}

	r.Status = checks.SSLStatus(leaf.NotAfter, now)
	if r.Status != checks.SSLExpired {
		for _, p := range r.Problems {
			if p.Severity == SeverityError {
				r.Status = StatusInvalid
				break
			}
		}
	}
	sort.SliceStable(r.Problems, func(i, j int) bool {
		return r.Problems[i].Severity == SeverityError && r.Problems[j].Severity != SeverityError
	})
	return r
}

// verifyChain checks that the chain leads to a trusted root, judged at a moment inside the leaf's validity so that an expired leaf (reported
// separately) does not also read as an untrusted one.
func (s *Service) verifyChain(leaf *x509.Certificate, rest []*x509.Certificate, now time.Time, add func(code, severity, format string, args ...any)) bool {
	at := now
	if at.After(leaf.NotAfter) {
		at = leaf.NotAfter
	}
	if at.Before(leaf.NotBefore) {
		at = leaf.NotBefore
	}
	inter := x509.NewCertPool()
	for _, c := range rest {
		inter.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{Roots: s.roots, Intermediates: inter, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	if err == nil {
		return true
	}
	var unknown x509.UnknownAuthorityError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(err, &unknown) && isSelfSigned(leaf):
		add("self_signed", SeverityError, "The certificate is self-signed, so browsers do not trust it.")
	case errors.As(err, &unknown):
		add("untrusted_chain", SeverityError, "The certificate chain does not lead to a certificate authority that browsers trust. The server may be missing an intermediate certificate.")
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired:
		add("chain_expired", SeverityError, "A certificate in the chain (an intermediate or root) has expired.")
	default:
		add("untrusted_chain", SeverityError, "The certificate chain could not be verified.")
	}
	return false
}

// isSelfSigned reports whether the certificate names itself as its issuer and carries a signature made with its own key. The signature is
// checked directly because CheckSignatureFrom refuses a parent that is not marked as a certificate authority, which many self-signed
// server certificates are not.
func isSelfSigned(c *x509.Certificate) bool {
	return string(c.RawIssuer) == string(c.RawSubject) && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil
}

func describe(c *x509.Certificate) Certificate {
	d := Certificate{
		SubjectName: name(c.Subject), SubjectOrg: strings.Join(c.Subject.Organization, ", "),
		IssuerName: name(c.Issuer), IssuerOrg: strings.Join(c.Issuer.Organization, ", "),
		NotBefore: c.NotBefore.UTC(), NotAfter: c.NotAfter.UTC(),
		DNSNames: append([]string{}, c.DNSNames...), SerialNumber: c.SerialNumber.Text(16),
		SignatureAlgorithm: c.SignatureAlgorithm.String(),
	}
	if len(d.DNSNames) > maxDNSNames {
		d.DNSNames, d.DNSNamesTruncated = d.DNSNames[:maxDNSNames], true
	}
	sum := sha256.Sum256(c.Raw)
	d.SHA256Fingerprint = hex.EncodeToString(sum[:])
	switch k := c.PublicKey.(type) {
	case *rsa.PublicKey:
		d.KeyAlgorithm, d.KeyBits = "RSA", k.N.BitLen()
	case *ecdsa.PublicKey:
		d.KeyAlgorithm, d.KeyBits = "ECDSA", k.Curve.Params().BitSize
	case ed25519.PublicKey:
		d.KeyAlgorithm, d.KeyBits = "Ed25519", 256
	default:
		d.KeyAlgorithm = c.PublicKeyAlgorithm.String()
	}
	return d
}

// name is the most readable name for a certificate subject or issuer: its common name, else its organization, else the full distinguished name.
func name(n pkix.Name) string {
	switch {
	case n.CommonName != "":
		return n.CommonName
	case len(n.Organization) > 0:
		return strings.Join(n.Organization, ", ")
	}
	return n.String()
}

func namesSummary(c *x509.Certificate) string {
	names := c.DNSNames
	if len(names) == 0 {
		return "no host names"
	}
	if len(names) > 5 {
		return strings.Join(names[:5], ", ") + fmt.Sprintf(" and %d more", len(names)-5)
	}
	return strings.Join(names, ", ")
}

func day(t time.Time) string { return t.UTC().Format("January 2, 2006") }

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func ago(d time.Duration) string {
	days := int(d / (24 * time.Hour))
	if days < 1 {
		return "less than a day ago"
	}
	return plural(days, "day") + " ago"
}
