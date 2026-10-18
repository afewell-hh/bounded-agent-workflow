package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// expectedPacket is the hand-written packet framing for the fixture.
func expectedPacket(f *fx) string {
	ins := f.task["instructions"].([]any)
	prof := f.profile()
	var b strings.Builder
	b.WriteString("BAW worker packet v1\n")
	b.WriteString("Assignment: worker; only this task; no tests/external actions\n")
	b.WriteString("Run: " + testRunID + "\n")
	b.WriteString("Task input SHA-256: " + f.taskSHA + "\n")
	b.WriteString("Base commit: " + f.head + "\n")
	b.WriteString("Scope SHA-256: " + testScope + "\n")
	b.WriteString("Profile: claude-2.1.285-supervised-v1 sha256=" + prof["approved_profile_sha256"].(string) + "\n")
	var manifest []map[string]any
	for _, v := range ins {
		manifest = append(manifest, v.(map[string]any))
	}
	mb, _ := json.Marshal(manifest) // keys sorted: blob_oid, path, sha256
	b.WriteString("Instruction manifest SHA-256: " + hexSum(mb) + "\n")
	b.WriteString("Instructions: the 3 documents below are the complete project instructions for this task, in order. " +
		"No other instruction file is loaded automatically. Do not edit any of them.\n")
	bodies := []string{"@AGENTS.md\n", ruleText, "Protocol body.\n"}
	for i, v := range manifest {
		k := string(rune('1' + i))
		b.WriteString("=== BEGIN INSTRUCTION " + k + "/3 path=" + v["path"].(string) + " blob=" + v["blob_oid"].(string) +
			" sha256=" + v["sha256"].(string) + " bytes=" + itoa(len(bodies[i])) + " ===\n")
		b.WriteString(bodies[i])
		b.WriteString("\n=== END INSTRUCTION " + k + "/3 ===\n")
	}
	task := "Record the marker required by the project rule."
	b.WriteString("=== BEGIN TASK bytes=" + itoa(len(task)) + " ===\n" + task + "\n=== END TASK ===\n")
	b.WriteString("Allowed changes: only these literal repository-relative files may be created or modified; change nothing else.\n")
	b.WriteString("- target.txt\n")
	b.WriteString("Do not run tests, commands or external actions. Do not commit.\n")
	b.WriteString("Final report: your final message must be exactly one JSON object and nothing else (no prose, no code fence):\n")
	b.WriteString(`{"schema_version":1,"run_id":"` + testRunID + `","task_input_sha256":"` + f.taskSHA +
		`","status":"STATUS","reason":"REASON"}` + "\n")
	b.WriteString("STATUS is completed or not_completed. completed requires REASON none; " +
		"not_completed requires REASON blocked, needs_clarification or failed.\n")
	return b.String()
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func attemptNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), ".pending-") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// A completed attempt supplies the exact framed instructions on stdin (never
// argv), uses the frozen argv/env, records every private artifact and binds
// the receipt to the inputs and the allowed change.
func TestWorkerCompletedExactPacket(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		f := newFx(t, format)
		out, completed, err := f.run(context.Background(), true)
		if err != nil || !completed || f.runs != 1 {
			t.Fatalf("%s: %v %v runs=%d", format, err, completed, f.runs)
		}
		if string(f.stdin) != expectedPacket(f) {
			t.Fatalf("%s: packet\n%s\nwant\n%s", format, f.stdin, expectedPacket(f))
		}
		if strings.Join(f.spec.Args, "\x00") != strings.Join(claude.Args(f.repo), "\x00") || f.spec.Path != f.exe ||
			f.spec.Dir != f.repo || f.spec.Timeout != Deadline || f.spec.StdoutCap != 8<<20 || f.spec.StderrCap != 1<<20 {
			t.Fatalf("%s: spec %+v", format, f.spec)
		}
		for _, a := range f.spec.Args {
			if strings.Contains(a, "marker") || strings.Contains(a, "Project rule") {
				t.Fatalf("task or instruction text in argv: %q", a)
			}
		}
		wantEnv := claude.Env(claude.Profile{UserHome: f.userHome, UserName: "fixture", Shell: "/bin/zsh"},
			filepath.Join(f.attempt(), "worker", "tmp"))
		if strings.Join(f.spec.Env, "\n") != strings.Join(wantEnv, "\n") {
			t.Fatalf("%s: env %q", format, f.spec.Env)
		}
		r := decodeResult(t, out)
		if r.Outcome != OutcomeCompleted || r.Worker.State != StateExited || *r.Worker.ExitCode != 0 ||
			r.InputBinding.TaskInputSHA256 != f.taskSHA || r.InputBinding.AssembledPacketSHA256 != hexSum(f.stdin) ||
			r.InputBinding.ExecutableSHA256 != hexSum(f.exeBytes) || r.Repository.AfterHead == nil ||
			*r.Repository.AfterHead != f.head || r.InstructionCount != 3 || r.InstructionDelivery != "embedded_full_documents" ||
			r.SourceObservation.Coverage != "complete_enumerated_metadata" || !r.StreamSummary.InitVerified ||
			!r.StreamSummary.TerminalVerified || r.StreamSummary.MemoryObservation != claude.MemoryEmptyReported ||
			*r.StreamSummary.PluginCount != 1 || r.Authority != "not_evaluated" || r.Readiness != "not_evaluated" {
			t.Fatalf("%s: %s", format, out)
		}
		want := []string{"changes.json", "intent.json", "packet.txt", "result.json", "source-manifest.json", "supplied-inputs.json", "worker"}
		if got := attemptNames(t, f.attempt()); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: attempt files %v", format, got)
		}
		for _, name := range want[:6] {
			fi, err := os.Lstat(filepath.Join(f.attempt(), name))
			if err != nil || fi.Mode().Perm() != 0o600 {
				t.Fatalf("%s: %s mode %v %v", format, name, fi, err) // fi may be nil on error
			}
		}
		pkt, _ := os.ReadFile(filepath.Join(f.attempt(), "packet.txt"))
		changes, _ := os.ReadFile(filepath.Join(f.attempt(), "changes.json"))
		intent, _ := os.ReadFile(filepath.Join(f.attempt(), "intent.json"))
		manifest, _ := os.ReadFile(filepath.Join(f.attempt(), "source-manifest.json"))
		var in Intent
		if json.Unmarshal(intent, &in) != nil || in.AssembledPacketSHA256 != hexSum(pkt) || string(pkt) != string(f.stdin) ||
			in.SourceManifestSHA256 != hexSum(manifest) || r.SourceObservation.ManifestSHA256 != hexSum(manifest) ||
			*r.Repository.ChangeManifestSHA256 != hexSum(changes) {
			t.Fatalf("%s: bindings", format)
		}
		if !strings.Contains(string(changes), `"path":"target.txt","state":"modified"`) {
			t.Fatalf("%s: changes %s", format, changes)
		}
		for _, secret := range []string{"marker", f.repo, f.userHome, "Project rule"} {
			if strings.Contains(out, secret) {
				t.Fatalf("%s: output leaks %q", format, secret)
			}
		}
		// Text output carries only the fixed summary lines.
		g := newFx(t, format)
		text, _, err := g.run(context.Background(), false)
		if err != nil || !strings.HasPrefix(text, "BAW worker observations\nRun: "+testRunID+"\n") ||
			!strings.Contains(text, "Outcome: worker_completed_unverified\n") || strings.Contains(text, g.repo) {
			t.Fatalf("%s: text %q %v", format, text, err)
		}
	}
}

