package cli

import (
	"bytes"
	"context"
	"encoding/json"
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

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// A child of this test binary started as "-baw-verify-signal-child MODE SIG
// DIR" runs one verify-only signal scenario instead of tests (dispatched from
// init so the existing TestMain stays unchanged).
func init() {
	if len(os.Args) == 5 && os.Args[1] == "-baw-verify-signal-child" {
		os.Exit(verifySignalChild(os.Args[2], os.Args[3], os.Args[4]))
	}
	if len(os.Args) == 3 && os.Args[1] == "-baw-verify-run-child" {
		os.Exit(verifyRunChild(os.Args[2]))
	}
}

func verifySignalChild(mode, sig, dir string) int {
	s := map[string]syscall.Signal{"INT": syscall.SIGINT, "TERM": syscall.SIGTERM}[sig]
	if s == 0 {
		return 60
	}
	switch mode {
	case "restored":
		missing := filepath.Join(dir, "missing")
		code := runVerify([]string{"run", "verify", "--repo", missing, "--state-dir", missing, "--run-id",
			strings.Repeat("a", 32), "--candidate", strings.Repeat("b", 40), "--plan", missing}, io.Discard, io.Discard)
		os.Stdout.WriteString(strconv.Itoa(code))
	case "held":
		signal.NotifyContext(context.Background(), s)
	default:
		return 61
	}
	syscall.Kill(os.Getpid(), s)
	time.Sleep(3 * time.Second)
	return 3
}

// verifyRunChild runs Run with the JSON argv in file and writes the exit
// code, stdout and stderr as JSON.
func verifyRunChild(file string) int {
	b, err := os.ReadFile(file)
	var args []string
	if err != nil || json.Unmarshal(b, &args) != nil {
		return 62
	}
	var out, errb bytes.Buffer
	code := Run(args, &out, &errb)
	r, _ := json.Marshal(map[string]any{"code": code, "stdout": out.String(), "stderr": errb.String()})
	os.Stdout.Write(r)
	return 0
}

// wantVerifyHelp is the frozen C1 command help, written out by hand.
const wantVerifyHelp = "Usage:\n" +
	"  baw run verify --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]\n" +
	"\n" +
	"Run one trusted local verifier against an explicit clean committed candidate and record its observed result.\n" +
	"Relative --repo, --state-dir and --plan paths resolve from the caller's working directory. Verifier cwd and relative verifier arguments use the physical Git top level.\n" +
	"This records one verifier-program observation, not complete gate evidence, a source freeze, approval or merge authority. No native agent adapter is used.\n"

type verifyFailWriter struct{}

func (verifyFailWriter) Write(b []byte) (int, error) { return 0, os.ErrClosed }

func TestVerifyHelpAndSyntax(t *testing.T) {
	if r := runCLI(t, nil, "run", "verify", "--help"); r.code != 0 || r.stdout != wantVerifyHelp || r.stderr != "" {
		t.Errorf("help: %+v", r)
	}
	var errb bytes.Buffer
	if code := Run([]string{"run", "verify", "--help"}, verifyFailWriter{}, &errb); code != 1 || errb.String() != "baw: output_unavailable\n" {
		t.Errorf("help sink: %d %q", code, errb.String())
	}
	id, c40 := strings.Repeat("a", 32), strings.Repeat("b", 40)
	valid := []string{"--repo", "r", "--state-dir", "s", "--run-id", id, "--candidate", c40, "--plan", "p"}
	bad := [][]string{
		{"-h"}, {"help"}, {"--help", "--json"},
		{},
		append(append([]string{}, valid...), "--json=1"),
		append(append([]string{}, valid...), "--json", "--json"),
		append(append([]string{}, valid...), "--repo", "r"),
		append(append([]string{}, valid...), "extra"),
		append(append([]string{}, valid...), "--unknown", "x"),
		{"--repo", "", "--state-dir", "s", "--run-id", id, "--candidate", c40, "--plan", "p"},
		{"--repo", "r\x00", "--state-dir", "s", "--run-id", id, "--candidate", c40, "--plan", "p"},
		{"--repo", "r", "--state-dir", "s", "--run-id", strings.ToUpper(id), "--candidate", c40, "--plan", "p"},
		{"--repo", "r", "--state-dir", "s", "--run-id", id, "--candidate", strings.Repeat("b", 41), "--plan", "p"},
		{"--repo", "r", "--state-dir", "s", "--run-id", id, "--candidate", strings.ToUpper(c40), "--plan", "p"},
		{"--state-dir", "s", "--run-id", id, "--candidate", c40, "--plan", "p"},
		{"--repo", "r", "--state-dir", "s", "--run-id", id, "--candidate", c40},
		{"--repo", "r", "--state-dir", "s", "--run-id", id, "--candidate", c40, "--plan"},
	}
	for _, a := range bad {
		r := runCLI(t, nil, append([]string{"run", "verify"}, a...)...)
		if r.code != 2 || r.stdout != "" || r.stderr != "baw: invalid_usage\n" {
			t.Errorf("%q: %+v", a, r)
		}
	}
	// Valid syntax (separated and equals forms) reaches the state root.
	missing := filepath.Join(t.TempDir(), "missing")
	for _, a := range [][]string{
		{"--repo", missing, "--state-dir", missing, "--run-id", id, "--candidate", c40, "--plan", missing},
		{"--repo=" + missing, "--state-dir=" + missing, "--run-id=" + id, "--candidate=" + strings.Repeat("b", 64), "--plan=" + missing, "--json"},
	} {
		r := runCLI(t, nil, append([]string{"run", "verify"}, a...)...)
		if r.code != 1 || r.stdout != "" || r.stderr != "baw: state_unavailable\n" {
			t.Errorf("valid syntax %q: %+v", a, r)
		}
	}
}

// listTree lists every path below dir with mode and size.
func listTree(t *testing.T, dir string) string {
	var b strings.Builder
	filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err == nil {
			b.WriteString(p + " " + fi.Mode().String() + " " + strconv.FormatInt(fi.Size(), 10) + "\n")
		}
		return nil
	})
	return b.String()
}

