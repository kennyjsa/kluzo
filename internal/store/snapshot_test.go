package store

import (
	"errors"
	"testing"
)

// The one lookup of a node in a snapshot, by id and by issue.
func TestSnapshotNode(t *testing.T) {
	s := Snapshot{Nodes: []NodeRow{{ID: "nd_1", IssueRef: "7"}, {ID: "nd_2", IssueRef: "9"}}}
	if n, err := s.Node("nd_2"); err != nil || n.IssueRef != "9" {
		t.Errorf("Node(nd_2) = %+v, %v", n, err)
	}
	if n, err := s.NodeByIssue("7"); err != nil || n.ID != "nd_1" {
		t.Errorf("NodeByIssue(7) = %+v, %v", n, err)
	}
	if _, err := s.Node("nd_3"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Node(nd_3) = %v", err)
	}
	if _, err := s.NodeByIssue("8"); !errors.Is(err, ErrNotFound) || err.Error() != "not found: no node for issue #8" {
		t.Errorf("NodeByIssue(8) = %v", err)
	}
}