// A truthful native noncompletion is recorded as worker_not_completed, not
// usable completion, even with a usable exit 0.
func TestWorkerNotCompleted(t *testing.T) {
	f := newFx(t, "sha1")
	f.stream = notCompletedStream
	out, completed, err := f.run(context.Background(), true)
	if err != nil || completed {
		t.Fatalf("%v %v", err, completed)
	}
	if r := decodeResult(t, out); r.Outcome != OutcomeNotCompleted || !r.StreamSummary.TerminalVerified {
		t.Fatal(out)
	}
}

func gitIn(f *fx, args ...string) {
	f.t.Helper()
	_, err := testfixture.Git(f.gitHome, f.repo, args...)
	f.ok(err)
}

// Every rejection before acquisition starts nothing and creates no attempt.
func TestWorkerRejectsBeforeStart(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fx)
		want  Code
	}{
		{"untracked file", func(f *fx) { f.ok(os.WriteFile(filepath.Join(f.repo, "new.txt"), nil, 0o644)) }, CodeNotClean},
		{"ignored file", func(f *fx) {
			f.ok(os.WriteFile(filepath.Join(f.repo, ".git", "info", "exclude"), []byte("ign.txt\n"), 0o644))
			f.ok(os.WriteFile(filepath.Join(f.repo, "ign.txt"), nil, 0o644))
		}, CodeNotClean},
		{"unstaged edit", func(f *fx) { f.ok(os.WriteFile(filepath.Join(f.repo, "target.txt"), []byte("x"), 0o644)) }, CodeNotClean},
		{"head moved", func(f *fx) { gitIn(f, "commit", "-q", "--allow-empty", "-m", "later") }, CodeCheckpointMismatch},
		{"instruction hash drift", func(f *fx) {
			f.task["instructions"].([]any)[1].(map[string]any)["sha256"] = strings.Repeat("0", 64)
			f.writeInputs()
		}, CodeInstructionsUnavailable},
		{"instruction blob drift", func(f *fx) {
			f.task["instructions"].([]any)[1] = f.instruction("target.txt")
			f.task["instructions"].([]any)[1].(map[string]any)["path"] = "AGENTS.md"
			f.writeInputs()
		}, CodeInstructionsUnavailable},
		{"instruction untracked", func(f *fx) {
			ins := f.task["instructions"].([]any)
			ins[2].(map[string]any)["path"] = "workflow/missing.md"
			f.writeInputs()
		}, CodeInstructionsUnavailable},
		{"allowed control path", func(f *fx) {
			f.task["allowed_changes"] = []string{".claude/settings.json"}
			f.writeInputs()
		}, CodeInvalidPlan},
		{"allowed instruction path", func(f *fx) {
			f.task["allowed_changes"] = []string{"AGENTS.md"}
			f.writeInputs()
		}, CodeInvalidPlan},
		{"allowed missing parent", func(f *fx) {
			f.task["allowed_changes"] = []string{"nodir/x.txt"}
			f.writeInputs()
		}, CodeInvalidPlan},
		{"allowed glob", func(f *fx) {
			f.task["allowed_changes"] = []string{"*.txt"}
			f.writeInputs()
		}, CodeInvalidPlan},
		{"task scope mismatch", func(f *fx) {
			f.task["scope_sha256"] = strings.Repeat("6", 64)
			f.writeInputs()
		}, CodeInvalidPlan},
		{"task text oversized", func(f *fx) {
			f.task["task"] = strings.Repeat("t", MaxTaskText+1)
			f.writeInputs()
		}, CodeInvalidPlan},
		{"plan permissions", func(f *fx) { f.ok(os.Chmod(filepath.Join(f.priv, "plan.json"), 0o644)) }, CodeStatePermissions},
		{"profile hash drift", func(f *fx) {
			m := f.profile()
			m["approved_profile_sha256"] = strings.Repeat("7", 64)
			f.writeProfile(m)
		}, CodeProfileUnavailable},
		{"executable drift", func(f *fx) { f.ok(os.WriteFile(f.exe, []byte("#!/bin/sh\nexit 1\n"), 0o755)) }, CodeProfileUnavailable},
		{"executable group writable", func(f *fx) { f.ok(os.Chmod(f.exe, 0o775)) }, CodeProfileUnavailable},
		{"alternate config present", func(f *fx) {
			f.mkdir(filepath.Join(f.userHome, ".config", "claude"), 0o755)
			f.ok(os.WriteFile(filepath.Join(f.userHome, ".config", "claude", "settings.json"), nil, 0o600))
		}, CodeSourceUnavailable},
		{"project memory present", func(f *fx) {
			f.mkdir(filepath.Join(f.userHome, ".claude", "projects", claude.EncodedProject(f.repo), "memory"), 0o700)
		}, CodeSourceUnavailable},
		{"unsafe user source", func(f *fx) {
			f.mkdir(filepath.Join(f.userHome, ".claude"), 0o755)
			f.ok(os.Symlink("/etc/hosts", filepath.Join(f.userHome, ".claude", "CLAUDE.md")))
		}, CodeSourceUnavailable},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		c.setup(f)
		_, _, err := f.run(context.Background(), true)
		if !IsCode(err, c.want) || f.runs != 0 {
			t.Errorf("%s: %v runs=%d, want %s", c.name, err, f.runs, c.want)
		}
		if _, err := os.Lstat(f.attempt()); err == nil {
			t.Errorf("%s: attempt created", c.name)
		}
	}
	// The platform gate precedes all state access.
	f := newFx(t, "sha1")
	var out strings.Builder
	d := Deps{Platform: func() (string, string) { return "linux", "amd64" }}
	if _, err := RunWith(context.Background(), f.request(true), &out, d); !IsCode(err, CodeProfileUnavailable) {
		t.Fatalf("platform: %v", err)
	}
	// A cancelled request starts nothing and acquires nothing.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := f.run(ctx, true); !IsCode(err, CodeCancelled) || f.runs != 0 {
		t.Fatalf("cancelled: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(f.stateDir, Namespace)); err == nil {
		t.Fatal("namespace created by cancelled request")
	}
}

