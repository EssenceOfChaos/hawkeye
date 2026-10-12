package plans

import (
	"strings"
	"testing"
)

func TestGrantsPaidAccess(t *testing.T) {
	want := map[string]bool{
		StatusActive: true, StatusTrialing: true, StatusPastDue: true,
		StatusCanceled: false, StatusIncomplete: false, "": false, "something_new": false,
	}
	for status, w := range want {
		if got := GrantsPaidAccess(status); got != w {
			t.Errorf("%q: got %v want %v", status, got, w)
		}
	}
}

func TestLimitMessages(t *testing.T) {
	free := Plan{Name: "Free"}
	at := (&LimitError{Kind: LimitMonitors, Limit: 5, Used: 5, Plan: free}).Message()
	if !strings.Contains(at, "up to 5 monitors") {
		t.Errorf("at-limit message: %s", at)
	}
	over := (&LimitError{Kind: LimitMonitors, Limit: 5, Used: 8, Plan: free}).Message()
	if !strings.Contains(over, "delete 3") || !strings.Contains(over, "untouched") {
		t.Errorf("over-limit message must explain how to recover: %s", over)
	}
}
