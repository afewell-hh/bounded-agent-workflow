package worker

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

func tempDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func writeN(t *testing.T, path string, n int, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, bytes.Repeat([]byte("z"), n), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// The capture accepts exactly 4096 entries, a 1 MiB file and 16 MiB in
// total, and fails one past each bound without truncating.
func TestSnapshotBounds(t *testing.T) {
	d := tempDir(t)
	for i := 0; i < 4095; i++ {
		writeN(t, filepath.Join(d, "f"+strconv.Itoa(i)), 0, 0o644)
	}
	if err := os.Mkdir(filepath.Join(d, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s, err := takeSnapshot(d, "sha1"); err != nil || len(s) != 4096 {
		t.Fatalf("4096 entries (one a directory): %d %v", len(s), err)
	}
	writeN(t, filepath.Join(d, "dir", "one-more"), 0, 0o644)
	if _, err := takeSnapshot(d, "sha1"); err == nil {
		t.Fatal("4097 entries accepted")
	}

	d = tempDir(t)
	writeN(t, filepath.Join(d, "exact"), 1<<20, 0o644)
	if s, err := takeSnapshot(d, "sha1"); err != nil || s["exact"].size != 1<<20 {
		t.Fatalf("1 MiB file: %v", err)
	}
	writeN(t, filepath.Join(d, "over"), 1<<20+1, 0o644)
	if _, err := takeSnapshot(d, "sha1"); err == nil {
		t.Fatal("1 MiB + 1 file accepted")
	}

	d = tempDir(t)
	for i := 0; i < 16; i++ {
		writeN(t, filepath.Join(d, "m"+strconv.Itoa(i)), 1<<20, 0o644)
	}
	if _, err := takeSnapshot(d, "sha1"); err != nil {
		t.Fatalf("16 MiB total: %v", err)
	}
	writeN(t, filepath.Join(d, "byte"), 1, 0o644)
	if _, err := takeSnapshot(d, "sha1"); err == nil {
		t.Fatal("16 MiB + 1 total accepted")
	}
}

// Only the top-level .git entry is skipped; a nested .git name or a
// special file is unsupported; a link is recorded without being followed.
func TestSnapshotLayout(t *testing.T) {
	d := tempDir(t)
	if err := os.Mkdir(filepath.Join(d, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeN(t, filepath.Join(d, ".git", "large"), 2<<20, 0o644)
	if err := os.Symlink(filepath.Join(d, ".git", "large"), filepath.Join(d, "link")); err != nil {
		t.Fatal(err)
	}
	s, err := takeSnapshot(d, "sha1")
	if err != nil || len(s) != 1 || s["link"].kind != 'L' || s["link"].size != int64(len(filepath.Join(d, ".git", "large"))) {
		t.Fatalf("top .git not skipped or link followed: %v %v", s, err)
	}
	if err := os.MkdirAll(filepath.Join(d, "sub", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := takeSnapshot(d, "sha1"); err == nil {
		t.Fatal("nested .git accepted")
	}
}

// Hand-computed blob IDs: the empty blob and the blob "a" in SHA-1.
const (
	emptyBlobSHA1 = "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"
	aBlobSHA1     = "2e65efe2a145dda7ee51d1741299f848e5bf752e"
)

// The launch capture must equal the tree exactly, independently of Git.
func TestMatchesTree(t *testing.T) {
	setup := func(t *testing.T) string {
		d := tempDir(t)
		writeN(t, filepath.Join(d, "a"), 0, 0o644)
		writeN(t, filepath.Join(d, "x"), 0, 0o755)
		if err := os.Symlink("a", filepath.Join(d, "l")); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(filepath.Join(d, "emptydir"), 0o755); err != nil {
			t.Fatal(err)
		}
		return d
	}
	tree := func() map[string]treeEntry {
		return map[string]treeEntry{"a": {"100644", "blob", emptyBlobSHA1}, "x": {"100755", "blob", emptyBlobSHA1},
			"l": {"120000", "blob", aBlobSHA1}}
	}
	d := setup(t)
	s, err := takeSnapshot(d, "sha1")
	if err != nil {
		t.Fatal(err)
	}
	if err := matchesTree(s, tree()); err != nil {
		t.Fatalf("exact tree: %v", err)
	}
	cases := []struct {
		name string
		edit func(d string, tr map[string]treeEntry)
		want Code
	}{
		{"executable bit", func(d string, _ map[string]treeEntry) { os.Chmod(filepath.Join(d, "x"), 0o644) }, CodeNotClean},
		{"content", func(d string, _ map[string]treeEntry) { writeN(t, filepath.Join(d, "a"), 1, 0o644) }, CodeNotClean},
		{"missing tracked", func(d string, _ map[string]treeEntry) { os.Remove(filepath.Join(d, "a")) }, CodeNotClean},
		{"untracked file", func(d string, _ map[string]treeEntry) { writeN(t, filepath.Join(d, "new"), 0, 0o644) }, CodeNotClean},
		{"link target", func(d string, _ map[string]treeEntry) {
			os.Remove(filepath.Join(d, "l"))
			os.Symlink("x", filepath.Join(d, "l"))
		}, CodeNotClean},
		{"file where link expected", func(d string, _ map[string]treeEntry) {
			os.Remove(filepath.Join(d, "l"))
			writeN(t, filepath.Join(d, "l"), 1, 0o644)
		}, CodeNotClean},
		{"submodule entry", func(_ string, tr map[string]treeEntry) {
			tr["sub"] = treeEntry{"160000", "commit", aBlobSHA1}
		}, CodeUnsafeLayout},
	}
	for _, c := range cases {
		d := setup(t)
		tr := tree()
		c.edit(d, tr)
		s, err := takeSnapshot(d, "sha1")
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if err := matchesTree(s, tr); !IsCode(err, c.want) {
			t.Errorf("%s: %v, want %s", c.name, err, c.want)
		}
	}
}

// The control capture changes with persistent control edits, including a
// byte-identical replacement, and is bounded.
func TestControlFiles(t *testing.T) {
	root := tempDir(t)
	home, repo := filepath.Join(root, "home"), filepath.Join(root, "repo")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := testfixture.Init(home, repo, "sha1"); err != nil {
		t.Fatal(err)
	}
	gd := filepath.Join(repo, ".git")
	capture := func() []byte {
		t.Helper()
		b, err := controlFiles(repo, gd, gd)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	base := capture()
	if !bytes.Equal(base, capture()) {
		t.Fatal("unchanged control state differs")
	}
	cfg, err := os.ReadFile(filepath.Join(gd, "config"))
	if err != nil {
		t.Fatal(err)
	}
	steps := []struct {
		name string
		do   func()
	}{
		{"config replaced byte-identically", func() {
			writeN(t, filepath.Join(gd, "config.tmp"), 0, 0o644)
			os.WriteFile(filepath.Join(gd, "config.tmp"), cfg, 0o644)
			os.Rename(filepath.Join(gd, "config.tmp"), filepath.Join(gd, "config"))
		}},
		{"config appended", func() { os.WriteFile(filepath.Join(gd, "config"), append(cfg, "\n# x\n"...), 0o644) }},
		{"exclude added", func() {
			os.MkdirAll(filepath.Join(gd, "info"), 0o755)
			os.WriteFile(filepath.Join(gd, "info", "exclude"), []byte("hidden\n"), 0o644)
		}},
		{"alternates added", func() {
			os.MkdirAll(filepath.Join(gd, "objects", "info"), 0o755)
			os.WriteFile(filepath.Join(gd, "objects", "info", "alternates"), []byte("/elsewhere\n"), 0o644)
		}},
		{"HEAD rewritten", func() { os.WriteFile(filepath.Join(gd, "HEAD"), []byte("ref: refs/heads/other\n"), 0o644) }},
	}
	prev := base
	for _, s := range steps {
		s.do()
		now := capture()
		if bytes.Equal(prev, now) {
			t.Errorf("%s: not detected", s.name)
		}
		prev = now
	}
	hooks := filepath.Join(gd, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 513; i++ {
		writeN(t, filepath.Join(hooks, "h"+strconv.Itoa(i)), 0, 0o644)
	}
	if _, err := controlFiles(repo, gd, gd); err == nil {
		t.Fatal("over-bound control capture accepted")
	}
}
