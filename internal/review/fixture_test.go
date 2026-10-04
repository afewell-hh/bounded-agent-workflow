package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

const (
	testID     = "0123456789abcdef0123456789abcdef"
	testTicket = "https://github.com/example/project/issues/21"
	fixedNow   = "2026-10-04T12:00:00Z"
)

var testScope = strings.Repeat("5", 64)

// receipts are the hand-built execution receipt fields.
type receipts struct {
	intentID, resultID, ticket, scope, policy, format, head string
	created, before, completed, outcome                     string
	after                                                   *string
	worker, verification                                    string // JSON program objects
}

type fx struct {
	t                                            *testing.T
	ctx                                          context.Context // optional, for assertPre
	base, home, repo, state, plan, tool, counter string
	format, head                                 string
	r                                            receipts
}

func resolvedTemp(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdir0700(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
}

func write0600(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newFx builds a committed fixture repository, a private state directory
// with a run record and a successful execution attempt, a private copy of
// this test binary as the reviewer and a plan running it with mode.
func newFx(t *testing.T, format, mode string, extra ...string) *fx {
	t.Helper()
	base := resolvedTemp(t)
	f := &fx{t: t, base: base, home: filepath.Join(base, "home"), repo: filepath.Join(base, "repo"),
		state: filepath.Join(base, "state"), plan: filepath.Join(base, "plan.json"),
		tool: filepath.Join(base, "reviewer-tool"), counter: filepath.Join(base, "counter"), format: format}
	mkdir0700(t, f.home)
	mkdir0700(t, f.counter)
	if err := tf.Init(f.home, f.repo, format); err != nil {
		t.Fatal(err)
	}
	head, err := tf.CommitSources(f.home, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	f.head = head
	mkdir0700(t, f.state)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.tool, b, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(f.tool, 0o700)
	h := head
	f.r = receipts{intentID: testID, resultID: testID, ticket: testTicket, scope: testScope, policy: head,
		format: format, head: head, created: "2026-10-04T10:00:00Z", before: head, completed: "2026-10-04T10:05:00Z",
		outcome: "verification_passed", after: &h,
		worker: `{"state":"exited","exit_code":0}`, verification: `{"state":"exited","exit_code":0}`}
	f.writeRecord(head)
	f.writeReceipts()
	f.writePlan(mode, extra...)
	return f
}

func (f *fx) writeRecord(head string) {
	mkdirAll0700(f.t, filepath.Join(f.state, "records-v1"))
	rec := fmt.Sprintf(`{"schema_version":1,"run_id":"%s","record_state":"recorded","ticket_url":"%s","scope_sha256":"%s",`+
		`"policy_commit":"%s","repository_object_format":"%s","repository_head":"%s","created_at":"2026-10-04T09:00:00Z"}`+"\n",
		testID, testTicket, testScope, head, f.format, head)
	write0600(f.t, filepath.Join(f.state, "records-v1", testID+".json"), []byte(rec))
}

func mkdirAll0700(t *testing.T, p string) {
	if _, err := os.Lstat(p); err == nil {
		return
	}
	mkdirAll0700(t, filepath.Dir(p))
	mkdir0700(t, p)
}

func (f *fx) intentJSON() string {
	r := f.r
	return fmt.Sprintf(`{"schema_version":1,"run_id":"%s","record_state":"execution_intent","ticket_url":"%s",`+
		`"scope_sha256":"%s","policy_commit":"%s","repository_object_format":"%s","repository_head":"%s",`+
		`"plan_sha256":"%s","created_at":"%s"}`+"\n",
		r.intentID, r.ticket, r.scope, r.policy, r.format, r.head, strings.Repeat("9", 64), r.created)
}

func (f *fx) resultJSON() string {
	r := f.r
	after := "null"
	if r.after != nil {
		after = `"` + *r.after + `"`
	}
	return fmt.Sprintf(`{"schema_version":1,"run_id":"%s","operation":"run_execute","authority":"not_evaluated",`+
		`"readiness":"not_evaluated","outcome":"%s","worker":%s,"verification":%s,"repository":{"object_format":"%s",`+
		`"before_head":"%s","after_head":%s},"receipt_state":"recorded","created_at":"%s","completed_at":"%s"}`+"\n",
		r.resultID, r.outcome, r.worker, r.verification, r.format, r.before, after, r.created, r.completed)
}

func (f *fx) execDir() string { return filepath.Join(f.state, ExecuteNamespace, testID) }

func (f *fx) writeReceipts() {
	mkdirAll0700(f.t, f.execDir())
	write0600(f.t, filepath.Join(f.execDir(), "intent.json"), []byte(f.intentJSON()))
	write0600(f.t, filepath.Join(f.execDir(), "result.json"), []byte(f.resultJSON()))
}

func (f *fx) writePlan(mode string, extra ...string) {
	args := append([]string{`"-baw-review-fake"`, `"` + mode + `"`, `"` + f.counter + `"`}, quoteAll(extra)...)
	plan := `{"schema_version":1,"reviewer":{"executable":"` + f.tool + `","arguments":[` + strings.Join(args, ",") +
		`],"timeout_seconds":20}}` + "\n"
	write0600(f.t, f.plan, []byte(plan))
}

func quoteAll(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = `"` + v + `"`
	}
	return out
}

func (f *fx) git(args ...string) string {
	f.t.Helper()
	out, err := tf.Git(f.home, f.repo, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

// commitDescendant commits one further change and returns the new HEAD.
func (f *fx) commitDescendant() string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.repo, "later.txt"), []byte("committed later by the coordinator\n"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	f.git("add", "later.txt")
	f.git("commit", "-q", "-m", "later descendant")
	oid, err := tf.OID(f.home, f.repo, "HEAD")
	if err != nil {
		f.t.Fatal(err)
	}
	return oid
}

func (f *fx) starts() int {
	b, _ := os.ReadFile(filepath.Join(f.counter, "reviewer"))
	return strings.Count(string(b), "\n")
}

func (f *fx) attempt() string { return filepath.Join(f.state, Namespace, testID) }

func (f *fx) req(candidate string, asJSON bool) Request {
	return Request{Repo: f.repo, StateDir: f.state, RunID: testID, Candidate: candidate, Plan: f.plan, JSON: asJSON}
}

// descriptors records every descriptor the package opens during fn.
type descriptors struct {
	mu    sync.Mutex
	files []*os.File
}

func (d *descriptors) add(f *os.File) { d.mu.Lock(); d.files = append(d.files, f); d.mu.Unlock() }

// assertClosed checks that each observed descriptor object is actually
// closed: any operation on it reports os.ErrClosed.
func (d *descriptors) assertClosed(t *testing.T, what string) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range d.files {
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s: descriptor for %s still open (%v)", what, filepath.Base(f.Name()), err)
		}
	}
}

// run executes Review with fixed time and descriptor observation, then
// asserts every descriptor was closed and restores the seams.
func (f *fx) run(ctx context.Context, req Request) (string, bool, error) {
	f.t.Helper()
	d := &descriptors{}
	opened = d.add
	now = func() time.Time { t, _ := time.Parse(time.RFC3339, fixedNow); return t }
	defer func() { opened, now = nil, time.Now }()
	var out bytes.Buffer
	passed, err := Review(ctx, req, &out)
	d.assertClosed(f.t, "review")
	return out.String(), passed, err
}

func wantResultJSON(outcome, state, code, verdict, format, before, after string) string {
	q := func(s string) string {
		if s == "null" {
			return s
		}
		return `"` + s + `"`
	}
	return `{"schema_version":1,"run_id":"` + testID + `","operation":"run_review","authority":"not_evaluated",` +
		`"readiness":"not_evaluated","outcome":"` + outcome + `","reviewer":{"state":"` + state + `","exit_code":` + code +
		`},"verdict":` + q(verdict) + `,"repository":{"object_format":"` + format + `","before_head":"` + before +
		`","after_head":` + q(after) + `},"receipt_state":"recorded","created_at":"` + fixedNow + `","completed_at":"` +
		fixedNow + `"}` + "\n"
}

func wantText(outcome, state, code, verdict, format, before, after string) string {
	u := func(s string) string {
		if s == "null" {
			return "unknown"
		}
		return s
	}
	return "BAW review observations\nRun: " + testID + "\nAuthority: not_evaluated\nReadiness: not_evaluated\nOutcome: " +
		outcome + "\nReviewer: state=" + state + " exit_code=" + u(code) + " verdict=" + u(verdict) +
		"\nRepository: object_format=" + format + " before_head=" + before + " after_head=" + u(after) + "\nReceipt: recorded\n"
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// snapshot records every entry below dir with type, mode, owner, size,
// inode, link count, mtime and content hash (atime excluded).
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			lines = append(lines, p+" error")
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			lines = append(lines, p+" lstat-error")
			return nil
		}
		st := fi.Sys().(*syscall.Stat_t)
		h := ""
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			h = sha(b)
		}
		lines = append(lines, fmt.Sprintf("%s %v %d %d %d %d %d %s", p, fi.Mode(), st.Uid, fi.Size(), st.Ino, st.Nlink,
			fi.ModTime().UnixNano(), h))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// assertPreAcquisition checks a pre-acquisition failure: the code, empty
