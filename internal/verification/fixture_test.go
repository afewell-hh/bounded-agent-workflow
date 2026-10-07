package verification

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

const (
	testID      = "0123456789abcdef0123456789abcdef"
	testTicket  = "https://github.com/example/project/issues/24"
	fixedNow    = "2026-10-04T12:00:00Z"
	dummySecret = "DUMMY-SECRET-5e1b7d20-not-a-real-token"
)

var testScope = strings.Repeat("5", 64)

// TestMain doubles as the compiled fake verifier: with argv
// "-baw-verify-fake MODE COUNTER" it never runs tests. Every verifier start
// appends one line to COUNTER/verifier, so tests count starts independently
// of verification output.
func TestMain(m *testing.M) {
	if len(os.Args) == 4 && os.Args[1] == "-baw-verify-fake" {
		os.Exit(fakeVerifier(os.Args[2], os.Args[3]))
	}
	if len(os.Args) == 10 && os.Args[1] == "-baw-verify-signal" {
		os.Exit(signalChild(os.Args[2], os.Args[3], os.Args[4], os.Args[5], os.Args[6], os.Args[7], os.Args[8], os.Args[9]))
	}
	if len(os.Args) == 6 && os.Args[1] == "-baw-verify-special" {
		os.Exit(specialChild(os.Args[2], os.Args[3], os.Args[4], os.Args[5]))
	}
	if len(os.Args) == 4 && os.Args[1] == "-baw-verify-lockhold" {
		os.Exit(lockHoldVerifier(os.Args[2], os.Args[3]))
	}
	if len(os.Args) == 3 && os.Args[1] == "-baw-verify-sockbind" {
		os.Exit(sockBindChild(os.Args[2]))
	}
	proc.GracePeriod, proc.JoinBound = 300*time.Millisecond, 300*time.Millisecond
	os.Exit(m.Run())
}

func fakeGit(args ...string) error {
	cmd := exec.Command("/usr/bin/git", append([]string{"-c", "user.name=Fake", "-c", "user.email=fake@example.invalid",
		"-c", "core.excludesFile=/dev/null"}, args...)...)
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	return cmd.Run()
}

func fakeGitCmd(args ...string) *exec.Cmd {
	cmd := exec.Command("/usr/bin/git", append([]string{"-c", "user.name=Fake", "-c", "user.email=fake@example.invalid",
		"-c", "core.excludesFile=/dev/null"}, args...)...)
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	return cmd
}

func fakeGitOut(args ...string) (string, error) {
	b, err := fakeGitCmd(args...).Output()
	return string(b), err
}

// marker atomically publishes a marker file.
func marker(counter, name string) {
	tmp := filepath.Join(counter, "."+name+".tmp")
	os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0o600)
	os.Rename(tmp, filepath.Join(counter, name))
}

// emptyPrivateDir reports whether p is an empty current-user 0700 directory.
func emptyPrivateDir(p string) bool {
	fi, err := os.Lstat(p)
	if err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		return false
	}
	if st := fi.Sys().(*syscall.Stat_t); st.Uid != uint32(os.Getuid()) {
		return false
	}
	names, err := os.ReadDir(p)
	return err == nil && len(names) == 0
}

