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
//
// RunObserved shares the same supervision and additionally reports the facts
// it established (Observation); it adds no containment beyond the above.
// RunObservedInput is RunObserved with an optional regular-file stdin and an
// optional bounded stdout observer; Run and RunObserved keep /dev/null.
package proc

import (
	"context"
	"errors"
	"io"
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
	Dir       string // working directory; empty keeps the caller's
	Timeout   time.Duration
	StdoutCap int
	StderrCap int
}

// Observation holds the facts RunObserved established about one invocation.
// They are independent of the legacy error value.
type Observation struct {
	Started       bool // Start returned success
	Exited        bool // a normal exit status is known
	ExitCode      int  // valid only when Exited
	Signaled      bool // the leader terminated by a signal
	Joined        bool // the leader was reaped synchronously
	GroupAbsent   bool // no signalable owned-group member remained before reaping
	StdoutEOF     bool // stdout reached actual EOF
	StderrEOF     bool // stderr reached actual EOF
	WatcherFailed bool // exit watcher setup or runtime failure
	TimedOut      bool
	Cancelled     bool
	OutputLimit   bool
}

// Usable reports whether the invocation started, exited normally, was
// joined, left no signalable owned-group member and closed both pipes with
// actual EOF, without timeout, cancellation, output cap or watcher failure.
// Escaped or unsignalable descendants are outside these facts.
func (o Observation) Usable() bool {
	return o.Started && o.Exited && !o.Signaled && o.Joined && o.GroupAbsent &&
		o.StdoutEOF && o.StderrEOF && !o.WatcherFailed && !o.TimedOut &&
		!o.Cancelled && !o.OutputLimit
}

// GracePeriod is the wait between SIGTERM and SIGKILL of an owned group.
var GracePeriod = time.Second

// JoinBound bounds each final wait: group extinction after SIGKILL, and pipe
// EOF after the group is gone.
var JoinBound = time.Second

const pollInterval = 10 * time.Millisecond

// Internal test seams; production code never sets them.
var (
	watchSetupFault   func() error
	watchRuntimeFault func() <-chan struct{}
	// pipeReader may wrap a pipe's reader, e.g. to inject a non-EOF read
	// error at the reader boundary.
	pipeReader func(r io.Reader, stdout bool) io.Reader
)

// capture drains one pipe, keeping at most limit bytes. Bytes beyond the limit
// are read and discarded so writers never block, and mark the stream exceeded.
type capture struct {
	mu             sync.Mutex
	buf            []byte
	keep           bool // stdout is kept; stderr is only counted, never stored
	n              int
	limit          int
	exceeded       bool
	observerFailed bool // written by reader, read only after done
	eof            bool // the reader ended on actual EOF, not a forced close or error
	over           chan struct{}
	done           chan struct{}
}

func newCapture(f *os.File, limit int, keep bool) *capture {
	return newObservedCapture(f, limit, keep, nil, nil)
}

// newObservedCapture is newCapture that also hands each accepted chunk, as a
// copy and outside the capture lock, to observe. A chunk is counted against
// the cap before it is dispatched; a chunk over the cap is never dispatched.
// After observe returns an error it is never called again and stop is called
// once. Only the reader goroutine calls observe, so it is never running after
// done is closed.
func newObservedCapture(f *os.File, limit int, keep bool, observe func([]byte) error, stop func()) *capture {
	c := &capture{limit: limit, keep: keep, over: make(chan struct{}), done: make(chan struct{})}
	var r io.Reader = f
	if pipeReader != nil {
		r = pipeReader(f, keep)
	}
	go func() {
		defer close(c.done)
		b := make([]byte, 32<<10)
		for {
			n, err := r.Read(b)
			if n > 0 {
				if c.add(b[:n]) && observe != nil {
					if observe(append([]byte(nil), b[:n]...)) != nil {
						observe = nil
						c.observerFailed = true
						stop()
					}
				}
			}
			if err != nil {
				if err == io.EOF {
					c.mu.Lock()
					c.eof = true
					c.mu.Unlock()
				}
				return
			}
		}
	}()
	return c
}

// add counts p and reports whether it was accepted within the cap.
func (c *capture) add(p []byte) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.exceeded {
		return false
	}
	if c.n+len(p) > c.limit {
		c.exceeded = true
		c.buf = nil
		close(c.over)
		return false
	}
	c.n += len(p)
	if c.keep {
		c.buf = append(c.buf, p...)
	}
	return true
}

func (c *capture) result() ([]byte, bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf, c.exceeded, c.eof
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
	out, _, err := run(ctx, spec, false, nil)
	return out, err
}

// RunObserved is Run that also reports the facts it established. Unlike Run
// it checks ctx before Start (a cancelled command is never started) and it
// treats an exit-watcher runtime failure as a failure instead of waiting for
// the timeout. Its error value follows Run; use the Observation for
// classification.
func RunObserved(ctx context.Context, spec Spec) ([]byte, Observation, error) {
	return run(ctx, spec, true, nil)
}

