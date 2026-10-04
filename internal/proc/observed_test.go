package proc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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

// With BAW_PROC_HELPER=marker the test binary is a finite fixture leader: it
// holds an exclusive lock on DIR/lock for its whole life, then makes
// DIR/ready visible containing its nonce and exits after DIR/release appears
// or 20 seconds. The harness observes it only through these files: the lock
// is free exactly when no fixture process holds it. No PID is used.
func init() {
	if os.Getenv("BAW_PROC_HELPER") == "marker" {
		os.Exit(markerHelper(os.Getenv("BAW_PROC_DIR"), os.Getenv("BAW_PROC_NONCE")))
	}
}

func markerHelper(dir, nonce string) int {
	l, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR, 0)
	if err != nil {
		return 2
	}
	if syscall.Flock(int(l.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return 3
	}
	tmp := filepath.Join(dir, "ready.tmp")
	if os.WriteFile(tmp, []byte(nonce), 0o600) != nil || os.Rename(tmp, filepath.Join(dir, "ready")) != nil {
		return 4
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if _, err := os.Lstat(filepath.Join(dir, "release")); err == nil {
			break
		}
	}
	runtime.KeepAlive(l)
	return 0
}

type markerFixture struct{ dir, nonce string }

func newMarkerFixture(t *testing.T) *markerFixture {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	m := &markerFixture{dir: t.TempDir(), nonce: hex.EncodeToString(b)}
	if err := os.WriteFile(filepath.Join(m.dir, "lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m *markerFixture) spec(t *testing.T) Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{Path: exe, Env: []string{"BAW_PROC_HELPER=marker", "BAW_PROC_DIR=" + m.dir, "BAW_PROC_NONCE=" + m.nonce},
		Timeout: 30 * time.Second, StdoutCap: 64, StderrCap: 64}
}

// held reports whether a fixture process holds the lock now.
func (m *markerFixture) held() bool {
	l, err := os.Open(filepath.Join(m.dir, "lock"))
	if err != nil {
		return false
	}
	defer l.Close()
	err = syscall.Flock(int(l.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		syscall.Flock(int(l.Fd()), syscall.LOCK_UN)
	}
	return errors.Is(err, syscall.EWOULDBLOCK)
}

// awaitStarted waits at most d for the fixture's own ready marker with the
// expected nonce while it is observed holding its lock.
func (m *markerFixture) awaitStarted(d time.Duration) bool {
	for deadline := time.Now().Add(d); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(m.dir, "ready")); err == nil && string(b) == m.nonce && m.held() {
			return true
		}
	}
	return false
}

// reconcile releases the finite fixture and waits at most 10 seconds until
// its lock is free, so it is independently known to have ended. It does not
// rely on the runner having joined it.
func (m *markerFixture) reconcile(t *testing.T) {
	t.Helper()
	os.WriteFile(filepath.Join(m.dir, "release"), nil, 0o600)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if !m.held() {
			return
		}
	}
	t.Errorf("marker fixture in %s still holds its lock", m.dir)
}

// Watcher setup failure after a successful Start: the fault is injected only
// after the fixture is independently visible as started (nonce marker and
// held lock). Started stays true, so the command is never reported as not
// started; legacy Run still says ErrStart. The leader is then joined only in
// the background, so Joined stays false: the fixture's end is established by
// its released lock, not by any wait having returned.
func TestObservedWatcherSetupFailureAfterStart(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		m := newMarkerFixture(t)
		established := false
		watchSetupFault = func() error {
			established = m.awaitStarted(10 * time.Second)
			return errors.New("injected")
		}
		var o Observation
		var err error
		if legacy {
			_, err = Run(context.Background(), m.spec(t))
		} else {
			_, o, err = RunObserved(context.Background(), m.spec(t))
		}
		watchSetupFault = nil
		m.reconcile(t)
		if !established {
			t.Fatalf("legacy=%v: fault injected without an observed start marker", legacy)
		}
		if !errors.Is(err, ErrStart) {
			t.Fatalf("legacy=%v: %v", legacy, err)
		}
		if !legacy && (!o.Started || !o.WatcherFailed || o.Usable() || o.Joined) {
			t.Fatalf("%+v", o)
		}
	}
}

