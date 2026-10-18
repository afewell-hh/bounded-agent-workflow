package proc

import "syscall"

// exitOps holds the system calls of one watchExit invocation. Production
// passes the real syscalls; tests pass their own operations to a single call.
type exitOps struct {
	kqueue      func() (int, error)
	closeOnExec func(fd int)
	kevent      func(kq int, changes, events []syscall.Kevent_t, timeout *syscall.Timespec) (int, error)
	close       func(fd int) error
}

// exitProof reports whether pid has terminated and is still unreaped, without
// reaping it. It is consulted only after registration failed with ESRCH. It is
// a per-call parameter rather than an exitOps field so that existing positional
// exitOps literals keep their meaning; nil means no proof.
type exitProof func(pid int) bool

// watchExit returns a channel closed when pid exits. It uses kqueue
// EVFILT_PROC/NOTE_EXIT, which reports the exit of an unreaped child without
// reaping it. Registration for a child that has already exited but is not yet
// reaped fails with ESRCH; only then, after the queue is closed, a bounded
// non-reaping waitid probe of the same pid may prove the exit (see
// proveExited), and the exit channel is returned already closed. The second
// channel is closed if exit can no longer be observed after setup.
// The queue's single Close call has returned before either channel closes;
// its error is ignored, so a closed channel does not prove the release.
func watchExit(pid int) (<-chan struct{}, <-chan struct{}, error) {
	ops := exitOps{kqueue: syscall.Kqueue, closeOnExec: syscall.CloseOnExec, kevent: syscall.Kevent, close: syscall.Close}
	ch, failed, _, err := watchExitProof(pid, ops, proveExited)
	return ch, failed, err
}

// watchExitOps is watchExit with explicit operations and no exit proof. The
// third channel is closed when the wait goroutine has returned; it is nil on
// setup failure.
func watchExitOps(pid int, ops exitOps) (<-chan struct{}, <-chan struct{}, <-chan struct{}, error) {
	return watchExitProof(pid, ops, nil)
}

// watchExitProof is watchExitOps with an optional exit proof. When
// registration fails with exactly ESRCH and proof reports the exit, it returns
// the exit channel and the third channel already closed, an open failure
// channel and no error, without starting a goroutine. Any other registration
// error, or a missing or failed proof, returns the registration error itself
// and nil channels.
func watchExitProof(pid int, ops exitOps, proof exitProof) (<-chan struct{}, <-chan struct{}, <-chan struct{}, error) {
	kq, err := ops.kqueue()
	if err != nil {
		return nil, nil, nil, err
	}
	ops.closeOnExec(kq)
	change := []syscall.Kevent_t{{Ident: uint64(pid), Filter: syscall.EVFILT_PROC,
		Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
	if _, err := ops.kevent(kq, change, nil, nil); err != nil {
		ops.close(kq)
		if err == syscall.ESRCH && proof != nil && proof(pid) {
			ch := make(chan struct{})
			done := make(chan struct{})
			close(ch)
			close(done)
			return ch, make(chan struct{}), done, nil
		}
		return nil, nil, nil, err
	}
	ch := make(chan struct{})
	failed := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ev := make([]syscall.Kevent_t, 1)
		for {
			n, err := ops.kevent(kq, nil, ev, nil)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				// Exit can no longer be observed: Run treats the leader as
				// not exited and fails after its bounded cleanup.
				ops.close(kq)
				close(failed)
				return
			}
			if n == 1 && ev[0].Fflags&syscall.NOTE_EXIT != 0 {
				ops.close(kq)
				close(ch)
				return
			}
		}
	}()
	return ch, failed, done, nil
}
