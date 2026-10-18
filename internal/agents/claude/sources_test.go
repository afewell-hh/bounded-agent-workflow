package claude

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
)

// Hand-written per-directory rows 1-3 as "class kind suffix", independent of
// the production lists.
var handDirRows = []string{
	"entry F CLAUDE.md",
	"entry F CLAUDE.local.md",
	"entry F AGENTS.md",
	"entry F .claude/CLAUDE.md",
	"scoped_settings F .claude/settings.json",
	"scoped_settings F .claude/settings.local.json",
	"scoped_settings F .mcp.json",
	"scoped_settings D .claude/hooks",
	"scoped_custom D .claude/rules",
	"scoped_custom D .claude/skills",
	"scoped_custom D .claude/commands",
	"scoped_custom D .claude/agents",
	"scoped_custom D .claude/plugins",
	"scoped_custom D .claude/memory",
}

// Hand-written fixed rows for home /h and worktree /w as "class kind path".
var handFixedRows = []string{
	"user F /h/.claude/CLAUDE.md",
	"user F /h/.claude/CLAUDE.local.md",
	"user F /h/.claude/settings.json",
	"user F /h/.claude/settings.local.json",
	"user F /h/.claude/.mcp.json",
	"user D /h/.claude/rules",
	"user D /h/.claude/skills",
	"user D /h/.claude/commands",
	"user D /h/.claude/agents",
	"user D /h/.claude/plugins",
	"user D /h/.claude/memory",
	"alternate_config F /h/.config/claude/CLAUDE.md",
	"alternate_config F /h/.config/claude/settings.json",
	"alternate_config F /h/.config/claude/settings.local.json",
	"alternate_config D /h/.config/claude/rules",
	"alternate_config D /h/.config/claude/skills",
	"alternate_config D /h/.config/claude/plugins",
	"alternate_config D /h/.config/claude/memory",
	"project_memory D /h/.claude/projects/-w/memory",
	"native_state D /h/.claude",
	"native_state D /h/.claude/projects",
	"native_state F /h/.claude.json",
	"admin_managed F /Library/Application Support/ClaudeCode/managed-settings.json",
	"admin_managed F /Library/Application Support/ClaudeCode/managed-mcp.json",
	"admin_managed F /Library/Application Support/ClaudeCode/CLAUDE.md",
	"admin_managed D /Library/Application Support/ClaudeCode/managed-settings.d",
	"admin_managed D /Library/Application Support/ClaudeCode/rules",
	"admin_managed D /Library/Application Support/ClaudeCode/skills",
	"admin_managed D /Library/Application Support/ClaudeCode/plugins",
	"admin_managed D /Library/Application Support/ClaudeCode/agents",
	"admin_managed D /Library/Application Support/ClaudeCode/memory",
}

