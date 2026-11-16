package github

import (
	"errors"
	"fmt"
	"net/http"

	gh "github.com/google/go-github/v93/github"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// fail classifies a go-github error into the port errors and masks secrets in
// its message. A rate limit or a server error stays unclassified, which the
// outbox treats as retryable.
func (a *Adapter) fail(op string, err error) error {
	if err == nil {
		return nil
	}
	class := classify(err)
	if class == nil {
		return a.redactor.Error(fmt.Errorf("github %s: %w", op, err))
	}
	return a.redactor.Error(fmt.Errorf("github %s: %w: %w", op, class, err))
}

func classify(err error) error {
	var rate *gh.RateLimitError
	var abuse *gh.AbuseRateLimitError
	if errors.As(err, &rate) || errors.As(err, &abuse) {
		return nil
	}
	var resp *gh.ErrorResponse
	if !errors.As(err, &resp) || resp.Response == nil {
		return nil
	}
	switch resp.Response.StatusCode {
	case http.StatusNotFound:
		return ports.ErrNotFound
	case http.StatusUnauthorized, http.StatusForbidden:
		return ports.ErrPermanent
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return ports.ErrInvalid
	}
	return nil
}

// isStatus reports whether err is a GitHub error response with that status.
func isStatus(err error, status int) bool {
	var resp *gh.ErrorResponse
	return errors.As(err, &resp) && resp.Response != nil && resp.Response.StatusCode == status
}
