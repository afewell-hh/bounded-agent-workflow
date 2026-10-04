// Package review implements `baw run review`: one trusted reviewer program
// run against an explicit clean committed candidate after a saved successful
// worker/verifier attempt, in an exclusive, durably recorded attempt. A
// program verdict is an observation only. It evaluates no approval, holds no
// credentials, provides no native agent adapter and never retries, resumes
// or recovers an attempt.
package review

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
	CodeInvalidPlan        Code = "invalid_review_plan"
	CodeReceiptUnavailable Code = "execution_receipt_unavailable"
	CodeInvalidReceipt     Code = "invalid_execution_receipt"
	CodeReceiptMismatch    Code = "execution_receipt_mismatch"
	CodeNotEligible        Code = "review_not_eligible"
	CodeCheckpointMismatch Code = "review_checkpoint_mismatch"
	CodeNotClean           Code = "candidate_not_clean"
	CodeUnsafeLayout       Code = "unsafe_execution_layout"
	CodeExecUnavailable    Code = "executable_unavailable"
	CodeExists             Code = "review_exists"
	CodeStorageUnavailable Code = "review_storage_unavailable"
	CodeUncertain          Code = "review_uncertain"
	CodeFailed             Code = "review_failed"
	CodeCancelled          Code = "review_cancelled"
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

// IsCode reports whether err carries the review code c.
func IsCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}

// Fixed review bounds.
const (
	Deadline  = 300 * time.Second
	StdoutCap = 2048
	StderrCap = 65536
)

// Request is a syntactically valid review request.
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

// prereq holds the validated prerequisite inputs.
type prereq struct {
	rec                  state.Record
	intent               execIntent
	result               execResult
	intentSum, resultSum string
}

