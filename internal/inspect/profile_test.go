package inspect

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Hand-written oracles, deliberately not derived from production tables.
var (
	wantLegacy = []string{"AGENTS.md", "workflow/protocol.md", "workflow/roles/lead.md", "README.md",
		"docs/design/product-contract.md", "docs/architecture/overview.md", "docs/developer/environment.md",
		"docs/operator/agent-lifecycle.md"}
	wantContext = map[string][]string{
		"lead": {"AGENTS.md", "workflow/protocol.md", "workflow/roles/lead.md", "README.md",
			"docs/design/product-contract.md", "docs/architecture/overview.md", "docs/developer/environment.md",
			"docs/operator/agent-lifecycle.md"},
		"worker": {"AGENTS.md", "workflow/protocol.md", "workflow/roles/worker.md", "README.md",
			"docs/design/product-contract.md", "docs/architecture/overview.md", "docs/developer/environment.md",
			"docs/operator/agent-lifecycle.md"},
		"reviewer": {"AGENTS.md", "workflow/protocol.md", "workflow/roles/reviewer.md", "README.md",
			"docs/design/product-contract.md", "docs/architecture/overview.md", "docs/developer/environment.md",
			"docs/operator/agent-lifecycle.md"},
	}
)

func TestContextSourcePathsFixedAndFresh(t *testing.T) {
	for role, want := range wantContext {
		got, ok := ContextSourcePaths(role)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: %v %v", role, ok, got)
		}
		got[2] = "mutated" // a caller's copy cannot change the next request
		if again, _ := ContextSourcePaths(role); !reflect.DeepEqual(again, want) {
			t.Fatalf("%s: shared slice %v", role, again)
		}
	}
	for _, bad := range []string{"", "helper", "Lead", "WORKER", "lead ", " lead", "lead.md", "../lead", "roles/lead", "lead\x00", "reviewers"} {
		if got, ok := ContextSourcePaths(bad); ok || got != nil {
			t.Fatalf("role %q accepted", bad)
		}
		if p, err := InspectContext("/nonexistent", bad, DefaultLimits); p != nil || err == nil || err.(*Error).Code != CodeUsage {
			t.Fatalf("role %q: %v %v", bad, p, err)
		}
	}
	if !reflect.DeepEqual(SourcePaths, wantLegacy) || !reflect.DeepEqual(legacyProfile().paths, wantLegacy) {
		t.Fatalf("legacy profile changed: %v", SourcePaths)
	}
	m := legacyProfile().member
	if len(m) != 8 || m["workflow/roles/worker.md"] || m["workflow/roles/reviewer.md"] || !m["workflow/roles/lead.md"] {
		t.Fatalf("legacy membership %v", m)
	}
}

// Synthetic Git output: counts cover every record, path states only the
// request's members, for the legacy profile and each role.
func TestParsersUseRequestMembership(t *testing.T) {
	oid := strings.Repeat("a", 40)
	rec := func(p string) string { return "1 .M N... 100644 100644 100644 " + oid + " " + oid + " " + p + "\x00" }
	status := rec("workflow/roles/lead.md") + rec("workflow/roles/worker.md") + "? workflow/roles/reviewer.md\x00"
	index := "100644 " + oid + " 0\tworkflow/roles/lead.md\x00100644 " + oid + " 0\tworkflow/roles/worker.md\x00"
	tree := "100644 blob " + oid + "\tworkflow/roles/lead.md\x00100644 blob " + oid + "\tworkflow/roles/worker.md\x00"
	cases := map[string]struct {
		member  map[string]bool
		kind    map[string]statusKind
		indexed map[string]bool
	}{
		"legacy":   {legacyProfile().member, map[string]statusKind{"workflow/roles/lead.md": kindChanged}, map[string]bool{"workflow/roles/lead.md": true}},
		"lead":     {map[string]bool{"workflow/roles/lead.md": true}, map[string]statusKind{"workflow/roles/lead.md": kindChanged}, map[string]bool{"workflow/roles/lead.md": true}},
		"worker":   {map[string]bool{"workflow/roles/worker.md": true}, map[string]statusKind{"workflow/roles/worker.md": kindChanged}, map[string]bool{"workflow/roles/worker.md": true}},
		"reviewer": {map[string]bool{"workflow/roles/reviewer.md": true}, map[string]statusKind{"workflow/roles/reviewer.md": kindUntracked}, map[string]bool{}},
	}
	for name, c := range cases {
		st, err := parseStatus([]byte(status), c.member)
		if err != nil || st.unstaged != 2 || st.untracked != 1 || !reflect.DeepEqual(st.paths, c.kind) {
			t.Fatalf("%s status %+v %v", name, st, err)
		}
		idx, err := parseIndex([]byte(index), 40, c.member)
		if err != nil || len(idx.stage0) != 2 || !reflect.DeepEqual(idx.indexed, c.indexed) {
			t.Fatalf("%s index %+v %v", name, idx, err)
		}
		entries, files, err := parseTree([]byte(tree), 40, c.member)
		if err != nil || len(entries) != 2 || !reflect.DeepEqual(files, c.indexed) {
			t.Fatalf("%s tree %v %v", name, files, err)
		}
	}
}

// The "tracked but missing with no status record" state cannot be produced
// honestly with real Git, so it is driven through the classification seam
// with independently chosen inputs.
func TestMissingTrackedWithoutStatusIsUnknown(t *testing.T) {
	top := t.TempDir()
	head := strings.Repeat("b", 64)
	path := "workflow/roles/worker.md"
	both := map[string]bool{path: true}
	got, err := inspectSources(top, head, []string{path}, both, both, map[string]statusKind{})
	want := []Source{{Path: path, Presence: "absent", WorktreeState: "unknown", HeadRef: sp("git:" + head + ":" + path)}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("in HEAD: %+v %v", got, err)
	}
	// Indexed only (not a regular blob in HEAD): no head reference.
	got, err = inspectSources(top, head, []string{path}, map[string]bool{}, both, map[string]statusKind{})
	want = []Source{{Path: path, Presence: "absent", WorktreeState: "unknown"}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("index only: %+v %v", got, err)
	}
	// Only the requested paths are probed: an unsafe unrequested sibling is
	// never examined.
	if err := os.MkdirAll(filepath.Join(top, "workflow/roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(top, "workflow/roles/lead.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectSources(top, head, []string{path}, both, both, nil); err != nil {
		t.Fatalf("unselected sibling probed: %v", err)
	}
	if _, err := inspectSources(top, head, []string{"workflow/roles/lead.md"}, nil, nil, nil); err == nil || err.(*Error).Code != CodeSourceSymlink {
		t.Fatalf("selected symlink: %v", err)
	}
}

func sp(s string) *string { return &s }
