package proc

import "syscall"

// watchExit returns a channel closed when pid exits. It uses kqueue
// EVFILT_PROC/NOTE_EXIT, which reports the exit of an unreaped child without
// reaping it (the event also fires if the child is already a zombie).
func watchExit(pid int) (<-chan struct{}, error) {
	kq, err := syscall.Kqueue()
	if err != nil {
		return nil, err
	}
	syscall.CloseOnExec(kq)
	change := []syscall.Kevent_t{{Ident: uint64(pid), Filter: syscall.EVFILT_PROC,
		Flags: syscall.EV_ADD | syscall.EV_ONESHOT, Fflags: syscall.NOTE_EXIT}}
	if _, err := syscall.Kevent(kq, change, nil, nil); err != nil {
		syscall.Close(kq)
		return nil, err
	}
	ch := make(chan struct{})
	go func() {
		defer syscall.Close(kq)
		ev := make([]syscall.Kevent_t, 1)
		for {
			n, err := syscall.Kevent(kq, nil, ev, nil)
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				// Exit can no longer be observed: Run treats the leader as
				// not exited and fails after its bounded cleanup.
				return
			}
			if n == 1 && ev[0].Fflags&syscall.NOTE_EXIT != 0 {
				close(ch)
				return
			}
		}
	}()
	return ch, nil
}
