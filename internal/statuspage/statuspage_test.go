package statuspage

import "testing"

func TestOverall(t *testing.T) {
	cases := []struct {
		states []string
		want   string
	}{
		{[]string{StateUp, StateUp}, Operational},
		{[]string{StateUp, StateDown}, Partial},
		{[]string{StateDown, StateDown}, Major},
		{[]string{StateDown}, Major},
		{[]string{StateMaintenance, StateMaintenance}, Maintenance},
		{[]string{StateMaintenance, StateUp}, Operational},
		{[]string{StateDown, StateMaintenance}, Partial},
		{[]string{StateUnknown, StateUp}, Operational},
		{[]string{StateUnknown}, Operational},
		{nil, Operational},
	}
	for _, c := range cases {
		if got := overall(c.states); got != c.want {
			t.Errorf("%v: got %s want %s", c.states, got, c.want)
		}
	}
}

func TestDayStatus(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	for pct, want := range map[*float64]string{nil: "none", f(100): "ok", f(99.5): "ok", f(99.4): "degraded", f(95): "degraded", f(94.9): "down", f(0): "down"} {
		if got := dayStatus(pct); got != want {
			t.Errorf("%v: got %s want %s", pct, got, want)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Config{Slug: " My-Status ", Title: " Acme ", Monitors: nil}
	got, err := validate(ok)
	if err != nil || got.Slug != "my-status" || got.Title != "Acme" || got.Monitors == nil {
		t.Fatalf("%+v %v", got, err)
	}
	for name, in := range map[string]Config{
		"short slug":      {Slug: "ab", Title: "x"},
		"leading hyphen":  {Slug: "-abc", Title: "x"},
		"trailing hyphen": {Slug: "abc-", Title: "x"},
		"underscore":      {Slug: "ab_c", Title: "x"},
		"space":           {Slug: "a b", Title: "x"},
		"slash":           {Slug: "a/b", Title: "x"},
		"no title":        {Slug: "abc", Title: " "},
		"publish empty":   {Slug: "abc", Title: "x", Published: true},
	} {
		if _, err := validate(in); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
