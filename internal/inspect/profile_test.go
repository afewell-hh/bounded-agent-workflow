package inspect

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
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
	// never examined. The real unsafe probes run in a bounded, joined child.
	if err := os.MkdirAll(filepath.Join(top, "workflow/roles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(top, "workflow/roles/lead.md")); err != nil {
		t.Fatal(err)
	}
	r := runProfileChild(t, top, head)
	if r.Unselected != "" {
		t.Fatalf("unselected sibling probed: %v", r.Unselected)
	}
	if r.Selected != string(CodeSourceSymlink) {
		t.Fatalf("selected symlink: %q", r.Selected)
	}
}

const (
	profileChildEnv    = "BAW_INSPECT_TEST_PROFILE_CHILD" // JSON [top, head]
	profileChildPrefix = "BAW-PROFILE-CHILD-RESULT "
)

// profileChildResult holds each probe's error ("" for none, a Code, or
// "unexpected: ..." for any other error).
type profileChildResult struct {
	Unselected string
	Selected   string
}

func probeError(err error) string {
	if err == nil {
		return ""
	}
	if e, ok := err.(*Error); ok {
		return string(e.Code)
	}
	return "unexpected: " + err.Error()
}

// TestProfileChildProcess is executed only as a child of this test binary.
// It probes an unrequested and then a requested unsafe role path.
func TestProfileChildProcess(t *testing.T) {
	spec := os.Getenv(profileChildEnv)
	if spec == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(spec), &args); err != nil || len(args) != 2 {
		t.Fatalf("bad child arguments %q: %v", spec, err)
	}
	top, head := args[0], args[1]
	both := map[string]bool{"workflow/roles/worker.md": true}
	_, unselected := inspectSources(top, head, []string{"workflow/roles/worker.md"}, both, both, nil)
	_, selected := inspectSources(top, head, []string{"workflow/roles/lead.md"}, nil, nil, nil)
	b, err := json.Marshal(profileChildResult{probeError(unselected), probeError(selected)})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n%s%s\n", profileChildPrefix, b)
}

// runProfileChild runs the probes in a child of this test binary, killed and
// always waited for within 10 seconds.
func runProfileChild(t *testing.T, top, head string) profileChildResult {
	t.Helper()
	spec, err := json.Marshal([]string{top, head})
	if err != nil {
		t.Fatal(err)
	}
	exe := os.Args[0]
	if !filepath.IsAbs(exe) {
		t.Fatalf("test binary path %q is not absolute", exe)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestProfileChildProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), profileChildEnv+"="+string(spec))
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err = cmd.Run() // waits for the child also when it is killed at the deadline
	if ctx.Err() != nil {
		t.Fatalf("profile child not finished within 10s; killed and joined after %v (%v)", time.Since(start), err)
	}
	if err != nil {
		t.Fatalf("profile child: %v\nstdout %q\nstderr %q", err, out.String(), errb.String())
	}
	_, res, ok := strings.Cut(out.String(), profileChildPrefix)
	res, _, _ = strings.Cut(res, "\n")
	var r profileChildResult
	if !ok || json.Unmarshal([]byte(res), &r) != nil {
		t.Fatalf("profile child reported no result: %q", out.String())
	}
	return r
}

func sp(s string) *string { return &s }
