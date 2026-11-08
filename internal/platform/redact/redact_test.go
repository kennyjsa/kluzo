package redact

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	r := New("s3cr3t-value", "", "abc")
	tests := []struct{ name, in, want string }{
		{"known secret", "auth failed for s3cr3t-value here", "auth failed for [REDACTED] here"},
		{"short value is not a secret", "abc stays", "abc stays"},
		{"github token", "using ghp_" + strings.Repeat("a", 36), "using [REDACTED]"},
		{"fine grained token", "x github_pat_" + strings.Repeat("A1_", 10), "x [REDACTED]"},
		{"bearer header", "Authorization: Bearer abcdefghijklmnop", "Authorization: [REDACTED]"},
		{"jwt", "jwt eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiIxMjMifQ.c2lnbmF0dXJl end", "jwt [REDACTED] end"},
		{"private key", "-----BEGIN RSA PRIVATE KEY-----\nMIIB\n-----END RSA PRIVATE KEY----- after", "[REDACTED] after"},
		{"truncated private key", "-----BEGIN PRIVATE KEY-----\nMIIB", "[REDACTED]"},
		{"nothing to hide", "issue #12 not found", "issue #12 not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.String(tt.in); got != tt.want {
				t.Errorf("String(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestLongestSecretIsMaskedWhole(t *testing.T) {
	r := New("secret", "secret-with-suffix")
	if got := r.String("x secret-with-suffix y"); got != "x [REDACTED] y" {
		t.Errorf("got %q", got)
	}
}

func TestErrorKeepsTheChain(t *testing.T) {
	sentinel := errors.New("not found")
	r := New("topsecret")
	err := r.Error(fmt.Errorf("GET with topsecret: %w", sentinel))
	if strings.Contains(err.Error(), "topsecret") {
		t.Errorf("message leaks the secret: %q", err)
	}
	if !errors.Is(err, sentinel) {
		t.Error("the redacted error must still unwrap to the original")
	}
	if r.Error(nil) != nil {
		t.Error("nil must stay nil")
	}
}
