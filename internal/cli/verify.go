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
	"github.com/afewell-hh/bounded-agent-workflow/internal/verification"
)

// VerifyUsage is the fixed `baw run verify --help` text.
const VerifyUsage = `Usage:
  baw run verify --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]

Run one trusted local verifier against an explicit clean committed candidate and record its observed result.
Relative --repo, --state-dir and --plan paths resolve from the caller's working directory. Verifier cwd and relative verifier arguments use the physical Git top level.
This records one verifier-program observation, not complete gate evidence, a source freeze, approval or merge authority. No native agent adapter is used.
`

func isVerify(args []string) bool { return len(args) >= 2 && args[0] == "run" && args[1] == "verify" }

// parseVerify validates verify syntax without touching the filesystem.
func parseVerify(args []string) (req verification.Request, help, ok bool) {
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

// runVerify handles `baw run verify`. SIGINT/SIGTERM become cancellation
// only after valid syntax, and the prior signal behavior is restored on every
// return.
func runVerify(args []string, stdout, stderr io.Writer) int {
	req, help, ok := parseVerify(args)
	if help {
		if write(stdout, []byte(VerifyUsage)) != nil {
			return failRecord(stderr, string(verification.CodeOutputUnavailable))
		}
		return 0
	}
	if !ok {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return verifyWith(ctx, req, stdout, stderr)
}

// verifyWith runs a syntactically valid request under ctx.
func verifyWith(ctx context.Context, req verification.Request, stdout, stderr io.Writer) int {
	passed, err := verification.Verify(ctx, req, stdout)
	if err != nil {
		return failRecord(stderr, verifyCode(err))
	}
	if passed {
		return 0
	}
	return failRecord(stderr, string(verification.CodeFailed))
}

func verifyCode(err error) string {
	var e *verification.Error
	if errors.As(err, &e) {
		return string(e.Code)
	}
	return codeOf(err)
}
