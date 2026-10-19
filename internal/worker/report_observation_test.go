package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
)

var testReasons = []string{"blocked", "needs_clarification", "failed"}

func reasonStream(reason string) func(*fx) string {
	return func(f *fx) string {
		return initLine(f.top()) + "\n" + terminalLine(testRunID, f.taskSHA, "not_completed", reason) + "\n"
	}
}

// companionOracle is the hand-written companion for the fixture and the exact
// result.json bytes.
func companionOracle(f *fx, result []byte, reason string) string {
	return `{"schema_version":1,"run_id":"` + testRunID + `","task_input_sha256":"` + f.taskSHA +
		`","result_sha256":"` + hexSum(result) + `","status":"not_completed","reason":"` + reason + `"}` + "\n"
}

// attemptFile reads one published attempt file; ok is false when absent.
func attemptFile(f *fx, name string) ([]byte, bool) {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.attempt(), name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	f.ok(err)
	return b, true
}

func restoreTarget(f *fx) {
	f.t.Helper()
	f.ok(os.WriteFile(filepath.Join(f.repo, "target.txt"), []byte("base\n"), 0o644))
}

// Every native noncompletion reason, in both object formats and both output
// forms, publishes the exact companion bound to the JSON result bytes and
// leaves the delivered output exactly the version 1 output.
func TestReportObservationReasons(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, reason := range testReasons {
			for _, asJSON := range []bool{true, false} {
				name := format + "/" + reason
				f := newFx(t, format)
				f.stream = reasonStream(reason)
				out, completed, err := f.run(context.Background(), asJSON)
				if err != nil || completed || f.runs != 1 {
					t.Fatalf("%s json=%v: %v %v runs=%d", name, asJSON, err, completed, f.runs)
				}
				result, ok := attemptFile(f, "result.json")
				if !ok || !ValidResult(result, testRunID) {
					t.Fatalf("%s: result %q", name, result)
				}
				r := decodeResult(t, string(result))
				if r.Outcome != OutcomeNotCompleted || r.Worker.State != StateExited || !r.StreamSummary.TerminalVerified {
					t.Fatalf("%s: %s", name, result)
				}
				if asJSON && out != string(result) {
					t.Fatalf("%s: JSON output differs from result.json\n%s\n%s", name, out, result)
				}
				if !asJSON && out != string(RenderText(r)) {
					t.Fatalf("%s: text output %q", name, out)
				}
				if strings.Contains(out, reason) || strings.Contains(string(result), reason) {
					t.Fatalf("%s: reason leaked into the version 1 receipt or output", name)
				}
				comp, ok := attemptFile(f, ReportObservationName)
				if !ok || string(comp) != companionOracle(f, result, reason) {
					t.Fatalf("%s: companion %q\nwant %q", name, comp, companionOracle(f, result, reason))
				}
				if !ValidReportObservation(comp, result, testRunID) {
					t.Fatalf("%s: companion rejected by its validator", name)
				}
				fi, err := os.Lstat(filepath.Join(f.attempt(), ReportObservationName))
				if err != nil || !fi.Mode().IsRegular() || !private(fi, 0o600) || fileSafety(fi) != nil {
					t.Fatalf("%s: companion mode %v %v", name, fi, err)
				}
				want := []string{"changes.json", "intent.json", "packet.txt", ReportObservationName, "result.json",
					"source-manifest.json", "supplied-inputs.json", "worker"}
				if got := attemptNames(t, f.attempt()); strings.Join(got, ",") != strings.Join(want, ",") {
					t.Fatalf("%s: attempt files %v", name, got)
				}
			}
		}
	}
}

