// Package worker implements `baw run worker`: one native Claude Code worker
// attempt for a frozen task, in an exclusive, durably recorded attempt. It
// supplies the full ordered project instructions on stdin, invokes the single
// approved profile, validates the native stream and final report, and records
// the bounded worktree change. A completed observation is not tests, review,
// acceptance or authority. It never retries, resumes, commits or recovers.
package worker

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// Code is a fixed, safe failure code published as `baw: CODE`.
type Code string

const (
	CodeUsage                   Code = "invalid_usage"
	CodePlanUnavailable         Code = "plan_unavailable"
	CodeInvalidPlan             Code = "invalid_worker_plan"
	CodeCheckpointMismatch      Code = "worker_checkpoint_mismatch"
	CodeNotClean                Code = "worker_not_clean"
	CodeUnsafeLayout            Code = "unsafe_execution_layout"
	CodeInstructionsUnavailable Code = "instructions_unavailable"
	CodePacketUnavailable       Code = "packet_unavailable"
	CodeProfileUnavailable      Code = "profile_unavailable"
	CodeSourceUnavailable       Code = "source_profile_unavailable"
	CodeExists                  Code = "worker_exists"
	CodeStorageUnavailable      Code = "worker_storage_unavailable"
	CodeUncertain               Code = "worker_uncertain"
	CodeInputChanged            Code = "input_changed"
	CodeFailed                  Code = "worker_failed"
	CodeCancelled               Code = "worker_cancelled"
	CodeOutputUnavailable       Code = "output_unavailable"

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

// IsCode reports whether err carries the worker code c.
func IsCode(err error, c Code) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == c
}

var errGit = errors.New("git observation failed")

func asExit(err error, ee **proc.ExitError) bool { return errors.As(err, ee) }

// Fixed native bounds.
const (
	Deadline        = 300 * time.Second
	StdoutCap       = 8 << 20
	StderrCap       = 1 << 20
	MaxChangeFiles  = 4096
	MaxChangeFile   = 1 << 20
	MaxChangeTotal  = 16 << 20
	maxExecutable   = 1 << 30
	changeStateSame = "unchanged"
)

// Request is a syntactically valid worker request.
type Request struct {
	Repo     string
	StateDir string
	RunID    string
	Plan     string
	JSON     bool
}

// Deps holds optional per-call seams. The zero value is production: the
// darwin/arm64 platform check, proc.RunObservedInput, time.Now, no faults
// and os.Lstat for source metadata.
type Deps struct {
	Platform    func() (goos, goarch string)
	Run         func(context.Context, proc.Spec, *os.File, func([]byte) error) ([]byte, proc.Observation, error)
	Now         func() time.Time
	Fault       func(stage string) error
	SourceLstat claude.Lstat
}

func (d *Deps) fill() {
	if d.Platform == nil {
		d.Platform = func() (string, string) { return runtime.GOOS, runtime.GOARCH }
	}
	if d.Run == nil {
		d.Run = proc.RunObservedInput
	}
	if d.Now == nil {
		d.Now = time.Now
	}
}

func (d Deps) at(stage string) error {
	if d.Fault == nil {
		return nil
	}
	return d.Fault(stage)
}

// Run is RunWith with production dependencies.
func Run(ctx context.Context, req Request, stdout io.Writer) (completed bool, err error) {
	return RunWith(ctx, req, stdout, Deps{})
}

// inputs is everything validated before acquisition, plus the private state
// identities retained after acquisition.
type inputs struct {
	rootP          string
	ns             string
	attempt        string
	rootInfo       os.FileInfo
	nsInfo         os.FileInfo
	attemptInfo    os.FileInfo
	workerInfo     os.FileInfo
	tmpInfo        os.FileInfo
	recordsInfo    os.FileInfo
	recordPath     string
	recordInfo     os.FileInfo
	recordSHA      string
	top            string
	topInfo        os.FileInfo
	rec            state.Record
	planPath       string
	taskPath       string
	profilePath    string
	planInfo       os.FileInfo
	taskInfo       os.FileInfo
	profileInfo    os.FileInfo
	planSHA        string
	taskSHA        string
	profileFileSHA string
	profile        claude.Profile
	task           Task
	tree           map[string]treeEntry
	snap           snapshot
	width          int
	bodies         [][]byte
	nested         []string
	packet         []byte
	manifestSHA    string
	exeSHA         string
	sources        []claude.SourcePath
	observed       []claude.SourceEntry
	bases          []string
	baseObs        []claude.SourceEntry
	control        []byte
	g              *git
}

