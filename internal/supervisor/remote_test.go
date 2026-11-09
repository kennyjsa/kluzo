package supervisor_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
)

// newRemote creates dir/origin.git, a bare repository with the branches of
// dir/repo, and returns the SCM on it.
func newRemote(t *testing.T, dir string) *repoSCM {
	t.Helper()
	run := testGit(t)
	bare := filepath.Join(dir, "origin.git")
	run(dir, "init", "-q", "--bare", bare)
	run(filepath.Join(dir, "repo"), "push", "-q", bare, "refs/heads/*:refs/heads/*")
	return &repoSCM{SCM: fakes.NewSCM("main"), bare: bare}
}

// repoSCM is the SCM of a test: the branches are those of the bare remote,
// the PRs those of the in-memory fake.
type repoSCM struct {
	*fakes.SCM
	bare string
}

func (s *repoSCM) BranchHead(_ context.Context, name string) (string, error) {
	out, err := gitRun(s.bare, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil {
		return "", fmt.Errorf("branch %s: %w", name, ports.ErrNotFound)
	}
	return strings.TrimSpace(out), nil
}

func (s *repoSCM) EnsureBranch(ctx context.Context, name, from string) (string, error) {
	if sha, err := s.BranchHead(ctx, name); err == nil {
		return sha, nil
	}
	sha, err := s.BranchHead(ctx, from)
	if err != nil {
		return "", err
	}
	if out, err := gitRun(s.bare, "update-ref", "refs/heads/"+name, sha); err != nil {
		return "", fmt.Errorf("%w: %s", err, out)
	}
	return sha, nil
}

// OpenPR opens the PR in the fake once the remote branches differ.
func (s *repoSCM) OpenPR(ctx context.Context, in ports.NewPR, idemKey string) (ports.PR, error) {
	head, err := s.BranchHead(ctx, in.Head)
	if err != nil {
		return ports.PR{}, err
	}
	if base, err := s.BranchHead(ctx, in.Base); err != nil || base == head {
		return ports.PR{}, fmt.Errorf("open PR %s -> %s: no commits between the branches: %w", in.Head, in.Base, ports.ErrInvalid)
	}
	if _, err := s.SCM.EnsureBranch(ctx, in.Head, in.Base); err != nil {
		return ports.PR{}, err
	}
	if _, err := s.Commit(in.Head); err != nil {
		return ports.PR{}, err
	}
	return s.SCM.OpenPR(ctx, in, idemKey)
}

// fakeDocker is a docker CLI that records its calls and lists the compose
// projects written to its ps file.
type fakeDocker struct {
	bin, calls, ps string
}

func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	dir := t.TempDir()
	d := &fakeDocker{bin: filepath.Join(dir, "docker"), calls: filepath.Join(dir, "calls"), ps: filepath.Join(dir, "ps")}
	script := "#!/bin/sh\necho \"$*\" >> " + d.calls + "\nif [ \"$1\" = ps ] && [ -f " + d.ps + " ]; then cat " + d.ps + "; fi\n"
	if err := os.WriteFile(d.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// Calls returns the docker command lines run so far.
func (d *fakeDocker) Calls() []string {
	b, err := os.ReadFile(d.calls)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
