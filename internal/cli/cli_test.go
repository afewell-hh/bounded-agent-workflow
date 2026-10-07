package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// env prepares an isolated HOME whose user-level Git ignore and config would
// change results if the inspector read them, and poisons inherited Git/gh
// variables with decoys. Every git run (fixture setup and inspector) goes
// through a wrapper that prints the reviewer host's stderr warning.
func env(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	if err := tf.InstallWarningGit(bin); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	home := t.TempDir()
	mustWrite(t, filepath.Join(home, ".config/git/ignore"), "*.txt\n*\n")
	mustWrite(t, filepath.Join(home, ".gitconfig"), "[core]\n\texcludesFile = "+filepath.Join(home, ".config/git/ignore")+"\n[status]\n\tshowUntrackedFiles = no\n")
	decoy := t.TempDir()
	if _, err := tf.CommitSources(home, mustInit(t, home, decoy, "sha1")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("GIT_DIR", filepath.Join(decoy, ".git"))
	t.Setenv("GIT_WORK_TREE", decoy)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(decoy, ".git/index"))
	t.Setenv("GIT_PAGER", "/nonexistent-decoy-pager")
	t.Setenv("GIT_EXTERNAL_DIFF", "/nonexistent-decoy-diff")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'status.showuntrackedfiles'='no'")
	t.Setenv("GH_TOKEN", "decoy-gh-token-value")
	t.Setenv("GITHUB_TOKEN", "decoy-github-token-value")
	t.Setenv("GH_HOST", "decoy.example.invalid")
	t.Setenv("GH_CONFIG_DIR", "/nonexistent-decoy-config")
	return home
}

