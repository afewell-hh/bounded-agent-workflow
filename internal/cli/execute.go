package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/afewell-hh/bounded-agent-workflow/internal/execution"
	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// ExecuteUsage is the fixed `baw run execute --help` text.
const ExecuteUsage = `Usage:
  baw run execute --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]

Runs one trusted local worker and then one verification program in the Git worktree.
The existing run record and nonsecret plan do not prove approval or authority.
One attempt per run ID; interrupted or uncertain attempts are never retried automatically.
Native agent adapters, review, acceptance, merge and recovery are not provided.
`

func isExecute(args []string) bool { return len(args) >= 2 && args[0] == "run" && args[1] == "execute" }

// parseExecute validates execute syntax without touching the filesystem.
func parseExecute(args []string) (req execution.Request, help, ok bool) {
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

// runExecute handles `baw run execute`. SIGINT/SIGTERM become cancellation
// only after valid syntax, and the prior signal behavior is restored on every
// return.
func runExecute(args []string, stdout, stderr io.Writer) int {
	req, help, ok := parseExecute(args)
	if help {
		if write(stdout, []byte(ExecuteUsage)) != nil {
			return failRecord(stderr, string(state.CodeOutputUnavailable))
		}
		return 0
	}
	if !ok {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return executeWith(ctx, req, stdout, stderr)
}

// executeWith runs a syntactically valid request under ctx.
func executeWith(ctx context.Context, req execution.Request, stdout, stderr io.Writer) int {
	passed, err := execution.Execute(ctx, req, stdout)
	if err != nil {
		return failRecord(stderr, executeCode(err))
	}
	if passed {
		return 0
	}
	return failRecord(stderr, string(execution.CodeFailed))
}

func executeCode(err error) string {
	var e *execution.Error
	if errors.As(err, &e) {
		return string(e.Code)
	}
	return codeOf(err)
}