// The validator is strict about form, types, values and every binding, and
// the constructor validates the report pair rather than trusting it.
func TestReportObservationValidator(t *testing.T) {
	f := newFx(t, "sha1")
	f.stream = reasonStream("blocked")
	if _, _, err := f.run(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	result, _ := attemptFile(f, "result.json")
	comp, _ := attemptFile(f, ReportObservationName)
	good := companionOracle(f, result, "blocked")
	if string(comp) != good || !ValidReportObservation([]byte(good), result, testRunID) {
		t.Fatalf("control rejected: %q", comp)
	}
	rep := func(old, new string) string {
		t.Helper()
		if !strings.Contains(good, old) {
			t.Fatalf("fixture lacks %q", old)
		}
		return strings.Replace(good, old, new, 1)
	}
	// Insignificant JSON whitespace anywhere is permitted up to exactly 1024
	// bytes; the companion has a strict JSON schema, not a byte layout.
	pad := func(n int) string { return "{" + strings.Repeat(" ", n-len(good)) + good[1:] }
	lead := func(n int) string { return strings.Repeat("\n", n-len(good)) + good }
	trail := func(n int) string { return good + strings.Repeat(" ", n-len(good)) }
	for _, p := range []func(int) string{pad, lead, trail} {
		if len(p(1024)) != 1024 || len(p(1025)) != 1025 {
			t.Fatal("pad fixture")
		}
	}
	// A JSON Unicode escape of one letter of a key decodes to the same name.
	// The backslash is joined at run time so the fixture bytes hold the
	// six-byte escape for "s", not the letter.
	esc := "\\" + "u0073"
	escaped := rep(`"reason":`, `"rea`+esc+`on":`)
	if escaped == good || len(escaped) != len(good)+5 || !strings.Contains(escaped, `"rea`+esc+`on"`) ||
		strings.Contains(escaped, `"reason"`) {
		t.Fatal("escape fixture")
	}
	valid := map[string]string{
		"padded to 1024":      pad(1024),
		"leading to 1024":     lead(1024),
		"trailing to 1024":    trail(1024),
		"escaped key":         escaped,
		"no trailing newline": strings.TrimSuffix(good, "\n"),
		"two newlines":        good + "\n",
		"CRLF":                strings.TrimSuffix(good, "\n") + "\r\n",
		"leading space":       " " + good,
		"leading tab":         "\t" + good,
	}
	for name, body := range valid {
		if !ValidReportObservation([]byte(body), result, testRunID) {
			t.Errorf("%s rejected (len %d)", name, len(body))
		}
	}
	other := strings.Repeat("a", 32)
	hash := hexSum(result)
	bad := map[string]string{
		"1025 bytes":                pad(1025),
		"1025 bytes leading":        lead(1025),
		"1025 bytes trailing":       trail(1025),
		"empty":                     "",
		"whitespace only":           " \n",
		"truncated":                 good[:len(good)-3],
		"unterminated string":       rep(`"reason":"blocked"`, `"reason":"blocked`),
		"trailing comma":            rep(`"reason":"blocked"`, `"reason":"blocked",`),
		"single quotes":             rep(`"reason":"blocked"`, `'reason':'blocked'`),
		"trailing value":            strings.TrimSuffix(good, "\n") + "{}\n",
		"trailing number":           good + "1\n",
		"two objects":               good + good,
		"trailing bracket":          strings.TrimSuffix(good, "\n") + "]\n",
		"array":                     "[" + strings.TrimSuffix(good, "\n") + "]\n",
		"string value":              quote(good),
		"duplicate reason":          rep(`"reason":"blocked"`, `"reason":"blocked","reason":"blocked"`),
		"escaped duplicate":         rep(`"reason":"blocked"`, `"reason":"blocked","rea`+esc+`on":"failed"`),
		"escaped duplicate same":    rep(`"reason":"blocked"`, `"rea`+esc+`on":"blocked","reason":"blocked"`),
		"extra key":                 rep(`"reason":"blocked"`, `"reason":"blocked","detail":"x"`),
		"missing reason":            rep(`,"reason":"blocked"`, ``),
		"missing run":               rep(`"run_id":"`+testRunID+`",`, ``),
		"missing task":              rep(`"task_input_sha256":"`+f.taskSHA+`",`, ``),
		"missing result hash":       rep(`"result_sha256":"`+hash+`",`, ``),
		"missing status":            rep(`"status":"not_completed",`, ``),
		"duplicate result hash":     rep(`"result_sha256":"`+hash+`"`, `"result_sha256":"`+hash+`","result_sha256":"`+hash+`"`),
		"escaped dup result hash":   rep(`"result_sha256":"`+hash+`"`, `"result_sha256":"`+hash+`","result_`+esc+`ha256":"`+hash+`"`),
		"duplicate run":             rep(`"run_id":"`+testRunID+`"`, `"run_id":"`+testRunID+`","run_id":"`+testRunID+`"`),
		"result hash non-hex":       rep(hash, strings.Repeat("g", 64)),
		"result hash long":          rep(hash, hash+"0"),
		"result hash uppercase":     rep(hash, strings.ToUpper(hash)),
		"result hash empty":         rep(`"`+hash+`"`, `""`),
		"result hash number":        rep(`"`+hash+`"`, `1`),
		"result hash array":         rep(`"`+hash+`"`, `["`+hash+`"]`),
		"result hash of no newline": rep(hash, hexSum([]byte(strings.TrimSuffix(string(result), "\n")))),
		"run number":                rep(`"`+testRunID+`"`, `123`),
		"run null":                  rep(`"`+testRunID+`"`, `null`),
		"run object":                rep(`"`+testRunID+`"`, `{}`),
		"task number":               rep(`"`+f.taskSHA+`"`, `0`),
		"task null":                 rep(`"`+f.taskSHA+`"`, `null`),
		"task bool":                 rep(`"`+f.taskSHA+`"`, `false`),
		"status null":               rep(`"status":"not_completed"`, `"status":null`),
		"reason number":             rep(`"reason":"blocked"`, `"reason":1`),
		"schema null":               rep(`"schema_version":1`, `"schema_version":null`),
		"schema string":             rep(`"schema_version":1`, `"schema_version":"1"`),
		"schema float":              rep(`"schema_version":1`, `"schema_version":1.0`),
		"schema 2":                  rep(`"schema_version":1`, `"schema_version":2`),
		"schema bool":               rep(`"schema_version":1`, `"schema_version":true`),
		"run uppercase":             rep(testRunID, strings.ToUpper(testRunID)),
		"other run":                 rep(testRunID, other),
		"task mismatch":             rep(f.taskSHA, strings.Repeat("6", 64)),
		"task uppercase":            rep(`"task_input_sha256":"`+f.taskSHA, `"task_input_sha256":"`+strings.ToUpper(f.taskSHA)),
		"result hash mismatch":      rep(hexSum(result), strings.Repeat("7", 64)),
		"result hash short":         rep(hexSum(result), hexSum(result)[:63]),
		"result hash null":          rep(`"`+hexSum(result)+`"`, `null`),
		"status completed":          rep(`"status":"not_completed"`, `"status":"completed"`),
		"status number":             rep(`"status":"not_completed"`, `"status":1`),
		"reason none":               rep(`"reason":"blocked"`, `"reason":"none"`),
		"reason empty":              rep(`"reason":"blocked"`, `"reason":""`),
		"reason case":               rep(`"reason":"blocked"`, `"reason":"Blocked"`),
		"reason unknown":            rep(`"reason":"blocked"`, `"reason":"timeout"`),
		"reason null":               rep(`"reason":"blocked"`, `"reason":null`),
		"reason array":              rep(`"reason":"blocked"`, `"reason":["blocked"]`),
		"reason free text":          rep(`"reason":"blocked"`, `"reason":"blocked: see /tmp/x"`),
		"invalid UTF-8 in text":     rep(`"reason":"blocked"`, "\"reason\":\"blocked\xff\""),
	}
	for name, body := range bad {
		if ValidReportObservation([]byte(body), result, testRunID) {
			t.Errorf("%s accepted", name)
		}
	}
	// Result bindings: the companion is meaningful only beside its exact,
	// valid worker_not_completed result.
	if ValidReportObservation([]byte(good), result, other) {
		t.Error("other run ID accepted")
	}
	if ValidReportObservation([]byte(good), nil, testRunID) {
		t.Error("orphan companion accepted")
	}
	// The hash covers the trailing newline: the same result without it is
	// different bytes.
	trimmed := []byte(strings.TrimSuffix(string(result), "\n"))
	if ValidReportObservation([]byte(good), trimmed, testRunID) {
		t.Error("hash of the newline-terminated result accepted for other bytes")
	}
	for _, outcome := range []string{OutcomeCompleted, OutcomeUnusable} {
		mutated := []byte(strings.Replace(string(result), `"outcome":"`+OutcomeNotCompleted+`"`, `"outcome":"`+outcome+`"`, 1))
		if !ValidResult(mutated, testRunID) {
			t.Fatalf("%s control result invalid", outcome)
		}
		if ValidReportObservation([]byte(rep(hexSum(result), hexSum(mutated))), mutated, testRunID) {
			t.Errorf("companion accepted beside a %s result", outcome)
		}
		if _, err := NewReportObservation(mutated, testRunID, "not_completed", "blocked"); err == nil {
			t.Errorf("constructed for a %s result", outcome)
		}
	}
	// Constructor: explicit pair validation and exact oracle bytes.
	for _, reason := range testReasons {
		b, err := NewReportObservation(result, testRunID, "not_completed", reason)
		if err != nil || string(b) != companionOracle(f, result, reason) {
			t.Errorf("%s: %q %v", reason, b, err)
		}
	}
	for _, p := range [][2]string{{"completed", "none"}, {"completed", "blocked"}, {"not_completed", "none"},
		{"not_completed", ""}, {"not_completed", "Blocked"}, {"", ""}, {"not_completed", "timeout"}} {
		if _, err := NewReportObservation(result, testRunID, p[0], p[1]); err == nil {
			t.Errorf("pair %q accepted", p)
		}
	}
	if _, err := NewReportObservation(result, other, "not_completed", "blocked"); err == nil {
		t.Error("constructed for another run ID")
	}
	if _, err := NewReportObservation(nil, testRunID, "not_completed", "blocked"); err == nil {
		t.Error("constructed without a result")
	}
}

// Cancellation observed when the native runner returns makes the attempt
// ineligible; cancellation after that observation does not by itself change
// the classification or suppress the companion.
func TestReportObservationCancellation(t *testing.T) {
	// Cutoff: cancelled while the runner is still running.
	f := newFx(t, "sha1")
	f.stream = reasonStream("failed")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	edit := f.edit
	f.edit = func(top string) { edit(top); cancel() }
	out, completed, err := f.run(ctx, true)
	if err != nil || completed || f.runs != 1 {
		t.Fatalf("cutoff: %v %v runs=%d", err, completed, f.runs)
	}
	if r := decodeResult(t, out); r.Outcome != OutcomeUnusable || r.Worker.State != StateUnverified {
		t.Fatalf("cutoff result %s", out)
	}
	if _, ok := attemptFile(f, ReportObservationName); ok {
		t.Fatal("companion published for a cancelled observation")
	}
	// Late: cancelled after launch, at each later publication stage.
	for _, stage := range []string{"output-limit", "changes.json-random", ReportObservationName + "-random", ReportObservationName + "-dir-sync",
		"result.json-random", "deliver"} {
		g := newFx(t, "sha256")
		g.stream = reasonStream("needs_clarification")
		ctx, cancel := context.WithCancel(context.Background())
		g.fault = func(s string) error {
			if s == stage {
				cancel()
			}
			return nil
		}
		out, completed, err := g.run(ctx, true)
		cancel()
		if err != nil || completed {
			t.Fatalf("%s: %v %v", stage, err, completed)
		}
		result, _ := attemptFile(g, "result.json")
		comp, ok := attemptFile(g, ReportObservationName)
		if out != string(result) || decodeResult(t, out).Outcome != OutcomeNotCompleted || !ok ||
			string(comp) != companionOracle(g, result, "needs_clarification") {
			t.Fatalf("%s: late cancellation changed the attempt: %s %q", stage, out, comp)
		}
	}
	// Cancelled inside launch after the cutoff: later existing checks still
	// decide; whatever they decide, a companion exists only beside a valid
	// worker_not_completed result.
	h := newFx(t, "sha1")
	h.stream = reasonStream("blocked")
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	h.fault = func(s string) error {
		if s == "post-observation" {
			cancel2()
		}
		return nil
	}
	out, _, err = h.run(ctx2, true)
	if err != nil {
		t.Fatalf("post-observation: %v", err)
	}
	result, _ := attemptFile(h, "result.json")
	comp, ok := attemptFile(h, ReportObservationName)
	if r := decodeResult(t, out); ok != (r.Outcome == OutcomeNotCompleted) || (ok && !ValidReportObservation(comp, result, testRunID)) {
		t.Fatalf("post-observation: outcome %s companion %v", r.Outcome, ok)
	}
}

// Completed, unusable and report-less attempts publish no companion, even
// when a not_completed report was parsed.
func TestReportObservationIneligible(t *testing.T) {
	exit1 := proc.Observation{Started: true, Exited: true, ExitCode: 1, Joined: true, GroupAbsent: true, StdoutEOF: true, StderrEOF: true}
	timedOut := proc.Observation{Started: true, TimedOut: true, Joined: true, GroupAbsent: true}
	notStarted := proc.Observation{}
	stopAt := func(stage string) func(string) error {
		return func(s string) error {
			if s == stage {
				return os.ErrInvalid
			}
			return nil
		}
	}
	cases := []struct {
		name   string
		stream func(*fx) string
		obs    *proc.Observation
		edit   func(f *fx, top string)
		fault  func(string) error
		want   string
		code   Code
	}{
		{"completed", completedStream, nil, nil, nil, OutcomeCompleted, ""},
		{"extra file", reasonStream("blocked"), nil, func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, "extra.txt"), []byte("x"), 0o644))
		}, nil, OutcomeUnusable, ""},
		// Post-run drift of an instruction, a source matrix row or a supplied
		// input beside the otherwise allowed edit.
		{"instruction drift", reasonStream("blocked"), nil, func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, "target.txt"), []byte("marker: kestrel\n"), 0o644))
			f.ok(os.WriteFile(filepath.Join(top, "AGENTS.md"), []byte("changed\n"), 0o644))
		}, nil, OutcomeUnusable, ""},
		{"source drift", reasonStream("needs_clarification"), nil, func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, "target.txt"), []byte("marker: kestrel\n"), 0o644))
			f.mkdir(filepath.Join(f.userHome, ".claude"), 0o700)
			f.ok(os.WriteFile(filepath.Join(f.userHome, ".claude", "CLAUDE.md"), nil, 0o600))
		}, nil, OutcomeUnusable, ""},
		{"task input drift", reasonStream("failed"), nil, func(f *fx, top string) {
			f.ok(os.WriteFile(filepath.Join(top, "target.txt"), []byte("marker: kestrel\n"), 0o644))
			p := filepath.Join(f.priv, "task.json")
			b, err := os.ReadFile(p)
			f.ok(err)
			f.ok(os.WriteFile(p, append(b, ' '), 0o600))
		}, nil, OutcomeUnusable, ""},
		{"nonzero exit", reasonStream("blocked"), &exit1, nil, nil, OutcomeUnusable, ""},
		{"timed out", reasonStream("failed"), &timedOut, nil, nil, OutcomeUnusable, ""},
		{"not started", func(*fx) string { return "" }, &notStarted, nil, nil, OutcomeUnusable, ""},
		{"event after report", func(f *fx) string {
			return reasonStream("blocked")(f) + `{"type":"system","subtype":"status","status":null}` + "\n"
		}, nil, nil, nil, OutcomeUnusable, ""},
		{"report without final newline", func(f *fx) string {
			return strings.TrimSuffix(reasonStream("blocked")(f), "\n")
		}, nil, nil, nil, OutcomeUnusable, ""},
		{"report bound to another task", func(f *fx) string {
			return initLine(f.top()) + "\n" + terminalLine(testRunID, strings.Repeat("9", 64), "not_completed", "blocked") + "\n"
		}, nil, nil, nil, OutcomeUnusable, ""},
		{"no report", func(f *fx) string { return initLine(f.top()) + "\n" }, nil, nil, nil, OutcomeUnusable, ""},
		{"post-observation fault", reasonStream("blocked"), nil, nil, stopAt("post-observation"), OutcomeUnusable, ""},
		{"prestart fault", reasonStream("blocked"), nil, nil, stopAt("prestart"), OutcomeUnusable, CodeInputChanged},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		f.stream, f.fault = c.stream, c.fault
		if c.obs != nil {
			f.obs = *c.obs
		}
		if c.edit != nil {
			edit := c.edit
			f.edit = func(top string) { edit(f, top) }
		}
		out, _, err := f.run(context.Background(), true)
		if (c.code == "" && err != nil) || (c.code != "" && !IsCode(err, c.code)) {
			t.Fatalf("%s: %v", c.name, err)
		}
		r := decodeResult(t, out)
		if r.Outcome != c.want {
			t.Errorf("%s: outcome %s", c.name, r.Outcome)
		}
		// Drift alone decides: the native run and its report were usable.
		if strings.HasSuffix(c.name, " drift") && (r.Worker.State != StateExited || !r.StreamSummary.TerminalVerified ||
			r.Repository.AfterHead != nil || r.Repository.ChangeManifestSHA256 != nil) {
			t.Errorf("%s: not a drift-only rejection: %s", c.name, out)
		}
		if result, ok := attemptFile(f, "result.json"); !ok || string(result) != out {
			t.Errorf("%s: result not published as delivered", c.name)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), ReportObservationName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: companion present (%v)", c.name, err)
		}
	}
}

