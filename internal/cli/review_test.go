package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A child of this test binary started as "-baw-review-signal-child MODE SIG DIR"
// runs one review-only signal scenario instead of tests (dispatched from init
// so the existing TestMain stays unchanged).
func init() {
	if len(os.Args) == 5 && os.Args[1] == "-baw-review-signal-child" {
		os.Exit(reviewSignalChild(os.Args[2], os.Args[3], os.Args[4]))
	}
}

func reviewSignalChild(mode, sig, dir string) int {
	s := map[string]syscall.Signal{"INT": syscall.SIGINT, "TERM": syscall.SIGTERM}[sig]
	if s == 0 {
		return 60
	}
	switch mode {
	case "restored":
		// A valid review that fails before acquisition, then the signal: the
		// default disposition must terminate this process.
		missing := filepath.Join(dir, "missing")
		code := runReview([]string{"run", "review", "--repo", missing, "--state-dir", missing, "--run-id",
			strings.Repeat("a", 32), "--candidate", strings.Repeat("b", 40), "--plan", missing}, io.Discard, io.Discard)
		os.Stdout.WriteString(strconv.Itoa(code))
	case "held":
		// Control: a handler left installed absorbs the signal.
		signal.NotifyContext(context.Background(), s)
	default:
		return 61
	}
	syscall.Kill(os.Getpid(), s)
	time.Sleep(3 * time.Second)
	return 3
}

// wantReviewHelp is the frozen C2 command help, written out by hand.
const wantReviewHelp = "Usage: baw run review --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]\n" +
	"\n" +
	"Run one trusted reviewer program against a clean committed candidate after a recorded successful worker/verifier attempt.\n" +
	"Relative --repo, --state-dir and --plan paths resolve from the caller's working directory. Reviewer cwd and relative reviewer arguments use the physical Git top level.\n" +
	"A program verdict grants no approval, acceptance or merge authority. No native agent adapter is used.\n"

func TestReviewHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"run", "review", "--help"}, &out, &errb); code != 0 || out.String() != wantReviewHelp || errb.Len() != 0 {
		t.Fatalf("help %d %q %q", code, out.String(), errb.String())
	}
	if ReviewUsage != wantReviewHelp {
		t.Fatal("ReviewUsage differs")
	}
	// Only the new command help checks its sink.
	var e bytes.Buffer
	if code := Run([]string{"run", "review", "--help"}, failWriter{}, &e); code != 1 || e.String() != "baw: output_unavailable\n" {
		t.Fatalf("help sink failure %d %q", code, e.String())
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("sink") }

