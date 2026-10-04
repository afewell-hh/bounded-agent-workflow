package review

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Real SHA-1 and SHA-256 repositories; both must run, never skip.
func TestReviewPositiveBothFormats(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, c := range []struct{ mode, outcome, verdict string }{
			{"pass", "review_passed", "PASS"}, {"fixes", "review_required_fixes", "REQUIRED_FIXES"},
		} {
			for _, asJSON := range []bool{true, false} {
				f := newFx(t, format, c.mode)
				out, passed, err := f.run(context.Background(), f.req(f.head, asJSON))
				if err != nil {
					t.Fatalf("%s %s: %v", format, c.mode, err)
				}
				if passed != (c.outcome == "review_passed") {
					t.Errorf("%s %s: passed %v", format, c.mode, passed)
				}
				wantJ := wantResultJSON(c.outcome, "exited", "0", c.verdict, format, f.head, f.head)
				want := wantJ
				if !asJSON {
					want = wantText(c.outcome, "exited", "0", c.verdict, format, f.head, f.head)
				}
				if out != want {
					t.Errorf("%s %s json=%v:\n got %q\nwant %q", format, c.mode, asJSON, out, want)
				}
				if got := string(readFile(t, filepath.Join(f.attempt(), "result.json"))); got != wantJ {
					t.Errorf("result.json %q", got)
				}
				if f.starts() != 1 {
					t.Errorf("starts %d", f.starts())
				}
				checkIntent(t, f, f.head)
				checkLayout(t, f, true)
			}
		}
	}
}

// checkIntent compares intent.json with hand-built bytes whose hashes are
// computed independently from the fixture files.
func checkIntent(t *testing.T, f *fx, candidate string) {
	t.Helper()
	want := `{"schema_version":1,"run_id":"` + testID + `","record_state":"review_intent","ticket_url":"` + testTicket +
		`","scope_sha256":"` + testScope + `","policy_commit":"` + f.head + `","repository_object_format":"` + f.format +
		`","repository_head":"` + f.head + `","candidate_head":"` + candidate + `","plan_sha256":"` +
		sha(readFile(t, f.plan)) + `","execution_intent_sha256":"` + sha(readFile(t, filepath.Join(f.execDir(), "intent.json"))) +
		`","execution_result_sha256":"` + sha(readFile(t, filepath.Join(f.execDir(), "result.json"))) +
		`","created_at":"` + fixedNow + `","authority":"not_evaluated"}` + "\n"
	if got := string(readFile(t, filepath.Join(f.attempt(), "intent.json"))); got != want {
		t.Errorf("intent:\n got %s\nwant %s", got, want)
	}
	var m map[string]any
	json.Unmarshal(readFile(t, filepath.Join(f.attempt(), "intent.json")), &m)
	if len(m) != 14 {
		t.Errorf("intent keys %d", len(m))
	}
}

// checkLayout verifies retained staging (link count 2), modes and the
// reviewer scratch layout.
func checkLayout(t *testing.T, f *fx, withResult bool) {
	t.Helper()
	es, err := os.ReadDir(f.attempt())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range es {
		n := e.Name()
		if strings.HasPrefix(n, ".pending-intent-") && len(n) == len(".pending-intent-")+32 {
			n = ".pending-intent-X"
		}
		if strings.HasPrefix(n, ".pending-result-") && len(n) == len(".pending-result-")+32 {
			n = ".pending-result-X"
		}
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{".pending-intent-X", "intent.json", "reviewer"}
	if withResult {
		want = []string{".pending-intent-X", ".pending-result-X", "intent.json", "result.json", "reviewer"}
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("attempt entries %v want %v", names, want)
	}
	for _, n := range []string{"intent.json", "result.json"} {
		if n == "result.json" && !withResult {
			continue
		}
		fi, err := os.Lstat(filepath.Join(f.attempt(), n))
		if err != nil || fi.Mode().Perm() != 0o600 || fi.Sys().(*syscall.Stat_t).Nlink != 2 {
			t.Errorf("%s: %v %v", n, err, fi)
		}
	}
	for _, d := range []string{f.attempt(), filepath.Join(f.attempt(), "reviewer"), filepath.Join(f.attempt(), "reviewer", "home"),
		filepath.Join(f.attempt(), "reviewer", "tmp"), filepath.Join(f.state, Namespace)} {
		if fi, err := os.Lstat(d); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode %v", d, err)
		}
	}
}

// The coordinator may commit the worker's previously dirty work: a later
// committed descendant of after_head is accepted and recorded as candidate.
func TestReviewLaterDescendantCandidate(t *testing.T) {
	f := newFx(t, "sha1", "pass")
	cand := f.commitDescendant()
	if cand == f.head {
		t.Fatal("descendant equals head")
	}
	out, passed, err := f.run(context.Background(), f.req(cand, true))
	if err != nil || !passed || out != wantResultJSON("review_passed", "exited", "0", "PASS", "sha1", cand, cand) {
		t.Fatalf("%v %v %s", err, passed, out)
	}
	checkIntent(t, f, cand)
}

func TestReviewCandidateRejections(t *testing.T) {
	t.Run("head mismatch", func(t *testing.T) {
		f := newFx(t, "sha1", "pass")
		f.commitDescendant()
		f.assertPre(t, "mismatch", f.req(f.head, false), nil, "review_checkpoint_mismatch")
	})
	t.Run("wrong width", func(t *testing.T) {
		f := newFx(t, "sha1", "pass")
		f.assertPre(t, "width", f.req(strings.Repeat("a", 64), false), nil, "invalid_usage")
		g := newFx(t, "sha256", "pass")
		g.assertPre(t, "width256", g.req(g.head[:40], false), nil, "invalid_usage")
	})
	t.Run("diverged", func(t *testing.T) {
		f := newFx(t, "sha1", "pass")
		f.git("checkout", "-q", "--orphan", "other")
		f.git("commit", "-q", "-m", "unrelated root")
		cand := f.git("rev-parse", "HEAD")
		f.assertPre(t, "diverged", f.req(cand, false), nil, "checkpoint_diverged")
	})
	t.Run("missing checkpoint", func(t *testing.T) {
		f := newFx(t, "sha1", "pass")
		missing := strings.Repeat("1", 40)
		f.r.after = &missing
		f.writeReceipts()
		f.assertPre(t, "missing", f.req(f.head, false), nil, "checkpoint_missing")
	})
	t.Run("unborn wins over width and cleanliness", func(t *testing.T) {
		f := newFx(t, "sha1", "pass")
		f.git("checkout", "-q", "--orphan", "unborn")
		f.assertPre(t, "unborn", f.req(strings.Repeat("a", 64), false), nil, "checkpoint_unborn")
	})
	for _, dirty := range []string{"unstaged", "staged", "untracked", "conflicted"} {
		t.Run("dirty "+dirty, func(t *testing.T) {
			f := newFx(t, "sha1", "pass")
			cand := f.head
			switch dirty {
			case "unstaged":
				os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("dirty\n"), 0o644)
			case "staged":
				os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("dirty\n"), 0o644)
				f.git("add", "README.md")
			case "untracked":
				os.WriteFile(filepath.Join(f.repo, "untracked.txt"), []byte("x\n"), 0o644)
			case "conflicted":
				f.git("checkout", "-q", "-b", "side")
				os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("side\n"), 0o644)
				f.git("commit", "-q", "-am", "side")
				f.git("checkout", "-q", "-")
				os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("main\n"), 0o644)
				f.git("commit", "-q", "-am", "main")
				cand = f.git("rev-parse", "HEAD")
				tfGitAllowFail(f, "merge", "-q", "side")
			}
			f.assertPre(t, dirty, f.req(cand, false), nil, "candidate_not_clean")
		})
	}
}