// A companion publication fault is uncertain: no result, no output, edits
// kept and no replay. A later result or delivery fault keeps its existing
// behavior and may leave an orphan companion that validates against nothing.
// Published files are never replaced.
func TestReportObservationPublicationFaults(t *testing.T) {
	// The store's actual companion seams, in order (storage.go publish and
	// openChecked). The descriptor Stat inside openChecked has no seam of its
	// own; "-dir-open" precedes the directory Lstat, open and Stat.
	p := ReportObservationName
	seams := []string{p + "-random", p + "-create", p + "-write", p + "-sync", p + "-link", p + "-dir-open", p + "-dir-sync"}
	ctl := newFx(t, "sha1")
	ctl.stream = reasonStream("blocked")
	var calls []string
	ctl.fault = func(s string) error {
		if strings.HasPrefix(s, p+"-") {
			calls = append(calls, s)
		}
		return nil
	}
	if _, _, err := ctl.run(context.Background(), true); err != nil || strings.Join(calls, ",") != strings.Join(seams, ",") {
		t.Fatalf("companion seams %v (%v), want %v", calls, err, seams)
	}
	cases := []struct {
		stage            string
		companion, final bool
	}{
		// Before the link: no final companion.
		{p + "-random", false, false},
		{p + "-create", false, false},
		{p + "-write", false, false},
		{p + "-sync", false, false},
		{p + "-link", false, false},
		// After the link: the directory step may leave an orphan.
		{p + "-dir-open", true, false},
		{p + "-dir-sync", true, false},
		{"result.json-write", true, false},
		{"deliver", true, true},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		f.stream = reasonStream("blocked")
		var seen []string
		f.fault = func(s string) error {
			seen = append(seen, s)
			if s == c.stage {
				return os.ErrInvalid
			}
			return nil
		}
		out, completed, err := f.run(context.Background(), true)
		if !IsCode(err, CodeUncertain) || completed || out != "" || f.runs != 1 {
			t.Fatalf("%s: %v %v %q runs=%d", c.stage, err, completed, out, f.runs)
		}
		// The faulted stage is the last one reached: no later result or
		// delivery step ran.
		if len(seen) == 0 || seen[len(seen)-1] != c.stage {
			t.Fatalf("%s: stages after the fault: %v", c.stage, seen)
		}
		comp, hasComp := attemptFile(f, ReportObservationName)
		result, hasResult := attemptFile(f, "result.json")
		if hasComp != c.companion || hasResult != c.final {
			t.Fatalf("%s: companion %v result %v", c.stage, hasComp, hasResult)
		}
		if hasComp && !hasResult && ValidReportObservation(comp, nil, testRunID) {
			t.Fatalf("%s: orphan companion validated", c.stage)
		}
		if hasResult && (string(comp) != companionOracle(f, result, "blocked") || !ValidReportObservation(comp, result, testRunID)) {
			t.Fatalf("%s: published pair %q", c.stage, comp)
		}
		var before fs.FileInfo
		if hasComp {
			before, err = os.Lstat(filepath.Join(f.attempt(), ReportObservationName))
			f.ok(err)
		}
		b, err := os.ReadFile(filepath.Join(f.repo, "target.txt"))
		if err != nil || string(b) != "marker: kestrel\n" {
			t.Fatalf("%s: edits not preserved", c.stage)
		}
		f.fault = nil
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeNotClean) || f.runs != 1 {
			t.Fatalf("%s: dirty replay %v runs %d", c.stage, err, f.runs)
		}
		restoreTarget(f)
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeExists) || f.runs != 1 {
			t.Fatalf("%s: replay %v runs %d", c.stage, err, f.runs)
		}
		after, hasAfter := attemptFile(f, ReportObservationName)
		if _, resultAfter := attemptFile(f, "result.json"); hasAfter != hasComp || resultAfter != hasResult || string(after) != string(comp) {
			t.Fatalf("%s: replay changed the attempt", c.stage)
		}
		if hasComp {
			fi, err := os.Lstat(filepath.Join(f.attempt(), ReportObservationName))
			if err != nil || !os.SameFile(fi, before) {
				t.Fatalf("%s: companion replaced", c.stage)
			}
		}
	}
}