func mustInit(t *testing.T, home, dir, format string) string {
	t.Helper()
	if err := tf.Init(home, dir, format); err != nil {
		t.Fatal(err)
	}
	return dir
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := tf.Write(path, content); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, home, dir string, args ...string) string {
	t.Helper()
	out, err := tf.Git(home, dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// oid resolves a fixture revision to a validated full object ID.
func oid(t *testing.T, home, dir, rev string) string {
	t.Helper()
	out, err := tf.OID(home, dir, rev)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func script(t *testing.T, path, body string) {
	t.Helper()
	mustWrite(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

type result struct {
	code           int
	stdout, stderr string
}

func runCLI(t *testing.T, limits *inspect.Limits, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	var code int
	if limits != nil {
		code = RunWithLimits(args, &out, &errb, *limits)
	} else {
		code = Run(args, &out, &errb)
	}
	return result{code, out.String(), errb.String()}
}

func wantFail(t *testing.T, r result, code int, msg string) {
	t.Helper()
	if r.code != code || r.stdout != "" || r.stderr != "baw: "+msg+"\n" {
		t.Fatalf("want exit %d %q with empty stdout, got exit %d stdout %q stderr %q", code, msg, r.code, r.stdout, r.stderr)
	}
}

func decode(t *testing.T, r result) inspect.Packet {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d stderr %q", r.code, r.stderr)
	}
	if !strings.HasSuffix(r.stdout, "}\n") || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("not one JSON object plus newline: %q", r.stdout)
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	dec.DisallowUnknownFields()
	var p inspect.Packet
	if err := dec.Decode(&p); err != nil {
		t.Fatal(err)
	}
	return p
}

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }

// expectedSources builds the oracle for a fixture where every source is
// committed and clean.
func cleanSources(head string) []inspect.Source {
	var out []inspect.Source
	for _, s := range tf.Sources {
		out = append(out, inspect.Source{Path: s, Presence: "present", WorktreeState: "clean",
			HeadRef: sp("git:" + head + ":" + s), WorktreeRef: sp("worktree:" + s)})
	}
	return out
}

func TestCleanFixtureExactJSON(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	head, err := tf.CommitSources(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	r := runCLI(t, nil, "inspect", "--repo", dir, "--json")
	got := decode(t, r)
	want := inspect.Packet{
		SchemaVersion: 1, Status: "complete",
		Repository: inspect.Repository{State: "observed", ObjectFormat: "sha1", HeadState: "present",
			Head: sp(head), BranchState: "attached", Checkpoint: inspect.Checkpoint{State: "not_requested"}},
		Sources:              cleanSources(head),
		Coordination:         inspect.Coordination{Mode: "none", State: "absent"},
		RemoteFreshness:      "unknown",
		RuntimeState:         "unknown",
		ProcessOwnership:     "unknown",
		ReservationOwnership: "unknown",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("packet mismatch\n got %+v\nwant %+v", got, want)
	}
	// Exact key set and null retention.
	var raw map[string]any
	json.Unmarshal([]byte(r.stdout), &raw)
	keys := []string{}
	for k := range raw {
		keys = append(keys, k)
	}
	if len(keys) != 9 || raw["coordination"].(map[string]any)["number"] != nil {
		t.Fatalf("unexpected top-level keys %v", keys)
	}
	if _, ok := raw["coordination"].(map[string]any)["source_url"]; !ok {
		t.Fatal("null field omitted")
	}
	// Stable output for identical state.
	if r2 := runCLI(t, nil, "inspect", "--repo", dir, "--json"); r2.stdout != r.stdout {
		t.Fatal("output not stable")
	}
}

func TestOperatorFixtureTerminalExact(t *testing.T) {
	home := env(t)
	dir := t.TempDir()
	head, err := tf.Operator(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	r := runCLI(t, nil, "inspect", "--repo", dir)
	var want strings.Builder
	want.WriteString("BAW inspection\nHEAD: " + head + "\nBranch state: attached\nObject format: sha1\n")
	want.WriteString("Changes: staged=1 unstaged=1 untracked=1 conflicted=0\nLinked worktrees: 0\nSubmodules: 0\nCheckpoint: not_requested\n")
	for _, s := range tf.Sources {
		want.WriteString("Source: " + s + " presence=present worktree_state=clean head_ref=git:" + head + ":" + s + " worktree_ref=worktree:" + s + "\n")
	}
	want.WriteString("Coordination: none\nRemote freshness: unknown\nRuntime state: unknown\nProcess ownership: unknown\nReservation ownership: unknown\n")
	if r.code != 0 || r.stderr != "" || r.stdout != want.String() {
		t.Fatalf("exit %d stderr %q\n got:\n%s\nwant:\n%s", r.code, r.stderr, r.stdout, want.String())
	}
}

func snapshotState(t *testing.T, home, dir string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString(oid(t, home, dir, "HEAD"))
	for _, f := range []string{".git/index", ".git/HEAD", "a.txt", "new-source.go"} {
		data, _ := os.ReadFile(filepath.Join(dir, f))
		b.Write(data)
	}
	return b.String()
}

func TestRecoveryFixture(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, filepath.Join(t.TempDir(), "main"), "sha1")
	mustWrite(t, filepath.Join(dir, "a.txt"), "one\n")
	checkpoint, err := tf.CommitSources(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "b.txt"), "two\n")
	git(t, home, dir, "add", "b.txt")
	git(t, home, dir, "commit", "-q", "-m", "second")
	// Gitlink entry (submodule) committed, third commit.
	git(t, home, dir, "update-index", "--add", "--cacheinfo", "160000,"+checkpoint+",vendor/sub")
	git(t, home, dir, "commit", "-q", "-m", "third")
	head := oid(t, home, dir, "HEAD")
	git(t, home, dir, "worktree", "add", "-q", filepath.Join(filepath.Dir(dir), "linked"))
	mustWrite(t, filepath.Join(dir, "a.txt"), "staged version\n")
	git(t, home, dir, "add", "a.txt")
	mustWrite(t, filepath.Join(dir, "a.txt"), "unstaged version\n")
	mustWrite(t, filepath.Join(dir, "new-source.go"), "package dummy\n")

	before := snapshotState(t, home, dir)
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", strings.ToUpper(checkpoint), "--json"))
	if after := snapshotState(t, home, dir); after != before {
		t.Fatal("inspection mutated fixture")
	}
	wantCounts := inspect.Counts{Staged: 1, Unstaged: 1, Untracked: 1, Conflicted: 0, LinkedWorktrees: 1, Submodules: 1}
	if got.Repository.Counts != wantCounts || *got.Repository.Head != head {
		t.Fatalf("counts %+v head %v", got.Repository.Counts, *got.Repository.Head)
	}
	cp := got.Repository.Checkpoint
	if cp.State != "ancestor" || *cp.OID != checkpoint || *cp.CommitsAhead != 2 {
		t.Fatalf("checkpoint %+v", cp)
	}
	// Equal checkpoint.
	got = decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", head, "--json"))
	if cp := got.Repository.Checkpoint; cp.State != "equal" || *cp.CommitsAhead != 0 {
		t.Fatalf("equal checkpoint %+v", cp)
	}
	// From the linked worktree, the main worktree is the one linked worktree.
	got = decode(t, runCLI(t, nil, "inspect", "--repo", filepath.Join(filepath.Dir(dir), "linked"), "--json"))
	if got.Repository.Counts.LinkedWorktrees != 1 || got.Repository.BranchState != "attached" {
		t.Fatalf("linked view %+v", got.Repository)
	}
}

func TestConflictCountedSeparately(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	mustWrite(t, filepath.Join(dir, "c.txt"), "base\n")
	tf.CommitSources(home, dir)
	git(t, home, dir, "checkout", "-q", "-b", "other")
	mustWrite(t, filepath.Join(dir, "c.txt"), "other\n")
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "other agents\n")
	git(t, home, dir, "commit", "-q", "-am", "other")
	git(t, home, dir, "checkout", "-q", "main")
	mustWrite(t, filepath.Join(dir, "c.txt"), "main\n")
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), "main agents\n")
	git(t, home, dir, "commit", "-q", "-am", "main")
	cmdErr := func() error { _, err := tf.Git(home, dir, "merge", "-q", "other"); return err }()
	if cmdErr == nil {
		t.Fatal("expected merge conflict")
	}
	mustWrite(t, filepath.Join(dir, "d.txt"), "staged new\n")
	git(t, home, dir, "add", "d.txt")
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	want := inspect.Counts{Staged: 1, Unstaged: 0, Untracked: 0, Conflicted: 2}
	if got.Repository.Counts != want {
		t.Fatalf("counts %+v", got.Repository.Counts)
	}
	if got.Sources[0].WorktreeState != "modified" {
		t.Fatalf("conflicted source %+v", got.Sources[0])
	}
}

