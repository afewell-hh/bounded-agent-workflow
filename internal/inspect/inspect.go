package inspect

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
)

// Code is a fixed, safe failure code published as `baw: CODE`.
type Code string

const (
	CodeUsage                 Code = "invalid_usage"
	CodeRepositoryUnavailable Code = "repository_unavailable"
	CodeUnsupportedFilters    Code = "unsupported_filters"
	CodeUnsupportedPartial    Code = "unsupported_partial_clone"
	CodeGitConfigInvalid      Code = "git_config_invalid"
	CodeGitFailed             Code = "git_failed"
	CodeInvalidGitOutput      Code = "invalid_git_output"
	CodeCheckpointMissing     Code = "checkpoint_missing"
	CodeCheckpointDiverged    Code = "checkpoint_diverged"
	CodeCheckpointUnborn      Code = "checkpoint_unborn"
	CodeSourceSymlink         Code = "source_symlink"
	CodeSourceUnavailable     Code = "source_unavailable"
	CodeCoordinationUnavail   Code = "coordination_unavailable"
	CodeCoordinationInvalid   Code = "coordination_invalid"
	CodeInputLimit            Code = "input_limit"
	CodeCommandTimeout        Code = "command_timeout"
	CodeCommandOutputLimit    Code = "command_output_limit"
	CodeOutputLimit           Code = "output_limit"
)

// Error carries only a fixed code; no paths, values or subprocess text.
type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }

func fail(c Code) error { return &Error{Code: c} }

// Limits holds the C1 numeric bounds. Tests may lower them.
type Limits struct {
	GitTimeout  time.Duration
	GHTimeout   time.Duration
	Total       time.Duration
	GitStdout   int
	GHStdout    int // also the local coordination file cap
	ChildStderr int
	FinalStdout int
}

// DefaultLimits are the contract values.
var DefaultLimits = Limits{
	GitTimeout:  10 * time.Second,
	GHTimeout:   30 * time.Second,
	Total:       120 * time.Second,
	GitStdout:   8 << 20,
	GHStdout:    1 << 20,
	ChildStderr: 64 << 10,
	FinalStdout: 32 << 10,
}

// Options is a validated inspect request.
type Options struct {
	Repo             string
	Checkpoint       string // lowercase 40 or 64 hex, or empty
	CoordinationFile string
	GitHubOwner      string
	GitHubRepo       string
	GitHubNumber     int
	Limits           Limits
}

var hexRE = regexp.MustCompile(`^[0-9a-f]+$`)

// Inspect performs the read-only inspection. It returns either a complete
// packet or a fixed-code error; partial packets are never returned.
func Inspect(opts Options) (*Packet, error) {
	return inspect(opts, legacyProfile())
}

// InspectTop is Inspect that also returns the resolved physical Git top
// level used by the inspection. The packet, guards and limits are those of
// Inspect; the top level is returned only with a complete packet.
func InspectTop(opts Options) (*Packet, string, error) {
	var top string
	p, err := inspectInto(opts, legacyProfile(), &top)
	if err != nil {
		return nil, "", err
	}
	return p, top, nil
}

// inspect is Inspect with the maintained sources taken from prof.
func inspect(opts Options, prof sourceProfile) (*Packet, error) {
	return inspectInto(opts, prof, nil)
}

