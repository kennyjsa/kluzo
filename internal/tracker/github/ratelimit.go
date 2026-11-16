package github

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/kennyjsa/kluzo/internal/platform/clock"
)

const (
	maxRateLimitRetries = 3
	// A secondary limit without Retry-After asks for a pause of at least this.
	rateLimitPad = time.Second
)

// rateLimiter is an http.RoundTripper that respects GitHub's rate limits: when
// the quota is spent it holds requests until the reset, and a request refused
// for rate limiting is repeated after the time GitHub asks for. It gives up
// (returning the refusal, which go-github turns into a RateLimitError) when the
// wait would exceed maxWait. The core never sees any of this.
type rateLimiter struct {
	base    http.RoundTripper
	clock   clock.Clock
	sleep   func(ctx context.Context, d time.Duration) error
	maxWait time.Duration
	log     *slog.Logger

	mu      sync.Mutex
	resetAt time.Time // when the exhausted quota comes back
}

// RoundTrip implements http.RoundTripper.
func (r *rateLimiter) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	for attempt := 0; ; attempt++ {
		if d := r.untilReset(); d > 0 && d <= r.maxWait {
			if err := r.pause(ctx, d, "quota exhausted"); err != nil {
				return nil, err
			}
		}
		resp, err := r.base.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		wait, limited := r.observe(resp)
		if !limited || attempt >= maxRateLimitRetries || wait > r.maxWait || !rewind(req) {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if err := r.pause(ctx, wait, "request refused"); err != nil {
			return nil, err
		}
		r.clearReset() // we have waited for it
	}
}

func (r *rateLimiter) pause(ctx context.Context, d time.Duration, why string) error {
	r.log.Info("github rate limit: waiting", "reason", why, "wait", d.String())
	return r.sleep(ctx, d)
}

func (r *rateLimiter) clearReset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resetAt = time.Time{}
}

func (r *rateLimiter) untilReset() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resetAt.Sub(r.clock.Now())
}

// observe records an exhausted quota and reports whether the response is a
// rate-limit refusal and how long to wait before repeating the request.
func (r *rateLimiter) observe(resp *http.Response) (wait time.Duration, limited bool) {
	now := r.clock.Now()
	var reset time.Time
	if sec, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		reset = time.Unix(sec, 0)
	}
	exhausted := resp.Header.Get("X-RateLimit-Remaining") == "0" && !reset.IsZero()
	r.mu.Lock()
	if exhausted {
		r.resetAt = reset.Add(rateLimitPad)
	} else {
		r.resetAt = time.Time{}
	}
	r.mu.Unlock()

	if resp.StatusCode != http.StatusForbidden && resp.StatusCode != http.StatusTooManyRequests {
		return 0, false
	}
	if sec, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
		return time.Duration(sec)*time.Second + rateLimitPad, true
	}
	if exhausted {
		return max(reset.Sub(now), 0) + rateLimitPad, true
	}
	return 0, false // a plain 403: permissions, not limits
}

// rewind resets the request body so the request can be sent again.
func rewind(req *http.Request) bool {
	if req.Body == nil || req.Body == http.NoBody {
		return true
	}
	if req.GetBody == nil {
		return false
	}
	body, err := req.GetBody()
	if err != nil {
		return false
	}
	req.Body = body
	return true
}
