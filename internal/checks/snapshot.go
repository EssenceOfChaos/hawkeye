package checks

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// What a check saw of a response, for failure snapshots (docs/plan-failure-snapshots.md). ResponseFacts is never stored with a check result:
// it is kept as each location's last good response (monitor_last_good) and, when an outage is confirmed, in the incident's snapshot.

const (
	maxSnapshotHeaders = 20
	maxHeaderValueLen  = 200
	// MaxExcerptBytes is how much of a failed response's body a monitor that opted in keeps.
	MaxExcerptBytes = 4 << 10
	// excerptSlack is read beyond MaxExcerptBytes so that a secret crossing the cut is still recognized and hidden before cutting.
	excerptSlack = 1 << 10
	// minPartialSecret is the shortest start of a secret left at the end of an excerpt that is dropped (shorter ones say nothing).
	minPartialSecret = 3
	// maxChanges bounds the "what changed since the last good check" list.
	maxChanges = 6
)

// snapshotHeaders is the allowlist of response headers a snapshot keeps. It is an allowlist on purpose: a denylist would miss the custom
// header that carries a token. Never add Set-Cookie, Authorization or anything that can carry a credential (TestHeaderAllowlistHasNoSecrets).
// Volatile headers change from one response to the next (or from one CDN edge to another): they are shown, but never count as a change.
var snapshotHeaders = []struct {
	name     string // canonical form; with prefix, every header starting with it
	prefix   bool
	volatile bool
}{
	{name: "Age", volatile: true},
	{name: "Cache-Control"},
	{name: "Cf-Cache-Status", volatile: true}, // HIT, MISS, EXPIRED... from one request to the next
	{name: "Cf-Ray", volatile: true},
	{name: "Content-Encoding"},
	{name: "Content-Length", volatile: true}, // size is compared on its own, with a tolerance
	{name: "Content-Type", volatile: true},   // the media type is compared on its own (ContentType), without parameters such as charset
	{name: "Fly-Request-Id", volatile: true},
	{name: "Location"},
	{name: "Ratelimit-", prefix: true, volatile: true},
	{name: "Retry-After", volatile: true},
	{name: "Server"},
	{name: "Via", volatile: true}, // names the edge server (CloudFront puts a per-edge ID in it)
	{name: "Www-Authenticate"},
	{name: "X-Amz-Cf-Pop", volatile: true},
	{name: "X-Cache", volatile: true}, // hit or miss
	{name: "X-Powered-By"},
	{name: "X-Ratelimit-", prefix: true, volatile: true},
	{name: "X-Request-Id", volatile: true},
	{name: "X-Served-By", volatile: true},
	{name: "X-Vercel-Id", volatile: true},
}

// allowedHeader reports whether a snapshot keeps the header, and whether it is volatile.
func allowedHeader(name string) (keep, volatile bool) {
	name = http.CanonicalHeaderKey(name)
	for _, h := range snapshotHeaders {
		if name == h.name || h.prefix && strings.HasPrefix(name, h.name) {
			return true, h.volatile
		}
	}
	return false, false
}

