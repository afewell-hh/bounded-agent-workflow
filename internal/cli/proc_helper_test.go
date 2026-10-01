package cli

import (
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// detachedDescendant is shell text that starts a TERM-ignoring `sleep 30`
// with stdin/stdout/stderr detached from the fake command's pipes, waits until
// its trap is installed and records its PID in pidFile.
func detachedDescendant(pidFile string) string {
	return `(trap '' TERM; sh -c 'echo $PPID' > '` + pidFile + `.tmp'; mv '` + pidFile + `.tmp' '` + pidFile + `'; exec sleep 30) </dev/null >/dev/null 2>&1 &
while [ ! -s '` + pidFile + `' ]; do sleep 0.01; done
`
}

// assertProcessGone checks that the background descendant recorded by a fake
// command no longer exists.
func assertProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("descendant pid not recorded: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Do not leak the test's own child even when the inspector failed.
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("owned descendant %d still running", pid)
}
