// Package verification implements `baw run verify`: one trusted local
// verifier program run against an explicit clean committed candidate, in an
// exclusive, durably recorded attempt, with the candidate inspected before
// and after. A passed observation is not complete gate evidence, a source
// freeze, approval or merge authority. It holds no credentials, provides no
// native agent adapter and never retries, resumes or recovers an attempt.
package verification

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/execution"
	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// Code is a fixed, safe failure code published as `baw: CODE`.
type Code string

const (
	CodeUsage              Code = "invalid_usage"
	CodePlanUnavailable    Code = "plan_unavailable"
	CodeInvalidPlan        Code = "invalid_verification_plan"
	CodeCheckpointMismatch Code = "verification_checkpoint_mismatch"
	CodeNotClean           Code = "candidate_not_clean"
	CodeUnsafeLayout       Code = "unsafe_execution_layout"
	CodeExecUnavailable    Code = "executable_unavailable"
	CodeExists             Code = "verification_exists"
	CodeStorageUnavailable Code = "verification_storage_unavailable"
	CodeUncertain          Code = "verification_uncertain"
	CodeFailed             Code = "verification_failed"
	CodeCancelled          Code = "verification_cancelled"
	CodeOutputUnavailable  Code = "output_unavailable"
	// Existing state-contract codes reused with their existing meaning.
	CodeUnsafeStatePath  Code = "unsafe_state_path"
	CodeStatePermissions Code = "state_permissions"
	CodeStateChanged     Code = "state_changed"
	CodeStateUnavailable Code = "state_unavailable"
	CodeDurability       Code = "durability_unavailable"
)

// Error carries only a fixed code; no paths, values or OS error text.
type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }

func fail(c Code) error { return &Error{Code: c} }

// IsCode reports whether err carries the verification code c.
func IsCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}

// Fixed verification bounds. Verifier output is discarded.
const (
	Deadline  = 300 * time.Second
	StdoutCap = 65536
	StderrCap = 65536
)

// Request is a syntactically valid verification request.
type Request struct {
	Repo      string
	StateDir  string
	RunID     string
	Candidate string
	Plan      string
	JSON      bool
}

// Internal test seams; production code never sets them. runner, when
// non-nil, replaces proc.RunObserved so tests can supply observation facts
// that a real child cannot produce on demand.
var (
	runner     func(context.Context, proc.Spec) ([]byte, proc.Observation, error)
	inspectTop = inspect.InspectTop
	now        = time.Now
)

