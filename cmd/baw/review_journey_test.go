package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Review binary journeys use the same flags as TestBinaryJourneys:
//
//	go test ./cmd/baw -run '^TestReviewBinaryJourneys$' -count=1 -v -args -baw-binary=PATH -journey-dir=DIR
//
// The fake worker, verifier and reviewer are this compiled test binary,
// copied into DIR and selected by "-baw-review-journey-fake MODE COUNTER".
// They are dispatched from init so the existing TestMain stays unchanged. No
// shell, PATH lookup, model, provider or network is involved; every expected
// packet is written by hand. This is a fake-program observation, not a model
// or native agent validation.
func init() {
	if len(os.Args) == 4 && os.Args[1] == "-baw-review-journey-fake" {
		os.Exit(reviewJourneyFake(os.Args[2], os.Args[3]))
	}
}

const reviewJourneySecret = "DUMMY-JOURNEY-SECRET-0c41e7"

func reviewJourneyFake(mode, counter string) int {
	role := strings.SplitN(mode, "-", 2)[0]
	f, err := os.OpenFile(filepath.Join(counter, role), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 90
	}
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	f.Close()
	if len(os.Environ()) != 4 || os.Getenv("LANG") != "C" || os.Getenv("LC_ALL") != "C" {
		return 97
	}
	if strings.HasPrefix(mode, "reviewer-sleep-") {
		// Holds an exclusive lock for its whole life (released by the kernel
		// however it ends), publishes a start marker, and an end marker only
		// if its finite sleep completes.
		lf, err := os.OpenFile(filepath.Join(counter, "lock-"+mode), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil || syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			return 92
		}
		journeyMarker(counter, "started-"+mode)
		time.Sleep(3 * time.Second)
		journeyMarker(counter, "end-"+mode)
		os.Stdout.WriteString(`{"schema_version":1,"verdict":"PASS"}` + "\n")
		return 0
	}
	switch mode {
	case "worker-noop", "verifier-ok":
		return 0
	case "worker-write":
		if os.WriteFile("worker-output.txt", []byte("dummy worker change, committed later by the coordinator\n"), 0o644) != nil {
			return 96
		}
		return 0
	case "reviewer-pass":
		os.Stdout.WriteString(`{"schema_version":1,"verdict":"PASS"}` + "\n")
	case "reviewer-fixes":
		os.Stdout.WriteString(`{"schema_version":1,"verdict":"REQUIRED_FIXES"}` + "\n")
	case "reviewer-secret-stderr":
		os.Stderr.WriteString("finding prose " + reviewJourneySecret + "\n")
		os.Stdout.WriteString(`{"schema_version":1,"verdict":"PASS"}` + "\n")
	case "reviewer-prose":
		os.Stdout.WriteString("Looks fine " + reviewJourneySecret + "\n")
	case "reviewer-nonzero":
		os.Stdout.WriteString(`{"schema_version":1,"verdict":"PASS"}` + "\n")
		return 5
	default:
		return 99
	}
	return 0
}

func journeyMarker(counter, name string) {
	tmp := filepath.Join(counter, "."+name+".tmp")
	os.WriteFile(tmp, []byte("marker\n"), 0o600)
	os.Rename(tmp, filepath.Join(counter, name))
}

var journeyTimeRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

