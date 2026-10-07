package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// --- bounded batches of joined unsafe-source child requests ---
//
// Each request still runs in its own child of this test binary with its own
// context, captures and two Run calls (TestContextChildProcess). Only the
// number of such children in flight changes: at most ctxBatchWidth, inside
// TestContextUnsafeSources, against a completed fixture that the parent does
// not touch until every started child has been joined.

const ctxBatchWidth = 3

// Stable error kinds of one child request, in precedence order after setup.
const (
	ctxErrSetup   = "setup_failure"   // marshal or self-path, before any start
	ctxErrContext = "context_failure" // context done after Run, even with a report
	ctxErrProcess = "process_failure" // start, exit or read error from Run
	ctxErrReport  = "report_invalid"  // missing, malformed or truncated report
	ctxErrFacts   = "facts_invalid"   // descriptor or repeat facts not as required
)

// ctxChildError keeps runCtxChild's original diagnostic as its message.
type ctxChildError struct {
	kind  string
	msg   string
	cause error
}

func (e *ctxChildError) Error() string { return e.msg }
func (e *ctxChildError) Unwrap() error { return e.cause }

// ctxChildOps are the operations of one child request, passed explicitly per
// call; there is no package-level hook. run receives its own argv and env
// slices and distinct captures, and must return only after joining its child.
type ctxChildOps struct {
	marshal    func(any) ([]byte, error)
	executable func() string
	newContext func(time.Duration) (context.Context, context.CancelFunc)
	run        func(ctx context.Context, exe string, argv, env []string, stdout, stderr *cappedOutput) error
}

// newCtxChildOps returns the original operations built from the supplied
// constructors; callers pass context.WithTimeout, exec.CommandContext and a
// one-second WaitDelay.
func newCtxChildOps(withTimeout func(context.Context, time.Duration) (context.Context, context.CancelFunc),
	command func(context.Context, string, ...string) *exec.Cmd, waitDelay time.Duration) ctxChildOps {
	return ctxChildOps{
		marshal:    json.Marshal,
		executable: func() string { return os.Args[0] },
		newContext: func(d time.Duration) (context.Context, context.CancelFunc) {
			return withTimeout(context.Background(), d)
		},
		run: func(ctx context.Context, exe string, argv, env []string, stdout, stderr *cappedOutput) error {
			cmd := command(ctx, exe, argv...)
			cmd.Env = env
			cmd.WaitDelay = waitDelay
			cmd.Stdout, cmd.Stderr = stdout, stderr
			return cmd.Run() // waits for the child also when it is killed at the deadline
		},
	}
}

// runCtxChildWith is runCtxChild without testing.T: the same steps in the same
// order, returning the original diagnostic as an error instead of failing.
// env is the parent environment captured before dispatch; it is copied.
func runCtxChildWith(ops ctxChildOps, env, args []string) (result, error) {
	spec, err := ops.marshal(args)
	if err != nil {
		return result{}, &ctxChildError{ctxErrSetup, err.Error(), err}
	}
	exe := ops.executable()
	if !filepath.IsAbs(exe) {
		return result{}, &ctxChildError{ctxErrSetup, fmt.Sprintf("test binary path %q is not absolute", exe), nil}
	}
	ctx, cancel := ops.newContext(10 * time.Second)
	defer cancel()
	argv := []string{"-test.run=^TestContextChildProcess$", "-test.count=1"}
	childEnv := append(slices.Clone(env), ctxChildEnv+"="+string(spec))
	var out, errb cappedOutput
	start := time.Now()
	err = ops.run(ctx, exe, argv, childEnv, &out, &errb)
	if ctx.Err() != nil {
		return result{}, &ctxChildError{ctxErrContext, fmt.Sprintf("context child %q not finished within 10s; killed and joined after %v (%v)",
			args, time.Since(start), err), ctx.Err()}
	}
	if err != nil {
		return result{}, &ctxChildError{ctxErrProcess, fmt.Sprintf("context child %q: %v\nstdout %q\nstderr %q",
			args, err, out.b.String(), errb.b.String()), err}
	}
	_, res, ok := strings.Cut(out.b.String(), ctxChildPrefix)
	res, _, _ = strings.Cut(res, "\n")
	var r ctxChildResult
	if !ok || json.Unmarshal([]byte(res), &r) != nil {
		return result{}, &ctxChildError{ctxErrReport, fmt.Sprintf("context child %q reported no result: %q", args, out.b.String()), nil}
	}
	if len(r.FDBefore) == 0 || !reflect.DeepEqual(r.FDBefore, r.FDAfter) || !r.SameAsWarmUp {
		return result{}, &ctxChildError{ctxErrFacts, fmt.Sprintf("context child %q: descriptors before %v after %v, repeat identical %v",
			args, r.FDBefore, r.FDAfter, r.SameAsWarmUp), nil}
	}
	return result{r.Code, r.Stdout, r.Stderr}, nil
}

// ctxBatchRun runs one request; it must not call testing.T.
type ctxBatchRun func(env, args []string) (result, error)

