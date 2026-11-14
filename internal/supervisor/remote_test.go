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
	// openPRErr fails OpenPR while set; prKeys are the idempotency keys
	// OpenPR was called with.
	openPRErr error
	prKeys    []string
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
	s.prKeys = append(s.prKeys, idemKey)
	if s.openPRErr != nil {
		return ports.PR{}, s.openPRErr
	}
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

// fakeDocker is a docker CLI that records its calls, lists the compose
// projects written to its ps file and the images of its images file (id,
// compose project and size, tab-separated).
type fakeDocker struct {
	bin, calls, ps, images string
}

func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	dir := t.TempDir()
	d := &fakeDocker{bin: filepath.Join(dir, "docker"), calls: filepath.Join(dir, "calls"), ps: filepath.Join(dir, "ps"), images: filepath.Join(dir, "images")}
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 27.3.1; exit 0; fi\necho \"$*\" >> " + d.calls + "\nif [ \"$1\" = ps ] && [ -f " + d.ps + " ]; then cat " + d.ps + "; fi\n" +
		"if [ -f " + d.images + " ]; then case \"$1 $2\" in 'image ls') cut -f1 " + d.images + " ;; 'image inspect') cat " + d.images + " ;; esac; fi\n"
	if err := os.WriteFile(d.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

// down makes the daemon unreachable: every call is recorded, and docker
// version answers without the server part.
func (d *fakeDocker) down(t *testing.T) {
	t.Helper()
	script := "#!/bin/sh\necho \"$*\" >> " + d.calls + "\necho 'Cannot connect to the Docker daemon' >&2\nexit 1\n"
	if err := os.WriteFile(d.bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Calls returns the docker command lines run so far.
func (d *fakeDocker) Calls() []string {
	b, err := os.ReadFile(d.calls)
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}
