package execution

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Registered read-only fixtures, read by tests only and never modified; baw
// never reads them. Their layouts are those documented in internal/state.
const (
	socketStateEnv = "BAW_TEST_SOCKET_STATE_DIR"
	diagSpecialEnv = "BAW_TEST_DIAG_SPECIAL_FIXTURES"
	execSetgidEnv  = "BAW_TEST_EXEC_SETGID_PLAN"
	fixtureNS      = "records-v1"
	fixtureID      = "00112233445566778899aabbccddeeff"
)

// probe runs one operation in a child of the test binary so the parent can
// always bound and join it.
func probe(mode, arg string) int {
	switch mode {
	case "-baw-probe-read":
		_, _, err := ReadPlan(arg)
		var e *Error
		switch {
		case err == nil:
			os.Stdout.WriteString("ok")
		case errors.As(err, &e):
			os.Stdout.WriteString(string(e.Code))
		default:
			os.Stdout.WriteString("other")
		}
		return 0
	case "-baw-probe-bind":
		// The parent sets the working directory; the relative name avoids
		// the sun_path length limit of long temporary paths.
		l, err := net.Listen("unix", arg)
		if err != nil {
			return 2
		}
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		l.Close()
		return 0
	}
	return 3
}

// child runs the test binary once with a 10 second bound and always waits
// for it; the returned process state proves it was joined.
func child(t *testing.T, dir string, args ...string) (string, *os.ProcessState) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], args...)
	cmd.Dir = dir
	cmd.WaitDelay = time.Second
	out, _ := cmd.Output()
	if cmd.ProcessState == nil {
		t.Fatalf("probe child %v did not start", args)
	}
	if ctx.Err() != nil {
		t.Fatalf("probe child %v exceeded 10s", args)
	}
	return string(out), cmd.ProcessState
}

func childRead(t *testing.T, path string) string {
	t.Helper()
	out, ps := child(t, "", "-baw-probe-read", path)
	if !ps.Exited() || ps.ExitCode() != 0 {
		t.Fatalf("read child %s: %v", path, ps)
	}
	return out
}

type meta struct {
	mode  os.FileMode
	size  int64
	mtime time.Time
	ino   uint64
}

func metaOf(t *testing.T, p string) meta {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return meta{fi.Mode(), fi.Size(), fi.ModTime(), fi.Sys().(*syscall.Stat_t).Ino}
}

// socketPlan returns an actual Unix socket path and a preservation check.
func socketPlan(t *testing.T, dir string) (string, func()) {
	t.Helper()
	var sock string
	if base := os.Getenv(socketStateEnv); base != "" {
		sock = filepath.Join(base, fixtureNS, fixtureID+".json")
	} else {
		sdir := filepath.Join(dir, "sockdir")
		if err := os.Mkdir(sdir, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, ps := child(t, sdir, "-baw-probe-bind", "p"); !ps.Exited() || ps.ExitCode() != 0 {
			t.Fatalf("socket bind child failed: %v (set %s to the registered fixture where binding is unavailable)", ps, socketStateEnv)
		}
		sock = filepath.Join(sdir, "p")
	}
	before := metaOf(t, sock)
	if before.mode&os.ModeSocket == 0 {
		t.Fatalf("socket fixture %s is not a socket: %v", sock, before.mode)
	}
	return sock, func() {
		if metaOf(t, sock) != before {
			t.Fatalf("socket fixture %s changed", sock)
		}
	}
}

// setuidPlan returns an actual mode 04600 regular file and a preservation
// check.
func setuidPlan(t *testing.T, dir string) (string, func()) {
	t.Helper()
	var p string
	if base := os.Getenv(diagSpecialEnv); base != "" {
		p = filepath.Join(base, "final", fixtureNS, fixtureID+".json")
	} else {
		p = filepath.Join(dir, "setuid.json")
		os.WriteFile(p, []byte(planWith(okCmd)), 0o600)
		os.Chmod(p, 0o600|os.ModeSetuid)
	}
	before := metaOf(t, p)
	if !before.mode.IsRegular() || before.mode&os.ModeSetuid == 0 || before.mode.Perm() != 0o600 {
		t.Fatalf("setuid fixture %s not observed as 04600: %v (set %s where chmod cannot set it)", p, before.mode, diagSpecialEnv)
	}
	return p, func() {
		if metaOf(t, p) != before {
			t.Fatalf("setuid fixture %s changed", p)
		}
	}
}

// fileSnap is the preserved state of a supplied fixture: bytes, metadata
// other than atime, and its parent directory listing.
type fileSnap struct {
	sum          string
	mode         os.FileMode
	size         int64
	dev          int32
	ino          uint64
	nlink        uint16
	uid          uint32
	mtime, ctime syscall.Timespec
	listing      string
}

func snapFile(t *testing.T, p string) fileSnap {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatalf("fixture %s: %v", p, err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("fixture %s: %v", p, err)
	}
	ents, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatalf("fixture parent %s: %v", p, err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name()+":"+e.Type().String())
	}
	return fileSnap{sha256Hex(b), fi.Mode(), fi.Size(), st.Dev, st.Ino, st.Nlink, st.Uid,
		st.Mtimespec, st.Ctimespec, strings.Join(names, "/")}
}

