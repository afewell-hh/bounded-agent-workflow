package inspect

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Real-Git selected-source states for every role in both object formats.
// Each fixture changes only the selected role file (or, for the unborn and
// never-tracked rows, starts without it), so every count is fixed by the
// setup. Expected values are written by hand from that setup.

var common = []string{"AGENTS.md", "workflow/protocol.md", "README.md", "docs/design/product-contract.md",
	"docs/architecture/overview.md", "docs/developer/environment.md", "docs/operator/agent-lifecycle.md"}

var roles = []string{"lead", "worker", "reviewer"}

func roleFile(r string) string { return "workflow/roles/" + r + ".md" }

// poison makes inherited Git variables and a user ignore file change the
// result if the inspector honoured them.
func poison(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	must(t, tf.Write(filepath.Join(home, ".config/git/ignore"), "*\n"))
	must(t, tf.Write(filepath.Join(home, ".gitconfig"), "[status]\n\tshowUntrackedFiles = no\n"))
	t.Setenv("HOME", home)
	t.Setenv("GIT_DIR", "/nonexistent-decoy/.git")
	t.Setenv("GIT_WORK_TREE", "/nonexistent-decoy")
	t.Setenv("GIT_INDEX_FILE", "/nonexistent-decoy/index")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'status.showuntrackedfiles'='no'")
	return t.TempDir() // fixture HOME, unpoisoned
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func gitf(t *testing.T, home, dir string, args ...string) string {
	t.Helper()
	out, err := tf.Git(home, dir, args...)
	must(t, err)
	return out
}

// base writes the common sources and the given role files and commits them
// unless unborn. It returns HEAD ("" when unborn).
func base(t *testing.T, home, format string, roleFiles []string, unborn bool) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	must(t, tf.Init(home, dir, format))
	for _, p := range append(append([]string{}, common...), roleFiles...) {
		must(t, tf.Write(filepath.Join(dir, p), "dummy maintained source\n"))
	}
	if unborn {
		return dir, ""
	}
	gitf(t, home, dir, "add", "-A")
	gitf(t, home, dir, "commit", "-q", "-m", "base")
	h, err := tf.OID(home, dir, "HEAD")
	must(t, err)
	return dir, h
}

type row struct {
	name                        string
	presence, state             string
	headRef, worktreeRef        bool
	counts                      Counts
	othersCommitted, commonGone bool
}

func others(r string) []string {
	var out []string
	for _, o := range roles {
		if o != r {
			out = append(out, roleFile(o))
		}
	}
	return out
}

// setup builds the row's fixture for selected role r and returns dir, HEAD.
func setup(t *testing.T, home, format, name, r string) (string, string) {
	t.Helper()
	sel := roleFile(r)
	all := append([]string{sel}, others(r)...)
	switch name {
	case "committed":
		return base(t, home, format, all, false)
	case "modified":
		dir, h := base(t, home, format, all, false)
		must(t, tf.Write(filepath.Join(dir, sel), "edited\n"))
		return dir, h
	case "untracked":
		dir, h := base(t, home, format, others(r), false)
		must(t, tf.Write(filepath.Join(dir, sel), "new\n"))
		return dir, h
	case "never-absent":
		return base(t, home, format, others(r), false)
	case "deleted":
		dir, h := base(t, home, format, all, false)
		must(t, os.Remove(filepath.Join(dir, sel)))
		return dir, h
	case "conflict":
		dir, _ := base(t, home, format, all, false)
		gitf(t, home, dir, "checkout", "-q", "-b", "side")
		must(t, tf.Write(filepath.Join(dir, sel), "side\n"))
		gitf(t, home, dir, "commit", "-q", "-am", "side")
		gitf(t, home, dir, "checkout", "-q", "main")
		must(t, tf.Write(filepath.Join(dir, sel), "main\n"))
		gitf(t, home, dir, "commit", "-q", "-am", "main")
		h, err := tf.OID(home, dir, "HEAD")
		must(t, err)
		if _, err := tf.Git(home, dir, "merge", "-q", "side"); err == nil {
			t.Fatal("expected a merge conflict")
		}
		return dir, h
	case "head-regular-index-gitlink":
		dir, h := base(t, home, format, all, false)
		gitf(t, home, dir, "update-index", "--cacheinfo", "160000,"+h+","+sel)
		return dir, h
	case "head-gitlink-index-regular":
		dir, h0 := base(t, home, format, all, false)
		gitf(t, home, dir, "update-index", "--cacheinfo", "160000,"+h0+","+sel)
		gitf(t, home, dir, "commit", "-q", "-m", "gitlink")
		h, err := tf.OID(home, dir, "HEAD")
		must(t, err)
		gitf(t, home, dir, "add", sel) // the worktree file becomes a regular index entry
		must(t, tf.Write(filepath.Join(dir, sel), "further edit\n"))
		return dir, h
	case "intermediate-file":
		dir, h := base(t, home, format, []string{sel}, false)
		must(t, os.RemoveAll(filepath.Join(dir, "workflow/roles")))
		must(t, tf.Write(filepath.Join(dir, "workflow/roles"), "not a directory\n"))
		return dir, h
	case "unborn":
		dir := filepath.Join(t.TempDir(), "repo")
		must(t, tf.Init(home, dir, format))
		must(t, tf.Write(filepath.Join(dir, sel), "new\n"))
		return dir, ""
	}
	t.Fatalf("unknown row %s", name)
	return "", ""
}