func tfGitAllowFail(f *fx, args ...string) {
	cmd := exec.Command("git", append([]string{"-C", f.repo, "-c", "user.name=F", "-c", "user.email=f@example.invalid"}, args...)...)
	cmd.Env = []string{"HOME=" + f.home, "PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	cmd.Run()
}

// Reviewer rows observed through real processes.
func TestReviewReviewerRows(t *testing.T) {
	for _, c := range []struct {
		mode, outcome, state, code, verdict string
		after                               bool
	}{
		{"nonzero-pass", "reviewer_failed", "exited", "3", "null", false},
		{"dup-escaped", "reviewer_unverified", "exited", "0", "null", false},
		{"dup-nested-escaped", "reviewer_unverified", "exited", "0", "null", false},
		{"escaped-control", "reviewer_unverified", "exited", "0", "null", false},
		{"raw-control", "reviewer_unverified", "exited", "0", "null", false},
		{"bad-utf8", "reviewer_unverified", "exited", "0", "null", false},
		{"version-2", "reviewer_unverified", "exited", "0", "null", false},
		{"version-float", "reviewer_unverified", "exited", "0", "null", false},
		{"version-string", "reviewer_unverified", "exited", "0", "null", false},
		{"unknown-key", "reviewer_unverified", "exited", "0", "null", false},
		{"lower-verdict", "reviewer_unverified", "exited", "0", "null", false},
		{"two-objects", "reviewer_unverified", "exited", "0", "null", false},
		{"prose", "reviewer_unverified", "exited", "0", "null", false},
		{"exact-2048", "review_passed", "exited", "0", "PASS", true},
		{"over-2048", "reviewer_unverified", "unverified", "null", "null", false},
		{"stderr-over", "reviewer_unverified", "unverified", "null", "null", false},
		{"stderr-secret", "review_passed", "exited", "0", "PASS", true},
		{"modify", "candidate_changed", "exited", "0", "PASS", false},
		{"untracked", "candidate_changed", "exited", "0", "PASS", false},
		{"stage", "candidate_changed", "exited", "0", "PASS", false},
		{"commit", "candidate_changed", "exited", "0", "PASS", false},
		{"unknown-mode", "reviewer_failed", "exited", "99", "null", false},
	} {
		f := newFx(t, "sha1", c.mode)
		after := "null"
		if c.after {
			after = f.head
		}
		for _, asJSON := range []bool{true} {
			out, passed, err := f.run(context.Background(), f.req(f.head, asJSON))
			want := wantResultJSON(c.outcome, c.state, c.code, c.verdict, "sha1", f.head, after)
			if err != nil || out != want || passed != (c.outcome == "review_passed") {
				t.Errorf("%s: %v %v\n got %q\nwant %q", c.mode, err, passed, out, want)
			}
			if strings.Contains(out, dummySecret) || strings.Contains(string(readFile(t, filepath.Join(f.attempt(), "result.json"))), dummySecret) {
				t.Errorf("%s: secret leaked", c.mode)
			}
			if f.starts() != 1 {
				t.Errorf("%s: starts %d", c.mode, f.starts())
			}
		}
	}
}

// Each false RunObserved usability fact is supplied through the review-local
// runner seam (nil in production); a real child would need proc changes.
func TestReviewObservationFactsSeam(t *testing.T) {
	usable := proc.Observation{Started: true, Exited: true, Joined: true, GroupAbsent: true, StdoutEOF: true, StderrEOF: true}
	cases := map[string]func(o *proc.Observation){
		"not exited":     func(o *proc.Observation) { o.Exited = false },
		"signaled":       func(o *proc.Observation) { o.Signaled = true },
		"not joined":     func(o *proc.Observation) { o.Joined = false },
		"group present":  func(o *proc.Observation) { o.GroupAbsent = false },
		"stdout not eof": func(o *proc.Observation) { o.StdoutEOF = false },
		"stderr not eof": func(o *proc.Observation) { o.StderrEOF = false },
		"watcher failed": func(o *proc.Observation) { o.WatcherFailed = true },
		"timed out":      func(o *proc.Observation) { o.TimedOut = true },
		"cancelled":      func(o *proc.Observation) { o.Cancelled = true },
		"output limit":   func(o *proc.Observation) { o.OutputLimit = true },
	}
	defer func() { runner = nil }()
	for name, mut := range cases {
		f := newFx(t, "sha1", "pass")
		o := usable
		mut(&o)
		calls := 0
		runner = func(ctx context.Context, s proc.Spec) ([]byte, proc.Observation, error) {
			calls++
			return []byte(reportPass), o, nil
		}
		out, _, err := f.run(context.Background(), f.req(f.head, true))
		if want := wantResultJSON("reviewer_unverified", "unverified", "null", "null", "sha1", f.head, "null"); err != nil || out != want || calls != 1 {
			t.Errorf("%s: %v %d %s", name, err, calls, out)
		}
	}
	// Never started, even with a legacy nil error and PASS output.
	f := newFx(t, "sha1", "pass")
	runner = func(ctx context.Context, s proc.Spec) ([]byte, proc.Observation, error) {
		return []byte(reportPass), proc.Observation{}, proc.ErrStart
	}
	out, _, err := f.run(context.Background(), f.req(f.head, true))
	if want := wantResultJSON("reviewer_unverified", "not_started", "null", "null", "sha1", f.head, "null"); err != nil || out != want {
		t.Errorf("not started: %v %s", err, out)
	}
	// The runner receives exactly the reviewer spec.
	g := newFx(t, "sha1", "pass", "rel/arg")
	var spec proc.Spec
	runner = func(ctx context.Context, s proc.Spec) ([]byte, proc.Observation, error) {
		spec = s
		return []byte(reportPass), usable, nil
	}
	if _, _, err := g.run(context.Background(), g.req(g.head, true)); err != nil {
		t.Fatal(err)
	}
	wantEnv := []string{"HOME=" + filepath.Join(g.attempt(), "reviewer", "home"), "TMPDIR=" + filepath.Join(g.attempt(), "reviewer", "tmp"), "LANG=C", "LC_ALL=C"}
	if spec.Path != g.tool || spec.Dir != g.repo || strings.Join(spec.Env, "|") != strings.Join(wantEnv, "|") ||
		strings.Join(spec.Args, "|") != "-baw-review-fake|pass|"+g.counter+"|rel/arg" || spec.Timeout != 20*time.Second ||
		spec.StdoutCap != 2048 || spec.StderrCap != 65536 {
		t.Errorf("spec %+v", spec)
	}
}

// Scratch HOME/TMPDIR start empty 0700, the environment is exactly four
// variables, stdin is /dev/null and cwd is the physical top level even when
// invoked from a subdirectory with relative state and plan paths.
func TestReviewScratchEnvironmentAndRelativePaths(t *testing.T) {
	f := newFx(t, "sha1", "env")
	f.writePlan("env", f.repo)
	sub := filepath.Join(f.repo, "docs")
	t.Chdir(sub)
	relState, _ := filepath.Rel(sub, f.state)
	relPlan, _ := filepath.Rel(sub, f.plan)
	req := Request{Repo: ".", StateDir: relState, RunID: testID, Candidate: f.head, Plan: relPlan, JSON: true}
	out, passed, err := f.run(context.Background(), req)
	if err != nil || !passed || out != wantResultJSON("review_passed", "exited", "0", "PASS", "sha1", f.head, f.head) {
		t.Fatalf("%v %v %s", err, passed, out)
	}
}

func TestReviewPlanParsing(t *testing.T) {
	exe := "/bin/true"
	cmd := func(inner string) string { return `{"schema_version":1,"reviewer":` + inner + `}` }
	ok := cmd(`{"executable":"` + exe + `","arguments":[],"timeout_seconds":300}`)
	long := strings.Repeat("a", 1024)
	args64 := strings.TrimSuffix(strings.Repeat(`"a",`, 64), ",")
	for name, c := range map[string]struct {
		data string
		ok   bool
	}{
		"valid":          {ok, true},
		"pad 65536":      {ok + strings.Repeat(" ", 65536-len(ok)), true},
		"pad 65537":      {ok + strings.Repeat(" ", 65537-len(ok)), false},
		"escaped dup":    {`{"schema_version":1,"reviewer":{"executable":"/bin/true","arguments":[],"timeout_seconds":1},"r\u0065viewer":{}}`, false},
		"nested dup":     {cmd(`{"executable":"/bin/true","arguments":[],"timeout_seconds":1,"timeout_second\u0073":2}`), false},
		"bad utf8":       {cmd(`{"executable":"/bin/tr` + "\xff" + `ue","arguments":[],"timeout_seconds":1}`), false},
		"schema 1.0":     {`{"schema_version":1.0,"reviewer":{"executable":"/bin/true","arguments":[],"timeout_seconds":1}}`, false},
		"schema string":  {`{"schema_version":"1","reviewer":{"executable":"/bin/true","arguments":[],"timeout_seconds":1}}`, false},
		"schema 2":       {`{"schema_version":2,"reviewer":{"executable":"/bin/true","arguments":[],"timeout_seconds":1}}`, false},
		"timeout 0":      {cmd(`{"executable":"/bin/true","arguments":[],"timeout_seconds":0}`), false},
		"timeout 301":    {cmd(`{"executable":"/bin/true","arguments":[],"timeout_seconds":301}`), false},
		"timeout exp":    {cmd(`{"executable":"/bin/true","arguments":[],"timeout_seconds":1e2}`), false},
		"timeout lead 0": {cmd(`{"executable":"/bin/true","arguments":[],"timeout_seconds":010}`), false},
		"relative exe":   {cmd(`{"executable":"true","arguments":[],"timeout_seconds":1}`), false},
		"64 args":        {cmd(`{"executable":"/bin/true","arguments":[` + args64 + `],"timeout_seconds":1}`), true},
		"65 args":        {cmd(`{"executable":"/bin/true","arguments":[` + args64 + `,"a"],"timeout_seconds":1}`), false},
		"arg 1024":       {cmd(`{"executable":"/bin/true","arguments":["` + long + `"],"timeout_seconds":1}`), true},
		"arg 1025":       {cmd(`{"executable":"/bin/true","arguments":["` + long + `a"],"timeout_seconds":1}`), false},
		"arg nul":        {cmd(`{"executable":"/bin/true","arguments":["a\u0000"],"timeout_seconds":1}`), false},
		"unknown key":    {`{"schema_version":1,"reviewer":{"executable":"/bin/true","arguments":[],"timeout_seconds":1},"x":1}`, false},
		"execute plan":   {`{"schema_version":1,"worker":{},"verification":{}}`, false},
		"trailing value": {ok + " {}", false},
		"array":          {`[` + ok + `]`, false},
	} {
		_, err := ParsePlan([]byte(c.data))
		if (err == nil) != c.ok || (err != nil && !IsCode(err, CodeInvalidPlan)) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestReviewPlanFileSafety(t *testing.T) {
	f := newFx(t, "sha1", "pass")
	good := readFile(t, f.plan)
	reset := func() {
		os.Remove(f.plan)
		write0600(t, f.plan, good)
	}
	t.Run("symlink", func(t *testing.T) {
		os.Remove(f.plan)
		other := filepath.Join(f.base, "plan-target.json")
		write0600(t, other, good)
		os.Symlink(other, f.plan)
		f.assertPre(t, "symlink", f.req(f.head, false), nil, "unsafe_state_path")
		reset()
	})
	t.Run("fifo", func(t *testing.T) {
		os.Remove(f.plan)
		if err := syscall.Mkfifo(f.plan, 0o600); err != nil {
			t.Fatal(err)
		}
		f.assertPreChild(t, "fifo", f.req(f.head, false), "unsafe_state_path")
		if fi, err := os.Lstat(f.plan); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("fifo plan not preserved: %v", err)
		}
		reset()
	})
	t.Run("directory", func(t *testing.T) {
		os.Remove(f.plan)
		mkdir0700(t, f.plan)
		f.assertPre(t, "dir", f.req(f.head, false), nil, "unsafe_state_path")
		os.Remove(f.plan)
		reset()
	})
	t.Run("mode", func(t *testing.T) {
		os.Chmod(f.plan, 0o644)
		f.assertPre(t, "0644", f.req(f.head, false), nil, "state_permissions")
		os.Chmod(f.plan, 0o400)
		f.assertPre(t, "0400", f.req(f.head, false), nil, "state_permissions")
		reset()
	})
	t.Run("missing", func(t *testing.T) {
		os.Remove(f.plan)
		f.assertPre(t, "missing", f.req(f.head, false), nil, "plan_unavailable")
		reset()
	})
	t.Run("linkcount 2 accepted", func(t *testing.T) {
		os.Link(f.plan, filepath.Join(f.base, "plan-link.json"))
		g := *f
		out, _, err := g.run(context.Background(), g.req(g.head, true))
		if err != nil || !strings.Contains(out, `"review_passed"`) {
			t.Fatalf("%v %s", err, out)
		}
	})
}

// Safety precedes identity: a file replaced between Lstat and open is
// rejected by mode when unsafe and by identity when safe; the original inode
// is retained via a second link, never unlinked or rewritten.
func TestReviewSafeReadReplacementAndPrecedence(t *testing.T) {
	defer func() { readHook = nil }()
	for _, name := range []string{"plan", "intent", "result"} {
		for _, c := range []struct {
			mode os.FileMode
			want string
		}{{0o644, "state_permissions"}, {0o600, "state_changed"}} {
			f := newFx(t, "sha1", "pass")
			target := map[string]string{"plan": f.plan, "intent": filepath.Join(f.execDir(), "intent.json"),
				"result": filepath.Join(f.execDir(), "result.json")}[name]
			keep := filepath.Join(f.base, "kept-original")
			orig, _ := os.Lstat(target)
			readHook = func(stage string) error {
				if stage == name+"-open" {
					os.Link(target, keep)
					repl := target + ".new"
					os.WriteFile(repl, readFile(t, target), c.mode)
					os.Chmod(repl, c.mode)
					os.Rename(repl, target)
				}
				return nil
			}
			d := &descriptors{}
			opened = d.add
			_, err := Review(context.Background(), f.req(f.head, true), &strings.Builder{})
			opened, readHook = nil, nil
			d.assertClosed(t, name)
			if err == nil || err.Error() != c.want {
				t.Errorf("%s %v: %v want %s", name, c.mode, err, c.want)
			}
			kept, err2 := os.Lstat(keep)
			if err2 != nil || !os.SameFile(kept, orig) {
				t.Errorf("%s: original inode not retained", name)
			}
			if f.starts() != 0 {
				t.Errorf("%s: started", name)
			}
		}
	}
	// Read and parser failures win over a close failure; close fails alone
	// only after everything else succeeded.
	for _, c := range []struct {
		name, fail string
		corrupt    bool
		want       string
	}{
		{"plan", "plan-close", false, "plan_unavailable"},
		{"plan", "plan-close", true, "invalid_review_plan"},
		{"plan", "plan-read", false, "plan_unavailable"},
		{"intent", "intent-close", false, "execution_receipt_unavailable"},
		{"intent", "intent-close", true, "invalid_execution_receipt"},
		{"intent", "intent-read", false, "execution_receipt_unavailable"},
		{"result", "result-close", false, "execution_receipt_unavailable"},
		{"result", "result-close", true, "invalid_execution_receipt"},
		{"result", "result-open", false, "execution_receipt_unavailable"},
	} {
		f := newFx(t, "sha1", "pass")
		if c.corrupt {
			p := map[string]string{"plan": f.plan, "intent": filepath.Join(f.execDir(), "intent.json"),
				"result": filepath.Join(f.execDir(), "result.json")}[c.name]
			write0600(t, p, []byte("{}"))
		}
		closeCalled := false
		readHook = func(stage string) error {
			if stage == c.fail {
				closeCalled = true
				return errors.New("injected")
			}
			return nil
		}
		d := &descriptors{}
		opened = d.add
		_, err := Review(context.Background(), f.req(f.head, true), &strings.Builder{})
		opened, readHook = nil, nil
		d.assertClosed(t, c.fail)
		if err == nil || err.Error() != c.want {
			t.Errorf("%s corrupt=%v: %v want %s", c.fail, c.corrupt, err, c.want)
		}
		if !c.corrupt && !closeCalled {
			t.Errorf("%s: stage not reached", c.fail)
		}
	}
}

// Special files: the supplied read-only socket/special-bit fixtures are used
// only as unsafe inputs when present; otherwise sockets and setgid files are
// created here. Permission bits are observed, never assumed.
func TestReviewSpecialFiles(t *testing.T) {
	f := newFx(t, "sha1", "pass")
	if p := os.Getenv("BAW_TEST_EXEC_SETGID_PLAN"); p != "" {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeSetgid == 0 {
			t.Fatalf("supplied setgid plan fixture invalid: %v", err)
		}
		req := f.req(f.head, false)
		req.Plan = p
		f.assertPre(t, "supplied setgid plan", req, nil, "state_permissions")
	} else {
		os.Chmod(f.plan, 0o600|os.ModeSetgid)
		fi, _ := os.Lstat(f.plan)
		if fi.Mode()&os.ModeSetgid == 0 {
			t.Fatal("setgid bit not observed on created plan; cannot exercise route")
		}
		f.assertPre(t, "setgid plan", f.req(f.head, false), nil, "state_permissions")
		os.Chmod(f.plan, 0o600)
	}
	sock := ""
	if d := os.Getenv("BAW_TEST_SOCKET_STATE_DIR"); d != "" {
		filepath.WalkDir(d, func(p string, e os.DirEntry, err error) error {
			if err == nil && e.Type()&os.ModeSocket != 0 && sock == "" {
				sock = p
			}
			return nil
		})
		if sock == "" {
			t.Fatal("supplied socket fixture has no socket")
		}
	} else {
		dir, err := os.MkdirTemp("/tmp", "bawr")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		sock = filepath.Join(dir, "s")
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd)
		if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: sock}); err != nil {
			t.Fatal(err)
		}
	}
	sfi, err := os.Lstat(sock)
	if err != nil || sfi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket not observed: %v", err)
	}
	req := f.req(f.head, false)
	req.Plan = sock
	f.assertPreChild(t, "socket plan", req, "unsafe_state_path")
	if after, err := os.Lstat(sock); err != nil || !os.SameFile(after, sfi) || after.Mode() != sfi.Mode() {
		t.Errorf("socket fixture not preserved: %v", err)
	}
}

