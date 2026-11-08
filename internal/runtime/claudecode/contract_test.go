package claudecode

import (
	"testing"

	"github.com/kennyjsa/kluzo/internal/ports"
	"github.com/kennyjsa/kluzo/internal/ports/contracttest"
)

// The adapter passes the same suite as the in-memory fake.
func TestRuntimeContract(t *testing.T) {
	e := newEnv(t)
	fixture := map[contracttest.Scenario]string{contracttest.Finish: "success", contracttest.Hang: "hang", contracttest.Crash: "crash"}
	contracttest.RunRuntime(t, contracttest.RuntimeEnv{
		Runtime: e.rt,
		Spec: func(_ *testing.T, s contracttest.Scenario) ports.SessionSpec {
			return e.spec(fixture[s], nil)
		},
	})
}