// ctxBatch attempts every request exactly once, at most ctxBatchWidth at a
// time, after copying env and every argument slice. Results stay at their
// original index. It returns only after every attempted request has returned
// (each run joins its own child), then reports the first error by index;
// an error never stops the remaining queued requests.
func ctxBatch(env []string, reqs [][]string, run ctxBatchRun) ([]result, error) {
	env = slices.Clone(env)
	own := make([][]string, len(reqs))
	for i, r := range reqs {
		own[i] = slices.Clone(r)
	}
	res := make([]result, len(own))
	errs := make([]error, len(own))
	next := make(chan int)
	var wg sync.WaitGroup
	for range min(ctxBatchWidth, len(own)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				res[i], errs[i] = run(env, own[i])
			}
		}()
	}
	for i := range own {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// ctxIndexFacts is a completed fixture's .git/index content and mtime, taken
// before a batch and compared after it has joined: the shared fixture's
// index must not be rewritten by the concurrent read-only children.
type ctxIndexFacts struct {
	data  []byte
	mtime time.Time
}

func ctxIndexOf(dir string) (ctxIndexFacts, error) {
	p := filepath.Join(dir, ".git", "index")
	data, err := os.ReadFile(p)
	if err != nil {
		return ctxIndexFacts{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return ctxIndexFacts{}, err
	}
	return ctxIndexFacts{data, fi.ModTime()}, nil
}

// ctxUnsafeMatrix is the hand-written frozen list of the 63 unsafe-source
// requests and their expected responses, in original order. ID forms:
// role/KIND/BAD/ROLE, legacy/KIND/BAD, common/KIND/PATH/ROLE, ancestor/PATH/ROLE.
// It is not derived from the executor or from the test's loops.
var ctxUnsafeMatrix = [][2]string{
	{"role/fifo/lead/lead", "fail:source_unavailable"},
	{"role/fifo/lead/worker", "ok:context"},
	{"role/fifo/lead/reviewer", "ok:context"},
	{"legacy/fifo/lead", "fail:source_unavailable"},
	{"role/fifo/worker/lead", "ok:context"},
	{"role/fifo/worker/worker", "fail:source_unavailable"},
	{"role/fifo/worker/reviewer", "ok:context"},
	{"legacy/fifo/worker", "ok:legacy"},
	{"role/fifo/reviewer/lead", "ok:context"},
	{"role/fifo/reviewer/worker", "ok:context"},
	{"role/fifo/reviewer/reviewer", "fail:source_unavailable"},
	{"legacy/fifo/reviewer", "ok:legacy"},
	{"common/fifo/AGENTS.md/lead", "fail:source_unavailable"},
	{"common/fifo/AGENTS.md/worker", "fail:source_unavailable"},
	{"common/fifo/AGENTS.md/reviewer", "fail:source_unavailable"},
	{"common/fifo/docs/operator/agent-lifecycle.md/lead", "fail:source_unavailable"},
	{"common/fifo/docs/operator/agent-lifecycle.md/worker", "fail:source_unavailable"},
	{"common/fifo/docs/operator/agent-lifecycle.md/reviewer", "fail:source_unavailable"},
	{"role/directory/lead/lead", "fail:source_unavailable"},
	{"role/directory/lead/worker", "ok:context"},
	{"role/directory/lead/reviewer", "ok:context"},
	{"legacy/directory/lead", "fail:source_unavailable"},
	{"role/directory/worker/lead", "ok:context"},
	{"role/directory/worker/worker", "fail:source_unavailable"},
	{"role/directory/worker/reviewer", "ok:context"},
	{"legacy/directory/worker", "ok:legacy"},
	{"role/directory/reviewer/lead", "ok:context"},
	{"role/directory/reviewer/worker", "ok:context"},
	{"role/directory/reviewer/reviewer", "fail:source_unavailable"},
	{"legacy/directory/reviewer", "ok:legacy"},
	{"common/directory/AGENTS.md/lead", "fail:source_unavailable"},
	{"common/directory/AGENTS.md/worker", "fail:source_unavailable"},
	{"common/directory/AGENTS.md/reviewer", "fail:source_unavailable"},
	{"common/directory/docs/operator/agent-lifecycle.md/lead", "fail:source_unavailable"},
	{"common/directory/docs/operator/agent-lifecycle.md/worker", "fail:source_unavailable"},
	{"common/directory/docs/operator/agent-lifecycle.md/reviewer", "fail:source_unavailable"},
	{"role/symlink/lead/lead", "fail:source_symlink"},
	{"role/symlink/lead/worker", "ok:context"},
	{"role/symlink/lead/reviewer", "ok:context"},
	{"legacy/symlink/lead", "fail:source_symlink"},
	{"role/symlink/worker/lead", "ok:context"},
	{"role/symlink/worker/worker", "fail:source_symlink"},
	{"role/symlink/worker/reviewer", "ok:context"},
	{"legacy/symlink/worker", "ok:legacy"},
	{"role/symlink/reviewer/lead", "ok:context"},
	{"role/symlink/reviewer/worker", "ok:context"},
	{"role/symlink/reviewer/reviewer", "fail:source_symlink"},
	{"legacy/symlink/reviewer", "ok:legacy"},
	{"common/symlink/AGENTS.md/lead", "fail:source_symlink"},
	{"common/symlink/AGENTS.md/worker", "fail:source_symlink"},
	{"common/symlink/AGENTS.md/reviewer", "fail:source_symlink"},
	{"common/symlink/docs/operator/agent-lifecycle.md/lead", "fail:source_symlink"},
	{"common/symlink/docs/operator/agent-lifecycle.md/worker", "fail:source_symlink"},
	{"common/symlink/docs/operator/agent-lifecycle.md/reviewer", "fail:source_symlink"},
	{"ancestor/workflow/roles/lead", "fail:source_symlink"},
	{"ancestor/workflow/roles/worker", "fail:source_symlink"},
	{"ancestor/workflow/roles/reviewer", "fail:source_symlink"},
	{"ancestor/workflow/lead", "fail:source_symlink"},
	{"ancestor/workflow/worker", "fail:source_symlink"},
	{"ancestor/workflow/reviewer", "fail:source_symlink"},
	{"ancestor/docs/lead", "fail:source_symlink"},
	{"ancestor/docs/worker", "fail:source_symlink"},
	{"ancestor/docs/reviewer", "fail:source_symlink"},
}

// ctxCheckMatrix compares the executed request IDs with the hand-written list
// as a multiset (exact multiplicity) and then in order.
func ctxCheckMatrix(ids []string) error {
	want := map[string]int{}
	for _, m := range ctxUnsafeMatrix {
		want[m[0]]++
	}
	got := map[string]int{}
	for _, id := range ids {
		got[id]++
	}
	if len(ids) != len(ctxUnsafeMatrix) || !reflect.DeepEqual(got, want) {
		var diff []string
		for id, n := range got {
			if want[id] != n {
				diff = append(diff, fmt.Sprintf("%s ran %d want %d", id, n, want[id]))
			}
		}
		for id, n := range want {
			if got[id] == 0 {
				diff = append(diff, fmt.Sprintf("%s ran 0 want %d", id, n))
			}
		}
		slices.Sort(diff)
		return fmt.Errorf("multiplicity: %d requests, %v", len(ids), diff)
	}
	for i, m := range ctxUnsafeMatrix {
		if ids[i] != m[0] {
			return fmt.Errorf("order: request %d is %s want %s", i, ids[i], m[0])
		}
	}
	return nil
}

// ctxWantMatrix checks r against the hand-written expectation for id.
func ctxWantMatrix(id string, r result) error {
	var want string
	for _, m := range ctxUnsafeMatrix {
		if m[0] == id {
			want = m[1]
		}
	}
	parts := strings.Split(id, "/")
	switch {
	case strings.HasPrefix(want, "fail:"):
		if r.code != 1 || r.stdout != "" || r.stderr != "baw: "+strings.TrimPrefix(want, "fail:")+"\n" {
			return fmt.Errorf("%s: want %s, got %+v", id, want, r)
		}
	case want == "ok:context":
		bad, role := parts[2], parts[3]
		if r.code != 0 || r.stderr != "" || strings.Contains(r.stdout, "workflow/roles/"+bad+".md") ||
			!strings.Contains(r.stdout, `"path":"workflow/roles/`+role+`.md"`) {
			return fmt.Errorf("%s: want %s, got %+v", id, want, r)
		}
	case want == "ok:legacy":
		if r.code != 0 || strings.Contains(r.stdout, parts[2]+".md") {
			return fmt.Errorf("%s: want %s, got %+v", id, want, r)
		}
	default:
		return fmt.Errorf("%s: not in the hand-written matrix", id)
	}
	return nil
}

// ctxWantArgv checks a request's argument vector against the original call
// written by hand for its ID form; dir is the fixture the request targeted.
func ctxWantArgv(id, dir string, argv []string) error {
	role := id[strings.LastIndex(id, "/")+1:]
	var want []string
	switch strings.Split(id, "/")[0] {
	case "role":
		want = []string{"context", "--repo", dir, "--role", role, "--json"}
	case "legacy":
		want = []string{"inspect", "--repo", dir}
	case "common", "ancestor":
		want = []string{"context", "--repo", dir, "--role", role}
	default:
		return fmt.Errorf("%s: no original argument vector", id)
	}
	if !slices.Equal(argv, want) {
		return fmt.Errorf("%s: argv %q want %q", id, argv, want)
	}
	return nil
}

// --- real finite fake controllers for the batch controls ---
//
// A fake run operation starts a real controller through its own handle: this
// test binary running only TestContextChildProcess with no request, which
// returns at once (natural lifetime well under 1 s, checked). The operation
// then waits for an explicit release (or a bounded watchdog), joins the
// controller, writes its prepared output to the request's own captures and
// returns. Instrumentation is independent of the executor under test.

type ctlBehavior struct {
	startFail bool   // start a missing program: no handle, process error
	exitFail  bool   // the controller exits 1 (child helper rejects "[]")
	readFault bool   // drain output through a Reader whose Read fails
	out       string // written to the request's stdout capture; "" = good report
	awaitHeld bool   // before Start, wait (bounded) for request prereq's held acknowledgement
	prereq    int
}

type ctlAck struct {
	id  int
	ctx context.Context
}

type ctlRec struct {
	t         *testing.T // only for TempDir in setup; never used by operations
	missing   string
	held      map[int]bool
	behaviors map[int]ctlBehavior
	acks      chan ctlAck
	release   map[int]chan struct{}
	heldAck   map[int]chan struct{} // closed once request id has started and is held
	watchdog  time.Duration
	ackWait   time.Duration // bound for a held acknowledgement before Start
	fixture   string        // must exist when each controller is joined

	mu             sync.Mutex
	heldAcked      map[int]bool
	active, peak   int
	attempts       map[int]int
	started        map[int]bool
	joined         map[int]bool
	returned       int
	events         []string
	argv, env      map[int][]string
	bufs           map[*cappedOutput]bool
	problems       []string
	wantDurations  []time.Duration
	parsedRequests map[int][]string
}

var errCtlRead = errors.New("fake controller read failed")

type ctlFaultReader struct{}

func (ctlFaultReader) Read([]byte) (int, error) { return 0, errCtlRead }

func newCtlRec(t *testing.T, n int, held map[int]bool, behaviors map[int]ctlBehavior) *ctlRec {
	rec := &ctlRec{t: t, missing: filepath.Join(t.TempDir(), "missing-controller"), held: held, behaviors: behaviors,
		acks: make(chan ctlAck, 4*n+8), release: map[int]chan struct{}{}, heldAck: map[int]chan struct{}{},
		watchdog: 3 * time.Second, ackWait: time.Second, heldAcked: map[int]bool{},
		attempts: map[int]int{}, started: map[int]bool{}, joined: map[int]bool{}, argv: map[int][]string{},
		env: map[int][]string{}, bufs: map[*cappedOutput]bool{}, parsedRequests: map[int][]string{}}
	for i := range n {
		rec.release[i] = make(chan struct{})
		rec.heldAck[i] = make(chan struct{})
	}
	return rec
}

func (r *ctlRec) event(s string) {
	r.mu.Lock()
	r.events = append(r.events, s)
	r.mu.Unlock()
}

func (r *ctlRec) isJoined(id int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.joined[id]
}

func (r *ctlRec) problem(format string, a ...any) {
	r.mu.Lock()
	r.problems = append(r.problems, fmt.Sprintf(format, a...))
	r.mu.Unlock()
}

// ctlReport is a plausible child report for id; ctlWant is its hand-written
// expected result.
func ctlReport(id int, before, after []string, same bool) string {
	b, _ := json.Marshal(ctxChildResult{10 + id, fmt.Sprintf("out-%d", id), fmt.Sprintf("err-%d", id), before, after, same})
	return "noise\n" + ctxChildPrefix + string(b) + "\n"
}

func ctlWant(id int) result {
	return result{10 + id, fmt.Sprintf("out-%d", id), fmt.Sprintf("err-%d", id)}
}

var ctlFDs = []string{"0", "1", "2", "3"}

// ops returns per-call operations whose context factory records the
// requested duration and returns a context the driver triggers explicitly.
func (r *ctlRec) ops() ctxChildOps {
	return ctxChildOps{
		marshal:    json.Marshal,
		executable: func() string { return os.Args[0] },
		newContext: func(d time.Duration) (context.Context, context.CancelFunc) {
			r.mu.Lock()
			r.wantDurations = append(r.wantDurations, d)
			r.mu.Unlock()
			c := newCtlCtx()
			return c, func() { c.trigger(context.Canceled) }
		},
		run: r.run,
	}
}

func (r *ctlRec) run(ctx context.Context, exe string, argv, env []string, stdout, stderr *cappedOutput) error {
	argvIn, envIn := slices.Clone(argv), slices.Clone(env)
	id := -1
	var spec []string
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, ctxChildEnv+"="); ok && json.Unmarshal([]byte(v), &spec) == nil && len(spec) == 2 {
			id, _ = strconv.Atoi(spec[1])
		}
	}
	r.mu.Lock()
	r.attempts[id]++
	r.argv[id], r.env[id] = argvIn, envIn
	r.parsedRequests[id] = spec
	if r.bufs[stdout] || r.bufs[stderr] || stdout == stderr {
		r.problems = append(r.problems, fmt.Sprintf("request %d shares a capture", id))
	}
	r.bufs[stdout], r.bufs[stderr] = true, true
	b := r.behaviors[id]
	rel := r.release[id]
	prereq := r.heldAck[b.prereq]
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.returned++
		r.mu.Unlock()
	}()

	ctlExe, ctlEnv := exe, slices.DeleteFunc(slices.Clone(env), func(e string) bool { return strings.HasPrefix(e, ctxChildEnv+"=") })
	if b.startFail {
		ctlExe = r.missing
	}
	if b.exitFail {
		ctlEnv = append(ctlEnv, ctxChildEnv+"=[]")
	}
	if b.awaitHeld {
		select {
		case <-prereq:
			r.event(fmt.Sprintf("prereq:%d", id))
		case <-time.After(r.ackWait):
			r.problem("request %d: request %d not acknowledged held before start", id, b.prereq)
		}
	}
	cmd := exec.Command(ctlExe, argv...)
	cmd.Env = ctlEnv
	t0 := time.Now()
	if err := cmd.Start(); err != nil {
		r.event(fmt.Sprintf("startfail:%d", id))
		return err
	}
	exited := make(chan error, 1)
	var life time.Duration
	go func() {
		err := cmd.Wait()
		life = time.Since(t0)
		exited <- err
	}()
	r.mu.Lock()
	r.started[id] = true
	r.active++
	r.peak = max(r.peak, r.active)
	r.events = append(r.events, fmt.Sprintf("start:%d", id))
	r.mu.Unlock()
	r.acks <- ctlAck{id, ctx}
	if r.held[id] {
		r.mu.Lock()
		if !r.heldAcked[id] {
			r.heldAcked[id] = true
			r.events = append(r.events, fmt.Sprintf("held:%d", id))
			close(r.heldAck[id])
		}
		r.mu.Unlock()
		select {
		case <-rel:
		case <-time.After(r.watchdog):
			r.problem("request %d not released within watchdog", id)
		}
	}
	var err error
	if b.readFault {
		_, err = io.Copy(stdout, io.MultiReader(strings.NewReader("partial\n"), ctlFaultReader{}))
	} else {
		out := b.out
		if out == "" {
			out = ctlReport(id, ctlFDs, ctlFDs, true)
		}
		stdout.Write([]byte(out))
		stderr.Write([]byte(fmt.Sprintf("stderr-%d\n", id)))
	}
	werr := <-exited // join the owned controller
	if life > time.Second {
		r.problem("controller %d lived %v", id, life)
	}
	if r.fixture != "" {
		if _, serr := os.Stat(r.fixture); serr != nil {
			r.problem("fixture missing when controller %d joined: %v", id, serr)
		}
	}
	if !reflect.DeepEqual(argv, argvIn) || !reflect.DeepEqual(env, envIn) {
		r.problem("request %d inputs changed while running", id)
	}
	r.mu.Lock()
	r.joined[id] = true
	r.active--
	r.events = append(r.events, fmt.Sprintf("join:%d", id))
	r.mu.Unlock()
	if err != nil {
		return fmt.Errorf("drain output: %w", err)
	}
	return werr
}

