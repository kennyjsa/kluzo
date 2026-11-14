package claudecode

import (
	"os"
	"testing"
)

// TestMain lets the test binary play the part of the claude CLI: the wrapper
// scripts of the tests run it with KLUZO_FAKE_CLAUDE=1.
func TestMain(m *testing.M) {
	if os.Getenv("KLUZO_FAKE_CLAUDE") == "1" {
		os.Exit(fakeClaude(os.Args[1:]))
	}
	os.Exit(m.Run())
}