// Verify validates, acquires the exclusive attempt, runs the verifier once
// and, only after a usable zero exit, re-inspects the candidate. It
// publishes the result and then delivers its exact output. passed is true
// only for a delivered candidate_verification_passed result; a nil error
// with passed false is a delivered non-passing result. Errors from the state
// and inspect packages are returned unchanged.
func Verify(ctx context.Context, req Request, stdout io.Writer) (passed bool, err error) {
	cancelled := func() bool { return ctx.Err() != nil }
	// 2. State root and existing verification namespace safety, creating nothing.
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
	// 3. Plan.
	planBytes, cmd, err := ReadPlan(req.Plan)
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
	// 4. Run record: references only, not approval.
	rec, err := root.Read(req.RunID)
	if err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 5. Guarded inspection with the record HEAD as checkpoint. A record and
	// repository of different object formats surface as the inspector's
	// unchanged checkpoint-width error.
	pkt, top, err := inspectTop(inspect.Options{Repo: req.Repo, Checkpoint: rec.RepositoryHead,
		Limits: inspect.DefaultLimits})
	if err != nil {
		return false, err
	}
	repo := pkt.Repository
	if !oidFor(repo.ObjectFormat, req.Candidate) {
		return false, fail(CodeUsage)
	}
	if repo.HeadState != "present" || repo.Head == nil || *repo.Head != req.Candidate {
		return false, fail(CodeCheckpointMismatch)
	}
	if !cleanCounts(repo.Counts) {
		return false, fail(CodeNotClean)
	}
	topInfo, err := os.Stat(top)
	if err != nil {
		return false, fail(CodeUnsafeLayout)
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 6. Layout: the repository and state root are disjoint and the plan is
	// outside the repository. This is a layout constraint, not containment.
	if within(top, rootP) || within(rootP, top) || within(top, planPath) {
		return false, fail(CodeUnsafeLayout)
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 7. Verifier executable.
	exe, err := execution.CheckExecutable(cmd.Executable)
	if err != nil {
		return false, fail(CodeExecUnavailable)
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 8. Existing verification attempt, last.
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
	planSum := sha256.Sum256(planBytes)
	createdAt := now().UTC().Format(state.TimeLayout)
	intent := Intent{1, req.RunID, recordStateIntent, rec.TicketURL, rec.ScopeSHA256, rec.PolicyCommit,
		rec.RepositoryObjectFormat, rec.RepositoryHead, req.Candidate, hex.EncodeToString(planSum[:]),
		createdAt, notEvaluated, notEvaluated}
	data, err := marshal(intent)
	if err != nil || !ValidIntent(data, req.RunID) || cancelled() {
		return false, fail(CodeUncertain)
	}
	if err := publish(attempt, "intent", data, cancelled); err != nil {
		return false, fail(CodeUncertain)
	}

	// Durable intent: the deadline starts now. The outcome is fixed once
	// classified; later cancellation is ignored for the outcome.
	dctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	res := Result{SchemaVersion: 1, RunID: req.RunID, Operation: operationVerify, Authority: notEvaluated,
		Readiness: notEvaluated, Outcome: OutcomeUnverified, Verification: Verification{State: StateNotStarted},
		Repository:   Repository{ObjectFormat: repo.ObjectFormat, BeforeHead: req.Candidate},
		ReceiptState: receiptStateRecorded, CreatedAt: createdAt}
	x := &attemptRun{top: top, topInfo: topInfo, attempt: attempt, candidate: req.Candidate}
	x.classify(dctx, cmd, exe, &res)
	res.CompletedAt = now().UTC().Format(state.TimeLayout)

	// Serialize once, self-validate, render, cap, publish, deliver.
	data, err = marshal(res)
	if err != nil || !ValidResult(data, req.RunID) {
		return false, fail(CodeUncertain)
	}
	out := data
	if !req.JSON {
		out = RenderText(res)
	}
	if !outputFits(data, out) || at("output-limit") != nil {
		return false, fail(CodeUncertain)
	}
	// The outcome is classified: late cancellation does not fail publication.
	if err := publish(attempt, "result", data, nil); err != nil {
		return false, fail(CodeUncertain)
	}
	if err := at("deliver"); err != nil {
		return false, fail(CodeUncertain)
	}
	if n, err := stdout.Write(out); err != nil || n != len(out) {
		return false, fail(CodeUncertain)
	}
	return res.Outcome == OutcomePassed, nil
}

// outputFits reports whether both the saved result and the delivered output
// are within MaxOutput bytes.
func outputFits(data, out []byte) bool {
	return len(data) <= MaxOutput && len(out) <= MaxOutput
}

func cleanCounts(c inspect.Counts) bool {
	return c.Staged == 0 && c.Unstaged == 0 && c.Untracked == 0 && c.Conflicted == 0
}

type attemptRun struct {
	top       string
	topInfo   os.FileInfo
	attempt   string
	candidate string
}

// classify applies the outcome rows to res. Cancellation or deadline before
// classification gives verification_unverified.
func (x *attemptRun) classify(ctx context.Context, cmd Command, exe string, res *Result) {
	unverified := func(started bool) {
		res.Outcome, res.Repository.AfterHead = OutcomeUnverified, nil
		res.Verification = Verification{State: StateNotStarted}
		if started {
			res.Verification = Verification{State: StateUnverified}
		}
	}
	if ctx.Err() != nil || at("verifier-start") != nil {
		unverified(false)
		return
	}
	dir := filepath.Join(x.attempt, "verifier")
	spec := proc.Spec{
		Path: exe, Args: cmd.Arguments, Dir: x.top,
		Env:     []string{"HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + filepath.Join(dir, "tmp"), "LANG=C", "LC_ALL=C"},
		Timeout: time.Duration(cmd.TimeoutSeconds) * time.Second, StdoutCap: StdoutCap, StderrCap: StderrCap,
	}
	run := proc.RunObserved
	if runner != nil {
		run = runner
	}
	_, obs, _ := run(ctx, spec) // output is discarded, never parsed or saved
	if obs.Started && at("verifier-receipt") != nil {
		obs = proc.Observation{Started: true}
	}
	switch {
	case !obs.Started:
		unverified(false)
		return
	case ctx.Err() != nil || !obs.Usable():
		unverified(true)
		return
	case obs.ExitCode != 0:
		code := obs.ExitCode
		res.Verification = Verification{State: StateExited, ExitCode: &code}
		res.Outcome = OutcomeFailed
		return
	}
	if ctx.Err() != nil {
		unverified(true)
		return
	}
	pkt, top, err := inspectTop(inspect.Options{Repo: x.top, Checkpoint: x.candidate, Limits: inspect.DefaultLimits})
	if err == nil {
		err = at("post-inspection")
	}
	// Immediately before classification; cancellation wins over the
	// simultaneous inspection result.
	if at("classification") != nil || ctx.Err() != nil {
		unverified(true)
		return
	}
	zero := 0
	res.Verification = Verification{State: StateExited, ExitCode: &zero}
	res.Outcome = OutcomeChanged
	if err != nil || top != x.top || pkt.Repository.ObjectFormat != res.Repository.ObjectFormat ||
		pkt.Repository.HeadState != "present" || pkt.Repository.Head == nil || *pkt.Repository.Head != x.candidate ||
		!cleanCounts(pkt.Repository.Counts) {
		return
	}
	if fi, err := os.Stat(top); err != nil || !os.SameFile(fi, x.topInfo) {
		return
	}
	after := x.candidate
	res.Repository.AfterHead = &after
	res.Outcome = OutcomePassed
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