func TestReviewBinaryJourneys(t *testing.T) {
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
	summary.WriteString("fake compiled programs only; no model, native agent, provider or network\n")

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(jd, "fake-tool")
	b, err := os.ReadFile(self)
	if err != nil || os.WriteFile(tool, b, 0o700) != nil {
		t.Fatal("copy fake tool")
	}
	os.Chmod(tool, 0o700)

	run := func(name string, wantCode int, stdout *os.File, args ...string) (string, string) {
		t.Helper()
		cmd := exec.Command(*bawBinary, args...)
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		if stdout != nil {
			cmd.Stdout = stdout
		}
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
		if strings.Contains(out.String()+errb.String(), reviewJourneySecret) {
			t.Errorf("%s: reviewer output leaked", name)
		}
		return out.String(), errb.String()
	}
	starts := func(counter, role string) int {
		b, _ := os.ReadFile(filepath.Join(counter, role))
		return strings.Count(string(b), "\n")
	}
	writePlan := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Chmod(path, 0o600)
	}
	cmdJSON := func(mode, counter string) string {
		return `{"executable":"` + tool + `","arguments":["-baw-review-journey-fake","` + mode + `","` + counter + `"],"timeout_seconds":30}`
	}
	type packet struct {
		Outcome    string `json:"outcome"`
		CreatedAt  string `json:"created_at"`
		Completed  string `json:"completed_at"`
		Repository struct {
			ObjectFormat string  `json:"object_format"`
			BeforeHead   string  `json:"before_head"`
			AfterHead    *string `json:"after_head"`
		} `json:"repository"`
	}
	decode := func(s string) packet {
		var p packet
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			t.Fatalf("decode %q: %v", s, err)
		}
		if !journeyTimeRE.MatchString(p.CreatedAt) || !journeyTimeRE.MatchString(p.Completed) || p.Completed < p.CreatedAt {
			t.Fatalf("timestamps %q %q", p.CreatedAt, p.Completed)
		}
		return p
	}
	wantJSON := func(id, outcome, state, code, verdict, format, before, after, created, completed string) string {
		q := func(s string) string {
			if s == "null" {
				return s
			}
			return `"` + s + `"`
		}
		return `{"schema_version":1,"run_id":"` + id + `","operation":"run_review","authority":"not_evaluated",` +
			`"readiness":"not_evaluated","outcome":"` + outcome + `","reviewer":{"state":"` + state + `","exit_code":` + code +
			`},"verdict":` + q(verdict) + `,"repository":{"object_format":"` + format + `","before_head":"` + before +
			`","after_head":` + q(after) + `},"receipt_state":"recorded","created_at":"` + created + `","completed_at":"` +
			completed + `"}` + "\n"
	}
	wantText := func(id, outcome, state, code, verdict, format, before, after string) string {
		return "BAW review observations\nRun: " + id + "\nAuthority: not_evaluated\nReadiness: not_evaluated\nOutcome: " +
			outcome + "\nReviewer: state=" + state + " exit_code=" + code + " verdict=" + verdict +
			"\nRepository: object_format=" + format + " before_head=" + before + " after_head=" + after + "\nReceipt: recorded\n"
	}

	ticket := "https://github.com/example/project/issues/21"
	scope := strings.Repeat("d", 64)
	for _, format := range []string{"sha1", "sha256"} {
		fd := filepath.Join(jd, format)
		home, repo, state, counter := filepath.Join(fd, "home"), filepath.Join(fd, "repo"), filepath.Join(fd, "state"), filepath.Join(fd, "counter")
		for _, d := range []string{fd, home, state, counter} {
			if err := os.Mkdir(d, 0o700); err != nil {
				t.Fatal(err)
			}
			os.Chmod(d, 0o700)
		}
		if err := tf.Init(home, repo, format); err != nil {
			t.Fatal(err)
		}
		head, err := tf.CommitSources(home, repo)
		if err != nil {
			t.Fatal(err)
		}
		// One run record and execute attempt per scenario, as an operator would.
		prepareID := func(id, label, worker string) string {
			cur, err := tf.OID(home, repo, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			run(format+" create "+label, 0, nil, "run", "create", "--state-dir", state, "--run-id", id, "--repo", repo,
				"--ticket", ticket, "--scope-sha256", scope, "--policy-commit", cur, "--json")
			ep := filepath.Join(fd, "execute-plan-"+label+".json")
			writePlan(ep, `{"schema_version":1,"worker":`+cmdJSON(worker, counter)+`,"verification":`+cmdJSON("verifier-ok", counter)+`}`)
			out, _ := run(format+" execute "+label, 0, nil, "run", "execute", "--repo", repo, "--state-dir", state,
				"--run-id", id, "--plan", ep, "--json")
			p := decode(out)
			if p.Outcome != "verification_passed" || p.Repository.AfterHead == nil {
				t.Fatalf("execute %s: %s", label, out)
			}
			return *p.Repository.AfterHead
		}
		prepare := func(n int, worker string) (id, after string) {
			id = strings.Repeat(strconv.Itoa(n), 32)
			return id, prepareID(id, itoa(n), worker)
		}
		// savedTimes reads created_at from the separately saved review intent
		// and completed_at from the saved result, and requires both inside the
		// wall-clock window the test observed around the command, so the
		// hand-written packets do not take their timestamps on trust.
		savedTimes := func(id string, lo, hi time.Time) (string, string) {
			var in, res struct {
				CreatedAt   string `json:"created_at"`
				CompletedAt string `json:"completed_at"`
			}
			ib, err1 := os.ReadFile(filepath.Join(state, "review-v1", id, "intent.json"))
			rb, err2 := os.ReadFile(filepath.Join(state, "review-v1", id, "result.json"))
			if err1 != nil || err2 != nil || json.Unmarshal(ib, &in) != nil || json.Unmarshal(rb, &res) != nil {
				t.Fatalf("saved review %s unreadable", id)
			}
			c, err1 := time.Parse(time.RFC3339, in.CreatedAt)
			d, err2 := time.Parse(time.RFC3339, res.CompletedAt)
			if err1 != nil || err2 != nil || c.Before(lo.Truncate(time.Second)) || d.Before(c) || d.After(hi) ||
				!journeyTimeRE.MatchString(in.CreatedAt) || !journeyTimeRE.MatchString(res.CompletedAt) {
				t.Fatalf("saved times %q %q outside [%v, %v]", in.CreatedAt, res.CompletedAt, lo, hi)
			}
			return in.CreatedAt, res.CompletedAt
		}
		savedResult := func(id string) string {
			b, err := os.ReadFile(filepath.Join(state, "review-v1", id, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			return string(b)
		}
		reviewPlan := func(mode string) string {
			p := filepath.Join(fd, "review-plan-"+mode+".json")
			writePlan(p, `{"schema_version":1,"reviewer":`+cmdJSON(mode, counter)+`}`)
			return p
		}
		review := func(name string, code int, id, cand, plan string, asJSON bool, stdout *os.File) (string, string) {
			args := []string{"run", "review", "--repo", repo, "--state-dir", state, "--run-id", id, "--candidate", cand, "--plan", plan}
			if asJSON {
				args = append(args, "--json")
			}
			return run(format+" "+name, code, stdout, args...)
		}

		// 1. Matching candidate, JSON PASS: D1 same-ID comparison.
		id1, after1 := prepare(1, "worker-noop")
		out, errb := review("review pass json", 0, id1, head, reviewPlan("reviewer-pass"), true, nil)
		p := decode(out)
		if out != wantJSON(id1, "review_passed", "exited", "0", "PASS", format, head, head, p.CreatedAt, p.Completed) || errb != "" {
			t.Errorf("%s pass json: %q %q", format, out, errb)
		}
		if p.Repository.BeforeHead != after1 {
			t.Errorf("%s: D1 same-ID comparison failed", format)
		}
		summary.WriteString("D1 " + format + ": review.before_head=" + p.Repository.BeforeHead + " execute.after_head=" + after1 + " -> same ID\n")
		if b, _ := os.ReadFile(filepath.Join(state, "review-v1", id1, "result.json")); string(b) != out {
			t.Errorf("%s: result.json differs from --json output", format)
		}
		// Replay of the same ID never starts another reviewer.
		n := starts(counter, "reviewer")
		out, errb = review("replay", 1, id1, head, reviewPlan("reviewer-pass"), true, nil)
		if out != "" || errb != "baw: review_exists\n" || starts(counter, "reviewer") != n {
			t.Errorf("%s replay: %q %q", format, out, errb)
		}

		// 2. Text REQUIRED_FIXES, exit 1 with review_failed.
		id2, _ := prepare(2, "worker-noop")
		lo := time.Now().UTC()
		out, errb = review("review fixes text", 1, id2, head, reviewPlan("reviewer-fixes"), false, nil)
		hi := time.Now().UTC()
		if out != wantText(id2, "review_required_fixes", "exited", "0", "REQUIRED_FIXES", format, head, head) || errb != "baw: review_failed\n" {
			t.Errorf("%s fixes text: %q %q", format, out, errb)
		}
		c, d := savedTimes(id2, lo, hi)
		if got := savedResult(id2); got != wantJSON(id2, "review_required_fixes", "exited", "0", "REQUIRED_FIXES", format, head, head, c, d) {
			t.Errorf("%s fixes text: saved result %q", format, got)
		}

		// 2b. JSON REQUIRED_FIXES, exit 1 with review_failed; stdout and the
		// raw saved result are both the hand-written packet.
		idFixesJSON := strings.Repeat("a", 32)
		prepareID(idFixesJSON, "fixes-json", "worker-noop")
		lo = time.Now().UTC()
		out, errb = review("review fixes json", 1, idFixesJSON, head, reviewPlan("reviewer-fixes"), true, nil)
		hi = time.Now().UTC()
		c, d = savedTimes(idFixesJSON, lo, hi)
		wantFixes := wantJSON(idFixesJSON, "review_required_fixes", "exited", "0", "REQUIRED_FIXES", format, head, head, c, d)
		if out != wantFixes || errb != "baw: review_failed\n" {
			t.Errorf("%s fixes json: %q %q", format, out, errb)
		}
		if got := savedResult(idFixesJSON); got != wantFixes {
			t.Errorf("%s fixes json: saved result %q", format, got)
		}

		// 3. Later committed descendant: different IDs, verification not inherited.
		id3, after3 := prepare(3, "worker-write")
		if _, err := tf.Git(home, repo, "add", "worker-output.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := tf.Git(home, repo, "commit", "-q", "-m", "coordinator commits worker output"); err != nil {
			t.Fatal(err)
		}
		desc, err := tf.OID(home, repo, "HEAD")
		if err != nil || desc == after3 {
			t.Fatalf("descendant %v", err)
		}
		out, errb = review("review descendant text", 0, id3, desc, reviewPlan("reviewer-pass"), false, nil)
		if out != wantText(id3, "review_passed", "exited", "0", "PASS", format, desc, desc) || errb != "" {
			t.Errorf("%s descendant: %q %q", format, out, errb)
		}
		summary.WriteString("D1 " + format + ": review.before_head=" + desc + " execute.after_head=" + after3 +
			" -> different IDs; execute verification does not cover the candidate\n")
		head = desc

		// 4. Negative and uncertain paths.
		id4, _ := prepare(4, "worker-noop")
		os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("x\n"), 0o644)
		n = starts(counter, "reviewer")
		out, errb = review("dirty candidate", 1, id4, head, reviewPlan("reviewer-pass"), true, nil)
		if out != "" || errb != "baw: candidate_not_clean\n" || starts(counter, "reviewer") != n {
			t.Errorf("%s dirty: %q %q", format, out, errb)
		}
		os.Remove(filepath.Join(repo, "untracked.txt"))
		out, errb = review("nonzero overrides PASS", 1, id4, head, reviewPlan("reviewer-nonzero"), true, nil)
		if p := decode(out); out != wantJSON(id4, "reviewer_failed", "exited", "5", "null", format, head, "null", p.CreatedAt, p.Completed) ||
			errb != "baw: review_failed\n" {
			t.Errorf("%s nonzero: %q %q", format, out, errb)
		}
		id5, _ := prepare(5, "worker-noop")
		out, errb = review("prose with dummy secret", 1, id5, head, reviewPlan("reviewer-prose"), false, nil)
		if out != wantText(id5, "reviewer_unverified", "exited", "0", "unknown", format, head, "unknown") || errb != "baw: review_failed\n" {
			t.Errorf("%s prose: %q %q", format, out, errb)
		}
		id6, _ := prepare(6, "worker-noop")
		out, errb = review("stderr dummy secret", 0, id6, head, reviewPlan("reviewer-secret-stderr"), true, nil)
		if p := decode(out); out != wantJSON(id6, "review_passed", "exited", "0", "PASS", format, head, head, p.CreatedAt, p.Completed) {
			t.Errorf("%s stderr secret: %q %q", format, out, errb)
		}
		// Delivery failure after publication: stdout is a read-only file.
		id7, _ := prepare(7, "worker-noop")
		ro := filepath.Join(fd, "readonly-stdout")
		os.WriteFile(ro, nil, 0o600)
		rf, err := os.Open(ro)
		if err != nil {
			t.Fatal(err)
		}
		out, errb = review("delivery failure", 1, id7, head, reviewPlan("reviewer-pass"), true, rf)
		rf.Close()
		if errb != "baw: review_uncertain\n" {
			t.Errorf("%s uncertain: %q", format, errb)
		}
		if b, err := os.ReadFile(filepath.Join(state, "review-v1", id7, "result.json")); err != nil || !strings.Contains(string(b), `"review_passed"`) {
			t.Errorf("%s uncertain: result not retained", format)
		}
		// Seven started reviews plus the JSON REQUIRED_FIXES scenario (2b).
		if starts(counter, "reviewer") != 8 {
			t.Errorf("%s: reviewer starts %d want 8", format, starts(counter, "reviewer"))
		}

		// 5. SIGINT (JSON) and SIGTERM (text) sent to the built binary only
		// after the reviewer's start marker is visible. The binary is this
		// test's own child, signalled through its exec handle and always
		// joined; the reviewer is never signalled by the test. Its end is
		// established by the kernel releasing the lock it held.
		for i, sg := range []struct {
			name string
			sig  syscall.Signal
			json bool
		}{{"INT", syscall.SIGINT, true}, {"TERM", syscall.SIGTERM, false}} {
			id, _ := prepare(8+i, "worker-noop")
			mode := "reviewer-sleep-" + sg.name
			args := []string{"run", "review", "--repo", repo, "--state-dir", state, "--run-id", id, "--candidate", head,
				"--plan", reviewPlan(mode)}
			if sg.json {
				args = append(args, "--json")
			}
			cmd := exec.Command(*bawBinary, args...)
			var out, errb strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &errb
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			seen := false
			for end := time.Now().Add(20 * time.Second); time.Now().Before(end) && !seen; time.Sleep(10 * time.Millisecond) {
				_, err := os.Lstat(filepath.Join(counter, "started-"+mode))
				seen = err == nil
			}
			if seen {
				cmd.Process.Signal(sg.sig)
			}
			var werr error
			select {
			case werr = <-done:
			case <-time.After(30 * time.Second):
				cmd.Process.Kill()
				werr = <-done
				t.Errorf("%s %s: binary did not finish after the signal", format, sg.name)
			}
			if !seen {
				t.Fatalf("%s %s: reviewer start marker not seen", format, sg.name)
			}
			code := 0
			if ee, ok := werr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			summary.WriteString("== " + format + " SIG" + sg.name + " after reviewer start exit=" + itoa(code) +
				"\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
			res, rerr := os.ReadFile(filepath.Join(state, "review-v1", id, "result.json"))
			if rerr != nil {
				t.Fatalf("%s %s: no durable result", format, sg.name)
			}
			p := decode(string(res))
			wj := wantJSON(id, "reviewer_unverified", "unverified", "null", "null", format, head, "null", p.CreatedAt, p.Completed)
			want := wj
			if !sg.json {
				want = wantText(id, "reviewer_unverified", "unverified", "unknown", "unknown", format, head, "unknown")
			}
			if code != 1 || errb.String() != "baw: review_failed\n" || out.String() != want || string(res) != wj {
				t.Errorf("%s %s: exit %d stdout %q stderr %q", format, sg.name, code, out.String(), errb.String())
			}
			released := false
			for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && !released; time.Sleep(10 * time.Millisecond) {
				if lf, err := os.OpenFile(filepath.Join(counter, "lock-"+mode), os.O_RDWR, 0); err == nil {
					if syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
						released = true
					}
					lf.Close()
				}
			}
			if !released {
				t.Errorf("%s %s: reviewer lock not released", format, sg.name)
			}
			_, endErr := os.Lstat(filepath.Join(counter, "end-"+mode))
			summary.WriteString("-- reviewer lock released=" + strconv.FormatBool(released) + " end marker=" + strconv.FormatBool(endErr == nil) + "\n")
			if st, err := tf.Git(home, repo, "status", "--porcelain", "--untracked-files=all"); err != nil || st != "" {
				t.Errorf("%s %s: candidate changed: %q", format, sg.name, st)
			}
			if h, _ := tf.OID(home, repo, "HEAD"); h != head {
				t.Errorf("%s %s: HEAD moved", format, sg.name)
			}
		}
		if starts(counter, "reviewer") != 10 {
			t.Errorf("%s: reviewer starts %d want 10", format, starts(counter, "reviewer"))
		}
	}
}
