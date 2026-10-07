package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Independent hand-written oracles for `baw context`; none is derived from
// production tables, structs or output.

const wantContextHelp = "Usage:\n" +
	"  baw context --repo PATH --role ROLE [--json]\n" +
	"\n" +
	"ROLE is lead, worker or reviewer.\n" +
	"Paths are relative to the top level of the Git worktree containing --repo.\n" +
	"Read AGENTS.md, workflow/protocol.md and only the selected role.\n" +
	"Reconcile the assigned ticket, approved authority and run evidence before action.\n" +
	"This packet grants no assignment, readiness or authority and starts no agent.\n"

var ctxRoles = []string{"lead", "worker", "reviewer"}

// ctxPaths is the hand-written eight-source oracle for role.
func ctxPaths(role string) []string {
	return []string{"AGENTS.md", "workflow/protocol.md", "workflow/roles/" + role + ".md", "README.md",
		"docs/design/product-contract.md", "docs/architecture/overview.md", "docs/developer/environment.md",
		"docs/operator/agent-lifecycle.md"}
}

var allRoleFiles = []string{"workflow/roles/lead.md", "workflow/roles/worker.md", "workflow/roles/reviewer.md"}

// ctxFixture commits the seven common sources, all three role files and
// x.txt, then stages a new y.txt, edits x.txt and adds untracked z.txt:
// counts staged=1 unstaged=1 untracked=1 by construction.
func ctxFixture(t *testing.T, home, format string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	if err := tf.Init(home, dir, format); err != nil {
		t.Fatalf("%s repository required, not skipped: %v", format, err)
	}
	for _, p := range append(ctxPaths("lead"), "workflow/roles/worker.md", "workflow/roles/reviewer.md", "x.txt") {
		mustWrite(t, filepath.Join(dir, p), "dummy maintained source\n")
	}
	git(t, home, dir, "add", "-A")
	git(t, home, dir, "commit", "-q", "-m", "base")
	head := oid(t, home, dir, "HEAD")
	mustWrite(t, filepath.Join(dir, "y.txt"), "staged\n")
	git(t, home, dir, "add", "y.txt")
	mustWrite(t, filepath.Join(dir, "x.txt"), "edited\n")
	mustWrite(t, filepath.Join(dir, "z.txt"), "untracked\n")
	return dir, head
}

func wantContextText(role, format, head string) string {
	var b strings.Builder
	b.WriteString("BAW context references\nRole: " + role + "\nAssignment: unassigned\nAuthority: not_evaluated\n")
	b.WriteString("Readiness: not_evaluated\nSnapshot: non_atomic\nRead first: AGENTS.md\nRead next: workflow/protocol.md\n")
	b.WriteString("Read role: workflow/roles/" + role + ".md\n")
	b.WriteString("BAW inspection\nHEAD: " + head + "\nBranch state: attached\nObject format: " + format + "\n")
	b.WriteString("Changes: staged=1 unstaged=1 untracked=1 conflicted=0\nLinked worktrees: 0\nSubmodules: 0\nCheckpoint: not_requested\n")
	for _, p := range ctxPaths(role) {
		b.WriteString("Source: " + p + " presence=present worktree_state=clean head_ref=git:" + head + ":" + p + " worktree_ref=worktree:" + p + "\n")
	}
	b.WriteString("Coordination: none\nRemote freshness: unknown\nRuntime state: unknown\nProcess ownership: unknown\nReservation ownership: unknown\n")
	return b.String()
}