// FIFO execution receipts are rejected in a bounded child before any open
// can block, and the FIFOs are left in place.
func TestReviewReceiptFIFOChild(t *testing.T) {
	for _, name := range []string{"intent.json", "result.json"} {
		f := newFx(t, "sha1", "pass")
		p := filepath.Join(f.execDir(), name)
		os.Remove(p)
		if err := syscall.Mkfifo(p, 0o600); err != nil {
			t.Fatal(err)
		}
		f.assertPreChild(t, name+" fifo", f.req(f.head, false), "unsafe_state_path")
		if fi, err := os.Lstat(p); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
			t.Errorf("%s fifo not preserved: %v", name, err)
		}
	}
}

func TestReviewReceiptValidation(t *testing.T) {
	other := strings.Repeat("f", 32)
	str := func(s string) *string { return &s }
	for _, c := range []struct {
		name string
		mut  func(f *fx)
		want string
	}{
		{"intent malformed id", func(f *fx) { f.r.intentID = "ABC" }, "invalid_execution_receipt"},
		{"intent different id", func(f *fx) { f.r.intentID = other }, "execution_receipt_mismatch"},
		{"result malformed id", func(f *fx) { f.r.resultID = strings.Repeat("g", 32) }, "invalid_execution_receipt"},
		{"result different id", func(f *fx) { f.r.resultID = other }, "execution_receipt_mismatch"},
		{"reversed interval with mismatching link", func(f *fx) {
			f.r.completed = "2026-10-04T09:59:59Z"
			f.r.resultID = other
		}, "invalid_execution_receipt"},
		{"equal timestamps", func(f *fx) { f.r.completed = f.r.created }, ""},
		{"noncanonical timestamp", func(f *fx) { f.r.completed = "2026-10-04T10:05:00+00:00" }, "invalid_execution_receipt"},
		{"ticket", func(f *fx) { f.r.ticket = "https://github.com/example/project/issues/22" }, "execution_receipt_mismatch"},
		{"scope", func(f *fx) { f.r.scope = strings.Repeat("6", 64) }, "execution_receipt_mismatch"},
		{"policy", func(f *fx) { f.r.policy = strings.Repeat("7", 40) }, "execution_receipt_mismatch"},
		{"head", func(f *fx) { f.r.head = strings.Repeat("8", 40); f.r.before = f.r.head }, "execution_receipt_mismatch"},
		{"before head", func(f *fx) { f.r.before = strings.Repeat("8", 40) }, "execution_receipt_mismatch"},
		{"created", func(f *fx) {
			f.r.created = "2026-10-04T10:00:01Z"
			write0600(f.t, filepath.Join(f.execDir(), "result.json"), []byte(f.resultJSON()))
			f.r.created = "2026-10-04T10:00:00Z"
			write0600(f.t, filepath.Join(f.execDir(), "intent.json"), []byte(f.intentJSON()))
		}, "execution_receipt_mismatch"},
		{"not passed", func(f *fx) {
			f.r.outcome = "verification_failed"
			f.r.verification = `{"state":"exited","exit_code":1}`
		}, "review_not_eligible"},
		{"after null", func(f *fx) { f.r.after = nil }, "review_not_eligible"},
		{"worker failed", func(f *fx) {
			f.r.outcome, f.r.worker, f.r.verification, f.r.after = "worker_failed", `{"state":"exited","exit_code":2}`, `{"state":"not_started","exit_code":null}`, nil
		}, "review_not_eligible"},
		{"after head descendant oid", func(f *fx) { f.r.after = str(f.head) }, ""},
	} {
		f := newFx(t, "sha1", "pass")
		c.mut(f)
		if c.name != "created" {
			f.writeReceipts()
		}
		if c.want == "" {
			if _, _, err := f.run(context.Background(), f.req(f.head, true)); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		f.assertPre(t, c.name, f.req(f.head, false), nil, c.want)
	}
	// Byte-level receipt cases.
	for _, c := range []struct {
		name, file string
		data       func(f *fx) string
		want       string
	}{
		{"intent 65536", "intent.json", func(f *fx) string { s := f.intentJSON(); return s + strings.Repeat(" ", 65536-len(s)) }, ""},
		{"intent 65537", "intent.json", func(f *fx) string { s := f.intentJSON(); return s + strings.Repeat(" ", 65537-len(s)) }, "invalid_execution_receipt"},
		{"result 65536", "result.json", func(f *fx) string { s := f.resultJSON(); return s + strings.Repeat(" ", 65536-len(s)) }, ""},
		{"result 65537", "result.json", func(f *fx) string { s := f.resultJSON(); return s + strings.Repeat(" ", 65537-len(s)) }, "invalid_execution_receipt"},
		{"intent escaped dup", "intent.json", func(f *fx) string {
			return strings.Replace(f.intentJSON(), `"created_at"`, `"plan_sh\u0061256":"`+strings.Repeat("9", 64)+`","created_at"`, 1)
		}, "invalid_execution_receipt"},
		{"result escaped dup", "result.json", func(f *fx) string {
			return strings.Replace(f.resultJSON(), `"completed_at"`, `"outcom\u0065":"verification_passed","completed_at"`, 1)
		}, "invalid_execution_receipt"},
		{"intent bad utf8", "intent.json", func(f *fx) string { return strings.Replace(f.intentJSON(), "}", `,"x":"`+"\xff"+`"}`, 1) }, "invalid_execution_receipt"},
		{"intent schema 2", "intent.json", func(f *fx) string {
			return strings.Replace(f.intentJSON(), `"schema_version":1`, `"schema_version":2`, 1)
		}, "invalid_execution_receipt"},
		{"result schema 1.0", "result.json", func(f *fx) string {
			return strings.Replace(f.resultJSON(), `"schema_version":1`, `"schema_version":1.0`, 1)
		}, "invalid_execution_receipt"},
		{"intent missing run_id", "intent.json", func(f *fx) string { return strings.Replace(f.intentJSON(), `"run_id":"`+testID+`",`, "", 1) }, "invalid_execution_receipt"},
		{"result numeric run_id", "result.json", func(f *fx) string { return strings.Replace(f.resultJSON(), `"run_id":"`+testID+`"`, `"run_id":1`, 1) }, "invalid_execution_receipt"},
	} {
		f := newFx(t, "sha1", "pass")
		write0600(t, filepath.Join(f.execDir(), c.file), []byte(c.data(f)))
		if c.want == "" {
			if _, _, err := f.run(context.Background(), f.req(f.head, true)); err != nil {
				t.Errorf("%s: %v", c.name, err)
			}
			continue
		}
		f.assertPre(t, c.name, f.req(f.head, false), nil, c.want)
	}
	// File safety, absence and link count.
	for _, c := range []struct {
		name string
		mut  func(f *fx)
		want string
	}{
		{"no execute namespace", func(f *fx) { os.RemoveAll(filepath.Join(f.state, ExecuteNamespace)) }, "execution_receipt_unavailable"},
		{"no execute id", func(f *fx) { os.RemoveAll(f.execDir()) }, "execution_receipt_unavailable"},
		{"no result", func(f *fx) { os.Remove(filepath.Join(f.execDir(), "result.json")) }, "execution_receipt_unavailable"},
		{"no intent", func(f *fx) { os.Remove(filepath.Join(f.execDir(), "intent.json")) }, "execution_receipt_unavailable"},
		{"execute id 0755", func(f *fx) { os.Chmod(f.execDir(), 0o755) }, "state_permissions"},
		{"execute id symlink", func(f *fx) {
			os.Rename(f.execDir(), f.execDir()+"-real")
			os.Symlink(f.execDir()+"-real", f.execDir())
		}, "unsafe_state_path"},
		{"result symlink", func(f *fx) {
			p := filepath.Join(f.execDir(), "result.json")
			os.Rename(p, p+".real")
			os.Symlink(p+".real", p)
		}, "unsafe_state_path"},
		{"intent 0640", func(f *fx) { os.Chmod(filepath.Join(f.execDir(), "intent.json"), 0o640) }, "state_permissions"},
		{"review namespace 0755 before plan", func(f *fx) {
			mkdir0700(f.t, filepath.Join(f.state, Namespace))
			os.Chmod(filepath.Join(f.state, Namespace), 0o755)
			os.Remove(f.plan)
		}, "state_permissions"},
		{"invalid plan before missing record", func(f *fx) {
			write0600(f.t, f.plan, []byte("{}"))
			os.Remove(filepath.Join(f.state, "records-v1", testID+".json"))
		}, "invalid_review_plan"},
		{"invalid receipt before existing review", func(f *fx) {
			mkdirAll0700(f.t, f.attempt())
			write0600(f.t, filepath.Join(f.execDir(), "intent.json"), []byte("{}"))
		}, "invalid_execution_receipt"},
		{"existing empty review id", func(f *fx) { mkdirAll0700(f.t, f.attempt()) }, "review_exists"},
		{"unsafe executable", func(f *fx) { os.Chmod(f.tool, 0o722) }, "executable_unavailable"},
		{"plan inside top level", func(f *fx) {
			p := filepath.Join(f.repo, ".git", "plan.json")
			write0600(f.t, p, readFile(f.t, f.plan))
			f.plan = p
		}, "unsafe_execution_layout"},
	} {
		f := newFx(t, "sha1", "pass")
		c.mut(f)
		f.assertPre(t, c.name, f.req(f.head, false), nil, c.want)
	}
	// Receipts with a second hard link are accepted.
	f := newFx(t, "sha1", "pass")
	os.Link(filepath.Join(f.execDir(), "result.json"), filepath.Join(f.base, "result-link"))
	os.Link(filepath.Join(f.execDir(), "intent.json"), filepath.Join(f.base, "intent-link"))
	if _, passed, err := f.run(context.Background(), f.req(f.head, true)); err != nil || !passed {
		t.Errorf("linked receipts: %v", err)
	}
}

// Every acquisition/storage/close/link/delivery stage fault: starts stay 0
// before intent, uncertainty after the ID, retained effects, no replay.
func TestReviewStageFaults(t *testing.T) {
	defer func() { hook = nil }()
	type row struct {
		stage, want string
		owned       bool
		starts      int
	}
	var rows []row
	for _, s := range []string{"ns-mkdir", "ns-chmod", "root-lstat", "root-open", "root-recheck", "root-close", "ns-open", "ns-recheck", "id-mkdir"} {
		rows = append(rows, row{s, "review_storage_unavailable", false, 0})
	}
	rows = append(rows, row{"root-sync", "durability_unavailable", false, 0})
	for _, s := range []string{"id-chmod", "ns-sync", "ns-close", "id-open", "id-recheck", "reviewer-mkdir", "reviewer-chmod",
		"reviewer-home-mkdir", "reviewer-home-chmod", "reviewer-home-open", "reviewer-home-recheck", "reviewer-home-sync",
		"reviewer-home-close", "reviewer-tmp-mkdir", "reviewer-tmp-sync", "reviewer-tmp-close", "reviewer-open",
		"reviewer-recheck", "reviewer-sync", "reviewer-close", "id-sync", "id-close", "intent-random", "intent-create",
		"intent-chmod", "intent-write", "intent-sync", "intent-close", "intent-link", "intent-dir-open", "intent-dir-recheck",
		"intent-dir-sync", "intent-dir-close"} {
		rows = append(rows, row{s, "review_uncertain", true, 0})
	}
	for _, s := range []string{"output-limit", "result-random", "result-create", "result-chmod", "result-write", "result-sync",
		"result-close", "result-link", "result-dir-open", "result-dir-sync", "result-dir-close", "deliver"} {
		rows = append(rows, row{s, "review_uncertain", true, 1})
	}
	if len(rows) != 55 {
		t.Fatalf("stage table has %d rows, want 55", len(rows))
	}
	// Stages hit while this package holds an open descriptor of its own.
	heldOpen := map[string]bool{"root-open": true, "id-mkdir": true, "root-recheck": true, "root-sync": true, "ns-recheck": true, "id-chmod": true,
		"ns-sync": true, "id-recheck": true, "reviewer-mkdir": true, "reviewer-chmod": true, "reviewer-home-mkdir": true,
		"reviewer-home-chmod": true, "reviewer-home-open": true, "reviewer-home-recheck": true, "reviewer-home-sync": true,
		"reviewer-home-close": true, "reviewer-tmp-mkdir": true, "reviewer-tmp-sync": true, "reviewer-tmp-close": true,
		"reviewer-open": true, "reviewer-recheck": true, "reviewer-sync": true, "reviewer-close": true, "id-sync": true,
		"intent-chmod": true, "intent-write": true, "intent-sync": true, "intent-dir-recheck": true, "intent-dir-sync": true,
		"result-chmod": true, "result-write": true, "result-sync": true, "result-dir-sync": true}
	order := make(map[string]int, len(rows))
	for i, r := range rows {
		order[r.stage] = i
	}
	after := func(stage, first string) bool { return order[stage] >= order[first] }
	for _, r := range rows {
		f := newFx(t, "sha1", "pass")
		hits := 0
		var mine []*os.File
		var openAtHit []*os.File
		hook = func(stage string) error {
			if stage == r.stage {
				hits++
				for _, d := range mine {
					if _, err := d.Stat(); err == nil {
						openAtHit = append(openAtHit, d)
					}
				}
				return errors.New("injected")
			}
			return nil
		}
		execBefore := snapshot(t, filepath.Join(f.state, ExecuteNamespace))
		recBefore := snapshot(t, filepath.Join(f.state, "records-v1"))
		// Wrap the observer f.run installs to see storage descriptors too.
		d := &descriptors{}
		opened = func(x *os.File) { d.add(x); mine = append(mine, x) }
		var outb bytes.Buffer
		now = func() time.Time { tm, _ := time.Parse(time.RFC3339, fixedNow); return tm }
		passed, err := Review(context.Background(), f.req(f.head, true), &outb)
		opened, now, hook = nil, time.Now, nil
		out := outb.String()
		d.assertClosed(t, r.stage)
		if err == nil || err.Error() != r.want || out != "" || passed || hits != 1 {
			t.Errorf("%s: %v out %q hits %d want %s", r.stage, err, out, hits, r.want)
		}
		if heldOpen[r.stage] != (len(openAtHit) > 0) {
			t.Errorf("%s: %d descriptors open at the injected stage, held=%v", r.stage, len(openAtHit), heldOpen[r.stage])
		}
		for _, x := range openAtHit {
			if _, err := x.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("%s: descriptor open at the fault was not closed", r.stage)
			}
		}
		if snapshot(t, filepath.Join(f.state, ExecuteNamespace)) != execBefore ||
			snapshot(t, filepath.Join(f.state, "records-v1")) != recBefore {
			t.Errorf("%s: execution receipts or run record changed", r.stage)
		}
		_, nsErr := os.Lstat(filepath.Join(f.state, Namespace))
		if (nsErr == nil) != (r.stage != "ns-mkdir") {
			t.Errorf("%s: namespace presence %v", r.stage, nsErr)
		}
		if r.owned {
			pending := func(name string) int {
				m, _ := filepath.Glob(filepath.Join(f.attempt(), ".pending-"+name+"-*"))
				return len(m)
			}
			has := func(name string) bool { _, e := os.Lstat(filepath.Join(f.attempt(), name)); return e == nil }
			wantInt := r.starts == 1 || after(r.stage, "intent-dir-open")
			wantIntStage := r.starts == 1 || after(r.stage, "intent-chmod")
			wantRes := after(r.stage, "result-dir-open") && r.starts == 1
			wantResStage := after(r.stage, "result-chmod") && r.starts == 1
			if has("intent.json") != wantInt || (pending("intent") == 1) != wantIntStage ||
				has("result.json") != wantRes || (pending("result") == 1) != wantResStage {
				t.Errorf("%s: retained receipts intent=%v/%d result=%v/%d", r.stage, has("intent.json"), pending("intent"),
					has("result.json"), pending("result"))
			}
		}
		if f.starts() != r.starts {
			t.Errorf("%s: starts %d want %d", r.stage, f.starts(), r.starts)
		}
		_, statErr := os.Lstat(f.attempt())
		if (statErr == nil) != r.owned {
			t.Errorf("%s: owned ID %v", r.stage, statErr)
		}
		// No replay: the retained attempt refuses a second review.
		if r.owned {
			before := snapshot(t, f.state)
			hook = nil
			if _, _, err := f.run(context.Background(), f.req(f.head, true)); err == nil || err.Error() != "review_exists" {
				t.Errorf("%s: replay %v", r.stage, err)
			}
			if snapshot(t, f.state) != before || f.starts() != r.starts {
				t.Errorf("%s: replay changed state or started", r.stage)
			}
		}
		if strings.HasPrefix(r.stage, "result-") || r.stage == "deliver" {
			if _, err := os.Lstat(filepath.Join(f.attempt(), "intent.json")); err != nil {
				t.Errorf("%s: intent not retained", r.stage)
			}
		}
	}
	// A short delivery write is review_uncertain after publication.
	f := newFx(t, "sha1", "pass")
	_, err := Review(context.Background(), f.req(f.head, true), shortWriter{})
	if err == nil || err.Error() != "review_uncertain" {
		t.Errorf("short write: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err != nil {
		t.Error("result not retained after short write")
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) / 2, nil }

func TestReviewCancellationBoundaries(t *testing.T) {
	defer func() { hook = nil }()
	t.Run("pre-acquisition", func(t *testing.T) {
		f := newFx(t, "sha1", "pass")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		f.ctx = ctx
		f.assertPre(t, "pre", f.req(f.head, true), nil, "review_cancelled")
	})
	for _, c := range []struct {
		stage, want string
		owned, ns   bool
	}{
		{"ns-chmod", "review_cancelled", false, true},
		{"root-sync", "review_cancelled", false, true},
		{"ns-recheck", "review_cancelled", false, true},
		{"id-mkdir", "review_cancelled", false, true},
		{"id-chmod", "review_uncertain", true, true},
		{"reviewer-home-mkdir", "review_uncertain", true, true},
	} {
		f := newFx(t, "sha1", "pass")
		ctx, cancel := context.WithCancel(context.Background())
		hook = func(stage string) error {
			if stage == c.stage {
				cancel()
			}
			return nil
		}
		out, _, err := f.run(ctx, f.req(f.head, true))
		hook = nil
		cancel()
		if err == nil || err.Error() != c.want || out != "" || f.starts() != 0 {
			t.Errorf("%s: %v %q starts %d", c.stage, err, out, f.starts())
		}
		if _, e := os.Lstat(f.attempt()); (e == nil) != c.owned {
			t.Errorf("%s: owned %v", c.stage, e)
		}
		if _, e := os.Lstat(filepath.Join(f.state, Namespace)); (e == nil) != c.ns {
			t.Errorf("%s: namespace %v", c.stage, e)
		}
	}
	// After durable intent, cancellation before classification is recorded.
	for _, c := range []struct {
		stage, state string
	}{{"reviewer-start", "not_started"}, {"post-inspection", "unverified"}} {
		f := newFx(t, "sha1", "pass")
		ctx, cancel := context.WithCancel(context.Background())
		hook = func(stage string) error {
			if stage == c.stage {
				cancel()
			}
			return nil
		}
		out, _, err := f.run(ctx, f.req(f.head, true))
		hook = nil
		cancel()
		want := wantResultJSON("reviewer_unverified", c.state, "null", "null", "sha1", f.head, "null")
		if err != nil || out != want {
			t.Errorf("%s: %v %s", c.stage, err, out)
		}
		wantStarts := 1
		if c.state == "not_started" {
			wantStarts = 0
		}
		if f.starts() != wantStarts {
			t.Errorf("%s: starts %d", c.stage, f.starts())
		}
	}
	// Cancellation while the real reviewer runs, after its nonce marker.
	f := newFx(t, "sha1", "sleep", "n0nce1")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			if _, err := os.Lstat(filepath.Join(f.counter, "started-n0nce1")); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()
	out, _, err := f.run(ctx, f.req(f.head, true))
	<-done
	if want := wantResultJSON("reviewer_unverified", "unverified", "null", "null", "sha1", f.head, "null"); err != nil || out != want {
		t.Errorf("running: %v %s", err, out)
	}
	// Late cancellation after classification does not change the outcome.
	g := newFx(t, "sha1", "pass")
	ctx2, cancel2 := context.WithCancel(context.Background())
	hook = func(stage string) error {
		if stage == "output-limit" {
			cancel2()
		}
		return nil
	}
	out, passed, err := g.run(ctx2, g.req(g.head, true))
	hook = nil
	cancel2()
	if err != nil || !passed || !strings.Contains(out, `"review_passed"`) {
		t.Errorf("late: %v %s", err, out)
	}
}

// Same-ID concurrency admits exactly one reviewer.
func TestReviewSameIDRace(t *testing.T) {
	f := newFx(t, "sha1", "pass")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Review(context.Background(), f.req(f.head, true), &strings.Builder{})
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, e := range errs {
		switch {
		case e == nil:
			ok++
		case e.Error() != "review_exists":
			t.Errorf("race error %v", e)
		}
	}
	if ok != 1 || f.starts() != 1 {
		t.Errorf("admitted %d starts %d", ok, f.starts())
	}
}

// A real controller is SIGKILLed before and after reviewer start; the
// intent, staging and scratch remain, nothing is replayed. The controller is
// this test's own child, killed through its exec handle and joined. The
// finite fake reviewer is never signalled: its termination is established
// from independently visible evidence only (its start/end nonce markers and
// the kernel releasing the exclusive lock it held for its whole life), with
// bounded waits.
func TestReviewControllerSIGKILL(t *testing.T) {
	waitFor := func(bound time.Duration, cond func() bool) bool {
		for end := time.Now().Add(bound); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
			if cond() {
				return true
			}
		}
		return cond()
	}
	for _, format := range []string{"sha1", "sha256"} {
		for _, before := range []bool{true, false} {
			nonce := "k1ll" + format
			f := newFx(t, format, "sleep", nonce)
			exists := func(name string) bool { _, e := os.Lstat(filepath.Join(f.counter, name)); return e == nil }
			lockFree := func() bool {
				lf, err := os.OpenFile(filepath.Join(f.counter, "lock-"+nonce), os.O_RDWR, 0)
				if err != nil {
					return false
				}
				defer lf.Close()
				if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
					return false
				}
				syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)
				return true
			}
			dir := filepath.Join(f.base, "controller")
			mkdir0700(t, dir)
			cfg, _ := json.Marshal(controllerConfig{Request: f.req(f.head, true), Counter: f.counter, Nonce: nonce, Before: before})
			write0600(t, filepath.Join(dir, "config.json"), cfg)
			self, _ := os.Executable()
			cmd := exec.Command(self, "-baw-review-controller", dir)
			var cout bytes.Buffer
			cmd.Stdout = &cout
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			mk := "started-" + nonce
			if before {
				mk = "ready-" + nonce
			}
			seen := waitFor(20*time.Second, func() bool { return exists(mk) })
			cmd.Process.Kill()
			werr := cmd.Wait()
			if !seen {
				t.Fatalf("%s before=%v: marker not seen", format, before)
			}
			if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
				t.Errorf("%s before=%v: controller not SIGKILLed: %v", format, before, werr)
			}
			if cout.Len() != 0 {
				t.Errorf("%s before=%v: controller delivered output", format, before)
			}
			wantStarts := 0
			if before {
				// No reviewer ever started: no lock, start or end marker
				// appears during a bounded observation window.
				if waitFor(500*time.Millisecond, func() bool {
					return exists("lock-"+nonce) || exists("started-"+nonce) || exists("end-"+nonce)
				}) {
					t.Errorf("%s: a reviewer started after a pre-start SIGKILL", format)
				}
			} else {
				wantStarts = 1
				// The orphaned finite reviewer finishes on its own: end marker
				// visible and its lock released by the kernel.
				if !waitFor(10*time.Second, func() bool { return exists("end-"+nonce) && lockFree() }) {
					t.Fatalf("%s: orphaned finite reviewer not observed to finish", format)
				}
			}
			if f.starts() != wantStarts {
				t.Errorf("%s before=%v: starts %d", format, before, f.starts())
			}
			// Exactly the intent and its staging survive; no result.
			checkLayout(t, f, false)
			if m, _ := filepath.Glob(filepath.Join(f.attempt(), ".pending-result-*")); len(m) != 0 {
				t.Errorf("%s before=%v: result staging present", format, before)
			}
			// External reconciliation: the retained attempt refuses replay
			// and the retained evidence is unchanged by the refusal.
			snap := snapshot(t, f.state)
			if _, _, err := f.run(context.Background(), f.req(f.head, true)); err == nil || err.Error() != "review_exists" {
				t.Errorf("%s before=%v: replay %v", format, before, err)
			}
			if f.starts() != wantStarts || snapshot(t, f.state) != snap {
				t.Errorf("%s before=%v: replay started or changed state", format, before)
			}
		}
	}
}

