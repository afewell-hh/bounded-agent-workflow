package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// replaceSame replaces path with a new file of identical bytes and mode, so
// only its identity (inode) changes.
func replaceSame(f *fx, path string) {
	f.t.Helper()
	b, err := os.ReadFile(path)
	f.ok(err)
	fi, err := os.Lstat(path)
	f.ok(err)
	tmp := path + ".replace-tmp"
	f.ok(os.WriteFile(tmp, b, fi.Mode().Perm()))
	f.ok(os.Chmod(tmp, fi.Mode().Perm()))
	f.ok(os.Rename(tmp, path))
}

func appendFile(f *fx, path, text string) {
	f.t.Helper()
	b, err := os.ReadFile(path)
	f.ok(err)
	fi, err := os.Lstat(path)
	f.ok(err)
	f.ok(os.WriteFile(path, append(b, text...), fi.Mode().Perm()))
}

// dropProtocol makes workflow/protocol.md an ordinary tracked file that is
// neither an instruction nor allowed.
func dropProtocol(f *fx) {
	f.task["instructions"] = f.task["instructions"].([]any)[:2]
	f.writeInputs()
}

// Persistent changes that Git status hides or never reports, Git control
// changes and identity/mode drift of non-allowed entries all make the
// attempt unusable, with the allowed edit preserved and no manifest. An
// ordinary allowed edit (in place or by replacement) still completes and an
// ordinary extra file is still rejected.
func TestWorkerFullCaptureAndControl(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fx)
		edit  func(f *fx, top string)
		want  string
	}{
		{"allowed edit control", nil, func(*fx, string) {}, OutcomeCompleted},
		{"allowed file replaced by rename", nil, func(f *fx, top string) {
			replaceSame(f, filepath.Join(top, "target.txt"))
		}, OutcomeCompleted},
		{"extra file control", nil, func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, "extra.txt"), []byte("extra"), 0o644))
		}, OutcomeUnusable},
		{"assume-unchanged hides non-allowed bytes", dropProtocol, func(f *fx, top string) {
			gitIn(f, "update-index", "--assume-unchanged", "workflow/protocol.md")
			f.ok(os.WriteFile(filepath.Join(top, "workflow", "protocol.md"), []byte("hidden\n"), 0o644))
		}, OutcomeUnusable},
		{"skip-worktree hides non-allowed bytes", dropProtocol, func(f *fx, top string) {
			gitIn(f, "update-index", "--skip-worktree", "workflow/protocol.md")
			f.ok(os.WriteFile(filepath.Join(top, "workflow", "protocol.md"), []byte("hidden\n"), 0o644))
		}, OutcomeUnusable},
		{"index flag alone", nil, func(f *fx, _ string) {
			gitIn(f, "update-index", "--assume-unchanged", "AGENTS.md")
		}, OutcomeUnusable},
		{"git config appended", nil, func(f *fx, top string) {
			appendFile(f, filepath.Join(top, ".git", "config"), "\n# unexpected control mutation\n")
		}, OutcomeUnusable},
		{"info exclude replaced", nil, func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, ".git", "info", "exclude"), []byte("invisible\n"), 0o644))
		}, OutcomeUnusable},
		{"hook added", nil, func(f *fx, top string) {
			f.ok(os.MkdirAll(filepath.Join(top, ".git", "hooks"), 0o755))
			f.ok(os.WriteFile(filepath.Join(top, ".git", "hooks", "pre-commit"), []byte("#!/bin/sh\n"), 0o755))
		}, OutcomeUnusable},
		{"instruction mode drift", nil, func(f *fx, top string) {
			f.ok(os.Chmod(filepath.Join(top, "workflow", "protocol.md"), 0o600))
		}, OutcomeUnusable},
		{"instruction byte-identical replacement", nil, func(f *fx, top string) {
			replaceSame(f, filepath.Join(top, "AGENTS.md"))
		}, OutcomeUnusable},
		{"tracked byte-identical replacement", dropProtocol, func(f *fx, top string) {
			replaceSame(f, filepath.Join(top, "workflow", "protocol.md"))
		}, OutcomeUnusable},
		{"tracked mode drift hidden from status", dropProtocol, func(f *fx, top string) {
			f.ok(os.Chmod(filepath.Join(top, "workflow", "protocol.md"), 0o664))
		}, OutcomeUnusable},
		{"empty directory created", nil, func(f *fx, top string) {
			f.ok(os.Mkdir(filepath.Join(top, "newdir"), 0o755))
		}, OutcomeUnusable},
		{"symbolic link created", nil, func(f *fx, top string) {
			f.ok(os.Symlink("target.txt", filepath.Join(top, "link")))
		}, OutcomeUnusable},
		{"directory mode drift", nil, func(f *fx, top string) {
			f.ok(os.Chmod(filepath.Join(top, "workflow"), 0o700))
		}, OutcomeUnusable},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		if c.setup != nil {
			c.setup(f)
		}
		base, edit := f.edit, c.edit
		f.edit = func(top string) {
			base(top)
			edit(f, top)
		}
		out, completed, err := f.run(context.Background(), true)
		if err != nil || f.runs != 1 {
			t.Fatalf("%s: %v runs=%d", c.name, err, f.runs)
		}
		r := decodeResult(t, out)
		if r.Outcome != c.want || completed != (c.want == OutcomeCompleted) {
			t.Errorf("%s: outcome %s", c.name, r.Outcome)
		}
		if c.want == OutcomeUnusable && (r.Repository.AfterHead != nil || r.Repository.ChangeManifestSHA256 != nil) {
			t.Errorf("%s: unusable attempt claimed a change manifest", c.name)
		}
		if b, err := os.ReadFile(filepath.Join(f.repo, "target.txt")); err != nil || string(b) != "marker: kestrel\n" {
			t.Errorf("%s: allowed edit not preserved", c.name)
		}
	}
}

