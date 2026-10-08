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

// watchExit returns a channel closed when pid exits. It uses kqueue
// EVFILT_PROC/NOTE_EXIT, which reports the exit of an unreaped child without
// reaping it (the event also fires if the child is already a zombie). The
// second channel is closed if exit can no longer be observed after setup.
// The queue's single Close call has returned before either channel closes;
// its error is ignored, so a closed channel does not prove the release.
func watchExit(pid int) (<-chan struct{}, <-chan struct{}, error) {
	ch, failed, _, err := watchExitOps(pid, exitOps{syscall.Kqueue, syscall.CloseOnExec, syscall.Kevent, syscall.Close})
	return ch, failed, err
}

// watchExitOps is watchExit with explicit operations. The third channel is
// closed when the wait goroutine has returned; it is nil on setup failure.
func watchExitOps(pid int, ops exitOps) (<-chan struct{}, <-chan struct{}, <-chan struct{}, error) {
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
