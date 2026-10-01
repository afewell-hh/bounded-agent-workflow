package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// gitlinkBase commits sources plus gitlinks upd, del and keep (all pointing
// at commit c1) and a regular file tc. It returns the repository, c1 and a
// second commit c2 usable as a new gitlink target. When ignore is set, the
// committed .gitmodules and the repository config tell ordinary Git to
// ignore every submodule change.
func gitlinkBase(t *testing.T, home string, ignore bool) (dir, c1, c2 string) {
	t.Helper()
	dir = mustInit(t, home, t.TempDir(), "sha1")
	mustWrite(t, filepath.Join(dir, "tc"), "regular\n")
	c1, err := tf.CommitSources(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	git(t, home, dir, "commit", "-q", "--allow-empty", "-m", "c2")
	c2 = oid(t, home, dir, "HEAD")
	for _, p := range []string{"upd", "del", "keep"} {
		git(t, home, dir, "update-index", "--add", "--cacheinfo", "160000,"+c1+","+p)
	}
	if ignore {
		var gm strings.Builder
		for _, p := range []string{"upd", "del", "keep", "new"} {
			gm.WriteString("[submodule \"" + p + "\"]\n\tpath = " + p + "\n\turl = ./missing-" + p + "\n\tignore = all\n")
		}
		mustWrite(t, filepath.Join(dir, ".gitmodules"), gm.String())
		git(t, home, dir, "add", ".gitmodules")
		git(t, home, dir, "config", "diff.ignoreSubmodules", "all")
		git(t, home, dir, "config", "submodule.upd.ignore", "all")
	}
	git(t, home, dir, "commit", "-q", "-m", "gitlinks")
	return dir, c1, c2
}

// stagedOps applies the named staged gitlink operations with plumbing only.
func stagedOps(t *testing.T, home, dir, c1, c2 string, ops ...string) {
	t.Helper()
	for _, op := range ops {
		switch op {
		case "add":
			git(t, home, dir, "update-index", "--add", "--cacheinfo", "160000,"+c1+",new")
		case "update":
			git(t, home, dir, "update-index", "--cacheinfo", "160000,"+c2+",upd")
		case "delete":
			git(t, home, dir, "update-index", "--force-remove", "del")
		case "typechange": // regular file in HEAD becomes a gitlink in the index
			git(t, home, dir, "update-index", "--force-remove", "tc")
			git(t, home, dir, "update-index", "--add", "--cacheinfo", "160000,"+c1+",tc")
		case "file": // an ordinary staged file, counted by status
			mustWrite(t, filepath.Join(dir, "plain.txt"), "plain\n")
			git(t, home, dir, "add", "plain.txt")
		default:
			t.Fatalf("unknown op %s", op)
		}
	}
}

// Each expected count is fixed by the operations applied, never derived from
// inspector output. submodules counts stage-0 gitlinks after the operations.
func TestStagedGitlinks(t *testing.T) {
	cases := []struct {
		name       string
		ops        []string
		staged     int
		submodules int
	}{
		{"none", nil, 0, 3},
		{"add", []string{"add"}, 1, 4},
		{"update", []string{"update"}, 1, 3},
		{"delete", []string{"delete"}, 1, 2},
		{"typechange", []string{"typechange"}, 1, 4},
		{"all plus file", []string{"add", "update", "delete", "typechange", "file"}, 5, 4},
	}
	for _, ignore := range []bool{false, true} {
		for _, c := range cases {
			home := env(t)
			dir, c1, c2 := gitlinkBase(t, home, ignore)
			stagedOps(t, home, dir, c1, c2, c.ops...)
			if ignore && len(c.ops) > 0 && c.ops[0] == "update" {
				// Positive control: the ignore settings really hide the staged
				// update from ordinary Git, so this case is meaningful.
				if out := git(t, home, dir, "diff", "--cached", "--name-only"); out != "" {
					t.Fatalf("ignore settings ineffective in fixture: %q", out)
				}
			}
			got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
			want := inspect.Counts{Staged: c.staged, Submodules: c.submodules}
			if got.Repository.Counts != want {
				t.Fatalf("ignore=%v %s: counts %+v want %+v", ignore, c.name, got.Repository.Counts, want)
			}
		}
	}
}

// Unborn HEAD: every index gitlink is a staged addition.
func TestStagedGitlinkUnborn(t *testing.T) {
	home := env(t)
	other := mustInit(t, home, t.TempDir(), "sha1")
	c1, err := tf.CommitSources(home, other)
	if err != nil {
		t.Fatal(err)
	}
	dir := mustInit(t, home, t.TempDir(), "sha1")
	git(t, home, dir, "update-index", "--add", "--cacheinfo", "160000,"+c1+",sub")
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if got.Repository.Counts != (inspect.Counts{Staged: 1, Submodules: 1}) || got.Repository.HeadState != "unborn" {
		t.Fatalf("unborn gitlink %+v", got.Repository)
	}
}

// A conflicted gitlink is counted once as conflicted, not as staged.
func TestConflictedGitlink(t *testing.T) {
	home := env(t)
	dir, _, c2 := gitlinkBase(t, home, false)
	git(t, home, dir, "checkout", "-q", "-b", "other")
	git(t, home, dir, "update-index", "--cacheinfo", "160000,"+c2+",upd")
	git(t, home, dir, "commit", "-q", "-m", "other")
	git(t, home, dir, "checkout", "-q", "main")
	git(t, home, dir, "update-index", "--cacheinfo", "160000,"+oid(t, home, dir, "HEAD")+",upd")
	git(t, home, dir, "commit", "-q", "-m", "main")
	if _, err := tf.Git(home, dir, "merge", "-q", "other"); err == nil {
		t.Fatal("expected gitlink merge conflict")
	}
	stages := git(t, home, dir, "ls-files", "--stage", "--", "upd")
	if strings.Count(stages, "\n")+1 != 3 {
		t.Fatalf("fixture did not produce three stages: %q", stages)
	}
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if got.Repository.Counts != (inspect.Counts{Conflicted: 1, Submodules: 2}) {
		t.Fatalf("conflicted gitlink %+v", got.Repository.Counts)
	}
}

// An initialized nested submodule whose own repository configures
// marker-writing filter and fsmonitor helpers, with dirty content: inspection
// must count the staged gitlink update yet never enter the submodule.
func TestNestedSubmoduleHelpersNotRun(t *testing.T) {
	home := env(t)
	markers := t.TempDir()
	bin := t.TempDir()
	for _, name := range []string{"clean", "smudge", "fsmonitor", "textconv"} {
		script(t, filepath.Join(bin, name), "touch '"+filepath.Join(markers, name)+"'\ncat")
	}
	src := mustInit(t, home, filepath.Join(t.TempDir(), "src"), "sha1")
	mustWrite(t, filepath.Join(src, "inner.txt"), "one\n")
	git(t, home, src, "add", "inner.txt")
	git(t, home, src, "commit", "-q", "-m", "inner")

	dir := mustInit(t, home, filepath.Join(t.TempDir(), "super"), "sha1")
	tf.CommitSources(home, dir)
	git(t, home, dir, "submodule", "add", "-q", src, "sub")
	git(t, home, dir, "commit", "-q", "-m", "add submodule")
	sub := filepath.Join(dir, "sub")
	// Staged gitlink update: a new submodule commit recorded in the index.
	mustWrite(t, filepath.Join(sub, "inner.txt"), "two\n")
	git(t, home, sub, "commit", "-q", "-am", "inner two")
	git(t, home, dir, "add", "sub")
	// Now arm the submodule's own helpers and dirty its worktree with a
	// same-size change so a status inside it must run the clean filter.
	for _, kv := range [][2]string{
		{"filter.mark.clean", filepath.Join(bin, "clean")},
		{"filter.mark.smudge", filepath.Join(bin, "smudge")},
		{"core.fsmonitor", filepath.Join(bin, "fsmonitor")},
		{"diff.mark.textconv", filepath.Join(bin, "textconv")},
	} {
		git(t, home, sub, "config", kv[0], kv[1])
	}
	infoDir := strings.TrimSpace(git(t, home, sub, "rev-parse", "--git-path", "info"))
	if !filepath.IsAbs(infoDir) {
		infoDir = filepath.Join(sub, infoDir)
	}
	mustWrite(t, filepath.Join(infoDir, "attributes"), "* filter=mark diff=mark\n")
	mustWrite(t, filepath.Join(sub, "inner.txt"), "six\n")

	// Positive control: ordinary recursive status does run the submodule's
	// helpers, proving the markers would detect any entry into the submodule.
	git(t, home, dir, "status", "--porcelain=v2", "--ignore-submodules=none")
	if ents, _ := os.ReadDir(markers); len(ents) == 0 {
		t.Fatal("positive control: submodule helpers did not run under recursive status")
	}
	for _, e := range mustReadDir(t, markers) {
		os.Remove(filepath.Join(markers, e.Name()))
	}

	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if got.Repository.Counts != (inspect.Counts{Staged: 1, Submodules: 1}) {
		t.Fatalf("nested submodule counts %+v", got.Repository.Counts)
	}
	assertNoMarkers(t, markers)
}

func mustReadDir(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ents
}
