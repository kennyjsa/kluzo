package contracts

import (
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestMCPToolsMatchContract keeps MCPTools in step with docs/contracts/mcp.md:
// every `## kluzo_*` heading is listed, and every listed tool has a heading
// (the three code tools share one table).
func TestMCPToolsMatchContract(t *testing.T) {
	doc, err := os.ReadFile("../../docs/contracts/mcp.md")
	if err != nil {
		t.Fatal(err)
	}
	var documented []string
	for _, m := range regexp.MustCompile("(?m)^## `(kluzo_[a-z_]+)`").FindAllStringSubmatch(string(doc), -1) {
		documented = append(documented, m[1])
	}
	for _, m := range regexp.MustCompile("(?m)^\\| `(kluzo_code_[a-z]+)` ").FindAllStringSubmatch(string(doc), -1) {
		documented = append(documented, m[1])
	}
	got := MCPTools()
	slices.Sort(documented)
	slices.Sort(got)
	if !slices.Equal(got, documented) {
		t.Errorf("MCPTools = %v, mcp.md documents %v", got, documented)
	}
}

func TestWriteToolsAreCanonical(t *testing.T) {
	for _, w := range WriteTools() {
		if !slices.Contains(Tools(), w) {
			t.Errorf("write tool %q is not in the canonical vocabulary", w)
		}
	}
}

// TestEventsMatchContract keeps the event kinds and payload fields of events.go
// in step with the core-events table of docs/contracts/events.md.
func TestEventsMatchContract(t *testing.T) {
	doc, err := os.ReadFile("../../docs/contracts/events.md")
	if err != nil {
		t.Fatal(err)
	}
	row := regexp.MustCompile("(?m)^\\| `([a-z.]+)` \\| `\\{([^}]*)\\}`")
	documented := map[string][]string{}
	for _, m := range row.FindAllStringSubmatch(string(doc), -1) {
		var fields []string
		for _, f := range strings.Split(m[2], ",") {
			f = strings.TrimSpace(f)
			f = strings.TrimSuffix(f, "?")
			if name, _, ok := strings.Cut(f, ":"); ok {
				f = name
			}
			fields = append(fields, f)
		}
		slices.Sort(fields)
		documented[m[1]] = fields
	}
	payloads := map[string]any{
		EventWorkspaceCreated:    WorkspaceCreated{},
		EventWorkspaceTransition: WorkspaceTransition{},
		EventNodeAdded:           NodeAdded{},
		EventNodeTransition:      NodeTransition{},
		EventEdgeAdded:           EdgeAdded{},
		EventMergeDone:           MergeDone{},
	}
	for kind, payload := range payloads {
		want, ok := documented[kind]
		if !ok {
			t.Errorf("kind %q is not in events.md", kind)
			continue
		}
		if got := jsonFields(payload); !slices.Equal(got, want) {
			t.Errorf("%s fields = %v, events.md documents %v", kind, got, want)
		}
	}
}

// jsonFields returns the sorted JSON field names of a payload struct.
func jsonFields(v any) []string {
	var names []string
	typ := reflect.TypeOf(v)
	for i := range typ.NumField() {
		name, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
