package gates

import (
	"bytes"
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

type testKey struct{ pkg, test string }

// goTests reads go test -json events (go doc test2json) and gives the failed
// tests: only the innermost ones, since a test fails with its subtests, and a
// package that failed without a failed test (a build error). It keeps at most
// testOutMax bytes of output per test.
type goTests struct {
	outputs map[testKey]*bytes.Buffer
	failed  []testKey
}

func (g *goTests) add(ev testEvent) {
	k := testKey{ev.Package, ev.Test}
	switch ev.Action {
	case "output":
		b := g.outputs[k]
		if b == nil {
			b = &bytes.Buffer{}
			g.outputs[k] = b
		}
		if b.Len() < testOutMax {
			b.WriteString(ev.Output[:min(len(ev.Output), testOutMax-b.Len())])
		}
	case "fail":
		if len(g.failed) < 4*failuresMax {
			g.failed = append(g.failed, k)
		}
	}
}

func (g *goTests) output(k testKey) []string {
	if b := g.outputs[k]; b != nil {
		return strings.SplitAfter(b.String(), "\n")
	}
	return nil
}

func (g *goTests) failures() []ports.TestFailure {
	failedTests := map[string]bool{} // package\x00test of the failed tests
	for _, k := range g.failed {
		if k.test != "" {
			failedTests[k.pkg+"\x00"+k.test] = true
		}
	}
	var out []ports.TestFailure
	for _, k := range g.failed {
		if len(out) == failuresMax {
			break
		}
		if k.test == "" {
			if !hasFailedTest(failedTests, k.pkg+"\x00") {
				out = append(out, ports.TestFailure{Test: k.pkg, Message: strings.TrimSpace(strings.Join(g.output(k), ""))})
			}
			continue
		}
		if hasFailedTest(failedTests, k.pkg+"\x00"+k.test+"/") {
			continue // the failure is in a subtest
		}
		out = append(out, goFailure(k.test, g.output(k)))
	}
	return out
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
	for len(out) < failuresMax {
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
	return out[:min(len(out), failuresMax)], nil
}
