// Package proc runs one bounded child process in its own process group.
//
// The child is started without a shell. On timeout or stdout overflow the
// whole process group created for that child is sent SIGTERM, given one
// second, then sent SIGKILL. Run always joins the child before returning.
package proc

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Failure kinds returned by Run.
var (
	ErrTimeout     = errors.New("command timeout")
	ErrOutputLimit = errors.New("command output limit")
	ErrStart       = errors.New("command start failed")
)

// ExitError reports a child that ran to completion with a nonzero status.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "command exited nonzero" }

// Spec describes one child invocation.
type Spec struct {
	Path      string
	Args      []string // argv[1:]
	Env       []string
	Timeout   time.Duration
	StdoutCap int
	StderrCap int
}

// GracePeriod is the wait between SIGTERM and SIGKILL of an owned group.
var GracePeriod = time.Second

type cappedBuffer struct {
	mu       sync.Mutex
	buf      []byte
	limit    int
	exceeded bool
	signal   chan struct{}
	once     sync.Once
	discard  bool // stderr: keep bounded, never fail the command
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := c.limit - len(c.buf)
	if len(p) > room {
		if room > 0 {
			c.buf = append(c.buf, p[:room]...)
		}
		if !c.discard {
			c.exceeded = true
			c.once.Do(func() { close(c.signal) })
		}
		return len(p), nil
	}
	c.buf = append(c.buf, p...)
	return len(p), nil
}

// Run executes spec under ctx and returns captured stdout. Stderr is captured
// only up to its cap and is never returned: callers must not publish it.
func Run(ctx context.Context, spec Spec) ([]byte, error) {
	cmd := &exec.Cmd{Path: spec.Path, Args: append([]string{spec.Path}, spec.Args...), Env: spec.Env}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout := &cappedBuffer{limit: spec.StdoutCap, signal: make(chan struct{})}
	stderr := &cappedBuffer{limit: spec.StderrCap, signal: make(chan struct{}), discard: true}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, ErrStart
	}
	pgid := cmd.Process.Pid
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(spec.Timeout)
	defer timer.Stop()

	var failure error
	var waitErr error
	select {
	case waitErr = <-done:
	case <-timer.C:
		failure = ErrTimeout
	case <-ctx.Done():
		failure = ErrTimeout
	case <-stdout.signal:
		failure = ErrOutputLimit
	}
	if failure != nil {
		// The group cannot have been reused: Wait has not returned, so either
		// the leader is unreaped or a member still holds its stdio pipes.
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(GracePeriod):
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
			<-done
		}
		return nil, failure
	}
	stdout.mu.Lock()
	exceeded := stdout.exceeded
	out := stdout.buf
	stdout.mu.Unlock()
	if exceeded {
		return nil, ErrOutputLimit
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			return nil, &ExitError{Code: ee.ExitCode()}
		}
		return nil, ErrStart
	}
	return out, nil
}