// inspectInto is inspect that also stores the resolved top level in topOut.
func inspectInto(opts Options, prof sourceProfile, topOut *string) (*Packet, error) {
	ctx, cancel := context.WithTimeout(context.Background(), opts.Limits.Total)
	defer cancel()

	// Usage/path validation.
	if opts.Repo == "" {
		return nil, fail(CodeUsage)
	}
	canon, err := filepath.EvalSymlinks(opts.Repo)
	if err != nil {
		return nil, fail(CodeRepositoryUnavailable)
	}
	if fi, err := os.Stat(canon); err != nil || !fi.IsDir() {
		return nil, fail(CodeRepositoryUnavailable)
	}
	canon, err = filepath.Abs(canon)
	if err != nil {
		return nil, fail(CodeRepositoryUnavailable)
	}

	gitPath, err := exec.LookPath("git")
	if err != nil {
		return nil, fail(CodeGitFailed)
	}
	g := &gitRunner{path: gitPath, env: gitEnv(), limits: opts.Limits, ctx: ctx}

	// Git safety guard and state.
	top, err := g.discover(canon)
	if err != nil {
		return nil, err
	}
	g.dir = top
	if topOut != nil {
		*topOut = top
	}
	if err := g.guardConfig(); err != nil {
		return nil, err
	}
	repo := Repository{State: "observed", Checkpoint: Checkpoint{State: "not_requested"}}
	fmtOut, err := g.run("rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	switch strings.TrimSuffix(string(fmtOut), "\n") {
	case "sha1":
		repo.ObjectFormat = "sha1"
	case "sha256":
		repo.ObjectFormat = "sha256"
	default:
		return nil, fail(CodeInvalidGitOutput)
	}
	width := 40
	if repo.ObjectFormat == "sha256" {
		width = 64
	}
	if opts.Checkpoint != "" && len(opts.Checkpoint) != width {
		return nil, fail(CodeUsage)
	}

	attached, err := g.exitStatus([]int{0, 1}, "symbolic-ref", "-q", "HEAD")
	if err != nil {
		return nil, err
	}
	headOut, code, err := g.runCodes([]int{0, 1}, "rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		return nil, err
	}
	var head string
	if code == 0 {
		head = strings.TrimSuffix(string(headOut), "\n")
		if len(head) != width || !hexRE.MatchString(head) {
			return nil, fail(CodeInvalidGitOutput)
		}
		repo.HeadState = "present"
		repo.Head = &head
		if attached == 0 {
			repo.BranchState = "attached"
		} else {
			repo.BranchState = "detached"
		}
	} else {
		if attached != 0 {
			// Detached HEAD that does not resolve is not an unborn branch.
			return nil, fail(CodeGitFailed)
		}
		repo.HeadState = "unborn"
		repo.BranchState = "unborn"
	}

	statusOut, err := g.run("status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignore-submodules=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	st, err := parseStatus(statusOut, prof.member)
	if err != nil {
		return nil, err
	}

	// Status ignores submodules entirely (no submodule worktree or config is
	// touched), so staged gitlink changes come from comparing index and HEAD
	// tree metadata. Neither command reads submodules or applies
	// diff.ignoreSubmodules / submodule.<name>.ignore.
	indexOut, err := g.run("ls-files", "--stage", "-z")
	if err != nil {
		return nil, err
	}
	idx, err := parseIndex(indexOut, width, prof.member)
	if err != nil {
		return nil, err
	}
	headEntries := map[string]entry{}
	var headFiles map[string]bool
	if head != "" {
		out, err := g.run("ls-tree", "-r", "-z", "--full-tree", head)
		if err != nil {
			return nil, err
		}
		headEntries, headFiles, err = parseTree(out, width, prof.member)
		if err != nil {
			return nil, err
		}
	}
	repo.Counts.Staged = st.staged + stagedGitlinks(headEntries, idx)
	repo.Counts.Unstaged, repo.Counts.Untracked = st.unstaged, st.untracked
	// Each unmerged path counts once. Git 2.39.5 status still reports gitlink
	// conflicts under --ignore-submodules=all; the index check is defensive.
	repo.Counts.Conflicted = st.conflicted
	for p := range idx.unmerged {
		if !st.unmerged[p] {
			repo.Counts.Conflicted++
		}
	}
	repo.Counts.Submodules = idx.gitlinks

	wtOut, err := g.run("worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	linked, err := countWorktrees(wtOut)
	if err != nil {
		return nil, err
	}
	repo.Counts.LinkedWorktrees = linked

	// Checkpoint.
	if opts.Checkpoint != "" {
		if head == "" {
			return nil, fail(CodeCheckpointUnborn)
		}
		cp := opts.Checkpoint
		out, code, err := g.runCodes([]int{0, 1, 128}, "rev-parse", "--verify", "-q", "--end-of-options", cp+"^{commit}")
		if err != nil {
			return nil, err
		}
		if code != 0 || strings.TrimSuffix(string(out), "\n") != cp {
			return nil, fail(CodeCheckpointMissing)
		}
		ahead := 0
		state := "equal"
		if cp != head {
			_, code, err := g.runCodes([]int{0, 1}, "merge-base", "--is-ancestor", cp, head)
			if err != nil {
				return nil, err
			}
			if code != 0 {
				return nil, fail(CodeCheckpointDiverged)
			}
			out, err := g.run("rev-list", "--count", cp+".."+head)
			if err != nil {
				return nil, err
			}
			n, err := strconv.Atoi(strings.TrimSuffix(string(out), "\n"))
			if err != nil || n <= 0 {
				return nil, fail(CodeInvalidGitOutput)
			}
			ahead, state = n, "ancestor"
		}
		repo.Checkpoint = Checkpoint{State: state, OID: &cp, CommitsAhead: &ahead}
	}

	// Sources.
	sources, err := inspectSources(top, head, prof.paths, headFiles, idx.indexed, st.paths)
	if err != nil {
		return nil, err
	}

	// Coordination.
	coord := Coordination{Mode: "none", State: "absent"}
	switch {
	case opts.CoordinationFile != "":
		coord, err = readSnapshot(opts.CoordinationFile, opts.Limits.GHStdout)
	case opts.GitHubNumber != 0:
		coord, err = readLive(ctx, opts)
	}
	if err != nil {
		return nil, err
	}

	return &Packet{
		SchemaVersion:        1,
		Status:               "complete",
		Repository:           repo,
		Sources:              sources,
		Coordination:         coord,
		RemoteFreshness:      "unknown",
		RuntimeState:         "unknown",
		ProcessOwnership:     "unknown",
		ReservationOwnership: "unknown",
	}, nil
}

// --- Git invocation ---

var envAllow = []string{"HOME", "PATH", "TMPDIR", "USER", "LOGNAME", "LANG", "LC_ALL"}

func allowEnv(keys []string) []string {
	var env []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func gitEnv() []string {
	env := allowEnv(envAllow[:len(envAllow)-1]) // LC_ALL is fixed below
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_TERMINAL_PROMPT=0",
		"LC_ALL=C",
	)
}

// GitPrefix is the fixed option prefix of every Git invocation.
var GitPrefix = []string{
	"--no-pager", "--no-optional-locks",
	"-c", "core.fsmonitor=false",
	"-c", "core.pager=cat",
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.attributesFile=/dev/null",
	"-c", "core.excludesFile=/dev/null",
	"-c", "core.untrackedCache=false",
	"-c", "core.preloadIndex=false",
	"-c", "log.showSignature=false",
	"-c", "diff.external=",
	"-c", "submodule.recurse=false",
}

type gitRunner struct {
	path   string
	env    []string
	dir    string
	limits Limits
	ctx    context.Context
}

func mapProcErr(err error) error {
	switch {
	case errors.Is(err, proc.ErrTimeout), errors.Is(err, proc.ErrCleanup):
		// ErrCleanup: the bounded join expired with an owned process or
		// pipe holder still present.
		return fail(CodeCommandTimeout)
	case errors.Is(err, proc.ErrOutputLimit):
		return fail(CodeCommandOutputLimit)
	}
	return fail(CodeGitFailed)
}

// runCodes runs git in g.dir and accepts any exit code listed in ok.
func (g *gitRunner) runCodesIn(dir string, ok []int, args ...string) ([]byte, int, error) {
	argv := append(append([]string{"-C", dir}, GitPrefix...), args...)
	out, err := proc.Run(g.ctx, proc.Spec{
		Path: g.path, Args: argv, Env: g.env,
		Timeout: g.limits.GitTimeout, StdoutCap: g.limits.GitStdout, StderrCap: g.limits.ChildStderr,
	})
	if err != nil {
		var ee *proc.ExitError
		if errors.As(err, &ee) {
			for _, c := range ok {
				if c == ee.Code {
					return nil, ee.Code, nil
				}
			}
			return nil, ee.Code, fail(CodeGitFailed)
		}
		return nil, -1, mapProcErr(err)
	}
	return out, 0, nil
}

func (g *gitRunner) runCodes(ok []int, args ...string) ([]byte, int, error) {
	return g.runCodesIn(g.dir, ok, args...)
}

func (g *gitRunner) run(args ...string) ([]byte, error) {
	out, _, err := g.runCodes(nil, args...)
	return out, err
}

func (g *gitRunner) exitStatus(ok []int, args ...string) (int, error) {
	_, code, err := g.runCodes(ok, args...)
	return code, err
}

// discover returns the canonical top-level worktree containing dir.
func (g *gitRunner) discover(dir string) (string, error) {
	out, _, err := g.runCodesIn(dir, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		var e *Error
		if errors.As(err, &e) && e.Code == CodeGitFailed {
			// Distinguish unreadable configuration from "not a repository".
			if _, _, cerr := g.runCodesIn(dir, nil, "config", "--name-only", "--list"); cerr != nil {
				return "", fail(CodeGitConfigInvalid)
			}
			return "", fail(CodeRepositoryUnavailable)
		}
		return "", err
	}
	if string(out) != "true\n" {
		return "", fail(CodeRepositoryUnavailable)
	}
	out, _, err = g.runCodesIn(dir, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	top := strings.TrimSuffix(string(out), "\n")
	if top == "" || strings.ContainsAny(top, "\n\x00") || !filepath.IsAbs(top) {
		return "", fail(CodeInvalidGitOutput)
	}
	top, err = filepath.EvalSymlinks(top)
	if err != nil {
		return "", fail(CodeRepositoryUnavailable)
	}
	if fi, err := os.Stat(top); err != nil || !fi.IsDir() {
		return "", fail(CodeRepositoryUnavailable)
	}
	return top, nil
}

// guardConfig refuses repositories whose effective configuration names a
// clean/process filter driver or partial-clone/promisor remote. Names and
// values are inspected only internally and never published.
func (g *gitRunner) guardConfig() error {
	out, code, err := g.runCodes(nil, "config", "-z", "--name-only", "--list")
	if err != nil {
		if code > 0 {
			return fail(CodeGitConfigInvalid)
		}
		return err
	}
	filters, partial := false, false
	for _, name := range bytes.Split(out, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		n := strings.ToLower(string(name))
		first := strings.Index(n, ".")
		last := strings.LastIndex(n, ".")
		if first <= 0 || last == len(n)-1 {
			return fail(CodeInvalidGitOutput)
		}
		section, key := n[:first], n[last+1:]
		switch {
		case section == "filter" && last > first && (key == "clean" || key == "process"):
			filters = true
		case n == "extensions.partialclone":
			partial = true
		case section == "remote" && last > first && key == "promisor":
			partial = true
		}
	}
	if filters {
		return fail(CodeUnsupportedFilters)
	}
	if partial {
		return fail(CodeUnsupportedPartial)
	}
	return nil
}
