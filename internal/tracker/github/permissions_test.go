package github

import (
	"fmt"
	"testing"

	gh "github.com/google/go-github/v93/github"
)

func TestMissingPermissions(t *testing.T) {
	s := func(v string) *string { return &v }
	for _, tt := range []struct {
		name string
		p    *gh.InstallationPermissions
		want string
	}{
		{"none", &gh.InstallationPermissions{}, "[metadata: read issues: write contents: write pull_requests: write statuses: read checks: read]"},
		{"write covers read, admin covers write", &gh.InstallationPermissions{
			Metadata: s("read"), Issues: s("write"), Contents: s("admin"), PullRequests: s("write"), Statuses: s("write"), Checks: s("write"),
		}, "[]"},
		{"read is not write", &gh.InstallationPermissions{
			Metadata: s("read"), Issues: s("read"), Contents: s("write"), PullRequests: s("write"), Statuses: s("read"), Checks: s("read"),
		}, "[issues: write]"},
	} {
		if got := fmt.Sprint(missingPermissions(tt.p)); got != tt.want {
			t.Errorf("%s: missing = %s, want %s", tt.name, got, tt.want)
		}
	}
}
