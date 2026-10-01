package proc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestMain doubles as a helper process: with BAW_PROC_HELPER=escape it leaves
// the owned process group (setsid) while keeping the inherited stdout/stderr
// pipes open, records its PID and sleeps.
func TestMain(m *testing.M) {
	if os.Getenv("BAW_PROC_HELPER") == "escape" {
		if _, err := syscall.Setsid(); err != nil {
			os.Exit(3)
		}
		os.WriteFile(os.Getenv("BAW_PROC_PIDFILE"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	GracePeriod, JoinBound = 300*time.Millisecond, 300*time.Millisecond
	os.Exit(m.Run())
}

func sh(body string, outCap, errCap int, timeout time.Duration, env ...string) ([]byte, error) {
	return Run(context.Background(), Spec{Path: "/bin/sh", Args: []string{"-c", body}, Env: env,
		Timeout: timeout, StdoutCap: outCap, StderrCap: errCap})
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pid not recorded: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		t.Fatalf("bad pid %q", data)
	}
	return pid
}

// gone polls briefly: a killed descendant orphaned to launchd may stay a
// zombie for a moment, and kill(pid, 0) still succeeds on a zombie.
func gone(pid int) bool {
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
	}
	return false
}

// killTestChild removes a process this test deliberately left running.
func killTestChild(t *testing.T, pid int) {
	t.Helper()
	syscall.Kill(pid, syscall.SIGKILL)
	if !gone(pid) {
		t.Errorf("test child %d not removed", pid)
	}
}

func TestStdoutCapBoundary(t *testing.T) {
	out, err := sh("printf 12345678", 8, 8, 5*time.Second)
	if err != nil || string(out) != "12345678" {
		t.Fatalf("at cap: %q %v", out, err)
	}
	if out, err := sh("printf 123456789", 8, 8, 5*time.Second); !errors.Is(err, ErrOutputLimit) || out != nil {
		t.Fatalf("over cap: %q %v", out, err)
	}
}

func TestStderrCapBoundary(t *testing.T) {
	// Exactly at the stderr cap: accepted, stderr never returned.
	out, err := sh("printf 'secret-v' >&2; printf ok", 64, 8, 5*time.Second)
	if err != nil || string(out) != "ok" {
		t.Fatalf("stderr at cap: %q %v", out, err)
	}
	// One byte over: command_output_limit even though stdout and exit are fine.
	out, err = sh("printf 'secret-va' >&2; printf ok", 64, 8, 5*time.Second)
	if !errors.Is(err, ErrOutputLimit) || out != nil {
		t.Fatalf("stderr over cap: %q %v", out, err)
	}
	// Real 64 KiB boundary with a large write.
	const kib64 = 64 << 10
	body := "head -c %d /dev/zero >&2; printf ok"
	if out, err := sh(strings.Replace(body, "%d", strconv.Itoa(kib64), 1), 64, kib64, 5*time.Second); err != nil || string(out) != "ok" {
		t.Fatalf("64KiB stderr: %q %v", out, err)
	}
	if _, err := sh(strings.Replace(body, "%d", strconv.Itoa(kib64+1), 1), 64, kib64, 5*time.Second); !errors.Is(err, ErrOutputLimit) {
		t.Fatalf("64KiB+1 stderr: %v", err)
	}
}

// Stderr overflow while the leader keeps running and a TERM-ignoring
// descendant holds stderr: the whole owned group is stopped.
func TestStderrOverflowCleansGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	start := time.Now()
	_, err := sh(descendant(pidFile, false)+"head -c 100 /dev/zero >&2; sleep 30", 64, 64, 10*time.Second)
	if !errors.Is(err, ErrOutputLimit) || time.Since(start) > 5*time.Second {
		t.Fatalf("stderr overflow: %v after %v", err, time.Since(start))
	}
	if pid := readPID(t, pidFile); !gone(pid) {
		killTestChild(t, pid)
		t.Fatalf("descendant %d survived", pid)
	}
}

