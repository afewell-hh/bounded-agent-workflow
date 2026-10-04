package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

const (
	testID     = "0123456789abcdef0123456789abcdef"
	testTicket = "https://github.com/afewell-hh/bounded-agent-workflow/issues/18"
	testScope  = "abababababababababababababababababababababababababababababababab"
)

type fx struct {
	t                                   *testing.T
	base, home, repo, root, plan, count string
	format, head, exe, vexe             string
}

func newFx(t *testing.T, format string) *fx {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fx{t: t, base: base, home: filepath.Join(base, "home"), repo: filepath.Join(base, "repo"),
		root: filepath.Join(base, "state"), plan: filepath.Join(base, "plan.json"), count: filepath.Join(base, "count"), format: format}
	os.Mkdir(f.home, 0o700)
	os.Mkdir(f.count, 0o700)
	if err := tf.Init(f.home, f.repo, format); err != nil {
		t.Fatalf("%s fixture mandatory: %v", format, err)
	}
	tf.Write(filepath.Join(f.repo, "dummy.txt"), "base\n")
	if f.head, err = tf.CommitSources(f.home, f.repo); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(f.root, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(f.root, 0o700)
	f.record(f.head)
	if f.exe, err = os.Executable(); err != nil {
		t.Fatal(err)
	}
	f.setPlan("worker-ok", "verifier-check")
	return f
}

func (f *fx) record(head string) {
	policy := strings.Repeat("5", len(head))
	data, err := state.Marshal(state.Record{SchemaVersion: 1, RunID: testID, RecordState: "recorded", TicketURL: testTicket,
		ScopeSHA256: testScope, PolicyCommit: policy, RepositoryObjectFormat: f.format, RepositoryHead: head, CreatedAt: "2026-10-03T00:00:00Z"})
	if err != nil {
		f.t.Fatal(err)
	}
	r, err := state.OpenRoot(f.root)
	if err != nil {
		f.t.Fatal(err)
	}
	if err := r.Create(testID, data, func() error { return nil }); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) planText(worker, verifier string, wt, vt int) string {
	git, _ := exec.LookPath("git")
	cmd := func(exe, mode string, timeout int) map[string]any {
		return map[string]any{"executable": exe, "arguments": []string{"-baw-fake", mode, f.count, f.repo, git}, "timeout_seconds": timeout}
	}
	vexe := f.exe
	if f.vexe != "" {
		vexe = f.vexe
	}
	b, _ := json.Marshal(map[string]any{"schema_version": 1, "worker": cmd(f.exe, worker, wt), "verification": cmd(vexe, verifier, vt)})
	return string(b)
}

func (f *fx) setPlan(worker, verifier string) { f.writePlan(f.planText(worker, verifier, 30, 30)) }

func (f *fx) writePlan(s string) {
	os.Remove(f.plan)
	if err := os.WriteFile(f.plan, []byte(s), 0o600); err != nil {
		f.t.Fatal(err)
	}
	os.Chmod(f.plan, 0o600)
}

func (f *fx) req(asJSON bool) Request {
	return Request{Repo: f.repo, StateDir: f.root, RunID: testID, Plan: f.plan, JSON: asJSON}
}

func (f *fx) exec(ctx context.Context, asJSON bool) (bool, string, error) {
	var out bytes.Buffer
	ok, err := Execute(ctx, f.req(asJSON), &out)
	return ok, out.String(), err
}

// starts counts the lines the fake appended for role.
func (f *fx) starts(role string) int {
	b, err := os.ReadFile(filepath.Join(f.count, role))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return strings.Count(string(b), "\n")
}

func (f *fx) attempt() string { return filepath.Join(f.root, Namespace, testID) }

func codeIs(err error, c string) bool {
	var e *Error
	if errors.As(err, &e) {
		return string(e.Code) == c
	}
	var se *state.Error
	return errors.As(err, &se) && string(se.Code) == c
}

// wantText is the hand-written nine-line report.
func wantText(outcome, ws, wc, vs, vc, format, before, after string) string {
	return "BAW execution observations\nRun: " + testID + "\nAuthority: not_evaluated\nReadiness: not_evaluated\nOutcome: " + outcome +
		"\nWorker: state=" + ws + " exit_code=" + wc + "\nVerification: state=" + vs + " exit_code=" + vc +
		"\nRepository: object_format=" + format + " before_head=" + before + " after_head=" + after + "\nReceipt: recorded\n"
}

// checkResultJSON compares a JSON result with hand-written expected values;
// timestamps are checked for shape only.
func checkResultJSON(t *testing.T, data string, outcome string, worker, verifier, repo map[string]any) {
	t.Helper()
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil || !strings.HasSuffix(data, "}\n") {
		t.Fatalf("result json %q: %v", data, err)
	}
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if strings.Join(keys, ",") != "authority,completed_at,created_at,operation,outcome,readiness,receipt_state,repository,run_id,schema_version,verification,worker" {
		t.Fatalf("keys %v", keys)
	}
	for _, k := range []string{"created_at", "completed_at"} {
		if s, _ := m[k].(string); !state.ValidTimestamp(s) {
			t.Fatalf("%s %v", k, m[k])
		}
		delete(m, k)
	}
	want := map[string]any{"schema_version": json.Number("1"), "run_id": testID, "operation": "run_execute",
		"authority": "not_evaluated", "readiness": "not_evaluated", "outcome": outcome, "worker": worker,
		"verification": verifier, "repository": repo, "receipt_state": "recorded"}
	got, _ := json.Marshal(m)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("result\n got %s\nwant %s", got, exp)
	}
}