func TestUnbornDetachedAndSourceStates(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	mustWrite(t, filepath.Join(dir, "README.md"), "untracked readme\n")
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	r := got.Repository
	if r.HeadState != "unborn" || r.Head != nil || r.BranchState != "unborn" || r.Counts.Untracked != 1 {
		t.Fatalf("unborn %+v", r)
	}
	if s := got.Sources[3]; s.Presence != "present" || s.WorktreeState != "untracked" || s.HeadRef != nil || *s.WorktreeRef != "worktree:README.md" {
		t.Fatalf("untracked source %+v", s)
	}
	if s := got.Sources[0]; s.Presence != "absent" || s.WorktreeState != "absent" || s.HeadRef != nil || s.WorktreeRef != nil {
		t.Fatalf("absent source %+v", s)
	}
	text := runCLI(t, nil, "inspect", "--repo", dir)
	if !strings.Contains(text.stdout, "HEAD: unborn\nBranch state: unborn\n") || !strings.Contains(text.stdout, "Source: AGENTS.md presence=absent worktree_state=absent head_ref=- worktree_ref=-\n") {
		t.Fatalf("unborn text %q", text.stdout)
	}
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", strings.Repeat("a", 40)), 1, "checkpoint_unborn")

	head, _ := tf.CommitSources(home, dir)
	os.Remove(filepath.Join(dir, "AGENTS.md"))
	mustWrite(t, filepath.Join(dir, "workflow/protocol.md"), "changed\n")
	git(t, home, dir, "checkout", "-q", "--detach")
	got = decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if got.Repository.BranchState != "detached" || *got.Repository.Head != head {
		t.Fatalf("detached %+v", got.Repository)
	}
	if s := got.Sources[0]; s.Presence != "absent" || s.WorktreeState != "deleted" || *s.HeadRef != "git:"+head+":AGENTS.md" || s.WorktreeRef != nil {
		t.Fatalf("deleted source %+v", s)
	}
	if s := got.Sources[1]; s.WorktreeState != "modified" || s.Presence != "present" {
		t.Fatalf("modified source %+v", s)
	}
}

func TestSourceSymlinksRejected(t *testing.T) {
	home := env(t)
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "AGENTS.md"), tf.SecretContent)
	mustWrite(t, filepath.Join(outside, "design/product-contract.md"), tf.SecretContent)

	dir := mustInit(t, home, t.TempDir(), "sha1")
	if err := os.Symlink(filepath.Join(outside, "AGENTS.md"), filepath.Join(dir, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir), 1, "source_symlink")

	dir2 := mustInit(t, home, t.TempDir(), "sha1")
	os.MkdirAll(filepath.Join(dir2, "docs"), 0o755)
	os.Symlink(filepath.Join(outside, "design"), filepath.Join(dir2, "docs/design"))
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir2), 1, "source_symlink")
}

func TestRepoPathForms(t *testing.T) {
	home := env(t)
	base := t.TempDir()
	dir := mustInit(t, home, filepath.Join(base, "repo"), "sha1")
	head, _ := tf.CommitSources(home, dir)
	os.Symlink(dir, filepath.Join(base, "link"))
	for _, p := range []string{filepath.Join(dir, "docs/design"), filepath.Join(base, "link"), filepath.Join(base, "link", "workflow")} {
		got := decode(t, runCLI(t, nil, "inspect", "--repo", p, "--json"))
		if *got.Repository.Head != head || got.Sources[0].WorktreeState != "clean" {
			t.Fatalf("path form %s: %+v", p, got.Repository)
		}
	}
	wantFail(t, runCLI(t, nil, "inspect", "--repo", filepath.Join(base, "missing")), 1, "repository_unavailable")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", filepath.Join(dir, "README.md")), 1, "repository_unavailable")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", t.TempDir()), 1, "repository_unavailable")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", filepath.Join(dir, ".git")), 1, "repository_unavailable")
	bare := filepath.Join(base, "bare.git")
	git(t, home, base, "init", "-q", "--bare", bare)
	wantFail(t, runCLI(t, nil, "inspect", "--repo", bare), 1, "repository_unavailable")
	// Invalid repository configuration fails closed.
	bad := mustInit(t, home, filepath.Join(base, "bad"), "sha1")
	mustWrite(t, filepath.Join(bad, ".git/config"), "[core\n\tbroken = = =\n")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", bad), 1, "git_config_invalid")
}

