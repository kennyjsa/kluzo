package github

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bradleyfalzon/ghinstallation/v2"
	gh "github.com/google/go-github/v93/github"

	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/redact"
	"github.com/kennyjsa/kluzo/internal/ports"
)

// Authentication kinds (tracker.auth.kind in kluzo.yaml).
const (
	AuthToken     = "token"
	AuthGitHubApp = "github_app"
)

const (
	pageSize           = 100
	defaultMaxWait     = 15 * time.Minute
	defaultHTTPTimeout = 30 * time.Second
)

// Auth holds the credentials, already read from the environment by the caller.
type Auth struct {
	Kind       string // AuthToken | AuthGitHubApp
	Token      string // AuthToken
	AppID      int64  // AuthGitHubApp
	PrivateKey []byte // AuthGitHubApp: the PEM of the App's private key
}

// Options configure New. Only Repo and Auth are required.
type Options struct {
	Repo string // "owner/name"
	Auth Auth

	// BaseURL is the API root (GitHub Enterprise, or a test server). Empty
	// means https://api.github.com/.
	BaseURL string
	// HTTPClient carries the requests (a test server's client). Empty means a
	// client with a timeout.
	HTTPClient *http.Client
	// Clock and Sleep are used to wait out rate limits; the defaults are the
	// system clock and a timer.
	Clock clock.Clock
	Sleep func(ctx context.Context, d time.Duration) error
	// MaxRateLimitWait is the longest pause the adapter accepts for a rate
	// limit before returning the refusal. Zero means 15 minutes.
	MaxRateLimitWait time.Duration
	Logger           *slog.Logger
}

// Adapter implements ports.Tracker, ports.SCM and ports.CI for one repository.
type Adapter struct {
	gh          *gh.Client
	owner, repo string
	redactor    *redact.Redactor

	mu    sync.Mutex
	login string // who the adapter acts as; set at once for an App, on demand for a token

	// installation are the permissions of the App installation, nil for a token.
	installation *gh.InstallationPermissions

	// token returns the credential git uses to fetch and push as the bot:
	// the token, or a fresh installation token of the App.
	token func(context.Context) (string, error)
}

var (
	_ ports.Tracker = (*Adapter)(nil)
	_ ports.SCM     = (*Adapter)(nil)
	_ ports.CI      = (*Adapter)(nil)
)

// New builds the adapter. With AuthGitHubApp it asks GitHub which installation
// of the App covers the repository, which needs the network.
func New(ctx context.Context, opts Options) (*Adapter, error) {
	owner, repo, ok := strings.Cut(opts.Repo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("github repo %q must look like owner/name", opts.Repo)
	}
	secrets, err := opts.Auth.secrets()
	if err != nil {
		return nil, err
	}
	a := &Adapter{owner: owner, repo: repo, redactor: redact.New(secrets...)}

	base := opts.HTTPClient
	if base == nil {
		base = &http.Client{Timeout: defaultHTTPTimeout}
	}
	apiRoot := strings.TrimRight(opts.BaseURL, "/")
	authed, err := a.authTransport(ctx, opts, base, apiRoot)
	if err != nil {
		return nil, err
	}
	limiter := &rateLimiter{
		base: authed, clock: opts.Clock, sleep: opts.Sleep, maxWait: opts.MaxRateLimitWait, log: opts.Logger,
	}
	if limiter.clock == nil {
		limiter.clock = clock.System{}
	}
	if limiter.sleep == nil {
		limiter.sleep = sleepContext
	}
	if limiter.maxWait == 0 {
		limiter.maxWait = defaultMaxWait
	}
	if limiter.log == nil {
		limiter.log = slog.Default()
	}

	if a.gh, err = a.newClient(&http.Client{Transport: limiter, Timeout: base.Timeout}, apiRoot); err != nil {
		return nil, err
	}
	return a, nil
}

