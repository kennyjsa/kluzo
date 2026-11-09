//go:build integration

package supervisor_test

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/platform/clock"
	"github.com/kennyjsa/kluzo/internal/platform/ids"
	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/fakes"
	"github.com/kennyjsa/kluzo/internal/runtime/claudecode"
)

// A trivial node run for real: the implementer and the reviewer are Claude
// Code sessions (haiku), in a temporary repository, with the in-memory
// tracker and gates. It runs only when KLUZO_IT_NODE=1 and a model credential
// for a synthetic HOME is set (CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY),
// and the node may spend at most US$ 0.30.
//
//	KLUZO_IT_NODE=1 CLAUDE_CODE_OAUTH_TOKEN=... go test -tags integration -run TestIntegrationNode -v ./internal/supervisor/
func TestIntegrationNode(t *testing.T) {
	cred := claudecode.Credential{Name: "CLAUDE_CODE_OAUTH_TOKEN", Value: os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")}
	if cred.Value == "" {
		cred = claudecode.Credential{Name: "ANTHROPIC_API_KEY", Value: os.Getenv("ANTHROPIC_API_KEY")}
	}
	if os.Getenv("KLUZO_IT_NODE") != "1" || cred.Value == "" {
		t.Skip("KLUZO_IT_NODE=1 and CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY are not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	dir := t.TempDir()
	makeRepo(t, filepath.Join(dir, "repo"))
	db := openDB(t, filepath.Join(dir, "state.db"))
	gen := ids.NewGenerator(rand.Reader)
	tracker := fakes.NewTracker(clock.System{})
	issue, err := tracker.CreateIssue(ctx, ports.NewIssue{Title: "Hello file", Body: "Create the file hello.txt at the root of the repository with the single line: hello"}, "it")
	if err != nil {
		t.Fatal(err)
	}
	node := seed(t, db, gen, issue.Ref, 300_000)
	rt := claudecode.New(claudecode.Config{
		Home: filepath.Join(dir, "ws", "home"), ConfigDir: filepath.Join(dir, "cfg"), LogDir: filepath.Join(dir, "logs"),
		ParentEnv: os.Environ(), Credential: cred, Clock: clock.System{},
	})
	if _, err := rt.Check(ctx); err != nil {
		t.Fatal(err)
	}
	sup := buildWith(t, dir, deps{db: db, clock: clock.System{}, ids: gen, tracker: tracker, runtime: rt, gates: fakes.NewGates(), notifier: &fakes.Notifier{}}, issue.Ref, "haiku")
	for i := 0; i < 100; i++ {
		busy, err := sup.Step(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !busy {
			break
		}
	}
	e := &env{t: t, db: db, node: node}
	n, r := e.nodeRow()
	t.Logf("node %s (%s), attempt %d round %d, cost US$ %.4f, moves %v", n.State, n.BlockedReason, n.Attempt, n.Round, float64(r.CostUSD)/1e6, e.moves())
	if n.State != core.NodeQueued {
		t.Errorf("node = %s, want queued", n.State)
	}
}