// setgidPlan returns an actual mode 02600 regular file and a preservation
// check. With BAW_TEST_EXEC_SETGID_PLAN set it uses that registered
// read-only fixture as is: it must already be an absolute, current-user,
// single-link regular file of mode exactly 02600 (no setuid, sticky or
// execute bit), otherwise the test fails; it is never modified. Unset, the
// test creates and observes its own.
func setgidPlan(t *testing.T, dir string) (string, func()) {
	t.Helper()
	p := os.Getenv(execSetgidEnv)
	if p == "" {
		p = filepath.Join(dir, "setgid.json")
		if err := os.WriteFile(p, []byte(planWith(okCmd)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o600|os.ModeSetgid); err != nil {
			t.Fatal(err)
		}
	} else if !filepath.IsAbs(p) {
		t.Fatalf("%s is not absolute", execSetgidEnv)
	}
	before := snapFile(t, p)
	if !before.mode.IsRegular() || before.mode&(os.ModeSetgid|os.ModeSetuid|os.ModeSticky) != os.ModeSetgid ||
		before.mode.Perm() != 0o600 || before.uid != uint32(os.Getuid()) || before.nlink != 1 {
		t.Fatalf("setgid fixture %s not observed as current-user single-link 02600 regular file: %v uid=%d nlink=%d (set %s where chmod cannot set it)",
			p, before.mode, before.uid, before.nlink, execSetgidEnv)
	}
	return p, func() {
		if snapFile(t, p) != before {
			t.Fatalf("setgid fixture %s changed", p)
		}
	}
}

// track retains every descriptor the package opens during fn and proves
// each is actually closed afterwards: Stat on a closed *os.File fails with
// os.ErrClosed. It returns how many were opened.
func track(t *testing.T, fn func()) int {
	t.Helper()
	var mu sync.Mutex
	var files []*os.File
	opened = func(f *os.File) {
		mu.Lock()
		files = append(files, f)
		mu.Unlock()
	}
	defer func() { opened = nil }()
	fn()
	for _, f := range files {
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("descriptor %s still open: %v", f.Name(), err)
		}
	}
	return len(files)
}

func faultAt(stage string) {
	hook = func(s string) error {
		if s == stage {
			return errors.New("injected")
		}
		return nil
	}
}