// Physical changes to the top level or object format after the review are
// never review_passed. The reviewer replaces the top-level directory at the
// same path (original retained beside it); the test independently confirms
// what changed with Git and inode observations.
func TestReviewTopLevelAndFormatChanged(t *testing.T) {
	other := map[string]string{"sha1": "sha256", "sha256": "sha1"}
	for _, format := range []string{"sha1", "sha256"} {
		f := newFx(t, format, "replace-top")
		orig, err := os.Stat(f.repo)
		if err != nil {
			t.Fatal(err)
		}
		// Record (never alter) the real post-inspection result, so the
		// test can show that only the physical identity differed.
		realTop := inspectTop
		var post *inspect.Packet
		var postTop string
		var postErr error
		calls := 0
		inspectTop = func(o inspect.Options) (*inspect.Packet, string, error) {
			p, top, err := realTop(o)
			if calls++; calls == 2 {
				post, postTop, postErr = p, top, err
			}
			return p, top, err
		}
		out, passed, err := f.run(context.Background(), f.req(f.head, true))
		inspectTop = realTop
		if calls != 2 || postErr != nil || postTop != f.repo || post.Repository.ObjectFormat != format ||
			post.Repository.Head == nil || *post.Repository.Head != f.head || !cleanCounts(post.Repository.Counts) {
			t.Errorf("%s: post-inspection differed beyond identity: calls %d err %v top %q", format, calls, postErr, postTop)
		}
		if want := wantResultJSON("candidate_changed", "exited", "0", "PASS", format, f.head, "null"); err != nil || passed || out != want {
			t.Errorf("%s replace-top: %v %s", format, err, out)
		}
		kept, err1 := os.Stat(f.repo + ".orig")
		cur, err2 := os.Stat(f.repo)
		if err1 != nil || err2 != nil || !os.SameFile(kept, orig) || os.SameFile(cur, orig) {
			t.Errorf("%s: top level not physically replaced with original retained", format)
		}
		// Only identity changed: the replacement has the same HEAD and is clean.
		if h, _ := tf.OID(f.home, f.repo, "HEAD"); h != f.head {
			t.Errorf("%s: replacement HEAD %s", format, h)
		}
		if st := f.git("status", "--porcelain", "--untracked-files=all"); st != "" {
			t.Errorf("%s: replacement not clean: %q", format, st)
		}
		if f.starts() != 1 {
			t.Errorf("%s: starts %d", format, f.starts())
		}

		g := newFx(t, format, "replace-format", other[format])
		out, passed, err = g.run(context.Background(), g.req(g.head, false))
		if want := wantText("candidate_changed", "exited", "0", "PASS", format, g.head, "null"); err != nil || passed || out != want {
			t.Errorf("%s replace-format: %v %s", format, err, out)
		}
		if fm := strings.TrimSpace(g.git("rev-parse", "--show-object-format")); fm != other[format] {
			t.Errorf("%s: replacement format %q", format, fm)
		}
		if _, err := os.Stat(filepath.Join(g.repo+".orig", ".git")); err != nil {
			t.Errorf("%s: original repository not retained", format)
		}
	}
}

