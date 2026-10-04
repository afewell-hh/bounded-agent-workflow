package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Execution binary journeys use the same flags as TestBinaryJourneys:
//
//	go test ./cmd/baw -run '^TestExecutionBinaryJourneys$' -count=1 -v -args -baw-binary=PATH -journey-dir=DIR
//
// The fake worker and verifier are this compiled test binary, copied into DIR
// and selected by its first argument; no shell, PATH lookup, provider or
// network is involved. Every expected packet is written by hand.
func TestMain(m *testing.M) {
	if len(os.Args) > 3 && os.Args[1] == "-baw-fake" {
		os.Exit(journeyFake(os.Args[2], os.Args[3]))
	}
	os.Exit(m.Run())
}

const journeyWorkerBytes = "dummy change written by the fake worker\n"

// journeyFake records each start in COUNTER/ROLE, then acts by mode. The
// working directory is the Git top level chosen by baw.
func journeyFake(mode, counter string) int {
	role := strings.SplitN(mode, "-", 2)[0]
	f, err := os.OpenFile(filepath.Join(counter, role), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 90
	}
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	f.Close()
	env := os.Environ()
	home, tmp := os.Getenv("HOME"), os.Getenv("TMPDIR")
	if len(env) != 4 || os.Getenv("LANG") != "C" || os.Getenv("LC_ALL") != "C" ||
		!strings.HasSuffix(home, "/"+role+"/home") || !strings.HasSuffix(tmp, "/"+role+"/tmp") {
		return 97
	}
	switch mode {
	case "worker-ok":
		os.WriteFile(filepath.Join(home, "marker"), []byte("w"), 0o600)
		os.WriteFile(filepath.Join(tmp, "marker"), []byte("w"), 0o600)
		os.Stdout.WriteString("SECRET-JOURNEY-OUTPUT")
		if os.WriteFile("dummy.txt", []byte(journeyWorkerBytes), 0o644) != nil {
			return 96
		}
		return 0
	case "worker-fail":
		return 4
	case "worker-sleep":
		os.WriteFile(filepath.Join(counter, "sleeper.pid.tmp"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		os.Rename(filepath.Join(counter, "sleeper.pid.tmp"), filepath.Join(counter, "sleeper.pid"))
		time.Sleep(60 * time.Second)
		return 0
	case "verifier-check":
		for _, d := range []string{home, tmp} {
			if es, err := os.ReadDir(d); err != nil || len(es) != 0 {
				return 98
			}
		}
		if b, err := os.ReadFile("dummy.txt"); err != nil || string(b) != journeyWorkerBytes {
			return 1
		}
		return 0
	}
	return 99
}

func TestExecutionBinaryJourneys(t *testing.T) {
	if *bawBinary == "" || *journeyDir == "" {
		t.Skip("binary journeys need -baw-binary and -journey-dir")
	}
	if err := os.Mkdir(*journeyDir, 0o700); err != nil {
		t.Fatalf("journey dir must be new: %v", err)
	}
	jd, err := filepath.EvalSymlinks(*journeyDir)
	if err != nil {
		t.Fatal(err)
	}
	var summary strings.Builder
	defer func() { os.WriteFile(filepath.Join(jd, "summary.txt"), []byte(summary.String()), 0o600) }()
	summary.WriteString("binary sha256: " + fileSHA(t, *bawBinary) + "\n")
	home := filepath.Join(jd, "home")
	os.Mkdir(home, 0o700)

	// The fake tool: a private copy of this compiled test binary.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(jd, "fake-tool")
	b, err := os.ReadFile(self)
	if err != nil || os.WriteFile(tool, b, 0o700) != nil {
		t.Fatal("copy fake tool")
	}
	os.Chmod(tool, 0o700)

	run := func(name string, wantCode int, stdout io.Writer, args ...string) (string, string) {
		cmd := exec.Command(*bawBinary, args...)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		if stdout != nil {
			cmd.Stdout = stdout
		}
		start := time.Now()
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		summary.WriteString("== " + name + " exit=" + itoa(code) + " elapsed_ms=" + itoa(int(time.Since(start).Milliseconds())) +
			"\n-- argv: baw " + strings.Join(args, " ") + "\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
		if code != wantCode {
			t.Errorf("%s: exit %d want %d", name, code, wantCode)
		}
		return out.String(), errb.String()
	}
	starts := func(counter, role string) int {
		b, _ := os.ReadFile(filepath.Join(counter, role))
		return strings.Count(string(b), "\n")
	}
	text := func(id, outcome, ws, wc, vs, vc, format, before, after string) string {
		return "BAW execution observations\nRun: " + id + "\nAuthority: not_evaluated\nReadiness: not_evaluated\nOutcome: " + outcome +
			"\nWorker: state=" + ws + " exit_code=" + wc + "\nVerification: state=" + vs + " exit_code=" + vc +
			"\nRepository: object_format=" + format + " before_head=" + before + " after_head=" + after + "\nReceipt: recorded\n"
	}

	// Help and usage, before any filesystem work.
	wantHelp := "Usage:\n  baw run execute --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]\n\n" +
		"Runs one trusted local worker and then one verification program in the Git worktree.\n" +
		"The existing run record and nonsecret plan do not prove approval or authority.\n" +
		"One attempt per run ID; interrupted or uncertain attempts are never retried automatically.\n" +
		"Native agent adapters, review, acceptance, merge and recovery are not provided.\n"
	if out, errs := run("execute help", 0, nil, "run", "execute", "--help"); out != wantHelp || errs != "" {
		t.Errorf("execute help %q", out)
	}
	for _, a := range [][]string{{"run", "execute", "-h"}, {"run", "execute", "--help", "--json"}, {"run", "execute"},
		{"run", "execute", "--repo", "x", "--state-dir", "y", "--run-id", "ABC", "--plan", "z"}} {
		if out, errs := run("usage "+strings.Join(a, " "), 2, nil, a...); out != "" || errs != "baw: invalid_usage\n" {
			t.Errorf("usage %v: %q %q", a, out, errs)
		}
	}

	ids := map[string]string{"sha1": "11111111111111111111111111111111", "sha256": "22222222222222222222222222222222"}
	n := 0
	setup := func(format, worker, verifier string) (repo, root, plan, counter, head string) {
		n++
		base := filepath.Join(jd, format+"-"+itoa(n))
		repo, root, counter = filepath.Join(base, "project"), filepath.Join(base, "state"), filepath.Join(base, "count")
		os.MkdirAll(counter, 0o700)
		if err := tf.Init(home, repo, format); err != nil {
			t.Fatalf("%s fixture mandatory: %v", format, err)
		}
		tf.Write(filepath.Join(repo, "dummy.txt"), "original\n")
		head, err := tf.CommitSources(home, repo)
		if err != nil {
			t.Fatal(err)
		}
		os.Mkdir(root, 0o700)
		os.Chmod(root, 0o700)
		policy := strings.Repeat("5", len(head))
		run(format+" run create", 0, nil, "run", "create", "--state-dir", root, "--run-id", ids[format], "--repo", repo,
			"--ticket", journeyTicket, "--scope-sha256", journeyScope, "--policy-commit", policy)
		plan = filepath.Join(base, "plan.json")
		pj, _ := json.Marshal(map[string]any{"schema_version": 1,
			"worker":       map[string]any{"executable": tool, "arguments": []string{"-baw-fake", worker, counter}, "timeout_seconds": 60},
			"verification": map[string]any{"executable": tool, "arguments": []string{"-baw-fake", verifier, counter}, "timeout_seconds": 60}})
		os.WriteFile(plan, pj, 0o600)
		os.Chmod(plan, 0o600)
		return
	}
	execArgs := func(repo, root, id, plan string, extra ...string) []string {
		return append([]string{"run", "execute", "--repo", repo, "--state-dir", root, "--run-id", id, "--plan", plan}, extra...)
	}

	for _, format := range []string{"sha1", "sha256"} {
		id := ids[format]
		for _, asJSON := range []bool{false, true} {
			repo, root, plan, counter, head := setup(format, "worker-ok", "verifier-check")
			var extra []string
			if asJSON {
				extra = []string{"--json"}
			}
			out, errs := run(format+" passed json="+strconv.FormatBool(asJSON), 0, nil, execArgs(filepath.Join(repo, "docs"), root, id, plan, extra...)...)
			if errs != "" || starts(counter, "worker") != 1 || starts(counter, "verifier") != 1 {
				t.Errorf("%s passed: %q starts %d %d", format, errs, starts(counter, "worker"), starts(counter, "verifier"))
			}
			if b, _ := os.ReadFile(filepath.Join(repo, "dummy.txt")); string(b) != journeyWorkerBytes {
				t.Errorf("dummy %q", b)
			}
			if h, _ := tf.OID(home, repo, "HEAD"); h != head {
				t.Error("HEAD changed")
			}
			attempt := filepath.Join(root, "execute-v1", id)
			res, _ := os.ReadFile(filepath.Join(attempt, "result.json"))
			if asJSON {
				var m map[string]any
				d := json.NewDecoder(strings.NewReader(out))
				d.UseNumber()
				if err := d.Decode(&m); err != nil || out != string(res) {
					t.Fatalf("json %q", out)
				}
				delete(m, "created_at")
				delete(m, "completed_at")
				got, _ := json.Marshal(m)
				want := `{"authority":"not_evaluated","operation":"run_execute","outcome":"verification_passed","readiness":"not_evaluated","receipt_state":"recorded","repository":{"after_head":"` +
					head + `","before_head":"` + head + `","object_format":"` + format + `"},"run_id":"` + id + `","schema_version":1,"verification":{"exit_code":0,"state":"exited"},"worker":{"exit_code":0,"state":"exited"}}`
				if string(got) != want {
					t.Errorf("json\n got %s\nwant %s", got, want)
				}
			} else if out != text(id, "verification_passed", "exited", "0", "exited", "0", format, head, head) {
				t.Errorf("text %q", out)
			}
			if strings.Contains(out, "SECRET") || strings.Contains(string(res), "SECRET") {
				t.Error("raw output leaked")
			}
			for _, d := range []string{"", "worker", "worker/home", "worker/tmp", "verifier", "verifier/home", "verifier/tmp"} {
				if fi, err := os.Lstat(filepath.Join(attempt, d)); err != nil || fi.Mode().Perm() != 0o700 {
					t.Errorf("mode %s", d)
				}
			}
			var names []string
			es, _ := os.ReadDir(attempt)
			for _, e := range es {
				names = append(names, e.Name())
			}
			sort.Strings(names)
			summary.WriteString("attempt listing: " + strings.Join(names, " ") + "\n")
			out, errs = run(format+" repeat", 1, nil, execArgs(repo, root, id, plan, extra...)...)
			if out != "" || errs != "baw: execution_exists\n" || starts(counter, "worker") != 1 {
				t.Errorf("repeat %q %q", out, errs)
			}
		}

		// Nonzero worker: recorded, exit 1, verifier never admitted.
		repo, root, plan, counter, head := setup(format, "worker-fail", "verifier-check")
		out, errs := run(format+" worker failed", 1, nil, execArgs(repo, root, id, plan)...)
		if out != text(id, "worker_failed", "exited", "4", "not_started", "unknown", format, head, "unknown") ||
			errs != "baw: execution_failed\n" || starts(counter, "verifier") != 0 {
			t.Errorf("worker failed %q %q", out, errs)
		}

		// Delivery failure after publication: uncertain, record retained.
		repo, root, plan, counter, _ = setup(format, "worker-ok", "verifier-check")
		ro, _ := os.Open(os.DevNull)
		out, errs = run(format+" delivery failure", 1, ro, execArgs(repo, root, id, plan, "--json")...)
		ro.Close()
		if errs != "baw: execution_uncertain\n" || starts(counter, "verifier") != 1 {
			t.Errorf("delivery failure %q", errs)
		}
		if _, err := os.Lstat(filepath.Join(root, "execute-v1", id, "result.json")); err != nil {
			t.Error("result not retained")
		}
	}

	// Real SIGINT and SIGTERM while the worker runs. The harness owns the
	// fake's recorded PID and independently checks it is gone afterwards.
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM} {
		repo, root, plan, counter, head := setup("sha1", "worker-sleep", "verifier-check")
		cmd := exec.Command(*bawBinary, execArgs(repo, root, ids["sha1"], plan)...)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		pidFile := filepath.Join(counter, "sleeper.pid")
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(pidFile); err == nil {
				break
			}
		}
		cmd.Process.Signal(sig)
		err := cmd.Wait()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		summary.WriteString("== signal " + sig.String() + " exit=" + itoa(code) + "\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
		if code != 1 || out.String() != text(ids["sha1"], "worker_unverified", "unverified", "unknown", "not_started", "unknown", "sha1", head, "unknown") ||
			errb.String() != "baw: execution_failed\n" || starts(counter, "verifier") != 0 {
			t.Errorf("signal %v: %d %q %q", sig, code, out.String(), errb.String())
		}
		if !processGone(t, pidFile) {
			t.Errorf("signal %v: owned fake worker survived", sig)
		}
	}

	// Documentation example (docs/operator/execution.md) against the binary.
	docExample(t, jd, tool, run)
}

// docExample follows the documented steps with the fake tool standing in for
// the documented locally compiled dummy programs.
func docExample(t *testing.T, jd, tool string, run func(string, int, io.Writer, ...string) (string, string)) {
	home := filepath.Join(jd, "doc-home")
	os.Mkdir(home, 0o700)
	work := filepath.Join(jd, "doc")
	repo, state := filepath.Join(work, "project"), filepath.Join(work, "state")
	if err := tf.Init(home, repo, "sha1"); err != nil {
		t.Fatal(err)
	}
	tf.Write(filepath.Join(repo, "dummy.txt"), "original\n")
	head, err := tf.CommitSources(home, repo)
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(state, 0o700)
	os.Chmod(state, 0o700)
	id := "0123456789abcdef0123456789abcdef"
	counter := filepath.Join(work, "count")
	os.Mkdir(counter, 0o700)
	run("doc run create", 0, nil, "run", "create", "--state-dir", state, "--run-id", id, "--repo", repo,
		"--ticket", "https://github.com/OWNER/REPO/issues/1", "--scope-sha256", strings.Repeat("0", 64), "--policy-commit", head)
	plan := filepath.Join(work, "plan.json")
	pj := `{"schema_version":1,"worker":{"executable":"` + tool + `","arguments":["-baw-fake","worker-ok","` + counter +
		`"],"timeout_seconds":60},"verification":{"executable":"` + tool + `","arguments":["-baw-fake","verifier-check","` + counter + `"],"timeout_seconds":60}}`
	os.WriteFile(plan, []byte(pj), 0o600)
	os.Chmod(plan, 0o600)
	out, _ := run("doc execute", 0, nil, "run", "execute", "--repo", repo, "--state-dir", state, "--run-id", id, "--plan", plan)
	if !strings.Contains(out, "Outcome: verification_passed\n") {
		t.Errorf("doc example %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "dummy.txt")); string(b) != journeyWorkerBytes {
		t.Error("doc dummy file")
	}
	if fi, err := os.Lstat(filepath.Join(state, "execute-v1", id, "result.json")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Error("doc result mode")
	}
	out, errs := run("doc repeat", 1, nil, "run", "execute", "--repo", repo, "--state-dir", state, "--run-id", id, "--plan", plan)
	if out != "" || errs != "baw: execution_exists\n" {
		t.Error("doc repeat")
	}
}
