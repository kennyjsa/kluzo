package core

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The tables in docs/contracts/state-machine.md are the contract; these tests
// fail when the code and the document disagree (docs/architecture/patterns.md,
// pattern 3).

const contractPath = "../../docs/contracts/state-machine.md"

// readTable returns the first two columns of the markdown table that follows
// the given heading.
func readTable(t *testing.T, heading string) [][2]string {
	t.Helper()
	f, err := os.Open(filepath.Clean(contractPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	var rows [][2]string
	inSection, inTable := false, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") {
			if inTable {
				break
			}
			inSection = line == heading
			continue
		}
		if !inSection || !strings.HasPrefix(line, "|") {
			if inTable {
				break
			}
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 2 {
			continue
		}
		from, to := strings.TrimSpace(cells[0]), strings.TrimSpace(cells[1])
		if from == "De" || strings.HasPrefix(from, "---") {
			inTable = true
			continue
		}
		inTable = true
		rows = append(rows, [2]string{unquote(from), unquote(to)})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatalf("no table found under %q in %s", heading, contractPath)
	}
	return rows
}

func unquote(s string) string { return strings.Trim(s, "`") }

func pairKey(from, to string) string { return from + " → " + to }

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func compare(t *testing.T, doc, code map[string]bool) {
	t.Helper()
	for _, k := range sortedKeys(doc) {
		if !code[k] {
			t.Errorf("contract allows %s, code does not", k)
		}
	}
	for _, k := range sortedKeys(code) {
		if !doc[k] {
			t.Errorf("code allows %s, contract does not", k)
		}
	}
}

func TestNodeTableMatchesContract(t *testing.T) {
	nonFinal := []NodeState{}
	for _, s := range NodeStates {
		if !s.Final() {
			nonFinal = append(nonFinal, s)
		}
	}

	doc := map[string]bool{}
	for _, r := range readTable(t, "### Transições do nó") {
		from, to := r[0], r[1]
		if to == "estado anterior" {
			to = "previous"
		}
		if from == "qualquer não final" {
			for _, s := range nonFinal {
				if NodeState(to) == NodeBlocked && s == NodeBlocked {
					continue // blocking a blocked node is not a transition
				}
				doc[pairKey(string(s), to)] = true
			}
			continue
		}
		doc[pairKey(from, to)] = true
	}

	code := map[string]bool{}
	for _, e := range nodeTable {
		code[pairKey(string(e.from), string(e.to))] = true
	}
	for _, s := range nonFinal {
		if s != NodeBlocked {
			code[pairKey(string(s), string(NodeBlocked))] = true
		}
		code[pairKey(string(s), string(NodeFailed))] = true
		code[pairKey(string(s), string(NodeCancelled))] = true
	}
	code[pairKey(string(NodeBlocked), "previous")] = true

	compare(t, doc, code)
}

func TestWorkspaceTableMatchesContract(t *testing.T) {
	active := []WorkspaceState{}
	for _, s := range WorkspaceStates {
		if s.Active() {
			active = append(active, s)
		}
	}

	doc := map[string]bool{}
	for _, r := range readTable(t, "## Workspace") {
		from, to := r[0], r[1]
		switch from {
		case "—":
			doc[pairKey("none", to)] = true
		case "ativo":
			for _, s := range active {
				doc[pairKey(string(s), to)] = true
			}
		default:
			doc[pairKey(from, to)] = true
		}
	}

	code := map[string]bool{}
	for _, e := range workspaceTable {
		from := string(e.from)
		if e.from == WorkspaceNone {
			from = "none"
		}
		code[pairKey(from, string(e.to))] = true
	}
	for _, s := range active {
		code[pairKey(string(s), string(WorkspaceCancelled))] = true
		code[pairKey(string(s), string(WorkspaceFailed))] = true
	}

	compare(t, doc, code)
}
