package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Diagnose binary journeys use the same flags as TestBinaryJourneys:
//
//	go test ./cmd/baw -run TestRunDiagnoseBinaryJourneys -count=1 -args -baw-binary=PATH -journey-dir=DIR
//
// DIR receives a new private state root, dummy SHA-1 and SHA-256 fixtures and
// summary.txt with every argv, exit code, stdout and stderr. Each run is a
// joined child bounded to 10 seconds.
const (
	diagSecretTicket = "https://github.com/sparrow-secret-owner/indigo-secret-repo/issues/6061"
	diagSecretScope  = "5ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2"
)

func diagReport(id, ns, final string, c [6]int) string {
	return fmt.Sprintf("BAW run diagnosis\nRun: %s\nNamespace: %s\nFinal record: %s\n"+
		"Staging: total=%d valid=%d invalid_record=%d record_too_large=%d unsupported_record_version=%d linked_to_final=%d\n"+
		"Snapshot: non_atomic\nDurability: unknown\nAuthority: not_evaluated\nRuntime state: unknown\n"+
		"Process ownership: unknown\nRemote freshness: unknown\nReservation ownership: unknown\n",
		id, ns, final, c[0], c[1], c[2], c[3], c[4], c[5])
}

func diagObject(id, ns, final string, c [6]int) string {
	return fmt.Sprintf(`{"schema_version":1,"operation":"diagnose","run_id":"%s","namespace":"%s","final_record":"%s",`+
		`"staging":{"total":%d,"valid":%d,"invalid_record":%d,"record_too_large":%d,"unsupported_record_version":%d,"linked_to_final":%d},`+
		`"snapshot":"non_atomic","durability":"unknown","authority":"not_evaluated","runtime_state":"unknown",`+
		`"process_ownership":"unknown","remote_freshness":"unknown","reservation_ownership":"unknown"}`+"\n",
		id, ns, final, c[0], c[1], c[2], c[3], c[4], c[5])
}