func TestCheckpointValidation(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	base, _ := tf.CommitSources(home, dir)
	git(t, home, dir, "checkout", "-q", "-b", "side")
	mustWrite(t, filepath.Join(dir, "s.txt"), "side\n")
	git(t, home, dir, "add", "s.txt")
	git(t, home, dir, "commit", "-q", "-m", "side")
	side := oid(t, home, dir, "HEAD")
	git(t, home, dir, "checkout", "-q", "main")
	mustWrite(t, filepath.Join(dir, "m.txt"), "main\n")
	git(t, home, dir, "add", "m.txt")
	git(t, home, dir, "commit", "-q", "-m", "main")
	blob := oid(t, home, dir, "HEAD:m.txt")

	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", side), 1, "checkpoint_diverged")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", strings.Repeat("e", 40)), 1, "checkpoint_missing")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", blob), 1, "checkpoint_missing")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", strings.Repeat("e", 64)), 2, "invalid_usage")
	for _, bad := range []string{base[:12], "HEAD", "HEAD~1", base + "^", "-" + base[1:], strings.Repeat("g", 40)} {
		wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", bad), 2, "invalid_usage")
	}
}

func TestSHA256Repository(t *testing.T) {
	home := env(t)
	dir := filepath.Join(t.TempDir(), "r")
	if err := tf.Init(home, dir, "sha256"); err != nil {
		t.Skipf("SHA-256 repositories unavailable in this Git: %v", err)
	}
	cp, err := tf.CommitSources(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "x.txt"), "x\n")
	git(t, home, dir, "add", "x.txt")
	git(t, home, dir, "commit", "-q", "-m", "x")
	head := oid(t, home, dir, "HEAD")
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", cp, "--json"))
	if got.Repository.ObjectFormat != "sha256" || len(*got.Repository.Head) != 64 || *got.Repository.Head != head ||
		got.Repository.Checkpoint.State != "ancestor" || *got.Repository.Checkpoint.CommitsAhead != 1 {
		t.Fatalf("sha256 %+v", got.Repository)
	}
	if *got.Sources[0].HeadRef != "git:"+head+":AGENTS.md" {
		t.Fatalf("sha256 head ref %v", *got.Sources[0].HeadRef)
	}
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", cp[:40]), 2, "invalid_usage")
}

// helperFixture configures marker-writing programs for every Git helper that
// must not run. Returns repo dir and marker dir.
func helperFixture(t *testing.T, home string, withFilters bool) (string, string) {
	t.Helper()
	markers := t.TempDir()
	bin := t.TempDir()
	for _, name := range []string{"fsmonitor", "clean", "process", "pager", "extdiff", "textconv", "gpg", "hook"} {
		script(t, filepath.Join(bin, name), "touch '"+filepath.Join(markers, name)+"'\ncat")
	}
	dir := mustInit(t, home, t.TempDir(), "sha1")
	mustWrite(t, filepath.Join(dir, "f.txt"), "one\n")
	tf.CommitSources(home, dir)
	hooks := filepath.Join(dir, "hooks-dir")
	for _, h := range []string{"post-index-change", "reference-transaction", "post-checkout", "pre-commit"} {
		script(t, filepath.Join(hooks, h), "touch '"+filepath.Join(markers, "hook-"+h)+"'")
		script(t, filepath.Join(dir, ".git/hooks", h), "touch '"+filepath.Join(markers, "githook-"+h)+"'")
	}
	cfg := [][2]string{
		{"core.fsmonitor", filepath.Join(bin, "fsmonitor")},
		{"core.pager", filepath.Join(bin, "pager")},
		{"core.hooksPath", hooks},
		{"diff.external", filepath.Join(bin, "extdiff")},
		{"diff.dummy.textconv", filepath.Join(bin, "textconv")},
		{"log.showSignature", "true"},
		{"gpg.program", filepath.Join(bin, "gpg")},
		{"core.untrackedCache", "true"},
		{"user.secret", tf.SecretConfig},
	}
	if withFilters {
		cfg = append(cfg, [2]string{"filter.dummy.clean", filepath.Join(bin, "clean")}, [2]string{"filter.dummy.process", filepath.Join(bin, "process")})
	}
	for _, kv := range cfg {
		git(t, home, dir, "config", kv[0], kv[1])
	}
	mustWrite(t, filepath.Join(dir, ".gitattributes"), "* filter=dummy diff=dummy\n")
	mustWrite(t, filepath.Join(dir, "f.txt"), "two\n")
	return dir, markers
}

