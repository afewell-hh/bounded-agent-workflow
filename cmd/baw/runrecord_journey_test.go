package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Run-record binary journeys use the same flags as TestBinaryJourneys:
//
//	go test ./cmd/baw -run TestRunRecordBinaryJourneys -count=1 -args -baw-binary=PATH -journey-dir=DIR
//
// DIR receives a new private state root, dummy SHA-1 and SHA-256 fixtures and
// summary.txt with every argv, exit code, stdout and stderr.
const (
	journeyID1     = "00112233445566778899aabbccddeeff"
	journeyID256   = "0123456789abcdef0123456789abcdef"
	journeyCorrupt = "fedcba9876543210fedcba9876543210"
	journeyTicket  = "https://github.com/afewell-hh/bounded-agent-workflow/issues/9"
	journeyScope   = "6464646464646464646464646464646464646464646464646464646464646464"
)

func fileSHA(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestRunRecordBinaryJourneys(t *testing.T) {
	if *bawBinary == "" || *journeyDir == "" {
		t.Skip("binary journeys need -baw-binary and -journey-dir")
	}
	if err := os.Mkdir(*journeyDir, 0o700); err != nil {
		t.Fatalf("journey dir must be new: %v", err)
	}
	var summary strings.Builder
	defer func() {
		os.WriteFile(filepath.Join(*journeyDir, "summary.txt"), []byte(summary.String()), 0o600)
	}()
	summary.WriteString("binary sha256: " + fileSHA(t, *bawBinary) + "\n")
	home := filepath.Join(*journeyDir, "home")
	os.MkdirAll(home, 0o700)
	root := filepath.Join(*journeyDir, "state")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(root, 0o700)
	marker := filepath.Join(root, "run.json")
	os.WriteFile(marker, []byte("pre-existing manual receipt\n"), 0o600)
	summary.WriteString("state root (new, 0700): " + root + "\n")

	run := func(name string, wantCode int, env []string, args ...string) (string, string) {
		cmd := exec.Command(*bawBinary, args...)
		cmd.Env = env
		var out, errb strings.Builder
		cmd.Stdout, cmd.Stderr = &out, &errb
		start := time.Now()
		err := cmd.Run()
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
	wantErr := func(name string, code int, want string, args ...string) {
		out, errs := run(name, code, nil, args...)
		if out != "" || errs != "baw: "+want+"\n" {
			t.Errorf("%s: stdout %q stderr %q", name, out, errs)
		}
	}
	create := func(id, repo, policy string, extra ...string) []string {
		return append([]string{"run", "create", "--state-dir", root, "--run-id", id, "--repo", repo,
			"--ticket", journeyTicket, "--scope-sha256", journeyScope, "--policy-commit", policy}, extra...)
	}
	report := func(op, id, policy, head, format, created string) string {
		return "BAW run record\nOperation: " + op + "\nRun: " + id + "\nRecord state: recorded\nTicket: " + journeyTicket +
			"\nScope SHA-256: " + journeyScope + "\nPolicy reference: " + policy + "\nRecorded HEAD: " + head +
			"\nObject format: " + format + "\nCreated at: " + created +
			"\nAuthority: not_evaluated\nRemote freshness: unknown\nRuntime state: unknown\nProcess ownership: unknown\nReservation ownership: unknown\n"
	}

	// Help and usage.
	for _, a := range [][]string{{"run", "--help"}, {"run", "create", "--help"}, {"status", "--help"}} {
		if out, _ := run("help "+strings.Join(a, " "), 0, nil, a...); !strings.Contains(out, "baw status --state-dir DIR --run-id ID [--json]") {
			t.Errorf("help %v", a)
		}
	}
	wantErr("help mixed with action flag", 2, "invalid_usage", "status", "--help", "--json")
	wantErr("uppercase run id", 2, "invalid_usage", "status", "--state-dir", root, "--run-id", strings.ToUpper(journeyID1))
	wantErr("unknown flag", 2, "invalid_usage", "run", "create", "--bogus")
	wantErr("status before any record", 1, "record_missing", "status", "--state-dir", root, "--run-id", journeyID1)
	if _, err := os.Lstat(filepath.Join(root, "records-v1")); err == nil {
		t.Fatal("status created the namespace")
	}

	type fixture struct{ id, format, repo, head, policy, opposite string }
	var fx []fixture
	for _, f := range []struct{ id, format string }{{journeyID1, "sha1"}, {journeyID256, "sha256"}} {
		repo := filepath.Join(*journeyDir, f.format+"-fixture")
		if err := tf.Init(home, repo, f.format); err != nil {
			t.Fatalf("%s fixture unavailable (mandatory gate): %v", f.format, err)
		}
		head, err := tf.CommitSources(home, repo)
		if err != nil {
			t.Fatal(err)
		}
		os.WriteFile(filepath.Join(repo, "untracked.txt"), []byte("dirty\n"), 0o644)
		w := 40
		if f.format == "sha256" {
			w = 64
		}
		// Well-shaped policy references that name no object in the fixture.
		policy := strings.Repeat("5", w)
		opposite := strings.Repeat("5", 104-w)
		summary.WriteString(f.format + " fixture HEAD (from fixture setup): " + head + "\n")
		fx = append(fx, fixture{f.id, f.format, repo, head, policy, opposite})
	}

	for _, f := range fx {
		wantErr(f.format+" opposite-width policy", 2, "invalid_usage", create(f.id, f.repo, f.opposite)...)
		upper := strings.ToUpper(f.policy[:0] + "aBc" + f.policy[3:])
		lower := strings.ToLower(upper)
		out, _ := run(f.format+" create text (uppercase policy input)", 0, nil, create(f.id, f.repo, upper)...)
		created := ""
		for _, l := range strings.Split(out, "\n") {
			if c, ok := strings.CutPrefix(l, "Created at: "); ok {
				created = c
			}
		}
		if out != report("create", f.id, lower, f.head, f.format, created) || len(created) != 20 {
			t.Errorf("%s create text mismatch", f.format)
		}
		js, _ := run(f.format+" status json", 0, nil, "status", "--state-dir", root, "--run-id", f.id, "--json")
		var top map[string]any
		if err := json.Unmarshal([]byte(js), &top); err != nil || len(top) != 8 {
			t.Fatalf("%s status json %q", f.format, js)
		}
		rec, _ := top["record"].(map[string]any)
		want := map[string]any{"schema_version": float64(1), "run_id": f.id, "record_state": "recorded", "ticket_url": journeyTicket,
			"scope_sha256": journeyScope, "policy_commit": lower, "repository_object_format": f.format,
			"repository_head": f.head, "created_at": created}
		if len(rec) != 9 {
			t.Errorf("%s record keys %v", f.format, rec)
		}
		for k, v := range want {
			if rec[k] != v {
				t.Errorf("%s %s = %v want %v", f.format, k, rec[k], v)
			}
		}
		if top["operation"] != "status" || top["authority"] != "not_evaluated" || top["remote_freshness"] != "unknown" ||
			top["runtime_state"] != "unknown" || top["process_ownership"] != "unknown" || top["reservation_ownership"] != "unknown" {
			t.Errorf("%s status envelope %v", f.format, top)
		}
		final := filepath.Join(root, "records-v1", f.id+".json")
		h := fileSHA(t, final)
		summary.WriteString("-- " + f.format + " record sha256: " + h + "\n")
		dup := create(f.id, f.repo, f.policy)
		dup[len(dup)-3] = strings.Repeat("0", 64)
		wantErr(f.format+" duplicate create, different scope", 1, "record_exists", dup...)
		if fileSHA(t, final) != h {
			t.Errorf("%s duplicate changed record", f.format)
		}
		f := f
		defer func() {
			// Saved observation: status without the repository and without Git/gh.
			poison := filepath.Join(*journeyDir, "poisoned-bin")
			marks := filepath.Join(*journeyDir, "poison-markers")
			os.MkdirAll(poison, 0o700)
			os.MkdirAll(marks, 0o700)
			for _, n := range []string{"git", "gh"} {
				os.WriteFile(filepath.Join(poison, n), []byte("#!/bin/sh\ntouch '"+filepath.Join(marks, n)+"'\nexit 1\n"), 0o700)
			}
			out, _ := run(f.format+" status text with repo removed and git/gh poisoned PATH", 0, []string{"PATH=" + poison, "HOME=" + home}, "status", "--state-dir", root, "--run-id", f.id)
			if out != report("status", f.id, strings.ToLower("aBc"+f.policy[3:]), f.head, f.format, created) {
				t.Errorf("%s git-free status mismatch", f.format)
			}
			if ents, _ := os.ReadDir(marks); len(ents) != 0 {
				t.Errorf("git/gh invoked by status")
			}
			summary.WriteString("-- poison markers present: " + itoa(len(func() []os.DirEntry { e, _ := os.ReadDir(marks); return e }())) + "\n")
		}()
	}
	// Remove the fixture repositories before the deferred Git-free status runs.
	defer func() {
		for _, f := range fx {
			os.RemoveAll(f.repo)
			if _, err := os.Lstat(f.repo); err == nil {
				t.Errorf("fixture repo still present")
			}
		}
		summary.WriteString("-- fixture repositories removed before Git-free status\n")
	}()

	// The docs/operator/run-records.md example: new private root, empty-commit repo.
	docRoot := filepath.Join(*journeyDir, "doc-example-state")
	os.Mkdir(docRoot, 0o700)
	os.Chmod(docRoot, 0o700)
	docRepo := filepath.Join(*journeyDir, "doc-example-repo")
	if err := tf.Init(home, docRepo, "sha1"); err != nil {
		t.Fatal(err)
	}
	if _, err := tf.Git(home, docRepo, "commit", "-q", "--allow-empty", "-m", "dummy"); err != nil {
		t.Fatal(err)
	}
	docHead, err := tf.OID(home, docRepo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	docPolicy := strings.Repeat("5", 40)
	out, _ := run("docs example create", 0, nil, "run", "create", "--state-dir", docRoot, "--run-id", journeyID1,
		"--repo", docRepo, "--ticket", "https://github.com/OWNER/REPO/issues/1", "--scope-sha256", journeyScope, "--policy-commit", docPolicy)
	if !strings.Contains(out, "Ticket: https://github.com/OWNER/REPO/issues/1\n") || !strings.Contains(out, "Recorded HEAD: "+docHead+"\n") {
		t.Errorf("docs example create mismatch")
	}
	if js, _ := run("docs example status json", 0, nil, "status", "--state-dir", docRoot, "--run-id", journeyID1, "--json"); !strings.Contains(js, `"repository_head":"`+docHead+`"`) {
		t.Errorf("docs example status mismatch")
	}

	// Corrupt record: fixed error, empty stdout.
	run("create record to corrupt", 0, nil, create(journeyCorrupt, fx[0].repo, fx[0].policy, "--json")...)
	os.WriteFile(filepath.Join(root, "records-v1", journeyCorrupt+".json"), []byte(`{"schema_version":1,"run_id":"dummy-secret-xyz`+"\x1b[2J"+`"`), 0o600)
	wantErr("status corrupt record", 1, "invalid_record", "status", "--state-dir", root, "--run-id", journeyCorrupt)

	if b, _ := os.ReadFile(marker); string(b) != "pre-existing manual receipt\n" {
		t.Errorf("pre-existing entry changed")
	}
	ents, _ := os.ReadDir(filepath.Join(root, "records-v1"))
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	summary.WriteString("-- namespace entries: " + strings.Join(names, " ") + "\n")
}