// stateMeta lists path, mode, owner, inode, link count, size, mtime and bytes
// of everything under dir, excluding access time.
func stateMeta(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		st := fi.Sys().(*syscall.Stat_t)
		line := fmt.Sprintf("%s %v uid=%d ino=%d nlink=%d size=%d mtime=%d", strings.TrimPrefix(p, dir),
			fi.Mode(), st.Uid, st.Ino, st.Nlink, fi.Size(), fi.ModTime().UnixNano())
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			line += " sha256=" + fileSHA(t, p) + " len=" + itoa(len(b))
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestRunDiagnoseBinaryJourneys(t *testing.T) {
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
	os.WriteFile(filepath.Join(root, "run.json"), []byte("pre-existing manual receipt\n"), 0o600)
	ns := filepath.Join(root, "records-v1")
	summary.WriteString("state root (new, 0700): " + root + "\n")

	// Poisoned PATH: marker-writing git/gh only.
	poison := filepath.Join(*journeyDir, "poisoned-bin")
	marks := filepath.Join(*journeyDir, "poison-markers")
	os.MkdirAll(poison, 0o700)
	os.MkdirAll(marks, 0o700)
	for _, n := range []string{"git", "gh"} {
		os.WriteFile(filepath.Join(poison, n), []byte("#!/bin/sh\ntouch '"+filepath.Join(marks, n)+"'\nexit 1\n"), 0o700)
	}
	poisoned := []string{"PATH=" + poison, "HOME=" + home}

	run := func(name string, wantCode int, env []string, args ...string) (string, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, *bawBinary, args...)
		cmd.Env = env
		cmd.WaitDelay = time.Second
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
		if ctx.Err() != nil {
			t.Fatalf("%s: not finished within 10s", name)
		}
		summary.WriteString("== " + name + " exit=" + itoa(code) + " elapsed_ms=" + itoa(int(time.Since(start).Milliseconds())) +
			"\n-- argv: baw " + strings.Join(args, " ") + "\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
		if code != wantCode {
			t.Errorf("%s: exit %d want %d", name, code, wantCode)
		}
		return out.String(), errb.String()
	}
	// diag runs text and JSON forms with poisoned PATH, compares exact
	// oracles and checks saved bytes/metadata/listing are unchanged.
	diag := func(name, dir, id, nsState, final string, c [6]int, leaks ...string) {
		before := stateMeta(t, dir)
		for _, js := range []bool{false, true} {
			args := []string{"run", "diagnose", "--state-dir", dir, "--run-id", id}
			want := diagReport(id, nsState, final, c)
			label := name + " text"
			if js {
				args = append(args, "--json")
				want = diagObject(id, nsState, final, c)
				label = name + " json"
			}
			out, errs := run(label, 0, poisoned, args...)
			if out != want || errs != "" {
				t.Errorf("%s: stdout %q stderr %q", label, out, errs)
			}
			for _, s := range append(leaks, dir, ".pending", "manual receipt") {
				if strings.Contains(out+errs, s) {
					t.Errorf("%s leaked %q", label, s)
				}
			}
			if after := stateMeta(t, dir); after != before {
				t.Errorf("%s changed saved data/metadata:\n%s\n--\n%s", label, before, after)
			}
		}
		summary.WriteString("-- " + name + ": saved bytes, listing and metadata (excl. atime) unchanged\n")
	}
	wantErr := func(name string, code int, want string, args ...string) {
		out, errs := run(name, code, poisoned, args...)
		if out != "" || errs != "baw: "+want+"\n" {
			t.Errorf("%s: stdout %q stderr %q", name, out, errs)
		}
	}

	// Help and usage.
	if out, _ := run("diagnose help", 0, poisoned, "run", "diagnose", "--help"); !strings.Contains(out, "\n  baw run diagnose --state-dir DIR --run-id ID [--json]\n") {
		t.Errorf("diagnose help lacks usage")
	}
	wantErr("help mixed with action flag", 2, "invalid_usage", "run", "diagnose", "--help", "--json")
	wantErr("uppercase run id", 2, "invalid_usage", "run", "diagnose", "--state-dir", root, "--run-id", strings.ToUpper(journeyID1))
	wantErr("missing run id", 2, "invalid_usage", "run", "diagnose", "--state-dir", root)
	diag("absent namespace", root, journeyID1, "absent", "missing", [6]int{})
	if _, err := os.Lstat(ns); err == nil {
		t.Fatal("diagnose created the namespace")
	}

	type fixture struct{ id, format, repo, head, policy string }
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
		policy := strings.Repeat("7", len(head))
		summary.WriteString(f.format + " fixture HEAD (from fixture setup): " + head + "\n")
		fx = append(fx, fixture{f.id, f.format, repo, head, policy})
		run(f.format+" create", 0, nil, "run", "create", "--state-dir", root, "--run-id", f.id, "--repo", repo,
			"--ticket", diagSecretTicket, "--scope-sha256", diagSecretScope, "--policy-commit", policy)
	}
	// Repositories removed: diagnose never needs them.
	for _, f := range fx {
		os.RemoveAll(f.repo)
	}
	summary.WriteString("-- fixture repositories removed before diagnose\n")
	for _, f := range fx {
		final := filepath.Join(ns, f.id+".json")
		matches, _ := filepath.Glob(filepath.Join(ns, ".pending-"+f.id+"-*"))
		if len(matches) != 1 {
			t.Fatalf("%s staging %v", f.format, matches)
		}
		a, _ := os.Lstat(final)
		b, _ := os.Lstat(matches[0])
		if !os.SameFile(a, b) {
			t.Fatalf("%s fixture: staging not linked", f.format)
		}
		summary.WriteString("-- " + f.format + " final and staging share an inode (fixture Lstat)\n")
		rnd := strings.TrimPrefix(filepath.Base(matches[0]), ".pending-"+f.id+"-")
		leaks := []string{"sparrow", "indigo", "6061", diagSecretScope, f.policy, f.head, rnd, f.format, "dummy-secret"}
		diag(f.format+" created record", root, f.id, "present", "valid", [6]int{1, 1, 0, 0, 0, 1}, leaks...)

		// Separate identical copy, mixed categories, ignored unsafe names.
		stored, _ := os.ReadFile(final)
		put := func(rnd string, data []byte) {
			p := filepath.Join(ns, ".pending-"+f.id+"-"+rnd)
			os.WriteFile(p, data, 0o600)
			os.Chmod(p, 0o600)
		}
		put("eeeeeeeeeeeeeeeeeeeeeeeeeeeeee01", stored)
		put("eeeeeeeeeeeeeeeeeeeeeeeeeeeeee02", stored[:len(stored)/2])
		put("eeeeeeeeeeeeeeeeeeeeeeeeeeeeee03", []byte(strings.Repeat(" ", 16385)))
		put("eeeeeeeeeeeeeeeeeeeeeeeeeeeeee04", []byte(`{"schema_version":7,"run_id":"dummy-secret-xyz"}`))
		os.WriteFile(filepath.Join(ns, ".pending-"+f.id+"-dummy-secret-name"), []byte("dummy-secret-content"), 0o644)
		os.Symlink(filepath.Join(root, "run.json"), filepath.Join(ns, ".pending-"+f.id+"-EEEEEEEEEEEEEEEEEEEEEEEEEEEEEE05"))
		syscall.Mkfifo(filepath.Join(ns, "dummy-secret-fifo"), 0o600)
		diag(f.format+" mixed staging", root, f.id, "present", "valid", [6]int{5, 2, 1, 1, 1, 1}, append(leaks, "eeeeeeee")...)
	}
	if ents, _ := os.ReadDir(marks); len(ents) != 0 {
		t.Errorf("git/gh invoked by diagnose")
	}
	summary.WriteString("-- poison markers present: " + itoa(len(func() []os.DirEntry { e, _ := os.ReadDir(marks); return e }())) + "\n")

	// Special files and permissions at relevant names, each a bounded child.
	special := filepath.Join(*journeyDir, "special-state")
	os.Mkdir(special, 0o700)
	os.Chmod(special, 0o700)
	sns := filepath.Join(special, "records-v1")
	os.Mkdir(sns, 0o700)
	os.Chmod(sns, 0o700)
	final := filepath.Join(sns, journeyCorrupt+".json")
	syscall.Mkfifo(final, 0o600)
	wantErr("final FIFO", 1, "unsafe_state_path", "run", "diagnose", "--state-dir", special, "--run-id", journeyCorrupt)
	os.Remove(final)
	os.Symlink(filepath.Join(root, "run.json"), final)
	wantErr("final symlink", 1, "unsafe_state_path", "run", "diagnose", "--state-dir", special, "--run-id", journeyCorrupt, "--json")
	os.Remove(final)
	os.WriteFile(final, []byte("dummy-secret-xyz"), 0o644)
	os.Chmod(final, 0o644)
	wantErr("final mode 0644", 1, "state_permissions", "run", "diagnose", "--state-dir", special, "--run-id", journeyCorrupt)
	syscall.Mkfifo(filepath.Join(sns, ".pending-"+journeyID1+"-00000000000000000000000000000001"), 0o600)
	wantErr("pending FIFO", 1, "unsafe_state_path", "run", "diagnose", "--state-dir", special, "--run-id", journeyID1, "--json")
	summary.WriteString("-- special-file probes finished as joined children within 10s\n")

	// The docs/operator/run-records.md diagnose example.
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
	run("docs example create", 0, nil, "run", "create", "--state-dir", docRoot, "--run-id", journeyID1,
		"--repo", docRepo, "--ticket", "https://github.com/OWNER/REPO/issues/1", "--scope-sha256", journeyScope,
		"--policy-commit", strings.Repeat("5", 40))
	diag("docs example diagnose", docRoot, journeyID1, "present", "valid", [6]int{1, 1, 0, 0, 0, 1})

	ents, _ := os.ReadDir(ns)
	summary.WriteString("-- namespace entry count: " + itoa(len(ents)) + "\n")
}
