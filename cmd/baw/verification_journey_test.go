package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Verification binary journeys use the same flags as TestBinaryJourneys:
//
//	go test ./cmd/baw -run '^TestVerificationBinaryJourneys$' -count=1 -v -args -baw-binary=PATH -journey-dir=DIR
//
// The fake verifier is this compiled test binary, copied into DIR and
// selected by "-baw-verify-journey-fake MODE". It is dispatched from init so
// the existing TestMain stays unchanged. No shell, PATH lookup, model,
// provider or network is involved; every expected packet is written by hand.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "-baw-verify-journey-fake" {
		os.Exit(verifyJourneyFake(os.Args[2]))
	}
}

const verifyJourneySecret = "DUMMY-VERIFY-JOURNEY-SECRET-93be41"

func verifyJourneyFake(mode string) int {
	if len(os.Environ()) != 4 || os.Getenv("LANG") != "C" || os.Getenv("LC_ALL") != "C" {
		return 97
	}
	switch mode {
	case "pass":
		// Arbitrary stdout PASS text has no meaning and is never shown.
		os.Stdout.WriteString("PASS " + verifyJourneySecret + "\n")
		os.Stderr.WriteString("note " + verifyJourneySecret + "\n")
		return 0
	case "fail":
		os.Stdout.WriteString("FAIL " + verifyJourneySecret + "\n")
		return 4
	case "dirty":
		if os.WriteFile("README.md", []byte("changed by verifier\n"), 0o644) != nil {
			return 96
		}
		return 0
	}
	return 99
}

