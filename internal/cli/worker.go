package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
	"github.com/afewell-hh/bounded-agent-workflow/internal/worker"
)

// WorkerUsage is the fixed `baw run worker --help` text.
const WorkerUsage = `Usage:
  baw run worker --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]

Runs one approved native Claude Code worker (profile claude-2.1.285-supervised-v1, darwin/arm64 only)
on a clean committed worktree, with the full frozen project instructions supplied on stdin.
It records observations only: no tests, review, acceptance, commit, merge, authority or recovery.
One attempt per run ID; an interrupted or uncertain attempt is never retried or replayed.
`

func isWorker(args []string) bool { return len(args) >= 2 && args[0] == "run" && args[1] == "worker" }

// parseWorker validates worker syntax without touching the filesystem.
func parseWorker(args []string) (req worker.Request, help, ok bool) {
	rest := args[2:]
	if len(rest) == 1 && rest[0] == "--help" {
		return req, true, true
	}
	vals := map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < len(rest); i++ {
		name, val, hasVal := strings.Cut(rest[i], "=")
		if seen[name] {
			return req, false, false
		}
		seen[name] = true
		switch name {
		case "--json":
			if hasVal {
				return req, false, false
			}
			req.JSON = true
			continue
		case "--repo", "--state-dir", "--run-id", "--plan":
		default:
			return req, false, false
		}
		if !hasVal {
			if i+1 >= len(rest) {
				return req, false, false
			}
			i++
			val = rest[i]
		}
		if val == "" || strings.ContainsRune(val, 0) {
			return req, false, false
		}
		vals[name] = val
	}
	req.Repo, req.StateDir, req.RunID, req.Plan = vals["--repo"], vals["--state-dir"], vals["--run-id"], vals["--plan"]
	if req.Repo == "" || req.StateDir == "" || req.Plan == "" || !state.ValidRunID(req.RunID) {
		return req, false, false
	}
	return req, false, true
}

// runWorker handles `baw run worker`. SIGINT/SIGTERM become cancellation
// only after valid syntax, and the prior signal behavior is restored on every
// return.
func runWorker(args []string, stdout, stderr io.Writer) int {
	req, help, ok := parseWorker(args)
	if help {
		if write(stdout, []byte(WorkerUsage)) != nil {
			return failRecord(stderr, string(worker.CodeOutputUnavailable))
		}
		return 0
	}
	if !ok {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return workerWith(ctx, req, stdout, stderr)
}

// workerWith runs a syntactically valid request under ctx.
func workerWith(ctx context.Context, req worker.Request, stdout, stderr io.Writer) int {
	completed, err := worker.Run(ctx, req, stdout)
	if err != nil {
		return failRecord(stderr, workerCode(err))
	}
	if completed {
		return 0
	}
	return failRecord(stderr, string(worker.CodeFailed))
}

func workerCode(err error) string {
	var e *worker.Error
	if errors.As(err, &e) {
		return string(e.Code)
	}
	return codeOf(err)
}