// RunWith validates, acquires the exclusive attempt, publishes the supplied
// inputs, packet, source manifest and intent, runs the native worker once
// and classifies it. It publishes the result and then delivers its exact
// output. completed is true only for a delivered worker_completed_unverified
// result; a nil error with completed false is a delivered other outcome.
// A pre-start input change after intent publishes and delivers a not_started
// result and then returns input_changed. If the state root, namespace or
// attempt directory is no longer the retained private directory before start
// or before publication, nothing more is published and worker_uncertain is
// returned. Errors from the state and inspect packages are returned unchanged.
func RunWith(ctx context.Context, req Request, stdout io.Writer, d Deps) (completed bool, err error) {
	d.fill()
	in, err := validate(ctx, req, d)
	if err != nil {
		return false, err
	}
	st := store{fault: d.Fault}
	cancelled := func() bool { return ctx.Err() != nil }
	if err := st.acquire(in.rootP, in.ns, in.attempt, cancelled); err != nil {
		return false, err
	}
	// Retain the namespace and attempt identities; the root's was retained
	// during validation. From here on every failure is uncertain.
	if in.nsInfo, err = checkPrivateDir(in.ns); err != nil {
		return false, fail(CodeUncertain)
	}
	if in.attemptInfo, err = checkPrivateDir(in.attempt); err != nil {
		return false, fail(CodeUncertain)
	}
	if in.workerInfo, err = checkPrivateDir(filepath.Join(in.attempt, "worker")); err != nil {
		return false, fail(CodeUncertain)
	}
	if in.tmpInfo, err = checkPrivateDir(filepath.Join(in.attempt, "worker", "tmp")); err != nil || !in.stateSafe() {
		return false, fail(CodeUncertain)
	}
	createdAt := d.Now().UTC().Format(state.TimeLayout)
	sup, err := marshal(suppliedInputs(req.RunID, in))
	if err != nil {
		return false, fail(CodeUncertain)
	}
	srcManifest, err := marshal(sourceManifest{1, claude.SourceMatrixID, createdAt, in.observed, in.baseObs})
	if err != nil {
		return false, fail(CodeUncertain)
	}
	var wst syscall.Stat_t
	if s, ok := in.topInfo.Sys().(*syscall.Stat_t); ok {
		wst = *s
	}
	intent := Intent{1, req.RunID, recordStateIntent, in.rec.TicketURL, in.rec.ScopeSHA256, in.rec.PolicyCommit,
		in.rec.RepositoryObjectFormat, in.rec.RepositoryHead, strconv.FormatUint(uint64(wst.Dev), 10),
		strconv.FormatUint(wst.Ino, 10), in.planSHA, in.taskSHA, in.manifestSHA, sum(in.packet), sum(sup),
		in.profile.ApprovedSHA256, in.exeSHA, claude.Version, sum(srcManifest), claude.InjectionDisposition,
		notEvaluated, notEvaluated, createdAt}
	idata, err := marshal(intent)
	if err != nil || !ValidIntent(idata, req.RunID) || cancelled() {
		return false, fail(CodeUncertain)
	}
	for _, f := range []struct {
		name string
		data []byte
	}{{"supplied-inputs.json", sup}, {"packet.txt", in.packet}, {"source-manifest.json", srcManifest}, {"intent.json", idata}} {
		if err := st.publish(in.attempt, f.name, f.data); err != nil {
			return false, fail(CodeUncertain)
		}
	}

	res := Result{SchemaVersion: 1, RunID: req.RunID, Operation: operation, Authority: notEvaluated, Readiness: notEvaluated}
	res.Outcome, res.Worker = OutcomeUnusable, Worker{State: StateNotStarted}
	res.InputBinding = InputBinding{in.taskSHA, in.manifestSHA, sum(in.packet), in.profile.ApprovedSHA256, in.exeSHA}
	res.Repository = Repository{ObjectFormat: in.rec.RepositoryObjectFormat, BeforeHead: in.rec.RepositoryHead}
	res.InstructionCount, res.InstructionDelivery = len(in.task.Instructions), instructionDelivery
	res.SourceObservation = SourceObservation{claude.SourceMatrixID, sum(srcManifest), coverage, suppressionBasis}
	res.StreamSummary = StreamSummary{MemoryObservation: notEvaluated, InformationCounts: map[string]int{}}
	res.InjectionDisposition, res.ReceiptState, res.CreatedAt = claude.InjectionDisposition, receiptRecorded, createdAt

	var changes []byte
	prestart := in.stateSafe() && in.recheck(d) == nil && d.at("prestart") == nil
	if prestart {
		changes = in.launch(ctx, d, &res)
	}
	res.CompletedAt = d.Now().UTC().Format(state.TimeLayout)

	data, err := marshal(res)
	if err != nil || !ValidResult(data, req.RunID) {
		return false, fail(CodeUncertain)
	}
	out := data
	if !req.JSON {
		out = RenderText(res)
	}
	if len(data) > MaxOutput || len(out) > MaxOutput || d.at("output-limit") != nil {
		return false, fail(CodeUncertain)
	}
	// A state root, namespace or attempt that is no longer the retained safe
	// directory makes publication unsafe: nothing more is published, the
	// attempt stays blocked and every worktree edit is preserved.
	if !in.stateSafe() {
		return false, fail(CodeUncertain)
	}
	if changes != nil {
		if err := st.publish(in.attempt, "changes.json", changes); err != nil {
			return false, fail(CodeUncertain)
		}
	}
	if err := st.publish(in.attempt, "result.json", data); err != nil {
		return false, fail(CodeUncertain)
	}
	if err := d.at("deliver"); err != nil {
		return false, fail(CodeUncertain)
	}
	if n, err := stdout.Write(out); err != nil || n != len(out) {
		return false, fail(CodeUncertain)
	}
	if !prestart {
		return false, fail(CodeInputChanged)
	}
	return res.Outcome == OutcomeCompleted, nil
}

