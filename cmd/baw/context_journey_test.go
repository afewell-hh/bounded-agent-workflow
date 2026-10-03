package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Recorded `baw context` journeys against a built artifact:
//
//	go test ./cmd/baw -run '^TestContextBinaryJourneys$' -count=1 -args -baw-binary=PATH -journey-dir=DIR
//
// Every expected value below is written by hand from the fixture setup.

func ctxSources(role string) []string {
	return []string{"AGENTS.md", "workflow/protocol.md", "workflow/roles/" + role + ".md", "README.md",
		"docs/design/product-contract.md", "docs/architecture/overview.md", "docs/developer/environment.md",
		"docs/operator/agent-lifecycle.md"}
}

func ctxText(role, format, head string) string {
	s := "BAW context references\nRole: " + role + "\nAssignment: unassigned\nAuthority: not_evaluated\n" +
		"Readiness: not_evaluated\nSnapshot: non_atomic\nRead first: AGENTS.md\nRead next: workflow/protocol.md\n" +
		"Read role: workflow/roles/" + role + ".md\n" +
		"BAW inspection\nHEAD: " + head + "\nBranch state: attached\nObject format: " + format + "\n" +
		"Changes: staged=1 unstaged=1 untracked=1 conflicted=0\nLinked worktrees: 0\nSubmodules: 0\nCheckpoint: not_requested\n"
	for _, p := range ctxSources(role) {
		s += "Source: " + p + " presence=present worktree_state=clean head_ref=git:" + head + ":" + p + " worktree_ref=worktree:" + p + "\n"
	}
	return s + "Coordination: none\nRemote freshness: unknown\nRuntime state: unknown\nProcess ownership: unknown\nReservation ownership: unknown\n"
}

func ctxJSON(role, format, head string) map[string]any {
	var src []any
	for _, p := range ctxSources(role) {
		src = append(src, map[string]any{"path": p, "presence": "present", "worktree_state": "clean",
			"head_ref": "git:" + head + ":" + p, "worktree_ref": "worktree:" + p})
	}
	n := json.Number("1")
	z := json.Number("0")
	return map[string]any{
		"schema_version": n, "operation": "context", "role": role, "assignment": "unassigned", "authority": "not_evaluated",
		"readiness": "not_evaluated", "snapshot": "non_atomic",
		"reading_order": []any{"AGENTS.md", "workflow/protocol.md", "workflow/roles/" + role + ".md"},
		"inspection": map[string]any{"schema_version": n, "status": "complete",
			"repository": map[string]any{"state": "observed", "object_format": format, "head_state": "present", "head": head,
				"branch_state": "attached",
				"counts":       map[string]any{"staged": n, "unstaged": n, "untracked": n, "conflicted": z, "linked_worktrees": z, "submodules": z},
				"checkpoint":   map[string]any{"state": "not_requested", "oid": nil, "commits_ahead": nil}},
			"sources":          src,
			"coordination":     map[string]any{"mode": "none", "state": "absent", "number": nil, "issue_state": nil, "updated_at": nil, "source_url": nil},
			"remote_freshness": "unknown", "runtime_state": "unknown", "process_ownership": "unknown", "reservation_ownership": "unknown"},
	}
}

const ctxHelp = "Usage:\n  baw context --repo PATH --role ROLE [--json]\n\nROLE is lead, worker or reviewer.\n" +
	"Paths are relative to the top level of the Git worktree containing --repo.\n" +
	"Read AGENTS.md, workflow/protocol.md and only the selected role.\n" +
	"Reconcile the assigned ticket, approved authority and run evidence before action.\n" +
	"This packet grants no assignment, readiness or authority and starts no agent.\n"

