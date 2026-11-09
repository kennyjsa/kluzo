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