// wantContextJSON is the exact generic JSON value (numbers as json.Number).
func wantContextJSON(role, format, head string) map[string]any {
	var sources []any
	for _, p := range ctxPaths(role) {
		sources = append(sources, map[string]any{"path": p, "presence": "present", "worktree_state": "clean",
			"head_ref": "git:" + head + ":" + p, "worktree_ref": "worktree:" + p})
	}
	n := func(s string) json.Number { return json.Number(s) }
	return map[string]any{
		"schema_version": n("1"), "operation": "context", "role": role, "assignment": "unassigned",
		"authority": "not_evaluated", "readiness": "not_evaluated", "snapshot": "non_atomic",
		"reading_order": []any{"AGENTS.md", "workflow/protocol.md", "workflow/roles/" + role + ".md"},
		"inspection": map[string]any{
			"schema_version": n("1"), "status": "complete",
			"repository": map[string]any{"state": "observed", "object_format": format, "head_state": "present", "head": head,
				"branch_state": "attached",
				"counts": map[string]any{"staged": n("1"), "unstaged": n("1"), "untracked": n("1"), "conflicted": n("0"),
					"linked_worktrees": n("0"), "submodules": n("0")},
				"checkpoint": map[string]any{"state": "not_requested", "oid": nil, "commits_ahead": nil}},
			"sources": sources,
			"coordination": map[string]any{"mode": "none", "state": "absent", "number": nil, "issue_state": nil,
				"updated_at": nil, "source_url": nil},
			"remote_freshness": "unknown", "runtime_state": "unknown", "process_ownership": "unknown",
			"reservation_ownership": "unknown",
		},
	}
}

// decodeContext parses exactly one JSON object plus newline into generic
// values, keeping numbers literal so integer types are checked exactly.
func decodeContext(t *testing.T, r result) map[string]any {
	t.Helper()
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("exit %d stderr %q", r.code, r.stderr)
	}
	if !strings.HasSuffix(r.stdout, "}\n") || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("not one JSON object plus newline: %q", r.stdout)
	}
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	dec.UseNumber()
	var v map[string]any
	if err := dec.Decode(&v); err != nil || dec.More() {
		t.Fatalf("decode: %v", err)
	}
	return v
}

func sourcePathsOf(t *testing.T, v map[string]any) []string {
	t.Helper()
	var out []string
	for _, s := range v["inspection"].(map[string]any)["sources"].([]any) {
		out = append(out, s.(map[string]any)["path"].(string))
	}
	return out
}

func TestContextExactMatrix(t *testing.T) {
	home := env(t)
	ghDir, calls := fakeGH(t, "echo should-not-run")
	t.Setenv("PATH", ghDir+":"+os.Getenv("PATH"))
	for _, format := range []string{"sha1", "sha256"} {
		dir, head := ctxFixture(t, home, format)
		width := map[string]int{"sha1": 40, "sha256": 64}[format]
		if len(head) != width {
			t.Fatalf("fixture %s HEAD %q", format, head)
		}
		for _, role := range ctxRoles {
			text := runCLI(t, nil, "context", "--repo", dir, "--role", role)
			if want := wantContextText(role, format, head); text.code != 0 || text.stderr != "" || text.stdout != want {
				t.Fatalf("%s %s text exit %d stderr %q\n got:\n%s\nwant:\n%s", format, role, text.code, text.stderr, text.stdout, want)
			}
			r := runCLI(t, nil, "context", "--json", "--role="+role, "--repo="+dir)
			got := decodeContext(t, r)
			if want := wantContextJSON(role, format, head); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %s json\n got %v\nwant %v", format, role, got, want)
			}
			// Same observations in both outputs, and stable for unchanged state.
			if again := runCLI(t, nil, "context", "--repo", dir, "--role", role, "--json"); again.stdout != r.stdout {
				t.Fatalf("%s %s unstable JSON", format, role)
			}
			if !strings.Contains(r.stdout, `"inspection":{"schema_version":1,"status":"complete","repository":{`) {
				t.Fatalf("embedded inspection not in the inspect schema: %s", r.stdout)
			}
		}
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("gh was invoked")
	}
}

// I2: references are relative to the top level of the worktree containing
// --repo; a subdirectory or an ancestor symlink alias yields identical bytes.
func TestContextTopLevelRelative(t *testing.T) {
	home := env(t)
	dir, head := ctxFixture(t, home, "sha1")
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(dir), link); err != nil {
		t.Fatal(err)
	}
	for _, role := range ctxRoles {
		for _, js := range []bool{false, true} {
			args := func(repo string) []string {
				a := []string{"context", "--repo", repo, "--role", role}
				if js {
					a = append(a, "--json")
				}
				return a
			}
			top := runCLI(t, nil, args(dir)...)
			if top.code != 0 || !strings.Contains(top.stdout, "worktree:workflow/roles/"+role+".md") || !strings.Contains(top.stdout, "git:"+head+":AGENTS.md") {
				t.Fatalf("top %s: %+v", role, top)
			}
			for _, p := range []string{filepath.Join(dir, "docs/design"), filepath.Join(dir, "workflow", "roles"), filepath.Join(link, "repo"), filepath.Join(link, "repo", "docs")} {
				if got := runCLI(t, nil, args(p)...); got != top {
					t.Fatalf("%s from %s differs:\n%s\nvs\n%s", role, p, got.stdout, top.stdout)
				}
			}
			if strings.Contains(top.stdout, dir) || strings.Contains(top.stdout, filepath.Dir(dir)) {
				t.Fatal("top-level path printed")
			}
		}
	}
}

