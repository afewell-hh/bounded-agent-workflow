package proc

import (
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

const proofPID = 4343 // never passed to a real syscall

// proofStep is one scripted waitid result. cost advances the fake clock
// during the call.
type proofStep struct {
	r     uintptr
	errno syscall.Errno
	info  siginfo
	cost  time.Duration
}

// fakeProof scripts one proof's waitid, clock and sleeper. Each sleep advances
// the clock by the requested duration plus extra.
type fakeProof struct {
	steps   []proofStep
	extra   time.Duration
	base    time.Time
	elapsed time.Duration
	log     []string
}

func (f *fakeProof) ops() proofOps {
	return proofOps{
		waitid: func(pid int, info *siginfo) (uintptr, syscall.Errno) {
			if *info != (siginfo{}) {
				f.log = append(f.log, fmt.Sprintf("call %d at %v dirty", pid, f.elapsed))
			} else {
				f.log = append(f.log, fmt.Sprintf("call %d at %v", pid, f.elapsed))
			}
			if len(f.steps) == 0 {
				f.log = append(f.log, "exhausted")
				return ^uintptr(0), syscall.EINVAL
			}
			s := f.steps[0]
			f.steps = f.steps[1:]
			*info = s.info
			f.elapsed += s.cost
			return s.r, s.errno
		},
		now: func() time.Time { return f.base.Add(f.elapsed) },
		sleep: func(d time.Duration) {
			f.log = append(f.log, fmt.Sprintf("sleep %v", d))
			f.elapsed += d + f.extra
		},
	}
}

// noState is a 0 result with si_pid 0. Every other field carries stale junk
// that the next call must not see.
func noState(cost time.Duration) proofStep {
	junk := siginfo{Signo: sigCHLD, Errno: 9, Code: cldExited, Uid: 501, Status: 7, Addr: 1, Value: 2, Band: 3}
	for i := range junk.Pad {
		junk.Pad[i] = ^uint64(0)
	}
	return proofStep{info: junk, cost: cost}
}

func childState(pid int32, signo, code int32, cost time.Duration) proofStep {
	return proofStep{info: siginfo{Signo: signo, Code: code, Pid: pid, Status: 1}, cost: cost}
}

func terminal(code int32, cost time.Duration) proofStep {
	return childState(proofPID, sigCHLD, code, cost)
}

// TestExitProofSequence drives proveExitedOps with scripted waitid results and
// a per-call fake clock and sleeper. The expected call/sleep logs are written
// by hand from the frozen bounds (5 calls, 1 ms spacing, 10 ms budget).
func TestExitProofSequence(t *testing.T) {
	ms := time.Millisecond
	call := func(at time.Duration) string { return fmt.Sprintf("call %d at %v", proofPID, at) }
	sleep := func(d time.Duration) string { return fmt.Sprintf("sleep %v", d) }
	for _, c := range []struct {
		name  string
		steps []proofStep
		extra time.Duration
		want  bool
		log   []string
	}{
		{name: "zero-then-exited", steps: []proofStep{noState(0), terminal(cldExited, 0)}, want: true,
			log: []string{call(0), sleep(ms), call(ms)}},
		{name: "exited", steps: []proofStep{terminal(cldExited, 0)}, want: true, log: []string{call(0)}},
		{name: "killed", steps: []proofStep{terminal(cldKilled, 0)}, want: true, log: []string{call(0)}},
		{name: "dumped", steps: []proofStep{terminal(cldDumped, 0)}, want: true, log: []string{call(0)}},
		{name: "zero-until-attempt-bound",
			steps: []proofStep{noState(0), noState(0), noState(0), noState(0), noState(0), terminal(cldExited, 0)},
			log:   []string{call(0), sleep(ms), call(ms), sleep(ms), call(2 * ms), sleep(ms), call(3 * ms), sleep(ms), call(4 * ms)}},
		{name: "zero-then-stopped", steps: []proofStep{noState(0), terminal(5, 0), terminal(cldExited, 0)},
			log: []string{call(0), sleep(ms), call(ms)}},
		{name: "trapped", steps: []proofStep{terminal(4, 0), terminal(cldExited, 0)}, log: []string{call(0)}},
		{name: "stopped", steps: []proofStep{terminal(5, 0), terminal(cldExited, 0)}, log: []string{call(0)}},
		{name: "continued", steps: []proofStep{terminal(6, 0), terminal(cldExited, 0)}, log: []string{call(0)}},
		{name: "code-zero", steps: []proofStep{terminal(0, 0), terminal(cldExited, 0)}, log: []string{call(0)}},
		{name: "code-unknown", steps: []proofStep{terminal(7, 0), terminal(cldExited, 0)}, log: []string{call(0)}},
		{name: "other-pid", steps: []proofStep{childState(proofPID+1, sigCHLD, cldExited, 0), terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "negative-pid", steps: []proofStep{childState(-1, sigCHLD, cldExited, 0), terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "wrong-signal", steps: []proofStep{childState(proofPID, sigCHLD-1, cldExited, 0), terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "signal-zero", steps: []proofStep{childState(proofPID, 0, cldExited, 0), terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "eintr", steps: []proofStep{{r: ^uintptr(0), errno: syscall.EINTR}, terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "zero-then-eintr", steps: []proofStep{noState(0), {r: ^uintptr(0), errno: syscall.EINTR}, terminal(cldExited, 0)},
			log: []string{call(0), sleep(ms), call(ms)}},
		{name: "echild", steps: []proofStep{{r: ^uintptr(0), errno: syscall.ECHILD}, terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "error-with-terminal-info", steps: []proofStep{{r: ^uintptr(0), errno: syscall.EINVAL,
			info: siginfo{Signo: sigCHLD, Code: cldExited, Pid: proofPID}}}, log: []string{call(0)}},
		{name: "nonzero-result", steps: []proofStep{{r: 1, info: siginfo{Signo: sigCHLD, Code: cldExited, Pid: proofPID}}},
			log: []string{call(0)}},
		// Deadline controls.
		{name: "terminal-exactly-at-deadline", steps: []proofStep{terminal(cldExited, 10*ms)}, want: true,
			log: []string{call(0)}},
		{name: "terminal-after-deadline", steps: []proofStep{terminal(cldExited, 10*ms+1)},
			log: []string{call(0)}},
		{name: "second-terminal-exactly-at-deadline", steps: []proofStep{noState(8 * ms), terminal(cldExited, ms)}, want: true,
			log: []string{call(0), sleep(ms), call(9 * ms)}},
		{name: "second-terminal-after-deadline", steps: []proofStep{noState(8 * ms), terminal(cldExited, ms+1)},
			log: []string{call(0), sleep(ms), call(9 * ms)}},
		{name: "no-call-at-deadline", steps: []proofStep{noState(9 * ms), terminal(cldExited, 0)},
			log: []string{call(0), sleep(ms)}},
		{name: "sleep-clamped-to-budget", steps: []proofStep{noState(9*ms + 500*time.Microsecond), terminal(cldExited, 0)},
			log: []string{call(0), sleep(500 * time.Microsecond)}},
		{name: "no-sleep-when-budget-spent", steps: []proofStep{noState(10 * ms), terminal(cldExited, 0)},
			log: []string{call(0)}},
		{name: "late-sleeps-limit-calls", extra: 3 * ms,
			steps: []proofStep{noState(0), noState(0), noState(0), terminal(cldExited, 0)},
			log:   []string{call(0), sleep(ms), call(4 * ms), sleep(ms), call(8 * ms), sleep(ms)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeProof{steps: c.steps, extra: c.extra, base: time.Unix(1000, 0)}
			got := proveExitedOps(proofPID, f.ops())
			if got != c.want || !reflect.DeepEqual(f.log, c.log) {
				t.Errorf("got %v with log %q, want %v with %q", got, f.log, c.want, c.log)
			}
		})
	}

	for _, pid := range []int{0, -1, math.MaxInt32 + 1} {
		t.Run(fmt.Sprintf("invalid-pid-%d", pid), func(t *testing.T) {
			f := &fakeProof{steps: []proofStep{terminal(cldExited, 0)}, base: time.Unix(1000, 0)}
			if proveExitedOps(pid, f.ops()) || len(f.log) != 0 {
				t.Errorf("pid %d: log %q, want rejection with no call", pid, f.log)
			}
		})
	}

	// Negative control: a loop that reuses the buffer without zeroing it is
	// caught by the dirty-buffer check.
	t.Run("unzeroed-buffer-detected", func(t *testing.T) {
		f := &fakeProof{steps: []proofStep{noState(0), terminal(cldExited, 0)}, base: time.Unix(1000, 0)}
		ops := f.ops()
		var info siginfo
		ops.waitid(proofPID, &info)
		ops.waitid(proofPID, &info)
		want := []string{call(0), call(0) + " dirty"}
		if !reflect.DeepEqual(f.log, want) {
			t.Errorf("log %q, want %q", f.log, want)
		}
	})
}

// TestExitProofConstants pins the values passed to the kernel.
func TestExitProofConstants(t *testing.T) {
	got := []int{waitPPID, waitNoHang, waitExited, waitNoWait, sigCHLD, cldExited, cldKilled, cldDumped,
		syscall.SYS_WAITID, proofCalls}
	want := []int{1, 1, 4, 32, 20, 1, 2, 3, 173, 5}
	if !reflect.DeepEqual(got, want) || proofSpace != time.Millisecond || proofBudget != 10*time.Millisecond {
		t.Errorf("constants %v %v %v, want %v 1ms 10ms", got, proofSpace, proofBudget, want)
	}
	if unsafe.Sizeof(siginfo{}) != 104 || unsafe.Alignof(siginfo{}) != 8 ||
		unsafe.Offsetof(siginfo{}.Signo) != 0 || unsafe.Offsetof(siginfo{}.Code) != 8 ||
		unsafe.Offsetof(siginfo{}.Pid) != 12 || unsafe.Offsetof(siginfo{}.Status) != 20 ||
		unsafe.Offsetof(siginfo{}.Addr) != 24 || unsafe.Offsetof(siginfo{}.Value) != 32 ||
		unsafe.Offsetof(siginfo{}.Band) != 40 || unsafe.Offsetof(siginfo{}.Pad) != 48 {
		t.Error("siginfo layout differs from the pinned darwin/arm64 header")
	}
}

// ownedChild is a direct child of the test binary in the existing
// BAW_EXITWATCH_CHILD=stdin mode: it exits 0 at stdin EOF, or 3 after 20 s.
// It is signalled, continued, killed and joined only through its own handle.
type ownedChild struct {
	t         *testing.T
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	waited    chan error
	startWait sync.Once
	joined    bool
}

func startOwnedChild(t *testing.T) *ownedChild {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe)
	cmd.Env = []string{"BAW_EXITWATCH_CHILD=stdin"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c := &ownedChild{t: t, cmd: cmd, stdin: stdin, waited: make(chan error, 1)}
	t.Cleanup(c.cleanup)
	return c
}

func (c *ownedChild) wait() { c.startWait.Do(func() { go func() { c.waited <- c.cmd.Wait() }() }) }

// cleanup always releases and joins the child, bounded, on every path.
func (c *ownedChild) cleanup() {
	c.stdin.Close()
	if c.joined {
		return
	}
	c.cmd.Process.Signal(syscall.SIGCONT)
	c.wait()
	if _, ok := receiveErrWithin(c.waited, 25*time.Second); !ok {
		c.cmd.Process.Kill()
		if _, ok := receiveErrWithin(c.waited, bound); !ok {
			c.t.Errorf("child not joined")
		}
	}
}

// join closes stdin and reaps the child through its handle, requiring a
// normal exit 0, which also shows its exit status was not consumed earlier.
func (c *ownedChild) join() {
	c.t.Helper()
	c.stdin.Close()
	c.wait()
	err, ok := receiveErrWithin(c.waited, bound)
	if !ok {
		c.t.Fatal("child Wait did not return")
	}
	c.joined = true
	if err != nil || !c.cmd.ProcessState.Exited() || c.cmd.ProcessState.ExitCode() != 0 {
		c.t.Errorf("child Wait %v, state %v", err, c.cmd.ProcessState)
	}
}

// awaitState blocks in waitid(P_PID, pid, WNOWAIT|options) without reaping,
// from a goroutine, bounded; on timeout it kills the child through its handle
// and joins the call. It retries EINTR with a fresh buffer.
func (c *ownedChild) awaitState(options uintptr) siginfo {
	c.t.Helper()
	type result struct {
		info siginfo
		err  syscall.Errno
	}
	res := make(chan result, 1)
	pid := c.cmd.Process.Pid
	go func() {
		for {
			var info siginfo
			_, _, e := syscall.Syscall6(syscall.SYS_WAITID, waitPPID, uintptr(pid),
				uintptr(unsafe.Pointer(&info)), options|waitNoWait, 0, 0)
			if e == syscall.EINTR {
				continue
			}
			res <- result{info, e}
			return
		}
	}()
	timer := time.NewTimer(bound)
	defer timer.Stop()
	select {
	case r := <-res:
		if r.err != 0 {
			c.t.Fatalf("blocking waitid: %v", r.err)
		}
		return r.info
	case <-timer.C:
	}
	c.cmd.Process.Kill()
	late := time.NewTimer(bound)
	defer late.Stop()
	select {
	case <-res:
	case <-late.C:
		c.t.Error("blocking waitid not joined")
	}
	c.t.Fatal("child did not reach the awaited state")
	return siginfo{}
}

type realCall struct {
	r    uintptr
	e    syscall.Errno
	info siginfo
}

// recordingProof is the production proof with each real waitid result recorded.
func recordingProof(calls *[]realCall) proofOps {
	return proofOps{
		waitid: func(pid int, info *siginfo) (uintptr, syscall.Errno) {
			r, e := waitidNoWait(pid, info)
			*calls = append(*calls, realCall{r, e, *info})
			return r, e
		},
		now:   time.Now,
		sleep: time.Sleep,
	}
}

// TestExitProofKernelExited establishes an exited, unreaped direct child before
// any watcher call, then shows that registration fails with ESRCH, that the
// fallback reports the exit through the same shape as production watchExit,
// and that the child's exit status is still available to its own Wait.
func TestExitProofKernelExited(t *testing.T) {
	c := startOwnedChild(t)
	pid := c.cmd.Process.Pid
	c.stdin.Close()
	if info := c.awaitState(waitExited); info.Pid != int32(pid) || info.Signo != sigCHLD || info.Code != cldExited {
		t.Fatalf("child not observed exited: %+v", info)
	}

	// Real calls with the registration result recorded.
	var regErr error
	var regCalls, closes int
	ops := exitOps{
		kqueue:      syscall.Kqueue,
		closeOnExec: syscall.CloseOnExec,
		kevent: func(kq int, changes, events []syscall.Kevent_t, timeout *syscall.Timespec) (int, error) {
			n, err := syscall.Kevent(kq, changes, events, timeout)
			if changes != nil {
				regCalls++
				regErr = err
			}
			return n, err
		},
		close: func(fd int) error { closes++; return syscall.Close(fd) },
	}
	ch, failed, done, err := watchExitProof(pid, ops, proveExited)
	if regCalls != 1 || regErr != syscall.ESRCH || closes != 1 {
		t.Errorf("%d registrations, error %v, %d closes; want 1, ESRCH, 1", regCalls, regErr, closes)
	}
	if err != nil || ch == nil || failed == nil || done == nil {
		t.Fatalf("fallback: %v %v %v %v", err, ch, failed, done)
	}
	if !isClosed(ch) || !isClosed(done) || isClosed(failed) {
		t.Errorf("exit closed %v, done closed %v, failure closed %v", isClosed(ch), isClosed(done), isClosed(failed))
	}

	// Production watchExit on the same unreaped child.
	ch, failed, err = watchExit(pid)
	if err != nil || ch == nil || failed == nil || !isClosed(ch) || isClosed(failed) {
		t.Errorf("watchExit: err %v, exit %v, failure %v", err, ch, failed)
	}

	var calls []realCall
	if !proveExitedOps(pid, recordingProof(&calls)) || len(calls) != 1 {
		t.Errorf("direct proof %+v", calls)
	}
	c.join()
}

// TestExitProofKernelStopped stops a live direct child through its handle and
// waits, without reaping, until the kernel reports it stopped. The proof must
// reject it. Each real result must be either no state or this child's stop,
// and a stop must end the proof at once.
func TestExitProofKernelStopped(t *testing.T) {
	c := startOwnedChild(t)
	pid := c.cmd.Process.Pid
	if err := c.cmd.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	if info := c.awaitState(waitStopped | waitExited); info.Pid != int32(pid) || info.Code != cldStopped {
		t.Fatalf("child not observed stopped: %+v", info)
	}
	var calls []realCall
	if proveExitedOps(pid, recordingProof(&calls)) {
		t.Error("stopped child accepted")
	}
	if len(calls) == 0 || len(calls) > proofCalls {
		t.Errorf("%d calls", len(calls))
	}
	for i, r := range calls {
		stopped := r.info.Pid == int32(pid) && r.info.Signo == sigCHLD && r.info.Code == cldStopped
		if r.r != 0 || r.e != 0 || !(r.info.Pid == 0 || stopped) || (stopped && i != len(calls)-1) {
			t.Errorf("call %d: %+v", i, r)
		}
	}
	if err := c.cmd.Process.Signal(syscall.SIGCONT); err != nil {
		t.Error(err)
	}
	c.join()
}

// TestExitProofKernelLive: a running child has no waitable state; the proof
// returns without blocking after at most the frozen number of calls.
func TestExitProofKernelLive(t *testing.T) {
	c := startOwnedChild(t)
	var calls []realCall
	if proveExitedOps(c.cmd.Process.Pid, recordingProof(&calls)) {
		t.Error("live child accepted")
	}
	if len(calls) == 0 || len(calls) > proofCalls {
		t.Errorf("%d calls", len(calls))
	}
	for i, r := range calls {
		if r.r != 0 || r.e != 0 || r.info.Pid != 0 {
			t.Errorf("call %d: %+v", i, r)
		}
	}
	c.join()
}

const (
	waitStopped = 0x8 // WSTOPPED, test-only
	cldStopped  = 5   // CLD_STOPPED, test-only
)
