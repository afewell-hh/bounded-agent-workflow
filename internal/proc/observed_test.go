package proc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func obsSh(ctx context.Context, body string, timeout time.Duration) ([]byte, Observation, error) {
	return RunObserved(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", body}, Timeout: timeout, StdoutCap: 64, StderrCap: 64})
}

func TestObservedNormalExitFacts(t *testing.T) {
	out, o, err := obsSh(context.Background(), "printf ok", 5*time.Second)
	if err != nil || string(out) != "ok" || !o.Usable() || o.ExitCode != 0 {
		t.Fatalf("%q %+v %v", out, o, err)
	}
	if !(o.Started && o.Exited && o.Joined && o.GroupAbsent && o.StdoutEOF && o.StderrEOF) {
		t.Fatalf("facts %+v", o)
	}
	_, o, err = obsSh(context.Background(), "exit 7", 5*time.Second)
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 7 || !o.Usable() || o.ExitCode != 7 {
		t.Fatalf("nonzero %+v %v", o, err)
	}
}

func TestObservedSignalIsNotNormalExit(t *testing.T) {
	_, o, _ := obsSh(context.Background(), "kill -KILL $$", 5*time.Second)
	if !o.Started || o.Exited || !o.Signaled || o.Usable() {
		t.Fatalf("%+v", o)
	}
}

func TestObservedCancelledBeforeStartIsNotStarted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	marker := filepath.Join(t.TempDir(), "m")
	_, o, err := obsSh(ctx, "touch '"+marker+"'", 5*time.Second)
	if o.Started || !o.Cancelled || !errors.Is(err, ErrStart) {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Fatal("cancelled command ran")
	}
	// Legacy Run keeps its Start-before-context behavior.
	if _, err := Run(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", "touch '" + marker + "'; sleep 5"}, Timeout: time.Minute, StdoutCap: 8, StderrCap: 8}); !errors.Is(err, ErrTimeout) {
		t.Fatalf("legacy: %v", err)
	}
}

func TestObservedStartFailureIsNotStarted(t *testing.T) {
	_, o, err := RunObserved(context.Background(), Spec{Path: "/nonexistent/x", Timeout: time.Second, StdoutCap: 8, StderrCap: 8})
	if o.Started || !errors.Is(err, ErrStart) {
		t.Fatalf("%+v %v", o, err)
	}
}

// Watcher setup failure after a successful Start: Started stays true, so the
// command is never reported as not started; legacy Run still says ErrStart.
func TestObservedWatcherSetupFailureAfterStart(t *testing.T) {
	watchSetupFault = func() error { return errors.New("injected") }
	defer func() { watchSetupFault = nil }()
	marker := filepath.Join(t.TempDir(), "m")
	_, o, err := obsSh(context.Background(), "touch '"+marker+"'; sleep 5", 5*time.Second)
	if !o.Started || !o.WatcherFailed || o.Usable() || !errors.Is(err, ErrStart) {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := Run(context.Background(), Spec{Path: "/bin/sh", Args: []string{"-c", "sleep 5"}, Timeout: time.Second, StdoutCap: 8, StderrCap: 8}); !errors.Is(err, ErrStart) {
		t.Fatalf("legacy: %v", err)
	}
}

func TestObservedWatcherRuntimeFailure(t *testing.T) {
	ch := make(chan struct{})
	close(ch)
	watchRuntimeFault = func() <-chan struct{} { return ch }
	defer func() { watchRuntimeFault = nil }()
	start := time.Now()
	_, o, _ := obsSh(context.Background(), "sleep 30", 30*time.Second)
	if !o.Started || !o.WatcherFailed || o.Usable() || time.Since(start) > 5*time.Second {
		t.Fatalf("%+v after %v", o, time.Since(start))
	}
}

// Actual pipe EOF versus a reader that ended for another reason.
func TestObservedPipeEOFVersusReadError(t *testing.T) {
	readFaultHook = func(stdout bool) bool { return stdout }
	_, o, err := obsSh(context.Background(), "printf ok", 5*time.Second)
	readFaultHook = nil
	if err != nil || o.StdoutEOF || !o.StderrEOF || o.Usable() {
		t.Fatalf("read error must not count as EOF: %+v %v", o, err)
	}
}

// An escaped holder keeps stdout open: the forced close is not EOF, the
// observation is unusable and the escaped process is not signalled.
func TestObservedEscapedPipeHolderForcedClose(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pid")
	_, o, _ := RunObserved(context.Background(), Spec{Path: "/bin/sh", Args: []string{"-c",
		`'` + exe + `' & while [ ! -s '` + pidFile + `' ]; do sleep 0.01; done; exit 0`},
		Env:     []string{"BAW_PROC_HELPER=escape", "BAW_PROC_PIDFILE=" + pidFile, "PATH=/usr/bin:/bin"},
		Timeout: 10 * time.Second, StdoutCap: 64, StderrCap: 64})
	pid := readPID(t, pidFile)
	defer killTestChild(t, pid)
	if o.StdoutEOF || o.Usable() || !o.Exited || o.ExitCode != 0 {
		t.Fatalf("%+v", o)
	}
}

func TestObservedTimeoutAndCapsUnusable(t *testing.T) {
	_, o, _ := obsSh(context.Background(), "sleep 30", 200*time.Millisecond)
	if !o.TimedOut || o.Usable() {
		t.Fatalf("timeout %+v", o)
	}
	_, o, _ = obsSh(context.Background(), "head -c 65 /dev/zero", 5*time.Second)
	if !o.OutputLimit || o.Usable() {
		t.Fatalf("cap %+v", o)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, o, _ = obsSh(ctx, "sleep 30", 30*time.Second)
	if !o.Started || !o.Cancelled || o.Usable() {
		t.Fatalf("cancel %+v", o)
	}
}

// Dir sets the working directory; empty keeps the caller's.
func TestSpecDir(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	out, _, err := RunObserved(context.Background(), Spec{Path: "/bin/pwd", Args: []string{"-P"}, Dir: dir, Timeout: 5 * time.Second, StdoutCap: 4096, StderrCap: 64})
	if err != nil || string(out) != dir+"\n" {
		t.Fatalf("%q %v", out, err)
	}
	wd, _ := os.Getwd()
	wd, _ = filepath.EvalSymlinks(wd)
	out, err = Run(context.Background(), Spec{Path: "/bin/pwd", Args: []string{"-P"}, Timeout: 5 * time.Second, StdoutCap: 4096, StderrCap: 64})
	if err != nil || string(out) != wd+"\n" {
		t.Fatalf("legacy cwd %q %v", out, err)
	}
}