// output, zero starts and an unchanged state directory.
func (f *fx) assertPre(t *testing.T, name string, req Request, want error, wantCode string) {
	t.Helper()
	before := snapshot(t, f.state)
	ctx := context.Background()
	if f.ctx != nil {
		ctx = f.ctx
	}
	out, passed, err := f.run(ctx, req)
	if err == nil || err.Error() != wantCode || out != "" || passed {
		t.Errorf("%s: err %v out %q want %s", name, err, out, wantCode)
	}
	if want != nil && !errors.Is(err, want) && err.Error() != want.Error() {
		t.Errorf("%s: err %v want %v", name, err, want)
	}
	if n := f.starts(); n != 0 {
		t.Errorf("%s: %d reviewer starts", name, n)
	}
	if after := snapshot(t, f.state); after != before {
		t.Errorf("%s: state changed\nbefore:\n%s\nafter:\n%s", name, before, after)
	}
}

// assertPreChild is assertPre with Review run in a separate test-binary
// process bounded by a 10s deadline and WaitDelay and always joined, even when
// starting or asserting fails. Use it for inputs (FIFOs, sockets) whose open
// could block a regressed implementation.
func (f *fx) assertPreChild(t *testing.T, name string, req Request, wantCode string) {
	t.Helper()
	before := snapshot(t, f.state)
	dir, err := os.MkdirTemp(f.base, "probe")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := json.Marshal(controllerConfig{Request: req})
	write0600(t, filepath.Join(dir, "config.json"), cfg)
	r, err := runProbe(dir)
	if err != nil {
		t.Fatalf("%s: probe child: %v", name, err)
	}
	if r.Code != wantCode || r.Out != "" || r.Open != 0 {
		t.Errorf("%s: child code %q out %q open %d of %d want %s", name, r.Code, r.Out, r.Open, r.Observed, wantCode)
	}
	if n := f.starts(); n != 0 {
		t.Errorf("%s: %d reviewer starts", name, n)
	}
	if after := snapshot(t, f.state); after != before {
		t.Errorf("%s: state changed", name)
	}
}

// runProbe starts the probe child and always waits for it.
func runProbe(dir string) (probeResult, error) {
	var r probeResult
	self, err := os.Executable()
	if err != nil {
		return r, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self, "-baw-review-probe", dir)
	cmd.WaitDelay = 2 * time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		return r, err
	}
	werr := cmd.Wait()
	if ctx.Err() != nil {
		return r, fmt.Errorf("deadline exceeded: %v", werr)
	}
	if werr != nil {
		return r, werr
	}
	return r, json.Unmarshal(out.Bytes(), &r)
}
