package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/review"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// ReviewUsage is the fixed `baw run review --help` text.
const ReviewUsage = `Usage: baw run review --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]

Run one trusted reviewer program against a clean committed candidate after a recorded successful worker/verifier attempt.
Relative --repo, --state-dir and --plan paths resolve from the caller's working directory. Reviewer cwd and relative reviewer arguments use the physical Git top level.
A program verdict grants no approval, acceptance or merge authority. No native agent adapter is used.
`

var candidateRE = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

func isReview(args []string) bool { return len(args) >= 2 && args[0] == "run" && args[1] == "review" }

// parseReview validates review syntax without touching the filesystem.
func parseReview(args []string) (req review.Request, help, ok bool) {
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
		case "--repo", "--state-dir", "--run-id", "--candidate", "--plan":
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
	req.Repo, req.StateDir, req.RunID = vals["--repo"], vals["--state-dir"], vals["--run-id"]
	req.Candidate, req.Plan = vals["--candidate"], vals["--plan"]
	if req.Repo == "" || req.StateDir == "" || req.Plan == "" || !state.ValidRunID(req.RunID) ||
		!candidateRE.MatchString(req.Candidate) {
		return req, false, false
	}
	return req, false, true
}

// runReview handles `baw run review`. SIGINT/SIGTERM become cancellation
// only after valid syntax, and the prior signal behavior is restored on every
// return.
func runReview(args []string, stdout, stderr io.Writer) int {
	req, help, ok := parseReview(args)
	if help {
		if write(stdout, []byte(ReviewUsage)) != nil {
			return failRecord(stderr, string(review.CodeOutputUnavailable))
		}
		return 0
	}
	if !ok {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return reviewWith(ctx, req, stdout, stderr)
}

// reviewWith runs a syntactically valid request under ctx.
func reviewWith(ctx context.Context, req review.Request, stdout, stderr io.Writer) int {
	passed, err := review.Review(ctx, req, stdout)
	if err != nil {
		return failRecord(stderr, reviewCode(err))
	}
	if passed {
		return 0
	}
	return failRecord(stderr, string(review.CodeFailed))
}

func reviewCode(err error) string {
	var e *review.Error
	if errors.As(err, &e) {
		return string(e.Code)
	}
	return codeOf(err)
}
