package fakes

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// SCM is an in-memory ports.SCM. Commits are fake shas ("c1", "c2"…).
type SCM struct {
	mu       sync.Mutex
	commits  int
	branches map[string]string
	prs      []*ports.PR
}

var _ ports.SCM = (*SCM)(nil)

// NewSCM returns a repository with one branch, base, at a first commit.
func NewSCM(base string) *SCM {
	s := &SCM{branches: map[string]string{}}
	s.branches[base] = s.newCommit()
	return s
}

// Commit moves the branch head to a new commit and returns its sha. It stands
// for a push done by the gitops package.
func (s *SCM) Commit(branch string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.branches[branch]; !ok {
		return "", fmt.Errorf("branch %s: %w", branch, ports.ErrNotFound)
	}
	sha := s.newCommit()
	s.branches[branch] = sha
	return sha, nil
}

// Merge marks the PR as merged, as the human does on the tracker.
func (s *SCM) Merge(prRef string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(prRef)
	if err != nil {
		return err
	}
	p.State, p.Merged = "closed", true
	return nil
}

// EnsureBranch implements ports.SCM.
func (s *SCM) EnsureBranch(_ context.Context, name, fromRef string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sha, ok := s.branches[name]; ok {
		return sha, nil
	}
	sha, ok := s.resolve(fromRef)
	if !ok {
		return "", fmt.Errorf("ref %s: %w", fromRef, ports.ErrNotFound)
	}
	s.branches[name] = sha
	return sha, nil
}

// BranchHead implements ports.SCM.
func (s *SCM) BranchHead(_ context.Context, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sha, ok := s.branches[name]
	if !ok {
		return "", fmt.Errorf("branch %s: %w", name, ports.ErrNotFound)
	}
	return sha, nil
}

// OpenPR implements ports.SCM.
func (s *SCM) OpenPR(_ context.Context, in ports.NewPR, idemKey string) (ports.PR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if idemKey == "" {
		return ports.PR{}, fmt.Errorf("open PR: empty idempotency key: %w", ports.ErrInvalid)
	}
	for _, p := range s.prs {
		if p.Head == in.Head && p.Base == in.Base {
			return s.snapshot(p), nil
		}
	}
	head, hok := s.branches[in.Head]
	base, bok := s.branches[in.Base]
	if !hok || !bok {
		return ports.PR{}, fmt.Errorf("open PR %s -> %s: unknown branch: %w", in.Head, in.Base, ports.ErrInvalid)
	}
	if head == base {
		return ports.PR{}, fmt.Errorf("open PR %s -> %s: no commits between the branches: %w", in.Head, in.Base, ports.ErrInvalid)
	}
	ref := strconv.Itoa(len(s.prs) + 1)
	p := &ports.PR{Ref: ref, Title: in.Title, Body: in.Body, Head: in.Head, Base: in.Base, State: "open", URL: "fake://pr/" + ref}
	s.prs = append(s.prs, p)
	return s.snapshot(p), nil
}

// UpdatePR implements ports.SCM.
func (s *SCM) UpdatePR(_ context.Context, ref, body string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(ref)
	if err != nil {
		return err
	}
	p.Body = body
	return nil
}

// PR implements ports.SCM.
func (s *SCM) PR(_ context.Context, ref string) (ports.PR, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(ref)
	if err != nil {
		return ports.PR{}, err
	}
	return s.snapshot(p), nil
}

// IsMerged implements ports.SCM.
func (s *SCM) IsMerged(_ context.Context, prRef string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.find(prRef)
	if err != nil {
		return false, err
	}
	return p.Merged, nil
}

func (s *SCM) newCommit() string {
	s.commits++
	return "c" + strconv.Itoa(s.commits)
}

// resolve accepts a branch name or a sha the repository knows.
func (s *SCM) resolve(ref string) (string, bool) {
	if sha, ok := s.branches[ref]; ok {
		return sha, true
	}
	for _, sha := range s.branches {
		if sha == ref {
			return sha, true
		}
	}
	return "", false
}

func (s *SCM) find(ref string) (*ports.PR, error) {
	n, err := strconv.Atoi(ref)
	if err != nil || n < 1 || n > len(s.prs) {
		return nil, fmt.Errorf("PR %s: %w", ref, ports.ErrNotFound)
	}
	return s.prs[n-1], nil
}

func (s *SCM) snapshot(p *ports.PR) ports.PR {
	out := *p
	out.HeadSHA = s.branches[p.Head]
	return out
}