func prog(state string, code any) map[string]any {
	if code != nil {
		code = json.Number(code.(string))
	}
	return map[string]any{"state": state, "exit_code": code}
}

func modeOf(t *testing.T, p string) fs.FileMode {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode()
}

func TestExecutePassedBothFormatsAndOutputs(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, asJSON := range []bool{false, true} {
			f := newFx(t, format)
			ok, out, err := f.exec(context.Background(), asJSON)
			if err != nil || !ok {
				t.Fatalf("%s json=%v: %v %v %q", format, asJSON, ok, err, out)
			}
			if f.starts("worker") != 1 || f.starts("verifier") != 1 {
				t.Fatalf("starts %d %d", f.starts("worker"), f.starts("verifier"))
			}
			// The verifier checked the worker's bytes itself; check them again.
			if b, _ := os.ReadFile(filepath.Join(f.repo, "dummy.txt")); string(b) != workerBytes {
				t.Fatalf("dummy %q", b)
			}
			head, _ := tf.OID(f.home, f.repo, "HEAD")
			if head != f.head {
				t.Fatal("worker did not commit; HEAD must be unchanged")
			}
			if asJSON {
				checkResultJSON(t, out, "verification_passed", prog("exited", "0"), prog("exited", "0"),
					map[string]any{"object_format": format, "before_head": f.head, "after_head": f.head})
			} else if out != wantText("verification_passed", "exited", "0", "exited", "0", format, f.head, f.head) {
				t.Fatalf("text %q", out)
			}
			a := f.attempt()
			res, _ := os.ReadFile(filepath.Join(a, "result.json"))
			if asJSON && string(res) != out {
				t.Fatal("stdout is not the published result bytes")
			}
			for _, d := range []string{"", "worker", "worker/home", "worker/tmp", "verifier", "verifier/home", "verifier/tmp"} {
				if m := modeOf(t, filepath.Join(a, d)); !m.IsDir() || m.Perm() != 0o700 {
					t.Fatalf("%s mode %v", d, m)
				}
			}
			if m := modeOf(t, filepath.Join(f.root, Namespace)); m.Perm() != 0o700 {
				t.Fatalf("namespace mode %v", m)
			}
			names := listing(t, a)
			if len(names) != 6 || names[2] != "intent.json" || names[3] != "result.json" ||
				!strings.HasPrefix(names[0], ".pending-intent-") || !strings.HasPrefix(names[1], ".pending-result-") {
				t.Fatalf("attempt listing %v", names)
			}
			for _, n := range names[:4] {
				fi, _ := os.Lstat(filepath.Join(a, n))
				if fi.Mode().Perm() != 0o600 || fi.Sys().(*syscall.Stat_t).Nlink != 2 {
					t.Fatalf("%s mode/links %v %d", n, fi.Mode(), fi.Sys().(*syscall.Stat_t).Nlink)
				}
			}
			checkIntent(t, filepath.Join(a, "intent.json"), format, f.head, f.plan)
			if l := listing(t, filepath.Join(a, "worker/home")); len(l) != 1 {
				t.Fatalf("worker home marker %v", l)
			}
			if l := listing(t, filepath.Join(a, "worker/tmp")); len(l) != 1 {
				t.Fatalf("worker tmp marker %v", l)
			}
			if len(listing(t, filepath.Join(a, "verifier/home")))+len(listing(t, filepath.Join(a, "verifier/tmp"))) != 0 {
				t.Fatal("verifier scratch not empty")
			}
			for _, n := range []string{"intent.json", "result.json"} {
				b, _ := os.ReadFile(filepath.Join(a, n))
				if bytes.Contains(b, []byte("SECRET")) || bytes.Contains(b, []byte(f.base)) || bytes.Contains(b, []byte("main")) {
					t.Fatalf("%s leaks raw output, path or branch: %s", n, b)
				}
			}
			if strings.Contains(out, "SECRET") || strings.Contains(out, f.base) {
				t.Fatal("output leaks")
			}
			// Every repeat refuses without starting anything.
			if _, out2, err := f.exec(context.Background(), asJSON); !codeIs(err, "execution_exists") || out2 != "" {
				t.Fatalf("repeat: %v %q", err, out2)
			}
			if f.starts("worker") != 1 || f.starts("verifier") != 1 {
				t.Fatal("repeat started a program")
			}
		}
	}
}

