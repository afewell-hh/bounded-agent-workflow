package proc

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"
)

// With BAW_EXITWATCH_CHILD=stdin the test binary is a finite direct child for
// TestExitWatchKernel: it exits 0 when its stdin reaches EOF, or 3 after 20 s.
// Without that variable this returns normally.
func init() {
	if os.Getenv("BAW_EXITWATCH_CHILD") == "stdin" {
		go func() {
			time.Sleep(20 * time.Second)
			os.Exit(3)
		}()
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
}

const (
	fakePID = 4242 // never passed to a real syscall
	fakeKQ  = 7    // never passed to a real syscall
	bound   = 5 * time.Second
)

var (
	errCreate   = errors.New("injected kqueue error")
	errRegister = errors.New("injected registration error")
	errWait     = errors.New("injected wait error")
	errClose    = errors.New("injected close error")
)

type waitStep struct {
	n      int
	fflags uint32
	err    error
}

// fakeOps is one invocation's scripted system calls. Close records its call,
// acknowledges entry on ack and then blocks until gate is closed.
type fakeOps struct {
	mu        sync.Mutex
	calls     []string
	closes    int
	waits     []waitStep
	createErr error
	regErr    error
	closeErr  error
	ack       chan struct{}
	gate      chan struct{}
}

func newFakeOps(waits []waitStep) *fakeOps {
	return &fakeOps{waits: waits, ack: make(chan struct{}, 4), gate: make(chan struct{})}
}

func (f *fakeOps) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeOps) snapshot() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), f.closes
}