func assertNoMarkers(t *testing.T, markers string) {
	t.Helper()
	ents, _ := os.ReadDir(markers)
	if len(ents) != 0 {
		t.Fatalf("helper executed: %v", ents[0].Name())
	}
}

func TestFiltersBlockBeforeStatus(t *testing.T) {
	home := env(t)
	dir, markers := helperFixture(t, home, true)
	index, _ := os.ReadFile(filepath.Join(dir, ".git/index"))
	head := oid(t, home, dir, "HEAD")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir), 1, "unsupported_filters")
	assertNoMarkers(t, markers)
	index2, _ := os.ReadFile(filepath.Join(dir, ".git/index"))
	if !bytes.Equal(index, index2) || oid(t, home, dir, "HEAD") != head {
		t.Fatal("fixture mutated")
	}
	// A filter configured only through a repository include is also detected.
	dir2 := mustInit(t, home, t.TempDir(), "sha1")
	inc := filepath.Join(t.TempDir(), "inc")
	mustWrite(t, inc, "[filter \"x\"]\n\tprocess = /nonexistent\n")
	git(t, home, dir2, "config", "include.path", inc)
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir2), 1, "unsupported_filters")
}

func TestHelpersNotLaunchedWithoutFilters(t *testing.T) {
	home := env(t)
	dir, markers := helperFixture(t, home, false)
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	// .gitattributes is untracked; f.txt is unstaged.
	if got.Repository.Counts != (inspect.Counts{Unstaged: 1, Untracked: 1 + 4}) {
		// hooks-dir holds four untracked hook scripts.
		t.Fatalf("counts %+v", got.Repository.Counts)
	}
	assertNoMarkers(t, markers)
}

func TestPartialCloneBlocked(t *testing.T) {
	home := env(t)
	for _, kv := range [][2]string{{"extensions.partialClone", "origin"}, {"remote.origin.promisor", "true"}} {
		dir := mustInit(t, home, t.TempDir(), "sha1")
		tf.CommitSources(home, dir)
		git(t, home, dir, "config", kv[0], kv[1])
		wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--checkpoint", strings.Repeat("e", 40)), 1, "unsupported_partial_clone")
	}
}

func TestSecretsWithheld(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored-*\n")
	mustWrite(t, filepath.Join(dir, tf.SecretFile+".txt"), tf.SecretContent+"\n")
	tf.CommitSources(home, dir)
	git(t, home, dir, "commit", "-q", "--allow-empty", "-m", tf.SecretMessage)
	git(t, home, dir, "checkout", "-q", "-b", tf.SecretBranch)
	git(t, home, dir, "remote", "add", "origin", "https://"+tf.SecretRemote+"@example.invalid/x.git")
	git(t, home, dir, "config", "user.note", tf.SecretConfig)
	mustWrite(t, filepath.Join(dir, tf.SecretFile+".txt"), tf.SecretContent+" changed\n")
	mustWrite(t, filepath.Join(dir, "untracked-"+tf.SecretFile), tf.SecretContent)
	mustWrite(t, filepath.Join(dir, "ignored-x.env"), "TOKEN="+tf.SecretIgnored)
	mustWrite(t, filepath.Join(dir, "README.md"), tf.SecretContent)
	snap := filepath.Join(t.TempDir(), "issue.json")
	mustWrite(t, snap, `{"number":7,"state":"open","updated_at":"2026-01-02T03:04:05Z","html_url":"https://github.com/acme/widgets/issues/7","title":"`+tf.SecretProse+`","body":"`+tf.SecretProse+`"}`)
	for _, args := range [][]string{
		{"inspect", "--repo", dir},
		{"inspect", "--repo", dir, "--json", "--coordination-file", snap},
		{"inspect", "--repo", dir, "--checkpoint", strings.Repeat("e", 40)},
	} {
		r := runCLI(t, nil, args...)
		all := r.stdout + r.stderr
		for _, s := range []string{tf.SecretBranch, tf.SecretFile, tf.SecretContent, tf.SecretMessage, tf.SecretRemote, tf.SecretConfig, tf.SecretIgnored, tf.SecretProse, dir, "\x1b"} {
			if strings.Contains(all, s) {
				t.Fatalf("output leaked %q", s)
			}
		}
	}
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if got.Repository.BranchState != "attached" || got.Repository.Counts.Unstaged != 2 || got.Repository.Counts.Untracked != 1 {
		t.Fatalf("secret fixture %+v", got.Repository.Counts)
	}
}

