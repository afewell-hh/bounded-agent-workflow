// Package proc runs one bounded child process in its own process group.
//
// The child is started without a shell, with stdin from /dev/null and stdout/
// stderr connected to pipes owned by Run. Its process group (PGID = leader
// PID) is created for this invocation only.
//
// Supervision boundary. The leader is never reaped until cleanup of its group
// is finished: its exit is observed without reaping (kqueue NOTE_EXIT on
// darwin), so the leader PID, and therefore the group ID, cannot be reused by
// another process while Run may still signal it. Run never signals any other
// process ID or group. After the leader exits, for any reason, Run checks
// whether the group still has signalable members; if so, or on timeout or an
// output cap, the group (and the unreaped leader) receive SIGTERM, then
// SIGKILL after GracePeriod. Run then waits at most JoinBound for the group to
// empty and for both pipes to reach EOF. If either cannot be established it
// returns a failure, never captured output.
//
// Not covered: a descendant that moves itself to another process group or
// session (setsid/setpgid) and closes the inherited pipes cannot be
// identified and is never signalled. If it still holds a pipe, Run detects
// that, stops waiting after JoinBound and fails. Group members this user
// cannot signal (for example after a privilege change) are not visible to the
// membership check. On platforms without an exit watcher Run fails closed.
package proc

import (
	"context"
	"errors"
	"os"
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
	// ErrCleanup reports that the owned group or its pipes could not be
	// confirmed closed within JoinBound after termination.
	ErrCleanup = errors.New("command cleanup not established")
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

// JoinBound bounds each final wait: group extinction after SIGKILL, and pipe
// EOF after the group is gone.
var JoinBound = time.Second

const pollInterval = 10 * time.Millisecond

// capture drains one pipe, keeping at most limit bytes. Bytes beyond the limit
// are read and discarded so writers never block, and mark the stream exceeded.
type capture struct {
	mu       sync.Mutex
	buf      []byte
	keep     bool // stdout is kept; stderr is only counted, never stored
	n        int
	limit    int
	exceeded bool
	over     chan struct{}
	done     chan struct{}
}

func newCapture(f *os.File, limit int, keep bool) *capture {
	c := &capture{limit: limit, keep: keep, over: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(c.done)
		b := make([]byte, 32<<10)
		for {
			n, err := f.Read(b)
			if n > 0 {
				c.add(b[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	return c
}

func (c *capture) add(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exceeded {
		return
	}
	if c.n+len(p) > c.limit {
		c.exceeded = true
		c.buf = nil
		close(c.over)
		return
	}
	c.n += len(p)
	if c.keep {
		c.buf = append(c.buf, p...)
	}
}

func (c *capture) result() ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf, c.exceeded
}

// groupLive reports whether the owned group has a member this process may
// signal. With only the unreaped (zombie) leader left, darwin returns EPERM.
func groupLive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }

// signalOwned signals the owned group and its unreaped leader. Both IDs are
// pinned by the unreaped leader, so neither can name another process.
func signalOwned(pgid int, sig syscall.Signal) {
	_ = syscall.Kill(-pgid, sig)
	_ = syscall.Kill(pgid, sig)
}

// waitGone polls until the leader has exited and the group has no signalable
// member, or until d elapses.
func waitGone(pgid int, exited <-chan struct{}, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		select {
		case <-exited:
			if !groupLive(pgid) {
				return true
			}
		default:
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
}

func closeAll(fs ...*os.File) {
	for _, f := range fs {
		f.Close()
	}
}

// Run executes spec under ctx and returns captured stdout. Stderr is counted
// against its cap and never stored or returned.
func Run(ctx context.Context, spec Spec) ([]byte, error) {
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, ErrStart
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(outR, outW)
		return nil, ErrStart
	}
	cmd := &exec.Cmd{Path: spec.Path, Args: append([]string{spec.Path}, spec.Args...), Env: spec.Env,
		Stdout: outW, Stderr: errW, SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	if err := cmd.Start(); err != nil {
		closeAll(outR, outW, errR, errW)
		return nil, ErrStart
	}
	closeAll(outW, errW)
	pgid := cmd.Process.Pid

	exited, err := watchExit(pgid)
	if err != nil {
		// Without an exit watcher the group cannot be supervised safely. The
		// leader is still unreaped, so its group may be signalled.
		signalOwned(pgid, syscall.SIGKILL)
		closeAll(outR, errR)
		go cmd.Wait()
		return nil, ErrStart
	}
	stdout := newCapture(outR, spec.StdoutCap, true)
	stderr := newCapture(errR, spec.StderrCap, false)

	timer := time.NewTimer(spec.Timeout)
	defer timer.Stop()

	var failure error
	select {
	case <-exited:
	case <-timer.C:
		failure = ErrTimeout
	case <-ctx.Done():
		failure = ErrTimeout
	case <-stdout.over:
		failure = ErrOutputLimit
	case <-stderr.over:
		failure = ErrOutputLimit
	}

	// Cleanup: leader exit is not group extinction.
	cleaned := true
	if failure != nil || groupLive(pgid) {
		signalOwned(pgid, syscall.SIGTERM)
		if !waitGone(pgid, exited, GracePeriod) {
			signalOwned(pgid, syscall.SIGKILL)
			cleaned = waitGone(pgid, exited, JoinBound)
		}
	}
	// Pipes reach EOF only when no process holds them, including one that
	// left the group. Bound that wait, then unblock the readers.
	pipesClosed := true
	deadline := time.NewTimer(JoinBound)
	for _, c := range []*capture{stdout, stderr} {
		select {
		case <-c.done:
		case <-deadline.C:
			pipesClosed = false
		}
		if !pipesClosed {
			break
		}
	}
	deadline.Stop()
	closeAll(outR, errR)
	<-stdout.done
	<-stderr.done

	// Reap the leader only now. If it never exited, reap it in the background
	// and never signal it again.
	var waitErr error
	select {
	case <-exited:
		waitErr = cmd.Wait()
	default:
		cleaned = false
		go cmd.Wait()
	}

	out, outOver := stdout.result()
	_, errOver := stderr.result()
	switch {
	case failure != nil:
		return nil, failure
	case outOver || errOver:
		return nil, ErrOutputLimit
	case !cleaned || !pipesClosed:
		return nil, ErrCleanup
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