// A regular file or a symbolic link already at the companion destination is
// never replaced or followed: the publication is uncertain, no result is
// published and the existing entry, its target, bytes and inode are kept.
func TestReportObservationPublicationCollision(t *testing.T) {
	existing := []byte("pre-existing disposable fixture\n")
	for _, kind := range []string{"regular", "symlink"} {
		f := newFx(t, "sha256")
		f.stream = reasonStream("failed")
		dest := filepath.Join(f.attempt(), ReportObservationName)
		target := filepath.Join(f.root, "collision-target")
		var destInfo, targetInfo fs.FileInfo
		f.fault = func(s string) error {
			if s != ReportObservationName+"-random" {
				return nil
			}
			var err error
			if kind == "regular" {
				f.ok(os.WriteFile(dest, existing, 0o600))
			} else {
				f.ok(os.WriteFile(target, existing, 0o600))
				f.ok(os.Symlink(target, dest))
				targetInfo, err = os.Lstat(target)
				f.ok(err)
			}
			destInfo, err = os.Lstat(dest)
			f.ok(err)
			return nil
		}
		out, completed, err := f.run(context.Background(), true)
		if !IsCode(err, CodeUncertain) || completed || out != "" || f.runs != 1 || destInfo == nil {
			t.Fatalf("%s: %v %v %q runs=%d", kind, err, completed, out, f.runs)
		}
		check := func(when string) {
			t.Helper()
			if _, ok := attemptFile(f, "result.json"); ok {
				t.Fatalf("%s %s: result published", kind, when)
			}
			fi, err := os.Lstat(dest)
			if err != nil || !os.SameFile(fi, destInfo) || fi.Mode() != destInfo.Mode() {
				t.Fatalf("%s %s: destination replaced (%v)", kind, when, err)
			}
			read := dest
			if kind == "symlink" {
				if l, err := os.Readlink(dest); err != nil || l != target {
					t.Fatalf("%s %s: link %q %v", kind, when, l, err)
				}
				ti, err := os.Lstat(target)
				if err != nil || !os.SameFile(ti, targetInfo) || ti.Mode() != targetInfo.Mode() {
					t.Fatalf("%s %s: link target replaced (%v)", kind, when, err)
				}
				read = target
			}
			if b, err := os.ReadFile(read); err != nil || string(b) != string(existing) {
				t.Fatalf("%s %s: bytes %q %v", kind, when, b, err)
			}
		}
		check("publish")
		if b, err := os.ReadFile(filepath.Join(f.repo, "target.txt")); err != nil || string(b) != "marker: kestrel\n" {
			t.Fatalf("%s: edits not preserved", kind)
		}
		f.fault = nil
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeNotClean) || f.runs != 1 {
			t.Fatalf("%s: dirty replay %v runs %d", kind, err, f.runs)
		}
		restoreTarget(f)
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeExists) || f.runs != 1 {
			t.Fatalf("%s: replay %v runs %d", kind, err, f.runs)
		}
		check("replay")
	}
}