// Pre-existing index flags and hidden differences are rejected before
// acquisition, independently of Git status; nothing starts.
func TestWorkerRejectsHiddenStateBeforeStart(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fx)
	}{
		{"assume-unchanged flag", func(f *fx) { gitIn(f, "update-index", "--assume-unchanged", "target.txt") }},
		{"skip-worktree flag", func(f *fx) { gitIn(f, "update-index", "--skip-worktree", "target.txt") }},
		{"hidden tracked edit", func(f *fx) {
			gitIn(f, "update-index", "--assume-unchanged", "target.txt")
			f.ok(os.WriteFile(filepath.Join(f.repo, "target.txt"), []byte("hidden\n"), 0o644))
		}},
		{"hidden tracked deletion", func(f *fx) {
			gitIn(f, "update-index", "--skip-worktree", "target.txt")
			f.ok(os.Remove(filepath.Join(f.repo, "target.txt")))
		}},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		c.setup(f)
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeNotClean) || f.runs != 0 {
			t.Errorf("%s: %v runs=%d", c.name, err, f.runs)
		}
		if _, err := os.Lstat(f.attempt()); err == nil {
			t.Errorf("%s: attempt created", c.name)
		}
	}
}

// The per-file bound applies to every tracked file at launch and to every
// file after return, exactly at 1 MiB, without truncation.
func TestWorkerPerFileBound(t *testing.T) {
	const mib = 1 << 20
	for _, n := range []int{mib, mib + 1} {
		f := newFxWith(t, "sha1", map[string]string{"big.bin": strings.Repeat("b", n)}, nil)
		_, completed, err := f.run(context.Background(), true)
		if n == mib && (err != nil || !completed) {
			t.Errorf("tracked %d bytes: %v %v", n, err, completed)
		}
		if n > mib && (!IsCode(err, CodeUnsafeLayout) || f.runs != 0) {
			t.Errorf("tracked %d bytes: %v runs=%d", n, err, f.runs)
		}
	}
	for _, n := range []int{mib, mib + 1} {
		f := newFx(t, "sha1")
		f.task["allowed_changes"] = []string{"target.txt", "new.bin"}
		f.writeInputs()
		size := n
		base := f.edit
		f.edit = func(top string) {
			base(top)
			f.ok(os.WriteFile(filepath.Join(top, "new.bin"), bytes.Repeat([]byte("n"), size), 0o644))
		}
		out, completed, err := f.run(context.Background(), true)
		if err != nil {
			t.Fatalf("allowed %d bytes: %v", n, err)
		}
		want := n == mib
		if completed != want || (decodeResult(t, out).Repository.ChangeManifestSHA256 != nil) != want {
			t.Errorf("allowed %d bytes: completed=%v %s", n, completed, out)
		}
	}
}