func (f *fakeOps) ops() exitOps {
	return exitOps{
		kqueue: func() (int, error) {
			f.record("kqueue")
			if f.createErr != nil {
				return -1, f.createErr
			}
			return fakeKQ, nil
		},
		closeOnExec: func(fd int) { f.record(fmt.Sprintf("cloexec %d", fd)) },
		kevent: func(kq int, changes, events []syscall.Kevent_t, timeout *syscall.Timespec) (int, error) {
			if changes != nil {
				want := []syscall.Kevent_t{{Ident: fakePID, Filter: syscall.EVFILT_PROC,
					Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
				if !reflect.DeepEqual(changes, want) || events != nil || timeout != nil {
					f.record(fmt.Sprintf("register-bad %d %+v %d %v", kq, changes, len(events), timeout))
				} else {
					f.record(fmt.Sprintf("register %d", kq))
				}
				return 0, f.regErr
			}
			if len(events) != 1 || timeout != nil {
				f.record(fmt.Sprintf("wait-bad %d %d %v", kq, len(events), timeout))
				return -1, errWait
			}
			f.mu.Lock()
			if len(f.waits) == 0 {
				f.mu.Unlock()
				f.record("wait-exhausted")
				return -1, errWait
			}
			s := f.waits[0]
			f.waits = f.waits[1:]
			f.mu.Unlock()
			f.record(fmt.Sprintf("wait %d", kq))
			events[0] = syscall.Kevent_t{Ident: fakePID, Filter: syscall.EVFILT_PROC, Fflags: s.fflags}
			return s.n, s.err
		},
		close: func(fd int) error {
			f.mu.Lock()
			f.calls = append(f.calls, fmt.Sprintf("close %d", fd))
			f.closes++
			f.mu.Unlock()
			f.ack <- struct{}{}
			<-f.gate
			return f.closeErr
		},
	}
}

// oldOrderWatch is the baseline ordering, kept only as a negative control: it
// notifies first and closes the queue in a deferred call afterwards.
func oldOrderWatch(pid int, ops exitOps) (<-chan struct{}, <-chan struct{}, <-chan struct{}, error) {
	kq, err := ops.kqueue()
	if err != nil {
		return nil, nil, nil, err
	}
	ops.closeOnExec(kq)
	change := []syscall.Kevent_t{{Ident: uint64(pid), Filter: syscall.EVFILT_PROC,
		Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
	if _, err := ops.kevent(kq, change, nil, nil); err != nil {
		ops.close(kq)
		return nil, nil, nil, err
	}
	ch := make(chan struct{})
	failed := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer ops.close(kq)
		ev := make([]syscall.Kevent_t, 1)
		for {
			n, err := ops.kevent(kq, nil, ev, nil)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				close(failed)
				return
			}
			if n == 1 && ev[0].Fflags&syscall.NOTE_EXIT != 0 {
				close(ch)
				return
			}
		}
	}()
	return ch, failed, done, nil
}

type watchFunc func(int, exitOps) (<-chan struct{}, <-chan struct{}, <-chan struct{}, error)

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func receiveWithin(c <-chan struct{}, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-c:
		return true
	case <-t.C:
		return false
	}
}

type orderingCase struct {
	name     string
	waits    []waitStep
	closeErr error
	failure  bool // the failure channel, not the exit channel, is expected
	calls    []string
}

// checkOrdering drives one scripted invocation. premature names each
// notification channel already closed when Close acknowledged entry; problems
// lists every other deviation. The gate is always released and the watcher
// goroutine joined (bounded) before it returns.
func checkOrdering(watch watchFunc, c orderingCase) (premature []string, problems []string) {
	f := newFakeOps(c.waits)
	f.closeErr = c.closeErr
	var release sync.Once
	open := func() { release.Do(func() { close(f.gate) }) }
	defer open()

	ch, failed, done, err := watch(fakePID, f.ops())
	if err != nil || ch == nil || failed == nil || done == nil {
		return nil, []string{fmt.Sprintf("setup: %v %v %v %v", err, ch, failed, done)}
	}
	defer func() {
		open()
		if !receiveWithin(done, bound) {
			problems = append(problems, "watcher goroutine not joined")
		}
	}()

	if !receiveWithin(f.ack, bound) {
		return nil, []string{"Close entry not acknowledged"}
	}
	// Close has been entered and is held: no notification may be visible.
	if isClosed(ch) {
		premature = append(premature, "exit")
	}
	if isClosed(failed) {
		premature = append(premature, "failure")
	}
	open()

	want, other := ch, failed
	if c.failure {
		want, other = failed, ch
	}
	if !receiveWithin(want, bound) {
		problems = append(problems, "selected channel not closed after release")
	}
	if !receiveWithin(done, bound) {
		problems = append(problems, "watcher goroutine not joined")
		return premature, problems
	}
	if isClosed(other) {
		problems = append(problems, "other channel closed")
	}
	calls, closes := f.snapshot()
	if closes != 1 {
		problems = append(problems, fmt.Sprintf("%d close calls", closes))
	}
	if !reflect.DeepEqual(calls, c.calls) {
		problems = append(problems, fmt.Sprintf("calls %q, want %q", calls, c.calls))
	}
	return premature, problems
}

// TestExitWatchOrdering: on both terminal paths the queue's one Close call has
// returned before the selected notification, the other channel stays open,
// and close errors are ignored as before. The baseline order is rejected by
// the same oracle, for premature notification only.
func TestExitWatchOrdering(t *testing.T) {
	exit := waitStep{n: 1, fflags: syscall.NOTE_EXIT}
	setup := []string{"kqueue", "cloexec 7", "register 7"}
	seq := func(rest ...string) []string { return append(append([]string(nil), setup...), rest...) }
	cases := []orderingCase{
		{name: "exit", waits: []waitStep{exit},
			calls: seq("wait 7", "close 7")},
		{name: "wait-error", waits: []waitStep{{n: -1, err: errWait}}, failure: true,
			calls: seq("wait 7", "close 7")},
		{name: "eintr-then-exit", waits: []waitStep{{n: -1, err: syscall.EINTR}, {n: -1, err: syscall.EINTR}, exit},
			calls: seq("wait 7", "wait 7", "wait 7", "close 7")},
		{name: "irrelevant-then-exit", waits: []waitStep{{n: 0}, {n: 1, fflags: syscall.NOTE_FORK}, exit},
			calls: seq("wait 7", "wait 7", "wait 7", "close 7")},
		{name: "close-error-exit", waits: []waitStep{exit}, closeErr: errClose,
			calls: seq("wait 7", "close 7")},
		{name: "wait-and-close-error", waits: []waitStep{{n: -1, err: errWait}}, closeErr: errClose, failure: true,
			calls: seq("wait 7", "close 7")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			premature, problems := checkOrdering(watchExitOps, c)
			if len(premature) != 0 || len(problems) != 0 {
				t.Errorf("premature %q, problems %q", premature, problems)
			}
		})
	}

	// Negative controls: the baseline order must be rejected because its
	// selected channel is already closed when Close is entered, and for no
	// other reason.
	for _, c := range []struct {
		oc   orderingCase
		want string
	}{
		{orderingCase{name: "old-order-exit-rejected", waits: []waitStep{exit}, calls: seq("wait 7", "close 7")}, "exit"},
		{orderingCase{name: "old-order-wait-error-rejected", waits: []waitStep{{n: -1, err: errWait}}, failure: true,
			calls: seq("wait 7", "close 7")}, "failure"},
	} {
		t.Run(c.oc.name, func(t *testing.T) {
			premature, problems := checkOrdering(oldOrderWatch, c.oc)
			if !reflect.DeepEqual(premature, []string{c.want}) || len(problems) != 0 {
				t.Errorf("old order not rejected for premature %s: premature %q, problems %q", c.want, premature, problems)
			}
		})
	}
}