func listing(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	sort.Strings(n)
	return n
}

func checkIntent(t *testing.T, path, format, head, plan string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		t.Fatal(err)
	}
	if s, _ := m["created_at"].(string); !state.ValidTimestamp(s) {
		t.Fatal("created_at")
	}
	delete(m, "created_at")
	pb, _ := os.ReadFile(plan)
	sum := sha256Hex(pb)
	want := map[string]any{"schema_version": json.Number("1"), "run_id": testID, "record_state": "execution_intent",
		"ticket_url": testTicket, "scope_sha256": testScope, "policy_commit": strings.Repeat("5", len(head)),
		"repository_object_format": format, "repository_head": head, "plan_sha256": sum}
	got, _ := json.Marshal(m)
	exp, _ := json.Marshal(want)
	if string(got) != string(exp) {
		t.Fatalf("intent\n got %s\nwant %s", got, exp)
	}
}

func TestWorkerCommitThenRepeatMismatch(t *testing.T) {
	f := newFx(t, "sha256")
	f.setPlan("worker-commit", "verifier-check")
	ok, out, err := f.exec(context.Background(), true)
	if err != nil || !ok {
		t.Fatalf("%v %v %q", ok, err, out)
	}
	after, _ := tf.OID(f.home, f.repo, "HEAD")
	if after == f.head || len(after) != 64 {
		t.Fatal("fixture worker commit missing")
	}
	checkResultJSON(t, out, "verification_passed", prog("exited", "0"), prog("exited", "0"),
		map[string]any{"object_format": "sha256", "before_head": f.head, "after_head": after})
	if _, _, err := f.exec(context.Background(), true); !codeIs(err, "execution_checkpoint_mismatch") {
		t.Fatalf("repeat after commit: %v", err)
	}
	f.writePlan("{")
	if _, _, err := f.exec(context.Background(), true); !codeIs(err, "invalid_execution_plan") {
		t.Fatalf("repeat with malformed plan: %v", err)
	}
	if f.starts("worker") != 1 || f.starts("verifier") != 1 {
		t.Fatal("repeat started a program")
	}
}