func TestContextBinaryJourneys(t *testing.T) {
	if *bawBinary == "" || *journeyDir == "" {
		t.Skip("context binary journeys need -baw-binary and -journey-dir")
	}
	if err := os.Mkdir(*journeyDir, 0o700); err != nil {
		t.Fatalf("journey dir must be new: %v", err)
	}
	home := filepath.Join(*journeyDir, "home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	git := func(dir string, args ...string) string {
		t.Helper()
		out, err := tf.Git(home, dir, args...)
		must(err)
		return out
	}
	// A fake gh first on PATH records any call; inherited Git variables are
	// decoys the binary must ignore.
	fakeDir := filepath.Join(*journeyDir, "fake-gh")
	must(os.Mkdir(fakeDir, 0o700))
	ghCalls := filepath.Join(*journeyDir, "fake-gh-called")
	must(os.WriteFile(filepath.Join(fakeDir, "gh"), []byte("#!/bin/sh\ntouch '"+ghCalls+"'\n"), 0o700))
	env := append(os.Environ(), "PATH="+fakeDir+":"+os.Getenv("PATH"), "GIT_DIR=/nonexistent-decoy/.git",
		"GIT_WORK_TREE=/nonexistent-decoy", "GIT_INDEX_FILE=/nonexistent-decoy/index")

	// Every binary call, including the unsafe-source ones, is killed and
	// always waited for within 10 seconds.
	var summary strings.Builder
	run := func(name string, wantCode int, args ...string) (string, string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, *bawBinary, args...)
		cmd.Env = env
		cmd.WaitDelay = time.Second
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		start := time.Now()
		err := cmd.Run() // waits for the child also when it is killed at the deadline
		if ctx.Err() != nil {
			t.Fatalf("%s: not finished within 10s; killed and joined after %v (%v)", name, time.Since(start), err)
		}
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		summary.WriteString("== " + name + " exit=" + itoa(code) + " elapsed_ms=" + itoa(int(time.Since(start).Milliseconds())) +
			"\n-- argv: baw " + strings.Join(args, " ") + "\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
		if code != wantCode {
			t.Errorf("%s: exit %d want %d", name, code, wantCode)
		}
		return out.String(), errb.String()
	}

	combos := 0
	for _, format := range []string{"sha1", "sha256"} {
		repo := filepath.Join(*journeyDir, format+"-fixture")
		if err := tf.Init(home, repo, format); err != nil {
			t.Fatalf("%s repository required, not skipped: %v", format, err)
		}
		for _, p := range append(ctxSources("lead"), "workflow/roles/worker.md", "workflow/roles/reviewer.md", "x.txt") {
			must(tf.Write(filepath.Join(repo, p), "dummy maintained source\n"))
		}
		git(repo, "add", "-A")
		git(repo, "commit", "-q", "-m", "base")
		head, err := tf.OID(home, repo, "HEAD")
		must(err)
		if len(head) != map[string]int{"sha1": 40, "sha256": 64}[format] {
			t.Fatalf("%s HEAD %q", format, head)
		}
		must(tf.Write(filepath.Join(repo, "y.txt"), "staged\n"))
		git(repo, "add", "y.txt")
		must(tf.Write(filepath.Join(repo, "x.txt"), "edited\n"))
		must(tf.Write(filepath.Join(repo, "z.txt"), "untracked\n"))
		summary.WriteString(format + " fixture HEAD (from fixture setup): " + head + "\n")
		for _, role := range []string{"lead", "worker", "reviewer"} {
			out, errs := run(format+" "+role+" terminal", 0, "context", "--repo", repo, "--role", role)
			if out != ctxText(role, format, head) || errs != "" {
				t.Errorf("%s %s terminal mismatch", format, role)
			}
			combos++
			out, errs = run(format+" "+role+" json", 0, "context", "--repo", repo, "--role", role, "--json")
			dec := json.NewDecoder(strings.NewReader(out))
			dec.UseNumber()
			var got map[string]any
			if err := dec.Decode(&got); err != nil || !strings.HasSuffix(out, "}\n") || strings.Count(out, "\n") != 1 ||
				errs != "" || !reflect.DeepEqual(got, ctxJSON(role, format, head)) {
				t.Errorf("%s %s json mismatch: %v", format, role, err)
			}
			combos++
			sub, _ := run(format+" "+role+" terminal from subdirectory", 0, "context", "--repo", filepath.Join(repo, "docs", "design"), "--role", role)
			if sub != ctxText(role, format, head) {
				t.Errorf("%s %s subdirectory output differs", format, role)
			}
		}
	}
	summary.WriteString("success combinations (3 roles x 2 formats x 2 outputs): " + itoa(combos) + "\n")
	if combos != 12 {
		t.Errorf("ran %d success combinations, want 12", combos)
	}

	// Negatives.
	sha1 := filepath.Join(*journeyDir, "sha1-fixture")
	if out, errs := run("context help", 0, "context", "--help"); out != ctxHelp || errs != "" {
		t.Errorf("help mismatch")
	}
	for _, args := range [][]string{{"context", "--repo", sha1, "--role", "helper"}, {"context", "--repo", sha1, "--role", "Lead"},
		{"context", "--repo", sha1}, {"context", "--repo", sha1, "--role", "lead", "--checkpoint", strings.Repeat("a", 40)}} {
		if out, errs := run("invalid usage", 2, args...); out != "" || errs != "baw: invalid_usage\n" {
			t.Errorf("usage %v: %q %q", args, out, errs)
		}
	}
	if out, errs := run("not a repository", 1, "context", "--repo", home, "--role", "worker"); out != "" || errs != "baw: repository_unavailable\n" {
		t.Errorf("repository_unavailable: %q %q", out, errs)
	}
	sym := filepath.Join(*journeyDir, "symlink-fixture")
	must(tf.Init(home, sym, "sha1"))
	must(tf.Write(filepath.Join(sym, "elsewhere.md"), "dummy\n"))
	must(os.MkdirAll(filepath.Join(sym, "workflow/roles"), 0o755))
	must(os.Symlink(filepath.Join(sym, "elsewhere.md"), filepath.Join(sym, "workflow/roles/reviewer.md")))
	if out, errs := run("selected role symlink", 1, "context", "--repo", sym, "--role", "reviewer"); out != "" || errs != "baw: source_symlink\n" {
		t.Errorf("source_symlink: %q %q", out, errs)
	}
	out, _ := run("unselected role symlink, unborn, documents missing", 0, "context", "--repo", sym, "--role", "worker", "--json")
	if !strings.Contains(out, `"head_state":"unborn","head":null`) || strings.Contains(out, "reviewer") ||
		!strings.Contains(out, `{"path":"workflow/roles/worker.md","presence":"absent","worktree_state":"absent","head_ref":null,"worktree_ref":null}`) {
		t.Errorf("unborn/unselected journey mismatch")
	}

	ctxDocExample(t, home, run, &summary)

	if _, err := os.Stat(ghCalls); err == nil {
		t.Errorf("gh was invoked")
	}
	must(os.WriteFile(filepath.Join(*journeyDir, "summary.txt"), []byte(summary.String()), 0o600))
}