// Every plan, directory and staging descriptor is closed on success and at
// every injected storage fault stage, before and after the ID mkdir and
// after the verifier started.
func TestDescriptorsClosedAtEveryStage(t *testing.T) {
	// Hand-written stage oracles: pre-ID stages keep their storage codes and
	// create no attempt; every later stage is uncertain. Starts are counted
	// independently by the fake.
	const unc = "execution_uncertain"
	type oracle struct {
		code             string
		attempt          bool
		worker, verifier int
	}
	pre := func(code string) oracle { return oracle{code, false, 0, 0} }
	stages := map[string]oracle{"": {"", true, 1, 1},
		"ns-mkdir": pre("execution_storage_unavailable"), "ns-chmod": pre("execution_storage_unavailable"),
		"root-lstat": pre("execution_storage_unavailable"), "root-open": pre("execution_storage_unavailable"),
		"root-recheck": pre("execution_storage_unavailable"), "root-sync": pre("durability_unavailable"),
		"root-close": pre("execution_storage_unavailable"), "ns-open": pre("execution_storage_unavailable"),
		"ns-recheck": pre("execution_storage_unavailable"), "id-mkdir": pre("execution_storage_unavailable"),
		"output-limit": {unc, true, 1, 1}, "result-random": {unc, true, 1, 1}, "result-create": {unc, true, 1, 1},
		"result-chmod": {unc, true, 1, 1}, "result-write": {unc, true, 1, 1}, "result-sync": {unc, true, 1, 1},
		"result-close": {unc, true, 1, 1}, "result-link": {unc, true, 1, 1}, "result-dir-open": {unc, true, 1, 1},
		"result-dir-recheck": {unc, true, 1, 1}, "result-dir-sync": {unc, true, 1, 1}, "result-dir-close": {unc, true, 1, 1},
		"deliver": {unc, true, 1, 1}}
	for _, s := range []string{"id-chmod", "ns-sync", "ns-close", "id-open", "id-recheck", "id-sync", "id-close",
		"intent-random", "intent-create", "intent-chmod", "intent-write", "intent-sync", "intent-close", "intent-link",
		"intent-dir-open", "intent-dir-recheck", "intent-dir-sync", "intent-dir-close"} {
		stages[s] = oracle{unc, true, 0, 0}
	}
	for _, prog := range []string{"worker", "verifier"} {
		for _, d := range []string{prog, prog + "-home", prog + "-tmp"} {
			for _, b := range []string{"mkdir", "chmod", "open", "recheck", "sync", "close"} {
				stages[d+"-"+b] = oracle{unc, true, 0, 0}
			}
		}
	}
	if len(stages) != 1+10+13+18+36 {
		t.Fatalf("oracle table has %d stages", len(stages))
	}
	for stage, want := range stages {
		t.Run("stage-"+stage, func(t *testing.T) {
			f := newFx(t, "sha1")
			faultAt(stage)
			var ok bool
			var err error
			var out string
			n := track(t, func() { ok, out, err = f.exec(context.Background(), true) })
			hook = nil
			if want.code == "" {
				if !ok || err != nil || out == "" {
					t.Fatalf("ok=%v err=%v", ok, err)
				}
			} else if ok || out != "" || !codeIs(err, want.code) {
				t.Fatalf("ok=%v out=%q err=%v want %s", ok, out, err, want.code)
			}
			if _, aerr := os.Lstat(f.attempt()); (aerr == nil) != want.attempt {
				t.Fatalf("attempt present=%v", aerr == nil)
			}
			if f.starts("worker") != want.worker || f.starts("verifier") != want.verifier {
				t.Fatalf("starts %d/%d", f.starts("worker"), f.starts("verifier"))
			}
			if want.attempt {
				if _, _, rerr := f.exec(context.Background(), true); !codeIs(rerr, "execution_exists") ||
					f.starts("worker") != want.worker || f.starts("verifier") != want.verifier {
					t.Fatalf("repeat: %v", rerr)
				}
			}
			if n == 0 {
				t.Fatal("no descriptor observed: the plan is always opened")
			}
			// Success opens: plan, root, ns, id, 2x(home, tmp, prog dir),
			// intent staging + dir, result staging + dir = 14.
			if stage == "" && n != 14 {
				t.Fatalf("success opened %d descriptors, want 14", n)
			}
		})
	}
	// Plan close fault and a plan rejected after open.
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	big := filepath.Join(dir, "big.json")
	os.WriteFile(big, []byte(planWith(okCmd)+strings.Repeat(" ", MaxPlanBytes)), 0o600)
	os.Chmod(big, 0o600)
	planHook = func(s string) error {
		if s == "close" {
			return errors.New("close")
		}
		return nil
	}
	if n := track(t, func() { ReadPlan(big) }); n != 1 {
		t.Fatalf("plan opened %d", n)
	}
	planHook = nil
}

