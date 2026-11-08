package prompt

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Sanitize cleans text from outside Kluzo before it is put in an envelope
// (docs/contracts/prompt.md#envelope-de-dado-não-confiável): it removes the C0
// and C1 control characters except \n and \t, the bidi controls, the
// zero-width characters and the Unicode Tags block, replaces invalid UTF-8,
// and normalizes to NFC.
func Sanitize(s string) string {
	s = strings.ToValidUTF8(s, string(utf8.RuneError))
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20, r >= 0x7f && r <= 0x9f, // C0, DEL and C1
			r >= 0x202A && r <= 0x202E, r >= 0x2066 && r <= 0x2069, // bidi
			r >= 0x200B && r <= 0x200D, r == 0x2060, r == 0xFEFF, // zero-width
			r >= 0xE0000 && r <= 0xE007F: // Tags
			return -1
		}
		return r
	}, s)
	return norm.NFC.String(s)
}

// attribute makes a value safe for the header line of an envelope: sanitized,
// with no whitespace.
func attribute(v string) string {
	return strings.Join(strings.Fields(Sanitize(v)), "_")
}

func openMarker(nonce string) string  { return "<<<UNTRUSTED:" + nonce }
func closeMarker(nonce string) string { return "UNTRUSTED:" + nonce + ">>>" }

// envelope wraps sanitized text from outside Kluzo (or written by another
// role). It reports whether the content contains the nonce marker, which
// means the session must be recreated with another nonce.
func envelope(nonce, source, kind, author, text string) (out string, collided bool) {
	attrs := " source=" + attribute(source) + " kind=" + attribute(kind)
	if author != "" {
		attrs += " author=" + attribute(author)
	}
	body := strings.TrimRight(Sanitize(text), "\n")
	out = openMarker(nonce) + attrs + "\n" + body + "\n" + closeMarker(nonce)
	// "UNTRUSTED:<nonce>" is part of both the opening and the closing marker.
	return out, strings.Contains(attrs+"\n"+body, "UNTRUSTED:"+nonce)
}

// truncateChars cuts s to at most max characters, marking the cut.
func truncateChars(s string, max int) string {
	const mark = "…(truncated)"
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	keep := max - utf8.RuneCountInString(mark)
	if keep < 0 {
		keep = 0
	}
	return string([]rune(s)[:keep]) + mark
}

// clipLog keeps the first head and the last tail lines of a log, and at most
// maxChars characters in total. Where it cuts, it says how much is omitted and
// where the full log is.
func clipLog(log, fullPath string, head, tail, maxChars int) string {
	lines := strings.Split(strings.TrimRight(log, "\n"), "\n")
	where := ""
	if fullPath != "" {
		where = ", full log: " + fullPath
	}
	if len(lines) > head+tail {
		omitted := len(lines) - head - tail
		cut := fmt.Sprintf("… %d lines omitted%s …", omitted, where)
		lines = append(append(append([]string{}, lines[:head]...), cut), lines[len(lines)-tail:]...)
	}
	out := strings.Join(lines, "\n")
	if r := []rune(out); len(r) > maxChars {
		cut := "\n… characters omitted" + where + " …\n"
		budget := max(maxChars-utf8.RuneCountInString(cut), 0)
		first := budget / 3
		out = string(r[:first]) + cut + string(r[len(r)-(budget-first):])
	}
	return out
}