func TestGlobalIgnoreIndependence(t *testing.T) {
	home := env(t) // HOME ignores "*" and disables untracked display
	dir := mustInit(t, home, t.TempDir(), "sha1")
	tf.CommitSources(home, dir)
	mustWrite(t, filepath.Join(dir, "one.txt"), "1")
	mustWrite(t, filepath.Join(dir, "sub/two.txt"), "2")
	mustWrite(t, filepath.Join(dir, ".git/info/exclude"), "excluded.txt\n")
	mustWrite(t, filepath.Join(dir, "excluded.txt"), "3")
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if got.Repository.Counts.Untracked != 2 {
		t.Fatalf("untracked %d", got.Repository.Counts.Untracked)
	}
}

func writeSnap(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "snap.json")
	mustWrite(t, p, content)
	return p
}

func TestCoordinationSnapshot(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	tf.CommitSources(home, dir)
	ghDir, calls := fakeGH(t, `echo should-not-run`)
	t.Setenv("PATH", ghDir+":"+os.Getenv("PATH"))

	ok := `{"number":4,"state":"closed","updated_at":"2026-09-30T21:25:00.5+02:00","html_url":"https://github.com/afewell-hh/bounded-agent-workflow/issues/4","body":"x"}`
	got := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json", "--coordination-file", writeSnap(t, ok)))
	want := inspect.Coordination{Mode: "snapshot", State: "observed", Number: ip(4), IssueState: sp("CLOSED"),
		UpdatedAt: sp("2026-09-30T19:25:00Z"), SourceURL: sp("https://github.com/afewell-hh/bounded-agent-workflow/issues/4")}
	if !reflect.DeepEqual(got.Coordination, want) {
		t.Fatalf("coordination %+v", got.Coordination)
	}
	text := runCLI(t, nil, "inspect", "--repo", dir, "--coordination-file", writeSnap(t, ok))
	if !strings.Contains(text.stdout, "Coordination: snapshot number=4 state=CLOSED updated_at=2026-09-30T19:25:00Z source_url=https://github.com/afewell-hh/bounded-agent-workflow/issues/4\n") {
		t.Fatalf("text %q", text.stdout)
	}
	// Exactly-at-limit input is accepted; one byte more is rejected.
	padded := ok + strings.Repeat(" ", (1<<20)-len(ok))
	decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json", "--coordination-file", writeSnap(t, padded)))
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--coordination-file", writeSnap(t, padded+" ")), 1, "input_limit")

	for _, bad := range []string{
		`{"number":4`,
		ok + ok,
		`[]`,
		`{"number":"4","state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/issues/4"}`,
		`{"number":5,"state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/issues/4"}`,
		`{"number":4,"state":"OPEN","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/issues/4"}`,
		`{"number":4,"state":"open\u001b[31m","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/issues/4"}`,
		`{"number":4,"state":"open","updated_at":"yesterday","html_url":"https://github.com/a/b/issues/4"}`,
		`{"number":4,"state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"http://github.com/a/b/issues/4"}`,
		`{"number":4,"state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/pull/4"}`,
		`{"number":4,"state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/issues/4","pull_request":{}}`,
		`{"number":2147483648,"state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/a/b/issues/2147483648"}`,
		`{"number":4,"state":"open","updated_at":"2026-01-01T00:00:00Z","html_url":"https://github.com/-a/b/issues/4"}`,
	} {
		wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--json", "--coordination-file", writeSnap(t, bad)), 1, "coordination_invalid")
	}
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--coordination-file", filepath.Join(dir, "missing.json")), 1, "coordination_unavailable")
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir, "--coordination-file", dir), 1, "coordination_unavailable")
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("gh was invoked in snapshot/local mode")
	}
	decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("gh was invoked in local mode")
	}
}

// fakeGH installs a fake gh that appends its argv and environment to a calls
// file before running body.
func fakeGH(t *testing.T, body string) (string, string) {
	t.Helper()
	d := t.TempDir()
	calls := filepath.Join(t.TempDir(), "calls")
	script(t, filepath.Join(d, "gh"), `printf 'ARGV' >> '`+calls+`'; for a in "$@"; do printf ' %s' "$a" >> '`+calls+`'; done; printf '\n' >> '`+calls+`'; env | sort >> '`+calls+`'; printf 'END\n' >> '`+calls+`'
`+body)
	return d, calls
}