type verifyEnv struct {
	base, home, state, counter string
	repos, heads               map[string]string
}

func newVerifyEnv(t *testing.T) *verifyEnv {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := &verifyEnv{base: base, home: filepath.Join(base, "home"), state: filepath.Join(base, "state"),
		counter: filepath.Join(base, "counter"), repos: map[string]string{}, heads: map[string]string{}}
	for _, d := range []string{e.home, e.state, e.counter} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
		os.Chmod(d, 0o700)
	}
	for _, format := range []string{"sha1", "sha256"} {
		repo := filepath.Join(base, "repo-"+format)
		if err := tf.Init(e.home, repo, format); err != nil {
			t.Fatal(err)
		}
		head, err := tf.CommitSources(e.home, repo)
		if err != nil {
			t.Fatal(err)
		}
		e.repos[format], e.heads[format] = repo, head
	}
	return e
}

func (e *verifyEnv) plan(t *testing.T, name, body string) string {
	p := filepath.Join(e.base, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chmod(p, 0o600)
	return p
}

// A valid SHA-1 record used against a real SHA-256 repository, and the
// reverse, give the unchanged inspector checkpoint-width invalid_usage: exit
// 2, exact stderr, empty stdout, no verify-v1 namespace and zero starts.
func TestVerifyCrossFormatRecords(t *testing.T) {
	e := newVerifyEnv(t)
	marker := filepath.Join(e.counter, "started")
	plan := e.plan(t, "plan.json", `{"schema_version":1,"verifier":{"executable":"/usr/bin/touch","arguments":["`+marker+`"],"timeout_seconds":5}}`)
	ids := map[string]string{"sha1": strings.Repeat("1", 32), "sha256": strings.Repeat("2", 32)}
	for format, id := range ids {
		r := runCLI(t, nil, "run", "create", "--state-dir", e.state, "--run-id", id, "--repo", e.repos[format],
			"--ticket", "https://github.com/example/project/issues/24", "--scope-sha256", strings.Repeat("d", 64),
			"--policy-commit", e.heads[format])
		if r.code != 0 {
			t.Fatalf("create %s: %+v", format, r)
		}
	}
	for recFormat, repoFormat := range map[string]string{"sha1": "sha256", "sha256": "sha1"} {
		before := listTree(t, e.state)
		r := runCLI(t, nil, "run", "verify", "--repo", e.repos[repoFormat], "--state-dir", e.state, "--run-id", ids[recFormat],
			"--candidate", e.heads[repoFormat], "--plan", plan)
		if r.code != 2 || r.stdout != "" || r.stderr != "baw: invalid_usage\n" {
			t.Errorf("%s record on %s repo: %+v", recFormat, repoFormat, r)
		}
		if after := listTree(t, e.state); after != before {
			t.Errorf("%s record: state root changed", recFormat)
		}
		if _, err := os.Lstat(filepath.Join(e.state, "verify-v1")); err == nil {
			t.Error("verify-v1 created")
		}
		if _, err := os.Lstat(marker); err == nil {
			t.Error("verifier started")
		}
	}
	// Positive control: the same record and plan against its own repository passes.
	r := runCLI(t, nil, "run", "verify", "--repo", e.repos["sha256"], "--state-dir", e.state, "--run-id", ids["sha256"],
		"--candidate", e.heads["sha256"], "--plan", plan)
	if r.code != 0 || r.stderr != "" || !strings.Contains(r.stdout, "Outcome: candidate_verification_passed\n") {
		t.Errorf("positive control: %+v", r)
	}
	if _, err := os.Lstat(marker); err != nil {
		t.Error("verifier did not start in the positive control")
	}
	// Subdirectory and symlinked-ancestor repository arguments are normalized.
	link := filepath.Join(e.base, "alias")
	os.Symlink(e.base, link)
	r2 := runCLI(t, nil, "run", "verify", "--repo", filepath.Join(link, "repo-sha1", "workflow"), "--state-dir", e.state,
		"--run-id", ids["sha1"], "--candidate", e.heads["sha1"], "--plan", plan, "--json")
	if r2.code != 0 || !strings.Contains(r2.stdout, `"outcome":"candidate_verification_passed"`) {
		t.Errorf("symlinked subdir: %+v", r2)
	}
}

// Verify installs SIGINT/SIGTERM handling only for its own call and restores
// the prior behavior on return; each child is bounded and joined.
func TestVerifySignalRestoration(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, sig := range []string{"INT", "TERM"} {
		want := map[string]syscall.Signal{"INT": syscall.SIGINT, "TERM": syscall.SIGTERM}[sig]
		for _, mode := range []string{"restored", "held"} {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, self, "-baw-verify-signal-child", mode, sig, t.TempDir())
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
					t.Errorf("%s %s: code %q status %v (%v)", mode, sig, out.String(), ws, err)
				}
			case "held":
				if ws.Signaled() || ws.ExitStatus() != 3 {
					t.Errorf("%s %s: control not absorbed: %v", mode, sig, ws)
				}
			}
		}
	}
}

