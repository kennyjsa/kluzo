package github

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// secretsEcho makes the server answer every request with an error that quotes
// the secrets, as a misbehaving proxy or a verbose API could.
func secretsEcho(status int, secrets ...string) func(http.ResponseWriter, *http.Request) bool {
	return func(w http.ResponseWriter, _ *http.Request) bool {
		writeJSON(w, status, map[string]string{"message": "rejected " + strings.Join(secrets, " and ")})
		return true
	}
}

// leaks reports every layer of err (its message, the message of the error
// wrapped around it again, and every error reachable through Unwrap) that
// contains one of the secrets.
func leaks(err error, secrets []string) []string {
	var found []string
	check := func(layer string, e error) {
		for _, s := range secrets {
			if strings.Contains(e.Error(), s) {
				found = append(found, layer+" has "+s[:min(len(s), 12)]+"...")
			}
		}
	}
	check("error", err)
	check("wrapped again", fmt.Errorf("supervisor: %w", err))
	check("joined", errors.Join(errors.New("other"), fmt.Errorf("again: %w", err)))
	for e, i := err, 0; e != nil; e, i = errors.Unwrap(e), i+1 {
		check(fmt.Sprintf("unwrap level %d", i), e)
	}
	return found
}

// calls is every operation of the adapter that talks to GitHub.
func calls(a *Adapter) map[string]func(context.Context) error {
	ign := func(_ any, err error) error { return err }
	return map[string]func(context.Context) error{
		"Milestone":       func(ctx context.Context) error { return ign(a.Milestone(ctx, "1")) },
		"MilestoneIssues": func(ctx context.Context) error { return ign(a.MilestoneIssues(ctx, "1")) },
		"Issue":           func(ctx context.Context) error { return ign(a.Issue(ctx, "1")) },
		"Dependencies":    func(ctx context.Context) error { return ign(a.Dependencies(ctx, "1")) },
		"SetDependencies": func(ctx context.Context) error {
			return a.SetDependencies(ctx, "1", []ports.Dependency{{FromRef: "2", ToRef: "1", Kind: contracts.EdgeBlocks}})
		},
		"CreateIssue": func(ctx context.Context) error {
			return ign(a.CreateIssue(ctx, ports.NewIssue{Title: "t", MilestoneRef: "1"}, "k"))
		},
		"SetStateLabel": func(ctx context.Context) error { return a.SetStateLabel(ctx, "1", "orch/ready") },
		"AddLabels":     func(ctx context.Context) error { return a.AddLabels(ctx, "1", []string{"bug"}) },
		"Comment":       func(ctx context.Context) error { return ign(a.Comment(ctx, "1", "b", "k")) },
		"CommentsSince": func(ctx context.Context) error { _, _, err := a.CommentsSince(ctx, "1", ""); return err },
		"Changes":       func(ctx context.Context) error { _, _, err := a.Changes(ctx, "1", "5"); return err },
		"Whoami":        func(ctx context.Context) error { return ign(a.Whoami(ctx)) },
		"EnsureBranch":  func(ctx context.Context) error { return ign(a.EnsureBranch(ctx, "b", "main")) },
		"BranchHead":    func(ctx context.Context) error { return ign(a.BranchHead(ctx, "main")) },
		"OpenPR": func(ctx context.Context) error {
			return ign(a.OpenPR(ctx, ports.NewPR{Title: "t", Head: "h", Base: "main"}, "k"))
		},
		"UpdatePR": func(ctx context.Context) error { return a.UpdatePR(ctx, "1", "body") },
		"PR":       func(ctx context.Context) error { return ign(a.PR(ctx, "1")) },
		"IsMerged": func(ctx context.Context) error { return ign(a.IsMerged(ctx, "1")) },
		"Checks":   func(ctx context.Context) error { return ign(a.Checks(ctx, strings.Repeat("a", 40), []string{"ci"})) },
	}
}

// Whatever layer of the adapter fails, the error it exposes carries neither the
// token nor the private key, not even after the caller wraps it again.
func TestErrorsNeverCarryTheCredentials(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	statuses := []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError}

	for _, kind := range []string{AuthToken, AuthGitHubApp} {
		h := newHarness(t)
		secret := h.f.token
		auth := Auth{Kind: AuthToken, Token: secret}
		if kind == AuthGitHubApp {
			secret = string(pemKey)
			auth = Auth{Kind: AuthGitHubApp, AppID: 4242, PrivateKey: pemKey}
			h.f.intercept = func(w http.ResponseWriter, r *http.Request) bool {
				switch r.URL.Path {
				case "/app":
					writeJSON(w, http.StatusOK, map[string]any{"slug": "kluzo-test"})
				case "/repos/acme/sandbox/installation":
					writeJSON(w, http.StatusOK, map[string]any{"id": 77})
				case "/app/installations/77/access_tokens":
					writeJSON(w, http.StatusCreated, map[string]any{"token": h.f.token, "expires_at": epoch.Add(time.Hour)})
				default:
					return false
				}
				return true
			}
		}
		a := h.adapter(t, auth)
		secrets := []string{secret, h.f.token}

		for _, status := range statuses {
			h.f.intercept = secretsEcho(status, secrets...)
			for name, call := range calls(a) {
				if kind == AuthGitHubApp && name == "Whoami" {
					continue // known since New, no request
				}
				t.Run(fmt.Sprintf("%s/%d/%s", kind, status, name), func(t *testing.T) {
					err := call(context.Background())
					if err == nil {
						t.Fatal("want an error")
					}
					if found := leaks(err, secrets); len(found) > 0 {
						t.Errorf("leak: %v\nerror: %v", found, err)
					}
				})
			}
		}
	}
}

// New fails with the secrets masked too: the App handshake is the first call.
func TestNewDoesNotLeakTheKeyWhenTheHandshakeFails(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	h := newHarness(t)
	h.f.intercept = secretsEcho(http.StatusUnauthorized, string(pemKey), h.f.token)
	_, err = New(context.Background(), Options{
		Repo: "acme/sandbox", Auth: Auth{Kind: AuthGitHubApp, AppID: 1, PrivateKey: pemKey},
		BaseURL: h.f.srv.URL, HTTPClient: h.f.srv.Client(),
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if found := leaks(err, []string{string(pemKey), h.f.token}); len(found) > 0 {
		t.Errorf("leak: %v\nerror: %v", found, err)
	}
}