// Instruction identity, safety and size are checked as one group; nothing
// starts on any failure and an exactly 64 KiB instruction is delivered.
func TestWorkerInstructionGroup(t *testing.T) {
	const limit = 65536
	extra := map[string]string{"docs/max.md": strings.Repeat("m", limit), "docs/over.md": strings.Repeat("o", limit+1)}
	links := map[string]string{"docs/link.md": "max.md"}
	newGroupFx := func() *fx { return newFxWith(t, "sha1", extra, links) }

	f := newGroupFx()
	ins := f.task["instructions"].([]any)
	f.task["instructions"] = append(ins, f.instruction("docs/max.md"))
	f.writeInputs()
	if _, completed, err := f.run(context.Background(), true); err != nil || !completed ||
		!bytes.Contains(f.stdin, []byte("bytes=65536 ===\n"+strings.Repeat("m", limit)+"\n=== END INSTRUCTION 4/4 ===\n")) {
		t.Fatalf("64 KiB instruction: %v %v", err, completed)
	}

	linkOID := func(f *fx) map[string]any {
		oid, err := testfixture.OID(f.gitHome, f.repo, "HEAD:docs/link.md")
		f.ok(err)
		return map[string]any{"path": "docs/link.md", "blob_oid": oid, "sha256": hexSum([]byte("max.md"))}
	}
	cases := []struct {
		name  string
		entry func(f *fx) map[string]any
	}{
		{"oversize", func(f *fx) map[string]any { return f.instruction("docs/over.md") }},
		{"missing", func(f *fx) map[string]any {
			e := f.instruction("docs/max.md")
			e["path"] = "docs/missing.md"
			return e
		}},
		{"content hash drift", func(f *fx) map[string]any {
			e := f.instruction("docs/max.md")
			e["sha256"] = strings.Repeat("0", 64)
			return e
		}},
		{"blob drift", func(f *fx) map[string]any {
			e := f.instruction("docs/max.md")
			e["blob_oid"] = f.instruction("target.txt")["blob_oid"]
			return e
		}},
		{"symbolic link in tree", linkOID},
	}
	for _, c := range cases {
		f := newGroupFx()
		ins := f.task["instructions"].([]any)
		f.task["instructions"] = append(ins, c.entry(f))
		f.writeInputs()
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeInstructionsUnavailable) || f.runs != 0 {
			t.Errorf("%s: %v runs=%d", c.name, err, f.runs)
		}
		if _, err := os.Lstat(f.attempt()); err == nil {
			t.Errorf("%s: attempt created", c.name)
		}
	}
}

// The packet bound includes every framing byte: a packet of exactly
// 262144 bytes is accepted and one more byte is rejected, although the
// bodies and task alone are still below the bound.
func TestPacketBoundIncludesFraming(t *testing.T) {
	in := PacketInput{RunID: testRunID, TaskInputSHA256: strings.Repeat("a", 64), BaseCommit: strings.Repeat("b", 40),
		ScopeSHA256: testScope, ProfileSHA256: strings.Repeat("c", 64), InstructionsSHA256: strings.Repeat("d", 64),
		Task: "task", AllowedChanges: []string{"target.txt"}}
	for i := 0; i < 4; i++ {
		in.Instructions = append(in.Instructions, Instruction{Path: "doc" + strconv.Itoa(i) + ".md",
			BlobOID: strings.Repeat("e", 40), SHA256: strings.Repeat("f", 64)})
	}
	build := func(last int) ([]byte, error) {
		in.Bodies = [][]byte{bytes.Repeat([]byte("x"), 65536), bytes.Repeat([]byte("x"), 65536),
			bytes.Repeat([]byte("x"), 65536), bytes.Repeat([]byte("x"), last)}
		return AssemblePacket(in)
	}
	p, err := build(10000)
	if err != nil {
		t.Fatal(err)
	}
	framing := len(p) - 3*65536 - 10000
	last := 262144 - 3*65536 - framing
	if framing <= 0 || last < 10000 || last+1 > 99999 {
		t.Fatalf("framing %d last %d", framing, last)
	}
	if p, err := build(last); err != nil || len(p) != 262144 {
		t.Fatalf("exact bound: %d %v", len(p), err)
	}
	if 3*65536+last+1+len(in.Task) >= 262144 {
		t.Fatal("over-bound case does not depend on framing")
	}
	if _, err := build(last + 1); !IsCode(err, CodePacketUnavailable) {
		t.Fatalf("one byte over: %v", err)
	}
}

