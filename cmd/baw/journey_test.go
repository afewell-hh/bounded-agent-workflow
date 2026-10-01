package main

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// Recorded binary journeys run only when -baw-binary names a built artifact:
//
//	go test ./cmd/baw -run TestBinaryJourneys -count=1 -args -baw-binary=PATH -journey-dir=DIR
//
// DIR receives the dummy fixtures and a summary of exit codes and outputs.
// GitHub journeys use a fake gh placed first on PATH; nothing contacts GitHub.
var (
	bawBinary  = flag.String("baw-binary", "", "built baw artifact to exercise")
	journeyDir = flag.String("journey-dir", "", "new directory for journey fixtures and summary")
)

const fakeIssue = `{"number":1,"state":"open","updated_at":"2026-09-30T00:00:00Z","html_url":"https://github.com/afewell-hh/bounded-agent-workflow/issues/1"}`

// detached starts a TERM-ignoring sleep with detached stdio, waits for its
// trap and records its PID.
func detached(pidFile string) string {
	return `(trap '' TERM; sh -c 'echo $PPID' > '` + pidFile + `.tmp'; mv '` + pidFile + `.tmp' '` + pidFile + `'; exec sleep 30) </dev/null >/dev/null 2>&1 &
while [ ! -s '` + pidFile + `' ]; do sleep 0.01; done
`
}

// processGone reports whether the recorded PID has exited; a survivor (a
// child of this journey's own fake) is killed so the journey leaks nothing.
func processGone(t *testing.T, pidFile string) bool {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Errorf("descendant pid not recorded: %v", err)
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		t.Errorf("bad pid %q", data)
		return false
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
	}
	syscall.Kill(pid, syscall.SIGKILL)
	return false
}

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
	run := func(name string, wantCode int, path string, args ...string) string {
		cmd := exec.Command(*bawBinary, args...)
		if path != "" {
			cmd.Env = append(os.Environ(), "PATH="+path)
		}
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
		return out.String() + "\x00" + errb.String()
	}
	text := run("terminal operator fixture", 0, "", "inspect", "--repo", op)
	if !strings.Contains(text, "HEAD: "+head+"\n") || !strings.Contains(text, "Changes: staged=1 unstaged=1 untracked=1 conflicted=0\nLinked worktrees: 0\nSubmodules: 0\n") {
		t.Errorf("terminal journey mismatch")
	}
	js := run("json operator fixture", 0, "", "inspect", "--repo", op, "--json")
	if !strings.Contains(js, `"counts":{"staged":1,"unstaged":1,"untracked":1,"conflicted":0,"linked_worktrees":0,"submodules":0}`) {
		t.Errorf("json journey mismatch")
	}
	if got := run("invalid repo", 1, "", "inspect", "--repo", filepath.Join(*journeyDir, "missing")); got != "\x00baw: repository_unavailable\n" {
		t.Errorf("invalid repo output %q", got)
	}
	if got := run("malformed snapshot", 1, "", "inspect", "--repo", op, "--coordination-file", bad); got != "\x00baw: coordination_invalid\n" {
		t.Errorf("malformed snapshot output %q", got)
	}
	if got := run("invalid usage", 2, "", "inspect"); got != "\x00baw: invalid_usage\n" {
		t.Errorf("usage output %q", got)
	}
	run("help", 0, "", "--help")

	// Staged gitlink add/update/delete under diff.ignoreSubmodules=all:
	// fixture facts fix staged=3 and submodules=3 (keep, upd, new), built
	// with plumbing only.
	gl := filepath.Join(*journeyDir, "gitlink-fixture")
	must := func(_ string, err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tf.Init(home, gl, "sha1"); err != nil {
		t.Fatal(err)
	}
	c1, err := tf.CommitSources(home, gl)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"upd", "del", "keep"} {
		must(tf.Git(home, gl, "update-index", "--add", "--cacheinfo", "160000,"+c1+","+p))
	}
	must(tf.Git(home, gl, "commit", "-q", "-m", "gitlinks"))
	c2, err := tf.OID(home, gl, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	must(tf.Git(home, gl, "config", "diff.ignoreSubmodules", "all"))
	must(tf.Git(home, gl, "update-index", "--add", "--cacheinfo", "160000,"+c1+",new"))
	must(tf.Git(home, gl, "update-index", "--cacheinfo", "160000,"+c2+",upd"))
	must(tf.Git(home, gl, "update-index", "--force-remove", "del"))
	if got := run("staged gitlinks", 0, "", "inspect", "--repo", gl, "--json"); !strings.Contains(got,
		`"counts":{"staged":3,"unstaged":0,"untracked":0,"conflicted":0,"linked_worktrees":0,"submodules":3}`) {
		t.Errorf("gitlink journey mismatch")
	}

	// Fake gh journeys (no network). Each fake records its argv.
	fakeDir := filepath.Join(*journeyDir, "fake-gh")
	os.MkdirAll(fakeDir, 0o700)
	fakePath := fakeDir + ":" + os.Getenv("PATH")
	fake := func(body string) {
		script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + filepath.Join(*journeyDir, "fake-gh-argv.txt") + "'\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(fakeDir, "gh"), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	gh := []string{"inspect", "--repo", op, "--github", "afewell-hh/bounded-agent-workflow#1"}

	fake(`head -c 65536 /dev/zero | tr '\0' x >&2; printf '%s' '` + fakeIssue + `'`)
	if got := run("fake gh stderr exactly 64KiB", 0, fakePath, gh...); !strings.Contains(got, "Coordination: live number=1 state=OPEN") || strings.Contains(got, "xxx") {
		t.Errorf("stderr boundary journey mismatch")
	}
	fake(`head -c 65537 /dev/zero | tr '\0' x >&2; printf '%s' '` + fakeIssue + `'`)
	if got := run("fake gh stderr 64KiB+1", 1, fakePath, gh...); got != "\x00baw: command_output_limit\n" {
		t.Errorf("stderr overflow output %q", got)
	}
	pid1 := filepath.Join(*journeyDir, "normal-descendant.pid")
	fake(detached(pid1) + `printf '%s' '` + fakeIssue + `'`)
	run("fake gh normal exit with detached TERM-ignoring descendant", 0, fakePath, gh...)
	gone1 := processGone(t, pid1)
	summary.WriteString("-- descendant gone after return: " + strconv.FormatBool(gone1) + "\n")
	if !gone1 {
		t.Errorf("normal-exit descendant survived")
	}
	pid2 := filepath.Join(*journeyDir, "overflow-descendant.pid")
	fake(detached(pid2) + `head -c 1048577 /dev/zero; sleep 30`)
	if got := run("fake gh stdout 1MiB+1 with running leader and detached TERM-ignoring descendant", 1, fakePath, gh...); got != "\x00baw: command_output_limit\n" {
		t.Errorf("stdout overflow output %q", got)
	}
	gone2 := processGone(t, pid2)
	summary.WriteString("-- descendant gone after return: " + strconv.FormatBool(gone2) + "\n")
	if !gone2 {
		t.Errorf("overflow descendant survived")
	}
	if err := os.WriteFile(filepath.Join(*journeyDir, "summary.txt"), []byte(summary.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
