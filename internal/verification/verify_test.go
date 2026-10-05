package verification

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// checkKept checks the attempt layout: verifier/home and verifier/tmp 0700,
// intent (and result) final names sharing an inode with retained staging
// names, each 0600 with link count 2, and the saved bytes.
func checkKept(t *testing.T, f *fx, intent, result string) {
	t.Helper()
	want := "intent.json,.pending-intent-X,verifier"
	if result != "" {
		want = "intent.json,.pending-intent-X,.pending-result-X,result.json,verifier"
	}
	got := listing(t, f.attempt())
	sorted := strings.Split(want, ",")
	if got != strings.Join(sortStrings(sorted), ",") {
		t.Fatalf("attempt listing %q want %q", got, want)
	}
	for _, d := range []string{"", "verifier", "verifier/home", "verifier/tmp"} {
		fi, err := os.Lstat(filepath.Join(f.attempt(), d))
		if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
			t.Errorf("dir %q: %v %v", d, fi, err)
		}
	}
	es, _ := os.ReadDir(f.attempt())
	for name, data := range map[string]string{"intent": intent, "result": result} {
		if data == "" {
			continue
		}
		final := filepath.Join(f.attempt(), name+".json")
		if got := string(readFile(t, final)); got != data {
			t.Errorf("saved %s:\n%s\nwant:\n%s", name, got, data)
		}
		ffi, _ := os.Lstat(final)
		for _, e := range es {
			if strings.HasPrefix(e.Name(), ".pending-"+name+"-") {
				sfi, _ := os.Lstat(filepath.Join(f.attempt(), e.Name()))
				if !os.SameFile(ffi, sfi) {
					t.Errorf("%s staging is not the same inode", name)
				}
			}
		}
		st := ffi.Sys().(*syscall.Stat_t)
		if ffi.Mode().Perm() != 0o600 || st.Nlink != 2 {
			t.Errorf("%s: mode %v links %d", name, ffi.Mode(), st.Nlink)
		}
	}
}