// validate performs every check before acquisition in the contract order;
// it changes no state.
func validate(ctx context.Context, req Request, d Deps) (*inputs, error) {
	cancelled := func() bool { return ctx.Err() != nil }
	in := &inputs{}
	if goos, arch := d.Platform(); goos != "darwin" || arch != "arm64" {
		return nil, fail(CodeProfileUnavailable)
	}
	root, err := state.OpenRoot(req.StateDir)
	if err != nil {
		return nil, err
	}
	if in.rootP, err = physical(req.StateDir); err != nil {
		return nil, fail(CodeStateUnavailable)
	}
	if in.rootInfo, err = checkPrivateDir(in.rootP); err != nil {
		if err == fs.ErrNotExist {
			return nil, fail(CodeStateUnavailable)
		}
		return nil, err
	}
	in.ns = filepath.Join(in.rootP, Namespace)
	if err := checkNamespace(in.ns); err != nil {
		return nil, err
	}
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	planBytes, planPath, planInfo, err := readPrivateInfo(req.Plan, MaxPlanBytes, CodePlanUnavailable, CodeInvalidPlan)
	if err != nil {
		return nil, err
	}
	plan, err := ParsePlan(planBytes)
	if err != nil {
		return nil, err
	}
	taskBytes, taskPath, taskInfo, err := readPrivateInfo(plan.TaskFile, MaxTaskBytes, CodePlanUnavailable, CodeInvalidPlan)
	if err != nil {
		return nil, err
	}
	profBytes, profPath, profInfo, err := readPrivateInfo(plan.ProfileFile, claude.MaxProfile, CodeProfileUnavailable, CodeProfileUnavailable)
	if err != nil {
		return nil, err
	}
	in.planInfo, in.taskInfo, in.profileInfo = planInfo, taskInfo, profInfo
	if in.profile, err = claude.ParseProfile(profBytes); err != nil {
		return nil, fail(CodeProfileUnavailable)
	}
	in.planPath, in.taskPath, in.profilePath = planPath, taskPath, profPath
	in.planSHA, in.taskSHA, in.profileFileSHA = sum(planBytes), sum(taskBytes), sum(profBytes)
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	if in.rec, err = root.Read(req.RunID); err != nil {
		return nil, err
	}
	if in.recordsInfo, err = checkPrivateDir(filepath.Join(in.rootP, state.Namespace)); err != nil {
		return nil, fail(CodeStateChanged)
	}
	recordBytes, recordPath, recordInfo, err := readPrivateInfo(filepath.Join(in.rootP, state.Namespace, req.RunID+".json"), state.MaxRecordBytes, CodeStateChanged, CodeStateChanged)
	if err != nil {
		return nil, err
	}
	boundRecord, err := state.Parse(recordBytes, req.RunID)
	if err != nil || boundRecord != in.rec {
		return nil, fail(CodeStateChanged)
	}
	in.recordPath, in.recordInfo, in.recordSHA = recordPath, recordInfo, sum(recordBytes)
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	pkt, top, err := inspect.InspectTop(inspect.Options{Repo: req.Repo, Checkpoint: in.rec.RepositoryHead, Limits: inspect.DefaultLimits})
	if err != nil {
		return nil, err
	}
	repo := pkt.Repository
	if repo.HeadState != "present" || repo.Head == nil || *repo.Head != in.rec.RepositoryHead ||
		repo.ObjectFormat != in.rec.RepositoryObjectFormat {
		return nil, fail(CodeCheckpointMismatch)
	}
	c := repo.Counts
	if c.Staged != 0 || c.Unstaged != 0 || c.Untracked != 0 || c.Conflicted != 0 {
		return nil, fail(CodeNotClean)
	}
	in.top = top
	if !ruleWorkspace(top) {
		return nil, fail(CodeUnsafeLayout)
	}
	if in.topInfo, err = os.Stat(top); err != nil {
		return nil, fail(CodeUnsafeLayout)
	}
	in.width = 40
	if repo.ObjectFormat == "sha256" {
		in.width = 64
	}
	if in.task, err = ParseTask(taskBytes, req.RunID, in.rec.ScopeSHA256, in.rec.RepositoryHead, repo.ObjectFormat); err != nil {
		return nil, err
	}
	if within(top, in.rootP) || within(in.rootP, top) || within(top, planPath) || within(top, taskPath) ||
		within(top, profPath) || within(top, in.profile.UserHome) {
		return nil, fail(CodeUnsafeLayout)
	}
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fail(CodeNotClean)
	}
	in.g = &git{path: gitPath, dir: top, ctx: ctx}
	if es, err := in.g.status(); err != nil || len(es) != 0 {
		return nil, fail(CodeNotClean)
	}
	if clean, err := in.g.flagsClean(); err != nil || !clean {
		return nil, fail(CodeNotClean)
	}
	if in.control, err = in.g.control(); err != nil {
		return nil, fail(CodeNotClean)
	}
	if in.tree, err = in.g.tree(in.rec.RepositoryHead, in.width); err != nil {
		return nil, fail(CodeInstructionsUnavailable)
	}
	if in.snap, err = takeSnapshot(top, repo.ObjectFormat); err != nil {
		return nil, fail(CodeUnsafeLayout)
	}
	if err := matchesTree(in.snap, in.tree); err != nil {
		return nil, err
	}
	if in.bodies, err = verifyInstructions(top, in.task.Instructions, in.tree, repo.ObjectFormat); err != nil {
		return nil, err
	}
	if err := checkAllowed(top, in.task.AllowedChanges, in.tree); err != nil {
		return nil, err
	}
	in.manifestSHA = InstructionManifestSHA256(in.task.Instructions)
	pi := PacketInput{RunID: req.RunID, TaskInputSHA256: in.taskSHA, BaseCommit: in.rec.RepositoryHead}
	pi.ScopeSHA256, pi.ProfileSHA256, pi.InstructionsSHA256 = in.rec.ScopeSHA256, in.profile.ApprovedSHA256, in.manifestSHA
	pi.Instructions, pi.Bodies = in.task.Instructions, in.bodies
	pi.Task, pi.AllowedChanges = in.task.Text, in.task.AllowedChanges
	if in.packet, err = AssemblePacket(pi); err != nil {
		return nil, err
	}
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	if in.exeSHA, err = checkPin(in.profile); err != nil {
		return nil, err
	}
	if err := checkHome(in.profile.UserHome); err != nil {
		return nil, err
	}
	in.nested = nestedDirs(in.tree, in.task.Instructions)
	if in.sources, err = claude.Matrix(in.profile.UserHome, top, in.nested); err != nil {
		return nil, fail(CodeSourceUnavailable)
	}
	if in.bases, err = claude.Bases(in.profile.UserHome, top, in.nested); err != nil {
		return nil, fail(CodeSourceUnavailable)
	}
	if in.observed, in.baseObs, err = in.observeSources(d); err != nil {
		return nil, err
	}
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	in.attempt = filepath.Join(in.ns, req.RunID)
	if err := checkAttempt(in.attempt); err != nil {
		return nil, err
	}
	if cancelled() {
		return nil, fail(CodeCancelled)
	}
	return in, nil
}

