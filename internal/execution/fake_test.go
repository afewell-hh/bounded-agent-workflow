package execution

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
)

// TestMain doubles as the compiled fake worker/verifier: with argv
// "-baw-fake MODE COUNTER TOP [EXTRA]" it never runs tests. Every start
// appends one line to COUNTER/ROLE, so tests count starts independently of
// executor output.
func TestMain(m *testing.M) {
	if len(os.Args) > 4 && os.Args[1] == "-baw-fake" {
		os.Exit(fake(os.Args[2:]))
	}
	if len(os.Args) == 5 && os.Args[1] == "-baw-fake-child" {
		os.Exit(fakeChild(os.Args[2], os.Args[3], os.Args[4]))
	}
	if len(os.Args) == 3 && strings.HasPrefix(os.Args[1], "-baw-probe-") {
		os.Exit(probe(os.Args[1], os.Args[2]))
	}
	if len(os.Args) == 3 && os.Args[1] == "-baw-controller" {
		os.Exit(controller(os.Args[2]))
	}
	proc.GracePeriod, proc.JoinBound = 300*time.Millisecond, 300*time.Millisecond
	os.Exit(m.Run())
}

const workerBytes = "worker wrote this dummy file\n"

func fake(a []string) int {
	mode, counter, top := a[0], a[1], a[2]
	role := "worker"
	if strings.HasPrefix(mode, "verifier") {
		role = "verifier"
	}
	f, err := os.OpenFile(filepath.Join(counter, role), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 90
	}
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	f.Close()
	bad := func(why string) int {
		os.WriteFile(filepath.Join(counter, role+"-bad"), []byte(why+"\n"), 0o600)
		return 97
	}
	env := os.Environ()
	sort.Strings(env)
	home, tmp := os.Getenv("HOME"), os.Getenv("TMPDIR")
	if len(env) != 4 || os.Getenv("LANG") != "C" || os.Getenv("LC_ALL") != "C" ||
		!strings.HasSuffix(home, "/"+role+"/home") || !strings.HasSuffix(tmp, "/"+role+"/tmp") {
		return bad("environment " + strings.Join(env, ","))
	}
	if wd, err := os.Getwd(); err != nil || wd != top {
		return bad("cwd")
	}
	in, err1 := os.Stdin.Stat()
	null, err2 := os.Stat("/dev/null")
	if err1 != nil || err2 != nil || !os.SameFile(in, null) {
		return bad("stdin")
	}
	switch mode {
	case "worker-ok", "worker-commit":
		os.WriteFile(filepath.Join(home, "cache-marker"), []byte("w"), 0o600)
		os.WriteFile(filepath.Join(tmp, "tmp-marker"), []byte("w"), 0o600)
		if os.WriteFile(filepath.Join(top, "dummy.txt"), []byte(workerBytes), 0o644) != nil {
			return bad("write")
		}
		os.Stdout.WriteString("SECRET-WORKER-STDOUT")
		os.Stderr.WriteString("SECRET-WORKER-STDERR")
		if mode == "worker-commit" {
			for _, args := range [][]string{{"add", "dummy.txt"}, {"-c", "user.name=F", "-c", "user.email=f@example.invalid", "commit", "-q", "-m", "worker"}} {
				cmd := exec.Command(a[3], append([]string{"-C", top}, args...)...)
				cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
				if cmd.Run() != nil {
					return bad("commit")
				}
			}
		}
		return 0
	case "worker-fail":
		return 3
	case "verifier-check":
		for _, d := range []string{home, tmp} {
			if names, err := os.ReadDir(d); err != nil || len(names) != 0 {
				return bad("scratch not empty")
			}
		}
		if b, err := os.ReadFile(filepath.Join(top, "dummy.txt")); err != nil || string(b) != workerBytes {
			return 6
		}
		return 0
	case "verifier-fail":
		return 5
	case "worker-hold":
		// Finite: waits for the outer harness release (at most 60s), then
		// records its own completion.
		for i := 0; i < 600; i++ {
			if _, err := os.Lstat(filepath.Join(counter, "release")); err == nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		os.WriteFile(filepath.Join(counter, "worker-done"), []byte("done\n"), 0o600)
		return 0
	case "worker-sleep", "verifier-sleep":
		time.Sleep(60 * time.Second)
		return 0
	case "worker-signal", "verifier-signal":
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(time.Second)
		return 0
	case "worker-stdout-cap":
		os.Stdout.Write(make([]byte, StdoutCap+1))
		return 0
	case "worker-stderr-cap":
		os.Stderr.Write(make([]byte, StderrCap+1))
		return 0
	case "worker-group-child", "worker-escaped-holder", "worker-escaped-nopipe":
		if os.WriteFile(filepath.Join(top, "dummy.txt"), []byte(workerBytes), 0o644) != nil {
			return bad("write")
		}
		nonce, err := os.ReadFile(filepath.Join(counter, "nonce"))
		if err != nil {
			return bad("nonce")
		}
		kind := strings.TrimPrefix(mode, "worker-")
		// The child is this compiled binary again, by absolute argv[0], with
		// an explicitly empty environment and the worker's own stdio.
		cmd := &exec.Cmd{Path: os.Args[0], Args: []string{os.Args[0], "-baw-fake-child", kind, counter, string(nonce)},
			Env: []string{}, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
		if kind != "group-child" {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		if cmd.Start() != nil {
			return bad("child start")
		}
		os.WriteFile(filepath.Join(counter, "child-started"), []byte("1\n"), 0o600)
		cmd.Process.Release()
		// Return only after the child is ready and the outer harness, having
		// observed it alive, allows it; both waits are finite.
		if !waitFor(filepath.Join(counter, "child-ready"), "", 20*time.Second) {
			return bad("child ready")
		}
		if !waitFor(filepath.Join(counter, "worker-go"), "", 20*time.Second) {
			return 96
		}
		return 0
	}
	return bad("mode")
}

// waitFor polls until path exists (and, when want is non-empty, holds
// exactly want) or d elapses.
func waitFor(path, want string, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && (want == "" || string(b) == want) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// fakeChild is the finite descendant started by the boundary worker modes.
// It holds an exclusive lock on DIR/child-lock for its whole life so the
// outer harness can observe liveness without any process ID. "group-child"
// stays in the worker's process group; "escaped-holder" and
// "escaped-nopipe" are in their own session, and "escaped-nopipe" closes the
// inherited stdout/stderr before reporting ready. It then waits for
// DIR/child-release holding its nonce (at most 30s), records child-done and
// exits; a group child is normally terminated by the executor's cleanup.
func fakeChild(kind, dir, nonce string) int {
	lock, err := os.OpenFile(filepath.Join(dir, "child-lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return 80
	}
	defer lock.Close()
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return 81
	}
	relation := "other"
	switch syscall.Getpgrp() {
	case syscall.Getppid():
		relation = "worker-group"
	case os.Getpid():
		relation = "own-group"
	}
	if kind == "escaped-nopipe" {
		os.Stdout.Close()
		os.Stderr.Close()
	} else {
		os.Stdout.WriteString("SECRET-CHILD-STDOUT")
	}
	tmp := filepath.Join(dir, "child-ready.tmp")
	if os.WriteFile(tmp, []byte(nonce+" "+kind+" "+relation+"\n"), 0o600) != nil ||
		os.Rename(tmp, filepath.Join(dir, "child-ready")) != nil {
		return 82
	}
	if !waitFor(filepath.Join(dir, "child-release"), nonce, 30*time.Second) {
		os.WriteFile(filepath.Join(dir, "child-done"), []byte("deadline\n"), 0o600)
		return 83
	}
	os.WriteFile(filepath.Join(dir, "child-done"), []byte(nonce), 0o600)
	return 0
}