// The root descriptor is rechecked for type, mode and identity before its
// Sync; a replaced or loosened root is refused before any attempt exists.
func TestRootDescriptorRecheck(t *testing.T) {
	for name, swap := range map[string]func(root string){
		"replaced": func(root string) {
			os.Rename(root, root+"-moved")
			os.Mkdir(root, 0o700)
			os.Chmod(root, 0o700)
		},
		"loosened": func(root string) { os.Chmod(root, 0o755) },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFx(t, "sha256")
			hook = func(s string) error {
				if s == "root-lstat" {
					swap(f.root)
				}
				return nil
			}
			var err error
			track(t, func() { _, _, err = f.exec(context.Background(), true) })
			hook = nil
			if !codeIs(err, "execution_storage_unavailable") {
				t.Fatalf("got %v", err)
			}
			for _, r := range []string{f.root, f.root + "-moved"} {
				if _, err := os.Lstat(filepath.Join(r, Namespace, testID)); err == nil {
					t.Fatal("attempt created")
				}
			}
			if f.starts("worker")+f.starts("verifier") != 0 {
				t.Fatal("a program started")
			}
		})
	}
}

// controller is the outer-harness crash subject: one Execute call that the
// harness kills with SIGKILL while its worker holds.
func controller(base string) int {
	r := Request{Repo: filepath.Join(base, "repo"), StateDir: filepath.Join(base, "state"), RunID: testID,
		Plan: filepath.Join(base, "plan.json"), JSON: true}
	if _, err := Execute(context.Background(), r, os.Stdout); err != nil {
		return 1
	}
	return 0
}

// A controller killed with SIGKILL during its worker leaves durable intent
// and no result; a repeat refuses with zero added starts. The harness owns
// exactly two finite identities: the controller (killed and joined through
// its own handle) and the worker it independently reads from the start
// counter, which it releases and observes finishing; no stored ID is
// signalled.
func TestControllerCrashLeavesIntentWithoutResult(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			f := newFx(t, format)
			f.setPlan("worker-hold", "verifier-check")
			cmd := exec.Command(os.Args[0], "-baw-controller", f.base)
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			released := false
			release := func() {
				if !released {
					released = true
					os.WriteFile(filepath.Join(f.count, "release"), nil, 0o600)
				}
			}
			defer release()
			deadline := time.Now().Add(10 * time.Second)
			for f.starts("worker") == 0 && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if f.starts("worker") != 1 {
				cmd.Process.Kill()
				cmd.Wait()
				t.Fatal("worker did not start")
			}
			cmd.Process.Kill()
			ps, _ := cmd.Process.Wait()
			if ps == nil || ps.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("controller not joined as SIGKILLed: %v", ps)
			}
			// The worker this harness launched, identified by its own start
			// record, is released and observed to finish.
			b, _ := os.ReadFile(filepath.Join(f.count, "worker"))
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			release()
			deadline = time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				_, derr := os.Lstat(filepath.Join(f.count, "worker-done"))
				if derr == nil && syscall.Kill(pid, 0) == syscall.ESRCH {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			if _, err := os.Lstat(filepath.Join(f.count, "worker-done")); err != nil {
				t.Fatal("worker did not finish after release")
			}
			if syscall.Kill(pid, 0) != syscall.ESRCH {
				t.Fatal("worker still present after release")
			}
			checkIntent(t, filepath.Join(f.attempt(), "intent.json"), format, f.head, f.plan)
			if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err == nil {
				t.Fatal("result published by a crashed controller")
			}
			for _, d := range []string{"worker/home", "worker/tmp", "verifier/home", "verifier/tmp"} {
				if m := modeOf(t, filepath.Join(f.attempt(), d)); !m.IsDir() || m.Perm() != 0o700 {
					t.Fatalf("%s mode %v", d, m)
				}
			}
			ok, out, err := f.exec(context.Background(), true)
			if ok || out != "" || !codeIs(err, "execution_exists") || f.starts("worker") != 1 || f.starts("verifier") != 0 {
				t.Fatalf("repeat after crash: %v %q %v", ok, out, err)
			}
			var m map[string]any
			ib, _ := os.ReadFile(filepath.Join(f.attempt(), "intent.json"))
			if json.Unmarshal(ib, &m) != nil || len(m) != 10 {
				t.Fatal("intent keys")
			}
		})
	}
}
