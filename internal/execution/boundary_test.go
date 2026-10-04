package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// childAlive reports whether the fixture child still holds its lock. The
// lock, not a process ID, is the liveness fact: it is released when the
// child's descriptors close at exit.
func childAlive(t *testing.T, dir string) bool {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, "child-lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatalf("child lock: %v", err)
	}
	defer f.Close()
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("child lock probe: %v", err)
	}
	return true
}

// waitChildGone polls the lock until the child has exited or d elapses.
func waitChildGone(t *testing.T, dir string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for childAlive(t, dir) {
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
	return true
}

// Process-group boundary probes through the real Execute/RunObserved
// pipeline (C2 §8). The worker starts one finite compiled child, waits for
// its ready record, and returns 0 only after this outer harness has itself
// observed the child alive. Outcomes, OIDs and start counts are hand-written:
//
//   - group-child stays in the worker's group: the executor's cleanup
//     terminates it, the worker is usable and the verifier is admitted.
//   - escaped-holder leaves the group but keeps the output pipe: EOF is never
//     reached, so the worker is unverified and no verifier starts.
//   - escaped-nopipe leaves the group and closes the pipes before the worker
//     returns: nothing the executor observes remains, so the verifier IS
//     admitted and verification_passed is reported while the child is still
//     running. This is the documented limit of the observation, not
//     containment; the harness itself releases and reconciles the child.
func TestDescendantBoundaries(t *testing.T) {
	type variant struct {
		kind, relation          string
		outcome, ws, wc, vs, vc string
		after                   bool
		verifierStarts          int
		aliveAfter              bool
	}
	unknown := "unknown"
	variants := []variant{
		{"group-child", "worker-group", "verification_passed", "exited", "0", "exited", "0", true, 1, false},
		{"escaped-holder", "own-group", "worker_unverified", "unverified", unknown, "not_started", unknown, false, 0, true},
		{"escaped-nopipe", "own-group", "verification_passed", "exited", "0", "exited", "0", true, 1, true},
	}
	for _, v := range variants {
		for _, format := range []string{"sha1", "sha256"} {
			for _, asJSON := range []bool{false, true} {
				name := v.kind + "-" + format + "-text"
				if asJSON {
					name = v.kind + "-" + format + "-json"
				}
				t.Run(name, func(t *testing.T) {
					descendantBoundary(t, v.kind, v.relation, format, asJSON, func(f *fx, out string) {
						after := unknown
						if v.after {
							after = f.head
						}
						if asJSON {
							repo := map[string]any{"object_format": format, "before_head": f.head, "after_head": nil}
							if v.after {
								repo["after_head"] = f.head
							}
							var wc, vc any
							if v.wc != unknown {
								wc = v.wc
							}
							if v.vc != unknown {
								vc = v.vc
							}
							checkResultJSON(t, out, v.outcome, prog(v.ws, wc), prog(v.vs, vc), repo)
						} else if out != wantText(v.outcome, v.ws, v.wc, v.vs, v.vc, format, f.head, after) {
							t.Fatalf("text %q", out)
						}
					}, v.outcome == "verification_passed", v.verifierStarts, v.aliveAfter)
				})
			}
		}
	}
}

func descendantBoundary(t *testing.T, kind, relation, format string, asJSON bool, check func(*fx, string),
	wantOK bool, wantVerifier int, aliveAfter bool) {
	f := newFx(t, format)
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(b)
	if err := os.WriteFile(filepath.Join(f.count, "nonce"), []byte(nonce), 0o600); err != nil {
		t.Fatal(err)
	}
	f.setPlan("worker-"+kind, "verifier-check")
	if len(f.head) != map[string]int{"sha1": 40, "sha256": 64}[format] {
		t.Fatalf("head %q", f.head)
	}

	type result struct {
		ok  bool
		out string
		err error
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	done := make(chan result, 1)
	go func() {
		ok, out, err := f.exec(ctx, asJSON)
		done <- result{ok, out, err}
	}()
	joined := false
	var r result
	join := func() result {
		if !joined {
			r, joined = <-done, true
		}
		return r
	}
	release := func() {
		os.WriteFile(filepath.Join(f.count, "worker-go"), nil, 0o600)
		os.WriteFile(filepath.Join(f.count, "child-release"), []byte(nonce), 0o600)
	}
	// On every path, including failed assertions: release both fixtures, join
	// Execute, and confirm the known child (if it started) has exited.
	defer func() {
		release()
		cancel()
		join()
		if _, err := os.Lstat(filepath.Join(f.count, "child-started")); err == nil {
			if !waitFor(filepath.Join(f.count, "child-lock"), "", 5*time.Second) || !waitChildGone(t, f.count, 40*time.Second) {
				t.Error("fixture child not reconciled")
			}
		}
	}()

	// The child is ready, in the expected group, and alive before the worker
	// is allowed to return.
	ready := filepath.Join(f.count, "child-ready")
	want := nonce + " " + kind + " " + relation + "\n"
	if !waitFor(ready, want, 20*time.Second) {
		rb, _ := os.ReadFile(ready)
		t.Fatalf("child ready %q want %q", rb, want)
	}
	if !childAlive(t, f.count) {
		t.Fatal("child not alive before worker return")
	}
	if f.starts("worker") != 1 || f.starts("verifier") != 0 {
		t.Fatalf("starts before worker return %d/%d", f.starts("worker"), f.starts("verifier"))
	}
	if err := os.WriteFile(filepath.Join(f.count, "worker-go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res := join()
	if res.err != nil || res.ok != wantOK {
		t.Fatalf("ok=%v err=%v out=%q", res.ok, res.err, res.out)
	}
	check(f, res.out)
	if f.starts("worker") != 1 || f.starts("verifier") != wantVerifier {
		t.Fatalf("starts %d/%d", f.starts("worker"), f.starts("verifier"))
	}
	if _, err := os.Lstat(filepath.Join(f.count, "worker-bad")); err == nil {
		t.Fatal("worker fixture reported a bad environment")
	}
	if _, err := os.Lstat(filepath.Join(f.count, "verifier-bad")); err == nil {
		t.Fatal("verifier fixture reported a bad environment")
	}

	// Child state after the result was returned.
	if aliveAfter {
		if !childAlive(t, f.count) {
			t.Fatal("escaped child not alive after the result")
		}
	} else if !waitChildGone(t, f.count, 5*time.Second) {
		t.Fatal("group child survived executor cleanup")
	}
	if _, err := os.Lstat(filepath.Join(f.count, "child-done")); err == nil {
		t.Fatal("child finished before release")
	}

	for _, n := range []string{"intent.json", "result.json"} {
		rb, err := os.ReadFile(filepath.Join(f.attempt(), n))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"SECRET", nonce, f.base} {
			if strings.Contains(string(rb), s) || strings.Contains(res.out, s) {
				t.Fatalf("%s or output leaks %q", n, s)
			}
		}
	}

	// The harness releases the escaped child and observes it finish.
	if aliveAfter {
		release()
		if !waitFor(filepath.Join(f.count, "child-done"), nonce, 35*time.Second) || !waitChildGone(t, f.count, 5*time.Second) {
			t.Fatal("escaped child did not finish after release")
		}
	}

	// A repeat refuses with zero additional starts.
	if _, out, err := f.exec(context.Background(), asJSON); !codeIs(err, "execution_exists") || out != "" ||
		f.starts("worker") != 1 || f.starts("verifier") != wantVerifier {
		t.Fatalf("repeat: %v %q", err, out)
	}
}