// A valid worker_not_completed version 1 result without a companion, as
// written before the companion existed, stays a valid result. Its absence is
// no claim: no category is derived from the result, and nothing recreates it.
func TestReportObservationLegacyMissing(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		f := newFx(t, format)
		f.stream = reasonStream("blocked")
		out, completed, err := f.run(context.Background(), true)
		if err != nil || completed {
			t.Fatalf("%s: %v %v", format, err, completed)
		}
		// Disposable fixture: remove the companion to form the legacy layout.
		f.ok(os.Remove(filepath.Join(f.attempt(), ReportObservationName)))
		result, ok := attemptFile(f, "result.json")
		if !ok || string(result) != out || !ValidResult(result, testRunID) || decodeResult(t, out).Outcome != OutcomeNotCompleted {
			t.Fatalf("%s: legacy result %q", format, result)
		}
		for _, reason := range append([]string{"none"}, testReasons...) {
			if strings.Contains(string(result), `"`+reason+`"`) {
				t.Fatalf("%s: category %q in the version 1 result", format, reason)
			}
		}
		for _, data := range [][]byte{nil, {}, []byte("{}\n")} {
			if ValidReportObservation(data, result, testRunID) {
				t.Fatalf("%s: absent companion %q validated", format, data)
			}
		}
		checkV1JSON(t, f, result, "worker_not_completed")
		restoreTarget(f)
		if _, _, err := f.run(context.Background(), true); !IsCode(err, CodeExists) || f.runs != 1 {
			t.Fatalf("%s: replay %v runs %d", format, err, f.runs)
		}
		if _, ok := attemptFile(f, ReportObservationName); ok {
			t.Fatalf("%s: companion fabricated", format)
		}
		if after, _ := attemptFile(f, "result.json"); string(after) != string(result) {
			t.Fatalf("%s: result changed", format)
		}
	}
}