// TestExitWatchSetup: creation failure returns its error and nil channels
// without a close; registration failure closes the queue once before
// returning its own error, which wins over a close error.
func TestExitWatchSetup(t *testing.T) {
	for _, c := range []struct {
		name      string
		createErr error
		regErr    error
		closeErr  error
		want      error
		calls     []string
		closes    int
	}{
		{"create-error", errCreate, nil, nil, errCreate, []string{"kqueue"}, 0},
		{"register-error", nil, errRegister, nil, errRegister,
			[]string{"kqueue", "cloexec 7", "register 7", "close 7"}, 1},
		{"register-and-close-error", nil, errRegister, errClose, errRegister,
			[]string{"kqueue", "cloexec 7", "register 7", "close 7"}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeOps(nil)
			f.createErr, f.regErr, f.closeErr = c.createErr, c.regErr, c.closeErr
			close(f.gate) // setup closes synchronously; nothing to hold
			ch, failed, done, err := watchExitOps(fakePID, f.ops())
			if err != c.want || ch != nil || failed != nil || done != nil {
				t.Errorf("got %v %v %v %v, want %v and nil channels", err, ch, failed, done, c.want)
			}
			calls, closes := f.snapshot()
			if closes != c.closes || !reflect.DeepEqual(calls, c.calls) {
				t.Errorf("calls %q (%d closes), want %q (%d)", calls, closes, c.calls, c.closes)
			}
		})
	}
}

func fcntlGetfd(fd int) (int, error) {
	r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if e != 0 {
		return -1, e
	}
	return int(r), nil
}

// kernelObs is what the real-queue wrappers saw, written by the watcher
// goroutine before it acknowledges Close entry.
type kernelObs struct {
	kq, closes                 int
	cloexecFlags               int
	cloexecErr                 error
	closedFD                   int
	beforeErr, closeErr, after error
}

// TestExitWatchKernel watches a real unreaped direct child on a real queue.
// The close wrapper checks the acquired queue is open, calls the real Close
// once and requires F_GETFD to report EBADF immediately afterwards, all before
// it acknowledges entry and while both notification channels are still open.
// "exit" waits for the real NOTE_EXIT; "wait-error" injects only the terminal
// wait error. Either way the child is then reaped through its own handle.
// This shows one owned queue closed at that boundary in this controlled
// test; it does not establish descriptor identity under concurrent reuse.
func TestExitWatchKernel(t *testing.T) {
	for _, injectWait := range []bool{false, true} {
		name := "exit"
		if injectWait {
			name = "wait-error"
		}
		t.Run(name, func(t *testing.T) { kernelCase(t, injectWait) })
	}
}

