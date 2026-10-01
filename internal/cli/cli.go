// Package cli implements the `baw` command line.
package cli

import (
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
)

// Usage is the fixed help text.
const Usage = `Usage:
  baw inspect --repo PATH [--checkpoint FULL_COMMIT_SHA] [--coordination-file FILE | --github OWNER/REPO#NUMBER] [--json]
  baw --help

baw inspect reports read-only Git state counts, fixed maintained-source
references and coordination metadata. Remote freshness, runtime state,
process ownership and reservation ownership are always unknown.

Exit status: 0 complete inspection, 1 inspection failed (stderr "baw: CODE"),
2 invalid usage.
`

var checkpointRE = regexp.MustCompile(`^(?:[0-9A-Fa-f]{40}|[0-9A-Fa-f]{64})$`)

func isHelp(a string) bool { return a == "--help" || a == "-h" || a == "help" }

func parse(args []string) (inspect.Options, bool, bool, bool) {
	opts := inspect.Options{Limits: inspect.DefaultLimits}
	if len(args) == 1 && isHelp(args[0]) {
		return opts, false, true, true
	}
	if len(args) == 0 || args[0] != "inspect" {
		return opts, false, false, false
	}
	rest := args[1:]
	if len(rest) == 1 && isHelp(rest[0]) {
		return opts, false, true, true
	}
	seen := map[string]bool{}
	asJSON := false
	github := ""
	for i := 0; i < len(rest); i++ {
		name, val, hasVal := strings.Cut(rest[i], "=")
		if seen[name] {
			return opts, false, false, false
		}
		seen[name] = true
		switch name {
		case "--json":
			if hasVal {
				return opts, false, false, false
			}
			asJSON = true
			continue
		case "--repo", "--checkpoint", "--coordination-file", "--github":
		default:
			return opts, false, false, false
		}
		if !hasVal {
			if i+1 >= len(rest) {
				return opts, false, false, false
			}
			i++
			val = rest[i]
		}
		if val == "" {
			return opts, false, false, false
		}
		switch name {
		case "--repo":
			opts.Repo = val
		case "--checkpoint":
			if !checkpointRE.MatchString(val) {
				return opts, false, false, false
			}
			opts.Checkpoint = strings.ToLower(val)
		case "--coordination-file":
			opts.CoordinationFile = val
		case "--github":
			github = val
		}
	}
	if opts.Repo == "" || (opts.CoordinationFile != "" && github != "") {
		return opts, false, false, false
	}
	if github != "" {
		ownerRepo, num, ok := strings.Cut(github, "#")
		owner, repo, ok2 := strings.Cut(ownerRepo, "/")
		n, ok3 := inspect.ParseIssueNumber(num)
		if !ok || !ok2 || !ok3 || !inspect.ValidOwnerRepo(owner, repo) {
			return opts, false, false, false
		}
		opts.GitHubOwner, opts.GitHubRepo, opts.GitHubNumber = owner, repo, n
	}
	return opts, asJSON, false, true
}

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, nil)
}

// RunWithLimits is Run with test-adjustable limits.
func RunWithLimits(args []string, stdout, stderr io.Writer, limits inspect.Limits) int {
	return run(args, stdout, stderr, &limits)
}

func run(args []string, stdout, stderr io.Writer, limits *inspect.Limits) int {
	opts, asJSON, help, ok := parse(args)
	if help {
		io.WriteString(stdout, Usage)
		return 0
	}
	if !ok {
		io.WriteString(stderr, "baw: invalid_usage\n")
		return 2
	}
	if limits != nil {
		opts.Limits = *limits
	}
	p, err := inspect.Inspect(opts)
	var out []byte
	if err == nil {
		if asJSON {
			out, err = inspect.RenderJSON(p)
		} else {
			out = inspect.RenderText(p)
		}
		if err == nil && len(out) > opts.Limits.FinalStdout {
			err = &inspect.Error{Code: inspect.CodeOutputLimit}
		}
	}
	if err != nil {
		code := inspect.CodeGitFailed
		var e *inspect.Error
		if errors.As(err, &e) {
			code = e.Code
		} else {
			code = inspect.CodeOutputLimit
		}
		io.WriteString(stderr, "baw: "+string(code)+"\n")
		if code == inspect.CodeUsage {
			return 2
		}
		return 1
	}
	stdout.Write(out)
	return 0
}
