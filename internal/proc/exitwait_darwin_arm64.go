package proc

import (
	"math"
	"syscall"
	"time"
	"unsafe"
)

// siginfo is the darwin/arm64 siginfo_t from <sys/signal.h> of the installed
// SDK: 104 bytes, 8-byte aligned. Only Signo, Code and Pid are consumed.
type siginfo struct {
	Signo  int32     // si_signo, offset 0
	Errno  int32     // si_errno, offset 4
	Code   int32     // si_code, offset 8
	Pid    int32     // si_pid, offset 12
	Uid    uint32    // si_uid, offset 16
	Status int32     // si_status, offset 20
	Addr   uint64    // si_addr, offset 24
	Value  uint64    // si_value (union sigval), offset 32
	Band   int64     // si_band, offset 40
	Pad    [7]uint64 // __pad, offset 48
}

// Compile-time layout checks: each pair fails to compile unless the value is
// exactly as stated, in both directions.
var (
	_ [unsafe.Sizeof(siginfo{}) - 104]byte
	_ [104 - unsafe.Sizeof(siginfo{})]byte
	_ [unsafe.Alignof(siginfo{}) - 8]byte
	_ [8 - unsafe.Alignof(siginfo{})]byte
	_ [unsafe.Offsetof(siginfo{}.Signo) - 0]byte
	_ [0 - unsafe.Offsetof(siginfo{}.Signo)]byte
	_ [unsafe.Offsetof(siginfo{}.Code) - 8]byte
	_ [8 - unsafe.Offsetof(siginfo{}.Code)]byte
	_ [unsafe.Offsetof(siginfo{}.Pid) - 12]byte
	_ [12 - unsafe.Offsetof(siginfo{}.Pid)]byte
	_ [unsafe.Offsetof(siginfo{}.Pad) - 48]byte
	_ [48 - unsafe.Offsetof(siginfo{}.Pad)]byte
)

// Values from <sys/wait.h> and <sys/signal.h> of the installed SDK.
const (
	waitPPID    = 1    // P_PID
	waitNoHang  = 0x1  // WNOHANG
	waitExited  = 0x4  // WEXITED
	waitNoWait  = 0x20 // WNOWAIT: leave the child waitable
	sigCHLD     = 20   // SIGCHLD
	cldExited   = 1    // CLD_EXITED
	cldKilled   = 2    // CLD_KILLED
	cldDumped   = 3    // CLD_DUMPED
	proofCalls  = 5
	proofSpace  = time.Millisecond
	proofBudget = 10 * time.Millisecond
)

// proofOps are the operations of one proof: a nonblocking, non-reaping
// waitid of pid into info, and a monotonic clock and sleeper.
type proofOps struct {
	waitid func(pid int, info *siginfo) (uintptr, syscall.Errno)
	now    func() time.Time
	sleep  func(time.Duration)
}

// proveExited is the production exit proof used by watchExit.
func proveExited(pid int) bool {
	return proveExitedOps(pid, proofOps{waitid: waitidNoWait, now: time.Now, sleep: time.Sleep})
}

// waitidNoWait is waitid(P_PID, pid, info, WEXITED|WNOWAIT|WNOHANG) through
// the darwin/arm64 system call trap. It never reaps the child.
func waitidNoWait(pid int, info *siginfo) (uintptr, syscall.Errno) {
	r, _, e := syscall.Syscall6(syscall.SYS_WAITID, waitPPID, uintptr(pid),
		uintptr(unsafe.Pointer(info)), waitExited|waitNoWait|waitNoHang, 0, 0)
	return r, e
}

// proveExitedOps reports whether pid has terminated (exited, killed or dumped)
// and is still waitable. It makes at most proofCalls calls, none starting at or
// after proofBudget from entry, with a sleep of at most proofSpace (and never
// past the budget) between them and none after the last. Only a result of 0
// with si_pid 0 (no waitable state yet) is retried; any error, including
// EINTR, another pid or signal, or a stopped, trapped, continued or unknown
// code fails at once. A terminal result must also be observed within the
// budget. The info buffer is zeroed before every call.
func proveExitedOps(pid int, ops proofOps) bool {
	if pid <= 0 || pid > math.MaxInt32 {
		return false
	}
	start := ops.now()
	var info siginfo
	for call := 1; ; call++ {
		if ops.now().Sub(start) >= proofBudget {
			return false
		}
		info = siginfo{}
		r, e := ops.waitid(pid, &info)
		if e != 0 || r != 0 {
			return false
		}
		if info.Pid != 0 {
			if int(info.Pid) != pid || info.Signo != sigCHLD {
				return false
			}
			switch info.Code {
			case cldExited, cldKilled, cldDumped:
				return ops.now().Sub(start) <= proofBudget
			default:
				return false
			}
		}
		if call == proofCalls {
			return false
		}
		remaining := proofBudget - ops.now().Sub(start)
		if remaining <= 0 {
			return false
		}
		ops.sleep(min(proofSpace, remaining))
	}
}