// Only the allowed literal path may differ; every other kind of change makes
// the attempt unusable without rollback.
func TestWorkerChangeCapture(t *testing.T) {
	write := func(rel, body string) func(*fx, string) {
		return func(f *fx, top string) { f.ok(os.WriteFile(filepath.Join(top, rel), []byte(body), 0o644)) }
	}
	cases := []struct {
		name string
		edit func(f *fx, top string)
		want string
	}{
		{"allowed edit", write("target.txt", "marker: kestrel\n"), OutcomeCompleted},
		{"no edit", func(*fx, string) {}, OutcomeCompleted},
		{"allowed delete", func(f *fx, top string) { f.ok(os.Remove(filepath.Join(top, "target.txt"))) }, OutcomeCompleted},
		{"instruction edit", write("AGENTS.md", "changed\n"), OutcomeUnusable},
		{"extra file", write("extra.txt", "x"), OutcomeUnusable},
		{"ignored file", func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, ".git", "info", "exclude"), []byte("ign.txt\n"), 0o644))
			f.ok(os.WriteFile(filepath.Join(top, "ign.txt"), nil, 0o644))
		}, OutcomeUnusable},
		{"index change", func(f *fx, top string) {
			write("target.txt", "staged\n")(f, top)
			gitIn(f, "add", "target.txt")
		}, OutcomeUnusable},
		{"new commit", func(f *fx, top string) {
			write("target.txt", "committed\n")(f, top)
			gitIn(f, "commit", "-q", "-am", "worker commit")
		}, OutcomeUnusable},
		{"allowed path made symlink", func(f *fx, top string) {
			f.ok(os.Remove(filepath.Join(top, "target.txt")))
			f.ok(os.Symlink("AGENTS.md", filepath.Join(top, "target.txt")))
		}, OutcomeUnusable},
		{"new nested instruction", write("workflow/CLAUDE.md", "x"), OutcomeUnusable},
		{"user source created", func(f *fx, _ string) {
			f.mkdir(filepath.Join(f.userHome, ".claude"), 0o700)
			f.ok(os.WriteFile(filepath.Join(f.userHome, ".claude", "CLAUDE.md"), nil, 0o600))
		}, OutcomeUnusable},
		{"native state created", func(f *fx, _ string) {
			f.ok(os.WriteFile(filepath.Join(f.userHome, ".claude.json"), []byte("{}"), 0o600))
		}, OutcomeCompleted},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		edit := c.edit
		f.edit = func(top string) { edit(f, top) }
		out, completed, err := f.run(context.Background(), true)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		r := decodeResult(t, out)
		if r.Outcome != c.want || completed != (c.want == OutcomeCompleted) {
			t.Errorf("%s: outcome %s", c.name, r.Outcome)
		}
		if c.want == OutcomeUnusable && (r.Repository.AfterHead != nil || r.Repository.ChangeManifestSHA256 != nil) {
			t.Errorf("%s: unusable attempt claimed a change manifest", c.name)
		}
	}
}

