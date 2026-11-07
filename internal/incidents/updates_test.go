package incidents

import (
	"strings"
	"testing"
)

func TestCleanMessage(t *testing.T) {
	for in, want := range map[string]string{
		"  Investigating.  ":          "Investigating.",
		"line one\r\nline two":        "line one\nline two",
		"old mac\rline":               "old mac\nline",
		"tab\tkept":                   "tab\tkept",
		"<script>stays text</script>": "<script>stays text</script>", // shown as text by the page, never run
	} {
		got, err := CleanMessage(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for name, in := range map[string]string{
		"empty":          "",
		"only spaces":    " \n\t ",
		"too long":       strings.Repeat("x", MaxUpdateLength+1),
		"bell":           "a\x07",
		"null":           "a\x00",
		"right-to-left":  "invoice\u202Etxt.exe",
		"line separator": "a b",
		"isolate":        "a\u2066b",
	} {
		if _, err := CleanMessage(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := CleanMessage(strings.Repeat("é", MaxUpdateLength)); err != nil {
		t.Errorf("the limit counts characters, not bytes: %v", err)
	}
}