// The matrix enumerates exactly the hand-written set: rows 1-3 for "/", W
// and the nested directory, plus every fixed row, each once, sorted.
func TestMatrixCompleteEnumeration(t *testing.T) {
	got, err := Matrix("/h", "/w", []string{"sub"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{}
	for _, r := range handFixedRows {
		f := strings.SplitN(r, " ", 3)
		want[f[2]] = f[0] + " " + f[1]
	}
	for _, d := range []string{"/", "/w", "/w/sub"} {
		for _, r := range handDirRows {
			f := strings.SplitN(r, " ", 3)
			want[filepath.Join(d, f[2])] = f[0] + " " + f[1]
		}
	}
	if len(got) != len(want) || len(got) != 3*14+31 {
		t.Fatalf("got %d paths, want %d", len(got), len(want))
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool { return got[i].Path < got[j].Path }) {
		t.Fatal("not sorted")
	}
	for _, sp := range got {
		if want[sp.Path] != sp.Class+" "+sp.Kind {
			t.Errorf("%s: %s %s, want %q", sp.Path, sp.Class, sp.Kind, want[sp.Path])
		}
	}
}

// A home that is a worktree ancestor yields each shared path once, keeping
// its first (project) row; an oversized list fails before any observation.
func TestMatrixDedupAndBound(t *testing.T) {
	got, err := Matrix("/u", "/u/r", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, sp := range got {
		seen[sp.Path]++
		if sp.Path == "/u/.claude/CLAUDE.md" && sp.Class != ClassEntry {
			t.Errorf("shared path kept class %s", sp.Class)
		}
	}
	for p, n := range seen {
		if n != 1 {
			t.Errorf("%s listed %d times", p, n)
		}
	}
	if seen["/u/.claude/CLAUDE.local.md"] != 1 || seen["/u/.claude/projects/-u-r/memory"] != 1 {
		t.Error("user-only rows missing")
	}
	var many []string
	for i := 0; i < 20; i++ {
		many = append(many, "d"+string(rune('a'+i)))
	}
	if _, err := Matrix("/h", "/w", many); err != ErrSourceUnavailable {
		t.Fatalf("oversized list: %v", err)
	}
}

type srcFixture struct {
	h, w string
	uid  uint32
}

func newSrcFixture(t *testing.T) srcFixture {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := srcFixture{h: filepath.Join(root, "home"), w: filepath.Join(root, "wt"), uid: uint32(os.Getuid())}
	for _, d := range []string{f.h, f.w} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// lstat hides a real admin root on the host, so rows are exercised with
// synthetic files only; admin paths under redirect go to the fixture.
func (f srcFixture) lstat(adminDir string) Lstat {
	return func(p string) (fs.FileInfo, error) {
		if p == AdminRoot || strings.HasPrefix(p, AdminRoot+"/") {
			if adminDir == "" {
				return nil, &fs.PathError{Op: "lstat", Path: p, Err: syscall.ENOENT}
			}
			p = adminDir + strings.TrimPrefix(p, AdminRoot)
		}
		return os.Lstat(p)
	}
}

func (f srcFixture) observe(t *testing.T, adminDir string) ([]SourceEntry, error) {
	t.Helper()
	paths, err := Matrix(f.h, f.w, nil)
	if err != nil {
		t.Fatal(err)
	}
	es, err := Observe(paths, f.uid, f.lstat(adminDir))
	if err == nil {
		err = CheckOwners(es, f.uid)
	}
	return es, err
}

func status(es []SourceEntry, p string) string {
	for _, e := range es {
		if e.Path == p {
			return e.Status
		}
	}
	return "missing"
}

func TestObserveRows(t *testing.T) {
	f := newSrcFixture(t)
	es, err := f.observe(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if status(es, filepath.Join(f.w, "CLAUDE.md")) != StatusAbsent || status(es, filepath.Join(f.h, ".claude")) != StatusAbsent {
		t.Fatal("empty fixture not absent")
	}
	// Safe present user and project sources are recorded, not rejected.
	must(t, os.MkdirAll(filepath.Join(f.h, ".claude", "skills"), 0o755))
	must(t, os.WriteFile(filepath.Join(f.h, ".claude", "settings.json"), []byte("{}"), 0o600))
	must(t, os.WriteFile(filepath.Join(f.w, "AGENTS.md"), []byte("x"), 0o644))
	must(t, os.WriteFile(filepath.Join(f.h, ".claude.json"), []byte("{}"), 0o600))
	es, err = f.observe(t, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{".claude/skills", ".claude/settings.json", ".claude.json"} {
		if status(es, filepath.Join(f.h, p)) != StatusSafe {
			t.Errorf("%s not present_safe", p)
		}
	}
	if status(es, filepath.Join(f.w, "AGENTS.md")) != StatusSafe {
		t.Error("project entry not present_safe")
	}
}

func TestObserveRejections(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f srcFixture, admin string)
	}{
		{"symlink leaf", func(f srcFixture, _ string) {
			os.Symlink("/etc/hosts", filepath.Join(f.w, "CLAUDE.md"))
		}},
		{"symlink on chain", func(f srcFixture, _ string) {
			os.Mkdir(filepath.Join(f.w, "real"), 0o755)
			os.Symlink(filepath.Join(f.w, "real"), filepath.Join(f.w, ".claude"))
		}},
		{"non-directory parent", func(f srcFixture, _ string) {
			os.WriteFile(filepath.Join(f.w, ".claude"), nil, 0o644)
		}},
		{"wrong type", func(f srcFixture, _ string) {
			os.Mkdir(filepath.Join(f.w, "CLAUDE.md"), 0o755)
		}},
		{"group writable", func(f srcFixture, _ string) {
			p := filepath.Join(f.w, "AGENTS.md")
			os.WriteFile(p, nil, 0o644)
			os.Chmod(p, 0o664)
		}},
		{"writable intermediate before absent alternate source", func(f srcFixture, _ string) {
			p := filepath.Join(f.h, ".config")
			must(t, os.Mkdir(p, 0o755))
			must(t, os.Chmod(p, 0o777))
		}},
		{"alternate config present", func(f srcFixture, _ string) {
			os.MkdirAll(filepath.Join(f.h, ".config", "claude"), 0o755)
			os.WriteFile(filepath.Join(f.h, ".config", "claude", "settings.json"), nil, 0o600)
		}},
		{"fresh project memory present", func(f srcFixture, _ string) {
			os.MkdirAll(filepath.Join(f.h, ".claude", "projects", EncodedProject(f.w), "memory"), 0o700)
		}},
		{"admin managed source present", func(f srcFixture, admin string) {
			os.WriteFile(filepath.Join(admin, "managed-settings.json"), nil, 0o644)
		}},
	}
	for _, c := range cases {
		f := newSrcFixture(t)
		admin := filepath.Join(filepath.Dir(f.h), "admin")
		must(t, os.Mkdir(admin, 0o755))
		c.setup(f, admin)
		if _, err := f.observe(t, admin); err != ErrSourceUnavailable {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// The admin root directory alone may exist safely.
	f := newSrcFixture(t)
	admin := filepath.Join(filepath.Dir(f.h), "admin")
	must(t, os.Mkdir(admin, 0o755))
	if _, err := f.observe(t, admin); err != nil {
		t.Fatalf("empty admin root: %v", err)
	}
}

// Unreadable metadata is unavailable, never absence.
func TestObserveErrorIsNotAbsence(t *testing.T) {
	f := newSrcFixture(t)
	paths, _ := Matrix(f.h, f.w, nil)
	target := filepath.Join(f.h, ".claude")
	lstat := func(p string) (fs.FileInfo, error) {
		if p == target {
			return nil, &fs.PathError{Op: "lstat", Path: p, Err: syscall.EACCES}
		}
		return f.lstat("")(p)
	}
	if _, err := Observe(paths, f.uid, lstat); err != ErrSourceUnavailable {
		t.Fatalf("EACCES: %v", err)
	}
	foreign := func(p string) (fs.FileInfo, error) { return f.lstat("")(p) }
	must(t, os.WriteFile(filepath.Join(f.h, ".claude.json"), nil, 0o600))
	es, err := Observe(paths, f.uid, foreign)
	if err != nil {
		t.Fatal(err)
	}
	if CheckOwners(es, f.uid+1) != ErrSourceUnavailable {
		t.Fatal("user source owned by another uid accepted")
	}
}

// Structural source changes invalidate; mutable native-state replacement
// does not.
func TestSourcesChanged(t *testing.T) {
	f := newSrcFixture(t)
	before, err := f.observe(t, "")
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.Mkdir(filepath.Join(f.h, ".claude"), 0o700))
	must(t, os.WriteFile(filepath.Join(f.h, ".claude.json"), []byte("{}"), 0o600))
	after, err := f.observe(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if SourcesChanged(before, after) {
		t.Fatal("native-state creation reported as source change")
	}
	must(t, os.WriteFile(filepath.Join(f.w, "CLAUDE.md"), []byte("x"), 0o644))
	after2, err := f.observe(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if !SourcesChanged(after, after2) {
		t.Fatal("new project entry not reported")
	}
	must(t, os.Chmod(filepath.Join(f.w, "CLAUDE.md"), 0o600))
	after3, _ := f.observe(t, "")
	if !SourcesChanged(after2, after3) {
		t.Fatal("mode change not reported")
	}
	if !SourcesChanged(after3, after3[:len(after3)-1]) {
		t.Fatal("skipped row not reported")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