func fakeVerifier(mode, counter string) int {
	f, err := os.OpenFile(filepath.Join(counter, "verifier"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 90
	}
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	f.Close()
	out := os.Stdout.WriteString
	switch mode {
	case "ok":
		return 0
	case "pass-prose":
		out("PASS all checks green " + dummySecret + "\n")
		os.Stderr.WriteString("stderr " + dummySecret + "\n")
		return 0
	case "pass-nonzero":
		out("PASS\n")
		return 3
	case "exit255":
		return 255
	case "env":
		env := os.Environ()
		sort.Strings(env)
		home, tmp := os.Getenv("HOME"), os.Getenv("TMPDIR")
		want := []string{"HOME=" + home, "LANG=C", "LC_ALL=C", "TMPDIR=" + tmp}
		if strings.Join(env, "\n") != strings.Join(want, "\n") || filepath.Base(home) != "home" || filepath.Base(tmp) != "tmp" ||
			filepath.Dir(home) != filepath.Dir(tmp) || filepath.Base(filepath.Dir(home)) != "verifier" ||
			!emptyPrivateDir(home) || !emptyPrivateDir(tmp) {
			return 97
		}
		wd, _ := os.Getwd()
		if _, err := os.Stat(filepath.Join(wd, ".git")); err != nil {
			return 98
		}
		if b, err := os.ReadFile("/dev/stdin"); err != nil || len(b) != 0 {
			return 96
		}
		return 0
	case "signal":
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
		time.Sleep(5 * time.Second)
		return 0
	case "sleep":
		time.Sleep(5 * time.Second)
		return 0
	case "stdout-65536":
		out(strings.Repeat("x", 65536))
	case "stdout-65537":
		out(strings.Repeat("x", 65537))
	case "stderr-65536":
		os.Stderr.WriteString(strings.Repeat("x", 65536))
	case "stderr-65537":
		os.Stderr.WriteString(strings.Repeat("x", 65537))
	case "hold-pipe":
		// A detached grandchild in its own session inherits stdout, so actual
		// EOF is not reached promptly; it exits by itself after a finite sleep.
		cmd := exec.Command("/bin/sleep", "3")
		cmd.Stdout = os.Stdout
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if cmd.Start() != nil {
			return 91
		}
		return 0
	case "mutate-tracked":
		return code(os.WriteFile("README.md", []byte("changed\n"), 0o644))
	case "mutate-index":
		if os.WriteFile("README.md", []byte("staged\n"), 0o644) != nil {
			return 91
		}
		return code(fakeGit("add", "README.md"))
	case "mutate-untracked":
		return code(os.WriteFile("new-file.txt", []byte("new\n"), 0o644))
	case "mutate-commit":
		if os.WriteFile("README.md", []byte("committed\n"), 0o644) != nil || fakeGit("add", "README.md") != nil {
			return 91
		}
		return code(fakeGit("commit", "-q", "-m", "verifier commit"))
	case "mutate-transient":
		// Edit and restore within the run: not observable afterwards.
		b, err := os.ReadFile("README.md")
		if err != nil || os.WriteFile("README.md", []byte("transient\n"), 0o644) != nil {
			return 91
		}
		return code(os.WriteFile("README.md", b, 0o644))
	case "mutate-ignored":
		return code(os.WriteFile("ignored.log", []byte("ignored\n"), 0o644))
	case "mutate-unborn":
		return code(fakeGit("symbolic-ref", "HEAD", "refs/heads/unborn-by-verifier"))
	case "mutate-diverged":
		// Amending gives a sibling of the candidate, not a descendant.
		return code(fakeGit("commit", "-q", "--amend", "--allow-empty", "-m", "diverged by verifier"))
	case "mutate-conflict":
		out, err := fakeGitOut("hash-object", "-w", "README.md")
		if err != nil {
			return 91
		}
		oid := strings.TrimSpace(out)
		info := "0 " + strings.Repeat("0", len(oid)) + "\tREADME.md\n"
		for _, stage := range []string{"1", "2", "3"} {
			info += "100644 " + oid + " " + stage + "\tREADME.md\n"
		}
		cmd := fakeGitCmd("update-index", "--index-info")
		cmd.Stdin = strings.NewReader(info)
		return code(cmd.Run())
	case "mutate-top-replace":
		// The original top directory (and its inode) is kept at top.old; a
		// new copy is put at the original path.
		wd, err := os.Getwd()
		if err != nil || os.Rename(wd, wd+".old") != nil {
			return 91
		}
		return code(fakeGit("clone", "-q", "--no-hardlinks", wd+".old", wd))
	case "mutate-format":
		// Same physical top, repository object format changed in place.
		out, err := fakeGitOut("rev-parse", "--show-object-format")
		if err != nil {
			return 91
		}
		other := map[string]string{"sha1": "sha256", "sha256": "sha1"}[strings.TrimSpace(out)]
		if other == "" || fakeGit("config", "core.repositoryformatversion", "1") != nil {
			return 92
		}
		return code(fakeGit("config", "extensions.objectFormat", other))
	case "started-sleep":
		marker(counter, "started")
		time.Sleep(4 * time.Second)
		marker(counter, "finished")
		return 0
	default:
		return 99
	}
	return 0
}

// lockHoldVerifier is a finite fake verifier: it records its start, takes the
// exclusive fixture lock COUNTER/lock, publishes NONCE to COUNTER/ready and
// holds the lock until COUNTER/release appears (at most 8s). It then removes
// the lock, publishes NONCE to COUNTER/exited and exits by itself.
func lockHoldVerifier(counter, nonce string) int {
	f, err := os.OpenFile(filepath.Join(counter, "verifier"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 90
	}
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	f.Close()
	lock := filepath.Join(counter, "lock")
	l, err := os.OpenFile(lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 91
	}
	l.WriteString(nonce)
	l.Close()
	publishFile(filepath.Join(counter, "ready"), nonce)
	for i := 0; i < 800; i++ {
		if _, err := os.Lstat(filepath.Join(counter, "release")); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Remove(lock)
	publishFile(filepath.Join(counter, "exited"), nonce)
	return 0
}

// publishFile atomically publishes data at name.
func publishFile(name, data string) {
	tmp := name + ".tmp"
	os.WriteFile(tmp, []byte(data), 0o600)
	os.Rename(tmp, name)
}

// sockBindChild binds a unix socket at the relative NAME in its working
// directory (a private test directory), keeps the socket file and exits.
func sockBindChild(name string) int {
	l, err := net.Listen("unix", name)
	if err != nil {
		os.Stdout.WriteString("ERR " + err.Error() + "\n")
		return 1
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	os.Stdout.WriteString("bound\n")
	return 0
}

func code(err error) int {
	if err != nil {
		return 91
	}
	return 0
}

type fx struct {
	t                                            *testing.T
	base, home, repo, state, plan, tool, counter string
	format, head                                 string
}

func resolvedTemp(t *testing.T) string {
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mkdir0700(t *testing.T, p string) {
	t.Helper()
	if err := os.Mkdir(p, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o700); err != nil {
		t.Fatal(err)
	}
}

func write0600(t *testing.T, p string, data []byte) {
	t.Helper()
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
}

// newFx builds a committed fixture repository, a private state directory
// with a run record only (no execute or review attempts), a private copy of
// this test binary as the verifier and a plan running it with mode.
func newFx(t *testing.T, format, mode string) *fx {
	t.Helper()
	base := resolvedTemp(t)
	f := &fx{t: t, base: base, home: filepath.Join(base, "home"), repo: filepath.Join(base, "repo"),
		state: filepath.Join(base, "state"), plan: filepath.Join(base, "plan.json"),
		tool: filepath.Join(base, "verifier-tool"), counter: filepath.Join(base, "counter"), format: format}
	mkdir0700(t, f.home)
	mkdir0700(t, f.counter)
	if err := tf.Init(f.home, f.repo, format); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head, err := tf.CommitSources(f.home, f.repo)
	if err != nil {
		t.Fatal(err)
	}
	f.head = head
	mkdir0700(t, f.state)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.tool, b, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(f.tool, 0o700)
	f.writeRecord(head)
	f.writePlan(mode, 20)
	return f
}

func (f *fx) recordJSON(head, format string) string {
	return fmt.Sprintf(`{"schema_version":1,"run_id":"%s","record_state":"recorded","ticket_url":"%s","scope_sha256":"%s",`+
		`"policy_commit":"%s","repository_object_format":"%s","repository_head":"%s","created_at":"2026-10-04T09:00:00Z"}`+"\n",
		testID, testTicket, testScope, strings.Repeat("c", len(head)), format, head)
}

func (f *fx) writeRecord(head string) {
	f.writeRecordFormat(head, f.format)
}

func (f *fx) writeRecordFormat(head, format string) {
	ns := filepath.Join(f.state, "records-v1")
	if _, err := os.Lstat(ns); err != nil {
		mkdir0700(f.t, ns)
	}
	p := filepath.Join(ns, testID+".json")
	os.Remove(p)
	write0600(f.t, p, []byte(f.recordJSON(head, format)))
}

func (f *fx) planJSON(mode string, timeout int) string {
	return `{"schema_version":1,"verifier":{"executable":"` + f.tool + `","arguments":["-baw-verify-fake","` + mode +
		`","` + f.counter + `"],"timeout_seconds":` + strconv.Itoa(timeout) + `}}` + "\n"
}

func (f *fx) writePlan(mode string, timeout int) {
	os.Remove(f.plan)
	write0600(f.t, f.plan, []byte(f.planJSON(mode, timeout)))
}

func (f *fx) git(args ...string) string {
	f.t.Helper()
	out, err := tf.Git(f.home, f.repo, args...)
	if err != nil {
		f.t.Fatal(err)
	}
	return out
}

func (f *fx) starts() int {
	b, _ := os.ReadFile(filepath.Join(f.counter, "verifier"))
	return strings.Count(string(b), "\n")
}

func (f *fx) attempt() string { return filepath.Join(f.state, Namespace, testID) }

func (f *fx) req(candidate string, asJSON bool) Request {
	return Request{Repo: f.repo, StateDir: f.state, RunID: testID, Candidate: candidate, Plan: f.plan, JSON: asJSON}
}

// descriptors records every descriptor the package opens.
type descriptors struct {
	mu    sync.Mutex
	files []*os.File
}

func (d *descriptors) add(f *os.File) { d.mu.Lock(); d.files = append(d.files, f); d.mu.Unlock() }

// assertClosed checks each observed descriptor object is actually closed.
func (d *descriptors) assertClosed(t *testing.T, what string) {
	t.Helper()
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, f := range d.files {
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s: descriptor for %s still open (%v)", what, filepath.Base(f.Name()), err)
		}
	}
}

// run executes Verify with fixed time and descriptor observation, asserts
// every descriptor was closed and restores the seams.
func (f *fx) run(ctx context.Context, req Request) (string, bool, error) {
	f.t.Helper()
	d := &descriptors{}
	opened = d.add
	now = func() time.Time { t, _ := time.Parse(time.RFC3339, fixedNow); return t }
	defer func() { opened, now = nil, time.Now }()
	var out bytes.Buffer
	passed, err := Verify(ctx, req, &out)
	d.assertClosed(f.t, "verify")
	return out.String(), passed, err
}

var timeNow = time.Now

func fixedTime() time.Time { t, _ := time.Parse(time.RFC3339, fixedNow); return t }

func q(s string) string {
	if s == "null" {
		return s
	}
	return `"` + s + `"`
}

// wantResultJSON is the hand-written result oracle.
func wantResultJSON(outcome, state, code, format, before, after string) string {
	return `{"schema_version":1,"run_id":"` + testID + `","operation":"run_verify","authority":"not_evaluated",` +
		`"readiness":"not_evaluated","outcome":"` + outcome + `","verification":{"state":"` + state + `","exit_code":` + code +
		`},"repository":{"object_format":"` + format + `","before_head":"` + before + `","after_head":` + q(after) +
		`},"receipt_state":"recorded","created_at":"` + fixedNow + `","completed_at":"` + fixedNow + `"}` + "\n"
}

// wantText is the hand-written eight-line text oracle.
func wantText(outcome, state, code, format, before, after string) string {
	u := func(s string) string {
		if s == "null" {
			return "unknown"
		}
		return s
	}
	return "BAW verification observations\nRun: " + testID + "\nAuthority: not_evaluated\nReadiness: not_evaluated\nOutcome: " +
		outcome + "\nVerification: state=" + state + " exit_code=" + u(code) +
		"\nRepository: object_format=" + format + " before_head=" + before + " after_head=" + u(after) + "\nReceipt: recorded\n"
}

// wantIntent is the hand-written intent oracle.
func (f *fx) wantIntent(candidate string) string {
	return `{"schema_version":1,"run_id":"` + testID + `","record_state":"verification_intent","ticket_url":"` + testTicket +
		`","scope_sha256":"` + testScope + `","policy_commit":"` + strings.Repeat("c", len(f.head)) + `","repository_object_format":"` +
		f.format + `","repository_head":"` + f.head + `","candidate_head":"` + candidate + `","plan_sha256":"` +
		sha(readFile(f.t, f.plan)) + `","created_at":"` + fixedNow + `","authority":"not_evaluated","readiness":"not_evaluated"}` + "\n"
}

func sha(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }

func readFile(t *testing.T, p string) []byte {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// snapshot records every entry below dir with type, mode, owner, size,
// inode, link count, mtime and content hash.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			lines = append(lines, p+" error")
			return nil
		}
		fi, err := os.Lstat(p)
		if err != nil {
			lines = append(lines, p+" lstat-error")
			return nil
		}
		st := fi.Sys().(*syscall.Stat_t)
		h := ""
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			h = sha(b)
		}
		lines = append(lines, fmt.Sprintf("%s %v %d %d %d %d %d %s", p, fi.Mode(), st.Uid, fi.Size(), st.Ino, st.Nlink,
			fi.ModTime().UnixNano(), h))
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// assertPre checks a pre-acquisition failure: the code, empty output, zero
// starts and an unchanged state directory.
func (f *fx) assertPre(t *testing.T, name string, ctx context.Context, req Request, wantCode string) {
	t.Helper()
	before := snapshot(t, f.state)
	out, passed, err := f.run(ctx, req)
	if err == nil || err.Error() != wantCode || out != "" || passed {
		t.Errorf("%s: err %v out %q want %s", name, err, out, wantCode)
	}
	if n := f.starts(); n != 0 {
		t.Errorf("%s: %d verifier starts", name, n)
	}
	if after := snapshot(t, f.state); after != before {
		t.Errorf("%s: state changed\nbefore:\n%s\nafter:\n%s", name, before, after)
	}
}

// listing returns the sorted names in dir.
func listing(t *testing.T, dir string) string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		return "error"
	}
	var n []string
	for _, e := range es {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, ".pending-intent-") && len(name) == len(".pending-intent-")+32:
			name = ".pending-intent-X"
		case strings.HasPrefix(name, ".pending-result-") && len(name) == len(".pending-result-")+32:
			name = ".pending-result-X"
		}
		n = append(n, name)
	}
	sort.Strings(n)
	return strings.Join(n, ",")
}