func TestFakeGitHub(t *testing.T) {
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	tf.CommitSources(home, dir)
	issue := `{"number":1,"state":"open","updated_at":"2026-09-30T00:00:00Z","html_url":"https://github.com/afewell-hh/bounded-agent-workflow/issues/1","title":"` + tf.SecretProse + `"}`
	limits := inspect.DefaultLimits
	limits.GHTimeout = 500 * time.Millisecond

	run := func(body string) (result, string) {
		d, calls := fakeGH(t, body)
		t.Setenv("PATH", d+":"+os.Getenv("PATH"))
		r := runCLI(t, &limits, "inspect", "--repo", dir, "--json", "--github", "afewell-hh/bounded-agent-workflow#1")
		data, _ := os.ReadFile(calls)
		return r, string(data)
	}

	r, calls := run(`printf '%s' '` + issue + `'`)
	got := decode(t, r)
	if got.Coordination.Mode != "live" || *got.Coordination.IssueState != "OPEN" || *got.Coordination.Number != 1 {
		t.Fatalf("live %+v", got.Coordination)
	}
	if strings.Count(calls, "ARGV") != 1 || !strings.Contains(calls, "ARGV api --method GET --hostname github.com repos/afewell-hh/bounded-agent-workflow/issues/1\n") {
		t.Fatalf("argv/calls %q", calls)
	}
	for _, want := range []string{"GH_NO_UPDATE_NOTIFIER=1", "GH_NO_EXTENSION_UPDATE_NOTIFIER=1", "GH_PROMPT_DISABLED=1", "GH_PAGER=cat", "NO_COLOR=1", "HOME=" + home} {
		if !strings.Contains(calls, "\n"+want+"\n") {
			t.Fatalf("missing env %s", want)
		}
	}
	for _, banned := range []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "GH_CONFIG_DIR", "GIT_DIR", "decoy"} {
		if strings.Contains(calls, banned) {
			t.Fatalf("inherited %s", banned)
		}
	}

	// Failure: provider stderr/stdout never published, no retry.
	r, calls = run(`echo "` + tf.SecretProse + `" >&2; echo "` + tf.SecretProse + `"; exit 1`)
	wantFail(t, r, 1, "coordination_unavailable")
	if strings.Count(calls, "ARGV") != 1 {
		t.Fatal("retried")
	}
	// Mismatched issue.
	r, _ = run(`printf '%s' '` + strings.Replace(issue, "issues/1", "issues/2", 1) + `'`)
	wantFail(t, r, 1, "coordination_invalid")
	// Control-bearing provider output.
	r, _ = run(`printf '{"number":1,"state":"open\033[2J","updated_at":"2026-09-30T00:00:00Z","html_url":"https://github.com/afewell-hh/bounded-agent-workflow/issues/1"}'`)
	wantFail(t, r, 1, "coordination_invalid")
	// Timeout: the fake and its background descendant are terminated and joined.
	pidFile := filepath.Join(t.TempDir(), "pid")
	start := time.Now()
	r, calls = run(`sleep 30 & echo $! > '` + pidFile + `'; wait`)
	wantFail(t, r, 1, "command_timeout")
	if time.Since(start) > 5*time.Second || strings.Count(calls, "ARGV") != 1 {
		t.Fatal("timeout not bounded or retried")
	}
	assertProcessGone(t, pidFile)
	limits.GHTimeout = 10 * time.Second
	// Child stderr at exactly 64 KiB is accepted (and never published); one
	// byte more is command_output_limit even with valid stdout and exit 0.
	r, _ = run(`head -c 65536 /dev/zero | tr '\0' x >&2; printf '%s' '` + issue + `'`)
	if got := decode(t, r); *got.Coordination.Number != 1 || strings.Contains(r.stderr, "x") {
		t.Fatalf("stderr at cap: %+v", got.Coordination)
	}
	r, _ = run(`head -c 65537 /dev/zero | tr '\0' x >&2; printf '%s' '` + issue + `'`)
	wantFail(t, r, 1, "command_output_limit")

	// Normal exit leaving a detached TERM-ignoring descendant: the packet is
	// only produced after that owned descendant is gone.
	pidFile = filepath.Join(t.TempDir(), "pid")
	r, _ = run(detachedDescendant(pidFile) + `printf '%s' '` + issue + `'`)
	if got := decode(t, r); *got.Coordination.Number != 1 {
		t.Fatalf("normal exit %+v", got.Coordination)
	}
	assertProcessGone(t, pidFile)

	// Stdout over 1 MiB while the leader keeps running and a detached
	// TERM-ignoring descendant exists (independent reviewer's probe shape).
	pidFile = filepath.Join(t.TempDir(), "pid")
	start = time.Now()
	r, _ = run(detachedDescendant(pidFile) + `head -c 1048577 /dev/zero; sleep 30`)
	wantFail(t, r, 1, "command_output_limit")
	if time.Since(start) > 5*time.Second {
		t.Fatalf("output cap cleanup not bounded: %v", time.Since(start))
	}
	assertProcessGone(t, pidFile)

	// Lowered output cap.
	limits.GHStdout = 64
	r, _ = run(`printf '%s' '` + issue + `'`)
	wantFail(t, r, 1, "command_output_limit")
}

