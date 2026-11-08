package ports

// Files reads the worktree of a node for the evidence check of the core
// (ADR 0023, item 5): the core cites no file it has not seen through this port.
type Files interface {
	// Lines returns how many lines the file at path has: the number of line
	// breaks, plus one when the last line has none. path is relative to the
	// worktree, with forward slashes. ok is false when the path is absolute,
	// leaves the worktree, does not exist or is not a regular file; err is
	// for failures to read.
	Lines(path string) (n int, ok bool, err error)
}
