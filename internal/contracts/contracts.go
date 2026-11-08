// Package contracts holds the types and constants shared by every layer
// (docs/architecture/packages.md). It contains no logic.
package contracts

// ProtocolVersion is the version of the Kluzo agent protocol implemented by
// this binary (docs/contracts/prompt.md). A role declares the version it was
// written for in its frontmatter (`protocol:`), and the two must match.
const ProtocolVersion = 1

// OutputSchemas returns the names of the role output schemas built into the
// binary (docs/contracts/results.md). A project can add more, or override
// these, in .kluzo/schemas/.
//
// TODO: derive this list from the embedded internal/contracts/schemas/*.json
// once those files exist (see docs/planning/questions.md, Q7).
func OutputSchemas() []string {
	return []string{
		"node-result", "review-result", "triage-result", "plan-result",
		"critique-result", "refinement-result", "architecture-result", "report-result",
	}
}