// Every malformed invocation is invalid_usage (exit 2) before any
// filesystem or Git access: the paths below do not exist.
func TestReviewSyntax(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")
	id := strings.Repeat("a", 32)
	oid := strings.Repeat("b", 40)
	valid := []string{"run", "review", "--repo", missing, "--state-dir", missing, "--run-id", id, "--candidate", oid, "--plan", missing}
	with := func(extra ...string) []string { return append(append([]string{}, valid...), extra...) }
	replace := func(name, val string) []string {
		a := append([]string{}, valid...)
		for i := range a {
			if a[i] == name {
				a[i+1] = val
			}
		}
		return a
	}
	drop := func(name string) []string {
		var a []string
		for i := 0; i < len(valid); i++ {
			if valid[i] == name {
				i++
				continue
			}
			a = append(a, valid[i])
		}
		return a
	}
	cases := map[string][]string{
		"no args":          {"run", "review"},
		"help with option": {"run", "review", "--help", "--json"},
		"help twice":       {"run", "review", "--help", "--help"},
		"-h":               {"run", "review", "-h"},
		"help word":        {"run", "review", "help"},
		"help=":            {"run", "review", "--help=1"},
		"positional":       with("extra"),
		"unknown":          with("--verbose"),
		"json value":       with("--json=true"),
		"json twice":       with("--json", "--json"),
		"repeat repo":      with("--repo", missing),
		"repeat equals":    with("--plan=" + missing),
		"empty value":      replace("--repo", ""),
		"nul value":        replace("--plan", "a\x00b"),
		"missing value":    append([]string{}, valid[:len(valid)-1]...),
		"no candidate":     drop("--candidate"),
		"no plan":          drop("--plan"),
		"no repo":          drop("--repo"),
		"no state":         drop("--state-dir"),
		"no run id":        drop("--run-id"),
		"upper candidate":  replace("--candidate", strings.Repeat("B", 40)),
		"abbrev candidate": replace("--candidate", oid[:12]),
		"41 candidate":     replace("--candidate", oid+"b"),
		"63 candidate":     replace("--candidate", strings.Repeat("b", 63)),
		"upper id":         replace("--run-id", strings.Repeat("A", 32)),
		"short id":         replace("--run-id", "abc"),
		"single dash":      {"run", "review", "-repo", missing},
		"candidate=":       replace("--candidate", "HEAD"),
	}
	for name, args := range cases {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 2 || out.Len() != 0 || errb.String() != "baw: invalid_usage\n" {
			t.Errorf("%s: %d %q %q", name, code, out.String(), errb.String())
		}
	}
	if _, err := os.Lstat(missing); err == nil {
		t.Fatal("syntax created a path")
	}
	// Valid syntax with both option forms reaches the state check.
	for _, args := range [][]string{valid, {"run", "review", "--repo=" + missing, "--state-dir=" + missing, "--run-id=" + id,
		"--candidate=" + strings.Repeat("c", 64), "--plan=" + missing, "--json"}} {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != 1 || out.Len() != 0 || errb.String() != "baw: state_unavailable\n" {
			t.Errorf("valid syntax: %d %q", code, errb.String())
		}
	}
}

// The global help gains exactly the review line and paragraph; every other
// byte and alias is the frozen base text.
func TestReviewGlobalUsageDelta(t *testing.T) {
	line := "  baw run review --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]\n"
	para := "\nRun review records one trusted reviewer-program verdict for a committed candidate;\n" +
		"it evaluates no approval and supplies no native agent adapter, verification or merge authority.\n"
	if strings.Count(Usage, line) != 1 || !strings.HasSuffix(Usage, para) ||
		!strings.Contains(Usage, "[--json]\n"+line+"  baw --help\n") {
		t.Fatal("review delta missing")
	}
	base := strings.Replace(strings.TrimSuffix(Usage, para), line, "", 1)
	if !strings.HasSuffix(base, "provides no native adapter and never retries or recovers an interrupted attempt.\n") ||
		strings.Contains(base, "review --repo") {
		t.Fatal("base text changed")
	}
}

// Review installs SIGINT/SIGTERM handling only for its own call and restores
// the prior (default) behavior on return. Each scenario runs in a child test
// binary with a 10s deadline and WaitDelay and is always joined; the "held"
// control shows the oracle distinguishes a leaked handler.
func TestReviewSignalRestoration(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, sig := range []string{"INT", "TERM"} {
		want := map[string]syscall.Signal{"INT": syscall.SIGINT, "TERM": syscall.SIGTERM}[sig]
		for _, mode := range []string{"restored", "held"} {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, self, "-baw-review-signal-child", mode, sig, t.TempDir())
			cmd.WaitDelay = 2 * time.Second
			var out bytes.Buffer
			cmd.Stdout = &out
			err := cmd.Start()
			if err == nil {
				err = cmd.Wait()
			}
			deadline := ctx.Err()
			cancel()
			if deadline != nil {
				t.Fatalf("%s %s: child exceeded its deadline", mode, sig)
			}
			ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
			switch mode {
			case "restored":
				if out.String() != "1" || !ws.Signaled() || ws.Signal() != want {
					t.Errorf("%s %s: review code %q, status %v (%v); want termination by the default %v disposition",
						mode, sig, out.String(), ws, err, want)
				}
			case "held":
				if ws.Signaled() || ws.ExitStatus() != 3 {
					t.Errorf("%s %s: control not absorbed: %v", mode, sig, ws)
				}
			}
		}
	}
}
