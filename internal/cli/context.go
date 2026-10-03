package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
)

// ContextUsage is the fixed `baw context --help` text.
const ContextUsage = `Usage:
  baw context --repo PATH --role ROLE [--json]

ROLE is lead, worker or reviewer.
Paths are relative to the top level of the Git worktree containing --repo.
Read AGENTS.md, workflow/protocol.md and only the selected role.
Reconcile the assigned ticket, approved authority and run evidence before action.
This packet grants no assignment, readiness or authority and starts no agent.
`

// MaxContextOutput caps a rendered context packet or context help.
const MaxContextOutput = 65536

// parseContext validates complete `baw context` syntax and the role without
// touching the filesystem. help is set only for a lone --help.
func parseContext(args []string) (repo, role string, asJSON, help, ok bool) {
	rest := args[1:]
	if len(rest) == 1 && rest[0] == "--help" {
		return "", "", false, true, true
	}
	seen := map[string]bool{}
	for i := 0; i < len(rest); i++ {
		name, val, hasVal := strings.Cut(rest[i], "=")
		if seen[name] {
			return "", "", false, false, false
		}
		seen[name] = true
		switch name {
		case "--json":
			if hasVal {
				return "", "", false, false, false
			}
			asJSON = true
			continue
		case "--repo", "--role":
		default:
			return "", "", false, false, false
		}
		if !hasVal {
			if i+1 >= len(rest) {
				return "", "", false, false, false
			}
			i++
			val = rest[i]
		}
		if val == "" {
			return "", "", false, false, false
		}
		if name == "--repo" {
			repo = val
		} else {
			role = val
		}
	}
	if _, valid := inspect.ContextSourcePaths(role); repo == "" || !valid {
		return "", "", false, false, false
	}
	return repo, role, asJSON, false, true
}

type contextPacket struct {
	SchemaVersion int             `json:"schema_version"`
	Operation     string          `json:"operation"`
	Role          string          `json:"role"`
	Assignment    string          `json:"assignment"`
	Authority     string          `json:"authority"`
	Readiness     string          `json:"readiness"`
	Snapshot      string          `json:"snapshot"`
	ReadingOrder  []string        `json:"reading_order"`
	Inspection    *inspect.Packet `json:"inspection"`
}

// renderContext renders the complete context packet. The embedded
// inspection uses the inspect schema and terminal report unchanged.
func renderContext(role string, p *inspect.Packet, asJSON bool) ([]byte, error) {
	order := []string{"AGENTS.md", "workflow/protocol.md", "workflow/roles/" + role + ".md"}
	if asJSON {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		err := enc.Encode(contextPacket{1, "context", role, "unassigned", "not_evaluated", "not_evaluated", "non_atomic", order, p})
		return buf.Bytes(), err
	}
	head := "BAW context references\nRole: " + role + "\nAssignment: unassigned\nAuthority: not_evaluated\n" +
		"Readiness: not_evaluated\nSnapshot: non_atomic\nRead first: " + order[0] + "\nRead next: " + order[1] +
		"\nRead role: " + order[2] + "\n"
	return append([]byte(head), inspect.RenderText(p)...), nil
}

// deliverContext writes a context packet or help within MaxContextOutput.
func deliverContext(w io.Writer, b []byte) string { return deliverBounded(w, b, MaxContextOutput) }

func runContext(args []string, stdout, stderr io.Writer, limits *inspect.Limits) int {
	repo, role, asJSON, help, ok := parseContext(args)
	if help {
		if code := deliverContext(stdout, []byte(ContextUsage)); code != "" {
			return failRecord(stderr, code)
		}
		return 0
	}
	if !ok {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	l := inspect.DefaultLimits
	if limits != nil {
		l = *limits
	}
	p, err := inspect.InspectContext(repo, role, l)
	if err != nil {
		return failRecord(stderr, codeOf(err))
	}
	out, err := renderContext(role, p, asJSON)
	if err != nil {
		return failRecord(stderr, string(inspect.CodeOutputLimit))
	}
	if code := deliverContext(stdout, out); code != "" {
		return failRecord(stderr, code)
	}
	return 0
}
