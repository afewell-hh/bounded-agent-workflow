package cli

import (
	"bytes"
	"context"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

// wantGlobalUsage is the frozen C2 §9 global help, written out by hand.
const wantGlobalUsage = "Usage:\n" +
	"  baw inspect --repo PATH [--checkpoint FULL_COMMIT_SHA] [--coordination-file FILE | --github OWNER/REPO#NUMBER] [--json]\n" +
	"  baw run create --state-dir DIR --run-id ID --repo PATH --ticket URL --scope-sha256 HASH --policy-commit OID [--json]\n" +
	"  baw status --state-dir DIR --run-id ID [--json]\n" +
	"  baw run diagnose --state-dir DIR --run-id ID [--json]\n" +
	"  baw context --repo PATH --role ROLE [--json]\n" +
	"  baw run execute --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]\n" +
	"  baw run review --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]\n" +
	"  baw --help\n" +
	"\n" +
	"baw inspect reports read-only Git state counts, fixed maintained-source\n" +
	"references and coordination metadata. Remote freshness, runtime state,\n" +
	"process ownership and reservation ownership are always unknown.\n" +
	"\n" +
	"baw run create saves an immutable run record of supplied references and the\n" +
	"observed committed HEAD in an existing private DIR. baw status reads that\n" +
	"saved record without running Git. A record is not an approval, completed\n" +
	"work or live state; authority is not evaluated.\n" +
	"\n" +
	"baw run diagnose counts the structural state of that record and its\n" +
	"retained staging files, read-only and without Git. Observations are\n" +
	"sequential, not a snapshot; it gives no recovery advice.\n" +
	"\n" +
	"baw context lists fixed onboarding source references for a lead, worker or\n" +
	"reviewer with the same read-only Git observations. It grants no assignment,\n" +
	"readiness or authority and starts no agent.\n" +
	"\n" +
	"Exit status: 0 success, 1 failure (stderr \"baw: CODE\"), 2 invalid usage.\n" +
	"\n" +
	"Run execute records one local worker and verification attempt; it evaluates no approval,\n" +
	"provides no native adapter and never retries or recovers an interrupted attempt.\n" +
	"\n" +
	"Run review records one trusted reviewer-program verdict for a committed candidate;\n" +
	"it evaluates no approval and supplies no native agent adapter, verification or merge authority.\n"

func TestGlobalUsageFrozenBytes(t *testing.T) {
	if Usage != wantGlobalUsage {
		t.Fatalf("global usage differs:\n%s", Usage)
	}
	for _, a := range [][]string{{"--help"}, {"-h"}, {"help"}, {"inspect", "--help"}, {"run", "--help"}, {"status", "--help"}} {
		var out, errb bytes.Buffer
		if code := Run(a, &out, &errb); code != 0 || out.String() != wantGlobalUsage || errb.Len() != 0 {
			t.Errorf("%v: %d %q", a, code, errb.String())
		}
	}
}

func TestExecuteSyntax(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"run", "execute", "--help"}, &out, &errb); code != 0 || out.String() != ExecuteUsage {
		t.Fatalf("help %d %q", code, out.String())
	}
	id := "0123456789abcdef0123456789abcdef"
	bad := [][]string{
		{"run", "execute"}, {"run", "execute", "-h"}, {"run", "execute", "--help", "--json"},
		{"run", "execute", "--repo", "r", "--state-dir", "s", "--run-id", id},
		{"run", "execute", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "--plan", "p"},
		{"run", "execute", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "--json=1"},
		{"run", "execute", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "extra"},
		{"run", "execute", "--repo", "", "--state-dir", "s", "--run-id", id, "--plan", "p"},
		{"run", "execute", "--repo", "r\x00", "--state-dir", "s", "--run-id", id, "--plan", "p"},
		{"run", "execute", "--repo", "r", "--state-dir", "s", "--run-id", "0123456789ABCDEF0123456789abcdef", "--plan", "p"},
		{"run", "execute", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "--timeout", "5"},
	}
	for _, a := range bad {
		out.Reset()
		errb.Reset()
		if code := Run(a, &out, &errb); code != 2 || out.Len() != 0 || errb.String() != "baw: invalid_usage\n" {
			t.Errorf("%q: %d %q", a, code, errb.String())
		}
	}
	// Help delivery failure.
	errb.Reset()
	if code := Run([]string{"run", "execute", "--help"}, failing{}, &errb); code != 1 || errb.String() != "baw: output_unavailable\n" {
		t.Fatalf("help delivery: %d %q", code, errb.String())
	}
}

type failing struct{}

func (failing) Write(p []byte) (int, error) { return 0, os.ErrClosed }

// After an execute call returns, the prior signal behavior is restored: a
// SIGINT handler installed by the caller still receives the signal and the
// execute context no longer consumes it.
func TestExecuteRestoresSignals(t *testing.T) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT)
	defer signal.Stop(ch)
	var out, errb bytes.Buffer
	dir := t.TempDir()
	code := Run([]string{"run", "execute", "--repo", dir, "--state-dir", dir + "/missing", "--run-id", "0123456789abcdef0123456789abcdef", "--plan", dir + "/p"}, &out, &errb)
	if code != 1 || errb.String() != "baw: state_unavailable\n" {
		t.Fatalf("%d %q", code, errb.String())
	}
	syscall.Kill(os.Getpid(), syscall.SIGINT)
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("caller handler did not receive SIGINT")
	}
}

// A cancelled context reaching the execute path reports execution_cancelled
// with empty stdout before any acquisition.
func TestExecuteWithCancelledContext(t *testing.T) {
	// The state root must pass its 0700/owner check so that cancellation,
	// not state_permissions, is the first failure: t.TempDir subdirectories
	// follow the umask, so set and confirm the mode explicitly.
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !fi.IsDir() || fi.Mode().Perm() != 0o700 || fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 ||
		!ok || int(st.Uid) != os.Getuid() {
		t.Fatalf("fixture root %v not an owned 0700 directory", fi.Mode())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errb bytes.Buffer
	req, _, ok := parseExecute([]string{"run", "execute", "--repo", root, "--state-dir", root, "--run-id", "0123456789abcdef0123456789abcdef", "--plan", root + "/p"})
	if !ok {
		t.Fatal("syntax")
	}
	if code := executeWith(ctx, req, &out, &errb); code != 1 || out.Len() != 0 || errb.String() != "baw: execution_cancelled\n" {
		t.Fatalf("%d %q", code, errb.String())
	}
}
