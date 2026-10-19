package worker

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	// Interior whitespace is permitted up to exactly 1024 bytes.
	pad := func(n int) string { return "{" + strings.Repeat(" ", n-len(good)) + good[1:] }
	escaped := rep(`"reason":`, `"reason":`)
	if !strings.Contains(escaped, `a`) {
		t.Fatal("escape fixture")
	}
	for name, body := range map[string]string{"padded to 1024": pad(1024), "escaped key": escaped} {
		if !ValidReportObservation([]byte(body), result, testRunID) {
			t.Errorf("%s rejected (len %d)", name, len(body))
		}
	}
	if len(pad(1024)) != 1024 || len(pad(1025)) != 1025 {
		t.Fatal("pad fixture")
	}
	other := strings.Repeat("a", 32)
	bad := map[string]string{
		"1025 bytes":            pad(1025),
		"no trailing newline":   strings.TrimSuffix(good, "\n"),
		"two newlines":          good + "\n",
		"CRLF":                  strings.TrimSuffix(good, "\n") + "\r\n",
		"leading space":         " " + good,
		"leading newline":       "\n" + good,
		"trailing value":        strings.TrimSuffix(good, "\n") + "{}\n",
		"array":                 "[" + strings.TrimSuffix(good, "\n") + "]\n",
		"duplicate reason":      rep(`"reason":"blocked"`, `"reason":"blocked","reason":"blocked"`),
		"escaped duplicate":     rep(`"reason":"blocked"`, `"reason":"blocked","reason":"failed"`),
		"extra key":             rep(`"reason":"blocked"`, `"reason":"blocked","detail":"x"`),
		"missing reason":        rep(`,"reason":"blocked"`, ``),
		"schema string":         rep(`"schema_version":1`, `"schema_version":"1"`),
		"schema float":          rep(`"schema_version":1`, `"schema_version":1.0`),
		"schema 2":              rep(`"schema_version":1`, `"schema_version":2`),
		"schema bool":           rep(`"schema_version":1`, `"schema_version":true`),
		"run uppercase":         rep(testRunID, strings.ToUpper(testRunID)),
		"other run":             rep(testRunID, other),
		"task mismatch":         rep(f.taskSHA, strings.Repeat("6", 64)),
		"task uppercase":        rep(`"task_input_sha256":"`+f.taskSHA, `"task_input_sha256":"`+strings.ToUpper(f.taskSHA)),
		"result hash mismatch":  rep(hexSum(result), strings.Repeat("7", 64)),
		"result hash short":     rep(hexSum(result), hexSum(result)[:63]),
		"result hash null":      rep(`"`+hexSum(result)+`"`, `null`),
		"status completed":      rep(`"status":"not_completed"`, `"status":"completed"`),
		"status number":         rep(`"status":"not_completed"`, `"status":1`),
		"reason none":           rep(`"reason":"blocked"`, `"reason":"none"`),
		"reason empty":          rep(`"reason":"blocked"`, `"reason":""`),
		"reason case":           rep(`"reason":"blocked"`, `"reason":"Blocked"`),
		"reason unknown":        rep(`"reason":"blocked"`, `"reason":"timeout"`),
		"reason null":           rep(`"reason":"blocked"`, `"reason":null`),
		"reason array":          rep(`"reason":"blocked"`, `"reason":["blocked"]`),
		"reason free text":      rep(`"reason":"blocked"`, `"reason":"blocked: see /tmp/x"`),
		"invalid UTF-8 in text": rep(`"reason":"blocked"`, "\"reason\":\"blocked\xff\""),
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
	for _, stage := range []string{"changes.json-random", ReportObservationName + "-random", ReportObservationName + "-dir-sync",
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
		if r := decodeResult(t, out); r.Outcome != c.want {
			t.Errorf("%s: outcome %s", c.name, r.Outcome)
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
	cases := []struct {
		stage            string
		companion, final bool
	}{
		{ReportObservationName + "-write", false, false},
		{ReportObservationName + "-link", false, false},
		{ReportObservationName + "-dir-sync", true, false},
		{"result.json-write", true, false},
		{"deliver", true, true},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		f.stream = reasonStream("blocked")
		f.fault = func(s string) error {
			if s == c.stage {
				return os.ErrInvalid
			}
			return nil
		}
		out, completed, err := f.run(context.Background(), true)
		if !IsCode(err, CodeUncertain) || completed || out != "" || f.runs != 1 {
			t.Fatalf("%s: %v %v %q runs=%d", c.stage, err, completed, out, f.runs)
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