// Every §7 row reachable with real fake programs, with hand-written packets.
func TestOutcomeRows(t *testing.T) {
	unknown := "unknown"
	cases := []struct {
		name, worker, verifier  string
		wt                      int
		outcome, ws, wc, vs, vc string
		after                   bool
		ok                      bool
		wantW, wantV            int
	}{
		{"worker nonzero", "worker-fail", "verifier-check", 30, "worker_failed", "exited", "3", "not_started", unknown, false, false, 1, 0},
		{"worker signal", "worker-signal", "verifier-check", 30, "worker_unverified", "unverified", unknown, "not_started", unknown, false, false, 1, 0},
		{"worker timeout", "worker-sleep", "verifier-check", 1, "worker_unverified", "unverified", unknown, "not_started", unknown, false, false, 1, 0},
		{"worker stdout cap", "worker-stdout-cap", "verifier-check", 30, "worker_unverified", "unverified", unknown, "not_started", unknown, false, false, 1, 0},
		{"worker stderr cap", "worker-stderr-cap", "verifier-check", 30, "worker_unverified", "unverified", unknown, "not_started", unknown, false, false, 1, 0},
		{"verifier nonzero", "worker-ok", "verifier-fail", 30, "verification_failed", "exited", "0", "exited", "5", true, false, 1, 1},
		{"verifier signal", "worker-ok", "verifier-signal", 30, "verification_unverified", "exited", "0", "unverified", unknown, true, false, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFx(t, "sha1")
			f.writePlan(f.planText(c.worker, c.verifier, c.wt, 30))
			ok, out, err := f.exec(context.Background(), false)
			after := unknown
			if c.after {
				after = f.head
			}
			if err != nil || ok != c.ok || out != wantText(c.outcome, c.ws, c.wc, c.vs, c.vc, "sha1", f.head, after) {
				t.Fatalf("%v %v %q", ok, err, out)
			}
			if f.starts("worker") != c.wantW || f.starts("verifier") != c.wantV {
				t.Fatalf("starts %d %d", f.starts("worker"), f.starts("verifier"))
			}
		})
	}
}

func TestSeamRows(t *testing.T) {
	unknown := "unknown"
	cases := []struct {
		name, stage             string
		outcome, ws, wc, vs, vc string
		after                   bool
		wantW, wantV            int
	}{
		{"worker start failure", "worker-start", "worker_unverified", "not_started", unknown, "not_started", unknown, false, 0, 0},
		{"worker receipt failure after start", "worker-receipt", "worker_unverified", "unverified", unknown, "not_started", unknown, false, 1, 0},
		{"post inspection failure", "post-inspection", "inspection_failed", "exited", "0", "not_started", unknown, false, 1, 0},
		{"verifier start failure", "verifier-start", "verification_unverified", "exited", "0", "not_started", unknown, true, 1, 0},
		{"verifier receipt failure after start", "verifier-receipt", "verification_unverified", "exited", "0", "unverified", unknown, true, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFx(t, "sha1")
			hook = func(s string) error {
				if s == c.stage {
					return errors.New("injected")
				}
				return nil
			}
			defer func() { hook = nil }()
			ok, out, err := f.exec(context.Background(), false)
			after := unknown
			if c.after {
				after = f.head
			}
			if err != nil || ok || out != wantText(c.outcome, c.ws, c.wc, c.vs, c.vc, "sha1", f.head, after) {
				t.Fatalf("%v %v %q", ok, err, out)
			}
			if f.starts("worker") != c.wantW || f.starts("verifier") != c.wantV {
				t.Fatalf("starts %d %d", f.starts("worker"), f.starts("verifier"))
			}
		})
	}
}

// A verifier executable removed after validation is not_started, never a
// pre-execution error.
func TestVanishedVerifierExecutable(t *testing.T) {
	f := newFx(t, "sha1")
	copyExe := filepath.Join(f.base, "verifier-tool")
	b, _ := os.ReadFile(f.exe)
	os.WriteFile(copyExe, b, 0o700)
	f.vexe = copyExe
	f.setPlan("worker-ok", "verifier-check")
	hook = func(s string) error {
		if s == "post-inspection" {
			os.Remove(copyExe)
		}
		return nil
	}
	defer func() { hook = nil }()
	_, out, err := f.exec(context.Background(), false)
	if err != nil || out != wantText("verification_unverified", "exited", "0", "not_started", "unknown", "sha1", f.head, f.head) {
		t.Fatalf("%v %q", err, out)
	}
}

