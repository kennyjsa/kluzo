package guard

import (
	"strings"
)

// testFiles are the globs of test files, by ecosystem.
var testFiles = []string{
	"*_test.go",
	"*.test.js", "*.test.jsx", "*.test.ts", "*.test.tsx", "*.test.mjs", "*.test.cjs",
	"*.spec.js", "*.spec.jsx", "*.spec.ts", "*.spec.tsx", "*.spec.mjs", "*.spec.cjs",
	"**/__tests__/**",
	"test_*.py", "*_test.py",
	"*Test.java", "*Tests.java", "*Test.kt", "*Tests.kt",
	"*_spec.rb", "*_test.rb",
	"tests/**/*.rs",
}

// IsTest reports whether the slash-separated path is a test file.
func IsTest(name string) bool { return matchAny(testFiles, name) }

// skipMarkers disable a test when added to a test file.
var skipMarkers = []string{
	"t.Skip(", "t.Skipf(", "t.SkipNow(", "b.Skip(", // Go
	".skip(", "xit(", "xdescribe(", "xtest(", ".todo(", // JavaScript
	"@pytest.mark.skip", "pytest.skip(", "@unittest.skip", "self.skipTest(", // Python
	"@Disabled", "@Ignore", // JUnit
	"#[ignore]",         // Rust
	"skip ", "pending ", // RSpec, at the start of a line
}

// suppressionMarkers silence a linter or a type checker.
var suppressionMarkers = []string{
	"//nolint", "// nolint", "//lint:ignore", "#nosec", "# nosec", // Go
	"eslint-disable", "@ts-ignore", "@ts-expect-error", "@ts-nocheck", "stylelint-disable", // JavaScript
	"# noqa", "#noqa", "# type: ignore", "pylint: disable", "pylint:disable", // Python
	"#[allow(", "#![allow(", // Rust
	"@SuppressWarnings", "NOSONAR", // Java
	"rubocop:disable",                    // Ruby
	"@phpstan-ignore", "@psalm-suppress", // PHP
}

// ruleTestRemoved flags a test file the round deleted.
func ruleTestRemoved(_ Policy, ch Change) []Violation {
	if ch.Deleted && IsTest(ch.Path) {
		return []Violation{{Rule: RuleTestRemoved, Path: ch.Path, Reason: ch.Path + " is a test file and was deleted"}}
	}
	return nil
}

// ruleTestDisabled flags a skip marker added to a test file.
func ruleTestDisabled(_ Policy, ch Change) []Violation {
	if !IsTest(ch.Path) {
		return nil
	}
	var out []Violation
	for _, l := range ch.Added {
		if m := marker(l.Text, skipMarkers); m != "" {
			out = append(out, Violation{Rule: RuleTestDisabled, Path: ch.Path, Line: l.N, Reason: "a test was disabled with " + strings.TrimSpace(m)})
		}
	}
	return out
}

// ruleSuppression flags a lint or type suppression added to any file.
func ruleSuppression(_ Policy, ch Change) []Violation {
	var out []Violation
	for _, l := range ch.Added {
		if m := marker(l.Text, suppressionMarkers); m != "" {
			out = append(out, Violation{Rule: RuleSuppression, Path: ch.Path, Line: l.N,
				Reason: "new lint suppression `" + strings.TrimSpace(l.Text) + "`: remove it and fix what the linter reports"})
		}
	}
	return out
}

// marker returns the first marker found in the line. The markers ending in a
// space ("skip ", "pending ") count only at the start of the line.
func marker(line string, markers []string) string {
	trimmed := strings.TrimSpace(line)
	for _, m := range markers {
		if strings.HasSuffix(m, " ") {
			if strings.HasPrefix(trimmed, m) {
				return m
			}
			continue
		}
		if strings.Contains(line, m) {
			return m
		}
	}
	return ""
}
