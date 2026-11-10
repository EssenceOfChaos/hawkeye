package audit

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The database refuses actions that don't match this (migrations/000037_audit_log.up.sql).
var actionPattern = regexp.MustCompile(`^[a-z_]+\.[a-z_]+$`)

func TestEveryActionIsWellFormedAndInExactlyOneLog(t *testing.T) {
	seen := map[string]string{}
	for log, list := range map[string][]string{"account": AccountActions, "organization": OrganizationActions} {
		for _, a := range list {
			if !actionPattern.MatchString(a) {
				t.Errorf("%q would be refused by the database", a)
			}
			if other, dup := seen[a]; dup {
				t.Errorf("%q is in both the %s and the %s log", a, other, log)
			}
			seen[a] = log
		}
	}
	for _, a := range AccountActions {
		if Category(a) != "account" {
			t.Errorf("account action %q should be in the account category", a)
		}
	}
	for _, c := range OrganizationCategories {
		if c == "account" {
			t.Error("an organization action uses the account category")
		}
	}
}

func TestParseQuery(t *testing.T) {
	q, err := ParseQuery("42", "10", "monitor")
	if err != nil || q.Before != 42 || q.Limit != 10 || q.Category != "monitor" {
		t.Fatalf("got %+v, %v", q, err)
	}
	for _, bad := range [][2]string{{"0", ""}, {"-1", ""}, {"x", ""}, {"", "0"}, {"", "201"}, {"", "x"}} {
		if _, err := ParseQuery(bad[0], bad[1], ""); err == nil {
			t.Errorf("before=%q limit=%q accepted", bad[0], bad[1])
		}
	}
}

// The web app shows each event as a sentence (web/src/securityLog.ts). An action with no sentence there would be shown as its raw name.
func TestEveryActionHasASentence(t *testing.T) {
	raw, err := os.ReadFile("../../web/src/securityLog.ts")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, a := range append(append([]string{}, AccountActions...), OrganizationActions...) {
		if !strings.Contains(src, "'"+a+"':") {
			t.Errorf("web/src/securityLog.ts has no sentence for %q", a)
		}
	}
	for _, c := range append(append([]string{}, AccountCategories...), OrganizationCategories...) {
		if !regexp.MustCompile(`(?m)^\s+` + c + `: '`).MatchString(src) {
			t.Errorf("web/src/securityLog.ts has no name for the category %q", c)
		}
	}
}