func TestVerificationBinaryJourneys(t *testing.T) {
	if *bawBinary == "" || *journeyDir == "" {
		t.Skip("binary journeys need -baw-binary and -journey-dir")
	}
	if err := os.Mkdir(*journeyDir, 0o700); err != nil {
		t.Fatalf("journey dir must be new: %v", err)
	}
	jd, err := filepath.EvalSymlinks(*journeyDir)
	if err != nil {
		t.Fatal(err)
	}
	var summary strings.Builder
	defer func() { os.WriteFile(filepath.Join(jd, "summary.txt"), []byte(summary.String()), 0o600) }()
	summary.WriteString("binary sha256: " + fileSHA(t, *bawBinary) + "\n")
	summary.WriteString("fake compiled verifier only; no model, native agent, provider or network\n")

	// Fixed negative controls for the timestamp oracle used below.
	lo := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	hi := time.Date(2026, 10, 4, 12, 0, 5, 0, time.UTC)
	for name, c := range map[string][3]string{
		"impossible": {"2026-02-30T12:00:01Z", "2026-02-30T12:00:01Z", "2026-02-30T12:00:01Z"},
		"malformed":  {"2026-10-04T12:00:01+00:00", "2026-10-04T12:00:01+00:00", "2026-10-04T12:00:02Z"},
		"stale":      {"2026-10-04T11:59:59Z", "2026-10-04T11:59:59Z", "2026-10-04T12:00:01Z"},
		"late":       {"2026-10-04T12:00:01Z", "2026-10-04T12:00:01Z", "2026-10-04T12:00:06Z"},
		"reversed":   {"2026-10-04T12:00:03Z", "2026-10-04T12:00:03Z", "2026-10-04T12:00:02Z"},
		"mismatch":   {"2026-10-04T12:00:01Z", "2026-10-04T12:00:02Z", "2026-10-04T12:00:03Z"},
	} {
		if checkJourneyTimes(c[0], c[1], c[2], lo, hi) == nil {
			t.Fatalf("timestamp oracle accepted %s control", name)
		}
	}
	if err := checkJourneyTimes("2026-10-04T12:00:01Z", "2026-10-04T12:00:01Z", "2026-10-04T12:00:05Z", lo, hi); err != nil {
		t.Fatalf("timestamp oracle rejected valid control: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(jd, "fake-verifier")
	b, err := os.ReadFile(self)
	if err != nil || os.WriteFile(tool, b, 0o700) != nil {
		t.Fatal("copy fake verifier")
	}
	os.Chmod(tool, 0o700)

	run := func(name string, wantCode int, args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(*bawBinary, args...)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		summary.WriteString("== " + name + " exit=" + itoa(code) + "\n-- argv: baw " + strings.Join(args, " ") +
			"\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
		if code != wantCode {
			t.Errorf("%s: exit %d want %d (stderr %q)", name, code, wantCode, errb.String())
		}
		if strings.Contains(out.String()+errb.String(), verifyJourneySecret) {
			t.Errorf("%s: verifier output leaked", name)
		}
		return out.String(), errb.String()
	}
	writePlan := func(path, mode string) []byte {
		body := []byte(`{"schema_version":1,"verifier":{"executable":"` + tool + `","arguments":["-baw-verify-journey-fake","` +
			mode + `"],"timeout_seconds":30}}` + "\n")
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, 0o600)
		return body
	}
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	q := func(s string) string {
		if s == "null" {
			return s
		}
		return `"` + s + `"`
	}
	wantJSON := func(id, outcome, state, code, format, before, after, created, completed string) string {
		return `{"schema_version":1,"run_id":"` + id + `","operation":"run_verify","authority":"not_evaluated",` +
			`"readiness":"not_evaluated","outcome":"` + outcome + `","verification":{"state":"` + state + `","exit_code":` + code +
			`},"repository":{"object_format":"` + format + `","before_head":"` + before + `","after_head":` + q(after) +
			`},"receipt_state":"recorded","created_at":"` + created + `","completed_at":"` + completed + `"}` + "\n"
	}
	wantText := func(id, outcome, state, code, format, before, after string) string {
		u := func(s string) string {
			if s == "null" {
				return "unknown"
			}
			return s
		}
		return "BAW verification observations\nRun: " + id + "\nAuthority: not_evaluated\nReadiness: not_evaluated\nOutcome: " +
			outcome + "\nVerification: state=" + state + " exit_code=" + u(code) + "\nRepository: object_format=" + format +
			" before_head=" + before + " after_head=" + u(after) + "\nReceipt: recorded\n"
	}
	// fieldAt extracts the canonical-position timestamp value following key
	// from raw saved bytes; the value is then checked by checkJourneyTimes.
	fieldAt := func(raw, key string) string {
		i := strings.Index(raw, `"`+key+`":"`)
		if i < 0 {
			return ""
		}
		rest := raw[i+len(key)+4:]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			return ""
		}
		return rest[:j]
	}

	ticket := "https://github.com/example/project/issues/24"
	scope := strings.Repeat("e", 64)
	n := 0
	for _, format := range []string{"sha1", "sha256"} {
		fd := filepath.Join(jd, format)
		home, repo, state := filepath.Join(fd, "home"), filepath.Join(fd, "repo"), filepath.Join(fd, "state")
		for _, d := range []string{fd, home, state} {
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
			os.Chmod(d, 0o700)
		}
		if err := tf.Init(home, repo, format); err != nil {
			t.Fatal(err)
		}
		recordHead, err := tf.CommitSources(home, repo)
		if err != nil {
			t.Fatal(err)
		}
		newID := func(label string) string {
			n++
			id := strings.Repeat("0", 30) + hex.EncodeToString([]byte{byte(n)})
			cur, err := tf.OID(home, repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			run(format+" create "+label, 0, "run", "create", "--state-dir", state, "--run-id", id, "--repo", repo,
				"--ticket", ticket, "--scope-sha256", scope, "--policy-commit", cur, "--json")
			return id
		}
		// Journeys: PASS and failure in text and JSON.
		for _, c := range []struct {
			label, mode, outcome, state, code string
			json                              bool
			exit                              int
		}{
			{"pass-text", "pass", "candidate_verification_passed", "exited", "0", false, 0},
			{"pass-json", "pass", "candidate_verification_passed", "exited", "0", true, 0},
			{"fail-text", "fail", "verification_failed", "exited", "4", false, 1},
			{"fail-json", "fail", "verification_failed", "exited", "4", true, 1},
			{"changed-json", "dirty", "candidate_changed", "exited", "0", true, 1},
		} {
			id := newID(c.label)
			cand, _ := tf.OID(home, repo, "HEAD")
			plan := filepath.Join(fd, "plan-"+c.label+".json")
			planBytes := writePlan(plan, c.mode)
			args := []string{"run", "verify", "--repo", repo, "--state-dir", state, "--run-id", id, "--candidate", cand, "--plan", plan}
			if c.json {
				args = append(args, "--json")
			}
			lo := time.Now()
			out, errOut := run(format+" verify "+c.label, c.exit, args...)
			hi := time.Now()
			wantErr := ""
			if c.exit != 0 {
				wantErr = "baw: verification_failed\n"
			}
			if errOut != wantErr {
				t.Errorf("%s %s: stderr %q", format, c.label, errOut)
			}
			attempt := filepath.Join(state, "verify-v1", id)
			intent, err1 := os.ReadFile(filepath.Join(attempt, "intent.json"))
			result, err2 := os.ReadFile(filepath.Join(attempt, "result.json"))
			if err1 != nil || err2 != nil {
				t.Fatalf("%s %s: saved receipts missing", format, c.label)
			}
			created := fieldAt(string(result), "created_at")
			completed := fieldAt(string(result), "completed_at")
			if err := checkJourneyTimes(fieldAt(string(intent), "created_at"), created, completed, lo, hi); err != nil {
				t.Errorf("%s %s: %v", format, c.label, err)
			}
			after := "null"
			if c.outcome == "candidate_verification_passed" {
				after = cand
			}
			wantResult := wantJSON(id, c.outcome, c.state, c.code, format, cand, after, created, completed)
			if string(result) != wantResult {
				t.Errorf("%s %s: saved result\n%s\nwant\n%s", format, c.label, result, wantResult)
			}
			wantIntent := `{"schema_version":1,"run_id":"` + id + `","record_state":"verification_intent","ticket_url":"` + ticket +
				`","scope_sha256":"` + scope + `","policy_commit":"` + recordHead + `","repository_object_format":"` + format +
				`","repository_head":"` + recordHead + `","candidate_head":"` + cand + `","plan_sha256":"` + sum(planBytes) +
				`","created_at":"` + created + `","authority":"not_evaluated","readiness":"not_evaluated"}` + "\n"
			if string(intent) != wantIntent {
				t.Errorf("%s %s: saved intent\n%s\nwant\n%s", format, c.label, intent, wantIntent)
			}
			wantOut := wantText(id, c.outcome, c.state, c.code, format, cand, after)
			if c.json {
				wantOut = wantResult
			}
			if out != wantOut {
				t.Errorf("%s %s: stdout\n%s\nwant\n%s", format, c.label, out, wantOut)
			}
			if strings.Contains(string(intent)+string(result), verifyJourneySecret) {
				t.Errorf("%s %s: secret saved", format, c.label)
			}
			if c.mode == "dirty" {
				if _, err := tf.Git(home, repo, "checkout", "--", "README.md"); err != nil {
					t.Fatal(err)
				}
			}
			// Replay of the same ID is refused without a new start.
			if c.label == "pass-json" {
				o, e := run(format+" replay", 1, args...)
				if o != "" || e != "baw: verification_exists\n" {
					t.Errorf("%s replay: %q %q", format, o, e)
				}
			}
		}
		// Precondition: a dirty candidate is refused before any attempt.
		id := newID("dirty-precondition")
		cand, _ := tf.OID(home, repo, "HEAD")
		plan := filepath.Join(fd, "plan-precondition.json")
		writePlan(plan, "pass")
		os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x\n"), 0o644)
		o, e := run(format+" dirty precondition", 1, "run", "verify", "--repo", repo, "--state-dir", state, "--run-id", id,
			"--candidate", cand, "--plan", plan)
		if o != "" || e != "baw: candidate_not_clean\n" {
			t.Errorf("%s dirty: %q %q", format, o, e)
		}
		if _, err := os.Lstat(filepath.Join(state, "verify-v1", id)); err == nil {
			t.Errorf("%s dirty: attempt created", format)
		}
		os.Remove(filepath.Join(repo, "untracked.txt"))
		// A later descendant commit is verified under its own new record ID.
		os.WriteFile(filepath.Join(repo, "later.txt"), []byte("later\n"), 0o644)
		tf.Git(home, repo, "add", "later.txt")
		tf.Git(home, repo, "commit", "-q", "-m", "later")
		desc, _ := tf.OID(home, repo, "HEAD")
		did := newID("descendant")
		dplan := filepath.Join(fd, "plan-descendant.json")
		writePlan(dplan, "pass")
		out, _ := run(format+" descendant", 0, "run", "verify", "--repo", repo, "--state-dir", state, "--run-id", did,
			"--candidate", desc, "--plan", dplan, "--json")
		if !strings.Contains(out, `"before_head":"`+desc+`","after_head":"`+desc+`"`) {
			t.Errorf("%s descendant: %s", format, out)
		}
	}
}