func TestContextHelpAndGlobalUsage(t *testing.T) {
	r := runCLI(t, nil, "context", "--help")
	if r.code != 0 || r.stderr != "" || r.stdout != wantContextHelp {
		t.Fatalf("context help %+v", r)
	}
	if !strings.Contains(Usage, "\n  baw context --repo PATH --role ROLE [--json]\n") {
		t.Fatal("global usage lacks context syntax")
	}
	for _, old := range []string{
		"  baw inspect --repo PATH [--checkpoint FULL_COMMIT_SHA] [--coordination-file FILE | --github OWNER/REPO#NUMBER] [--json]\n",
		"baw inspect reports read-only Git state counts, fixed maintained-source\nreferences and coordination metadata.",
		"baw run diagnose counts the structural state of that record and its\n",
		"Exit status: 0 success, 1 failure (stderr \"baw: CODE\"), 2 invalid usage.\n",
	} {
		if !strings.Contains(Usage, old) {
			t.Fatalf("old usage text lost: %q", old)
		}
	}
	for _, args := range [][]string{{"--help"}, {"-h"}, {"help"}, {"inspect", "--help"}, {"run", "--help"}, {"status", "--help"}, {"run", "diagnose", "--help"}} {
		if r := runCLI(t, nil, args...); r.code != 0 || r.stdout != Usage || r.stderr != "" {
			t.Fatalf("old alias %v: %+v", args, r)
		}
	}
	if len(Usage) > 4096 {
		t.Fatalf("global usage %d bytes exceeds the diagnose help cap", len(Usage))
	}
}

func TestContextUsageBeforeFilesystem(t *testing.T) {
	env(t)
	marker := t.TempDir()
	d := t.TempDir()
	script(t, filepath.Join(d, "git"), "touch '"+filepath.Join(marker, "git-ran")+"'\nexit 1")
	t.Setenv("PATH", d+":"+os.Getenv("PATH"))
	for _, args := range [][]string{
		{"context"}, {"context", "--repo", "/nonexistent"}, {"context", "--role", "worker"},
		{"context", "--repo", "/nonexistent", "--role", "helper"}, {"context", "--repo", "x", "--role", "Lead"},
		{"context", "--repo", "x", "--role", "WORKER"}, {"context", "--repo", "x", "--role", "lead "},
		{"context", "--repo", "x", "--role", "workflow/roles/lead.md"}, {"context", "--repo", "x", "--role", "../lead"},
		{"context", "--repo", "x", "--role", ""}, {"context", "--repo", "x", "--role="}, {"context", "--repo=", "--role", "lead"},
		{"context", "--repo", "x", "--role"}, {"context", "--repo"},
		{"context", "--repo", "x", "--role", "lead", "--role", "lead"}, {"context", "--repo", "x", "--repo", "x", "--role", "lead"},
		{"context", "--repo", "x", "--role", "lead", "--json", "--json"}, {"context", "--repo", "x", "--role", "lead", "--json=true"},
		{"context", "--repo", "x", "--role", "lead", "--json="}, {"context", "--repo", "x", "--role", "lead", "extra"},
		{"context", "--repo", "x", "--role", "lead", "--checkpoint", strings.Repeat("a", 40)},
		{"context", "--repo", "x", "--role", "lead", "--coordination-file", "f"},
		{"context", "--repo", "x", "--role", "lead", "--github", "a/b#1"},
		{"context", "--repo", "x", "--role", "lead", "--state-dir", "d"}, {"context", "--repo", "x", "--role", "lead", "--ticket", "t"},
		{"context", "-h"}, {"context", "help"}, {"context", "--help", "--json"}, {"context", "--repo", "x", "--role", "lead", "--help"},
		{"context", "--help", "--help"}, {"context", "--help=1"}, {"context", "--repo", "x", "--role", "lead", "-h"},
		{"context", "--Repo", "x", "--role", "lead"}, {"Context", "--repo", "x", "--role", "lead"},
	} {
		wantFail(t, runCLI(t, nil, args...), 2, "invalid_usage")
	}
	if _, err := os.Stat(filepath.Join(marker, "git-ran")); err == nil {
		t.Fatal("git ran before usage validation")
	}
}