// Each post-inspection identity/format comparison is decisive on its own:
// the real post-inspection result is altered in exactly one field through
// the existing package inspectTop seam.
func TestReviewPostInspectionFieldIsolation(t *testing.T) {
	realTop := inspectTop
	defer func() { inspectTop = realTop }()
	for _, c := range []struct {
		name string
		mut  func(p *inspect.Packet, top *string)
		want string
	}{
		{"unchanged control", func(*inspect.Packet, *string) {}, "review_passed"},
		{"top-level path", func(_ *inspect.Packet, top *string) { *top = *top + "-moved" }, "candidate_changed"},
		{"object format", func(p *inspect.Packet, _ *string) {
			p.Repository.ObjectFormat = map[string]string{"sha1": "sha256", "sha256": "sha1"}[p.Repository.ObjectFormat]
		}, "candidate_changed"},
	} {
		f := newFx(t, "sha1", "pass")
		calls := 0
		inspectTop = func(o inspect.Options) (*inspect.Packet, string, error) {
			p, top, err := realTop(o)
			calls++
			if calls == 2 && err == nil {
				c.mut(p, &top)
			}
			return p, top, err
		}
		out, _, err := f.run(context.Background(), f.req(f.head, true))
		inspectTop = realTop
		if err != nil || calls != 2 || !strings.Contains(out, `"outcome":"`+c.want+`"`) {
			t.Errorf("%s: %v calls %d %s", c.name, err, calls, out)
		}
		if c.want == "candidate_changed" && !strings.Contains(out, `"after_head":null`) {
			t.Errorf("%s: after_head not null", c.name)
		}
	}
}

