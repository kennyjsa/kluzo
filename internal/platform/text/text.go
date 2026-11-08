// Package text holds the text helpers shared by the packages that put text
// from outside Kluzo in events, prompts and summaries.
package text

import "unicode/utf8"

// Ellipsis is the mark of the *_summary fields and the other short cuts.
const Ellipsis = "…"

// Clip cuts s to at most max characters (runes), mark included, ending the
// cut text with mark (Ellipsis, or the mark a contract asks for). A text that
// fits is returned as it is; when max cannot hold the mark, the mark is cut.
func Clip(s string, max int, mark string) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	if max < 1 {
		return ""
	}
	m := []rune(mark)
	keep := max - len(m)
	if keep < 0 {
		return string(m[:max])
	}
	return string([]rune(s)[:keep]) + mark
}