// Stream, profile and process failures are never completion.
func TestWorkerNativeFailures(t *testing.T) {
	hook := func(f *fx) string {
		return initLine(f.top()) + "\n" + `{"type":"system","subtype":"hook_started"}` + "\n"
	}
	wrongReport := func(f *fx) string {
		return initLine(f.top()) + "\n" + terminalLine(testRunID, strings.Repeat("9", 64), "completed", "none") + "\n"
	}
	badInit := func(f *fx) string {
		return strings.Replace(initLine(f.top()), `"apiKeySource":"none"`, `"apiKeySource":"user"`, 1) + "\n" +
			terminalLine(testRunID, f.taskSHA, "completed", "none") + "\n"
	}
	timedOut := proc.Observation{Started: true, TimedOut: true, Joined: true, GroupAbsent: true}
	exit1 := proc.Observation{Started: true, Exited: true, ExitCode: 1, Joined: true, GroupAbsent: true, StdoutEOF: true, StderrEOF: true}
	cases := []struct {
		name     string
		stream   func(*fx) string
		obs      *proc.Observation
		state    string
		init     bool
		terminal bool
	}{
		{"hook event", hook, nil, StateExited, true, false},
		{"report binding", wrongReport, nil, StateExited, true, false},
		{"init mismatch", badInit, nil, StateExited, false, false},
		{"prompt hang timeout", completedStream, &timedOut, StateUnverified, true, true},
		{"nonzero exit", completedStream, &exit1, StateExited, true, true},
		{"not started", completedStream, &proc.Observation{}, StateNotStarted, false, false},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		f.stream = c.stream
		if c.obs != nil {
			f.obs = *c.obs
		}
		if c.state == StateNotStarted {
			f.stream = func(*fx) string { return "" }
		}
		out, completed, err := f.run(context.Background(), true)
		if err != nil || completed {
			t.Fatalf("%s: %v %v", c.name, err, completed)
		}
		r := decodeResult(t, out)
		if r.Outcome != OutcomeUnusable || r.Worker.State != c.state || r.StreamSummary.InitVerified != c.init ||
			r.StreamSummary.TerminalVerified != c.terminal {
			t.Errorf("%s: %s", c.name, out)
		}
	}
}