// The version 1 JSON and text receipts are unchanged by the companion. The
// expected key sets and text layout are hand-written; values come from the
// fixture and the attempt's other published files, never from RenderText or
// the Result type.
func TestReportObservationV1Compatibility(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		for _, asJSON := range []bool{true, false} {
			f := newFx(t, format)
			f.stream = reasonStream("needs_clarification")
			out, completed, err := f.run(context.Background(), asJSON)
			if err != nil || completed {
				t.Fatalf("%s json=%v: %v %v", format, asJSON, err, completed)
			}
			result, ok := attemptFile(f, "result.json")
			if !ok {
				t.Fatalf("%s: no result", format)
			}
			if _, ok := attemptFile(f, ReportObservationName); !ok {
				t.Fatalf("%s: no companion", format)
			}
			checkV1JSON(t, f, result, "worker_not_completed")
			if asJSON {
				if out != string(result) {
					t.Fatalf("%s: delivered JSON differs from result.json", format)
				}
				continue
			}
			if want := v1Text(t, f, "worker_not_completed"); out != want {
				t.Fatalf("%s: text\n%s\nwant\n%s", format, out, want)
			}
		}
	}
}

func v1Decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || dec.More() {
		t.Fatalf("decode %q: %v", b, err)
	}
	return m
}