// Real SIGINT/SIGTERM delivered to a child running `baw run verify` once its
// durable intent is visible yields a recorded verification_unverified result
// (exit 1, baw: verification_failed); real SIGKILL leaves the intent retained
// with no result. Children are bounded and joined.
func TestVerifyRealSignalsDuringAttempt(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for i, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL} {
		e := newVerifyEnv(t)
		id := strings.Repeat(strconv.Itoa(i+3), 32)
		if r := runCLI(t, nil, "run", "create", "--state-dir", e.state, "--run-id", id, "--repo", e.repos["sha1"],
			"--ticket", "https://github.com/example/project/issues/24", "--scope-sha256", strings.Repeat("d", 64),
			"--policy-commit", e.heads["sha1"]); r.code != 0 {
			t.Fatal(r)
		}
		plan := e.plan(t, "plan.json", `{"schema_version":1,"verifier":{"executable":"/bin/sleep","arguments":["3"],"timeout_seconds":20}}`)
		argv, _ := json.Marshal([]string{"run", "verify", "--repo", e.repos["sha1"], "--state-dir", e.state, "--run-id", id,
			"--candidate", e.heads["sha1"], "--plan", plan, "--json"})
		argFile := filepath.Join(e.base, "argv.json")
		os.WriteFile(argFile, argv, 0o600)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, self, "-baw-verify-run-child", argFile)
		cmd.WaitDelay = 2 * time.Second
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		attempt := filepath.Join(e.state, "verify-v1", id)
		for j := 0; j < 400; j++ {
			if _, err := os.Lstat(filepath.Join(attempt, "intent.json")); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		cmd.Process.Signal(sig)
		cmd.Wait()
		deadline := ctx.Err()
		cancel()
		if deadline != nil {
			t.Fatalf("%v: child exceeded its deadline", sig)
		}
		_, rerr := os.Lstat(filepath.Join(attempt, "result.json"))
		if sig == syscall.SIGKILL {
			if rerr == nil || out.Len() != 0 {
				t.Errorf("SIGKILL: result present or output %q", out.String())
			}
			if _, err := os.Lstat(filepath.Join(attempt, "intent.json")); err != nil {
				t.Error("SIGKILL: intent not retained")
			}
			continue
		}
		var got struct {
			Code   int    `json:"code"`
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		if err := json.Unmarshal(out.Bytes(), &got); err != nil {
			t.Fatalf("%v: child output %q", sig, out.String())
		}
		if got.Code != 1 || got.Stderr != "baw: verification_failed\n" ||
			!strings.Contains(got.Stdout, `"outcome":"verification_unverified","verification":{"state":"unverified","exit_code":null}`) {
			t.Errorf("%v: %+v", sig, got)
		}
		saved, _ := os.ReadFile(filepath.Join(attempt, "result.json"))
		if rerr != nil || string(saved) != got.Stdout {
			t.Errorf("%v: saved result differs from output", sig)
		}
	}
}