var scratchStages = []string{"id-chmod", "ns-sync", "ns-close", "id-open", "id-recheck",
	"worker-mkdir", "worker-chmod", "worker-home-mkdir", "worker-home-chmod", "worker-home-open", "worker-home-recheck",
	"worker-home-sync", "worker-home-close", "worker-tmp-mkdir", "worker-tmp-sync", "worker-tmp-close",
	"worker-open", "worker-sync", "worker-close", "verifier-mkdir", "verifier-home-mkdir", "verifier-home-sync",
	"verifier-home-close", "verifier-tmp-mkdir", "verifier-tmp-open", "verifier-tmp-sync", "verifier-tmp-close",
	"verifier-open", "verifier-sync", "verifier-close", "id-sync", "id-close",
	"intent-random", "intent-create", "intent-chmod", "intent-write", "intent-sync", "intent-close", "intent-link",
	"intent-dir-open", "intent-dir-sync", "intent-dir-close"}

// After the exclusive ID mkdir every scratch or intent failure is uncertain,
// retains the attempt and starts neither program; repeats refuse.
func TestScratchAndIntentFaultsAreUncertain(t *testing.T) {
	for _, stage := range scratchStages {
		t.Run(stage, func(t *testing.T) {
			f := newFx(t, "sha1")
			hook = func(s string) error {
				if s == stage {
					return errors.New("injected")
				}
				return nil
			}
			ok, out, err := f.exec(context.Background(), true)
			hook = nil
			if ok || out != "" || !codeIs(err, "execution_uncertain") {
				t.Fatalf("%v %q %v", ok, out, err)
			}
			if f.starts("worker")+f.starts("verifier") != 0 {
				t.Fatal("a program started")
			}
			if _, err := os.Lstat(f.attempt()); err != nil {
				t.Fatal("attempt not retained")
			}
			if _, _, err := f.exec(context.Background(), true); !codeIs(err, "execution_exists") {
				t.Fatalf("repeat: %v", err)
			}
		})
	}
}

func TestPreAcquisitionFaults(t *testing.T) {
	for stage, code := range map[string]string{"ns-mkdir": "execution_storage_unavailable", "ns-chmod": "execution_storage_unavailable",
		"root-open": "execution_storage_unavailable", "root-sync": "durability_unavailable", "root-close": "execution_storage_unavailable",
		"ns-open": "execution_storage_unavailable", "ns-recheck": "execution_storage_unavailable", "id-mkdir": "execution_storage_unavailable"} {
		t.Run(stage, func(t *testing.T) {
			f := newFx(t, "sha1")
			hook = func(s string) error {
				if s == stage {
					return errors.New("injected")
				}
				return nil
			}
			ok, out, err := f.exec(context.Background(), true)
			hook = nil
			if ok || out != "" || !codeIs(err, code) {
				t.Fatalf("%v %q %v", ok, out, err)
			}
			if _, err := os.Lstat(f.attempt()); err == nil {
				t.Fatal("attempt created")
			}
			if f.starts("worker") != 0 {
				t.Fatal("started")
			}
		})
	}
}

// Faults after the verifier independently started keep its effect and the
// records: uncertain, no stdout, no additional start on repeat.
func TestResultFaultsAfterVerifierStart(t *testing.T) {
	for _, stage := range []string{"output-limit", "result-random", "result-create", "result-write", "result-sync", "result-close",
		"result-link", "result-dir-sync", "result-dir-close", "deliver"} {
		t.Run(stage, func(t *testing.T) {
			f := newFx(t, "sha256")
			hook = func(s string) error {
				if s == stage {
					return errors.New("injected")
				}
				return nil
			}
			ok, out, err := f.exec(context.Background(), true)
			hook = nil
			if ok || out != "" || !codeIs(err, "execution_uncertain") {
				t.Fatalf("%v %q %v", ok, out, err)
			}
			if f.starts("verifier") != 1 {
				t.Fatal("verifier did not start")
			}
			if b, _ := os.ReadFile(filepath.Join(f.repo, "dummy.txt")); string(b) != workerBytes {
				t.Fatal("worker effect rolled back")
			}
			if _, err := os.Lstat(filepath.Join(f.attempt(), "intent.json")); err != nil {
				t.Fatal("intent not retained")
			}
			if _, _, err := f.exec(context.Background(), true); !codeIs(err, "execution_exists") || f.starts("worker") != 1 || f.starts("verifier") != 1 {
				t.Fatalf("repeat: %v", err)
			}
		})
	}
}

