package github

import (
	gh "github.com/google/go-github/v93/github"
)

// Permission is a permission of the bot in the repository and its minimum
// level (read or write).
type Permission struct{ Name, Level string }

func (p Permission) String() string { return p.Name + ": " + p.Level }

// RequiredPermissions are the permissions of the bot
// (docs/architecture/integrations.md#permissões-no-github).
var RequiredPermissions = []Permission{
	{"metadata", "read"}, {"issues", "write"}, {"contents", "write"},
	{"pull_requests", "write"}, {"statuses", "read"}, {"checks", "read"},
}

// PermissionCheck is what the adapter knows of its permissions. Verified is
// false for a personal token, whose permissions the API does not tell;
// Missing lists the required permissions an App installation lacks.
type PermissionCheck struct {
	Verified bool
	Missing  []Permission
}

// Permissions reports the permissions of the installation of the App, read
// when the adapter was built; a token cannot be checked.
func (a *Adapter) Permissions() PermissionCheck {
	if a.installation == nil {
		return PermissionCheck{}
	}
	return PermissionCheck{Verified: true, Missing: missingPermissions(a.installation)}
}

// missingPermissions compares the permissions of an installation with
// RequiredPermissions; write covers read, admin covers both.
func missingPermissions(p *gh.InstallationPermissions) []Permission {
	have := map[string]string{
		"metadata": p.GetMetadata(), "issues": p.GetIssues(), "contents": p.GetContents(),
		"pull_requests": p.GetPullRequests(), "statuses": p.GetStatuses(), "checks": p.GetChecks(),
	}
	rank := map[string]int{"read": 1, "write": 2, "admin": 3}
	var out []Permission
	for _, req := range RequiredPermissions {
		if rank[have[req.Name]] < rank[req.Level] {
			out = append(out, req)
		}
	}
	return out
}
