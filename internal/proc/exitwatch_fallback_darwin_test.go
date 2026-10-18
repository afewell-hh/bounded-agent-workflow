package proc

import (
	"fmt"
	"reflect"
	"syscall"
	"testing"
)

// scriptedProof records each call through f, with the number of close calls
// already made, and returns result.
func scriptedProof(f *fakeOps, result bool, calls *int) exitProof {
	return func(pid int) bool {
		*calls++
		_, closes := f.snapshot()
		f.record(fmt.Sprintf("proof %d after %d close", pid, closes))
		return result
	}
}

// TestExitFallbackSetup (named outside the existing ^TestExitWatch gate
// pattern): only a raw ESRCH registration error consults the
// proof, and only after the queue's one Close call has returned. A proved exit
// returns the exit and done channels already closed, an open failure channel
// and no error; every other outcome returns the original error and nil
// channels. Creation errors never consult the proof.
func TestExitFallbackSetup(t *testing.T) {
	setup := []string{"kqueue", "cloexec 7", "register 7", "close 7"}
	withProof := append(append([]string(nil), setup...), "proof 4242 after 1 close")
	for _, c := range []struct {
		name      string
		createErr error
		regErr    error
		closeErr  error
		proof     *bool // nil: no proof supplied
		want      error // nil: fallback success expected
		calls     []string
		closes    int
		proofs    int
	}{
		{name: "esrch-proved", regErr: syscall.ESRCH, proof: ptr(true),
			calls: withProof, closes: 1, proofs: 1},
		{name: "esrch-proved-close-error", regErr: syscall.ESRCH, closeErr: errClose, proof: ptr(true),
			calls: withProof, closes: 1, proofs: 1},
		{name: "esrch-not-proved", regErr: syscall.ESRCH, proof: ptr(false), want: syscall.ESRCH,
			calls: withProof, closes: 1, proofs: 1},
		{name: "esrch-no-proof", regErr: syscall.ESRCH, want: syscall.ESRCH,
			calls: setup, closes: 1},
		{name: "other-error", regErr: errRegister, proof: ptr(true), want: errRegister,
			calls: setup, closes: 1},
		{name: "eintr", regErr: syscall.EINTR, proof: ptr(true), want: syscall.EINTR,
			calls: setup, closes: 1},
		{name: "eperm", regErr: syscall.EPERM, proof: ptr(true), want: syscall.EPERM,
			calls: setup, closes: 1},
		{name: "wrapped-esrch", regErr: wrappedESRCH, proof: ptr(true), want: wrappedESRCH,
			calls: setup, closes: 1},
		{name: "create-esrch", createErr: syscall.ESRCH, proof: ptr(true), want: syscall.ESRCH,
			calls: []string{"kqueue"}},
		{name: "create-error", createErr: errCreate, proof: ptr(true), want: errCreate,
			calls: []string{"kqueue"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeOps(nil)
			f.createErr, f.regErr, f.closeErr = c.createErr, c.regErr, c.closeErr
			close(f.gate) // setup closes synchronously; nothing to hold
			var proofs int
			var proof exitProof
			if c.proof != nil {
				proof = scriptedProof(f, *c.proof, &proofs)
			}
			ch, failed, done, err := watchExitProof(fakePID, f.ops(), proof)
			if c.want != nil {
				if err != c.want || ch != nil || failed != nil || done != nil {
					t.Errorf("got %v %v %v %v, want %v and nil channels", err, ch, failed, done, c.want)
				}
			} else {
				if err != nil || ch == nil || failed == nil || done == nil {
					t.Fatalf("got %v %v %v %v, want no error and three channels", err, ch, failed, done)
				}
				if !isClosed(ch) || !isClosed(done) || isClosed(failed) {
					t.Errorf("exit closed %v, done closed %v, failure closed %v; want true, true, false",
						isClosed(ch), isClosed(done), isClosed(failed))
				}
			}
			calls, closes := f.snapshot()
			if closes != c.closes || proofs != c.proofs || !reflect.DeepEqual(calls, c.calls) {
				t.Errorf("calls %q (%d closes, %d proofs), want %q (%d, %d)", calls, closes, proofs, c.calls, c.closes, c.proofs)
			}
		})
	}

	// watchExitOps keeps the old setup behavior: no proof, so ESRCH is
	// returned as the registration error.
	t.Run("watchExitOps-esrch-unchanged", func(t *testing.T) {
		f := newFakeOps(nil)
		f.regErr = syscall.ESRCH
		close(f.gate)
		ch, failed, done, err := watchExitOps(fakePID, f.ops())
		if err != syscall.ESRCH || ch != nil || failed != nil || done != nil {
			t.Errorf("got %v %v %v %v, want ESRCH and nil channels", err, ch, failed, done)
		}
		if calls, closes := f.snapshot(); closes != 1 || !reflect.DeepEqual(calls, setup) {
			t.Errorf("calls %q (%d closes), want %q (1)", calls, closes, setup)
		}
	})

	// A successful registration never consults the proof, and the normal
	// watcher path keeps its ordering under the same oracle as
	// TestExitWatchOrdering.
	for _, c := range []orderingCase{
		{name: "registered-exit", waits: []waitStep{{n: 1, fflags: syscall.NOTE_EXIT}},
			calls: []string{"kqueue", "cloexec 7", "register 7", "wait 7", "close 7"}},
		{name: "registered-wait-error", waits: []waitStep{{n: -1, err: errWait}}, failure: true,
			calls: []string{"kqueue", "cloexec 7", "register 7", "wait 7", "close 7"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var proofs int
			watch := func(pid int, ops exitOps) (<-chan struct{}, <-chan struct{}, <-chan struct{}, error) {
				return watchExitProof(pid, ops, func(int) bool { proofs++; return true })
			}
			premature, problems := checkOrdering(watch, c)
			if len(premature) != 0 || len(problems) != 0 || proofs != 0 {
				t.Errorf("premature %q, problems %q, %d proofs", premature, problems, proofs)
			}
		})
	}
}

var wrappedESRCH = fmt.Errorf("wrapped: %w", syscall.ESRCH)

func ptr(b bool) *bool { return &b }