// Watcher runtime failure: the fault channel closes only after the fixture is
// independently visible as started; the observation is unusable without
// waiting for the timeout, and the fixture is reconciled by its lock.
func TestObservedWatcherRuntimeFailure(t *testing.T) {
	m := newMarkerFixture(t)
	ch := make(chan struct{})
	established := make(chan bool, 1)
	watchRuntimeFault = func() <-chan struct{} {
		go func() {
			established <- m.awaitStarted(10 * time.Second)
			close(ch)
		}()
		return ch
	}
	start := time.Now()
	_, o, err := RunObserved(context.Background(), m.spec(t))
	elapsed := time.Since(start)
	watchRuntimeFault = nil
	ok := <-established
	<-ch
	m.reconcile(t)
	if !ok {
		t.Fatal("runtime fault injected without an observed start marker")
	}
	if !o.Started || !o.WatcherFailed || o.Usable() || !errors.Is(err, ErrCleanup) || elapsed > 15*time.Second {
		t.Fatalf("%+v %v after %v", o, err, elapsed)
	}
}

var errInjectedRead = errors.New("injected read error")

// failingReader passes through the actual pipe reads until real bytes have
// arrived, then fails with a non-EOF error before any EOF was seen.
type failingReader struct {
	r        io.Reader
	got      int
	sawEOF   bool
	injected bool
}

func (f *failingReader) Read(b []byte) (int, error) {
	if f.got > 0 {
		f.injected = true
		return 0, errInjectedRead
	}
	n, err := f.r.Read(b)
	f.got += n
	if err == io.EOF {
		f.sawEOF = true
	}
	return n, err
}

// A non-EOF read failure at the reader boundary of one stream, distinct from
// forced closure: that stream's EOF is not established and the observation is
// unusable, while the other stream's actual EOF and the exit/join/group facts
// are observed independently. Run's result for the same failure is unchanged
// (it returns the bytes read before the failure and no error).
func TestObservedPipeReadErrorIsNotEOF(t *testing.T) {
	for _, onStdout := range []bool{true, false} {
		for _, legacy := range []bool{false, true} {
			var fr *failingReader
			pipeReader = func(r io.Reader, stdout bool) io.Reader {
				if stdout != onStdout {
					return r
				}
				fr = &failingReader{r: r}
				return fr
			}
			spec := Spec{Path: "/bin/sh", Args: []string{"-c", "printf ok; printf er >&2"}, Timeout: 5 * time.Second, StdoutCap: 64, StderrCap: 64}
			var out []byte
			var o Observation
			var err error
			if legacy {
				out, err = Run(context.Background(), spec)
			} else {
				out, o, err = RunObserved(context.Background(), spec)
			}
			pipeReader = nil
			if fr == nil || !fr.injected || fr.sawEOF || fr.got == 0 {
				t.Fatalf("stdout=%v: read failure not injected after real bytes before EOF: %+v", onStdout, fr)
			}
			if err != nil || string(out) != "ok" {
				t.Fatalf("stdout=%v legacy=%v: result %q %v", onStdout, legacy, out, err)
			}
			if legacy {
				continue
			}
			if o.StdoutEOF == onStdout || o.StderrEOF != onStdout || o.Usable() {
				t.Fatalf("stdout=%v: read error counted as EOF: %+v", onStdout, o)
			}
			if !(o.Started && o.Exited && o.ExitCode == 0 && o.Joined && o.GroupAbsent) {
				t.Fatalf("stdout=%v: other facts %+v", onStdout, o)
			}
		}
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
