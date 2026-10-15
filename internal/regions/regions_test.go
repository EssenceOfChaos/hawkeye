package regions

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	s, err := Parse("us-east:US East, eu-west , ap-south:Asia Pacific")
	if err != nil || s.Primary() != "us-east" || len(s.List()) != 3 || s.Name("eu-west") != "eu-west" || s.Name("ap-south") != "Asia Pacific" || s.Name("zz") != "zz" {
		t.Fatalf("%+v %v", s, err)
	}
	if d, _ := Parse("  "); d.Primary() != DefaultPrimary || !d.Has("primary") || len(d.List()) != 1 {
		t.Fatalf("%+v", d)
	}
	for _, bad := range []string{"US", "a", "ok,ok", "bad_code", "-x", "x-", "has space"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestEffective(t *testing.T) {
	s, _ := Parse("a1,b2,c3,d4")
	cases := []struct {
		chosen []string
		max    int
		want   []string
	}{
		{nil, 3, []string{"a1"}},
		{[]string{"c3"}, 3, []string{"c3"}},
		{[]string{"c3", "b2", "c3", "d4", "a1"}, 3, []string{"c3", "b2", "d4"}},
		{[]string{"zz", "b2"}, 3, []string{"b2"}},
		{[]string{"zz"}, 3, []string{"a1"}},
		{[]string{"b2", "c3"}, 1, []string{"b2"}},
	}
	for _, c := range cases {
		if got := s.Effective(c.chosen, c.max); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v max %d: got %v want %v", c.chosen, c.max, got, c.want)
		}
	}
}

func TestQuorumIsAMajority(t *testing.T) {
	for n, want := range map[int]int{1: 1, 2: 2, 3: 2, 4: 3, 5: 3} {
		if got := Quorum(n); got != want {
			t.Errorf("Quorum(%d) = %d, want %d", n, got, want)
		}
	}
}