func TestReviewResultValidator(t *testing.T) {
	h := strings.Repeat("a", 40)
	good := wantResultJSON("review_passed", "exited", "0", "PASS", "sha1", h, h)
	if !validResult([]byte(good), testID) {
		t.Fatal("valid result rejected")
	}
	for name, s := range map[string]string{
		"pass without after": wantResultJSON("review_passed", "exited", "0", "PASS", "sha1", h, "null"),
		"fixes verdict pass": wantResultJSON("review_required_fixes", "exited", "0", "PASS", "sha1", h, h),
		"failed exit 0":      wantResultJSON("reviewer_failed", "exited", "0", "null", "sha1", h, "null"),
		"unverified verdict": wantResultJSON("reviewer_unverified", "exited", "0", "PASS", "sha1", h, "null"),
		"changed with after": wantResultJSON("candidate_changed", "exited", "0", "PASS", "sha1", h, h),
		"exit 256":           wantResultJSON("reviewer_failed", "exited", "256", "null", "sha1", h, "null"),
		"not started code":   wantResultJSON("reviewer_unverified", "not_started", "0", "null", "sha1", h, "null"),
		"wrong width":        wantResultJSON("reviewer_unverified", "not_started", "null", "null", "sha256", h, "null"),
		"after differs":      wantResultJSON("review_passed", "exited", "0", "PASS", "sha1", h, strings.Repeat("b", 40)),
		"bad verdict":        wantResultJSON("candidate_changed", "exited", "0", "OK", "sha1", h, "null"),
	} {
		if validResult([]byte(s), testID) {
			t.Errorf("%s accepted", name)
		}
	}
}