type failWriter struct{ n int }

func (w *failWriter) Write(p []byte) (int, error) {
	if len(p) > w.n {
		return w.n, errors.New("short")
	}
	return len(p), nil
}

func TestDeliveryWriterFailureIsUncertain(t *testing.T) {
	f := newFx(t, "sha1")
	_, err := Execute(context.Background(), f.req(false), &failWriter{n: 10})
	if !codeIs(err, "execution_uncertain") {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err != nil {
		t.Fatal("result not retained")
	}
}

// Deterministic cancellation at every admission/receipt boundary.
func TestCancellationBoundaries(t *testing.T) {
	unknown := "unknown"
	cases := []struct {
		name, stage, worker, verifier string
		code                          string // pre-result error, or "" for a published packet
		outcome, ws, wc, vs, vc       string
		after, ok                     bool
		wantW, wantV                  int
	}{
		{"before acquisition", "ns-mkdir", "worker-ok", "verifier-check", "execution_cancelled", "", "", "", "", "", false, false, 0, 0},
		{"after id mkdir before intent", "worker-mkdir", "worker-ok", "verifier-check", "execution_uncertain", "", "", "", "", "", false, false, 0, 0},
		{"after intent before worker", "intent-dir-close", "worker-ok", "verifier-check", "", "worker_unverified", "not_started", unknown, "not_started", unknown, false, false, 0, 0},
		{"at worker start", "worker-start", "worker-ok", "verifier-check", "", "worker_unverified", "not_started", unknown, "not_started", unknown, false, false, 0, 0},
		{"while worker runs", "worker-running", "worker-sleep", "verifier-check", "", "worker_unverified", "unverified", unknown, "not_started", unknown, false, false, 1, 0},
		{"worker return boundary", "worker-receipt", "worker-ok", "verifier-check", "", "worker_unverified", "unverified", unknown, "not_started", unknown, false, false, 1, 0},
		{"during post inspection", "post-inspection", "worker-ok", "verifier-check", "", "verification_unverified", "exited", "0", "not_started", unknown, false, false, 1, 0},
		{"at verifier start", "verifier-start", "worker-ok", "verifier-check", "", "verification_unverified", "exited", "0", "not_started", unknown, true, false, 1, 0},
		{"while verifier runs", "verifier-running", "worker-ok", "verifier-sleep", "", "verification_unverified", "exited", "0", "unverified", unknown, true, false, 1, 1},
		{"verifier return boundary", "verifier-receipt", "worker-ok", "verifier-check", "", "verification_unverified", "exited", "0", "unverified", unknown, true, false, 1, 1},
		{"after classification", "result-create", "worker-ok", "verifier-check", "", "verification_passed", "exited", "0", "exited", "0", true, true, 1, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFx(t, "sha1")
			f.writePlan(f.planText(c.worker, c.verifier, 30, 30))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var once sync.Once
			hook = func(s string) error {
				if s == c.stage {
					once.Do(cancel)
				}
				return nil
			}
			defer func() { hook = nil }()
			if strings.HasSuffix(c.stage, "-running") {
				role := strings.TrimSuffix(c.stage, "-running")
				go func() {
					for f.starts(role) == 0 && ctx.Err() == nil {
						time.Sleep(10 * time.Millisecond)
					}
					cancel()
				}()
			}
			start := time.Now()
			ok, out, err := f.exec(ctx, false)
			if time.Since(start) > 20*time.Second {
				t.Fatal("cancellation not bounded")
			}
			if c.code != "" {
				if !codeIs(err, c.code) || out != "" {
					t.Fatalf("%v %q", err, out)
				}
			} else {
				after := unknown
				if c.after {
					after = f.head
				}
				if err != nil || ok != c.ok || out != wantText(c.outcome, c.ws, c.wc, c.vs, c.vc, "sha1", f.head, after) {
					t.Fatalf("%v %v %q", ok, err, out)
				}
			}
			if f.starts("worker") != c.wantW || f.starts("verifier") != c.wantV {
				t.Fatalf("starts %d %d", f.starts("worker"), f.starts("verifier"))
			}
			if c.stage == "ns-mkdir" {
				if _, err := os.Lstat(filepath.Join(f.root, Namespace)); err == nil {
					// Cancellation observed at acquisition start may keep a namespace; none expected here.
					t.Log("namespace retained")
				}
			}
		})
	}
}

