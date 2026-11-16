// Package contracts holds the types and closed vocabularies shared by every
// layer (docs/architecture/packages.md). It contains no logic. config, prompt,
// mcp and the harness adapters all read the same lists from here.
package contracts

import (
	"embed"
	"strings"
)

// ProtocolVersion is the version of the Kluzo agent protocol implemented by
// this binary (docs/contracts/prompt.md). A role declares the version it was
// written for in its frontmatter (`protocol:`), and the two must match.
const ProtocolVersion = 1

// Tools returns the canonical tool vocabulary of a role's `tools:` list
// (docs/contracts/config.md). Each harness adapter translates these names.
func Tools() []string { return []string{"read", "edit", "bash", "test"} }

// WriteTools returns the canonical tools that let a role change the worktree
// or run code. A read-only role (the reviewer, ADR 0020) cannot have them.
func WriteTools() []string { return []string{"edit", "bash"} }

// MCPTools returns the kluzo_* tools a role can list in `mcp:`
// (docs/contracts/mcp.md).
func MCPTools() []string {
	return []string{
		"kluzo_context", "kluzo_changes", "kluzo_decisions",
		"kluzo_code_map", "kluzo_code_find", "kluzo_code_refs",
		"kluzo_decision", "kluzo_ask", "kluzo_propose_node", "kluzo_result",
	}
}

// ReservedMarkers returns the untrusted-data delimiters that a role body must
// not contain (docs/contracts/prompt.md).
func ReservedMarkers() []string { return []string{"<<<UNTRUSTED", "UNTRUSTED:"} }

//go:embed schemas/*.json
var schemaFS embed.FS

// OutputSchemas returns the names of the role output schemas built into the
// binary (docs/contracts/results.md), sorted. A project can add more, or
// override these, in .kluzo/schemas/.
func OutputSchemas() []string {
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		panic(err) // the embedded directory always exists
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = strings.TrimSuffix(e.Name(), ".json")
	}
	return names
}

// OutputSchema returns the JSON Schema text of a built-in output schema.
func OutputSchema(name string) ([]byte, bool) {
	data, err := schemaFS.ReadFile("schemas/" + name + ".json")
	return data, err == nil
}

// CacheEcosystems returns the ecosystems `caches:` accepts (ADR 0025).
func CacheEcosystems() []string { return []string{"go", "node", "python", "rust", "java"} }