// secrets validates the credentials and lists the values to mask.
func (au Auth) secrets() ([]string, error) {
	switch au.Kind {
	case AuthToken:
		if au.Token == "" {
			return nil, errors.New("github auth: token is empty")
		}
		return []string{au.Token}, nil
	case AuthGitHubApp:
		if au.AppID == 0 || len(au.PrivateKey) == 0 {
			return nil, errors.New("github auth: github_app needs an app id and a private key")
		}
		return []string{string(au.PrivateKey)}, nil
	}
	return nil, fmt.Errorf("github auth: unknown kind %q (want %s or %s)", au.Kind, AuthToken, AuthGitHubApp)
}

// authTransport returns the RoundTripper that signs the requests.
func (a *Adapter) authTransport(ctx context.Context, opts Options, base *http.Client, apiRoot string) (http.RoundTripper, error) {
	inner := base.Transport
	if inner == nil {
		inner = http.DefaultTransport
	}
	if opts.Auth.Kind == AuthToken {
		a.token = func(context.Context) (string, error) { return opts.Auth.Token, nil }
		return bearer{token: opts.Auth.Token, base: inner}, nil
	}

	apps, err := ghinstallation.NewAppsTransport(inner, opts.Auth.AppID, opts.Auth.PrivateKey)
	if err != nil {
		return nil, a.fail("read the app private key", err)
	}
	if apiRoot != "" {
		apps.BaseURL = apiRoot
	}
	appClient, err := a.newClient(&http.Client{Transport: apps, Timeout: base.Timeout}, apiRoot)
	if err != nil {
		return nil, err
	}
	self, _, err := appClient.Apps.Get(ctx, "")
	if err != nil {
		return nil, a.fail("read the app", err)
	}
	// An App acts as its bot user; its installation token cannot call /user.
	a.login = self.GetSlug() + "[bot]"
	inst, _, err := appClient.Apps.GetRepositoryInstallation(ctx, a.owner, a.repo)
	if err != nil {
		return nil, a.fail("find the app installation of "+a.owner+"/"+a.repo, err)
	}
	a.installation = inst.GetPermissions()
	if a.installation == nil {
		a.installation = &gh.InstallationPermissions{}
	}
	installation := ghinstallation.NewFromAppsTransport(apps, inst.GetID())
	a.token = func(ctx context.Context) (string, error) {
		tok, err := installation.Token(ctx)
		if err != nil {
			return "", a.fail("get an installation token", err)
		}
		return tok, nil
	}
	return installation, nil
}

// GitToken is the credential of the bot for git fetch and push over https
// (gitops.Remote): the token itself, or an installation token of the App,
// renewed before it expires. It is never given to an agent (security.md).
func (a *Adapter) GitToken(ctx context.Context) (string, error) { return a.token(ctx) }

// newClient builds a go-github client for httpClient, pointing at apiRoot when
// it is not empty.
func (a *Adapter) newClient(httpClient *http.Client, apiRoot string) (*gh.Client, error) {
	// The rateLimiter transport does the waiting, with the injected clock.
	opts := []gh.ClientOptionsFunc{gh.WithHTTPClient(httpClient), gh.WithDisableRateLimitCheck()}
	if apiRoot != "" {
		u, err := url.Parse(apiRoot + "/")
		if err != nil || u.Host == "" {
			return nil, a.fail("configure the API url", fmt.Errorf("invalid base url %q", apiRoot))
		}
		opts = append(opts, gh.WithURLs(new(u.String()), nil))
	}
	client, err := gh.NewClient(opts...)
	if err != nil {
		return nil, a.fail("configure the API client", err)
	}
	return client, nil
}

// bearer adds the token to every request.
type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// number parses a ref (an issue, milestone or PR number).
func number(kind, ref string) (int, error) {
	n, err := strconv.Atoi(ref)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s ref %q is not a number: %w", kind, ref, ports.ErrInvalid)
	}
	return n, nil
}

// pages calls fetch for page 1, 2… until the last one. fetch returns the next
// page number (0 at the end).
func pages(fetch func(page int) (next int, err error)) error {
	for page := 1; page != 0; {
		next, err := fetch(page)
		if err != nil {
			return err
		}
		page = next
	}
	return nil
}
