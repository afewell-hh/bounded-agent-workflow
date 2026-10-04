package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// Fixed execution bounds.
const (
	Deadline  = 620 * time.Second
	StdoutCap = 1 << 20
	StderrCap = 64 << 10
)

// Request is a syntactically valid execute request.
type Request struct {
	Repo     string
	StateDir string
	RunID    string
	Plan     string
	JSON     bool
}

// Internal test seams; production code never sets them.
var (
	runObserved = proc.RunObserved
	inspectTop  = inspect.InspectTop
	now         = time.Now
)

// Execute validates, acquires the exclusive attempt, runs the worker and,
// only after a usable zero exit and a usable post-worker inspection, the
// verifier. It publishes the result and then delivers its exact output to
// stdout. passed is true only for a delivered verification_passed result; a
// nil error with passed false is a delivered non-passing result. Errors from
// the state and inspect packages are returned unchanged.
func Execute(ctx context.Context, req Request, stdout io.Writer) (passed bool, err error) {
	cancelled := func() bool { return ctx.Err() != nil }
	// State root and existing namespace safety, without creating anything.
	root, err := state.OpenRoot(req.StateDir)
	if err != nil {
		return false, err
	}
	rootP, err := rootPath(req.StateDir)
	if err != nil {
		return false, err
	}
	ns := filepath.Join(rootP, Namespace)
	if err := checkNamespace(ns); err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	planBytes, plan, err := ReadPlan(req.Plan)
	if err != nil {
		return false, err
	}
	planPath, err := physical(req.Plan)
	if err != nil {
		return false, fail(CodePlanUnavailable)
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	rec, err := root.Read(req.RunID)
	if err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	pkt, top, err := inspectTop(inspect.Options{Repo: req.Repo, Limits: inspect.DefaultLimits})
	if err != nil {
		return false, err
	}
	repo := pkt.Repository
	if repo.HeadState != "present" || repo.Head == nil || repo.ObjectFormat != rec.RepositoryObjectFormat ||
		*repo.Head != rec.RepositoryHead {
		return false, fail(CodeCheckpointMismatch)
	}
	topInfo, err := os.Stat(top)
	if err != nil {
		return false, fail(CodeUnsafeLayout)
	}
	if within(top, rootP) || within(top, planPath) || within(top, ns) || within(ns, top) {
		return false, fail(CodeUnsafeLayout)
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	workerExe, err := checkExecutable(plan.Worker.Executable)
	if err != nil {
		return false, err
	}
	verifierExe, err := checkExecutable(plan.Verification.Executable)
	if err != nil {
		return false, err
	}
	attempt := filepath.Join(ns, req.RunID)
	if err := checkAttempt(attempt); err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}

	// Acquisition. After the exclusive ID mkdir every failure is uncertain.
	if _, err := acquire(rootP, ns, attempt, cancelled); err != nil {
		return false, err
	}
	sum := sha256.Sum256(planBytes)
	createdAt := now().UTC().Format(state.TimeLayout)
	intent := Intent{1, req.RunID, "execution_intent", rec.TicketURL, rec.ScopeSHA256, rec.PolicyCommit,
		rec.RepositoryObjectFormat, rec.RepositoryHead, hex.EncodeToString(sum[:]), createdAt}
	data, err := marshal(intent)
	if err != nil || !validIntent(data, req.RunID) || cancelled() {
		return false, fail(CodeUncertain)
	}
	if err := publish(attempt, "intent", data); err != nil {
		return false, fail(CodeUncertain)
	}

	// Durable intent: the deadline starts now. Observations below are fixed
	// once the outcome is selected; later cancellation is ignored.
	dctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	x := &runner{top: top, attempt: attempt}
	res := Result{SchemaVersion: 1, RunID: req.RunID, Operation: "run_execute", Authority: "not_evaluated",
		Readiness: "not_evaluated", Worker: notStarted(), Verification: notStarted(),
		Repository:   Repository{ObjectFormat: rec.RepositoryObjectFormat, BeforeHead: rec.RepositoryHead},
		ReceiptState: "recorded", CreatedAt: createdAt}
	res.Outcome = OutcomeWorkerUnverified
	if dctx.Err() == nil {
		x.pipeline(dctx, plan, workerExe, verifierExe, topInfo, &res)
	}
	res.CompletedAt = now().UTC().Format(state.TimeLayout)

	// Serialize once, self-validate, render, cap, publish, deliver.
	data, err = marshal(res)
	if err != nil || !validResult(data, req.RunID) {
		return false, fail(CodeUncertain)
	}
	out := data
	if !req.JSON {
		out = RenderText(res)
	}
	if len(out) > MaxOutput || at("output-limit") != nil {
		return false, fail(CodeUncertain)
	}
	if err := publish(attempt, "result", data); err != nil {
		return false, fail(CodeUncertain)
	}
	if err := at("deliver"); err != nil {
		return false, fail(CodeUncertain)
	}
	if n, err := stdout.Write(out); err != nil || n != len(out) {
		return false, fail(CodeUncertain)
	}
	return res.Outcome == OutcomeVerificationPassed, nil
}

func notStarted() Program { return Program{State: StateNotStarted} }

func exited(code int) Program { return Program{State: StateExited, ExitCode: &code} }

type runner struct {
	top, attempt string
}

// pipeline applies the §7 transition rules to res.
func (x *runner) pipeline(ctx context.Context, plan Plan, workerExe, verifierExe string, topInfo fs.FileInfo, res *Result) {
	wobs := x.run(ctx, plan.Worker, workerExe, "worker")
	switch {
	case !wobs.Started:
		res.Outcome = OutcomeWorkerUnverified
		return
	case ctx.Err() != nil || !wobs.Usable():
		// Cancellation at the return boundary wins over a simultaneous exit.
		res.Worker = Program{State: StateUnverified}
		res.Outcome = OutcomeWorkerUnverified
		return
	case wobs.ExitCode != 0:
		res.Worker = exited(wobs.ExitCode)
		res.Outcome = OutcomeWorkerFailed
		return
	}
	res.Worker = exited(0)
	res.Outcome = OutcomeVerificationUnverified
	if ctx.Err() != nil {
		return
	}
	pkt, top, err := inspectTop(inspect.Options{Repo: x.top, Limits: inspect.DefaultLimits})
	if err == nil {
		err = at("post-inspection")
	}
	if ctx.Err() != nil {
		return // cancellation wins over the simultaneous inspection result
	}
	if err != nil || top != x.top || pkt.Repository.ObjectFormat != res.Repository.ObjectFormat {
		res.Outcome = OutcomeInspectionFailed
		return
	}
	if fi, err := os.Stat(top); err != nil || !os.SameFile(fi, topInfo) {
		res.Outcome = OutcomeInspectionFailed
		return
	}
	if pkt.Repository.HeadState == "present" && pkt.Repository.Head != nil {
		h := *pkt.Repository.Head
		res.Repository.AfterHead = &h
	}
	if ctx.Err() != nil {
		return
	}
	vobs := x.run(ctx, plan.Verification, verifierExe, "verifier")
	switch {
	case !vobs.Started:
	case ctx.Err() != nil || !vobs.Usable():
		res.Verification = Program{State: StateUnverified}
	case vobs.ExitCode != 0:
		res.Verification = exited(vobs.ExitCode)
		res.Outcome = OutcomeVerificationFailed
	default:
		res.Verification = exited(0)
		res.Outcome = OutcomeVerificationPassed
	}
}

// run starts one program with its own fresh environment. A receipt fault
// after Start keeps Started and makes the observation unusable.
func (x *runner) run(ctx context.Context, c Command, exe, prog string) proc.Observation {
	if ctx.Err() != nil || at(prog+"-start") != nil {
		return proc.Observation{}
	}
	dir := filepath.Join(x.attempt, prog)
	_, obs, _ := runObserved(ctx, proc.Spec{
		Path: exe, Args: c.Arguments, Dir: x.top,
		Env:     []string{"HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + filepath.Join(dir, "tmp"), "LANG=C", "LC_ALL=C"},
		Timeout: time.Duration(c.TimeoutSeconds) * time.Second, StdoutCap: StdoutCap, StderrCap: StderrCap,
	})
	if obs.Started && at(prog+"-receipt") != nil {
		return proc.Observation{Started: true}
	}
	return obs
}

// physical resolves ancestor aliases and keeps the final name.
func physical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

// within reports whether path equals dir or is beneath it by path
// components (never by textual prefix).
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// checkExecutable validates one plan executable and returns its resolved
// path. It is an observation, not a guarantee about the later exec.
func checkExecutable(path string) (string, error) {
	phys, err := physical(path)
	if err != nil {
		return "", fail(CodeExecUnavailable)
	}
	fi, err := os.Lstat(phys)
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return "", fail(CodeExecUnavailable)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || (st.Uid != uint32(os.Getuid()) && st.Uid != 0) ||
		fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 || fi.Mode().Perm()&0o022 != 0 {
		return "", fail(CodeExecUnavailable)
	}
	if os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() {
		return "", fail(CodeExecUnavailable)
	}
	if err := syscall.Access(phys, 1); err != nil {
		return "", fail(CodeExecUnavailable)
	}
	return phys, nil
}

// IsCode reports whether err carries the execution code c.
func IsCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}
