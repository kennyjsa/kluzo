package github

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// The idempotency key of an effect is written at the end of the body it
// produced, in an HTML comment readers do not see:
//
//	<!-- kluzo:idem=KEY -->
const (
	markerOpen  = "<!-- kluzo:idem="
	markerClose = " -->"
)

var (
	markerAtEnd = regexp.MustCompile(`(?s)\s*<!-- kluzo:idem=([^\s>]+) -->\s*$`)
	validKey    = regexp.MustCompile(`^[A-Za-z0-9_.:/@+=-]{1,200}$`)
)

// checkKey rejects keys that could break out of the HTML comment.
func checkKey(key string) error {
	if !validKey.MatchString(key) || strings.Contains(key, "--") {
		return fmt.Errorf("idempotency key %q must be 1-200 characters of [A-Za-z0-9_.:/@+=-] without \"--\": %w", key, ports.ErrInvalid)
	}
	return nil
}

// withMarker appends the marker of key to body.
func withMarker(body, key string) string {
	m := markerOpen + key + markerClose
	if body == "" {
		return m
	}
	return body + "\n\n" + m
}

// splitMarker returns the body without its trailing marker, and the key the
// marker carried ("" when there is none).
func splitMarker(body string) (clean, key string) {
	m := markerAtEnd.FindStringSubmatchIndex(body)
	if m == nil {
		return body, ""
	}
	return body[:m[0]], body[m[2]:m[3]]
}