func TestContextObservationsNotReadiness(t *testing.T) {
	home := env(t)
	// No maintained documents at all, unborn HEAD: exit 0, every source absent.
	dir := mustInit(t, home, filepath.Join(t.TempDir(), "r"), "sha1")
	for _, role := range ctxRoles {
		v := decodeContext(t, runCLI(t, nil, "context", "--repo", dir, "--role", role, "--json"))
		repo := v["inspection"].(map[string]any)["repository"].(map[string]any)
		if repo["head"] != nil || repo["head_state"] != "unborn" || repo["branch_state"] != "unborn" ||
			v["readiness"] != "not_evaluated" || v["assignment"] != "unassigned" || v["authority"] != "not_evaluated" {
			t.Fatalf("unborn %v", v)
		}
		for _, s := range v["inspection"].(map[string]any)["sources"].([]any) {
			m := s.(map[string]any)
			if m["presence"] != "absent" || m["worktree_state"] != "absent" || m["head_ref"] != nil || m["worktree_ref"] != nil {
				t.Fatalf("missing source %v", m)
			}
		}
		if !reflect.DeepEqual(sourcePathsOf(t, v), ctxPaths(role)) {
			t.Fatalf("paths %v", sourcePathsOf(t, v))
		}
	}
	// Detached HEAD keeps the OID; dirty selected role is an observation.
	head, err := tf.CommitSources(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	git(t, home, dir, "checkout", "-q", "--detach")
	mustWrite(t, filepath.Join(dir, "workflow/roles/reviewer.md"), "dirty untracked\n")
	r := runCLI(t, nil, "context", "--repo", dir, "--role", "reviewer")
	for _, want := range []string{"Readiness: not_evaluated\n", "HEAD: " + head + "\nBranch state: detached\n",
		"Changes: staged=0 unstaged=0 untracked=1 conflicted=0\n",
		"Source: workflow/roles/reviewer.md presence=present worktree_state=untracked head_ref=- worktree_ref=worktree:workflow/roles/reviewer.md\n"} {
		if r.code != 0 || r.stderr != "" || !strings.Contains(r.stdout, want) {
			t.Fatalf("detached/dirty missing %q in %q", want, r.stdout)
		}
	}
	for _, banned := range []string{"ready", "approved", "retry", "recover", "delete"} {
		if strings.Contains(strings.ToLower(strings.ReplaceAll(r.stdout, "Readiness: not_evaluated", "")), banned) {
			t.Fatalf("advice word %q in output", banned)
		}
	}
}

// --- joined child processes for real unsafe/special-file probes ---

const (
	ctxChildEnv    = "BAW_CLI_TEST_CONTEXT_ARGS" // JSON array of Run arguments
	ctxChildPrefix = "CLI-CONTEXT-RESULT "
)

type ctxChildResult struct {
	Code              int
	Stdout, Stderr    string
	FDBefore, FDAfter []string
	SameAsWarmUp      bool
}

func openFDs() ([]string, error) {
	return ctxObserveFDs(ctxFDOps{open: ctxOpenFDDir})
}

// TestContextChildProcess is executed only as a child of this test binary.
// It runs the request twice and lists its open descriptors around the
// second call: the first call lets the runtime create its long-lived
// descriptors (such as the poller), so any difference is a descriptor the
// request acquired and failed to close.
func TestContextChildProcess(t *testing.T) {
	spec := os.Getenv(ctxChildEnv)
	if spec == "" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(spec), &args); err != nil || len(args) < 1 || (args[0] != "context" && args[0] != "inspect") {
		t.Fatalf("bad child arguments %q: %v", spec, err)
	}
	var out0, errb0, out, errb bytes.Buffer
	code0 := Run(args, &out0, &errb0)
	before, err := openFDs()
	if err != nil {
		t.Fatal(err)
	}
	code := Run(args, &out, &errb)
	after, err := openFDs()
	if err != nil {
		t.Fatal(err)
	}
	same := code0 == code && out0.String() == out.String() && errb0.String() == errb.String()
	b, err := json.Marshal(ctxChildResult{code, out.String(), errb.String(), before, after, same})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n%s%s\n", ctxChildPrefix, b)
}

