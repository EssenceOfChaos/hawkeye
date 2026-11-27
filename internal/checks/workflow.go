package checks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Multi-step API checks ("workflows", docs/plan-synthetic-api.md). A workflow runs its steps in order, each an ordinary check (same SSRF-safe
// client, authentication, expected status and assertions), and passes values between them: a step can extract a value from its response, and
// later steps use it, or one of the workflow's secrets, as {{name}} or {{secret.name}} in their address, headers, body and authentication.
// Substitution is plain text, with no expressions. The run stops at the first failing step, and that step's outcome is the monitor's.

// CatWorkflowConfig is a workflow that can't run as written (a variable that was never set, or a value to extract that isn't in the response),
// as opposed to a service that is failing. It confirms an outage like any other failure, but the summary says the test needs fixing.
const CatWorkflowConfig = "workflow_config"

// Extraction sources.
const (
	ExtractJSONPath = "json_path"
	ExtractHeader   = "header"
	ExtractCookie   = "cookie"
)

const (
	// MaxWorkflowSteps is the most steps any plan allows.
	MaxWorkflowSteps = 10
	// WorkflowTimeout bounds a whole run, whatever the steps' own timeouts.
	WorkflowTimeout = 60 * time.Second
	// MaxExtractedBytes bounds one extracted value.
	MaxExtractedBytes = 4096
	// MaxExtractionsPerStep and MaxWorkflowSecrets keep a workflow small enough to read.
	MaxExtractionsPerStep = 10
	MaxWorkflowSecrets    = 20
	// minRedactLen: values shorter than this are not hidden in summaries (hiding "1" or "ok" everywhere would garble them).
	minRedactLen = 4
	hidden       = "[hidden]"
)

// NamePattern is what variable and secret names look like.
var NamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,39}$`)

// placeholder matches {{name}} and {{secret.name}}, with optional spaces inside the braces.
var placeholder = regexp.MustCompile(`\{\{\s*(secret\.)?([A-Za-z_][A-Za-z0-9_]{0,39})\s*\}\}`)

// Extraction saves a value from a step's response under Name: a JSON path in the body, a response header, or a cookie the response set.
type Extraction struct {
	Name string `json:"name"`
	From string `json:"from"`
	Path string `json:"path"` // the JSON path, header name or cookie name
}

// Step is one request in a workflow. Its Target's URL, header values, body and authentication may contain placeholders.
type Step struct {
	Name        string
	Target      Target
	Extractions []Extraction
}

// Workflow is everything a run needs. Secrets are decrypted values; they never leave the run except as "[hidden]".
type Workflow struct {
	Steps   []Step
	Secrets map[string]string
	Cookies bool // keep cookies between steps, in a jar that lasts one run
	// CaptureBody keeps the start of the failing step's response body in Result.Excerpt (Target.CaptureBody), with every secret and saved
	// value hidden.
	CaptureBody bool
}

// StepOutcome is what a run records about one step: never response content, header values or variable values.
type StepOutcome struct {
	Name           string `json:"name"`
	Success        bool   `json:"success"`
	StatusCode     *int   `json:"status_code"`
	ResponseTimeMs *int   `json:"response_time_ms"`
	// Timings is where this step's time went. A run's own Result has none: a sum across steps would mix up different hosts.
	Timings       *Timings `json:"timings,omitempty"`
	ErrorCategory string   `json:"error_category,omitempty"`
	ErrorSummary  string   `json:"error_summary,omitempty"`
	// Facts is what the step saw of its response, for failure snapshots: never stored with the result (hence no JSON), with secrets and saved
	// values hidden.
	Facts *ResponseFacts `json:"-"`
}

// RunWorkflow runs w and returns the monitor's result: the failing step's (named in ErrorSummary), or the last step's when all passed, with
// ResponseTimeMs the total of the steps' times, and Steps listing the steps that ran, in order. As with Check, the error is non-nil only when ctx was canceled; every failure of the service or the workflow is a Result.
func (c *Checker) RunWorkflow(ctx context.Context, w Workflow) (Result, error) {
	res, _, err := c.runWorkflow(ctx, w, false)
	return res, err
}

// MaxStepPreviewBytes bounds the response body a test run shows per step (a run has up to MaxWorkflowSteps of them).
const MaxStepPreviewBytes = 64 << 10

// TryWorkflow runs w like RunWorkflow and also returns, for each step that got a response, what a person testing it sees: the body (up to
// MaxStepPreviewBytes) and every check's outcome. Every secret and saved value is replaced with "[hidden]" in them, with the values known at the
// end of the run, so a token a step returns and saves is hidden in that step's own body too. Steps without a response have a nil entry; steps
// that did not run have none. For the app's test runs only: assistants (MCP) get outcomes only.
func (c *Checker) TryWorkflow(ctx context.Context, w Workflow) (Result, []*Preview, error) {
	return c.runWorkflow(ctx, w, true)
}

func (c *Checker) runWorkflow(ctx context.Context, w Workflow, preview bool) (Result, []*Preview, error) {
	start := c.now()
	deadline := start.Add(WorkflowTimeout)
	out := Result{CheckedAt: start, Steps: []StepOutcome{}, FailedStep: -1}
	vars := map[string]string{}
	red := newRedactor(w.Secrets)
	var jar http.CookieJar
	if w.Cookies {
		jar, _ = cookiejar.New(nil) // a nil option list never fails
	}
	total, timed := 0, false // timed: some step got a response, so the run has a time (even 0 ms)
	// What each step's response looked like, for a test run's previews: redacted once the run is over, when every saved value is known.
	type seen struct {
		resp       *response
		assertions []Assertion
	}
	var bodies []*seen
	previews := func() []*Preview {
		if !preview {
			return nil
		}
		out := make([]*Preview, len(bodies))
		for i, b := range bodies {
			if b != nil {
				out[i] = red.preview(b.resp, b.assertions)
			}
		}
		return out
	}

	fail := func(i int, st Step, step StepOutcome, category, summary string) (Result, []*Preview, error) {
		step.Success, step.ErrorCategory, step.ErrorSummary = false, category, red.clean(summary)
		out.Steps = append(out.Steps, step)
		out.Success, out.ErrorCategory = false, category
		out.ErrorSummary = truncate(fmt.Sprintf("Step %d (%s): %s", i+1, red.clean(st.Name), step.ErrorSummary), maxSummaryLen)
		out.StatusCode = step.StatusCode
		out.Facts, out.FailedStep = step.Facts, i
		out.FinishedAt = c.now()
		if timed {
			t := total
			out.ResponseTimeMs = &t
		}
		return out, previews(), nil
	}

	for i, st := range w.Steps {
		step := StepOutcome{Name: red.clean(st.Name)}
		t, err := resolve(st.Target, vars, w.Secrets)
		if err != nil {
			return fail(i, st, step, CatWorkflowConfig, err.Error())
		}
		left := deadline.Sub(c.now())
		if left <= 0 {
			return fail(i, st, step, CatTimeout, fmt.Sprintf("The run took longer than %d seconds", int(WorkflowTimeout/time.Second)))
		}
		t.Timeout = min(t.Timeout, left)
		t.CaptureBody = w.CaptureBody
		res, resp, err := c.run(ctx, t, jar, preview || len(st.Extractions) > 0)
		if err != nil {
			return Result{}, nil, err // shutting down
		}
		if preview {
			var b *seen
			if resp != nil {
				b = &seen{resp: resp, assertions: t.Assertions}
			}
			bodies = append(bodies, b)
		}
		if i == 0 {
			out.CertExpiresAt = res.CertExpiresAt // the first step's host is the one the workflow is about
		}
		step.StatusCode, step.ResponseTimeMs, step.Timings, step.Facts = res.StatusCode, res.ResponseTimeMs, res.Timings, red.facts(res.Facts)
		if e := res.Excerpt; e != nil {
			out.Excerpt = &Excerpt{Text: red.clean(e.Text), Truncated: e.Truncated, Binary: e.Binary} // only a failing step has one
		}
		if res.ResponseTimeMs != nil {
			total, timed = total+*res.ResponseTimeMs, true
		}
		out.FinalURL = red.clean(res.FinalURL)
		if !res.Success {
			return fail(i, st, step, res.ErrorCategory, res.ErrorSummary)
		}
		for _, x := range st.Extractions {
			v, err := extract(x, resp)
			if err != nil {
				return fail(i, st, step, CatWorkflowConfig, err.Error())
			}
			vars[x.Name] = v
			red.add(v)
		}
		step.Success = true
		out.Steps = append(out.Steps, step)
		out.StatusCode = res.StatusCode
	}
	out.Success = true
	out.ResponseTimeMs = &total
	out.FinishedAt = c.now()
	return out, previews(), nil
}

// resolve fills the placeholders in t's address, header values, body and authentication.
func resolve(t Target, vars, secrets map[string]string) (Target, error) {
	var missing error
	fill := func(s string, escape func(string) string) string {
		return placeholder.ReplaceAllStringFunc(s, func(m string) string {
			p := placeholder.FindStringSubmatch(m)
			var v string
			var ok bool
			if p[1] != "" {
				v, ok = secrets[p[2]]
				if !ok && missing == nil {
					missing = fmt.Errorf("the secret %s is not set", p[2])
				}
			} else {
				v, ok = vars[p[2]]
				if !ok && missing == nil {
					missing = fmt.Errorf("{{%s}} has no value: no earlier step extracts it", p[2])
				}
			}
			return escape(v)
		})
	}
	raw := func(s string) string { return s }
	noNewlines := func(where string) func(string) string {
		return func(s string) string {
			if strings.ContainsAny(s, "\r\n") && missing == nil {
				missing = fmt.Errorf("a value used in %s contains a line break", where)
			}
			return s
		}
	}

	out := t
	out.URL = fill(t.URL, urlEscape)
	out.Headers = make([]Header, len(t.Headers))
	for i, h := range t.Headers {
		out.Headers[i] = Header{Name: h.Name, Value: fill(h.Value, noNewlines("a header"))}
	}
	if jsonBody(t.Body) {
		out.Body = fill(t.Body, jsonEscape)
	} else {
		out.Body = fill(t.Body, raw)
	}
	out.Auth.Username = fill(t.Auth.Username, noNewlines("authentication"))
	out.Auth.Secret = fill(t.Auth.Secret, noNewlines("authentication"))
	if missing != nil {
		return Target{}, missing
	}
	return out, nil
}

// jsonBody reports whether a body is JSON, so values put into it are escaped as JSON string content. Write "{{token}}" inside quotes for a string.
func jsonBody(b string) bool {
	b = strings.TrimSpace(b)
	return strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[")
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s) // a string always marshals
	return string(b[1 : len(b)-1])
}

// urlEscape makes a value safe anywhere in an address: in the path or a query value. A value can't change which host a step reaches, and a
// host is checked by the SSRF-safe client after substitution in any case.
func urlEscape(s string) string { return strings.ReplaceAll(url.QueryEscape(s), "+", "%20") }

var errNotInResponse = errors.New("not in the response")

// extract reads one value from a response. Strings are taken as they are; numbers, true/false and null as their JSON text; objects and arrays
// as compact JSON.
func extract(x Extraction, r *response) (string, error) {
	var v string
	switch x.From {
	case ExtractHeader:
		v = r.header.Get(x.Path)
		if v == "" {
			return "", fmt.Errorf("the response has no %s header to save as %s", x.Path, x.Name)
		}
	case ExtractCookie:
		found := false
		for _, ck := range r.cookies {
			if ck.Name == x.Path {
				v, found = ck.Value, true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("the response sets no %s cookie to save as %s", x.Path, x.Name)
		}
	case ExtractJSONPath:
		if r.truncated {
			return "", fmt.Errorf("the response is too large to read %s from", x.Path)
		}
		var doc any
		dec := json.NewDecoder(bytes.NewReader(r.body))
		dec.UseNumber()
		if err := dec.Decode(&doc); err != nil || dec.More() {
			return "", fmt.Errorf("the response is not JSON, so %s can't be read from it", x.Path)
		}
		steps, err := parsePath(x.Path)
		if err != nil {
			return "", fmt.Errorf("invalid JSON path %q", x.Path)
		}
		got, ok := lookup(doc, steps)
		if !ok {
			return "", fmt.Errorf("JSON path %s %w", x.Path, errNotInResponse)
		}
		switch g := got.(type) {
		case string:
			v = g
		case json.Number:
			v = g.String()
		default:
			b, _ := json.Marshal(g)
			v = string(b)
		}
	default:
		return "", fmt.Errorf("unknown extraction source %q", x.From)
	}
	if len(v) > MaxExtractedBytes {
		return "", fmt.Errorf("%s is longer than %d KB", x.Name, MaxExtractedBytes/1024)
	}
	return v, nil
}

// redactor hides secret and extracted values in anything a run stores or alerts on, longest first so a value containing another is hidden whole.
type redactor struct{ values []string }

func newRedactor(secrets map[string]string) *redactor {
	r := &redactor{}
	for _, v := range secrets {
		r.add(v)
	}
	return r
}

func (r *redactor) add(v string) {
	if len(v) < minRedactLen {
		return
	}
	r.values = append(r.values, v)
	// URL-escaped and JSON-escaped forms reach summaries and addresses too.
	for _, alt := range []string{urlEscape(v), jsonEscape(v), url.PathEscape(v)} {
		if alt != v {
			r.values = append(r.values, alt)
		}
	}
	sort.Slice(r.values, func(i, j int) bool { return len(r.values[i]) > len(r.values[j]) })
}

// facts is f with secrets and saved values hidden in its addresses and header values (nil stays nil).
func (r *redactor) facts(f *ResponseFacts) *ResponseFacts {
	if f == nil {
		return nil
	}
	c := *f
	c.FinalURL = r.clean(c.FinalURL)
	c.Headers = make([]Header, len(f.Headers))
	for i, h := range f.Headers {
		c.Headers[i] = Header{Name: h.Name, Value: r.clean(h.Value)}
	}
	return &c
}

func (r *redactor) clean(s string) string {
	for _, v := range r.values {
		s = strings.ReplaceAll(s, v, hidden)
	}
	return s
}

// preview is a step's response as a test run shows it: every check's outcome on the real body, then the body cut to MaxStepPreviewBytes with
// every secret and saved value hidden. A JSON body is redacted value by value, so it stays valid JSON with its keys in order: a string holding a
// value has it replaced, and a number equal to one becomes the string "[hidden]". Other text is redacted as text.
func (r *redactor) preview(resp *response, assertions []Assertion) *Preview {
	p := &Preview{Checks: EvaluateEach(assertions, resp.body, resp.truncated)}
	for i, msg := range p.Checks {
		p.Checks[i] = r.clean(msg)
	}
	body := resp.body
	if !utf8.Valid(body) {
		p.Binary = true
		return p
	}
	if redacted, ok := r.cleanJSON(body); ok && !resp.truncated {
		body = redacted
	} else {
		body = []byte(r.clean(string(body)))
	}
	shown := body[:validPrefix(body, MaxStepPreviewBytes)]
	p.Body, p.Truncated = string(shown), len(shown) < len(body) || resp.truncated
	return p
}

// cleanJSON rewrites a JSON document with every secret and saved value hidden, keeping its keys in order. ok is false when b isn't one JSON value.
func (r *redactor) cleanJSON(b []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var out bytes.Buffer
	// Per open container: whether it is an object, and how many tokens it has had (in an object, even counts are keys).
	type level struct {
		object bool
		n      int
	}
	var stack []level
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false) // shown as it came, <b> and & included
	str := func(s string) {
		_ = enc.Encode(r.clean(s))  // a string always encodes
		out.Truncate(out.Len() - 1) // Encode ends with a newline
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		if d, ok := tok.(json.Delim); ok && (d == '}' || d == ']') {
			stack = stack[:len(stack)-1]
			out.WriteRune(rune(d))
			continue
		}
		if n := len(stack); n > 0 {
			top := &stack[n-1]
			switch {
			case top.object && top.n%2 == 1:
				out.WriteByte(':')
			case top.n > 0:
				out.WriteByte(',')
			}
			top.n++
		} else if out.Len() > 0 {
			return nil, false // a second top-level value
		}
		switch v := tok.(type) {
		case json.Delim:
			stack = append(stack, level{object: v == '{'})
			out.WriteRune(rune(v))
		case string:
			str(v)
		case json.Number:
			if r.clean(v.String()) != v.String() {
				str(hidden)
			} else {
				out.WriteString(v.String())
			}
		case bool:
			out.WriteString(strconv.FormatBool(v))
		case nil:
			out.WriteString("null")
		}
	}
	return out.Bytes(), out.Len() > 0 && len(stack) == 0
}

// ValidateExtraction checks one extraction's settings (the API uses it before saving).
func ValidateExtraction(x Extraction) error {
	if !NamePattern.MatchString(x.Name) {
		return errors.New("name must start with a letter or _, and contain only letters, digits and _ (up to 40)")
	}
	switch x.From {
	case ExtractJSONPath:
		return ValidatePath(x.Path)
	case ExtractHeader, ExtractCookie:
		if strings.TrimSpace(x.Path) == "" || len(x.Path) > 200 || strings.ContainsAny(x.Path, "\r\n:; ") {
			return errors.New("give the header or cookie name")
		}
		return nil
	}
	return errors.New(`from must be "json_path", "header" or "cookie"`)
}

// FillPlaceholders replaces every placeholder in s with v (the API uses it to check what an address looks like whatever the values are).
func FillPlaceholders(s, v string) string { return placeholder.ReplaceAllString(s, v) }

// Placeholders lists the names a text uses: variables, and secrets prefixed "secret.". The API uses it to check that every name will have a value.
func Placeholders(s string) []string {
	var out []string
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1]+m[2])
	}
	return out
}