func sortStrings(s []string) []string {
	for i := range s {
		for j := i + 1; j < len(s); j++ {
			if s[j] < s[i] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}

func TestVerifyPassedBothFormatsJSONAndText(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, asJSON := range []bool{true, false} {
			f := newFx(t, format, "pass-prose")
			out, passed, err := f.run(context.Background(), f.req(f.head, asJSON))
			if err != nil || !passed {
				t.Fatalf("%s: %v %v", format, err, passed)
			}
			res := wantResultJSON("candidate_verification_passed", "exited", "0", format, f.head, f.head)
			want := res
			if !asJSON {
				want = wantText("candidate_verification_passed", "exited", "0", format, f.head, f.head)
			}
			if out != want {
				t.Errorf("%s json=%v output:\n%s\nwant:\n%s", format, asJSON, out, want)
			}
			if !asJSON && strings.Count(out, "\n") != 8 {
				t.Errorf("text lines %d", strings.Count(out, "\n"))
			}
			checkKept(t, f, f.wantIntent(f.head), res)
			if f.starts() != 1 {
				t.Errorf("starts %d", f.starts())
			}
			for _, p := range []string{filepath.Join(f.attempt(), "intent.json"), filepath.Join(f.attempt(), "result.json")} {
				if strings.Contains(string(readFile(t, p)), dummySecret) {
					t.Error("verifier output saved")
				}
			}
			if strings.Contains(out, dummySecret) || strings.Contains(out, f.repo) || strings.Contains(out, f.plan) {
				t.Error("output leaks program output or paths")
			}
		}
	}
}

// A descendant candidate of the record HEAD is eligible; the intent keeps the
// record HEAD separately from the candidate.
func TestVerifyDescendantCandidate(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	os.WriteFile(filepath.Join(f.repo, "later.txt"), []byte("later\n"), 0o644)
	f.git("add", "later.txt")
	f.git("commit", "-q", "-m", "later")
	cand := strings.TrimSpace(f.git("rev-parse", "HEAD"))
	out, passed, err := f.run(context.Background(), f.req(cand, true))
	if err != nil || !passed || out != wantResultJSON("candidate_verification_passed", "exited", "0", "sha1", cand, cand) {
		t.Fatalf("%v %v %s", err, passed, out)
	}
	if got := string(readFile(t, filepath.Join(f.attempt(), "intent.json"))); got != f.wantIntent(cand) {
		t.Errorf("intent %s", got)
	}
}

func TestVerifyProgramRows(t *testing.T) {
	cases := []struct {
		mode, outcome, state, code, after string
		timeout                           int
	}{
		{"pass-nonzero", OutcomeFailed, "exited", "3", "null", 20},
		{"exit255", OutcomeFailed, "exited", "255", "null", 20},
		{"signal", OutcomeUnverified, "unverified", "null", "null", 20},
		{"sleep", OutcomeUnverified, "unverified", "null", "null", 1},
		{"stdout-65537", OutcomeUnverified, "unverified", "null", "null", 20},
		{"stderr-65537", OutcomeUnverified, "unverified", "null", "null", 20},
		{"hold-pipe", OutcomeUnverified, "unverified", "null", "null", 20},
		{"stdout-65536", OutcomePassed, "exited", "0", "HEAD", 20},
		{"stderr-65536", OutcomePassed, "exited", "0", "HEAD", 20},
		{"env", OutcomePassed, "exited", "0", "HEAD", 20},
		{"mutate-tracked", OutcomeChanged, "exited", "0", "null", 20},
		{"mutate-index", OutcomeChanged, "exited", "0", "null", 20},
		{"mutate-untracked", OutcomeChanged, "exited", "0", "null", 20},
		{"mutate-commit", OutcomeChanged, "exited", "0", "null", 20},
		// Documented limits: transient edit-and-restore and ignored files are
		// not observed, so these pass.
		{"mutate-transient", OutcomePassed, "exited", "0", "HEAD", 20},
		{"mutate-ignored", OutcomePassed, "exited", "0", "HEAD", 20},
	}
	for _, c := range cases {
		for _, format := range []string{"sha1", "sha256"} {
			f := newFx(t, format, c.mode)
			f.writePlan(c.mode, c.timeout)
			after := c.after
			if after == "HEAD" {
				after = f.head
			}
			out, passed, err := f.run(context.Background(), f.req(f.head, true))
			want := wantResultJSON(c.outcome, c.state, c.code, format, f.head, after)
			if err != nil || out != want || passed != (c.outcome == OutcomePassed) {
				t.Errorf("%s %s: err %v passed %v\n%s\nwant\n%s", c.mode, format, err, passed, out, want)
			}
			if f.starts() != 1 {
				t.Errorf("%s: starts %d", c.mode, f.starts())
			}
			checkKept(t, f, f.wantIntent(f.head), want)
		}
	}
}

// Each usability fact is required independently, regardless of exit status.
func TestVerifyObservationFactsSeam(t *testing.T) {
	good := proc.Observation{Started: true, Exited: true, Joined: true, GroupAbsent: true, StdoutEOF: true, StderrEOF: true}
	type mut struct {
		name string
		fn   func(*proc.Observation)
	}
	muts := []mut{
		{"exited", func(o *proc.Observation) { o.Exited = false }},
		{"joined", func(o *proc.Observation) { o.Joined = false }},
		{"group", func(o *proc.Observation) { o.GroupAbsent = false }},
		{"stdout-eof", func(o *proc.Observation) { o.StdoutEOF = false }},
		{"stderr-eof", func(o *proc.Observation) { o.StderrEOF = false }},
		{"signaled", func(o *proc.Observation) { o.Signaled = true }},
		{"watcher", func(o *proc.Observation) { o.WatcherFailed = true }},
		{"timeout", func(o *proc.Observation) { o.TimedOut = true }},
		{"cancelled", func(o *proc.Observation) { o.Cancelled = true }},
		{"output-limit", func(o *proc.Observation) { o.OutputLimit = true }},
	}
	defer func() { runner = nil }()
	f := newFx(t, "sha1", "ok")
	try := func(name string, o proc.Observation, runErr error, outcome, state, code, after string) {
		t.Helper()
		f2 := newFx(t, "sha1", "ok")
		runner = func(context.Context, proc.Spec) ([]byte, proc.Observation, error) { return []byte("PASS\n"), o, runErr }
		out, _, err := f2.run(context.Background(), f2.req(f2.head, true))
		if after == "HEAD" {
			after = f2.head
		}
		if want := wantResultJSON(outcome, state, code, "sha1", f2.head, after); err != nil || out != want {
			t.Errorf("%s: %v\n%s\nwant\n%s", name, err, out, want)
		}
	}
	_ = f
	try("all-usable", good, nil, OutcomePassed, "exited", "0", "HEAD")
	// A usable observation with a legacy start error is still usable: the
	// legacy error value is never consulted.
	try("usable-legacy-error", good, proc.ErrStart, OutcomePassed, "exited", "0", "HEAD")
	try("not-started", proc.Observation{}, nil, OutcomeUnverified, "not_started", "null", "null")
	for _, m := range muts {
		o := good
		m.fn(&o)
		try(m.name, o, nil, OutcomeUnverified, "unverified", "null", "null")
		n := o
		n.ExitCode = 4
		try(m.name+"-nonzero", n, nil, OutcomeUnverified, "unverified", "null", "null")
	}
	n := good
	n.ExitCode = 4
	try("usable-nonzero", n, nil, OutcomeFailed, "exited", "4", "null")
	// All five vetoes false and all six positives true are required; check
	// each positive alone set and each veto alone clear on an otherwise
	// failing observation is still unusable.
	for _, m := range muts {
		o := proc.Observation{Started: true}
		m.fn(&o)
		if o.Usable() {
			t.Errorf("%s alone usable", m.name)
		}
	}
}

func TestVerifyPlanParsing(t *testing.T) {
	esc := "\x5c" + "u"
	cmd := `{"executable":"/bin/true","arguments":[],"timeout_seconds":1}`
	valid := []string{
		`{"schema_version":1,"verifier":` + cmd + `}`,
		`{"schema_version":1,"verifier":` + cmd + "}\n \t\r\n",
		`{"schem` + esc + `0061_version":1,"verifie` + esc + `0072":` + cmd + `}`,
		`{"schema_version":1,"verifier":{"executabl` + esc + `0065":"/bin/true","arguments":[],"timeout_seconds":1}}`,
		`{"schema_version":1,"verifier":{"executable":"/bin/true","arguments":["a` + esc + `0062"],"timeout_seconds":300}}`,
	}
	for _, s := range valid {
		if _, err := ParsePlan([]byte(s)); err != nil {
			t.Errorf("valid %q: %v", s, err)
		}
	}
	invalid := []string{
		``, `[]`, `1`, `"x"`, `null`,
		`{"schema_version":1}`,
		`{"verifier":` + cmd + `}`,
		`{"schema_version":2,"verifier":` + cmd + `}`,
		`{"schema_version":1.0,"verifier":` + cmd + `}`,
		`{"schema_version":1e0,"verifier":` + cmd + `}`,
		`{"schema_version":"1","verifier":` + cmd + `}`,
		`{"schema_version":true,"verifier":` + cmd + `}`,
		`{"schema_version":1,"verifier":` + cmd + `,"extra":1}`,
		`{"schema_version":1,"verifier":` + cmd + `}x`,
		`{"schema_version":1,"verifier":` + cmd + `}{}`,
		`{"schema_version":1,"schema_version":1,"verifier":` + cmd + `}`,
		`{"schema_version":1,"verifier":` + cmd + `,"schem` + esc + `0061_version":1}`,
		`{"schema_version":1,"verifier":{"executable":"/bin/true","executabl` + esc + `0065":"/bin/true","arguments":[],"timeout_seconds":1}}`,
		`{"schema_version":1,"verifier":{"executable":"/bin/true","arguments":[],"timeout_seconds":1,"env":{}}}`,
		`{"schema_version":1,"verifier":{"executable":"true","arguments":[],"timeout_seconds":1}}`,
		`{"schema_version":1,"verifier":{"executable":"/bin/true","arguments":[],"timeout_seconds":0}}`,
		`{"schema_version":1,"verifier":{"executable":"/bin/true","arguments":[],"timeout_seconds":301}}`,
		`{"schema_version":1,"verifier":{"executable":"/bin/true","arguments":[],"timeout_seconds":1.0}}`,
		"{\"schema_version\":1,\"verifier\":" + cmd + ",\"\xff\":1}",
		`{"schema_version":1,"worker":` + cmd + `}`,
		`{"schema_version":1,"reviewer":` + cmd + `}`,
	}
	for _, s := range invalid {
		if _, err := ParsePlan([]byte(s)); !IsCode(err, CodeInvalidPlan) {
			t.Errorf("invalid %q: %v", s, err)
		}
	}
	base := `{"schema_version":1,"verifier":` + cmd + `}`
	at := base + strings.Repeat(" ", MaxPlanBytes-len(base))
	if _, err := ParsePlan([]byte(at)); err != nil || len(at) != 65536 {
		t.Errorf("65536 bytes: %v", err)
	}
	if _, err := ParsePlan([]byte(at + " ")); !IsCode(err, CodeInvalidPlan) {
		t.Errorf("65537 bytes: %v", err)
	}
	// File boundary through the safe reader.
	f := newFx(t, "sha1", "ok")
	os.Remove(f.plan)
	write0600(t, f.plan, []byte(at+" "))
	f.assertPre(t, "plan-65537", context.Background(), f.req(f.head, true), string(CodeInvalidPlan))
}

func TestVerifyPlanFileSafety(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	good := readFile(t, f.plan)
	req := f.req(f.head, true)
	os.Chmod(f.plan, 0o644)
	f.assertPre(t, "mode-0644", context.Background(), req, "state_permissions")
	os.Chmod(f.plan, 0o600)
	link := filepath.Join(f.base, "plan-link.json")
	os.Symlink(f.plan, link)
	r := req
	r.Plan = link
	f.assertPre(t, "symlink", context.Background(), r, "unsafe_state_path")
	r.Plan = filepath.Join(f.base, "missing.json")
	f.assertPre(t, "missing", context.Background(), r, "plan_unavailable")
	r.Plan = f.counter
	f.assertPre(t, "directory", context.Background(), r, "unsafe_state_path")
	_ = good
}

// Combined read/parse/close boundaries: the earlier read or parse failure
// wins over a simultaneously injected close failure, and a close-only failure
// is plan_unavailable. Each hook hit is counted and the descriptor closed.
func TestVerifyPlanReadParseClosePrecedence(t *testing.T) {
	defer func() { readHook = nil }()
	for _, c := range []struct {
		name, plan string
		fail       map[string]bool
		want       string
		hits       string
	}{
		{"read+close", "", map[string]bool{"plan-read": true, "plan-close": true}, "plan_unavailable", "plan-open,plan-read,plan-close"},
		{"parse+close", "bad", map[string]bool{"plan-close": true}, "invalid_verification_plan", "plan-open,plan-read,plan-close"},
		{"close-only", "", map[string]bool{"plan-close": true}, "plan_unavailable", "plan-open,plan-read,plan-close"},
		{"open", "", map[string]bool{"plan-open": true}, "plan_unavailable", "plan-open"},
	} {
		f := newFx(t, "sha1", "ok")
		if c.plan == "bad" {
			os.Remove(f.plan)
			write0600(t, f.plan, []byte(`{"schema_version":1}`))
		}
		var hits []string
		readHook = func(s string) error {
			hits = append(hits, s)
			if c.fail[s] {
				return errors.New(c.name + " " + s)
			}
			return nil
		}
		f.assertPre(t, c.name, context.Background(), f.req(f.head, true), c.want)
		if strings.Join(hits, ",") != c.hits {
			t.Errorf("%s hits %v", c.name, hits)
		}
		readHook = nil
	}
}

// The plan descriptor's identity is the original inode: replacement between
// Lstat and open is state_changed.
func TestVerifyPlanReplacedBeforeOpen(t *testing.T) {
	defer func() { readHook = nil }()
	f := newFx(t, "sha1", "ok")
	other := filepath.Join(f.base, "other.json")
	write0600(t, other, readFile(t, f.plan))
	readHook = func(s string) error {
		if s == "plan-open" {
			os.Rename(other, f.plan)
		}
		return nil
	}
	f.assertPre(t, "replaced", context.Background(), f.req(f.head, true), "state_changed")
}

func TestVerifyAdmissionCandidate(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	req := f.req(strings.Repeat("a", 40), true)
	f.assertPre(t, "mismatch", context.Background(), req, string(CodeCheckpointMismatch))
	f.assertPre(t, "wrong-width", context.Background(), f.req(strings.Repeat("a", 64), true), string(CodeUsage))
	os.WriteFile(filepath.Join(f.repo, "README.md"), []byte("dirty\n"), 0o644)
	f.assertPre(t, "dirty", context.Background(), f.req(f.head, true), string(CodeNotClean))
	f.git("checkout", "--", "README.md")
	os.WriteFile(filepath.Join(f.repo, "untracked.txt"), []byte("u\n"), 0o644)
	f.assertPre(t, "untracked", context.Background(), f.req(f.head, true), string(CodeNotClean))
	os.Remove(filepath.Join(f.repo, "untracked.txt"))
	// Missing record HEAD: the unchanged inspector error passes through.
	f.writeRecord(strings.Repeat("b", 40))
	before := snapshot(t, f.state)
	out, _, err := f.run(context.Background(), f.req(f.head, true))
	var ie *inspect.Error
	if !errors.As(err, &ie) || out != "" || f.starts() != 0 || snapshot(t, f.state) != before {
		t.Errorf("missing checkpoint: %v", err)
	}
	// Width-mismatched record HEAD surfaces as the inspector's invalid_usage.
	f.writeRecordFormat(strings.Repeat("b", 64), "sha256")
	before = snapshot(t, f.state)
	_, _, err = f.run(context.Background(), f.req(f.head, true))
	if !errors.As(err, &ie) || ie.Code != inspect.CodeUsage || snapshot(t, f.state) != before {
		t.Errorf("cross format: %v", err)
	}
}

func TestVerifyLayoutAndExecutable(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	// Plan inside the repository.
	inPlan := filepath.Join(f.repo, "plan.json")
	write0600(t, inPlan, readFile(t, f.plan))
	f.git("add", "plan.json")
	f.git("commit", "-q", "-m", "plan")
	head := strings.TrimSpace(f.git("rev-parse", "HEAD"))
	r := f.req(head, true)
	r.Plan = inPlan
	f.assertPre(t, "plan-in-repo", context.Background(), r, string(CodeUnsafeLayout))

	// State root inside the repository (ignored so the candidate stays clean).
	g := newFx(t, "sha1", "ok")
	os.WriteFile(filepath.Join(g.repo, ".gitignore"), []byte("*.log\nstate/\n"), 0o644)
	g.git("add", ".gitignore")
	g.git("commit", "-q", "-m", "ignore")
	h := strings.TrimSpace(g.git("rev-parse", "HEAD"))
	inner := filepath.Join(g.repo, "state")
	mkdir0700(t, inner)
	os.Rename(filepath.Join(g.state, "records-v1"), filepath.Join(inner, "records-v1"))
	r = g.req(h, true)
	r.StateDir = inner
	before := snapshot(t, inner)
	if _, _, err := g.run(context.Background(), r); !IsCode(err, CodeUnsafeLayout) || snapshot(t, inner) != before {
		t.Errorf("state in repo: %v", err)
	}

	// Executable unavailable.
	x := newFx(t, "sha1", "ok")
	os.Remove(x.plan)
	write0600(t, x.plan, []byte(`{"schema_version":1,"verifier":{"executable":"/nonexistent/v","arguments":[],"timeout_seconds":1}}`))
	x.assertPre(t, "exe", context.Background(), x.req(x.head, true), string(CodeExecUnavailable))
}

// Repository inside the state root is refused by verify only.
func TestVerifyRepoInsideStateRefused(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	inner := filepath.Join(f.state, "repo")
	if err := os.Rename(f.repo, inner); err != nil {
		t.Fatal(err)
	}
	r := f.req(f.head, true)
	r.Repo = inner
	f.assertPre(t, "repo-in-state", context.Background(), r, string(CodeUnsafeLayout))
}

// Existing ID directory is checked last: earlier invalid input wins, and a
// safe existing (even empty) directory is verification_exists.
func TestVerifyExistingIDLast(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	mkdir0700(t, filepath.Join(f.state, Namespace))
	mkdir0700(t, f.attempt())
	f.assertPre(t, "exists", context.Background(), f.req(f.head, true), string(CodeExists))
	f.assertPre(t, "earlier-wins", context.Background(), f.req(strings.Repeat("a", 40), true), string(CodeCheckpointMismatch))
	os.Chmod(f.attempt(), 0o755)
	f.assertPre(t, "perm", context.Background(), f.req(f.head, true), "state_permissions")
	os.Remove(f.attempt())
	os.WriteFile(f.attempt(), nil, 0o600)
	f.assertPre(t, "file", context.Background(), f.req(f.head, true), "unsafe_state_path")
	os.Remove(f.attempt())
	os.Symlink(f.base, f.attempt())
	f.assertPre(t, "symlink", context.Background(), f.req(f.head, true), "unsafe_state_path")
	os.Remove(f.attempt())
	os.Chmod(filepath.Join(f.state, Namespace), 0o750)
	f.assertPre(t, "namespace-perm", context.Background(), f.req(f.head, true), "state_permissions")
}

// Separate namespaces: an identical ID in execute-v1/review-v1 is untouched.
func TestVerifyNamespaceSeparation(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	for _, ns := range []string{"execute-v1", "review-v1"} {
		mkdir0700(t, filepath.Join(f.state, ns))
		mkdir0700(t, filepath.Join(f.state, ns, testID))
	}
	before := snapshot(t, filepath.Join(f.state, "execute-v1")) + snapshot(t, filepath.Join(f.state, "review-v1"))
	if _, passed, err := f.run(context.Background(), f.req(f.head, true)); err != nil || !passed {
		t.Fatal(err)
	}
	if snapshot(t, filepath.Join(f.state, "execute-v1"))+snapshot(t, filepath.Join(f.state, "review-v1")) != before {
		t.Error("other namespaces changed")
	}
}

// Every named storage boundary: before ID ownership the failure leaves no ID
// and starts nothing; after it the attempt is uncertain, retained, and no
// later program starts.
func TestVerifyStageFaults(t *testing.T) {
	pre := map[string]string{
		"ns-mkdir": "verification_storage_unavailable", "ns-chmod": "verification_storage_unavailable",
		"root-lstat": "verification_storage_unavailable", "root-open": "verification_storage_unavailable",
		"root-recheck": "verification_storage_unavailable", "root-sync": "durability_unavailable",
		"root-close": "verification_storage_unavailable", "ns-open": "verification_storage_unavailable",
		"ns-recheck": "verification_storage_unavailable", "id-mkdir": "verification_storage_unavailable",
	}
	post := []string{"id-chmod", "ns-sync", "ns-close", "id-open", "id-recheck",
		"verifier-mkdir", "verifier-chmod", "verifier-home-mkdir", "verifier-home-chmod", "verifier-home-open",
		"verifier-home-recheck", "verifier-home-sync", "verifier-home-close", "verifier-tmp-mkdir", "verifier-tmp-chmod",
		"verifier-tmp-open", "verifier-tmp-recheck", "verifier-tmp-sync", "verifier-tmp-close", "verifier-open",
		"verifier-recheck", "verifier-sync", "verifier-close", "id-sync", "id-close",
		"intent-random", "intent-create", "intent-chmod", "intent-write", "intent-sync", "intent-close", "intent-link",
		"intent-dir-open", "intent-dir-recheck", "intent-dir-sync", "intent-dir-close"}
	postStarted := []string{"output-limit", "result-random", "result-create", "result-chmod", "result-write",
		"result-sync", "result-close", "result-link", "result-dir-open", "result-dir-recheck", "result-dir-sync",
		"result-dir-close", "deliver"}
	defer func() { hook = nil }()
	try := func(stage, want string, starts int, owned bool) {
		t.Helper()
		f := newFx(t, "sha1", "ok")
		hits := 0
		hook = func(s string) error {
			if s == stage {
				hits++
				return errors.New("injected " + s)
			}
			return nil
		}
		out, passed, err := f.run(context.Background(), f.req(f.head, true))
		hook = nil
		if err == nil || err.Error() != want || passed || out != "" {
			t.Errorf("%s: err %v out %q want %s", stage, err, out, want)
		}
		if hits != 1 {
			t.Errorf("%s: %d hits", stage, hits)
		}
		if f.starts() != starts {
			t.Errorf("%s: %d starts want %d", stage, f.starts(), starts)
		}
		_, serr := os.Lstat(f.attempt())
		if owned != (serr == nil) {
			t.Errorf("%s: owned ID %v", stage, serr == nil)
		}
	}
	for stage, want := range pre {
		try(stage, want, 0, false)
	}
	for _, stage := range post {
		try(stage, "verification_uncertain", 0, true)
	}
	for _, stage := range postStarted {
		try(stage, "verification_uncertain", 1, true)
	}
	// Failing before start is still recorded not_started once intent is durable.
	f := newFx(t, "sha1", "ok")
	hook = func(s string) error {
		if s == "verifier-start" {
			return errors.New("x")
		}
		return nil
	}
	out, _, err := f.run(context.Background(), f.req(f.head, true))
	hook = nil
	if want := wantResultJSON(OutcomeUnverified, "not_started", "null", "sha1", f.head, "null"); err != nil || out != want || f.starts() != 0 {
		t.Errorf("verifier-start: %v %s", err, out)
	}
}

// A root Sync error and a simultaneous close error keep the earlier
// durability error.
func TestVerifyRootSyncAndCloseKeepEarlierError(t *testing.T) {
	defer func() { hook = nil }()
	f := newFx(t, "sha1", "ok")
	hook = func(s string) error {
		if s == "root-sync" || s == "root-close" {
			return errors.New(s)
		}
		return nil
	}
	f.assertPre2(t, "durability_unavailable")
}

func (f *fx) assertPre2(t *testing.T, want string) {
	t.Helper()
	out, _, err := f.run(context.Background(), f.req(f.head, true))
	if err == nil || err.Error() != want || out != "" || f.starts() != 0 {
		t.Errorf("err %v want %s", err, want)
	}
	if _, err := os.Lstat(f.attempt()); err == nil {
		t.Error("ID owned")
	}
}

// Cancellation at each pre-ID boundary, including immediately before the
// exclusive ID mkdir after its test boundary, leaves no ID and starts nothing.
func TestVerifyCancellationBoundaries(t *testing.T) {
	defer func() { hook = nil; readHook = nil; inspectTop = inspect.InspectTop }()
	for _, stage := range []string{"ns-mkdir", "root-lstat", "root-sync", "ns-open", "id-mkdir"} {
		f := newFx(t, "sha1", "ok")
		ctx, cancel := context.WithCancel(context.Background())
		hook = func(s string) error {
			if s == stage {
				cancel()
			}
			return nil
		}
		out, _, err := f.run(ctx, f.req(f.head, true))
		hook = nil
		if !IsCode(err, CodeCancelled) || out != "" || f.starts() != 0 {
			t.Errorf("%s: %v", stage, err)
		}
		if _, err := os.Lstat(f.attempt()); err == nil {
			t.Errorf("%s: ID owned", stage)
		}
	}
	// Admission stages: cancellation after the plan read is observed.
	f := newFx(t, "sha1", "ok")
	ctx, cancel := context.WithCancel(context.Background())
	readHook = func(s string) error {
		if s == "plan-close" {
			cancel()
		}
		return nil
	}
	f.assertPre(t, "after-plan", ctx, f.req(f.head, true), string(CodeCancelled))
	readHook = nil
	// An already observed stage error wins over cancellation.
	g := newFx(t, "sha1", "ok")
	ctx, cancel = context.WithCancel(context.Background())
	readHook = func(s string) error {
		if s == "plan-read" {
			cancel()
			return errors.New("read")
		}
		return nil
	}
	g.assertPre(t, "error-wins", ctx, g.req(g.head, true), string(CodePlanUnavailable))
	readHook = nil
	// After ID ownership but before durable intent: uncertain, retained.
	h := newFx(t, "sha1", "ok")
	ctx, cancel = context.WithCancel(context.Background())
	hook = func(s string) error {
		if s == "id-close" {
			cancel()
		}
		return nil
	}
	out, _, err := h.run(ctx, h.req(h.head, true))
	hook = nil
	if !IsCode(err, CodeUncertain) || out != "" || h.starts() != 0 {
		t.Errorf("before intent: %v", err)
	}
	// After intent, before start: recorded not_started.
	for _, c := range []struct{ stage, state string }{{"intent-dir-close", "not_started"}, {"classification", "unverified"}} {
		k := newFx(t, "sha1", "ok")
		ctx, cancel = context.WithCancel(context.Background())
		hook = func(s string) error {
			if s == c.stage {
				cancel()
			}
			return nil
		}
		out, _, err = k.run(ctx, k.req(k.head, true))
		hook = nil
		if want := wantResultJSON(OutcomeUnverified, c.state, "null", "sha1", k.head, "null"); err != nil || out != want {
			t.Errorf("%s: %v %s", c.stage, err, out)
		}
	}
	// Cancellation during post-inspection overrides its result.
	k := newFx(t, "sha1", "ok")
	ctx, cancel = context.WithCancel(context.Background())
	calls := 0
	inspectTop = func(o inspect.Options) (*inspect.Packet, string, error) {
		calls++
		if calls == 2 {
			cancel()
		}
		return inspect.InspectTop(o)
	}
	out, _, err = k.run(ctx, k.req(k.head, true))
	inspectTop = inspect.InspectTop
	if want := wantResultJSON(OutcomeUnverified, "unverified", "null", "sha1", k.head, "null"); err != nil || out != want {
		t.Errorf("post-inspection: %v %s", err, out)
	}
	// Cancellation after classification does not change the outcome.
	m := newFx(t, "sha1", "ok")
	ctx, cancel = context.WithCancel(context.Background())
	hook = func(s string) error {
		if s == "output-limit" {
			cancel()
		}
		return nil
	}
	out, passed, err := m.run(ctx, m.req(m.head, true))
	hook = nil
	if !passed || err != nil || out != wantResultJSON(OutcomePassed, "exited", "0", "sha1", m.head, m.head) {
		t.Errorf("late: %v %s", err, out)
	}
}

// Same-ID concurrency: exactly one winner and one program start.
func TestVerifySameIDRace(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	var mu sync.Mutex
	now = fixedTime
	defer func() { now = timeNow }()
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var b strings.Builder
			_, err := Verify(context.Background(), f.req(f.head, true), &b)
			mu.Lock()
			errs[i] = err
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case IsCode(err, CodeExists):
		default:
			t.Errorf("race err %v", err)
		}
	}
	if wins != 1 || f.starts() != 1 {
		t.Errorf("wins %d starts %d", wins, f.starts())
	}
}

