package contracts

import (
	"os"
	"regexp"
	"slices"
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
