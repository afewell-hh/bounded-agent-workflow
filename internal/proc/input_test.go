package proc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func packetFile(t *testing.T, content string) *os.File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "packet")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func catSpec() Spec {
	return Spec{Path: "/bin/cat", Timeout: 5 * time.Second, StdoutCap: 4096, StderrCap: 64}
}

// The packet is the child's stdin from the caller's offset, read to EOF; the
// caller's handle stays open and usable after return.
func TestInputPacketIsStdin(t *testing.T) {
	f := packetFile(t, "skip:exact packet bytes\n")
	if _, err := f.Seek(5, 0); err != nil {
		t.Fatal(err)
	}
	out, o, err := RunObservedInput(context.Background(), catSpec(), f, nil)
	if err != nil || string(out) != "exact packet bytes\n" || !o.Usable() {
		t.Fatalf("%q %+v %v", out, o, err)
	}
	if _, err := f.Seek(0, 0); err != nil {
		t.Fatalf("caller handle closed by runner: %v", err)
	}
}

// A nil packet and the legacy entry points keep /dev/null: cat sees EOF.
func TestInputNilPacketKeepsDevNull(t *testing.T) {
	out, o, err := RunObservedInput(context.Background(), catSpec(), nil, nil)
	if err != nil || len(out) != 0 || !o.Usable() {
		t.Fatalf("input nil: %q %+v %v", out, o, err)
	}
	out, o, err = RunObserved(context.Background(), catSpec())
	if err != nil || len(out) != 0 || !o.Usable() {
		t.Fatalf("observed: %q %+v %v", out, o, err)
	}
	out, err = Run(context.Background(), catSpec())
	if err != nil || len(out) != 0 {
		t.Fatalf("legacy: %q %v", out, err)
	}
	out, err = Run(context.Background(), Spec{Path: "/bin/sh", Args: []string{"-c", "test -c /dev/stdin && printf dev"},
		Timeout: 5 * time.Second, StdoutCap: 64, StderrCap: 64})
	if err != nil || string(out) != "dev" {
		t.Fatalf("legacy stdin is not a character device: %q %v", out, err)
	}
}

// A non-regular packet is refused before any start.
func TestInputNonRegularPacketNotStarted(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	marker := filepath.Join(t.TempDir(), "m")
	_, o, err := RunObservedInput(context.Background(), Spec{Path: "/bin/sh", Args: []string{"-c", "touch '" + marker + "'"},
		Timeout: 5 * time.Second, StdoutCap: 64, StderrCap: 64}, r, nil)
	if o.Started || !errors.Is(err, ErrStart) {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Fatal("child started with a pipe packet")
	}
}

type recorder struct {
	mu      sync.Mutex
	got     bytes.Buffer
	calls   int
	failAt  int // fail on this call number (1-based); 0 never
	running bool
	overlap bool
}

func (r *recorder) observe(p []byte) error {
	r.mu.Lock()
	if r.running {
		r.overlap = true
	}
	r.running = true
	r.calls++
	n := r.calls
	r.got.Write(p)
	r.running = false
	r.mu.Unlock()
	if r.failAt != 0 && n >= r.failAt {
		return errors.New("stop")
	}
	return nil
}

func (r *recorder) snapshot() (string, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.got.String(), r.calls
}

// The observer sees exactly the accepted stdout bytes in order, serially.
func TestInputObserverSeesStdoutInOrder(t *testing.T) {
	rec := &recorder{}
	out, o, err := RunObservedInput(context.Background(), Spec{Path: "/bin/sh",
		Args:    []string{"-c", "printf one; sleep 0.05; printf two; sleep 0.05; printf three; printf err >&2"},
		Timeout: 5 * time.Second, StdoutCap: 64, StderrCap: 64}, nil, rec.observe)
	got, calls := rec.snapshot()
	if err != nil || !o.Usable() || string(out) != "onetwothree" || got != "onetwothree" || calls < 1 || rec.overlap {
		t.Fatalf("%q %q %d %+v %v", out, got, calls, o, err)
	}
}