// TestFakeGitControlOutput uses a fake git whose HEAD output carries terminal
// control bytes; it must be rejected, not rendered.
func TestFakeGitControlOutput(t *testing.T) {
	env(t)
	top := t.TempDir()
	top, _ = filepath.EvalSymlinks(top)
	d := t.TempDir()
	script(t, filepath.Join(d, "git"), `for a in "$@"; do last="$a"; done
case "$*" in
*is-inside-work-tree*) echo true;;
*show-toplevel*) echo '`+top+`';;
*"config -z"*) ;;
*show-object-format*) echo sha1;;
*symbolic-ref*) exit 0;;
*"rev-parse --verify"*) printf '\033[31m`+tf.SecretContent+`\n';;
*) exit 0;;
esac`)
	t.Setenv("PATH", d+":"+os.Getenv("PATH"))
	r := runCLI(t, nil, "inspect", "--repo", top)
	wantFail(t, r, 1, "invalid_git_output")
}

func TestLimits(t *testing.T) {
	checkLimitsBudget(t)
	// The Git timeout and total-budget cases and their negative controls run
	// in isolated controllers (limits_timeout_test.go) while the cap cases
	// below run here.
	timeouts := startLimitsTimeoutCases(t)
	home := env(t)
	dir := mustInit(t, home, t.TempDir(), "sha1")
	tf.CommitSources(home, dir)
	mustWrite(t, filepath.Join(dir, "u1"), "x")
	limits := inspect.DefaultLimits
	limits.GitStdout = 4
	wantFail(t, runCLI(t, &limits, "inspect", "--repo", dir), 1, "command_output_limit")
	limits = inspect.DefaultLimits
	limits.FinalStdout = 100
	wantFail(t, runCLI(t, &limits, "inspect", "--repo", dir, "--json"), 1, "output_limit")
	full := decode(t, runCLI(t, nil, "inspect", "--repo", dir, "--json"))
	_ = full
	r := runCLI(t, nil, "inspect", "--repo", dir, "--json")
	limits.FinalStdout = len(r.stdout)
	decode(t, runCLI(t, &limits, "inspect", "--repo", dir, "--json"))
	limits.FinalStdout = len(r.stdout) - 1
	wantFail(t, runCLI(t, &limits, "inspect", "--repo", dir, "--json"), 1, "output_limit")

	// Git child stderr over 64 KiB fails even though Git itself succeeds.
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	noisy := t.TempDir()
	script(t, filepath.Join(noisy, "git"), `head -c 65537 /dev/zero >&2; exec '`+realGit+`' "$@"`)
	t.Setenv("PATH", noisy+":"+os.Getenv("PATH"))
	wantFail(t, runCLI(t, nil, "inspect", "--repo", dir), 1, "command_output_limit")

	// Git timeout via a slow fake git, and the total budget bounding the
	// whole operation, each with nonce readiness and owned-descendant
	// cleanup established before acceptance.
	timeouts.finish(t)
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{
		{}, {"inspect"}, {"bogus"}, {"inspect", "--repo"}, {"inspect", "--repo", ""},
		{"inspect", "--repo", "x", "--repo", "y"}, {"inspect", "--repo", "x", "--json=1"},
		{"inspect", "--repo", "x", "--output", "f"}, {"inspect", "--repo", "x", "--checkpoint", "abc"},
		{"inspect", "--repo", "x", "--coordination-file", "f", "--github", "a/b#1"},
		{"inspect", "--repo", "x", "--github", "a/b"}, {"inspect", "--repo", "x", "--github", "a/b#0"},
		{"inspect", "--repo", "x", "--github", "a/b#01"}, {"inspect", "--repo", "x", "--github", "a/b#2147483648"},
		{"inspect", "--repo", "x", "--github", "a;rm/b#1"}, {"inspect", "--repo", "x", "--github", "a/b/c#1"},
		{"inspect", "--repo", "x", "--github", "https://github.com/a/b#1"}, {"inspect", "--repo", "x", "--github", "a/..#1"},
	} {
		wantFail(t, runCLI(t, nil, args...), 2, "invalid_usage")
	}
	for _, args := range [][]string{{"--help"}, {"inspect", "--help"}, {"-h"}} {
		r := runCLI(t, nil, args...)
		if r.code != 0 || r.stdout != Usage || r.stderr != "" {
			t.Fatalf("help %v", args)
		}
	}
	// Usage validation precedes repository access.
	wantFail(t, runCLI(t, nil, "inspect", "--repo", "/nonexistent", "--github", "bad"), 2, "invalid_usage")
}
