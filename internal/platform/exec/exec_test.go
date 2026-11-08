//go:build unix

package exec

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var sh = Spec{Bin: "/bin/sh", Env: []string{"PATH=/usr/bin:/bin", "KEEP=yes"}}

func script(s string) Spec {
	spec := sh
	spec.Args = []string{"-c", s}
	return spec
}

func TestRunReturnsOutputAndStatus(t *testing.T) {
	out, st, err := Run(context.Background(), script(`echo "$KEEP"; exit 3`))
	if err != nil || st.Code != 3 || out != "yes\n" {
		t.Errorf("out %q, status %+v, err %v", out, st, err)
	}
	if _, _, err := Run(context.Background(), Spec{Bin: "/nonexistent/bin"}); err == nil {
		t.Error("missing binary accepted")
	}
}

func TestEnvironmentIsNotInherited(t *testing.T) {
	t.Setenv("SECRET_FROM_PARENT", "leak")
	out, _, err := Run(context.Background(), script(`echo "[$SECRET_FROM_PARENT]"`))
	if err != nil || out != "[]\n" {
		t.Errorf("out %q, err %v", out, err)
	}
}

func TestStdinStdoutAndStderrTail(t *testing.T) {
	p, err := Start(script(`read line; echo "got $line"; echo oops >&2; exit 1`))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(p.Stdin(), "hello\n")
	out, _ := io.ReadAll(p.Stdout())
	st := p.Wait()
	if string(out) != "got hello\n" || st.Code != 1 || !strings.Contains(p.Stderr(), "oops") {
		t.Errorf("out %q, status %+v, stderr %q", out, st, p.Stderr())
	}
}

func TestStderrKeepsOnlyTheEnd(t *testing.T) {
	tl := &tail{}
	for i := 0; i < 3000; i++ {
		_, _ = tl.Write([]byte("line-" + strconv.Itoa(i) + "-xxxxxxxxxx\n"))
	}
	if got := tl.String(); len(got) > stderrKeep || !strings.HasSuffix(got, "line-2999-xxxxxxxxxx\n") {
		t.Errorf("tail has %d bytes, ends %q", len(got), got[max(0, len(got)-30):])
	}
}

// Terminate stops the whole group: the child and the grandchild it started.
func TestTerminateKillsTheProcessGroup(t *testing.T) {
	pidFile := t.TempDir() + "/grandchild.pid"
	p, err := Start(script(`sleep 60 & echo $! > ` + pidFile + `; wait`))
	if err != nil {
		t.Fatal(err)
	}
	var pid int
	for range 100 {
		if b, err := os.ReadFile(pidFile); err == nil && strings.TrimSpace(string(b)) != "" {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if pid == 0 {
		t.Fatal("grandchild did not start")
	}
	p.Terminate(time.Second)
	_, _ = io.ReadAll(p.Stdout())
	st := p.Wait()
	if st.Signal == "" {
		t.Errorf("status = %+v, want a signal", st)
	}
	waitGone(t, pid)
}

// A process that ignores SIGTERM is killed after the grace period.
func TestTerminateEscalatesToKill(t *testing.T) {
	p, err := Start(script(`trap '' TERM; echo ready; while true; do sleep 0.05; done`))
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	_, _ = io.ReadFull(p.Stdout(), buf)
	start := time.Now()
	p.Terminate(200 * time.Millisecond)
	_, _ = io.ReadAll(p.Stdout())
	st := p.Wait()
	if st.Signal != "killed" || time.Since(start) < 150*time.Millisecond {
		t.Errorf("status %+v after %v, want SIGKILL after the grace", st, time.Since(start))
	}
}

func TestRunStopsWithTheContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, _, err := Run(ctx, script(`sleep 30`))
	if err == nil || time.Since(start) > 5*time.Second {
		t.Errorf("err %v after %v", err, time.Since(start))
	}
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	for range 100 {
		if err := syscall.Kill(pid, 0); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("process %d still alive", pid)
}