// An observer error cancels the invocation: the long-running child is
// cleaned up by the existing owner well before its timeout, the observation
// is unusable and the observer is never called again, during or after return.
func TestInputObserverErrorCancels(t *testing.T) {
	rec := &recorder{failAt: 1}
	start := time.Now()
	_, o, err := RunObservedInput(context.Background(), Spec{Path: "/bin/sh",
		Args:    []string{"-c", "printf first; sleep 0.2; printf second; sleep 30"},
		Timeout: 30 * time.Second, StdoutCap: 4096, StderrCap: 64}, nil, rec.observe)
	elapsed := time.Since(start)
	got, calls := rec.snapshot()
	if !o.Started || !o.Cancelled || o.Usable() || !errors.Is(err, ErrTimeout) || elapsed > 10*time.Second {
		t.Fatalf("%+v %v after %v", o, err, elapsed)
	}
	if calls != 1 || got != "first" {
		t.Fatalf("observer called after its error: %q %d", got, calls)
	}
	time.Sleep(50 * time.Millisecond)
	if _, c := rec.snapshot(); c != calls {
		t.Fatal("observer called after return")
	}
}

// A chunk over the stdout cap is counted first and never dispatched.
func TestInputObserverNotGivenOverCapBytes(t *testing.T) {
	rec := &recorder{}
	_, o, err := RunObservedInput(context.Background(), Spec{Path: "/bin/sh",
		Args:    []string{"-c", "printf abcd; sleep 0.1; printf efghijkl"},
		Timeout: 5 * time.Second, StdoutCap: 6, StderrCap: 64}, nil, rec.observe)
	got, _ := rec.snapshot()
	if !o.OutputLimit || o.Usable() || !errors.Is(err, ErrOutputLimit) || len(got) > 6 || got != "abcd" {
		t.Fatalf("%q %+v %v", got, o, err)
	}
}

// A cancelled context never starts the child, with or without inputs.
func TestInputCancelledBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := packetFile(t, "x")
	marker := filepath.Join(t.TempDir(), "m")
	_, o, err := RunObservedInput(ctx, Spec{Path: "/bin/sh", Args: []string{"-c", "touch '" + marker + "'"},
		Timeout: 5 * time.Second, StdoutCap: 64, StderrCap: 64}, f, func([]byte) error { return nil })
	if o.Started || !o.Cancelled || !errors.Is(err, ErrStart) {
		t.Fatalf("%+v %v", o, err)
	}
	if _, err := os.Lstat(marker); err == nil {
		t.Fatal("cancelled command ran")
	}
}

// Exactly one stdout capture must own the pipe. Two readers can consume safety
// events without delivering them to the observer, even if retained output looks valid.
func TestInputObserverHasSingleReader(t *testing.T) {
	readers := 0
	pipeReader = func(r io.Reader, stdout bool) io.Reader {
		if stdout {
			readers++
		}
		return r
	}
	defer func() { pipeReader = nil }()
	rec := &recorder{}
	out, o, err := RunObservedInput(context.Background(), catSpec(), packetFile(t, "complete event stream\n"), rec.observe)
	if readers != 1 {
		t.Fatalf("stdout reader constructions = %d, want 1", readers)
	}
	got, _ := rec.snapshot()
	if err != nil || !o.Usable() || string(out) != "complete event stream\n" || got != string(out) {
		t.Fatalf("incomplete observed stdout: %q %q %+v %v", out, got, o, err)
	}
}

func TestInputObserverErrorAfterImmediateExit(t *testing.T) {
	_, o, err := RunObservedInput(context.Background(), catSpec(), packetFile(t, "reject\n"), func([]byte) error { return errors.New("reject") })
	if !o.Cancelled || o.Usable() || !errors.Is(err, ErrTimeout) {
		t.Fatalf("observer rejection accepted: %+v %v", o, err)
	}
}
