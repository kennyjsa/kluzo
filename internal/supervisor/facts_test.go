package supervisor

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	"github.com/kennyjsa/kluzo/internal/contracts"
	"github.com/kennyjsa/kluzo/internal/core"
	"github.com/kennyjsa/kluzo/internal/store"
)

func ev(kind string, payload any) store.Event {
	b, _ := json.Marshal(payload)
	return store.Event{Kind: kind, Payload: b}
}

func transition(attempt int) store.Event {
	return ev(contracts.EventNodeTransition, contracts.NodeTransition{From: "gating", To: "running", Attempt: attempt})
}

func edited(path string) store.Event {
	return ev(contracts.EventFileEdited, contracts.FileEdited{Path: path})
}

func gate(name, state string) store.Event {
	return ev(contracts.EventGateFinished, contracts.GateRun{Gate: name, State: state})
}

func flowWith(findings ...core.Finding) store.Event {
	run, _ := json.Marshal(core.Run{Findings: core.Findings{List: findings}})
	return ev(contracts.EventNodeFlow, contracts.NodeFlow{Run: run})
}

func TestPreviousFacts(t *testing.T) {
	major := core.Finding{ID: "F2", Severity: core.SeverityMajor, File: "a.go", Line: 3, Message: "nil map", Status: core.FindingOpen}
	resolved := core.Finding{ID: "F1", Severity: core.SeverityBlocking, File: "a.go", Line: 1, Message: "fixed", Status: core.FindingResolved}
	minor := core.Finding{ID: "F3", Severity: core.SeverityMinor, File: "b.go", Line: 2, Message: "naming", Status: core.FindingOpen}
	many := make([]string, factFiles+2)
	var manyEvents []store.Event
	for i := range many {
		many[i] = fmt.Sprintf("f%03d.go", i)
		manyEvents = append(manyEvents, edited(many[i]))
	}
	for _, tt := range []struct {
		name    string
		events  []store.Event
		attempt int
		want    []string
	}{
		{
			name: "files, red gates and open findings of the attempt",
			events: []store.Event{
				transition(1), edited("b.go"), edited("/ws/wt/a.go"), edited("a.go"), edited("/etc/hosts"), edited("../x.go"), gate("build", "failed"), gate("build", "passed"),
				flowWith(resolved, major, minor), gate("test", "failed"), gate("lint", "error"), transition(2), flowWith(resolved, major, minor),
				edited("c.go"), gate("test", "passed"),
			},
			attempt: 1,
			want:    []string{"changed files: a.go, b.go", "gate test failed", "gate lint error", "finding F2 still open (major, a.go:3): nil map"},
		},
		{
			name:    "only the asked attempt",
			events:  []store.Event{transition(1), edited("a.go"), gate("test", "failed"), transition(2), edited("c.go"), gate("test", "failed"), flowWith(), transition(3)},
			attempt: 2,
			want:    []string{"changed files: c.go", "gate test failed"},
		},
		{
			name:    "the changed files are cut at the limit",
			events:  append([]store.Event{transition(1)}, manyEvents...),
			attempt: 1,
			want:    []string{"changed files: " + joinFirst(many, factFiles) + " (+2 files)"},
		},
		{name: "nothing journaled", events: []store.Event{transition(1)}, attempt: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := previousFacts(tt.events, tt.attempt, "/ws/wt")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("facts =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
	if _, err := previousFacts([]store.Event{{Kind: contracts.EventNodeTransition, Payload: json.RawMessage(`{`)}}, 1, "/ws/wt"); err == nil {
		t.Error("a bad journal entry is not an error")
	}
}

func joinFirst(s []string, n int) string {
	out := s[0]
	for _, x := range s[1:n] {
		out += ", " + x
	}
	return out
}