// Cancellation seen at the final boundary before the exclusive ID mkdir is
// review_cancelled with no owned ID, zero starts and empty output; the
// namespace descriptor held at that boundary is actually closed.
func TestReviewCancelAtIDMkdirBoundary(t *testing.T) {
	defer func() { hook, opened = nil, nil }()
	for _, format := range []string{"sha1", "sha256"} {
		for _, asJSON := range []bool{true, false} {
			f := newFx(t, format, "pass")
			ctx, cancel := context.WithCancel(context.Background())
			hits := 0
			var mine, openAtHit []*os.File
			hook = func(stage string) error {
				if stage == "id-mkdir" {
					hits++
					for _, d := range mine {
						if _, err := d.Stat(); err == nil {
							openAtHit = append(openAtHit, d)
						}
					}
					cancel()
				}
				return nil
			}
			d := &descriptors{}
			opened = func(x *os.File) { d.add(x); mine = append(mine, x) }
			var outb bytes.Buffer
			passed, err := Review(ctx, f.req(f.head, asJSON), &outb)
			hook, opened = nil, nil
			cancel()
			d.assertClosed(t, "id-mkdir cancel")
			name := format + " json=" + map[bool]string{true: "true", false: "false"}[asJSON]
			if err == nil || err.Error() != "review_cancelled" || outb.Len() != 0 || passed || hits != 1 {
				t.Errorf("%s: %v out %q passed %v hits %d", name, err, outb.String(), passed, hits)
			}
			ns := filepath.Join(f.state, Namespace)
			if len(openAtHit) != 1 || openAtHit[0].Name() != ns {
				t.Errorf("%s: descriptors open at id-mkdir %d, want only the namespace", name, len(openAtHit))
			}
			for _, x := range openAtHit {
				if _, err := x.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Errorf("%s: namespace descriptor not closed", name)
				}
			}
			if _, e := os.Lstat(f.attempt()); e == nil {
				t.Errorf("%s: ID owned after pre-ID cancellation", name)
			}
			if es, err := os.ReadDir(ns); err != nil || len(es) != 0 {
				t.Errorf("%s: namespace not retained empty: %v %d", name, err, len(es))
			}
			if f.starts() != 0 {
				t.Errorf("%s: starts %d", name, f.starts())
			}
		}
	}
}

// Identified state safety errors found by the acquisition rechecks of the
// state root and namespace before the exclusive ID mkdir are retained rather
// than collapsed into review_storage_unavailable (TestReviewStageFaults keeps
// injected generic faults at the same stages as storage failures). Each row
// really changes the state at one named boundary, and the change is
// confirmed independently with Lstat after the run. Safety precedes
// identity: a replaced root that is also 0755 is state_permissions.
func TestReviewAcquisitionSafetyRechecks(t *testing.T) {
	defer func() { hook, opened = nil, nil }()
	type row struct {
		name, stage, want string
		existingNS        bool
		change            func(t *testing.T, f *fx)
		fact              func(f *fx) bool
	}
	ns := func(f *fx) string { return filepath.Join(f.state, Namespace) }
	mode := func(p string, perm os.FileMode) bool {
		fi, err := os.Lstat(p)
		return err == nil && fi.IsDir() && fi.Mode().Perm() == perm
	}
	symlink := func(p string) bool { fi, err := os.Lstat(p); return err == nil && fi.Mode()&os.ModeSymlink != 0 }
	replaced := func(p string) bool {
		a, err1 := os.Lstat(p)
		b, err2 := os.Lstat(p + ".orig")
		return err1 == nil && err2 == nil && a.IsDir() && b.IsDir() && !os.SameFile(a, b)
	}
	replace := func(t *testing.T, p string, perm os.FileMode) {
		if err := os.Rename(p, p+".orig"); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		os.Chmod(p, perm)
	}
	toSymlink := func(t *testing.T, p string) {
		if err := os.Rename(p, p+".real"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(p+".real", p); err != nil {
			t.Fatal(err)
		}
	}
	rows := []row{
		{"root 0755 after namespace creation", "ns-chmod", "state_permissions", false,
			func(t *testing.T, f *fx) { os.Chmod(f.state, 0o755) }, func(f *fx) bool { return mode(f.state, 0o755) }},
		{"root 0755 with existing namespace", "ns-mkdir", "state_permissions", true,
			func(t *testing.T, f *fx) { os.Chmod(f.state, 0o755) }, func(f *fx) bool { return mode(f.state, 0o755) }},
		{"root symlink", "ns-chmod", "unsafe_state_path", false,
			func(t *testing.T, f *fx) { toSymlink(t, f.state) }, func(f *fx) bool { return symlink(f.state) }},
		{"root 0755 on the opened descriptor", "root-open", "state_permissions", false,
			func(t *testing.T, f *fx) { os.Chmod(f.state, 0o755) }, func(f *fx) bool { return mode(f.state, 0o755) }},
		{"root replaced before open", "root-lstat", "state_changed", false,
			func(t *testing.T, f *fx) { replace(t, f.state, 0o700) },
			func(f *fx) bool { return replaced(f.state) && mode(f.state, 0o700) }},
		{"root replaced 0755: safety before identity", "root-lstat", "state_permissions", false,
			func(t *testing.T, f *fx) { replace(t, f.state, 0o755) },
			func(f *fx) bool { return replaced(f.state) && mode(f.state, 0o755) }},
		{"namespace 0755", "ns-open", "state_permissions", false,
			func(t *testing.T, f *fx) { os.Chmod(ns(f), 0o755) }, func(f *fx) bool { return mode(ns(f), 0o755) }},
		{"namespace symlink", "ns-open", "unsafe_state_path", false,
			func(t *testing.T, f *fx) { toSymlink(t, ns(f)) }, func(f *fx) bool { return symlink(ns(f)) }},
		{"namespace removed", "ns-open", "state_changed", false,
			func(t *testing.T, f *fx) {
				if err := os.Rename(ns(f), ns(f)+".orig"); err != nil {
					t.Fatal(err)
				}
			},
			func(f *fx) bool { _, err := os.Lstat(ns(f)); return os.IsNotExist(err) && mode(ns(f)+".orig", 0o700) }},
	}
	for _, format := range []string{"sha1", "sha256"} {
		for _, r := range rows {
			name := format + " " + r.name
			f := newFx(t, format, "pass")
			if r.existingNS {
				mkdir0700(t, ns(f))
			}
			if !mode(f.state, 0o700) {
				t.Fatalf("%s: root not 0700 before the run", name)
			}
			hits := 0
			var mine, openAtHit []*os.File
			hook = func(stage string) error {
				if stage == r.stage {
					hits++
					for _, d := range mine {
						if _, err := d.Stat(); err == nil {
							openAtHit = append(openAtHit, d)
						}
					}
					r.change(t, f)
				}
				return nil
			}
			d := &descriptors{}
			opened = func(x *os.File) { d.add(x); mine = append(mine, x) }
			var outb bytes.Buffer
			passed, err := Review(context.Background(), f.req(f.head, true), &outb)
			hook, opened = nil, nil
			d.assertClosed(t, name)
			if err == nil || err.Error() != r.want || outb.Len() != 0 || passed || hits != 1 {
				t.Errorf("%s: %v out %q hits %d want %s", name, err, outb.String(), hits, r.want)
			}
			if !r.fact(f) {
				t.Errorf("%s: state change not independently observed", name)
			}
			if (r.stage == "root-open") != (len(openAtHit) == 1) {
				t.Errorf("%s: %d descriptors open at %s", name, len(openAtHit), r.stage)
			}
			for _, x := range openAtHit {
				if _, err := x.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Errorf("%s: descriptor open at the change was not closed", name)
				}
			}
			for _, p := range []string{ns(f), ns(f) + ".orig", ns(f) + ".real", filepath.Join(f.state+".orig", Namespace),
				filepath.Join(f.state+".real", Namespace)} {
				if _, e := os.Lstat(filepath.Join(p, testID)); e == nil {
					t.Errorf("%s: owned ID under %s", name, p)
				}
			}
			if f.starts() != 0 {
				t.Errorf("%s: starts %d", name, f.starts())
			}
		}
	}
}

