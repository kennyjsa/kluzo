package archtest

import "testing"

// TestRulesThemselves guards the rule definitions against silent breakage.
func TestRulesThemselves(t *testing.T) {
	cases := []struct {
		pkg, imp string
		want     bool // true = forbidden
	}{
		{internal("core"), internal("contracts"), false},
		{internal("core"), internal("ports"), false},
		{internal("core"), internal("store"), true},
		{internal("core"), internal("tracker/github"), true},
		{internal("core"), "net/http", true},
		{internal("core"), "os", true},
		{internal("core"), "math/rand", true},
		{internal("core"), "time", false},
		{internal("core"), "github.com/spf13/cobra", true},
		{internal("tracker/github"), internal("core"), true},
		{internal("tracker/github"), internal("ports"), false},
		{module + "/cmd/kluzo", internal("app"), false},
		{module + "/cmd/kluzo", internal("core"), true},
		{internal("web"), internal("store"), true},
		{internal("app"), "modernc.org/sqlite", true},
		{internal("store"), "modernc.org/sqlite", false},
		{internal("gitops"), "os/exec", true},
		{internal("platform/exec"), "os/exec", false},
		{internal("platform/ids"), internal("core"), true},
	}
	for _, c := range cases {
		got := false
		for _, r := range rules {
			if r.applies(c.pkg) && r.forbidden(c.pkg, c.imp) {
				got = true
			}
		}
		if got != c.want {
			t.Errorf("%s → %s: forbidden = %v, want %v", c.pkg, c.imp, got, c.want)
		}
	}
}
