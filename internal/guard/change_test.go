package guard

import (
	"slices"
	"testing"
)

func added(texts ...string) []Line {
	out := make([]Line, len(texts))
	for i, t := range texts {
		out[i] = Line{N: i + 10, Text: t}
	}
	return out
}

// Each guarantee of the check after the round (ADR 0020, item 2), by table.
func TestChangeRules(t *testing.T) {
	type want struct {
		rule     string
		line     int
		proposal bool
	}
	for _, tt := range []struct {
		name   string
		change Change
		want   []want
	}{
		{"protected path", Change{Path: ".golangci.yml", Added: added("disable-all: true")}, []want{{RuleProtectedPath, 0, true}}},
		{"Go test deleted", Change{Path: "internal/agenda/agenda_test.go", Deleted: true}, []want{{RuleTestRemoved, 0, true}}},
		{"TS spec deleted", Change{Path: "web/src/a.spec.ts", Deleted: true}, []want{{RuleTestRemoved, 0, true}}},
		{"Python test deleted", Change{Path: "tests/test_api.py", Deleted: true}, []want{{RuleTestRemoved, 0, true}}},
		{"a deleted source file is no test", Change{Path: "internal/agenda/agenda.go", Deleted: true}, nil},
		{"t.Skip added", Change{Path: "a_test.go", Added: added("func TestX(t *testing.T) {", "\tt.Skip(\"flaky\")")}, []want{{RuleTestDisabled, 11, true}}},
		{"it.skip added", Change{Path: "src/__tests__/list.js", Added: added("it.skip('lists', () => {")}, []want{{RuleTestDisabled, 10, true}}},
		{"pytest skip added", Change{Path: "test_api.py", Added: added("@pytest.mark.skip(reason='x')")}, []want{{RuleTestDisabled, 10, true}}},
		{"JUnit Disabled added", Change{Path: "src/test/java/AgendaTest.java", Added: added("  @Disabled")}, []want{{RuleTestDisabled, 10, true}}},
		{"skip outside a test file", Change{Path: "cmd/skip.go", Added: added("t.Skip()")}, nil},
		{"nolint", Change{Path: "a.go", Added: added("x := f() //nolint:errcheck")}, []want{{RuleSuppression, 10, false}}},
		{"eslint-disable", Change{Path: "a.ts", Added: added("// eslint-disable-next-line no-console")}, []want{{RuleSuppression, 10, false}}},
		{"noqa", Change{Path: "a.py", Added: added("import os  # noqa: F401")}, []want{{RuleSuppression, 10, false}}},
		{"ts-ignore", Change{Path: "a.ts", Added: added("// @ts-ignore")}, []want{{RuleSuppression, 10, false}}},
		{"rust allow", Change{Path: "src/a.rs", Added: added("#[allow(dead_code)]")}, []want{{RuleSuppression, 10, false}}},
		{"suppression in a test that is also disabled", Change{Path: "a_test.go", Added: added("t.Skip() //nolint")}, []want{{RuleTestDisabled, 10, true}, {RuleSuppression, 10, false}}},
		{"plain code", Change{Path: "a.go", Added: added("// skip the header", "return nil")}, nil},
		{"build constraint added to an existing test file", Change{Path: "a_test.go", Added: added("//go:build ignore")}, []want{{RuleTestDisabled, 10, true}}},
		{"legacy build constraint added to an existing test file", Change{Path: "a_test.go", Added: added("// +build ignore")}, []want{{RuleTestDisabled, 10, true}}},
		{"build constraint in a new test file", Change{Path: "a_test.go", Created: true, Added: added("//go:build integration", "", "package a")}, nil},
		{"legacy build constraint in a new test file", Change{Path: "a_test.go", Created: true, Added: added("// +build integration")}, nil},
		{"RSpec skip added to an existing spec", Change{Path: "spec/agenda_spec.rb", Added: added("  skip 'flaky'")}, []want{{RuleTestDisabled, 10, true}}},
		{"RSpec skip in a new spec", Change{Path: "spec/agenda_spec.rb", Created: true, Added: added("  skip 'not on CI'")}, nil},
		{"t.Skip in a new test file", Change{Path: "a_test.go", Created: true, Added: added("\tt.Skip(\"flaky\")")}, []want{{RuleTestDisabled, 10, true}}},
		{"focus", Change{Path: "a.spec.ts", Added: added("describe.only('agenda', () => {")}, []want{{RuleTestDisabled, 10, true}}},
		{"test renamed out of the runner", Change{Path: "a_test.go", Removed: added("func TestList(t *testing.T) {"), Added: added("func testList(t *testing.T) {")}, []want{{RuleTestRemoved, 0, true}}},
		{"test function deleted", Change{Path: "test_api.py", Removed: added("def test_list(client):")}, []want{{RuleTestRemoved, 0, true}}},
		{"JS test deleted", Change{Path: "a.test.js", Removed: added("  it('lists', () => {")}, []want{{RuleTestRemoved, 0, true}}},
		{"test moved inside the file", Change{Path: "a_test.go", Removed: added("func TestList(t *testing.T) {"), Added: added("func TestList(t *testing.T) {")}, nil},
		{"helper removed from a test file", Change{Path: "a_test.go", Removed: added("func helper() {}")}, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var got []want
			for _, v := range policy.CheckChanges([]Change{tt.change}) {
				if v.Path != tt.change.Path || v.Reason == "" {
					t.Errorf("violation = %+v", v)
				}
				got = append(got, want{v.Rule, v.Line, v.Proposal()})
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("violations = %+v, want %+v", got, tt.want)
			}
		})
	}
}