// Faults after acquisition leave the attempt blocked and never reusable; a
// later request for the same ID starts nothing.
func TestWorkerAcquiredFaults(t *testing.T) {
	for _, stage := range []string{"id-chmod", "supplied-inputs.json-write", "packet.txt-link", "intent.json-sync", "result.json-write", "deliver"} {
		f := newFx(t, "sha1")
		f.fault = func(s string) error {
			if s == stage {
				return os.ErrInvalid
			}
			return nil
		}
		_, _, err := f.run(context.Background(), true)
		if !IsCode(err, CodeUncertain) {
			t.Fatalf("%s: %v", stage, err)
		}
		before := f.runs
		if stage == "result.json-write" || stage == "deliver" {
			if before != 1 {
				t.Fatalf("%s: runs %d", stage, before)
			}
		} else if before != 0 {
			t.Fatalf("%s: started after an unpublished input", stage)
		}
		f.fault = nil
		if before == 1 {
			// Validation rejects the retained dirty worktree before inspecting
			// an existing attempt. Preserve that assertion, then restore only
			// this disposable fixture to exercise the exclusive-ID rejection.
			if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeNotClean) || f.runs != before {
				t.Fatalf("%s: dirty replay %v runs %d", stage, err, f.runs)
			}
			b, err := os.ReadFile(filepath.Join(f.repo, "target.txt"))
			if err != nil || string(b) != "marker: kestrel\n" {
				t.Fatalf("%s: edits not preserved", stage)
			}
			f.ok(os.WriteFile(filepath.Join(f.repo, "target.txt"), []byte("base\n"), 0o644))
		}
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeExists) || f.runs != before {
			t.Fatalf("%s: replay %v runs %d", stage, err, f.runs)
		}
	}
	// A pre-start change after intent records a not_started result, delivers
	// it and reports input_changed; nothing is started.
	f := newFx(t, "sha1")
	f.fault = func(s string) error {
		if s == "prestart" {
			return os.ErrInvalid
		}
		return nil
	}
	out, completed, err := f.run(context.Background(), true)
	if !IsCode(err, CodeInputChanged) || completed || f.runs != 0 {
		t.Fatalf("prestart: %v runs %d", err, f.runs)
	}
	if r := decodeResult(t, out); r.Outcome != OutcomeUnusable || r.Worker.State != StateNotStarted || r.StreamSummary.InitVerified {
		t.Fatalf("prestart result %s", out)
	}
	// A real pre-start input change: the task file rewritten after intent.
	g := newFx(t, "sha1")
	g.fault = func(s string) error {
		if s == "intent.json-dir-sync" {
			g.task["task"] = "changed after intent"
			g.writeInputs()
		}
		return nil
	}
	if _, _, err := g.run(context.Background(), true); !IsCode(err, CodeInputChanged) || g.runs != 0 {
		t.Fatalf("task change before start: %v runs %d", err, g.runs)
	}
}