func TestPrecancelledBeforeAcquisitionPreservesRoot(t *testing.T) {
	f := newFx(t, "sha1")
	before := snapshot(t, f.root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, out, err := f.exec(ctx, true); !codeIs(err, "execution_cancelled") || out != "" {
		t.Fatalf("%v %q", err, out)
	}
	if snapshot(t, f.root) != before {
		t.Fatal("state root changed")
	}
	// A validation failure completed before cancellation is observed wins.
	os.Mkdir(filepath.Join(f.root, Namespace), 0o755)
	os.Chmod(filepath.Join(f.root, Namespace), 0o755)
	if _, _, err := f.exec(ctx, true); !codeIs(err, "state_permissions") {
		t.Fatal(err)
	}
}

// snapshot lists names, modes, sizes and bytes beneath dir (atime excluded).
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Lstat(p)
		b.WriteString(p + " " + fi.Mode().String() + " " + fi.ModTime().String())
		if fi.Mode().IsRegular() {
			c, _ := os.ReadFile(p)
			b.Write(c)
		}
		b.WriteString("\n")
		return nil
	})
	return b.String()
}

func TestConcurrentSameIDStartsOneWorker(t *testing.T) {
	f := newFx(t, "sha1")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = Execute(context.Background(), f.req(true), &bytes.Buffer{})
		}(i)
	}
	wg.Wait()
	succeeded := 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case codeIs(err, "execution_exists"):
		default:
			t.Fatalf("unexpected %v", err)
		}
	}
	if succeeded != 1 || f.starts("worker") != 1 || f.starts("verifier") != 1 {
		t.Fatalf("succeeded %d starts %d", succeeded, f.starts("worker"))
	}
}

