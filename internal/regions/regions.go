// Package regions describes the locations checks can run from.
//
// A region is just a name that workers and monitors agree on: a worker started with WORKER_REGION=eu-west only runs jobs
// queued for eu-west, and the scheduler queues one job per region a monitor is set to use. The first configured region is the
// primary: it is where monitors with no regions chosen run, so a single-region installation needs no configuration at all.
package regions

import (
	"fmt"
	"regexp"
	"strings"
)

// DefaultPrimary is the code of the only region when none is configured.
const DefaultPrimary = "primary"

var (
	codeRE    = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,18}[a-z0-9]$`)
	countryRE = regexp.MustCompile(`^[A-Z]{2}$`)
)

type Region struct {
	Code string `json:"code"`
	Name string `json:"name"`
	// Country is the ISO 3166-1 alpha-2 code of where the location is (US, NL), for the flag shown beside its name; empty when not given.
	Country string `json:"country,omitempty"`
}

// Set is the configured list of regions, primary first. The zero value is not usable; use Parse or Default.
type Set struct{ list []Region }

// Default is a single region, "primary".
func Default() Set { return Set{list: []Region{{Code: DefaultPrimary, Name: "Primary"}}} }

// Parse reads "code[:Display name[:CC]],…", CC being the location's two-letter country code. An empty string gives Default.
func Parse(s string) (Set, error) {
	if strings.TrimSpace(s) == "" {
		return Default(), nil
	}
	var out Set
	seen := map[string]bool{}
	for _, part := range strings.Split(s, ",") {
		code, rest, _ := strings.Cut(strings.TrimSpace(part), ":")
		name, country, _ := strings.Cut(rest, ":")
		code, name, country = strings.TrimSpace(code), strings.TrimSpace(name), strings.ToUpper(strings.TrimSpace(country))
		if !codeRE.MatchString(code) {
			return Set{}, fmt.Errorf("region %q: use 2 to 20 lowercase letters, digits or hyphens", code)
		}
		if country != "" && !countryRE.MatchString(country) {
			return Set{}, fmt.Errorf("region %q: the country must be a two-letter code such as US or NL", code)
		}
		if seen[code] {
			return Set{}, fmt.Errorf("region %q is listed twice", code)
		}
		seen[code] = true
		if name == "" {
			name = code
		}
		out.list = append(out.list, Region{Code: code, Name: name, Country: country})
	}
	return out, nil
}

func (s Set) List() []Region { return append([]Region(nil), s.list...) }

// Primary is the code of the first region.
func (s Set) Primary() string {
	if len(s.list) == 0 {
		return DefaultPrimary
	}
	return s.list[0].Code
}

func (s Set) Has(code string) bool {
	for _, r := range s.list {
		if r.Code == code {
			return true
		}
	}
	return len(s.list) == 0 && code == DefaultPrimary
}

// Name is the display name for a code, or the code itself when it is unknown.
func (s Set) Name(code string) string {
	for _, r := range s.list {
		if r.Code == code {
			return r.Name
		}
	}
	return code
}

// Country is the two-letter country code for a code, or "" when it is unknown or was not given.
func (s Set) Country(code string) string {
	for _, r := range s.list {
		if r.Code == code {
			return r.Country
		}
	}
	return ""
}

// Effective is the regions a monitor actually runs in: the ones it chose that still exist, without repeats and in the order chosen,
// at most max of them. A monitor that chose none (or none that exist any more) runs in the primary region.
func (s Set) Effective(chosen []string, max int) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range chosen {
		if len(out) >= max {
			break
		}
		if s.Has(c) && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		out = []string{s.Primary()}
	}
	return out
}

// Quorum is how many of n regions must agree that a monitor is down before it counts as down: a majority, so one location having
// a network problem of its own never raises an alert. With one region it is that region; with two, both must agree.
func Quorum(n int) int { return n/2 + 1 }