// ctlCtx is a context the driver makes Done after a start acknowledgement.
type ctlCtx struct {
	once sync.Once
	mu   sync.Mutex
	done chan struct{}
	err  error
}

func newCtlCtx() *ctlCtx { return &ctlCtx{done: make(chan struct{})} }

func (c *ctlCtx) trigger(err error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		close(c.done)
	})
}
func (c *ctlCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *ctlCtx) Done() <-chan struct{}       { return c.done }
func (c *ctlCtx) Value(any) any               { return nil }
func (c *ctlCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func ctlRequests(n int) [][]string {
	var reqs [][]string
	for i := range n {
		reqs = append(reqs, []string{"ctl", strconv.Itoa(i)})
	}
	return reqs
}

type ctxExecutor func(env []string, reqs [][]string, run ctxBatchRun) ([]result, error)

// Private negative-control executors; each breaks one property but, like
// ctxBatch, copies its inputs before the first start.

func ctxCopyInputs(env []string, reqs [][]string) ([]string, [][]string) {
	own := make([][]string, len(reqs))
	for i, r := range reqs {
		own[i] = slices.Clone(r)
	}
	return slices.Clone(env), own
}

func ctxSerialExecutor(env []string, reqs [][]string, run ctxBatchRun) ([]result, error) {
	env, reqs = ctxCopyInputs(env, reqs)
	res := make([]result, len(reqs))
	var first error
	for i, r := range reqs {
		var err error
		if res[i], err = run(env, r); err != nil && first == nil {
			first = err
		}
	}
	return res, first
}

func ctxUnlimitedExecutor(env []string, reqs [][]string, run ctxBatchRun) ([]result, error) {
	env, reqs = ctxCopyInputs(env, reqs)
	res := make([]result, len(reqs))
	errs := make([]error, len(reqs))
	var wg sync.WaitGroup
	for i, r := range reqs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res[i], errs[i] = run(env, r)
		}()
	}
	wg.Wait()
	return res, errors.Join(errs...)
}