// observeSources observes every matrix row and every base directory the rows
// hang from (claude.Bases).
func (in *inputs) observeSources(d Deps) ([]claude.SourceEntry, []claude.SourceEntry, error) {
	uid := uint32(os.Getuid())
	es, err := claude.Observe(in.sources, uid, d.SourceLstat)
	if err == nil {
		err = claude.CheckOwners(es, uid)
	}
	var bs []claude.SourceEntry
	if err == nil {
		bs, err = claude.ObserveBases(in.bases, uid, d.SourceLstat)
	}
	if err != nil {
		return nil, nil, fail(CodeSourceUnavailable)
	}
	return es, bs, nil
}

// stateSafe reports whether the state root, both namespaces, attempt and scratch are each
// still a private 0700 directory owned by the operator and the same
// directory (device and inode) retained at validation or acquisition.
func (in *inputs) stateSafe() bool {
	for _, c := range []struct {
		path string
		want os.FileInfo
	}{{in.rootP, in.rootInfo}, {filepath.Join(in.rootP, state.Namespace), in.recordsInfo}, {in.ns, in.nsInfo}, {in.attempt, in.attemptInfo},
		{filepath.Join(in.attempt, "worker"), in.workerInfo}, {filepath.Join(in.attempt, "worker", "tmp"), in.tmpInfo}} {
		fi, err := checkPrivateDir(c.path)
		if err != nil || c.want == nil || !os.SameFile(fi, c.want) {
			return false
		}
	}
	return true
}

