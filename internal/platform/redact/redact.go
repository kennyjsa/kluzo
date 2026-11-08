// Package redact removes secrets from text before it reaches a log, an error
// message, the journal, the web or the tracker (docs/architecture/security.md).
// It knows the secret values the process holds and the shapes of common tokens.
package redact

import (
	"regexp"
	"slices"
	"strings"
)

// Mask replaces every secret found.
const Mask = "[REDACTED]"

// minSecretLen keeps a short or empty value from masking half of every message.
const minSecretLen = 6

// tokenPatterns match credentials by shape: GitHub tokens, PEM private keys,
// JWTs and bearer headers.
var tokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?(?:-----END [A-Z ]*PRIVATE KEY-----|$)`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]*`),
	regexp.MustCompile(`(?i)\b(bearer|token)\s+[A-Za-z0-9._~+/=-]{12,}`),
}

// Redactor masks a fixed set of secret values plus the token shapes above.
// The zero value masks only the shapes.
type Redactor struct {
	secrets []string // longest first, so a secret that contains another is masked whole
}

// New returns a Redactor for the given secret values. Values shorter than a few
// characters are ignored.
func New(secrets ...string) *Redactor {
	var keep []string
	for _, s := range secrets {
		if len(s) >= minSecretLen && !slices.Contains(keep, s) {
			keep = append(keep, s)
		}
	}
	slices.SortFunc(keep, func(a, b string) int { return len(b) - len(a) })
	return &Redactor{secrets: keep}
}

// String returns s with the secrets masked.
func (r *Redactor) String(s string) string {
	for _, secret := range r.secrets {
		s = strings.ReplaceAll(s, secret, Mask)
	}
	for _, re := range tokenPatterns {
		s = re.ReplaceAllString(s, Mask)
	}
	return s
}

// Error returns an error whose message is err's with the secrets masked and
// that still unwraps to err, so errors.Is and errors.As keep working. A nil err
// stays nil.
func (r *Redactor) Error(err error) error {
	if err == nil {
		return nil
	}
	return &redacted{msg: r.String(err.Error()), err: err}
}

type redacted struct {
	msg string
	err error
}

func (e *redacted) Error() string { return e.msg }
func (e *redacted) Unwrap() error { return e.err }