// ctxWrongIndexExecutor is bounded but stores each result one index late.
func ctxWrongIndexExecutor(env []string, reqs [][]string, run ctxBatchRun) ([]result, error) {
	env, reqs = ctxCopyInputs(env, reqs)
	res := make([]result, len(reqs))
	var mu sync.Mutex
	var wrapped ctxBatchRun = func(env, args []string) (result, error) {
		r, err := run(env, args)
		mu.Lock()
		defer mu.Unlock()
		for i, q := range reqs {
			if slices.Equal(q, args) {
				res[(i+1)%len(reqs)] = r
			}
		}
		return r, err
	}
	_, err := ctxBatch(env, reqs, wrapped)
	return res, err
}

// ctxOmitDuplicateExecutor runs request 1 twice instead of request 2: the
// total count is unchanged.
func ctxOmitDuplicateExecutor(env []string, reqs [][]string, run ctxBatchRun) ([]result, error) {
	reqs = slices.Clone(reqs)
	reqs[2] = reqs[1]
	return ctxBatch(env, reqs, run)
}

type ctlObservation struct {
	category    string
	peak        int
	early4th    bool
	results     []result
	err         error
	rec         *ctlRec
	returnAfter bool // every join event precedes the return event
}

// ctlOracle classifies an observation independently of the executor.
func ctlOracle(rec *ctlRec, results []result, n int) string {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.peak > ctxBatchWidth {
		return "over_capacity"
	}
	if rec.peak < 2 {
		return "no_overlap"
	}
	for i := range n {
		if rec.attempts[i] != 1 {
			return "multiplicity"
		}
	}
	if len(rec.attempts) != n {
		return "multiplicity"
	}
	var want []result
	for i := range n {
		want = append(want, ctlWant(i))
	}
	if !reflect.DeepEqual(results, want) {
		return "result_mismatch"
	}
	if !reflect.DeepEqual(rec.started, rec.joined) {
		return "join_mismatch"
	}
	return ""
}