// A read fault and a distinct close fault on the same plan or receipt: the
// earlier read error wins, both seams are reached exactly once (the close
// really ran), and the descriptor held at the read fault is actually closed.
// Under C2 the read and close faults of one file map to the same code, so
// the hit counts and closure, not the code, show both faults occurred.
func TestReviewSafeReadReadAndCloseFaults(t *testing.T) {
	defer func() { readHook, opened = nil, nil }()
	for _, c := range []struct{ name, want string }{
		{"plan", "plan_unavailable"}, {"intent", "execution_receipt_unavailable"}, {"result", "execution_receipt_unavailable"},
	} {
		f := newFx(t, "sha1", "pass")
		target := map[string]string{"plan": f.plan, "intent": filepath.Join(f.execDir(), "intent.json"),
			"result": filepath.Join(f.execDir(), "result.json")}[c.name]
		hits := map[string]int{}
		var mine, held []*os.File
		readHook = func(stage string) error {
			hits[stage]++
			switch stage {
			case c.name + "-read":
				for _, x := range mine {
					if _, err := x.Stat(); err == nil {
						held = append(held, x)
					}
				}
				return errors.New("injected read fault")
			case c.name + "-close":
				return errors.New("injected distinct close fault")
			}
			return nil
		}
		before := snapshot(t, f.state)
		d := &descriptors{}
		opened = func(x *os.File) { d.add(x); mine = append(mine, x) }
		passed, err := Review(context.Background(), f.req(f.head, true), &strings.Builder{})
		opened, readHook = nil, nil
		d.assertClosed(t, c.name)
		if err == nil || err.Error() != c.want || passed {
			t.Errorf("%s: %v want %s", c.name, err, c.want)
		}
		for _, s := range []string{"-open", "-read", "-close"} {
			if hits[c.name+s] != 1 {
				t.Errorf("%s: %s hit %d times", c.name, c.name+s, hits[c.name+s])
			}
		}
		if len(held) != 1 || held[0].Name() != target {
			t.Errorf("%s: %d descriptors open at the read fault", c.name, len(held))
		}
		for _, x := range held {
			if _, err := x.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("%s: descriptor not closed after read and close faults", c.name)
			}
		}
		if f.starts() != 0 || snapshot(t, f.state) != before {
			t.Errorf("%s: started or state changed", c.name)
		}
	}
}

// Reports spelled with genuine JSON escapes. The fixture bytes contain a
// backslash-u escape and decode to the literal member names and values; a
// single escaped spelling is accepted, an escaped duplicate is refused, in
// both object formats and output modes. Direct parser controls show the
// duplicate refusal independently, including at a nested level where the
// reviewer-run nested report is also invalid for its unknown key.
func TestReviewEscapedReports(t *testing.T) {
	keys := func(s string) ([]string, map[string]any) {
		dec := json.NewDecoder(strings.NewReader(s))
		var ks []string
		vals := map[string]any{}
		dec.Token()
		for dec.More() {
			k, _ := dec.Token()
			var v any
			dec.Decode(&v)
			ks = append(ks, k.(string))
			vals[k.(string)] = v
		}
		return ks, vals
	}
	for _, c := range []struct {
		raw, keys, verdict string
	}{
		{reportDupEscaped, "schema_version,verdict,verdict", "PASS"},
		{reportDupEscapedVersion, "schema_version,verdict,schema_version", "PASS"},
		{reportDupNestedEscaped, "schema_version,verdict,x", "PASS"},
		{reportEscapedKeys, "schema_version,verdict", "PASS"},
		{reportEscapedValue, "schema_version,verdict", "REQUIRED_FIXES"},
	} {
		ks, vals := keys(c.raw)
		if len(esc) != 2 || esc[0] != 0x5c || esc[1] != 'u' || !strings.Contains(c.raw, esc+"00") ||
			strings.Join(ks, ",") != c.keys || vals["verdict"] != c.verdict {
			t.Errorf("fixture %s decodes to %v %v", c.raw, ks, vals["verdict"])
		}
	}
	if !strings.Contains(reportEscapedKeys, `"verdic`+esc+`0074"`) || strings.Contains(reportEscapedKeys, `"verdict"`) ||
		!strings.Contains(reportEscapedValue, "REQUIRED"+esc+"005fFIXES") || strings.Contains(reportEscapedValue, "REQUIRED_FIXES") {
		t.Error("accepted controls are not escaped spellings")
	}
	for _, c := range []struct {
		data, verdict string
		ok            bool
	}{
		{reportEscapedKeys, "PASS", true},
		{reportEscapedValue, "REQUIRED_FIXES", true},
		{`{"schema_version":1,"verdict":"P` + esc + `0041SS"}`, "PASS", true},
		{reportDupEscaped, "", false},
		{reportDupEscapedVersion, "", false},
		{`{"schema_version":1,"verdic` + esc + `0074":"PASS","verdict":"REQUIRED_FIXES"}`, "", false},
	} {
		if v, ok := ParseReport([]byte(c.data)); ok != c.ok || v != c.verdict {
			t.Errorf("ParseReport(%s) = %q %v", c.data, v, ok)
		}
	}
	if _, ok := strictObject([]byte(`{"x":{"a":1,"` + esc + `0061":2}}`)); ok {
		t.Error("nested escaped duplicate accepted")
	}
	if _, ok := strictObject([]byte(`{"x":{"a":1,"` + esc + `0062":2}}`)); !ok {
		t.Error("nested single escaped member refused")
	}
	for _, format := range []string{"sha1", "sha256"} {
		for _, c := range []struct{ mode, outcome, verdict, after string }{
			{"escaped-keys", "review_passed", "PASS", "head"},
			{"escaped-value", "review_required_fixes", "REQUIRED_FIXES", "head"},
			{"dup-escaped", "reviewer_unverified", "null", "null"},
			{"dup-escaped-version", "reviewer_unverified", "null", "null"},
		} {
			for _, asJSON := range []bool{true, false} {
				f := newFx(t, format, c.mode)
				after := c.after
				if after == "head" {
					after = f.head
				}
				out, passed, err := f.run(context.Background(), f.req(f.head, asJSON))
				wantJ := wantResultJSON(c.outcome, "exited", "0", c.verdict, format, f.head, after)
				want := wantJ
				if !asJSON {
					want = wantText(c.outcome, "exited", "0", c.verdict, format, f.head, after)
				}
				if err != nil || out != want || passed != (c.outcome == "review_passed") {
					t.Errorf("%s %s json=%v: %v\n got %q\nwant %q", format, c.mode, asJSON, err, out, want)
				}
				if got := string(readFile(t, filepath.Join(f.attempt(), "result.json"))); got != wantJ {
					t.Errorf("%s %s: result.json %q", format, c.mode, got)
				}
				if f.starts() != 1 {
					t.Errorf("%s %s: starts %d", format, c.mode, f.starts())
				}
			}
		}
	}
}