// Output boundaries: short and failing writers after publication are
// uncertain with the result saved.
type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) / 2, nil }

type errWriter struct{}

func (errWriter) Write(b []byte) (int, error) { return 0, errors.New("w") }

func TestVerifyDeliveryFailures(t *testing.T) {
	for _, w := range []interface {
		Write([]byte) (int, error)
	}{shortWriter{}, errWriter{}} {
		f := newFx(t, "sha1", "ok")
		now = fixedTime
		_, err := Verify(context.Background(), f.req(f.head, true), w)
		now = timeNow
		if !IsCode(err, CodeUncertain) {
			t.Errorf("writer: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err != nil {
			t.Error("result not saved")
		}
	}
}

// The strict validators reject every contradictory or malformed packet.
func TestVerifyPacketValidators(t *testing.T) {
	h := strings.Repeat("a", 40)
	good := wantResultJSON(OutcomePassed, "exited", "0", "sha1", h, h)
	if !ValidResult([]byte(good), testID) {
		t.Fatal("good result rejected")
	}
	bad := map[string]string{
		"failed-zero":       wantResultJSON(OutcomeFailed, "exited", "0", "sha1", h, "null"),
		"failed-after":      wantResultJSON(OutcomeFailed, "exited", "3", "sha1", h, h),
		"passed-no-after":   wantResultJSON(OutcomePassed, "exited", "0", "sha1", h, "null"),
		"passed-nonzero":    wantResultJSON(OutcomePassed, "exited", "1", "sha1", h, h),
		"changed-after":     wantResultJSON(OutcomeChanged, "exited", "0", "sha1", h, h),
		"changed-nonzero":   wantResultJSON(OutcomeChanged, "exited", "2", "sha1", h, "null"),
		"unverified-exited": wantResultJSON(OutcomeUnverified, "exited", "0", "sha1", h, "null"),
		"unverified-code":   wantResultJSON(OutcomeUnverified, "unverified", "1", "sha1", h, "null"),
		"exit-256":          wantResultJSON(OutcomeFailed, "exited", "256", "sha1", h, "null"),
		"exit-01":           wantResultJSON(OutcomeFailed, "exited", "01", "sha1", h, "null"),
		"exit-float":        wantResultJSON(OutcomeFailed, "exited", "1.0", "sha1", h, "null"),
		"after-other":       wantResultJSON(OutcomePassed, "exited", "0", "sha1", h, strings.Repeat("b", 40)),
		"width":             wantResultJSON(OutcomePassed, "exited", "0", "sha256", h, h),
		"format":            wantResultJSON(OutcomePassed, "exited", "0", "sha3", h, h),
		"unknown-outcome":   wantResultJSON("review_passed", "exited", "0", "sha1", h, h),
		"upper":             wantResultJSON(OutcomePassed, "exited", "0", "sha1", strings.ToUpper(h), strings.ToUpper(h)),
		"extra":             strings.Replace(good, `"receipt_state"`, `"x":1,"receipt_state"`, 1),
		"dup":               strings.Replace(good, `"receipt_state":"recorded"`, `"receipt_state":"recorded","receipt_state":"recorded"`, 1),
		"version":           strings.Replace(good, `"schema_version":1`, `"schema_version":1.0`, 1),
		"op":                strings.Replace(good, "run_verify", "run_review", 1),
		"reversed":          strings.Replace(good, `"completed_at":"`+fixedNow, `"completed_at":"2026-10-04T11:59:59Z`, 1),
		"impossible":        strings.Replace(good, `"completed_at":"`+fixedNow, `"completed_at":"2026-02-30T12:00:00Z`, 1),
		"fractional":        strings.Replace(good, `"completed_at":"`+fixedNow, `"completed_at":"2026-10-04T12:00:00.5Z`, 1),
		"verification-key":  strings.Replace(good, `"exit_code":0}`, `"exit_code":0,"x":1}`, 1),
		"repository-key":    strings.Replace(good, `"after_head":"`+h+`"}`, `"after_head":"`+h+`","x":1}`, 1),
		"run-id":            strings.Replace(good, testID, strings.Repeat("f", 32), 1),
	}
	for name, s := range bad {
		if ValidResult([]byte(s), testID) {
			t.Errorf("%s accepted", name)
		}
	}
	f := &fx{t: t, format: "sha1", head: h, plan: filepath.Join(t.TempDir(), "p")}
	os.WriteFile(f.plan, []byte("x"), 0o600)
	intent := f.wantIntent(h)
	if !ValidIntent([]byte(intent), testID) {
		t.Fatal("good intent rejected")
	}
	for name, s := range map[string]string{
		"extra":     strings.Replace(intent, `"readiness"`, `"x":1,"readiness"`, 1),
		"missing":   strings.Replace(intent, `,"readiness":"not_evaluated"`, ``, 1),
		"readiness": strings.Replace(intent, `"readiness":"not_evaluated"`, `"readiness":"ready"`, 1),
		"state":     strings.Replace(intent, "verification_intent", "review_intent", 1),
		"width":     strings.Replace(intent, `"candidate_head":"`+h, `"candidate_head":"`+h+"aa", 1),
		"time":      strings.Replace(intent, fixedNow, "2026-10-04T12:00:00+00:00", 1),
	} {
		if ValidIntent([]byte(s), testID) {
			t.Errorf("intent %s accepted", name)
		}
	}
}

// Special files: FIFO and socket plans are refused before any open that could
// block; the actual default metadata is observed, not invented.
func TestVerifySpecialPlanFiles(t *testing.T) {
	f := newFx(t, "sha1", "ok")
	fifo := filepath.Join(f.base, "fifo.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	r := f.req(f.head, true)
	r.Plan = fifo
	f.assertPre(t, "fifo", context.Background(), r, "unsafe_state_path")
	fi, _ := os.Lstat(fifo)
	if fi.Mode()&fs.ModeNamedPipe == 0 {
		t.Error("fixture is not a FIFO")
	}
	_ = state.TimeLayout
}