// ResponseFacts is what a check saw, without the body. Every field is safe to show to the organization: headers come from the allowlist, and
// addresses are reduced as for the final URL.
type ResponseFacts struct {
	StatusCode  int    `json:"status_code,omitempty"` // 0 when no response arrived
	FinalURL    string `json:"final_url,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	// SizeBytes is Content-Length, or the bytes read when the server sent none; SizeAtLeast when reading stopped at a limit. -1 is unknown.
	SizeBytes   int64    `json:"size_bytes"`
	SizeAtLeast bool     `json:"size_at_least,omitempty"`
	Headers     []Header `json:"headers,omitempty"` // allowlisted, sorted by name, at most maxSnapshotHeaders
	// ServerAddr is the address of the server that answered (after redirects), or the last one connected to before a failure.
	ServerAddr    string     `json:"server_addr,omitempty"`
	CertExpiresAt *time.Time `json:"cert_expires_at,omitempty"`
	CertIssuer    string     `json:"cert_issuer,omitempty"` // the issuer's common name, such as "R11"
	Timings       *Timings   `json:"timings,omitempty"`
}

// Excerpt is the start of a failed response's body, kept only for monitors that opted in, with the monitor's credentials and header values
// (and a workflow's secrets and saved values) replaced by "[hidden]". It is sealed before it is stored, and is shown only in the app.
type Excerpt struct {
	Text      string // empty when Binary
	Truncated bool   // the body was longer than what is kept
	Binary    bool   // the body is not UTF-8 text, so nothing is kept
}

// keepHeaders returns the allowlisted headers of h, each value cut to maxHeaderValueLen, sorted by name.
func keepHeaders(h http.Header) []Header {
	var out []Header
	for name, values := range h {
		if ok, _ := allowedHeader(name); !ok || len(values) == 0 {
			continue
		}
		name = http.CanonicalHeaderKey(name)
		v := strings.Join(values, ", ")
		switch name {
		case "Location":
			v = reducedLocation(v)
		case "Www-Authenticate":
			v, _, _ = strings.Cut(strings.TrimSpace(v), " ") // the scheme only: realms and parameters can name internal things
		}
		v = truncate(strings.ToValidUTF8(v, "�"), maxHeaderValueLen)
		if v != "" {
			out = append(out, Header{Name: name, Value: v})
		}
	}
	slices.SortFunc(out, func(a, b Header) int { return strings.Compare(a.Name, b.Name) })
	if len(out) > maxSnapshotHeaders {
		out = out[:maxSnapshotHeaders]
	}
	return out
}

// reducedLocation is a redirect target as the final URL is shown: scheme, host and path only (a relative target keeps its path only).
func reducedLocation(v string) string {
	u, err := url.Parse(strings.TrimSpace(v))
	if err != nil {
		return ""
	}
	return displayURL(u)
}

// excerptOf builds the excerpt of a body (the first bytes read, at most MaxExcerptBytes kept), with secrets hidden. Secrets are hidden in a
// window a little longer than what is kept, before cutting, so one that crosses the cut is still caught; the start of a secret left at the very
// end (one that continues past the window) is dropped.
func excerptOf(body []byte, more bool, red *redactor) *Excerpt {
	w := validPrefix(body, MaxExcerptBytes+excerptSlack)
	window := body[:w]
	if !utf8.Valid(window) {
		return &Excerpt{Binary: true}
	}
	text := red.clean(string(window))
	n := validPrefix([]byte(text), MaxExcerptBytes)
	cut := n < len(text)
	return &Excerpt{Text: red.trimPartial(text[:n]), Truncated: more || w < len(body) || cut}
}

// credentialRedactor hides what a monitor sends that may come back in a response (an error page that echoes the request): its credentials,
// including the encoded form basic authentication actually sends, its custom header values, and its request body with each string in it.
func credentialRedactor(t Target) *redactor {
	r := &redactor{}
	r.add(t.Auth.Secret)
	if t.Auth.Type == AuthBasic {
		r.add(base64.StdEncoding.EncodeToString([]byte(t.Auth.Username + ":" + t.Auth.Secret)))
	}
	for _, h := range t.Headers {
		r.add(h.Value)
	}
	if b := strings.TrimSpace(t.Body); b != "" {
		r.add(b)
		var v any
		if json.Unmarshal([]byte(b), &v) == nil {
			addJSONStrings(r, v)
		}
	}
	return r
}

// addJSONStrings adds every string value (not key) of a JSON document to r: a login body's password, a token request's secret.
func addJSONStrings(r *redactor, v any) {
	switch x := v.(type) {
	case string:
		r.add(x)
	case []any:
		for _, e := range x {
			addJSONStrings(r, e)
		}
	case map[string]any:
		for _, e := range x {
			addJSONStrings(r, e)
		}
	}
}

// Fingerprint identifies the parts of the facts that make a response "the same" as before: status, final address, content type, stable
// headers, server address, certificate and size in steps of 25%. Timings are left out (they are compared with the week's averages instead),
// so a steady site's last good response is written once and then rarely.
func (f ResponseFacts) Fingerprint() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d\n%s\n%s\n%s\n%s\n", f.StatusCode, f.FinalURL, f.ContentType, addrNetwork(f.ServerAddr), f.CertIssuer)
	if f.CertExpiresAt != nil {
		b.WriteString(f.CertExpiresAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "\n%d\n", sizeStep(f.SizeBytes))
	for _, h := range f.Headers {
		if _, volatile := allowedHeader(h.Name); !volatile {
			fmt.Fprintf(&b, "%s: %s\n", h.Name, h.Value)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:16])
}

// sizeStep puts a size in steps of 25% (-1 when unknown), so small changes in a page's size don't count as a different response.
func sizeStep(n int64) int {
	if n < 0 {
		return -1
	}
	return int(math.Floor(math.Log(float64(n)+1) / math.Log(1.25)))
}

// Change is one difference between the last good response and the failing one. Before and After are ready to show.
type Change struct {
	Field  string `json:"field"`          // status, server_addr, final_url, content_type, certificate, header, size or timing
	Name   string `json:"name,omitempty"` // the header's name, or the timing phase (dns, connect, tls, wait)
	Before string `json:"before"`
	After  string `json:"after"`
}

// String is the change as one short line, such as "Server: nginx → cloudflare".
func (c Change) String() string {
	label := map[string]string{"status": "Status", "server_addr": "Server address", "final_url": "Address", "content_type": "Content type",
		"certificate": "Certificate", "size": "Size"}[c.Field]
	switch c.Field {
	case "header":
		label = c.Name
	case "timing":
		return fmt.Sprintf("%s: %s (usually %s)", phaseLabel(c.Name), c.After, c.Before)
	}
	return label + ": " + c.Before + " → " + c.After
}

func phaseLabel(phase string) string {
	switch phase {
	case "dns":
		return "DNS lookup"
	case "connect":
		return "Connect"
	case "tls":
		return "TLS handshake"
	}
	return "Server wait"
}

// Compare lists what changed from the last good response to the failing one, most telling first, at most maxChanges. usual is the monitor's
// average timings over the past week (nil without a baseline); a phase counts when it is more than twice its usual time and at least 100 ms
// longer. Size counts when it more than halved or grew by half. Fields the failing check never reached (no response, no connection) are not
// compared. Nil lastGood (a new monitor) gives no changes.
func Compare(lastGood *ResponseFacts, failing ResponseFacts, usual *Timings) []Change {
	if lastGood == nil {
		return nil
	}
	var out []Change
	add := func(field, name, before, after string) {
		if before != after {
			out = append(out, Change{Field: field, Name: name, Before: before, After: after})
		}
	}
	responded := failing.StatusCode != 0
	if responded && lastGood.StatusCode != 0 {
		add("status", "", strconv.Itoa(lastGood.StatusCode), strconv.Itoa(failing.StatusCode))
	}
	// Load balancers and CDNs answer from many addresses of one network: only a move to another network counts. A check that followed a
	// redirect and then got no response may name an earlier hop's server, not the one the last good check reached: not compared.
	hopUnknown := !responded && failing.Timings != nil && failing.Timings.Redirects > 0
	if failing.ServerAddr != "" && lastGood.ServerAddr != "" && !hopUnknown && addrNetwork(failing.ServerAddr) != addrNetwork(lastGood.ServerAddr) {
		add("server_addr", "", lastGood.ServerAddr, failing.ServerAddr)
	}
	if responded {
		add("final_url", "", orNone(lastGood.FinalURL), orNone(failing.FinalURL))
		add("content_type", "", orNone(lastGood.ContentType), orNone(failing.ContentType))
	}
	if failing.CertExpiresAt != nil && lastGood.CertExpiresAt != nil {
		add("certificate", "", certLabel(lastGood), certLabel(&failing))
	}
	if responded {
		before, after := stableHeaders(lastGood.Headers), stableHeaders(failing.Headers)
		names := make([]string, 0, len(before)+len(after))
		for n := range before {
			names = append(names, n)
		}
		for n := range after {
			if _, ok := before[n]; !ok {
				names = append(names, n)
			}
		}
		slices.Sort(names)
		for _, n := range names {
			add("header", n, orNone(before[n]), orNone(after[n]))
		}
		if b, a := lastGood.SizeBytes, failing.SizeBytes; b > 0 && a >= 0 && !failing.SizeAtLeast && (a*2 < b || a*2 > b*3) {
			add("size", "", Bytes(b), Bytes(a))
		}
	}
	if t := failing.Timings; t != nil && usual != nil {
		// The single phase that grew the most, if any grew enough to matter.
		var slowest *Change
		bestExtra := 0
		for _, p := range []struct {
			name      string
			now, then int
		}{{"dns", t.DNSMs, usual.DNSMs}, {"connect", t.ConnectMs, usual.ConnectMs}, {"tls", t.TLSMs, usual.TLSMs}, {"wait", t.WaitMs, usual.WaitMs}} {
			if extra := p.now - p.then; p.now > 2*p.then && extra >= 100 && extra > bestExtra {
				bestExtra = extra
				slowest = &Change{Field: "timing", Name: p.name, Before: ms(p.then), After: ms(p.now)}
			}
		}
		if slowest != nil {
			out = append(out, *slowest)
		}
	}
	if len(out) > maxChanges {
		out = out[:maxChanges]
	}
	return out
}

// addrNetwork is the network an address belongs to for comparing servers: its /24 for IPv4 and /48 for IPv6, the blocks providers route and
// balance within. "" stays "".
func addrNetwork(s string) string {
	a, err := netip.ParseAddr(s)
	if err != nil {
		return s
	}
	bits := 48
	if a.Is4() {
		bits = 24
	}
	p, err := a.Prefix(bits)
	if err != nil {
		return s
	}
	return p.String()
}

func stableHeaders(hs []Header) map[string]string {
	m := map[string]string{}
	for _, h := range hs {
		if _, volatile := allowedHeader(h.Name); !volatile {
			m[h.Name] = h.Value
		}
	}
	return m
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

func certLabel(f *ResponseFacts) string {
	s := "expires " + f.CertExpiresAt.UTC().Format("2006-01-02")
	if f.CertIssuer != "" {
		s += ", issued by " + f.CertIssuer
	}
	return s
}

// ms renders milliseconds for people: "180 ms", "2.4 s".
func ms(v int) string {
	if v < 1000 {
		return strconv.Itoa(v) + " ms"
	}
	return strconv.FormatFloat(float64(v)/1000, 'f', 1, 64) + " s"
}

// Bytes renders a size for people: "512 B", "1.2 KB", "40 KB", "3.1 MB".
func Bytes(n int64) string {
	switch {
	case n < 1000:
		return strconv.FormatInt(n, 10) + " B"
	case n < 10_000:
		return strconv.FormatFloat(float64(n)/1000, 'f', 1, 64) + " KB"
	case n < 1_000_000:
		return strconv.FormatInt(n/1000, 10) + " KB"
	}
	return strconv.FormatFloat(float64(n)/1_000_000, 'f', 1, 64) + " MB"
}