var table = []row{
	{name: "committed", presence: "present", state: "clean", headRef: true, worktreeRef: true, othersCommitted: true},
	{name: "modified", presence: "present", state: "modified", headRef: true, worktreeRef: true, counts: Counts{Unstaged: 1}, othersCommitted: true},
	{name: "untracked", presence: "present", state: "untracked", worktreeRef: true, counts: Counts{Untracked: 1}, othersCommitted: true},
	{name: "never-absent", presence: "absent", state: "absent", othersCommitted: true},
	{name: "deleted", presence: "absent", state: "deleted", headRef: true, counts: Counts{Unstaged: 1}, othersCommitted: true},
	{name: "conflict", presence: "present", state: "modified", headRef: true, worktreeRef: true, counts: Counts{Conflicted: 1}, othersCommitted: true},
	{name: "head-regular-index-gitlink", presence: "present", state: "modified", headRef: true, worktreeRef: true, counts: Counts{Staged: 1, Submodules: 1}, othersCommitted: true},
	{name: "head-gitlink-index-regular", presence: "present", state: "modified", worktreeRef: true, counts: Counts{Staged: 1, Unstaged: 1}, othersCommitted: true},
	{name: "intermediate-file", presence: "absent", state: "deleted", headRef: true, counts: Counts{Unstaged: 1, Untracked: 1}},
	{name: "unborn", presence: "present", state: "untracked", worktreeRef: true, counts: Counts{Untracked: 1}, commonGone: true},
}

// expectSources is the hand-written oracle for requested role q on a
// fixture whose selected role is r.
func expectSources(c row, r, q, head string) []Source {
	var out []Source
	paths := []string{"AGENTS.md", "workflow/protocol.md", roleFile(q), "README.md", "docs/design/product-contract.md",
		"docs/architecture/overview.md", "docs/developer/environment.md", "docs/operator/agent-lifecycle.md"}
	for i, p := range paths {
		s := Source{Path: p}
		switch {
		case i == 2 && q == r:
			s.Presence, s.WorktreeState = c.presence, c.state
			if c.headRef {
				s.HeadRef = sp("git:" + head + ":" + p)
			}
			if c.worktreeRef {
				s.WorktreeRef = sp("worktree:" + p)
			}
		case c.commonGone, i == 2 && !c.othersCommitted:
			// Never written in this fixture.
			s.Presence, s.WorktreeState = "absent", "absent"
		default:
			s.Presence, s.WorktreeState = "present", "clean"
			s.HeadRef, s.WorktreeRef = sp("git:"+head+":"+p), sp("worktree:"+p)
		}
		out = append(out, s)
	}
	return out
}

func TestSelectedSourceStateTable(t *testing.T) {
	poison(t)
	for _, format := range []string{"sha1", "sha256"} {
		for _, c := range table {
			for _, r := range roles {
				home := t.TempDir()
				dir, head := setup(t, home, format, c.name, r)
				label := format + "/" + c.name + "/" + r
				width := 40
				if format == "sha256" {
					width = 64
				}
				if head != "" && len(head) != width {
					t.Fatalf("%s: fixture HEAD %q", label, head)
				}
				var packets = map[string]*Packet{}
				for _, q := range roles {
					p, err := InspectContext(dir, q, DefaultLimits)
					if err != nil {
						t.Fatalf("%s as %s: %v", label, q, err)
					}
					packets[q] = p
					wantRepo := Repository{State: "observed", ObjectFormat: format, HeadState: "present", Head: sp(head),
						BranchState: "attached", Counts: c.counts, Checkpoint: Checkpoint{State: "not_requested"}}
					if head == "" {
						wantRepo.HeadState, wantRepo.Head, wantRepo.BranchState = "unborn", nil, "unborn"
					}
					if !reflect.DeepEqual(p.Repository, wantRepo) {
						t.Fatalf("%s as %s: repository %+v\nwant %+v", label, q, p.Repository, wantRepo)
					}
					if want := expectSources(c, r, q, head); !reflect.DeepEqual(p.Sources, want) {
						t.Fatalf("%s as %s: sources\n got %s\nwant %s", label, q, show(p.Sources), show(want))
					}
					checkRendered(t, label+" as "+q, p, q, r)
				}
				// Legacy inspect on the same fixture equals the lead context
				// observation and is unaffected by the other requests.
				legacy, err := Inspect(Options{Repo: dir, Limits: DefaultLimits})
				if err != nil || !reflect.DeepEqual(legacy, packets["lead"]) {
					t.Fatalf("%s: legacy %+v %v", label, legacy, err)
				}
			}
		}
	}
}

// checkRendered verifies that both renderers carry exactly the requested
// role's source lines and never another role's path.
func checkRendered(t *testing.T, label string, p *Packet, q, r string) {
	t.Helper()
	text := string(RenderText(p))
	js, err := RenderJSON(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range roles {
		inText := strings.Contains(text, "Source: "+roleFile(o)+" ")
		inJSON := strings.Contains(string(js), `"path":"`+roleFile(o)+`"`)
		if inText != (o == q) || inJSON != (o == q) {
			t.Fatalf("%s: role %s text=%v json=%v", label, o, inText, inJSON)
		}
	}
	if strings.Count(text, "\nSource: ") != 8 || strings.Count(string(js), `"path":`) != 8 {
		t.Fatalf("%s: not eight sources", label)
	}
}

func show(s []Source) string {
	b, _ := json.Marshal(s)
	return string(b) + " (" + strconv.Itoa(len(s)) + ")"
}