// A composed packet over the bound starts nothing and creates no attempt.
func TestWorkerPacketOverBound(t *testing.T) {
	extra := map[string]string{}
	for i := 0; i < 4; i++ {
		extra["docs/d"+strconv.Itoa(i)+".md"] = strings.Repeat(strconv.Itoa(i), 65536)
	}
	f := newFxWith(t, "sha1", extra, nil)
	ins := f.task["instructions"].([]any)
	for i := 0; i < 4; i++ {
		ins = append(ins, f.instruction("docs/d"+strconv.Itoa(i)+".md"))
	}
	f.task["instructions"] = ins
	f.writeInputs()
	if _, _, err := f.run(context.Background(), true); !IsCode(err, CodePacketUnavailable) || f.runs != 0 {
		t.Fatalf("%v runs=%d", err, f.runs)
	}
	if _, err := os.Lstat(f.attempt()); err == nil {
		t.Fatal("attempt created")
	}
}

// State directory drift after durable intent and before start starts
// nothing and publishes no result; drift while the worker runs publishes no
// result, preserves the edits and is never replayed.
func TestWorkerStateIdentity(t *testing.T) {
	cases := []struct {
		name  string
		drift func(f *fx)
	}{
		{"state root mode", func(f *fx) { f.ok(os.Chmod(f.stateDir, 0o755)) }},
		{"namespace mode", func(f *fx) { f.ok(os.Chmod(filepath.Join(f.stateDir, Namespace), 0o750)) }},
		{"attempt mode", func(f *fx) { f.ok(os.Chmod(f.attempt(), 0o750)) }},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		f.fault = func(stage string) error {
			if stage == "intent.json-dir-sync" {
				c.drift(f)
			}
			return nil
		}
		out, completed, err := f.run(context.Background(), true)
		if !IsCode(err, CodeUncertain) || completed || f.runs != 0 || out != "" {
			t.Errorf("before start, %s: %v runs=%d out=%q", c.name, err, f.runs, out)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err == nil {
			t.Errorf("before start, %s: result published", c.name)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), "intent.json")); err != nil {
			t.Errorf("before start, %s: intent not retained: %v", c.name, err)
		}
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		base := f.edit
		f.edit = func(top string) {
			base(top)
			c.drift(f)
		}
		out, completed, err := f.run(context.Background(), true)
		if !IsCode(err, CodeUncertain) || completed || f.runs != 1 || out != "" {
			t.Errorf("during worker, %s: %v runs=%d out=%q", c.name, err, f.runs, out)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err == nil {
			t.Errorf("during worker, %s: result published", c.name)
		}
		if b, err := os.ReadFile(filepath.Join(f.repo, "target.txt")); err != nil || string(b) != "marker: kestrel\n" {
			t.Errorf("during worker, %s: edit not preserved", c.name)
		}
		if _, _, err := f.run(context.Background(), true); err == nil || f.runs != 1 {
			t.Errorf("during worker, %s: replay %v runs=%d", c.name, err, f.runs)
		}
	}
}