func kernelCase(t *testing.T, injectWait bool) {
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
	// Always release and join the child, on every path, through its handle.
	// Wait is started at most once; joined records that its result arrived.
	waited := make(chan error, 1)
	var startWait sync.Once
	wait := func() { startWait.Do(func() { go func() { waited <- cmd.Wait() }() }) }
	var joined bool
	defer func() {
		stdin.Close()
		if joined {
			return
		}
		wait()
		if _, ok := receiveErrWithin(waited, 25*time.Second); !ok {
			cmd.Process.Kill()
			if _, ok := receiveErrWithin(waited, bound); !ok {
				t.Errorf("child not joined")
			}
		}
	}()

	var obs kernelObs
	ack := make(chan struct{}, 4)
	gate := make(chan struct{})
	var release sync.Once
	open := func() { release.Do(func() { close(gate) }) }
	defer open()
	ops := exitOps{
		kqueue: func() (int, error) {
			kq, err := syscall.Kqueue()
			obs.kq = kq
			return kq, err
		},
		closeOnExec: func(fd int) {
			syscall.CloseOnExec(fd)
			obs.cloexecFlags, obs.cloexecErr = fcntlGetfd(fd)
		},
		kevent: func(kq int, changes, events []syscall.Kevent_t, timeout *syscall.Timespec) (int, error) {
			if injectWait && changes == nil {
				return -1, errWait
			}
			return syscall.Kevent(kq, changes, events, timeout)
		},
		close: func(fd int) error {
			obs.closes++
			obs.closedFD = fd
			_, obs.beforeErr = fcntlGetfd(fd)
			obs.closeErr = syscall.Close(fd)
			_, obs.after = fcntlGetfd(fd)
			ack <- struct{}{}
			<-gate
			return obs.closeErr
		},
	}

	ch, failed, done, err := watchExitOps(cmd.Process.Pid, ops)
	if err != nil {
		t.Fatalf("real watcher setup: %v", err)
	}
	defer func() {
		open()
		if !receiveWithin(done, bound) {
			t.Errorf("watcher goroutine not joined")
		}
	}()
	if !injectWait {
		stdin.Close() // the child exits only now, after registration
	}

	if !receiveWithin(ack, bound) {
		t.Fatal("Close entry not acknowledged")
	}
	chOpen, failedOpen := !isClosed(ch), !isClosed(failed)
	open()
	if !chOpen || !failedOpen {
		t.Errorf("notification before Close returned: exit open %v, failure open %v", chOpen, failedOpen)
	}
	want, other := ch, failed
	if injectWait {
		want, other = failed, ch
	}
	if !receiveWithin(want, bound) {
		t.Fatal("selected channel not closed")
	}
	if !receiveWithin(done, bound) {
		t.Fatal("watcher goroutine not joined")
	}
	if isClosed(other) {
		t.Error("other channel closed")
	}
	if obs.kq < 0 || obs.cloexecErr != nil || obs.cloexecFlags&syscall.FD_CLOEXEC == 0 {
		t.Errorf("queue %d close-on-exec flags %#x err %v", obs.kq, obs.cloexecFlags, obs.cloexecErr)
	}
	if obs.closes != 1 || obs.closedFD != obs.kq {
		t.Errorf("%d close calls on %d, acquired %d", obs.closes, obs.closedFD, obs.kq)
	}
	if obs.beforeErr != nil || obs.closeErr != nil || obs.after != syscall.EBADF {
		t.Errorf("F_GETFD before %v, Close %v, F_GETFD after %v (want nil, nil, EBADF)", obs.beforeErr, obs.closeErr, obs.after)
	}

	if injectWait {
		stdin.Close()
	}
	wait()
	werr, ok := receiveErrWithin(waited, bound)
	if !ok {
		t.Fatal("child Wait did not return")
	}
	joined = true
	if werr != nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != 0 {
		t.Errorf("child Wait %v, state %v", werr, cmd.ProcessState)
	}
}

func receiveErrWithin(c <-chan error, d time.Duration) (error, bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case err := <-c:
		return err, true
	case <-t.C:
		return nil, false
	}
}
