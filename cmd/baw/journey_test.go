package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Recorded binary journeys run only when -baw-binary names a built artifact:
//
//	go test ./cmd/baw -run TestBinaryJourneys -count=1 -args -baw-binary=PATH -journey-dir=DIR
//
// DIR receives the dummy fixtures and a summary of exit codes and outputs.
var (
	bawBinary  = flag.String("baw-binary", "", "built baw artifact to exercise")
	journeyDir = flag.String("journey-dir", "", "new directory for journey fixtures and summary")
)

func TestBinaryJourneys(t *testing.T) {
	if *bawBinary == "" || *journeyDir == "" {
		t.Skip("binary journeys need -baw-binary and -journey-dir")
	}
	if err := os.Mkdir(*journeyDir, 0o700); err != nil {
		t.Fatalf("journey dir must be new: %v", err)
	}
	home := filepath.Join(*journeyDir, "home")
	os.MkdirAll(home, 0o700)
	op := filepath.Join(*journeyDir, "operator-fixture")
	head, err := tf.Operator(home, op)
	if err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(*journeyDir, "malformed-snapshot.json")
	os.WriteFile(bad, []byte(`{"number":1,"state":"open"`), 0o600)

	var summary strings.Builder
	summary.WriteString("operator fixture HEAD (from fixture setup): " + head + "\n")
	run := func(name string, wantCode int, args ...string) string {
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
		summary.WriteString("== " + name + " exit=" + itoa(code) + "\n-- stdout\n" + out.String() + "-- stderr\n" + errb.String())
		if code != wantCode {
			t.Errorf("%s: exit %d want %d", name, code, wantCode)
		}
		return out.String() + "\x00" + errb.String()
	}
	text := run("terminal operator fixture", 0, "inspect", "--repo", op)
	if !strings.Contains(text, "HEAD: "+head+"\n") || !strings.Contains(text, "Changes: staged=1 unstaged=1 untracked=1 conflicted=0\nLinked worktrees: 0\nSubmodules: 0\n") {
		t.Errorf("terminal journey mismatch")
	}
	js := run("json operator fixture", 0, "inspect", "--repo", op, "--json")
	if !strings.Contains(js, `"counts":{"staged":1,"unstaged":1,"untracked":1,"conflicted":0,"linked_worktrees":0,"submodules":0}`) {
		t.Errorf("json journey mismatch")
	}
	if got := run("invalid repo", 1, "inspect", "--repo", filepath.Join(*journeyDir, "missing")); got != "\x00baw: repository_unavailable\n" {
		t.Errorf("invalid repo output %q", got)
	}
	if got := run("malformed snapshot", 1, "inspect", "--repo", op, "--coordination-file", bad); got != "\x00baw: coordination_invalid\n" {
		t.Errorf("malformed snapshot output %q", got)
	}
	if got := run("invalid usage", 2, "inspect"); got != "\x00baw: invalid_usage\n" {
		t.Errorf("usage output %q", got)
	}
	run("help", 0, "--help")
	if err := os.WriteFile(filepath.Join(*journeyDir, "summary.txt"), []byte(summary.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
