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

	// The same failures through TestExitWatchKernel's heldQueue wrappers over
	// scripted calls, with the caller still holding the gate: the synchronous
	// Close and its probes finish and watchExitOps returns the setup error.
	probe := func(rest ...string) []string {
		return append([]string{"kqueue", "cloexec 7", "getfd 7", "register 7", "getfd 7", "close 7", "getfd 7"}, rest...)
	}
	held := []heldSetupCase{
		{name: "held-create-error", createErr: errCreate, want: errCreate, calls: []string{"kqueue"}},
		{name: "held-register-error", regErr: errRegister, want: errRegister, calls: probe(), closes: 1},
		{name: "held-register-eintr", regErr: syscall.EINTR, want: syscall.EINTR, calls: probe(), closes: 1},
		{name: "held-register-and-close-error", regErr: errRegister, closeErr: errClose, want: errRegister,
			calls: probe(), closes: 1},
	}
	for _, c := range held {
		t.Run(c.name, func(t *testing.T) {
			blocked, problems := checkHeldSetup(c, false)
			if blocked || len(problems) != 0 {
				t.Errorf("blocked on the caller's gate %v, problems %q", blocked, problems)
			}
		})
	}
	// Negative controls: the original wrapper held every Close, so a
	// registration failure waited on a gate only its caller could release
	// after return. It must be rejected for that block and for no other reason.
	for _, c := range held[1:] {
		t.Run(c.name+"-always-hold-rejected", func(t *testing.T) {
			blocked, problems := checkHeldSetup(c, true)
			if !blocked || len(problems) != 0 {
				t.Errorf("always-hold not rejected: blocked %v, problems %q", blocked, problems)
			}
		})
	}
}

type heldSetupCase struct {
	name                        string
	createErr, regErr, closeErr error
	want                        error
	calls                       []string
	closes                      int
}

type setupResult struct {
	ch, failed, done <-chan struct{}
	err              error
}

func receiveResultWithin(c <-chan setupResult, d time.Duration) (setupResult, bool) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case r := <-c:
		return r, true
	case <-t.C:
		return setupResult{}, false
	}
}

// checkHeldSetup calls watchExitOps with one scripted setup failure through
// heldQueue, from a goroutine it owns, while holding the gate. blocked reports
// that the wrapper waited on the gate before the call returned. The gate is
// always released and the call joined (bounded) before it returns; problems
// lists every other deviation.
func checkHeldSetup(c heldSetupCase, alwaysHold bool) (blocked bool, problems []string) {
	f := newFakeOps(nil)
	f.createErr, f.regErr, f.closeErr = c.createErr, c.regErr, c.closeErr
	close(f.gate) // the scripted close itself does not block; heldQueue's gate is under test
	getfd := func(fd int) (int, error) {
		f.record(fmt.Sprintf("getfd %d", fd))
		if _, closes := f.snapshot(); closes > 0 {
			return -1, syscall.EBADF
		}
		return syscall.FD_CLOEXEC, nil
	}
	q := newHeldQueue(f.ops(), getfd, false)
	q.alwaysHold = alwaysHold
	defer q.open()

	res := make(chan setupResult, 1)
	go func() {
		ch, failed, done, err := watchExitOps(fakePID, q.ops())
		res <- setupResult{ch, failed, done, err}
	}()
	var r setupResult
	returned := false
	timer := time.NewTimer(bound)
	select {
	case r = <-res:
		returned = true
	case <-q.held:
		// The call is waiting on the gate this caller holds and cannot
		// have returned; release it and join.
		blocked = true
	case <-timer.C:
		problems = append(problems, "call neither returned nor held")
	}
	timer.Stop()
	if !returned {
		q.open()
		var ok bool
		if r, ok = receiveResultWithin(res, bound); !ok {
			return blocked, append(problems, "setup call not joined")
		}
	}
	q.open()
	select {
	case <-q.held:
		problems = append(problems, "held after returning")
	default:
	}

	if r.err != c.want || r.ch != nil || r.failed != nil || r.done != nil {
		problems = append(problems, fmt.Sprintf("got %v %v %v %v, want %v and nil channels", r.err, r.ch, r.failed, r.done, c.want))
	}
	calls, closes := f.snapshot()
	if closes != c.closes || q.obs.closes != c.closes || len(q.ack) != c.closes {
		problems = append(problems, fmt.Sprintf("%d scripted, %d wrapped close calls, %d acks; want %d", closes, q.obs.closes, len(q.ack), c.closes))
	}
	if !reflect.DeepEqual(calls, c.calls) {
		problems = append(problems, fmt.Sprintf("calls %q, want %q", calls, c.calls))
	}
	if c.closes == 1 && (q.obs.closedFD != fakeKQ || q.obs.beforeErr != nil || q.obs.closeErr != c.closeErr || q.obs.after != syscall.EBADF) {
		problems = append(problems, fmt.Sprintf("closed %d: F_GETFD before %v, Close %v, F_GETFD after %v", q.obs.closedFD, q.obs.beforeErr, q.obs.closeErr, q.obs.after))
	}
	return blocked, problems
}

