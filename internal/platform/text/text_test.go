package text

import (
	"testing"
	"unicode/utf8"
)

func TestClip(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		mark string
		want string
	}{
		{"fits", "abc", 3, Ellipsis, "abc"},
		{"empty", "", 3, Ellipsis, ""},
		{"cut", "abcdef", 4, Ellipsis, "abc…"},
		{"counts runes, not bytes", "ãéíõú", 3, Ellipsis, "ãé…"},
		{"only the mark fits", "abc", 1, Ellipsis, "…"},
		{"long mark", "abcdefghijklmnop", 15, "…(truncated)", "abc…(truncated)"},
		{"mark longer than max", "abcdefghijklmnop", 4, "…(truncated)", "…(tr"},
		{"no mark", "abcdef", 3, "", "abc"},
		{"zero", "abc", 0, Ellipsis, ""},
		{"negative", "abc", -1, Ellipsis, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Clip(c.in, c.max, c.mark)
			if got != c.want {
				t.Errorf("Clip(%q, %d, %q) = %q, want %q", c.in, c.max, c.mark, got, c.want)
			}
			if utf8.RuneCountInString(got) > max(c.max, 0) {
				t.Errorf("Clip(%q, %d) has %d runes", c.in, c.max, utf8.RuneCountInString(got))
			}
		})
	}
}
