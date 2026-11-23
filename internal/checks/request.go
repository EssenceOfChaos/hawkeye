package checks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// Assertion types and JSON operators. Assertions run on the response body after the status check passes.
const (
	AssertBodyContains    = "body_contains"
	AssertBodyNotContains = "body_not_contains"
	AssertJSONPath        = "json_path"

	OpEquals    = "equals"
	OpNotEquals = "not_equals"
	OpContains  = "contains"
	OpExists    = "exists"
	OpNotExists = "not_exists"

	// Number comparisons: the value at the path must be a number (or a string holding one), compared with the rule's value.
	OpGreaterThan    = "greater_than"
	OpGreaterOrEqual = "greater_or_equal"
	OpLessThan       = "less_than"
	OpLessOrEqual    = "less_or_equal"
)

// NumberOperators are the JSON operators that compare numbers; their value must be a number.
var NumberOperators = []string{OpGreaterThan, OpGreaterOrEqual, OpLessThan, OpLessOrEqual}

// ParseNumber reads a rule's number, refusing NaN and infinities (which compare with nothing).
func ParseNumber(s string) (float64, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// Authentication types. The secret is sent as the Basic password, the Bearer token, or the header value.
const (
	AuthNone   = "none"
	AuthBasic  = "basic"
	AuthBearer = "bearer"
	AuthHeader = "header"
)

// MaxBodyBytes bounds how much of a response is read for assertions. Text assertions search this much;
// JSON assertions fail when the body is larger, because a truncated document cannot be parsed.
const MaxBodyBytes = 1 << 20

// Header is one custom request header.
type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Auth holds the credentials sent with a check. It is never serialized to API clients.
type Auth struct {
	Type       string
	Username   string // basic
	HeaderName string // header
	Secret     string
}

// Assertion is one condition on the response body.
type Assertion struct {
	Type     string `json:"type"`
	Path     string `json:"path,omitempty"`
	Operator string `json:"operator,omitempty"`
	Value    string `json:"value,omitempty"`
}

// pathStep is one step of a parsed JSON path: an object key, or an array index when key is empty.
type pathStep struct {
	key   string
	index int
}

// ValidatePath reports whether p is a JSON path that assertions can evaluate.
func ValidatePath(p string) error {
	_, err := parsePath(p)
	return err
}

// parsePath parses a dotted JSON path such as "data.items[0].status" (a leading "$." is allowed).
// Keys cannot contain dots or brackets.
func parsePath(p string) ([]pathStep, error) {
	p = strings.TrimPrefix(strings.TrimPrefix(p, "$"), ".")
	if p == "" {
		return nil, fmt.Errorf("path is empty")
	}
	var steps []pathStep
	for _, seg := range strings.Split(p, ".") {
		key, rest, _ := strings.Cut(seg, "[")
		if key == "" && rest == "" {
			return nil, fmt.Errorf("path has an empty segment")
		}
		if key != "" {
			if strings.ContainsAny(key, "]") {
				return nil, fmt.Errorf("unexpected ] in %q", seg)
			}
			steps = append(steps, pathStep{key: key})
		}
		if !strings.Contains(seg, "[") {
			continue
		}
		rest = "[" + rest
		for rest != "" {
			if rest[0] != '[' {
				return nil, fmt.Errorf("unexpected text after ] in %q", seg)
			}
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				return nil, fmt.Errorf("missing ] in %q", seg)
			}
			n, err := strconv.Atoi(rest[1:end])
			if err != nil || n < 0 {
				return nil, fmt.Errorf("array index in %q must be a non-negative number", seg)
			}
			steps = append(steps, pathStep{index: n})
			rest = rest[end+1:]
		}
	}
	return steps, nil
}

func lookup(doc any, steps []pathStep) (any, bool) {
	cur := doc
	for _, s := range steps {
		if s.key != "" {
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, false
			}
			if cur, ok = obj[s.key]; !ok {
				return nil, false
			}
			continue
		}
		arr, ok := cur.([]any)
		if !ok || s.index >= len(arr) {
			return nil, false
		}
		cur = arr[s.index]
	}
	return cur, true
}

// matches compares a JSON value with the expected text. Strings compare as text; numbers compare
// numerically; anything else compares by its compact JSON form (true, null, {"a":1}).
func matches(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return x == want
	case json.Number:
		got, err1 := x.Float64()
		exp, err2 := strconv.ParseFloat(strings.TrimSpace(want), 64)
		return err1 == nil && err2 == nil && got == exp
	}
	b, err := json.Marshal(v)
	if err != nil {
		return false
	}
	var w bytes.Buffer
	if json.Compact(&w, []byte(want)) != nil {
		return false
	}
	return bytes.Equal(b, w.Bytes())
}

func contains(v any, want string) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, want)
	case []any:
		for _, e := range x {
			if matches(e, want) {
				return true
			}
		}
	}
	return false
}

