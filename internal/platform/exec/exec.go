//go:build unix

// Package exec runs external programs in their own process group, so that
// canceling one kills it and everything it started. It is the only place
// that imports os/exec (docs/architecture/packages.md, rule 4).
package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	osexec "os/exec"
	"sync"
	"syscall"
	"time"
)

// stderrKeep is how much of the end of stderr a process remembers.
const stderrKeep = 8 << 10

// Spec describes a program to run.
type Spec struct {
	Bin  string
	Args []string
	Dir  string
	Env  []string // the complete environment; nothing is inherited
}

// ErrNotFound means the program is not in the PATH of Kluzo.
var ErrNotFound = osexec.ErrNotFound

// Status is how a process ended.
type Status struct {
	Code   int    // exit code; -1 when a signal ended it
	Signal string // name of the signal that ended it, if any
}

// Process is a running program.
type Process struct {
	cmd    *osexec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr *tail
	exited chan struct{}
}

// Start launches the program in a new process group with a pipe on stdin and
// stdout. The caller reads stdout to the end before calling Wait.
func Start(spec Spec) (*Process, error) {
	cmd := osexec.Command(spec.Bin, spec.Args...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	if cmd.Env == nil {
		cmd.Env = []string{} // never inherit the environment of Kluzo
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p := &Process{cmd: cmd, stderr: &tail{}, exited: make(chan struct{})}
	cmd.Stderr = p.stderr
	var err error
	if p.stdin, err = cmd.StdinPipe(); err != nil {
		return nil, fmt.Errorf("stdin of %s: %w", spec.Bin, err)
	}
	if p.stdout, err = cmd.StdoutPipe(); err != nil {
		return nil, fmt.Errorf("stdout of %s: %w", spec.Bin, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", spec.Bin, err)
	}
	return p, nil
}

// Stdin is the pipe to the program's standard input.
func (p *Process) Stdin() io.WriteCloser { return p.stdin }

// Stdout is the program's standard output.
func (p *Process) Stdout() io.Reader { return p.stdout }

// Stderr returns the last part of the program's standard error.
func (p *Process) Stderr() string { return p.stderr.String() }

// Wait waits for the program to end, after stdout was read to the end. It then
// kills whatever is left of the process group.
func (p *Process) Wait() Status {
	err := p.cmd.Wait()
	close(p.exited)
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL) // orphans; ESRCH when none
	st := Status{Code: p.cmd.ProcessState.ExitCode()}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			st.Signal = ws.Signal().String()
		}
	}
	return st
}

// Terminate asks the whole process group to stop with SIGTERM and, if it is
// still running after grace, kills it with SIGKILL. It does not wait.
func (p *Process) Terminate(grace time.Duration) {
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	go func() {
		select {
		case <-p.exited:
		case <-time.After(grace):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}()
}

// Output is what a program run by Run left.
type Output struct {
	Stdout string
	Stderr string // the end of it, as Process.Stderr keeps
	Status Status // Code -1 when the program did not start
}

// Run runs a short program to the end, with stdin closed. The error is the
// failure to start it, the end of ctx (which kills the process group) or a
// failure to read its output; a non-zero exit is only in the Status.
func Run(ctx context.Context, spec Spec) (Output, error) {
	p, err := Start(spec)
	if err != nil {
		return Output{Status: Status{Code: -1}}, err
	}
	_ = p.stdin.Close()
	stop := context.AfterFunc(ctx, func() { p.Terminate(0) })
	defer stop()
	out, readErr := io.ReadAll(p.stdout)
	st := p.Wait()
	o := Output{Stdout: string(out), Stderr: p.Stderr(), Status: st}
	if err := ctx.Err(); err != nil {
		return o, err
	}
	return o, readErr
}

// tail keeps the end of what is written to it.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > stderrKeep {
		t.buf = t.buf[len(t.buf)-stderrKeep:]
	}
	return len(b), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// RunTo runs a program to the end with its standard output and error both
// written to w, and stdin empty. When ctx ends, the process group is killed.
// Once the program exits, its children that still hold the output get grace
// before the pipes are closed and the group is killed: a server left in the
// background does not hold the caller. The error is the failure to start it
// or the end of ctx; a non-zero exit is only in the Status.
func RunTo(ctx context.Context, spec Spec, w io.Writer, grace time.Duration) (Status, error) {
	cmd := osexec.Command(spec.Bin, spec.Args...)
	cmd.Dir, cmd.Env = spec.Dir, spec.Env
	if cmd.Env == nil {
		cmd.Env = []string{}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = w, w
	cmd.WaitDelay = grace
	if err := cmd.Start(); err != nil {
		return Status{Code: -1}, fmt.Errorf("start %s: %w", spec.Bin, err)
	}
	pgid := cmd.Process.Pid
	stop := context.AfterFunc(ctx, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
	err := cmd.Wait()
	stop()
	_ = syscall.Kill(-pgid, syscall.SIGKILL) // what is left of the group
	st := Status{Code: cmd.ProcessState.ExitCode()}
	var ee *osexec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			st.Signal = ws.Signal().String()
		}
	}
	return st, ctx.Err()
}