// The receipt validators reject a fabricated completion.
func TestWorkerResultValidation(t *testing.T) {
	f := newFx(t, "sha1")
	out, _, err := f.run(context.Background(), true)
	if err != nil || !ValidResult([]byte(out), testRunID) {
		t.Fatalf("%v %s", err, out)
	}
	bad := map[string]string{
		"completed without terminal": strings.Replace(out, `"terminal_verified":true`, `"terminal_verified":false`, 1),
		"completed without manifest": strings.Replace(out, `"change_manifest_sha256":"`, `"change_manifest_sha256":null,"x":"`, 1),
		"authority claimed":          strings.Replace(out, `"authority":"not_evaluated"`, `"authority":"granted"`, 1),
		"loaded-source claim":        strings.Replace(out, `"complete_enumerated_metadata"`, `"complete_loaded_source_inventory"`, 1),
		"wrong run":                  strings.Replace(out, testRunID, strings.Repeat("f", 32), 1),
	}
	for name, body := range bad {
		if ValidResult([]byte(body), testRunID) {
			t.Errorf("%s accepted", name)
		}
	}
}

// A mode-only change is still a change, bound in the private manifest rather
// than silently described as unchanged merely because its bytes match.
func TestWorkerModeOnlyChangeCapture(t *testing.T) {
	f := newFx(t, "sha1")
	f.edit = func(top string) { f.ok(os.Chmod(filepath.Join(top, "target.txt"), 0o755)) }
	_, completed, err := f.run(context.Background(), true)
	if err != nil || !completed {
		t.Fatalf("%v %v", completed, err)
	}
	b, err := os.ReadFile(filepath.Join(f.attempt(), "changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m changeManifest
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Changes) != 1 || m.Changes[0].State != "modified" || m.Changes[0].Mode == nil || *m.Changes[0].Mode != 0o755 {
		t.Fatalf("mode-only change not captured: %s", b)
	}
}
