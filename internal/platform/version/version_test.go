package version

import "testing"

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		sign int
	}{{"2.1.295", "2.1.295", 0}, {"2.1.319", "2.1.295", 1}, {"2.1.99", "2.1.295", -1}, {"2.2.0", "2.1.999", 1}, {"3.0", "2.9.9", 1}, {"2.38", "2.38.0", 0}} {
		got := Compare(c.a, c.b)
		if (got > 0) != (c.sign > 0) || (got < 0) != (c.sign < 0) {
			t.Errorf("Compare(%s, %s) = %d, want sign %d", c.a, c.b, got, c.sign)
		}
	}
}

func TestFind(t *testing.T) {
	for out, want := range map[string]string{"git version 2.43.0\n": "2.43.0", "2.1.319 (Claude Code)": "2.1.319", "git version 2.39": "2.39", "none": ""} {
		if got := Find(out); got != want {
			t.Errorf("Find(%q) = %q, want %q", out, got, want)
		}
	}
}