// driveConcurrency holds the first three starts behind a bounded barrier,
// checks a fourth has not started, mutates the caller's inputs, then
// releases in reverse index order and every later start at once.
func driveConcurrency(t *testing.T, exec ctxExecutor) ctlObservation {
	const n = 4
	held := map[int]bool{0: true, 1: true, 2: true, 3: true}
	rec := newCtlRec(t, n, held, nil)
	env := append(slices.Clone(os.Environ()), "CTL_MARK=orig")
	reqs := ctlRequests(n)
	ops := rec.ops()
	type out struct {
		res []result
		err error
	}
	done := make(chan out, 1)
	go func() {
		res, err := exec(env, reqs, func(env, args []string) (result, error) { return runCtxChildWith(ops, env, args) })
		rec.event("return")
		done <- out{res, err}
	}()
	var acked []int
	barrier := time.After(1500 * time.Millisecond)
collect:
	for len(acked) < 3 {
		select {
		case a := <-rec.acks:
			acked = append(acked, a.id)
		case <-barrier:
			break collect
		}
	}
	obs := ctlObservation{rec: rec}
	if len(acked) == 3 {
		select {
		case a := <-rec.acks:
			acked = append(acked, a.id)
			obs.early4th = true
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Caller-owned originals change only after dispatch has begun.
	reqs[3][1], reqs[0][0] = "mutated", "mutated"
	env[len(env)-1] = "CTL_MARK=mutated"
	// Release the held starts in reverse index order, each joined before the
	// next, so completion order differs from index order; later starts are
	// released as soon as they acknowledge.
	phase1 := slices.Clone(acked)
	handle := func(a ctlAck) {
		acked = append(acked, a.id)
		close(rec.release[a.id])
	}
	for _, id := range []int{3, 2, 1, 0} {
		if !slices.Contains(phase1, id) {
			continue
		}
		close(rec.release[id])
		wait := time.After(3 * time.Second)
	joined:
		for !rec.isJoined(id) {
			select {
			case a := <-rec.acks:
				handle(a)
			case <-wait:
				rec.problem("request %d not joined within 3s of release", id)
				break joined
			case <-time.After(2 * time.Millisecond):
			}
		}
	}
	var o out
	for o.res == nil && o.err == nil {
		select {
		case a := <-rec.acks:
			handle(a)
		case o = <-done:
		}
	}
	obs.results, obs.err = o.res, o.err
	obs.peak = rec.peak
	obs.category = ctlOracle(rec, o.res, n)
	rec.mu.Lock()
	ret := slices.Index(rec.events, "return")
	obs.returnAfter = ret >= 0
	for _, e := range rec.events[max(ret, 0):] {
		if strings.HasPrefix(e, "join:") {
			obs.returnAfter = false
		}
	}
	rec.mu.Unlock()
	return obs
}

func TestContextBatchConcurrency(t *testing.T) {
	parentEnv := os.Environ()
	obs := driveConcurrency(t, ctxBatch)
	rec := obs.rec
	if obs.category != "" || obs.err != nil || obs.peak != 3 || obs.early4th || !obs.returnAfter {
		t.Fatalf("bounded batch: category %q err %v peak %d fourth-early %v joins-before-return %v events %v problems %v",
			obs.category, obs.err, obs.peak, obs.early4th, obs.returnAfter, rec.events, rec.problems)
	}
	if len(rec.problems) != 0 || len(rec.bufs) != 8 {
		t.Fatalf("problems %v, distinct captures %d", rec.problems, len(rec.bufs))
	}
	for i := range 4 {
		wantEnv := append(slices.Clone(parentEnv), "CTL_MARK=orig", ctxChildEnv+`=["ctl","`+strconv.Itoa(i)+`"]`)
		if !reflect.DeepEqual(rec.env[i], wantEnv) || !reflect.DeepEqual(rec.parsedRequests[i], []string{"ctl", strconv.Itoa(i)}) ||
			!reflect.DeepEqual(rec.argv[i], []string{"-test.run=^TestContextChildProcess$", "-test.count=1"}) {
			t.Fatalf("request %d saw argv %q request %q env tail %q", i, rec.argv[i], rec.parsedRequests[i], rec.env[i][max(0, len(rec.env[i])-2):])
		}
	}
	for _, d := range rec.wantDurations {
		if d != 10*time.Second {
			t.Fatalf("context requested for %v, want 10s", d)
		}
	}
	if !reflect.DeepEqual(os.Environ(), parentEnv) {
		t.Fatal("parent environment changed")
	}

	// Negative controls must each be rejected for their own reason.
	for _, c := range []struct {
		name string
		exec ctxExecutor
		want string
	}{
		{"serial", ctxSerialExecutor, "no_overlap"},
		{"unlimited", ctxUnlimitedExecutor, "over_capacity"},
		{"wrong-index", ctxWrongIndexExecutor, "result_mismatch"},
	} {
		obs := driveConcurrency(t, c.exec)
		if obs.category != c.want {
			t.Fatalf("%s executor: oracle category %q, want %q (peak %d)", c.name, obs.category, c.want, obs.peak)
		}
		if !reflect.DeepEqual(obs.rec.started, obs.rec.joined) || !obs.returnAfter {
			t.Fatalf("%s executor: started %v joined %v", c.name, obs.rec.started, obs.rec.joined)
		}
	}
}

func TestContextBatchRoutingAndMatrix(t *testing.T) {
	var ids []string
	for _, m := range ctxUnsafeMatrix {
		ids = append(ids, m[0])
	}
	if len(ids) != 63 {
		t.Fatalf("hand-written matrix has %d entries", len(ids))
	}
	if err := ctxCheckMatrix(ids); err != nil {
		t.Fatal(err)
	}
	// Routing over all 63 requests with an in-process recorder: each result
	// must come back at its own index, every ID exactly once.
	var reqs [][]string
	for _, id := range ids {
		reqs = append(reqs, []string{"id", id})
	}
	recordRun := func(seen *[]string, mu *sync.Mutex) ctxBatchRun {
		return func(env, args []string) (result, error) {
			mu.Lock()
			*seen = append(*seen, args[1])
			mu.Unlock()
			return result{len(args[1]), "result " + args[1], ""}, nil
		}
	}
	check := func(exec ctxExecutor) (multiplicity, routing error) {
		var seen []string
		var mu sync.Mutex
		res, err := exec(nil, reqs, recordRun(&seen, &mu))
		if err != nil {
			return err, err
		}
		slices.SortFunc(seen, func(a, b string) int { return slices.Index(ids, a) - slices.Index(ids, b) })
		multiplicity = ctxCheckMatrix(seen)
		for i, id := range ids {
			if res[i] != (result{len(id), "result " + id, ""}) {
				return multiplicity, fmt.Errorf("result_mismatch at %d (%s): %+v", i, id, res[i])
			}
		}
		return multiplicity, nil
	}
	if m, r := check(ctxBatch); m != nil || r != nil {
		t.Fatalf("bounded batch: %v / %v", m, r)
	}
	if m, _ := check(ctxOmitDuplicateExecutor); m == nil || !strings.HasPrefix(m.Error(), "multiplicity:") ||
		!strings.Contains(m.Error(), ids[1]+" ran 2 want 1") || !strings.Contains(m.Error(), ids[2]+" ran 0 want 1") {
		t.Fatalf("omission+duplicate not rejected by multiplicity: %v", m)
	}
	if _, r := check(ctxWrongIndexExecutor); r == nil || !strings.HasPrefix(r.Error(), "result_mismatch") {
		t.Fatalf("wrong-index routing not rejected: %v", r)
	}
	// The expected-response rules distinguish each class.
	good := map[string]result{
		"role/fifo/lead/lead":     {1, "", "baw: source_unavailable\n"},
		"role/symlink/lead/lead":  {1, "", "baw: source_symlink\n"},
		"role/fifo/lead/worker":   {0, `{"path":"workflow/roles/worker.md"}`, ""},
		"legacy/fifo/worker":      {0, "Source: workflow/roles/lead.md\n", "warning\n"},
		"ancestor/docs/reviewer":  {1, "", "baw: source_symlink\n"},
		"common/fifo/AGENTS.md/w": {1, "", "baw: source_unavailable\n"},
	}
	for id, r := range good {
		err := ctxWantMatrix(id, r)
		if (err == nil) == (id == "common/fifo/AGENTS.md/w") {
			t.Fatalf("%s: %v", id, err)
		}
	}
	for id, r := range map[string]result{
		"role/fifo/lead/lead":   {1, "", "baw: source_symlink\n"},
		"role/fifo/lead/worker": {0, `{"path":"workflow/roles/worker.md"} workflow/roles/lead.md`, ""},
		"legacy/fifo/worker":    {0, "worker.md", ""},
	} {
		if ctxWantMatrix(id, r) == nil {
			t.Fatalf("%s accepted wrong response %+v", id, r)
		}
	}
	// The argument-vector rules accept only the original order, target and
	// role of each request form.
	const d = "/fixture"
	for _, c := range []struct {
		id   string
		argv []string
		ok   bool
	}{
		{"role/fifo/lead/worker", []string{"context", "--repo", d, "--role", "worker", "--json"}, true},
		{"role/fifo/lead/worker", []string{"context", "--repo", d, "--json", "--role", "worker"}, false},
		{"role/fifo/lead/worker", []string{"context", "--repo", d, "--role", "lead", "--json"}, false},
		{"role/fifo/lead/worker", []string{"context", "--repo", "/other", "--role", "worker", "--json"}, false},
		{"legacy/fifo/worker", []string{"inspect", "--repo", d}, true},
		{"legacy/fifo/worker", []string{"inspect", "--repo", d, "--json"}, false},
		{"common/fifo/AGENTS.md/reviewer", []string{"context", "--repo", d, "--role", "reviewer"}, true},
		{"common/fifo/AGENTS.md/reviewer", []string{"context", "--repo", d, "--role", "reviewer", "--json"}, false},
		{"ancestor/docs/lead", []string{"context", "--repo", d, "--role", "lead"}, true},
		{"ancestor/docs/lead", []string{"context", "--role", "lead", "--repo", d}, false},
	} {
		if err := ctxWantArgv(c.id, d, c.argv); (err == nil) != c.ok {
			t.Fatalf("%s %q: %v", c.id, c.argv, err)
		}
	}
}

// ctlStartFailOrder checks from the recorded events that request id's failed
// Start came after request prereq had started and acknowledged its hold,
// after id observed that acknowledgement, and before prereq was joined.
func ctlStartFailOrder(events []string, prereq, id int) error {
	at := func(f string, n int) int { return slices.Index(events, fmt.Sprintf(f, n)) }
	s, h, p, f, j := at("start:%d", prereq), at("held:%d", prereq), at("prereq:%d", id), at("startfail:%d", id), at("join:%d", prereq)
	if s < 0 || h < 0 || p < 0 || f < 0 || j < 0 || !(s < h && h < p && p < f && f < j) {
		return fmt.Errorf("start failure of request %d not after held request %d: %v", id, prereq, events)
	}
	return nil
}

func TestContextBatchFailureJoins(t *testing.T) {
	plausible := ctlReport(1, ctlFDs, ctlFDs, true)
	cases := []struct {
		name      string
		b         map[int]ctlBehavior
		trigger   map[int]error
		wantIdx   int
		wantKind  string
		wantCause error
	}{
		{"later-start-failure", map[int]ctlBehavior{1: {startFail: true, awaitHeld: true, prereq: 0}}, nil, 1, ctxErrProcess, nil},
		// Negative control: request 2 is never held, so the gate must report
		// the missing acknowledgement within its bound and the order fails.
		{"start-failure-missing-held-ack", map[int]ctlBehavior{1: {startFail: true, awaitHeld: true, prereq: 2}}, nil, 1, ctxErrProcess, nil},
		{"nonzero-exit", map[int]ctlBehavior{1: {exitFail: true, out: "no report\n"}}, nil, 1, ctxErrProcess, nil},
		{"missing-report", map[int]ctlBehavior{1: {out: "no report\n"}}, nil, 1, ctxErrReport, nil},
		{"malformed-report", map[int]ctlBehavior{1: {out: ctxChildPrefix + "{bad\n"}}, nil, 1, ctxErrReport, nil},
		{"truncated-report", map[int]ctlBehavior{1: {out: ctxChildPrefix + `{"Code":0,"Stdout":"` + strings.Repeat("a", 1<<16) + `"}` + "\n"}}, nil, 1, ctxErrReport, nil},
		{"empty-descriptors", map[int]ctlBehavior{1: {out: ctlReport(1, nil, nil, true)}}, nil, 1, ctxErrFacts, nil},
		{"changed-descriptors", map[int]ctlBehavior{1: {out: ctlReport(1, ctlFDs, ctlFDs[:3], true)}}, nil, 1, ctxErrFacts, nil},
		{"repeat-differs", map[int]ctlBehavior{1: {out: ctlReport(1, ctlFDs, ctlFDs, false)}}, nil, 1, ctxErrFacts, nil},
		{"cancel", nil, map[int]error{1: context.Canceled}, 1, ctxErrContext, context.Canceled},
		{"deadline", nil, map[int]error{1: context.DeadlineExceeded}, 1, ctxErrContext, context.DeadlineExceeded},
		{"reader-failure", map[int]ctlBehavior{1: {readFault: true}}, nil, 1, ctxErrProcess, errCtlRead},
		{"deadline+exit+plausible", map[int]ctlBehavior{1: {exitFail: true, out: plausible}}, map[int]error{1: context.DeadlineExceeded}, 1, ctxErrContext, context.DeadlineExceeded},
		{"exit+plausible", map[int]ctlBehavior{1: {exitFail: true, out: plausible}}, nil, 1, ctxErrProcess, nil},
		{"malformed+bad-descriptors", map[int]ctlBehavior{1: {out: ctxChildPrefix + "{bad\n" + ctlReport(1, nil, nil, false)}}, nil, 1, ctxErrReport, nil},
		{"first-error-by-index", map[int]ctlBehavior{1: {out: ctlReport(1, nil, nil, true)}}, map[int]error{3: context.Canceled}, 1, ctxErrFacts, nil},
	}
	for _, c := range cases {
		const n = 4
		held := map[int]bool{0: true}
		for id := range c.trigger {
			held[id] = true
		}
		rec := newCtlRec(t, n, held, c.b)
		rec.fixture = filepath.Join(t.TempDir(), "fixture")
		if err := os.Mkdir(rec.fixture, 0o700); err != nil {
			t.Fatal(err)
		}
		ops := rec.ops()
		type out struct {
			res []result
			err error
		}
		done := make(chan out, 1)
		go func() {
			res, err := ctxBatch(os.Environ(), ctlRequests(n), func(env, args []string) (result, error) {
				return runCtxChildWith(ops, env, args)
			})
			rec.event("return")
			done <- out{res, err}
		}()
		// Trigger contexts after their start acknowledgements, then wait
		// until every request except the held first one has returned.
		var failures []string
		deadline := time.After(3 * time.Second)
		pending := len(c.trigger) + 1
		for pending > 0 {
			select {
			case a := <-rec.acks:
				if err, ok := c.trigger[a.id]; ok {
					a.ctx.(*ctlCtx).trigger(err)
					close(rec.release[a.id])
					pending--
				} else if a.id == 0 {
					pending--
				}
			case <-deadline:
				failures = append(failures, "acknowledgements not received in 3s")
				pending = 0
			}
		}
		for wait := time.Now(); ; time.Sleep(5 * time.Millisecond) {
			rec.mu.Lock()
			r := rec.returned
			rec.mu.Unlock()
			if r == n-1 || time.Since(wait) > 3*time.Second {
				break
			}
		}
		// The held first child keeps the batch from returning.
		select {
		case <-done:
			failures = append(failures, "batch returned while request 0 was held")
		case <-time.After(50 * time.Millisecond):
		}
		rec.mu.Lock()
		early := slices.Contains(rec.events, "return") || rec.joined[0]
		rec.mu.Unlock()
		if early {
			failures = append(failures, "return or join of request 0 before release")
		}
		close(rec.release[0])
		o := <-done
		// Parent actions after the joined batch.
		rec.event("oracle")
		if err := os.RemoveAll(rec.fixture); err != nil {
			t.Fatal(err)
		}
		rec.event("fixture-delete")
		rec.event("env-restore")

		var ce *ctxChildError
		if !errors.As(o.err, &ce) || ce.kind != c.wantKind || !strings.Contains(ce.msg, fmt.Sprintf(`["ctl" "%d"]`, c.wantIdx)) ||
			(c.wantCause != nil && !errors.Is(o.err, c.wantCause)) {
			failures = append(failures, fmt.Sprintf("error %v", o.err))
		}
		for i := range n {
			if rec.attempts[i] != 1 {
				failures = append(failures, fmt.Sprintf("request %d attempted %d times", i, rec.attempts[i]))
			}
		}
		if !reflect.DeepEqual(rec.started, rec.joined) {
			failures = append(failures, fmt.Sprintf("started %v joined %v", rec.started, rec.joined))
		}
		if c.b[1].startFail && rec.started[1] {
			failures = append(failures, "start failure has a handle")
		}
		ret := slices.Index(rec.events, "return")
		for _, e := range rec.events[ret:] {
			if strings.HasPrefix(e, "join:") || strings.HasPrefix(e, "start:") {
				failures = append(failures, "event "+e+" after return")
			}
		}
		if !slices.Equal(rec.events[ret:], []string{"return", "oracle", "fixture-delete", "env-restore"}) {
			failures = append(failures, fmt.Sprintf("parent events %v", rec.events[ret:]))
		}
		missingAck := false
		for id, b := range c.b {
			if !b.awaitHeld {
				continue
			}
			err := ctlStartFailOrder(rec.events, b.prereq, id)
			if held[b.prereq] != (err == nil) {
				failures = append(failures, fmt.Sprintf("held prerequisite %v, order check %v", held[b.prereq], err))
			}
			missingAck = missingAck || !held[b.prereq]
		}
		const noAck = "not acknowledged held before start"
		for _, p := range rec.problems {
			if !strings.Contains(p, "watchdog") && !(missingAck && strings.Contains(p, noAck)) {
				failures = append(failures, p)
			}
		}
		if missingAck && !slices.ContainsFunc(rec.problems, func(p string) bool { return strings.Contains(p, noAck) }) {
			failures = append(failures, "missing held acknowledgement not reported")
		}
		if len(failures) != 0 {
			t.Fatalf("%s: %v (events %v)", c.name, failures, rec.events)
		}
	}
}

func TestContextChildRunnerCompatibility(t *testing.T) {
	home := env(t)
	_ = home
	parentEnv := os.Environ()
	dir := t.TempDir()
	args := []string{"context", "--repo", dir, "--role", "worker"}
	// Hand-written baseline: a non-repository fails with this fixed code.
	want := result{1, "", "baw: repository_unavailable\n"}
	if got := runCtxChild(t, args...); got != want {
		t.Fatalf("wrapper: %+v want %+v", got, want)
	}
	got, err := runCtxChildWith(newCtxChildOps(context.WithTimeout, exec.CommandContext, time.Second), os.Environ(), args)
	if err != nil || got != want {
		t.Fatalf("default runner: %+v %v want %+v", got, err, want)
	}

	// Fake operations: recorded order, exact argv/env, diagnostics and kinds.
	var calls []string
	type spec struct {
		marshalErr error
		exe        string
		trigger    error
		runErr     error
		stdout     string
		stderr     string
	}
	run := func(s spec) (result, error, string) {
		calls = nil
		var gotArgv, gotEnv []string
		ops := ctxChildOps{
			marshal: func(v any) ([]byte, error) {
				calls = append(calls, "marshal")
				if s.marshalErr != nil {
					return nil, s.marshalErr
				}
				return json.Marshal(v)
			},
			executable: func() string { calls = append(calls, "executable"); return s.exe },
			newContext: func(d time.Duration) (context.Context, context.CancelFunc) {
				calls = append(calls, fmt.Sprintf("context %v", d))
				c := newCtlCtx()
				return c, func() { calls = append(calls, "cancel"); c.trigger(context.Canceled) }
			},
			run: func(ctx context.Context, exe string, argv, env []string, stdout, stderr *cappedOutput) error {
				calls = append(calls, "run "+exe)
				gotArgv, gotEnv = argv, env
				stdout.Write([]byte(s.stdout))
				stderr.Write([]byte(s.stderr))
				if s.trigger != nil {
					ctx.(*ctlCtx).trigger(s.trigger)
				}
				return s.runErr
			},
		}
		r, err := runCtxChildWith(ops, []string{"A=1", "B=2"}, []string{"x"})
		if strings.HasPrefix(strings.Join(calls, ","), "marshal,executable,context 10s,run ") {
			if !reflect.DeepEqual(gotArgv, []string{"-test.run=^TestContextChildProcess$", "-test.count=1"}) ||
				!reflect.DeepEqual(gotEnv, []string{"A=1", "B=2", ctxChildEnv + `=["x"]`}) {
				t.Fatalf("argv %q env %q", gotArgv, gotEnv)
			}
		}
		kind := ""
		var ce *ctxChildError
		if errors.As(err, &ce) {
			kind = ce.kind
		}
		return r, err, kind
	}
	good := ctxChildPrefix + `{"Code":2,"Stdout":"o","Stderr":"e","FDBefore":["0"],"FDAfter":["0"],"SameAsWarmUp":true}`
	exe := "/abs/test.bin"
	for _, c := range []struct {
		name      string
		s         spec
		want      result
		wantKind  string
		wantMsg   string // exact, or prefix and suffix around "after "
		wantCalls string
	}{
		{"nonzero CLI code is a result", spec{exe: exe, stdout: "x\n" + good + "\ntrailing\n" + ctxChildPrefix + "{}\n"}, result{2, "o", "e"}, "", "",
			"marshal,executable,context 10s,run /abs/test.bin,cancel"},
		{"early report then junk past 64KiB", spec{exe: exe, stdout: good + "\n" + strings.Repeat("j", 1<<17)}, result{2, "o", "e"}, "", "", ""},
		{"marshal failure", spec{marshalErr: errors.New("no marshal"), exe: exe}, result{}, ctxErrSetup, "no marshal", "marshal"},
		{"relative executable", spec{exe: "rel/test.bin"}, result{}, ctxErrSetup, `test binary path "rel/test.bin" is not absolute`, "marshal,executable"},
		{"process", spec{exe: exe, runErr: errors.New("boom"), stdout: good, stderr: "e"}, result{}, ctxErrProcess,
			`context child ["x"]: boom` + "\nstdout " + strconv.Quote(good) + "\nstderr \"e\"", ""},
		{"context wins", spec{exe: exe, runErr: errors.New("killed"), trigger: context.DeadlineExceeded, stdout: good}, result{}, ctxErrContext,
			`context child ["x"] not finished within 10s; killed and joined after |(killed)`, ""},
		{"report", spec{exe: exe, stdout: "junk"}, result{}, ctxErrReport, `context child ["x"] reported no result: "junk"`, ""},
		{"truncated report", spec{exe: exe, stdout: strings.Repeat("j", 1<<16-5) + good}, result{}, ctxErrReport, "", ""},
		{"facts", spec{exe: exe, stdout: ctxChildPrefix + `{"Code":0,"FDBefore":[],"FDAfter":[],"SameAsWarmUp":true}`}, result{}, ctxErrFacts,
			`context child ["x"]: descriptors before [] after [], repeat identical true`, ""},
	} {
		r, err, kind := run(c.s)
		if r != c.want || kind != c.wantKind || (err == nil) != (c.wantKind == "") {
			t.Fatalf("%s: %+v %v kind %q", c.name, r, err, kind)
		}
		if c.wantMsg != "" {
			pre, suf, split := strings.Cut(c.wantMsg, "|")
			if (!split && err.Error() != c.wantMsg) || (split && (!strings.HasPrefix(err.Error(), pre) || !strings.HasSuffix(err.Error(), suf))) {
				t.Fatalf("%s: diagnostic %q want %q", c.name, err.Error(), c.wantMsg)
			}
		}
		if c.wantCalls != "" && strings.Join(calls, ",") != c.wantCalls {
			t.Fatalf("%s: calls %v want %s", c.name, calls, c.wantCalls)
		}
	}
	if !reflect.DeepEqual(os.Environ(), parentEnv) {
		t.Fatal("parent environment changed")
	}
}

// --- name-only descriptor observation (openFDs) ---
//
// openFDs lists the entry names of /dev/fd once, with no per-entry metadata
// query: os.ReadDir's per-entry lstat on darwin can drop entries, including
// standard handles that fcntl confirms open. Every open, read, close or name
// error fails; nothing is filtered, retried or reconciled. The listing is not
// atomic and may include the observer's own directory descriptor.

const ctxFDDirPath = "/dev/fd"

// ctxFDDir is the observer's only directory access: names and close.
type ctxFDDir interface {
	Readdirnames(n int) ([]string, error)
	Close() error
}

// ctxFDOps is passed per call; there is no package-level observer hook.
type ctxFDOps struct {
	open func(path string) (ctxFDDir, error)
}

func ctxOpenFDDir(path string) (ctxFDDir, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// ctxObserveFDs opens the fixed directory once, reads every name, always
// closes it once (a read error wins over a close error), rejects non-numeric
// and duplicate names and returns the names sorted lexicographically.
func ctxObserveFDs(ops ctxFDOps) ([]string, error) {
	d, err := ops.open(ctxFDDirPath)
	if err != nil {
		return nil, fmt.Errorf("descriptor observation: open %s: %w", ctxFDDirPath, err)
	}
	names, rerr := d.Readdirnames(-1)
	cerr := d.Close()
	if rerr != nil {
		return nil, fmt.Errorf("descriptor observation: read %s: %w", ctxFDDirPath, rerr)
	}
	if cerr != nil {
		return nil, fmt.Errorf("descriptor observation: close %s: %w", ctxFDDirPath, cerr)
	}
	seen := map[string]bool{}
	for _, n := range names {
		if v, err := strconv.Atoi(n); err != nil || v < 0 || strconv.Itoa(v) != n {
			return nil, fmt.Errorf("descriptor observation: non-numeric name %q in %s", n, ctxFDDirPath)
		}
		if seen[n] {
			return nil, fmt.Errorf("descriptor observation: duplicate name %q in %s", n, ctxFDDirPath)
		}
		seen[n] = true
	}
	slices.Sort(names)
	return names, nil
}

// ctlFDDir is an injected directory. Its metadata methods are never reachable
// through ctxFDDir; any call would still be recorded.
type ctlFDDir struct {
	names             []string
	readErr, closeErr error
	calls             *[]string
}

func (d *ctlFDDir) Readdirnames(n int) ([]string, error) {
	*d.calls = append(*d.calls, fmt.Sprintf("readdirnames %d", n))
	return slices.Clone(d.names), d.readErr
}

func (d *ctlFDDir) Close() error {
	*d.calls = append(*d.calls, "close")
	return d.closeErr
}

func (d *ctlFDDir) Stat() (os.FileInfo, error) {
	*d.calls = append(*d.calls, "stat")
	return nil, errors.New("metadata")
}

func (d *ctlFDDir) ReadDir(int) ([]os.DirEntry, error) {
	*d.calls = append(*d.calls, "readdir")
	return nil, errors.New("metadata")
}

var (
	errCtlFDOpen  = errors.New("fake descriptor directory open failed")
	errCtlFDRead  = errors.New("fake descriptor directory read failed")
	errCtlFDClose = errors.New("fake descriptor directory close failed")
)

// Real child probe of the default observer: BAW_CLI_TEST_FD_PROBE selects the
// mode of a child of this test binary running only TestContextFDObservation.
const (
	ctxFDProbeEnv    = "BAW_CLI_TEST_FD_PROBE"
	ctxFDProbePrefix = "CLI-FD-PROBE "
)

// ctxFDProbe is the child's independent evidence: fcntl(F_GETFD) results for
// the standard handles at each observation and for its own held descriptor.
type ctxFDProbe struct {
	StandardErrs                  []string
	Held                          int
	HeldOpen, HeldClosedAfter     bool
	WarmUp, Before, During, After []string
}

func ctxFcntl(fd, cmd, arg int) (int, error) {
	r, _, e := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(cmd), uintptr(arg))
	if e != 0 {
		return -1, e
	}
	return int(r), nil
}

// ctxFDProbeChild observes (warm-up, before), duplicates an owned /dev/null
// descriptor to an unused number >= 64, observes while it is held, closes
// only that descriptor and observes again. Mode "held" reports before/during
// as the child result, "restored" before/after; "error" fails an observation
// the way TestContextChildProcess does.
func ctxFDProbeChild(t *testing.T, mode string) {
	if mode == "error" {
		if _, err := ctxObserveFDs(ctxFDOps{open: func(string) (ctxFDDir, error) { return nil, errCtlFDOpen }}); err != nil {
			t.Fatal(err)
		}
		return
	}
	var p ctxFDProbe
	observe := func(stage string) []string {
		names, err := openFDs()
		if err != nil {
			t.Fatal(err)
		}
		for fd := range 3 {
			if _, err := ctxFcntl(fd, syscall.F_GETFD, 0); err != nil {
				p.StandardErrs = append(p.StandardErrs, fmt.Sprintf("%s fd %d: %v", stage, fd, err))
			}
		}
		return names
	}
	p.WarmUp = observe("warm-up")
	p.Before = observe("before")
	own, err := syscall.Open(os.DevNull, syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	held, err := ctxFcntl(own, syscall.F_DUPFD_CLOEXEC, 64)
	if cerr := syscall.Close(own); err != nil || cerr != nil {
		if err == nil {
			syscall.Close(held)
		}
		t.Fatalf("duplicate owned descriptor: %v, close %v", err, cerr)
	}
	p.Held = held
	_, err = ctxFcntl(held, syscall.F_GETFD, 0)
	p.HeldOpen = err == nil
	p.During = observe("during")
	cerr := syscall.Close(held)
	_, err = ctxFcntl(held, syscall.F_GETFD, 0)
	p.HeldClosedAfter = errors.Is(err, syscall.EBADF)
	if cerr != nil {
		t.Fatal(cerr)
	}
	p.After = observe("after")
	after := p.After
	if mode == "held" {
		after = p.During
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	r, err := json.Marshal(ctxChildResult{0, "", "", p.Before, after, true})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n%s%s\n%s%s\n", ctxFDProbePrefix, b, ctxChildPrefix, r)
}

func TestContextFDObservation(t *testing.T) {
	if mode := os.Getenv(ctxFDProbeEnv); mode != "" {
		ctxFDProbeChild(t, mode)
		return
	}
	// Injected per-call directories against hand-written expectations.
	full := []string{"open /dev/fd", "readdirnames -1", "close"}
	for _, c := range []struct {
		name      string
		names     []string
		openErr   error
		readErr   error
		closeErr  error
		want      []string
		wantErr   string
		wantIs    error
		wantNotIs error
		wantCalls []string
	}{
		{"sorted, all names kept", []string{"64", "2", "10", "0", "1", "3", "11", "5"}, nil, nil, nil,
			[]string{"0", "1", "10", "11", "2", "3", "5", "64"}, "", nil, nil, full},
		{"standard only", []string{"2", "1", "0"}, nil, nil, nil, []string{"0", "1", "2"}, "", nil, nil, full},
		{"word", []string{"0", "fd"}, nil, nil, nil, nil, `non-numeric name "fd"`, nil, nil, full},
		{"empty name", []string{"0", ""}, nil, nil, nil, nil, `non-numeric name ""`, nil, nil, full},
		{"negative", []string{"0", "-1"}, nil, nil, nil, nil, `non-numeric name "-1"`, nil, nil, full},
		{"leading zero", []string{"0", "01"}, nil, nil, nil, nil, `non-numeric name "01"`, nil, nil, full},
		{"sign", []string{"0", "+1"}, nil, nil, nil, nil, `non-numeric name "+1"`, nil, nil, full},
		{"suffix", []string{"0", "1a"}, nil, nil, nil, nil, `non-numeric name "1a"`, nil, nil, full},
		{"duplicate", []string{"0", "1", "2", "1"}, nil, nil, nil, nil, `duplicate name "1"`, nil, nil, full},
		{"open failure", nil, errCtlFDOpen, nil, nil, nil, "descriptor observation: open /dev/fd: ", errCtlFDOpen, nil, []string{"open /dev/fd"}},
		{"read failure with partial names", []string{"0", "1"}, nil, errCtlFDRead, nil, nil, "descriptor observation: read /dev/fd: ", errCtlFDRead, nil, full},
		{"read and close failure: read wins", []string{"0"}, nil, errCtlFDRead, errCtlFDClose, nil, "descriptor observation: read /dev/fd: ", errCtlFDRead, errCtlFDClose, full},
		{"close failure", []string{"0", "1", "2"}, nil, nil, errCtlFDClose, nil, "descriptor observation: close /dev/fd: ", errCtlFDClose, nil, full},
	} {
		var calls []string
		got, err := ctxObserveFDs(ctxFDOps{open: func(path string) (ctxFDDir, error) {
			calls = append(calls, "open "+path)
			if c.openErr != nil {
				return nil, c.openErr
			}
			return &ctlFDDir{names: c.names, readErr: c.readErr, closeErr: c.closeErr, calls: &calls}, nil
		}})
		if !slices.Equal(calls, c.wantCalls) {
			t.Fatalf("%s: calls %q want %q", c.name, calls, c.wantCalls)
		}
		if c.wantErr == "" {
			if err != nil || !reflect.DeepEqual(got, c.want) {
				t.Fatalf("%s: %q %v want %q", c.name, got, err, c.want)
			}
			continue
		}
		if err == nil || got != nil || !strings.Contains(err.Error(), c.wantErr) ||
			(c.wantIs != nil && !errors.Is(err, c.wantIs)) || (c.wantNotIs != nil && errors.Is(err, c.wantNotIs)) {
			t.Fatalf("%s: %q %v want error %q", c.name, got, err, c.wantErr)
		}
	}

	// Real joined children through the existing runner: the request's argv
	// selects this test in probe mode; everything else is the default runner.
	probe := func(mode string) (ctxFDProbe, result, error) {
		base := newCtxChildOps(context.WithTimeout, exec.CommandContext, time.Second)
		ops := base
		var stdout string
		ops.run = func(ctx context.Context, exe string, argv, env []string, out, errb *cappedOutput) error {
			err := base.run(ctx, exe, []string{"-test.run=^TestContextFDObservation$", "-test.count=1"},
				append(slices.Clone(env), ctxFDProbeEnv+"="+mode), out, errb)
			stdout = out.b.String()
			return err
		}
		r, err := runCtxChildWith(ops, os.Environ(), []string{"probe", mode})
		var p ctxFDProbe
		_, line, ok := strings.Cut(stdout, ctxFDProbePrefix)
		line, _, _ = strings.Cut(line, "\n")
		if mode != "error" && (!ok || json.Unmarshal([]byte(line), &p) != nil) {
			t.Fatalf("%s probe reported no evidence: %q (%v)", mode, stdout, err)
		}
		return p, r, err
	}
	// check uses fcntl evidence, not the observer, to decide what each list
	// must contain.
	check := func(mode string, p ctxFDProbe) {
		t.Helper()
		h := strconv.Itoa(p.Held)
		withHeld := append(slices.Clone(p.Before), h)
		slices.Sort(withHeld)
		if len(p.StandardErrs) != 0 || p.Held < 64 || !p.HeldOpen || !p.HeldClosedAfter {
			t.Fatalf("%s probe: standard %v held %d open %v closed after %v", mode, p.StandardErrs, p.Held, p.HeldOpen, p.HeldClosedAfter)
		}
		for _, l := range [][]string{p.WarmUp, p.Before, p.During, p.After} {
			for _, fd := range []string{"0", "1", "2"} {
				if !slices.Contains(l, fd) {
					t.Fatalf("%s probe: open standard handle %s missing from %v", mode, fd, l)
				}
			}
		}
		if !slices.Equal(p.WarmUp, p.Before) || slices.Contains(p.Before, h) || !slices.Equal(p.During, withHeld) || !slices.Equal(p.After, p.Before) {
			t.Fatalf("%s probe: warm-up %v before %v during %v after %v held %s", mode, p.WarmUp, p.Before, p.During, p.After, h)
		}
	}
	// The held extra descriptor must be rejected by the runner's facts check.
	p, r, err := probe("held")
	check("held", p)
	var ce *ctxChildError
	if !errors.As(err, &ce) || ce.kind != ctxErrFacts || r != (result{}) ||
		ce.msg != fmt.Sprintf("context child %q: descriptors before %v after %v, repeat identical true", []string{"probe", "held"}, p.Before, p.During) {
		t.Fatalf("held descriptor not rejected as facts_invalid: %+v %v", r, err)
	}
	// After the owned handle closes, the lists are equal again.
	p, r, err = probe("restored")
	check("restored", p)
	if err != nil || r != (result{}) {
		t.Fatalf("restored descriptors: %+v %v", r, err)
	}
	// An observation error fails the child; the parent sees a process failure.
	_, r, err = probe("error")
	var ee *exec.ExitError
	if !errors.As(err, &ce) || ce.kind != ctxErrProcess || !errors.As(err, &ee) || r != (result{}) ||
		!strings.Contains(ce.msg, "descriptor observation: open /dev/fd: "+errCtlFDOpen.Error()) {
		t.Fatalf("observation error not a process failure: %+v %v", r, err)
	}
}