// inputsSame reports whether the plan, task, profile and saved record are still safe,
// the same files (device and inode) with the same mode, and the same bytes.
// A byte-identical substitute is a change.
func (in *inputs) inputsSame() bool {
	for _, f := range []struct {
		path, want string
		info       os.FileInfo
	}{{in.planPath, in.planSHA, in.planInfo}, {in.taskPath, in.taskSHA, in.taskInfo}, {in.profilePath, in.profileFileSHA, in.profileInfo}, {in.recordPath, in.recordSHA, in.recordInfo}} {
		b, _, fi, err := readPrivateInfo(f.path, MaxTaskBytes, CodeInputChanged, CodeInputChanged)
		if err != nil || sum(b) != f.want || !os.SameFile(fi, f.info) || fi.Mode() != f.info.Mode() {
			return false
		}
	}
	return true
}

// recheck repeats the input, instruction, pin, source, repository, control
// and full worktree observations immediately before native start.
func (in *inputs) recheck(d Deps) error {
	changed := fail(CodeInputChanged)
	if !in.inputsSame() || !in.unchanged(d) {
		return changed
	}
	es, err := in.g.status()
	if err != nil || len(es) != 0 {
		return changed
	}
	snap, err := takeSnapshot(in.top, in.rec.RepositoryObjectFormat)
	if err != nil || !unchangedExcept(in.snap, snap, nil) {
		return changed
	}
	return nil
}