func v1Keys(t *testing.T, where string, v any, keys ...string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok || len(m) != len(keys) {
		t.Fatalf("%s: keys %v, want %v", where, m, keys)
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			t.Fatalf("%s: missing %s in %v", where, k, m)
		}
	}
	return m
}

// v1Bound returns the values bound into the attempt independently of the
// result: the fixture's task, executable and profile hashes, and the hashes
// of the attempt's packet, source manifest and change manifest files, plus the
// intent's manifest hash and creation time.
func v1Bound(t *testing.T, f *fx) map[string]string {
	t.Helper()
	b := map[string]string{"task": f.taskSHA, "exe": hexSum(f.exeBytes)}
	b["profile"], _ = f.profile()["approved_profile_sha256"].(string)
	for k, name := range map[string]string{"packet": "packet.txt", "sources": "source-manifest.json", "changes": "changes.json"} {
		data, ok := attemptFile(f, name)
		if !ok {
			t.Fatalf("no %s", name)
		}
		b[k] = hexSum(data)
	}
	intentBytes, ok := attemptFile(f, "intent.json")
	if !ok {
		t.Fatal("no intent")
	}
	intent := v1Decode(t, intentBytes)
	b["manifest"], _ = intent["instruction_manifest_sha256"].(string)
	b["created"], _ = intent["created_at"].(string)
	for k, v := range b {
		if v == "" {
			t.Fatalf("bound value %s missing", k)
		}
	}
	return b
}