// Review validates, acquires the exclusive attempt, runs the reviewer once
// and, only after a usable zero exit with a valid report, re-inspects the
// candidate. It publishes the result and then delivers its exact output.
// passed is true only for a delivered review_passed result; a nil error with
// passed false is a delivered non-passing result. Errors from the state and
// inspect packages are returned unchanged.
func Review(ctx context.Context, req Request, stdout io.Writer) (passed bool, err error) {
	cancelled := func() bool { return ctx.Err() != nil }
	// 2. State root and existing review namespace safety, creating nothing.
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
	// 4. Run record.
	rec, err := root.Read(req.RunID)
	if err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 5. Execution receipts.
	p, err := readPrereq(rootP, req.RunID, rec)
	if err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 6. Consistency and eligibility.
	if err := p.check(req.RunID); err != nil {
		return false, err
	}
	if cancelled() {
		return false, fail(CodeCancelled)
	}
	// 7. Guarded inspection with the saved after_head as checkpoint.
	pkt, top, err := inspectTop(inspect.Options{Repo: req.Repo, Checkpoint: *p.result.Repository.AfterHead,
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
	// 8. Layout and reviewer executable.
	if within(top, rootP) || within(top, planPath) || within(top, ns) || within(ns, top) {
		return false, fail(CodeUnsafeLayout)
	}
	exe, err := execution.CheckExecutable(cmd.Executable)
	if err != nil {
		return false, fail(CodeExecUnavailable)
	}
	// 9. Existing review attempt.
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
		p.intentSum, p.resultSum, createdAt, notEvaluated}
	data, err := marshal(intent)
	if err != nil || !validIntent(data, req.RunID) || cancelled() {
		return false, fail(CodeUncertain)
	}
	if err := publish(attempt, "intent", data); err != nil {
		return false, fail(CodeUncertain)
	}

	// Durable intent: the deadline starts now. The outcome is fixed once
	// classified; later cancellation is ignored for the outcome.
	dctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	res := Result{SchemaVersion: 1, RunID: req.RunID, Operation: operationReview, Authority: notEvaluated,
		Readiness: notEvaluated, Outcome: OutcomeUnverified, Reviewer: Reviewer{State: StateNotStarted},
		Repository:   Repository{ObjectFormat: repo.ObjectFormat, BeforeHead: req.Candidate},
		ReceiptState: receiptStateRecorded, CreatedAt: createdAt}
	x := &attemptRun{top: top, topInfo: topInfo, attempt: attempt, candidate: req.Candidate}
	x.classify(dctx, cmd, exe, &res)
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
	if len(data) > MaxOutput || len(out) > MaxOutput || at("output-limit") != nil {
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
	return res.Outcome == OutcomePassed, nil
}

func cleanCounts(c inspect.Counts) bool {
	return c.Staged == 0 && c.Unstaged == 0 && c.Untracked == 0 && c.Conflicted == 0
}

// readPrereq validates the execute-v1 namespace and ID directories, then
// reads intent.json and result.json independently, in that order.
func readPrereq(rootP, id string, rec state.Record) (prereq, error) {
	var p prereq
	p.rec = rec
	ens := filepath.Join(rootP, ExecuteNamespace)
	for _, dir := range []string{ens, filepath.Join(ens, id)} {
		if _, err := checkPrivateDir(dir); err != nil {
			if err == fs.ErrNotExist {
				return p, fail(CodeReceiptUnavailable)
			}
			return p, err
		}
	}
	dir := filepath.Join(ens, id)
	data, err := readReceipt(filepath.Join(dir, "intent.json"), "intent", func(b []byte) error {
		var err error
		p.intent, err = parseExecIntent(b)
		return err
	})
	if err != nil {
		return p, err
	}
	p.intentSum = sum(data)
	data, err = readReceipt(filepath.Join(dir, "result.json"), "result", func(b []byte) error {
		var err error
		p.result, err = parseExecResult(b)
		return err
	})
	if err != nil {
		return p, err
	}
	p.resultSum = sum(data)
	return p, nil
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// check applies the prerequisite consistency and eligibility rules.
func (p *prereq) check(id string) error {
	in, r, rec := p.intent, p.result, p.rec
	if rec.RunID != id || in.RunID != id || r.RunID != id ||
		rec.TicketURL != in.TicketURL || rec.ScopeSHA256 != in.ScopeSHA256 || rec.PolicyCommit != in.PolicyCommit ||
		rec.RepositoryObjectFormat != in.RepositoryObjectFormat || rec.RepositoryHead != in.RepositoryHead ||
		r.Repository.ObjectFormat != in.RepositoryObjectFormat || r.Repository.BeforeHead != in.RepositoryHead ||
		r.CreatedAt != in.CreatedAt {
		return fail(CodeReceiptMismatch)
	}
	exited0 := func(pr execProgram) bool { return pr.State == StateExited && pr.ExitCode != nil && *pr.ExitCode == 0 }
	if r.Outcome != execution.OutcomeVerificationPassed || !exited0(r.Worker) || !exited0(r.Verification) ||
		r.Repository.AfterHead == nil {
		return fail(CodeNotEligible)
	}
	return nil
}

type attemptRun struct {
	top       string
	topInfo   os.FileInfo
	attempt   string
	candidate string
}

// classify applies the §4 rows to res. Cancellation or deadline before
// classification gives reviewer_unverified with no verdict.
func (x *attemptRun) classify(ctx context.Context, cmd Command, exe string, res *Result) {
	unverified := func(started bool) {
		res.Outcome, res.Verdict, res.Repository.AfterHead = OutcomeUnverified, nil, nil
		res.Reviewer = Reviewer{State: StateNotStarted}
		if started {
			res.Reviewer = Reviewer{State: StateUnverified}
		}
	}
	if ctx.Err() != nil || at("reviewer-start") != nil {
		unverified(false)
		return
	}
	dir := filepath.Join(x.attempt, "reviewer")
	spec := proc.Spec{
		Path: exe, Args: cmd.Arguments, Dir: x.top,
		Env:     []string{"HOME=" + filepath.Join(dir, "home"), "TMPDIR=" + filepath.Join(dir, "tmp"), "LANG=C", "LC_ALL=C"},
		Timeout: time.Duration(cmd.TimeoutSeconds) * time.Second, StdoutCap: StdoutCap, StderrCap: StderrCap,
	}
	run := proc.RunObserved
	if runner != nil {
		run = runner
	}
	out, obs, _ := run(ctx, spec)
	if obs.Started && at("reviewer-receipt") != nil {
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
		res.Reviewer = Reviewer{State: StateExited, ExitCode: &code}
		res.Outcome = OutcomeFailed
		return
	}
	zero := 0
	res.Reviewer = Reviewer{State: StateExited, ExitCode: &zero}
	verdict, ok := ParseReport(out)
	if !ok {
		res.Outcome = OutcomeUnverified
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
	if ctx.Err() != nil {
		unverified(true) // cancellation wins over the simultaneous inspection result
		return
	}
	res.Verdict = &verdict
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
	res.Outcome = OutcomeRequiredFixes
	if verdict == VerdictPass {
		res.Outcome = OutcomePassed
	}
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
