// Package inspect builds the read-only `baw inspect` context packet.
//
// Every value in a packet is a fixed application string, a validated object
// ID, a count, or validated coordination metadata. Repository-controlled text
// (branch names, filenames, messages, config, diffs) is never copied into it.
package inspect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// SourcePaths is the fixed maintained-file allowlist, in output order.
var SourcePaths = []string{
	"AGENTS.md",
	"workflow/protocol.md",
	"workflow/roles/lead.md",
	"README.md",
	"docs/design/product-contract.md",
	"docs/architecture/overview.md",
	"docs/developer/environment.md",
	"docs/operator/agent-lifecycle.md",
}

// Packet is schema version 1 of a successful inspection.
type Packet struct {
	SchemaVersion        int          `json:"schema_version"`
	Status               string       `json:"status"`
	Repository           Repository   `json:"repository"`
	Sources              []Source     `json:"sources"`
	Coordination         Coordination `json:"coordination"`
	RemoteFreshness      string       `json:"remote_freshness"`
	RuntimeState         string       `json:"runtime_state"`
	ProcessOwnership     string       `json:"process_ownership"`
	ReservationOwnership string       `json:"reservation_ownership"`
}

type Repository struct {
	State        string     `json:"state"`
	ObjectFormat string     `json:"object_format"`
	HeadState    string     `json:"head_state"`
	Head         *string    `json:"head"`
	BranchState  string     `json:"branch_state"`
	Counts       Counts     `json:"counts"`
	Checkpoint   Checkpoint `json:"checkpoint"`
}

type Counts struct {
	Staged          int `json:"staged"`
	Unstaged        int `json:"unstaged"`
	Untracked       int `json:"untracked"`
	Conflicted      int `json:"conflicted"`
	LinkedWorktrees int `json:"linked_worktrees"`
	Submodules      int `json:"submodules"`
}

type Checkpoint struct {
	State        string  `json:"state"`
	OID          *string `json:"oid"`
	CommitsAhead *int    `json:"commits_ahead"`
}

type Source struct {
	Path          string  `json:"path"`
	Presence      string  `json:"presence"`
	WorktreeState string  `json:"worktree_state"`
	HeadRef       *string `json:"head_ref"`
	WorktreeRef   *string `json:"worktree_ref"`
}

type Coordination struct {
	Mode       string  `json:"mode"`
	State      string  `json:"state"`
	Number     *int    `json:"number"`
	IssueState *string `json:"issue_state"`
	UpdatedAt  *string `json:"updated_at"`
	SourceURL  *string `json:"source_url"`
}

// RenderJSON returns the packet as one JSON object followed by a newline.
func RenderJSON(p *Packet) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func refOrDash(s *string) string {
	if s == nil {
		return "-"
	}
	return *s
}

// RenderText returns the fixed-label terminal report.
func RenderText(p *Packet) []byte {
	var b strings.Builder
	r := p.Repository
	b.WriteString("BAW inspection\n")
	head := "unborn"
	if r.Head != nil {
		head = *r.Head
	}
	fmt.Fprintf(&b, "HEAD: %s\n", head)
	fmt.Fprintf(&b, "Branch state: %s\n", r.BranchState)
	fmt.Fprintf(&b, "Object format: %s\n", r.ObjectFormat)
	c := r.Counts
	fmt.Fprintf(&b, "Changes: staged=%d unstaged=%d untracked=%d conflicted=%d\n", c.Staged, c.Unstaged, c.Untracked, c.Conflicted)
	fmt.Fprintf(&b, "Linked worktrees: %d\n", c.LinkedWorktrees)
	fmt.Fprintf(&b, "Submodules: %d\n", c.Submodules)
	if r.Checkpoint.State == "not_requested" {
		b.WriteString("Checkpoint: not_requested\n")
	} else {
		fmt.Fprintf(&b, "Checkpoint: %s %s commits_ahead=%d\n", r.Checkpoint.State, *r.Checkpoint.OID, *r.Checkpoint.CommitsAhead)
	}
	for _, s := range p.Sources {
		fmt.Fprintf(&b, "Source: %s presence=%s worktree_state=%s head_ref=%s worktree_ref=%s\n",
			s.Path, s.Presence, s.WorktreeState, refOrDash(s.HeadRef), refOrDash(s.WorktreeRef))
	}
	co := p.Coordination
	if co.Mode == "none" {
		b.WriteString("Coordination: none\n")
	} else {
		fmt.Fprintf(&b, "Coordination: %s number=%d state=%s updated_at=%s source_url=%s\n",
			co.Mode, *co.Number, *co.IssueState, *co.UpdatedAt, *co.SourceURL)
	}
	b.WriteString("Remote freshness: unknown\n")
	b.WriteString("Runtime state: unknown\n")
	b.WriteString("Process ownership: unknown\n")
	b.WriteString("Reservation ownership: unknown\n")
	return []byte(b.String())
}