func fcntlGetfd(fd int) (int, error) {
	r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFD, 0)
	if e != 0 {
		return -1, e
	}
	return int(r), nil
}

// kernelObs is what heldQueue's wrappers saw, written by the closing goroutine
// before it acknowledges Close entry.
type kernelObs struct {
	kq, closes                 int
	cloexecFlags               int
	cloexecErr                 error
	closedFD                   int
	beforeErr, closeErr, after error
}

// heldQueue wraps one invocation's raw system calls: the real ones in
// TestExitWatchKernel, scripted ones in TestExitWatchSetup's held cases. Its
// close calls the raw Close once between two F_GETFD probes, acknowledges on
// ack, and then holds on gate only if registration succeeded, that is when
// the watcher goroutine is closing on a terminal path. A registration
// failure's Close runs inside watchExitOps, before its caller could release
// the gate, so it is not held. alwaysHold restores the original unconditional
// hold for the negative control; held is signalled just before a hold.
type heldQueue struct {
	sys        exitOps
	getfd      func(fd int) (int, error)
	injectWait bool
	alwaysHold bool
	registered bool
	obs        kernelObs
	ack, held  chan struct{}
	gate       chan struct{}
	release    sync.Once
}

func newHeldQueue(sys exitOps, getfd func(int) (int, error), injectWait bool) *heldQueue {
	return &heldQueue{sys: sys, getfd: getfd, injectWait: injectWait,
		ack: make(chan struct{}, 4), held: make(chan struct{}, 4), gate: make(chan struct{})}
}

func (q *heldQueue) open() { q.release.Do(func() { close(q.gate) }) }

func (q *heldQueue) ops() exitOps {
	return exitOps{
		kqueue: func() (int, error) {
			kq, err := q.sys.kqueue()
			q.obs.kq = kq
			return kq, err
		},
		closeOnExec: func(fd int) {
			q.sys.closeOnExec(fd)
			q.obs.cloexecFlags, q.obs.cloexecErr = q.getfd(fd)
		},
		kevent: func(kq int, changes, events []syscall.Kevent_t, timeout *syscall.Timespec) (int, error) {
			if q.injectWait && changes == nil {
				return -1, errWait
			}
			n, err := q.sys.kevent(kq, changes, events, timeout)
			if changes != nil && err == nil {
				q.registered = true // read later by the watcher goroutine it starts
			}
			return n, err
		},
		close: func(fd int) error {
			q.obs.closes++
			q.obs.closedFD = fd
			_, q.obs.beforeErr = q.getfd(fd)
			q.obs.closeErr = q.sys.close(fd)
			_, q.obs.after = q.getfd(fd)
			q.ack <- struct{}{}
			if q.registered || q.alwaysHold {
				q.held <- struct{}{}
				<-q.gate
			}
			return q.obs.closeErr
		},
	}
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

	q := newHeldQueue(exitOps{syscall.Kqueue, syscall.CloseOnExec, syscall.Kevent, syscall.Close}, fcntlGetfd, injectWait)
	obs, ack, open := &q.obs, q.ack, q.open
	defer open()

	// A setup error returns here with its Close not held (see heldQueue), so
	// the deferred cleanup above releases and joins the child.
	ch, failed, done, err := watchExitOps(cmd.Process.Pid, q.ops())
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