func TestPreexecutionFailuresStartNothing(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fx) Request
		code  string
	}{
		{"head mismatch", func(f *fx) Request {
			tf.Write(filepath.Join(f.repo, "x.txt"), "x\n")
			tf.Git(f.home, f.repo, "add", "x.txt")
			tf.Git(f.home, f.repo, "commit", "-q", "-m", "x")
			return f.req(true)
		}, "execution_checkpoint_mismatch"},
		{"state inside repo", func(f *fx) Request {
			inner := filepath.Join(f.repo, "state")
			os.Mkdir(inner, 0o700)
			os.Chmod(inner, 0o700)
			os.Rename(filepath.Join(f.root, "records-v1"), filepath.Join(inner, "records-v1"))
			r := f.req(true)
			r.StateDir = inner
			return r
		}, "unsafe_execution_layout"},
		{"plan inside repo", func(f *fx) Request {
			p := filepath.Join(f.repo, "plan.json")
			b, _ := os.ReadFile(f.plan)
			os.WriteFile(p, b, 0o600)
			os.Chmod(p, 0o600)
			r := f.req(true)
			r.Plan = p
			return r
		}, "unsafe_execution_layout"},
		{"repository is the namespace", func(f *fx) Request {
			ns := filepath.Join(f.root, Namespace)
			os.Rename(f.repo, ns)
			os.Chmod(ns, 0o700)
			r := f.req(true)
			r.Repo = ns
			return r
		}, "unsafe_execution_layout"},
		{"group writable executable", func(f *fx) Request {
			tool := filepath.Join(f.base, "tool")
			b, _ := os.ReadFile(f.exe)
			os.WriteFile(tool, b, 0o700)
			os.Chmod(tool, 0o770)
			f.writePlan(strings.ReplaceAll(f.planText("worker-ok", "verifier-check", 30, 30), f.exe, tool))
			return f.req(true)
		}, "executable_unavailable"},
		{"non-executable", func(f *fx) Request {
			tool := filepath.Join(f.base, "tool")
			os.WriteFile(tool, []byte("x"), 0o600)
			f.writePlan(strings.ReplaceAll(f.planText("worker-ok", "verifier-check", 30, 30), f.exe, tool))
			return f.req(true)
		}, "executable_unavailable"},
		{"symlinked executable", func(f *fx) Request {
			tool := filepath.Join(f.base, "tool")
			os.Symlink(f.exe, tool)
			f.writePlan(strings.ReplaceAll(f.planText("worker-ok", "verifier-check", 30, 30), f.exe, tool))
			return f.req(true)
		}, "executable_unavailable"},
		{"missing record", func(f *fx) Request {
			r := f.req(true)
			r.RunID = strings.Repeat("e", 32)
			return r
		}, "record_missing"},
		{"malformed plan before missing record", func(f *fx) Request {
			f.writePlan(`{"schema_version":1}`)
			r := f.req(true)
			r.RunID = strings.Repeat("e", 32)
			return r
		}, "invalid_execution_plan"},
		{"mismatch before executable", func(f *fx) Request {
			tf.Write(filepath.Join(f.repo, "x.txt"), "x\n")
			tf.Git(f.home, f.repo, "add", "x.txt")
			tf.Git(f.home, f.repo, "commit", "-q", "-m", "x")
			f.writePlan(strings.ReplaceAll(f.planText("worker-ok", "verifier-check", 30, 30), f.exe, "/nonexistent/tool"))
			return f.req(true)
		}, "execution_checkpoint_mismatch"},
		{"unsafe namespace before plan", func(f *fx) Request {
			os.Mkdir(filepath.Join(f.root, Namespace), 0o755)
			os.Chmod(filepath.Join(f.root, Namespace), 0o755)
			f.writePlan("{")
			return f.req(true)
		}, "state_permissions"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFx(t, "sha1")
			r := c.setup(f)
			var out bytes.Buffer
			_, err := Execute(context.Background(), r, &out)
			if !codeIs(err, c.code) || out.Len() != 0 {
				t.Fatalf("got %v %q want %s", err, out.String(), c.code)
			}
			if f.starts("worker")+f.starts("verifier") != 0 {
				t.Fatal("started")
			}
		})
	}
}

// Subdirectory and ancestor-alias repository paths resolve to the top level,
// and a sibling sharing a textual prefix with the namespace is allowed.
func TestRepoAliasAndPrefixSibling(t *testing.T) {
	f := newFx(t, "sha1")
	sibling := filepath.Join(f.root, Namespace+"-extra")
	if err := os.Rename(f.repo, sibling); err != nil {
		t.Fatal(err)
	}
	f.repo = sibling
	f.setPlan("worker-ok", "verifier-check")
	link := filepath.Join(f.base, "alias")
	os.Symlink(f.base, link)
	r := f.req(false)
	r.Repo = filepath.Join(link, "state", Namespace+"-extra", "docs")
	var out bytes.Buffer
	ok, err := Execute(context.Background(), r, &out)
	if err != nil || !ok || out.String() != wantText("verification_passed", "exited", "0", "exited", "0", "sha1", f.head, f.head) {
		t.Fatalf("%v %v %q", ok, err, out.String())
	}
}

func TestUnbornRepositoryMismatch(t *testing.T) {
	f := newFx(t, "sha1")
	os.RemoveAll(filepath.Join(f.repo, ".git"))
	tf.Init(f.home, f.repo, "sha1")
	if _, _, err := f.exec(context.Background(), true); !codeIs(err, "execution_checkpoint_mismatch") {
		t.Fatal(err)
	}
}