// unchanged reports whether the pin, instructions, worktree identity, HEAD,
// control identity, source matrix and base directories still match the
// validated observations.
func (in *inputs) unchanged(d Deps) bool {
	if sha, err := checkPin(in.profile); err != nil || sha != in.exeSHA {
		return false
	}
	if _, err := verifyInstructions(in.top, in.task.Instructions, in.tree, in.rec.RepositoryObjectFormat); err != nil {
		return false
	}
	if fi, err := os.Stat(in.top); err != nil || !os.SameFile(fi, in.topInfo) {
		return false
	}
	if ctl, err := in.g.control(); err != nil || string(ctl) != string(in.control) {
		return false
	}
	after, bases, err := in.observeSources(d)
	return err == nil && !claude.SourcesChanged(in.observed, after) && !claude.BasesChanged(in.baseObs, bases)
}

// launch opens the published packet, runs the native worker once and
// classifies the attempt into res. It returns the change manifest bytes when
// a bounded capture was completed.
func (in *inputs) launch(ctx context.Context, d Deps, res *Result) []byte {
	pkt, err := openPacket(filepath.Join(in.attempt, "packet.txt"), sum(in.packet))
	if err != nil {
		return nil
	}
	defer pkt.Close()
	if ctx.Err() != nil || d.at("worker-start") != nil {
		return nil
	}
	exp := claude.Expect{RunID: res.RunID, TaskInputSHA256: in.taskSHA, Worktree: in.top, Plugins: in.profile.Plugins}
	parser := claude.NewParser(exp)
	spec := proc.Spec{Path: in.profile.Executable, Args: claude.Args(in.top), Dir: in.top,
		Env:     claude.Env(in.profile, filepath.Join(in.attempt, "worker", "tmp")),
		Timeout: Deadline, StdoutCap: StdoutCap, StderrCap: StderrCap}
	_, obs, _ := d.Run(ctx, spec, pkt, parser.Feed)
	if obs.Started && d.at("worker-receipt") != nil {
		obs = proc.Observation{Started: true}
	}
	if !obs.Started {
		return nil
	}
	res.Worker = Worker{State: StateUnverified}
	usable := obs.Usable() && ctx.Err() == nil
	if usable {
		code := obs.ExitCode
		res.Worker = Worker{State: StateExited, ExitCode: &code}
	}
	summary, perr := parser.Finish()
	if summary.InitVerified {
		pc := summary.PluginCount
		ss := &res.StreamSummary
		ss.InitVerified, ss.MemoryObservation, ss.PluginCount = true, summary.MemoryObservation, &pc
		ss.InformationCounts = summary.InformationCounts
		ss.TerminalVerified = perr == nil && summary.TerminalVerified
	}
	if !usable || obs.ExitCode != 0 || perr != nil || !res.StreamSummary.TerminalVerified {
		return nil
	}
	// Post-return observations; any drift invalidates the attempt. The state
	// directories are rechecked by the caller before publication.
	if d.at("post-observation") != nil || !in.inputsSame() || !in.unchanged(d) {
		return nil
	}
	if h, err := in.g.head(); err != nil || h != in.rec.RepositoryHead {
		return nil
	}
	post, err := takeSnapshot(in.top, in.rec.RepositoryObjectFormat)
	if err != nil {
		return nil
	}
	manifest, ok := captureChanges(in.g, res.RunID, in.task.AllowedChanges, in.tree, in.snap, post)
	if !ok {
		return nil
	}
	after, change := in.rec.RepositoryHead, sum(manifest)
	res.Repository.AfterHead, res.Repository.ChangeManifestSHA256 = &after, &change
	res.Outcome = OutcomeNotCompleted
	if summary.Status == claude.StatusCompleted {
		res.Outcome = OutcomeCompleted
	}
	return manifest
}

// openPacket opens the published packet without following links, checks its
// bytes against the assembled hash and rewinds it for use as stdin.
func openPacket(path, want string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err == nil && (!fi.Mode().IsRegular() || !private(fi, 0o600)) {
		err = errUnsafe
	}
	var b []byte
	if err == nil {
		b, err = io.ReadAll(io.LimitReader(f, MaxPacketBytes+1))
	}
	if err == nil && sum(b) != want {
		err = errUnsafe
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// within reports whether path equals dir or is beneath it by components.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
