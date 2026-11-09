package gates

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/kennyjsa/kluzo/internal/ports"
)

// testEvent is a line of go test -json (go doc test2json).
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

// goLocation is the file:line: a failure message of the testing package starts with.
var goLocation = regexp.MustCompile(`^\s*([\w./-]+\.go):(\d+): ?(.*)$`)

// goTestJSON reads an output that may hold go test -json events. It returns
// the output as text (the Output of each event, other lines as they are) and
// the failed tests: only the innermost ones, since a test fails with its
// subtests, and a package that failed without a failed test (a build error).
func goTestJSON(output string) (string, []ports.TestFailure) {
	var text strings.Builder
	type key struct{ pkg, test string }
	outputs := map[key][]string{}
	var failed []key
	for line := range strings.Lines(output) {
		var ev testEvent
		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &ev) != nil || ev.Action == "" {
			text.WriteString(line)
			continue
		}
		text.WriteString(ev.Output)
		k := key{ev.Package, ev.Test}
		switch ev.Action {
		case "output":
			outputs[k] = append(outputs[k], ev.Output)
		case "fail":
			failed = append(failed, k)
		}
	}
	failedTests := map[string]bool{} // package/test of the failed tests
	for _, k := range failed {
		if k.test != "" {
			failedTests[k.pkg+"\x00"+k.test] = true
		}
	}
	var out []ports.TestFailure
	for _, k := range failed {
		if k.test == "" {
			if !hasFailedTest(failedTests, k.pkg) {
				out = append(out, ports.TestFailure{Test: k.pkg, Message: strings.TrimSpace(strings.Join(outputs[k], ""))})
			}
			continue
		}
		if hasFailedTest(failedTests, k.pkg+"\x00"+k.test+"/") {
			continue // the failure is in a subtest
		}
		out = append(out, goFailure(k.test, outputs[k]))
	}
	return text.String(), out
}

// hasFailedTest reports whether a failed test starts with prefix.
func hasFailedTest(tests map[string]bool, prefix string) bool {
	for t := range tests {
		if strings.HasPrefix(t, prefix) {
			return true
		}
	}
	return false
}

// goFailure builds a failure from the output of a test, without the lines
// of the runner (=== RUN, --- FAIL…).
func goFailure(test string, lines []string) ports.TestFailure {
	f := ports.TestFailure{Test: test}
	var msg []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "=== ") || strings.HasPrefix(trimmed, "--- ") {
			continue
		}
		if m := goLocation.FindStringSubmatch(strings.TrimRight(l, "\r\n")); m != nil && f.File == "" {
			f.File = m[1]
			f.Line, _ = strconv.Atoi(m[2])
			trimmed = m[3]
		}
		msg = append(msg, trimmed)
	}
	f.Message = strings.Join(msg, "\n")
	return f
}

// junitCase is a testcase of a JUnit XML report.
type junitCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr"`
	File      string        `xml:"file,attr"`
	Line      int           `xml:"line,attr"`
	Failures  []junitResult `xml:"failure"`
	Errors    []junitResult `xml:"error"`
}

type junitResult struct {
	Message string `xml:"message,attr"`
	Text    string `xml:",chardata"`
}

// junitFailures reads the failed and errored test cases of a JUnit report,
// whatever the nesting of testsuites and testsuite.
func junitFailures(data []byte) ([]ports.TestFailure, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	var out []ports.TestFailure
	for {
		tok, err := d.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil
			}
			return out, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "testcase" {
			continue
		}
		var c junitCase
		if err := d.DecodeElement(&c, &start); err != nil {
			return out, err
		}
		name := c.Name
		if c.Classname != "" {
			name = c.Classname + "." + c.Name
		}
		for _, r := range append(c.Failures, c.Errors...) {
			msg := strings.TrimSpace(strings.Join([]string{r.Message, strings.TrimSpace(r.Text)}, "\n"))
			out = append(out, ports.TestFailure{Test: name, File: c.File, Line: c.Line, Message: msg})
		}
	}
}