// ctxDocExample performs the docs/operator/context.md example, checks that
// the documented output is the binary's actual output, and follows each
// reference from the worktree top level.
func ctxDocExample(t *testing.T, home string, run func(string, int, ...string) (string, string), summary *strings.Builder) {
	t.Helper()
	work := filepath.Join(*journeyDir, "doc-example")
	repo := filepath.Join(work, "repo")
	if err := tf.Init(home, repo, "sha1"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"AGENTS.md", "workflow/protocol.md", "workflow/roles/worker.md"} {
		if err := tf.Write(filepath.Join(repo, p), "dummy\n"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := tf.Git(home, repo, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := tf.Git(home, repo, "commit", "-q", "-m", "dummy"); err != nil {
		t.Fatal(err)
	}
	head, err := tf.OID(home, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := run("documentation example", 0, "context", "--repo", filepath.Join(repo, "docs"), "--role", "worker")
	doc, err := os.ReadFile(filepath.Join("..", "..", "docs", "operator", "context.md"))
	if err != nil {
		t.Fatal(err)
	}
	documented := "```text\n" + strings.ReplaceAll(out, head, "<HEAD>") + "```\n"
	if !strings.Contains(string(doc), documented) {
		t.Errorf("docs/operator/context.md does not show the actual output:\n%s", documented)
	}
	// Follow the references: worktree refs and HEAD refs both resolve from
	// the top level, not from the --repo subdirectory.
	top, err := tf.Git(home, filepath.Join(repo, "docs"), "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	followed := 0
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, "Source: ")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		headRef, _ := strings.CutPrefix(f[3], "head_ref=")
		wtRef, _ := strings.CutPrefix(f[4], "worktree_ref=")
		if wtRef != "-" {
			p, _ := strings.CutPrefix(wtRef, "worktree:")
			if b, err := os.ReadFile(filepath.Join(top, p)); err != nil || string(b) != "dummy\n" {
				t.Errorf("worktree ref %s: %v", wtRef, err)
			}
			followed++
		}
		if headRef != "-" {
			spec, _ := strings.CutPrefix(headRef, "git:")
			if b, err := tf.Git(home, top, "cat-file", "-p", spec); err != nil || b != "dummy" {
				t.Errorf("head ref %s: %v", headRef, err)
			}
			followed++
		}
	}
	summary.WriteString("-- documentation example references followed from the top level: " + itoa(followed) + "\n")
	if followed != 6 {
		t.Errorf("followed %d references, want 6", followed)
	}
}