// input holds the optional per-call inputs of RunObservedInput.
type input struct {
	stdin   *os.File
	observe func([]byte) error
	stop    func()
}

// RunObservedInput is RunObserved with two optional per-call inputs. A
// non-nil packet must be an open regular file; it becomes the child's stdin
// from its current offset (no feeder pipe or goroutine) and the caller keeps
// and closes its own handle after return. A nil packet keeps /dev/null. A
// non-nil stdoutObserver receives copies of accepted stdout chunks in order,
// outside capture locks, from the stdout reader only; it must not block. Its
// first error cancels this invocation's context, so the existing cleanup
// applies (Cancelled is set even if exit wins the initial select),
// and it is never called again or after return. Captures, caps, group
// ownership and the Observation are those of RunObserved.
func RunObservedInput(ctx context.Context, spec Spec, packet *os.File, stdoutObserver func([]byte) error) ([]byte, Observation, error) {
	if packet != nil {
		fi, err := packet.Stat()
		if err != nil || !fi.Mode().IsRegular() {
			return nil, Observation{}, ErrStart
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return run(ctx, spec, true, &input{stdin: packet, observe: stdoutObserver, stop: cancel})
}

func run(ctx context.Context, spec Spec, observe bool, in *input) ([]byte, Observation, error) {
	var obs Observation
	if observe && ctx.Err() != nil {
		obs.Cancelled = true
		return nil, obs, ErrStart
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, obs, ErrStart
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		closeAll(outR, outW)
		return nil, obs, ErrStart
	}
	cmd := &exec.Cmd{Path: spec.Path, Args: append([]string{spec.Path}, spec.Args...), Env: spec.Env,
		Dir: spec.Dir, Stdout: outW, Stderr: errW, SysProcAttr: &syscall.SysProcAttr{Setpgid: true}}
	if in != nil && in.stdin != nil {
		cmd.Stdin = in.stdin // an *os.File is passed directly as fd 0
	}
	if err := cmd.Start(); err != nil {
		closeAll(outR, outW, errR, errW)
		return nil, obs, ErrStart
	}
	obs.Started = true
	closeAll(outW, errW)
	pgid := cmd.Process.Pid

	exited, watchFailed, err := watchExit(pgid)
	if err == nil && watchSetupFault != nil {
		err = watchSetupFault()
	}
	if err != nil {
		// Without an exit watcher the group cannot be supervised safely. The
		// leader is still unreaped, so its group may be signalled.
		signalOwned(pgid, syscall.SIGKILL)
		closeAll(outR, errR)
		go cmd.Wait()
		obs.WatcherFailed = true
		return nil, obs, ErrStart
	}
	switch {
	case !observe:
		watchFailed = nil // Run keeps waiting for its timeout, as before.
	case watchRuntimeFault != nil:
		watchFailed = watchRuntimeFault()
	}
	var stdout *capture
	if in != nil && in.observe != nil {
		stdout = newObservedCapture(outR, spec.StdoutCap, true, in.observe, in.stop)
	} else {
		stdout = newCapture(outR, spec.StdoutCap, true)
	}
	stderr := newCapture(errR, spec.StderrCap, false)

	timer := time.NewTimer(spec.Timeout)
	defer timer.Stop()

	var failure error
	select {
	case <-exited:
	case <-timer.C:
		failure = ErrTimeout
		obs.TimedOut = true
	case <-ctx.Done():
		failure = ErrTimeout
		obs.Cancelled = true
	case <-stdout.over:
		failure = ErrOutputLimit
	case <-stderr.over:
		failure = ErrOutputLimit
	case <-watchFailed:
		failure = ErrCleanup
		obs.WatcherFailed = true
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
		obs.GroupAbsent = cleaned
		waitErr = cmd.Wait()
		obs.Joined = true
		if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
			switch {
			case ws.Exited():
				obs.Exited, obs.ExitCode = true, ws.ExitStatus()
			case ws.Signaled():
				obs.Signaled = true
			}
		}
	default:
		cleaned = false
		go cmd.Wait()
	}

	// The leader may have exited before the callback cancels the context.
	// An observer rejection still independently disqualifies completion.
	if stdout.observerFailed {
		obs.Cancelled = true
		if failure == nil {
			failure = ErrTimeout
		}
	}
	out, outOver, outEOF := stdout.result()
	_, errOver, errEOF := stderr.result()
	obs.OutputLimit = outOver || errOver
	obs.StdoutEOF, obs.StderrEOF = outEOF, errEOF
	switch {
	case failure != nil:
		return nil, obs, failure
	case outOver || errOver:
		return nil, obs, ErrOutputLimit
	case !cleaned || !pipesClosed:
		return nil, obs, ErrCleanup
	}
	if waitErr != nil {
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			return nil, obs, &ExitError{Code: ee.ExitCode()}
		}
		return nil, obs, ErrStart
	}
	return out, obs, nil
}