// numberOf reads a JSON value as a number: a JSON number, or a string that holds one (some APIs send counts as "512").
func numberOf(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		return ParseNumber(x.String())
	case string:
		return ParseNumber(x)
	}
	return 0, false
}

func compare(op string, got, want float64) bool {
	switch op {
	case OpGreaterThan:
		return got > want
	case OpGreaterOrEqual:
		return got >= want
	case OpLessThan:
		return got < want
	case OpLessOrEqual:
		return got <= want
	}
	return false
}

// body is a response body as assertions see it, parsed as JSON once, when the first JSON assertion needs it.
type body struct {
	raw       []byte
	truncated bool
	parsed    bool
	doc       any
	parseErr  string
}

func (b *body) json() (any, string) {
	if !b.parsed {
		b.parsed = true
		switch {
		case b.truncated:
			b.parseErr = "Response body is too large to check as JSON"
		default:
			dec := json.NewDecoder(bytes.NewReader(b.raw))
			dec.UseNumber()
			if err := dec.Decode(&b.doc); err != nil || dec.More() {
				b.parseErr = "Response body is not valid JSON"
			}
		}
	}
	return b.doc, b.parseErr
}

// evaluate runs assertions in order and returns a summary for the first failure, or "" when all pass.
// Summaries name the assertion, never response content, because they are stored and emailed.
func evaluate(as []Assertion, raw []byte, truncated bool) string {
	b := &body{raw: raw, truncated: truncated}
	for _, a := range as {
		if msg := evaluateOne(a, b); msg != "" {
			return msg
		}
	}
	return ""
}

// EvaluateEach runs every assertion, not only up to the first failure, and returns a summary per assertion ("" when it passes). Test requests
// use it to show which rules hold.
func EvaluateEach(as []Assertion, raw []byte, truncated bool) []string {
	b := &body{raw: raw, truncated: truncated}
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = truncate(evaluateOne(a, b), maxSummaryLen)
	}
	return out
}

func evaluateOne(a Assertion, b *body) string {
	switch a.Type {
	case AssertBodyContains:
		if !bytes.Contains(b.raw, []byte(a.Value)) {
			return fmt.Sprintf("The page does not contain %q", a.Value)
		}
	case AssertBodyNotContains:
		if bytes.Contains(b.raw, []byte(a.Value)) {
			return fmt.Sprintf("The page contains %q", a.Value)
		}
	case AssertJSONPath:
		doc, parseErr := b.json()
		if parseErr != "" {
			return parseErr
		}
		steps, err := parsePath(a.Path)
		if err != nil {
			return fmt.Sprintf("Invalid JSON path %q", a.Path)
		}
		v, found := lookup(doc, steps)
		if slices.Contains(NumberOperators, a.Operator) {
			return numberFailure(a, v, found)
		}
		var ok bool
		switch a.Operator {
		case OpExists:
			ok = found
		case OpNotExists:
			ok = !found
		case OpEquals:
			ok = found && matches(v, a.Value)
		case OpNotEquals:
			ok = found && !matches(v, a.Value)
		case OpContains:
			ok = found && contains(v, a.Value)
		}
		if !ok {
			return jsonFailure(a, found)
		}
	default:
		return fmt.Sprintf("Unknown assertion %q", a.Type)
	}
	return ""
}

// numberFailure checks a number comparison and describes a failure, or returns "" when it holds. As elsewhere, the summary never includes the
// value found, which is response content.
func numberFailure(a Assertion, v any, found bool) string {
	if !found {
		return fmt.Sprintf("JSON path %s not found", a.Path)
	}
	want, ok := ParseNumber(a.Value)
	if !ok {
		return fmt.Sprintf("JSON check on %s has no number to compare with", a.Path)
	}
	got, ok := numberOf(v)
	if !ok {
		return fmt.Sprintf("JSON path %s is not a number", a.Path)
	}
	if compare(a.Operator, got, want) {
		return ""
	}
	words := map[string]string{OpGreaterThan: "greater than", OpGreaterOrEqual: "at least", OpLessThan: "less than", OpLessOrEqual: "at most"}
	return fmt.Sprintf("JSON path %s is not %s %s", a.Path, words[a.Operator], strings.TrimSpace(a.Value))
}

func jsonFailure(a Assertion, found bool) string {
	switch {
	case a.Operator == OpNotExists:
		return fmt.Sprintf("JSON path %s exists", a.Path)
	case !found:
		return fmt.Sprintf("JSON path %s not found", a.Path)
	case a.Operator == OpEquals:
		return fmt.Sprintf("JSON path %s does not equal %q", a.Path, a.Value)
	case a.Operator == OpNotEquals:
		return fmt.Sprintf("JSON path %s equals %q", a.Path, a.Value)
	case a.Operator == OpContains:
		return fmt.Sprintf("JSON path %s does not contain %q", a.Path, a.Value)
	}
	return fmt.Sprintf("Unknown JSON operator %q", a.Operator)
}