// runCtxChild runs args in a child of this test binary, killed and always
// waited for within 10 seconds.
func runCtxChild(t *testing.T, args ...string) result {
	t.Helper()
	r, err := runCtxChildWith(newCtxChildOps(context.WithTimeout, exec.CommandContext, time.Second), os.Environ(), args)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Selected and common unsafe sources fail with fixed codes; an unsafe
// unselected role file is never probed as a context source, while legacy
// inspect still probes its fixed lead file.
func TestContextUnsafeSources(t *testing.T) {
	home := env(t)
	outside := t.TempDir()
	mustWrite(t, filepath.Join(outside, "secret.md"), tf.SecretContent)
	fixtures := 0
	clean := func() string {
		fixtures++
		dir, _ := ctxFixture(t, home, "sha1")
		return dir
	}
	type kind struct {
		name string
		make func(t *testing.T, path string)
		code string
	}
	kinds := []kind{
		{"fifo", func(t *testing.T, p string) {
			os.Remove(p)
			if err := syscall.Mkfifo(p, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "source_unavailable"},
		{"directory", func(t *testing.T, p string) { os.Remove(p); os.Mkdir(p, 0o755) }, "source_unavailable"},
		{"symlink", func(t *testing.T, p string) {
			os.Remove(p)
			if err := os.Symlink(filepath.Join(outside, "secret.md"), p); err != nil {
				t.Fatal(err)
			}
		}, "source_symlink"},
	}
	// The three role requests against one completed fixture run as one
	// bounded batch of joined children. The parent touches the fixture again
	// only after every child has joined and its index is unchanged.
	ops := newCtxChildOps(context.WithTimeout, exec.CommandContext, time.Second)
	parentEnv := os.Environ()
	var ids []string
	var argvs [][]string
	var dirs []string
	var results []result
	batch := func(dir, id string, request func(role string) []string) []result {
		t.Helper()
		before, err := ctxIndexOf(dir)
		if err != nil {
			t.Fatal(err)
		}
		var reqs [][]string
		for _, role := range ctxRoles {
			reqs = append(reqs, request(role))
			ids = append(ids, id+role)
			argvs, dirs = append(argvs, append([]string(nil), reqs[len(reqs)-1]...)), append(dirs, dir)
		}
		res, err := ctxBatch(parentEnv, reqs, func(env, args []string) (result, error) { return runCtxChildWith(ops, env, args) })
		if err != nil {
			t.Fatal(err)
		}
		after, err := ctxIndexOf(dir)
		if err != nil || !bytes.Equal(after.data, before.data) || !after.mtime.Equal(before.mtime) {
			t.Fatalf("index of %s changed during the batch (%v)", dir, err)
		}
		results = append(results, res...)
		return res
	}
	for _, k := range kinds {
		for _, bad := range ctxRoles {
			dir := clean()
			k.make(t, filepath.Join(dir, "workflow/roles", bad+".md"))
			res := batch(dir, "role/"+k.name+"/"+bad+"/", func(role string) []string {
				return []string{"context", "--repo", dir, "--role", role, "--json"}
			})
			for i, role := range ctxRoles {
				r := res[i]
				if role == bad {
					wantFail(t, r, 1, k.code)
					continue
				}
				if r.code != 0 || r.stderr != "" || strings.Contains(r.stdout, "workflow/roles/"+bad+".md") ||
					!strings.Contains(r.stdout, `"path":"workflow/roles/`+role+`.md"`) {
					t.Fatalf("%s %s as %s: %+v", k.name, bad, role, r)
				}
			}
			legacy := runCtxChild(t, "inspect", "--repo", dir)
			ids, results = append(ids, "legacy/"+k.name+"/"+bad), append(results, legacy)
			argvs, dirs = append(argvs, []string{"inspect", "--repo", dir}), append(dirs, dir)
			if bad == "lead" {
				wantFail(t, legacy, 1, k.code)
			} else if legacy.code != 0 || strings.Contains(legacy.stdout, bad+".md") {
				t.Fatalf("legacy with unsafe %s: %+v", bad, legacy)
			}
		}
		// Common path and a related ancestor fail for every role.
		for _, p := range []string{"AGENTS.md", "docs/operator/agent-lifecycle.md"} {
			dir := clean()
			k.make(t, filepath.Join(dir, p))
			for _, r := range batch(dir, "common/"+k.name+"/"+p+"/", func(role string) []string {
				return []string{"context", "--repo", dir, "--role", role}
			}) {
				wantFail(t, r, 1, k.code)
			}
		}
	}
	for _, anc := range []string{"workflow/roles", "workflow", "docs"} {
		dir := clean()
		real := filepath.Join(outside, strings.ReplaceAll(anc, "/", "-"))
		if err := os.Rename(filepath.Join(dir, anc), real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(dir, anc)); err != nil {
			t.Fatal(err)
		}
		for _, r := range batch(dir, "ancestor/"+anc+"/", func(role string) []string {
			return []string{"context", "--repo", dir, "--role", role}
		}) {
			wantFail(t, r, 1, "source_symlink")
		}
	}
	// Independent hand-written matrix: every request exactly once, in order,
	// with its expected response, on 18 fixtures.
	if err := ctxCheckMatrix(ids); err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if err := ctxWantMatrix(id, results[i]); err != nil {
			t.Fatal(err)
		}
		if err := ctxWantArgv(id, dirs[i], argvs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if fixtures != 18 {
		t.Fatalf("%d fixtures, want 18", fixtures)
	}
}

// Repository-wide guards still apply whatever the role, before any helper
// can run; an unselected role cannot avoid them.
func TestContextGitGuards(t *testing.T) {
	home := env(t)
	dir, markers := helperFixture(t, home, true)
	for _, role := range ctxRoles {
		wantFail(t, runCLI(t, nil, "context", "--repo", dir, "--role", role), 1, "unsupported_filters")
	}
	assertNoMarkers(t, markers)
	dir, markers = helperFixture(t, home, false)
	for _, role := range ctxRoles {
		v := decodeContext(t, runCLI(t, nil, "context", "--repo", dir, "--role", role, "--json"))
		counts := v["inspection"].(map[string]any)["repository"].(map[string]any)["counts"].(map[string]any)
		if counts["unstaged"] != json.Number("1") || counts["untracked"] != json.Number("5") {
			t.Fatalf("helper fixture counts %v", counts)
		}
	}
	assertNoMarkers(t, markers)
	for _, kv := range [][2]string{{"extensions.partialClone", "origin"}, {"remote.origin.promisor", "true"}} {
		d := mustInit(t, home, t.TempDir(), "sha1")
		tf.CommitSources(home, d)
		git(t, home, d, "config", kv[0], kv[1])
		for _, role := range ctxRoles {
			wantFail(t, runCLI(t, nil, "context", "--repo", d, "--role", role), 1, "unsupported_partial_clone")
		}
	}
	wantFail(t, runCLI(t, nil, "context", "--repo", t.TempDir(), "--role", "worker"), 1, "repository_unavailable")
	limits := inspect.DefaultLimits
	limits.GitStdout = 4
	wantFail(t, runCLI(t, &limits, "context", "--repo", dir, "--role", "worker"), 1, "command_output_limit")
}

type fileFacts struct {
	mode                      os.FileMode
	uid, dev, ino, nlink      uint64
	size, mtimeSec, mtimeNsec int64
	content, link             string
}

// tree records every entry's metadata (excluding atime) and bytes.
func treeFacts(t *testing.T, root string) map[string]fileFacts {
	t.Helper()
	out := map[string]fileFacts{}
	err := filepath.Walk(root, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		st := fi.Sys().(*syscall.Stat_t)
		f := fileFacts{fi.Mode(), uint64(st.Uid), uint64(st.Dev), st.Ino, uint64(st.Nlink), st.Size, st.Mtimespec.Sec, st.Mtimespec.Nsec, "", ""}
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			f.content = string(b)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			f.link, _ = os.Readlink(p)
		}
		out[p] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestContextSecretsAndPreservation(t *testing.T) {
	home := env(t)
	ghDir, calls := fakeGH(t, "echo should-not-run")
	t.Setenv("PATH", ghDir+":"+os.Getenv("PATH"))
	dir, _ := ctxFixture(t, home, "sha1")
	for _, p := range allRoleFiles {
		mustWrite(t, filepath.Join(dir, p), tf.SecretProse+" "+tf.SecretContent+"\n")
	}
	mustWrite(t, filepath.Join(dir, "AGENTS.md"), tf.SecretProse+"\n")
	mustWrite(t, filepath.Join(dir, tf.SecretFile+".txt"), tf.SecretContent)
	mustWrite(t, filepath.Join(dir, ".gitignore"), "ignored-*\n")
	mustWrite(t, filepath.Join(dir, "ignored-x.env"), "TOKEN="+tf.SecretIgnored)
	git(t, home, dir, "commit", "-q", "--allow-empty", "-m", tf.SecretMessage)
	git(t, home, dir, "checkout", "-q", "-b", tf.SecretBranch)
	git(t, home, dir, "remote", "add", "origin", "https://"+tf.SecretRemote+"@example.invalid/x.git")
	git(t, home, dir, "config", "user.note", tf.SecretConfig)
	before := treeFacts(t, dir)
	for _, role := range ctxRoles {
		for _, args := range [][]string{
			{"context", "--repo", dir, "--role", role},
			{"context", "--repo", filepath.Join(dir, "workflow"), "--role", role, "--json"},
		} {
			r := runCLI(t, nil, args...)
			if r.code != 0 || r.stderr != "" {
				t.Fatalf("%v: %+v", args, r)
			}
			for _, s := range []string{tf.SecretBranch, tf.SecretFile, tf.SecretContent, tf.SecretMessage, tf.SecretRemote,
				tf.SecretConfig, tf.SecretIgnored, tf.SecretProse, dir, home, "\x1b", "decoy"} {
				if strings.Contains(r.stdout, s) {
					t.Fatalf("output leaked %q", s)
				}
			}
		}
	}
	after := treeFacts(t, dir)
	if !reflect.DeepEqual(before, after) {
		for p, f := range before {
			if after[p] != f {
				t.Errorf("changed: %s", p)
			}
		}
		t.Fatalf("context changed the fixture (%d vs %d entries)", len(before), len(after))
	}
	if _, err := os.Stat(calls); err == nil {
		t.Fatal("gh was invoked")
	}
}

// --- sinks and limits ---

type errWriter struct{}

func (errWriter) Write(p []byte) (int, error) { return 0, errors.New("sink failed") }

// shortWriter accepts at most n bytes per call and reports no error.
type shortWriter struct {
	n   int
	got bytes.Buffer
}

func (s *shortWriter) Write(p []byte) (int, error) {
	k := min(len(p), s.n)
	s.got.Write(p[:k])
	return k, nil
}

func TestContextDeliveryAndLimits(t *testing.T) {
	if MaxContextOutput != 65536 {
		t.Fatalf("context cap %d", MaxContextOutput)
	}
	exact := bytes.Repeat([]byte("a"), 65536)
	var w bytes.Buffer
	if code := deliverContext(&w, exact); code != "" || !bytes.Equal(w.Bytes(), exact) {
		t.Fatalf("exactly 65536: %q %d", code, w.Len())
	}
	w.Reset()
	if code := deliverContext(&w, append(exact, 'b')); code != "output_limit" || w.Len() != 0 {
		t.Fatalf("65537: %q wrote %d", code, w.Len())
	}
	if code := deliverContext(errWriter{}, []byte("x")); code != "output_unavailable" {
		t.Fatalf("error writer %q", code)
	}
	sw := &shortWriter{n: 3}
	if code := deliverContext(sw, []byte("abcdef")); code != "output_unavailable" || sw.got.String() != "abc" {
		t.Fatalf("short writer %q %q", code, sw.got.String())
	}

	home := env(t)
	dir, _ := ctxFixture(t, home, "sha1")
	full := runCLI(t, nil, "context", "--repo", dir, "--role", "worker")
	for _, args := range [][]string{{"context", "--repo", dir, "--role", "worker"}, {"context", "--help"}} {
		var errb bytes.Buffer
		if code := Run(args, errWriter{}, &errb); code != 1 || errb.String() != "baw: output_unavailable\n" {
			t.Fatalf("%v failing stdout: %d %q", args, code, errb.String())
		}
		sw := &shortWriter{n: 10}
		errb.Reset()
		if code := Run(args, sw, &errb); code != 1 || errb.String() != "baw: output_unavailable\n" || sw.got.Len() != 10 {
			t.Fatalf("%v short stdout: %d %q %q", args, code, errb.String(), sw.got.String())
		}
		// The prefix already accepted is the start of the packet; it is not withdrawn.
		if args[1] != "--help" && !strings.HasPrefix(full.stdout, sw.got.String()) {
			t.Fatalf("prefix %q", sw.got.String())
		}
		// A failing stderr keeps the chosen exit status.
		if code := Run(args, errWriter{}, errWriter{}); code != 1 {
			t.Fatalf("%v failing both: %d", args, code)
		}
	}
	if code := Run([]string{"context", "--role", "helper", "--repo", dir}, errWriter{}, errWriter{}); code != 2 {
		t.Fatalf("usage with failing stderr: %d", code)
	}
	// The legacy 32 KiB inspect cap is not applied to context output.
	limits := inspect.DefaultLimits
	limits.FinalStdout = 100
	if r := runCLI(t, &limits, "context", "--repo", dir, "--role", "worker"); r != full {
		t.Fatalf("context bound by legacy cap: %+v", r)
	}
	wantFail(t, runCLI(t, &limits, "inspect", "--repo", dir), 1, "output_limit")
}

// --- request-specific profiles: alternating and concurrent ---

func TestContextProfilesIndependent(t *testing.T) {
	home := env(t)
	dir, _ := ctxFixture(t, home, "sha1")
	mustWrite(t, filepath.Join(dir, "workflow/roles/worker.md"), "worker edit\n")
	if err := os.Remove(filepath.Join(dir, "workflow/roles/reviewer.md")); err != nil {
		t.Fatal(err)
	}
	wantRole := map[string]string{
		"lead":     "workflow/roles/lead.md presence=present worktree_state=clean",
		"worker":   "workflow/roles/worker.md presence=present worktree_state=modified",
		"reviewer": "workflow/roles/reviewer.md presence=absent worktree_state=deleted",
	}
	check := func(kind string, r result) error {
		if r.code != 0 || r.stderr != "" {
			return fmt.Errorf("%s: %+v", kind, r)
		}
		var lines []string
		for _, l := range strings.Split(r.stdout, "\n") {
			if p, ok := strings.CutPrefix(l, "Source: "); ok {
				lines = append(lines, strings.SplitN(p, " ", 2)[0])
			}
		}
		role := kind
		if kind == "legacy" {
			role = "lead"
			if strings.HasPrefix(r.stdout, "BAW context") {
				return fmt.Errorf("legacy output has a context prefix")
			}
		} else if !strings.HasPrefix(r.stdout, "BAW context references\nRole: "+kind+"\n") {
			return fmt.Errorf("%s: wrong prefix", kind)
		}
		if !reflect.DeepEqual(lines, ctxPaths(role)) || !strings.Contains(r.stdout, "Source: "+wantRole[role]) ||
			!strings.Contains(r.stdout, "Changes: staged=1 unstaged=3 untracked=1 conflicted=0\n") {
			return fmt.Errorf("%s: sources %v\n%s", kind, lines, r.stdout)
		}
		return nil
	}
	call := func(kind string) result {
		var out, errb bytes.Buffer
		args := []string{"context", "--repo", dir, "--role", kind}
		if kind == "legacy" {
			args = []string{"inspect", "--repo", dir}
		}
		code := Run(args, &out, &errb)
		return result{code, out.String(), errb.String()}
	}
	kinds := []string{"lead", "worker", "legacy", "reviewer", "worker", "lead", "reviewer", "legacy", "worker"}
	for _, k := range kinds {
		if err := check(k, call(k)); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 48)
	for i := 0; i < 48; i++ {
		k := []string{"legacy", "lead", "worker", "reviewer"}[i%4]
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- check(k, call(k))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(inspect.SourcePaths, ctxPaths("lead")) {
		t.Fatalf("shared legacy list changed: %v", inspect.SourcePaths)
	}
}