func TestExitAndTimeout(t *testing.T) {
	var ee *ExitError
	if _, err := sh("exit 3", 8, 8, 5*time.Second); !errors.As(err, &ee) || ee.Code != 3 {
		t.Fatalf("exit: %v", err)
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	start := time.Now()
	// TERM-ignoring leader and descendant are killed after the grace period.
	_, err := sh("trap '' TERM; sleep 30 & echo $! > '"+pidFile+"'; wait", 8, 8, 200*time.Millisecond)
	if !errors.Is(err, ErrTimeout) || time.Since(start) > 5*time.Second {
		t.Fatalf("timeout: %v after %v", err, time.Since(start))
	}
	if pid := readPID(t, pidFile); !gone(pid) {
		killTestChild(t, pid)
		t.Fatalf("descendant %d survived timeout", pid)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", "sleep 30"}, Timeout: time.Minute, StdoutCap: 8, StderrCap: 8}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("ctx: %v", err)
	}
}

// descendant starts a TERM-ignoring sleep in the owned group, waits until
// its trap is installed and records its PID. With detached=true it holds no
// inherited stdio.
func descendant(pidFile string, detached bool) string {
	redir := ""
	if detached {
		redir = " </dev/null >/dev/null 2>&1"
	}
	return `(trap '' TERM; sh -c 'echo $PPID' > '` + pidFile + `.tmp'; mv '` + pidFile + `.tmp' '` + pidFile + `'; exec sleep 30)` + redir + ` &
while [ ! -s '` + pidFile + `' ]; do sleep 0.01; done
`
}

// Leader Wait is not group extinction: after a normal leader exit, surviving
// TERM-ignoring group members are killed and joined before success.
func TestNormalExitCleansDescendants(t *testing.T) {
	for _, detached := range []bool{true, false} {
		pidFile := filepath.Join(t.TempDir(), "pid")
		start := time.Now()
		out, err := sh(descendant(pidFile, detached)+"printf done", 64, 64, 10*time.Second)
		pid := readPID(t, pidFile)
		if err != nil || string(out) != "done" || time.Since(start) > 5*time.Second {
			killTestChild(t, pid)
			t.Fatalf("detached=%v: %q %v after %v", detached, out, err, time.Since(start))
		}
		if !gone(pid) {
			killTestChild(t, pid)
			t.Fatalf("detached=%v: descendant %d survived normal exit", detached, pid)
		}
	}
}

// Output cap with a leader that keeps running and a detached TERM-ignoring
// descendant (the reviewer's probe shape).
func TestOutputCapCleansDetachedDescendant(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	out, err := sh(descendant(pidFile, true)+"head -c 65 /dev/zero; sleep 30", 64, 64, 10*time.Second)
	pid := readPID(t, pidFile)
	if !errors.Is(err, ErrOutputLimit) || out != nil || time.Since(start) > 5*time.Second {
		killTestChild(t, pid)
		t.Fatalf("output cap: %q %v after %v", out, err, time.Since(start))
	}
	if !gone(pid) {
		killTestChild(t, pid)
		t.Fatalf("descendant %d survived output cap", pid)
	}
}

// A descendant that leaves the owned group (setsid) but keeps the stdout
// pipe: Run cannot identify or signal it, must not wait forever, and must not
// report success.
func TestEscapedPipeHolderFailsBounded(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	out, err := sh(`'`+exe+`' & while [ ! -s '`+pidFile+`' ]; do sleep 0.01; done; printf partial`, 64, 64, 10*time.Second,
		"BAW_PROC_HELPER=escape", "BAW_PROC_PIDFILE="+pidFile, "PATH=/usr/bin:/bin")
	pid := readPID(t, pidFile)
	defer killTestChild(t, pid)
	if !errors.Is(err, ErrCleanup) || out != nil {
		t.Fatalf("escaped pipe holder: %q %v", out, err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Fatalf("join not bounded: %v", d)
	}
	// Outside the owned group: Run must not have signalled it.
	if syscall.Kill(pid, 0) != nil {
		t.Fatalf("escaped process %d was signalled", pid)
	}
}