func checkV1JSON(t *testing.T, f *fx, result []byte, outcome string) {
	t.Helper()
	b := v1Bound(t, f)
	m := v1Keys(t, "result", v1Decode(t, result), "schema_version", "run_id", "operation", "authority", "readiness",
		"outcome", "worker", "input_binding", "repository", "instruction_count", "instruction_delivery",
		"source_observation", "stream_summary", "injection_disposition", "receipt_state", "created_at", "completed_at")
	w := v1Keys(t, "worker", m["worker"], "state", "exit_code")
	ib := v1Keys(t, "input_binding", m["input_binding"], "task_input_sha256", "instruction_manifest_sha256",
		"assembled_packet_sha256", "profile_sha256", "executable_sha256")
	repo := v1Keys(t, "repository", m["repository"], "object_format", "before_head", "after_head", "change_manifest_sha256")
	so := v1Keys(t, "source_observation", m["source_observation"], "matrix_id", "manifest_sha256", "coverage", "suppression_basis")
	ss := v1Keys(t, "stream_summary", m["stream_summary"], "init_verified", "terminal_verified", "memory_observation",
		"plugin_count", "information_counts")
	want := map[string][2]any{
		"schema_version":         {m["schema_version"], json.Number("1")},
		"run_id":                 {m["run_id"], testRunID},
		"operation":              {m["operation"], "run_worker"},
		"authority":              {m["authority"], "not_evaluated"},
		"readiness":              {m["readiness"], "not_evaluated"},
		"outcome":                {m["outcome"], outcome},
		"instruction_count":      {m["instruction_count"], json.Number("3")},
		"instruction_delivery":   {m["instruction_delivery"], "embedded_full_documents"},
		"injection_disposition":  {m["injection_disposition"], claude.InjectionDisposition},
		"receipt_state":          {m["receipt_state"], "recorded"},
		"created_at":             {m["created_at"], b["created"]},
		"worker.state":           {w["state"], "exited"},
		"worker.exit_code":       {w["exit_code"], json.Number("0")},
		"task_input_sha256":      {ib["task_input_sha256"], b["task"]},
		"instruction_manifest":   {ib["instruction_manifest_sha256"], b["manifest"]},
		"assembled_packet":       {ib["assembled_packet_sha256"], b["packet"]},
		"profile_sha256":         {ib["profile_sha256"], b["profile"]},
		"executable_sha256":      {ib["executable_sha256"], b["exe"]},
		"object_format":          {repo["object_format"], f.format},
		"before_head":            {repo["before_head"], f.head},
		"after_head":             {repo["after_head"], f.head},
		"change_manifest_sha256": {repo["change_manifest_sha256"], b["changes"]},
		"matrix_id":              {so["matrix_id"], claude.SourceMatrixID},
		"source manifest":        {so["manifest_sha256"], b["sources"]},
		"coverage":               {so["coverage"], "complete_enumerated_metadata"},
		"suppression_basis":      {so["suppression_basis"], "documented_mechanisms_and_exposed_native_fields"},
		"init_verified":          {ss["init_verified"], true},
		"terminal_verified":      {ss["terminal_verified"], true},
		"plugin_count":           {ss["plugin_count"], json.Number("1")},
	}
	for k, p := range want {
		if p[0] != p[1] {
			t.Errorf("%s: %v, want %v", k, p[0], p[1])
		}
	}
	if mo := ss["memory_observation"]; mo != claude.MemoryEmptyReported && mo != claude.MemorySuppressionOnly {
		t.Errorf("memory_observation %v", mo)
	}
	if _, ok := ss["information_counts"].(map[string]any); !ok {
		t.Errorf("information_counts %v", ss["information_counts"])
	}
	if c, ok := m["completed_at"].(string); !ok || c < b["created"] {
		t.Errorf("completed_at %v", m["completed_at"])
	}
}

// v1Text is the literal version 1 text receipt for a usable exited worker.
func v1Text(t *testing.T, f *fx, outcome string) string {
	t.Helper()
	b := v1Bound(t, f)
	return "BAW worker observations\n" +
		"Run: " + testRunID + "\n" +
		"Authority: not_evaluated\n" +
		"Readiness: not_evaluated\n" +
		"Outcome: " + outcome + "\n" +
		"Worker: state=exited exit_code=0\n" +
		"Inputs: task_input_sha256=" + b["task"] + " instruction_manifest_sha256=" + b["manifest"] +
		" assembled_packet_sha256=" + b["packet"] + "\n" +
		"Profile: profile_sha256=" + b["profile"] + " executable_sha256=" + b["exe"] + "\n" +
		"Sources: matrix_id=" + claude.SourceMatrixID + " manifest_sha256=" + b["sources"] +
		" coverage=complete_enumerated_metadata\n" +
		"Receipt: recorded\n"
}
