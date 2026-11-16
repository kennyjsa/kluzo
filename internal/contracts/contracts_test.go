package contracts

import (
	"bytes"
	"maps"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
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
		EventMergeRebased:        MergeRebased{},
		EventMergeFreshness:      MergeFreshness{},
		EventAreaDeviation:       AreaDeviation{},
		EventNodeFlow:            NodeFlow{},
		EventEffectFailed:        EffectFailed{},
		EventEffectRetried:       EffectRetried{},
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

// Every built-in schema is a valid JSON Schema, and the examples of
// docs/contracts/results.md (the section named after the schema) satisfy it.
func TestOutputSchemasAcceptTheDocumentedExamples(t *testing.T) {
	doc, err := os.ReadFile("../../docs/contracts/results.md")
	if err != nil {
		t.Fatal(err)
	}
	sections := regexp.MustCompile("(?m)^## `([a-z-]+)`").FindAllStringSubmatchIndex(string(doc), -1)
	example := regexp.MustCompile("(?s)```json\n(.*?)```")
	documented := map[string]bool{}
	for i, m := range sections {
		name, end := string(doc)[m[2]:m[3]], len(doc)
		if i+1 < len(sections) {
			end = sections[i+1][0]
		}
		documented[name] = true
		data, ok := OutputSchema(name)
		if !ok {
			t.Errorf("results.md documents %s but there is no schema", name)
			continue
		}
		sch := compileSchema(t, name, data)
		ex := example.FindStringSubmatch(string(doc)[m[0]:end])
		if ex == nil {
			t.Errorf("%s: no JSON example in results.md", name)
			continue
		}
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(ex[1]))
		if err != nil {
			t.Fatalf("%s: example is not JSON: %v", name, err)
		}
		if err := sch.Validate(inst); err != nil {
			t.Errorf("%s: documented example is rejected: %v", name, err)
		}
	}
	if got := OutputSchemas(); !slices.Equal(got, slices.Sorted(maps.Keys(documented))) {
		t.Errorf("OutputSchemas = %v, results.md documents %v", got, slices.Sorted(maps.Keys(documented)))
	}
}

func compileSchema(t *testing.T, name string, data []byte) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:///"+name+".json", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("mem:///" + name + ".json")
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return sch
}

func TestOutputSchemasRejectBrokenOutput(t *testing.T) {
	tests := []struct{ schema, json string }{
		{"review-result", `{"v":1,"summary":"x","criteria":[],"findings":[{"severity":"blocking","category":"correctness","file":"a.go","line":1,"message":"m"}]}`},    // no check
		{"review-result", `{"v":1,"summary":"x","verdict":"approve","criteria":[],"findings":[]}`},                                                                     // verdict is gone
		{"review-result", `{"v":1,"summary":"x","criteria":[],"findings":[{"severity":"minor","category":"style","file":"a.go","line":1,"message":"m","auto":true}]}`}, // only Kluzo sets auto
		{"review-result", `{"v":1,"summary":"x","criteria":[],"findings":[{"severity":"bad","category":"style","file":"a","line":1,"message":"m"}]}`},
		{"node-result", `{"v":1,"summary":"x","status":"failed","criteria":[],"files_changed":[],"tests":{"added":0,"command":""}}`}, // failed without reason
		{"node-result", `{"v":1,"summary":"x","status":"done","criteria":[{"id":"1","evidence":[]}],"files_changed":[],"tests":{"added":0,"command":""}}`},
		{"triage-result", `{"v":1,"summary":"x","action":"split","reason":"r","params":{}}`},
		{"report-result", `{"v":2,"summary":"x","markdown":"m"}`},
	}
	for _, tt := range tests {
		data, _ := OutputSchema(tt.schema)
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(tt.json))
		if err != nil {
			t.Fatal(err)
		}
		if compileSchema(t, tt.schema, data).Validate(inst) == nil {
			t.Errorf("%s accepted %s", tt.schema, tt.json)
		}
	}
}