// Input files are bound by identity and mode as well as bytes: a
// byte-identical substitute or a mode change after intent is input_changed
// with nothing started; a substitute while the worker runs is unusable.
func TestWorkerInputIdentity(t *testing.T) {
	inputs := []string{"plan.json", "task.json", "profile.json"}
	for _, name := range inputs {
		for _, kind := range []string{"replace", "mode"} {
			f := newFx(t, "sha1")
			p := filepath.Join(f.priv, name)
			f.fault = func(stage string) error {
				if stage == "intent.json-dir-sync" {
					if kind == "replace" {
						replaceSame(f, p)
					} else {
						f.ok(os.Chmod(p, 0o400))
					}
				}
				return nil
			}
			out, completed, err := f.run(context.Background(), true)
			if !IsCode(err, CodeInputChanged) || completed || f.runs != 0 {
				t.Errorf("%s %s before start: %v runs=%d", kind, name, err, f.runs)
				continue
			}
			if r := decodeResult(t, out); r.Outcome != OutcomeUnusable || r.Worker.State != StateNotStarted {
				t.Errorf("%s %s before start: %s", kind, name, out)
			}
		}
		f := newFx(t, "sha1")
		p := filepath.Join(f.priv, name)
		base := f.edit
		f.edit = func(top string) {
			base(top)
			replaceSame(f, p)
		}
		out, completed, err := f.run(context.Background(), true)
		if err != nil || completed || f.runs != 1 {
			t.Errorf("replace %s during worker: %v %v runs=%d", name, err, completed, f.runs)
			continue
		}
		if r := decodeResult(t, out); r.Outcome != OutcomeUnusable || r.Repository.ChangeManifestSHA256 != nil {
			t.Errorf("replace %s during worker: %s", name, out)
		}
	}
}

// The worktree and nested source bases must be safe before acquisition;
// unsafe or changed bases after return make the attempt unusable.
func TestWorkerSourceBases(t *testing.T) {
	before := []struct {
		name string
		path func(f *fx) string
	}{
		{"worktree", func(f *fx) string { return f.repo }},
		{"nested instruction base", func(f *fx) string { return filepath.Join(f.repo, "workflow") }},
	}
	for _, c := range before {
		f := newFx(t, "sha1")
		f.ok(os.Chmod(c.path(f), 0o777))
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeSourceUnavailable) || f.runs != 0 {
			t.Errorf("unsafe %s: %v runs=%d", c.name, err, f.runs)
		}
		if _, err := os.Lstat(f.attempt()); err == nil {
			t.Errorf("unsafe %s: attempt created", c.name)
		}
	}
	during := []struct {
		name string
		mode os.FileMode
	}{{"worktree made writable", 0o777}, {"worktree safe mode change", 0o750}}
	for _, c := range during {
		f := newFx(t, "sha1")
		base, mode := f.edit, c.mode
		f.edit = func(top string) {
			base(top)
			f.ok(os.Chmod(top, mode))
		}
		out, completed, err := f.run(context.Background(), true)
		if err != nil || completed || f.runs != 1 || decodeResult(t, out).Outcome != OutcomeUnusable {
			t.Errorf("%s: %v %v runs=%d", c.name, err, completed, f.runs)
		}
	}
}

// A supported linked worktree completes; a change to its common Git
// configuration while the worker runs makes the attempt unusable.
func TestWorkerLinkedWorktree(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		f := newFx(t, "sha1")
		main := f.repo
		linked := filepath.Join(f.root, "linked")
		gitIn(f, "worktree", "add", "-q", "--detach", linked, "HEAD")
		f.repo = linked
		f.writeInputs()
		base := f.edit
		f.edit = func(top string) {
			base(top)
			if mutate {
				appendFile(f, filepath.Join(main, ".git", "config"), "\n# unexpected common control mutation\n")
			}
		}
		out, completed, err := f.run(context.Background(), true)
		if err != nil || f.runs != 1 || completed == mutate {
			t.Fatalf("mutate=%v: %v %v runs=%d %s", mutate, err, completed, f.runs, out)
		}
		if f.spec.Dir != linked {
			t.Fatalf("worker ran in %q", f.spec.Dir)
		}
	}
}
