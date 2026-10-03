package state

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// newRoot returns a fresh private 0700 state root containing one unrelated
// pre-existing entry.
func newRoot(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "run.json"), []byte("manual receipt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// snapshot lists every path under dir with its mode, size and contents.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		fi, _ := os.Lstat(p)
		line := strings.TrimPrefix(p, dir) + " " + fi.Mode().String()
		if fi.Mode().IsRegular() {
			b, _ := os.ReadFile(p)
			line += " " + string(b)
		}
		lines = append(lines, line)
		return nil
	})
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func validData() []byte { return []byte(obj(fields1()) + "\n") }

func withHook(t *testing.T, h func(stage, path string) error) {
	t.Helper()
	hook = h
	t.Cleanup(func() { hook = nil })
}

func create(t *testing.T, dir string, data []byte) error {
	t.Helper()
	r, err := OpenRoot(dir)
	if err != nil {
		return err
	}
	if err := r.CheckAbsent(tID); err != nil {
		return err
	}
	return r.Create(tID, data, func() error { return nil })
}

func pending(t *testing.T, dir string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(dir, Namespace, ".pending-"+tID+"-*"))
	return m
}

func TestCreateReadAndLinks(t *testing.T) {
	dir := newRoot(t)
	var stages []string
	withHook(t, func(stage, path string) error {
		stages = append(stages, stage+" "+strings.TrimPrefix(path, mustEval(t, dir)))
		switch stage {
		case "root-sync":
			// Namespace already acquired; no record names yet.
			if fi, err := os.Lstat(filepath.Join(dir, Namespace)); err != nil || !fi.IsDir() {
				t.Errorf("root sync before namespace acquisition")
			}
			if _, err := os.Lstat(filepath.Join(dir, Namespace, tID+".json")); err == nil {
				t.Errorf("root sync after publication")
			}
		case "ns-sync":
			if _, err := os.Lstat(filepath.Join(dir, Namespace, tID+".json")); err != nil {
				t.Errorf("namespace sync before publication")
			}
		}
		return nil
	})
	if err := create(t, dir, validData()); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(stages, "|")
	want := "mkdir /records-v1|root-sync |random /records-v1|stage-create /records-v1/.pending-" + tID + "-"
	if !strings.HasPrefix(got, want) {
		t.Fatalf("stage order %s", got)
	}
	rest := got[len(want)+32:]
	wantRest := "|stage-write /records-v1/.pending-" + tID + "-" + got[len(want):len(want)+32] +
		"|stage-sync /records-v1/.pending-" + tID + "-" + got[len(want):len(want)+32] +
		"|stage-close /records-v1/.pending-" + tID + "-" + got[len(want):len(want)+32] +
		"|link /records-v1/" + tID + ".json|ns-sync /records-v1|dir-close |dir-close /records-v1|deliver /records-v1/" + tID + ".json"
	if rest != wantRest {
		t.Fatalf("stage order rest %s\nwant %s", rest, wantRest)
	}
	hook = nil
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := r.Read(tID)
	if err != nil || rec.RunID != tID || rec.RepositoryHead != tHead1 {
		t.Fatalf("read %+v %v", rec, err)
	}
	final := filepath.Join(dir, Namespace, tID+".json")
	fi, _ := os.Lstat(final)
	ns, _ := os.Lstat(filepath.Join(dir, Namespace))
	if fi.Mode() != 0o600 || ns.Mode().Perm() != 0o700 || !ns.IsDir() {
		t.Fatalf("modes %v %v", fi.Mode(), ns.Mode())
	}
	p := pending(t, dir)
	if len(p) != 1 {
		t.Fatalf("staging %v", p)
	}
	pfi, _ := os.Lstat(p[0])
	if !os.SameFile(fi, pfi) || fi.Sys().(*syscall.Stat_t).Nlink != 2 {
		t.Fatalf("staging is not a second link to the record")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "run.json")); string(b) != "manual receipt\n" {
		t.Fatal("pre-existing entry changed")
	}
	// Duplicate: record_exists, unchanged bytes, no new staging.
	before := snapshot(t, dir)
	if err := create(t, dir, []byte("other\n")); codeOf(err) != "record_exists" {
		t.Fatalf("duplicate %v", err)
	}
	if snapshot(t, dir) != before {
		t.Fatal("duplicate changed state")
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	e, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestStageFailures(t *testing.T) {
	cases := []struct {
		stage string
		want  Code
		final bool // final record present afterwards
	}{
		{"mkdir", "state_unavailable", false},
		{"root-sync", "durability_unavailable", false},
		{"random", "record_unavailable", false},
		{"stage-create", "record_unavailable", false},
		{"stage-write", "record_unavailable", false},
		{"stage-sync", "durability_unavailable", false},
		{"stage-close", "record_unavailable", false},
		{"link", "record_unavailable", false},
		{"ns-sync", "commit_uncertain", true},
		{"dir-close", "commit_uncertain", true},
		{"deliver", "commit_uncertain", true},
	}
	for _, c := range cases {
		t.Run(c.stage, func(t *testing.T) {
			dir := newRoot(t)
			withHook(t, func(stage, _ string) error {
				if stage == c.stage {
					return errors.New("injected")
				}
				return nil
			})
			err := create(t, dir, validData())
			if codeOf(err) != c.want {
				t.Fatalf("got %v want %s", err, c.want)
			}
			hook = nil
			r, err := OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			_, rerr := r.Read(tID)
			if c.final {
				if rerr != nil {
					t.Fatalf("post-publication record not retained: %v", rerr)
				}
			} else if codeOf(rerr) != "record_missing" {
				t.Fatalf("pre-publication final present: %v", rerr)
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "run.json")); string(b) != "manual receipt\n" {
				t.Fatal("pre-existing entry changed")
			}
			// Staging exists (retained) from stage creation onward.
			n := len(pending(t, dir))
			switch c.stage {
			case "mkdir", "root-sync", "random", "stage-create":
				if n != 0 {
					t.Fatalf("unexpected staging %d", n)
				}
			default:
				if n != 1 {
					t.Fatalf("staging not retained: %d", n)
				}
			}
			if c.stage == "stage-write" {
				b, _ := os.ReadFile(pending(t, dir)[0])
				if len(b) >= len(validData()) {
					t.Fatal("partial write not simulated")
				}
			}
		})
	}
}

func TestPublicationCollision(t *testing.T) {
	// A destination appearing after the existing-ID check is never replaced.
	for _, kind := range []string{"safe", "symlink", "mode", "fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := newRoot(t)
			final := filepath.Join(dir, Namespace, tID+".json")
			withHook(t, func(stage, _ string) error {
				if stage != "link" {
					return nil
				}
				switch kind {
				case "safe":
					os.WriteFile(final, []byte("winner\n"), 0o600)
				case "symlink":
					os.Symlink(filepath.Join(dir, "run.json"), final)
				case "mode":
					os.WriteFile(final, []byte("winner\n"), 0o644)
					os.Chmod(final, 0o644)
				case "fifo":
					syscall.Mkfifo(final, 0o600)
				}
				return nil
			})
			want := map[string]Code{"safe": "record_exists", "symlink": "unsafe_state_path", "mode": "state_permissions", "fifo": "unsafe_state_path"}[kind]
			if err := create(t, dir, validData()); codeOf(err) != want {
				t.Fatalf("got %v want %s", err, want)
			}
			if kind == "safe" || kind == "mode" {
				if b, _ := os.ReadFile(final); string(b) != "winner\n" {
					t.Fatal("destination overwritten")
				}
			}
			if b, _ := os.ReadFile(filepath.Join(dir, "run.json")); string(b) != "manual receipt\n" {
				t.Fatal("symlink target changed")
			}
		})
	}
}

func TestRootAndNamespaceSafety(t *testing.T) {
	base := t.TempDir()
	mk := func(name string, mode os.FileMode) string {
		p := filepath.Join(base, name)
		os.Mkdir(p, 0o700)
		os.Chmod(p, mode)
		return p
	}
	good := mk("good", 0o700)
	os.Symlink(good, filepath.Join(base, "link"))
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	cases := []struct {
		dir  string
		want Code
	}{
		{filepath.Join(base, "missing"), "state_unavailable"},
		{file, "state_unavailable"},
		{filepath.Join(base, "link"), "unsafe_state_path"},
		{filepath.Join(base, "link") + "/", "unsafe_state_path"},
		{filepath.Join(base, "link") + "/.", "unsafe_state_path"},
		{mk("open", 0o755), "state_permissions"},
		{mk("group", 0o770), "state_permissions"},
		{mk("sticky", 0o700|os.ModeSticky), "state_permissions"},
	}
	for _, c := range cases {
		if _, err := OpenRoot(c.dir); codeOf(err) != c.want {
			t.Errorf("%s: got %v want %s", c.dir, err, c.want)
		}
	}
	// Trailing slash and dot components on a real directory are accepted.
	for _, d := range []string{good + "/", good + "/.", filepath.Join(base, "x", "..", "good")} {
		if _, err := OpenRoot(d); err != nil {
			t.Errorf("%s: %v", d, err)
		}
	}
	// An ancestor alias (like macOS /tmp -> /private/tmp) is resolved, while
	// the same symlink as the final component is still refused.
	target := mk("target", 0o700)
	if err := os.Mkdir(filepath.Join(target, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(alias); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("alias fixture is not a symlink: %v", err)
	}
	if _, err := OpenRoot(filepath.Join(alias, "state")); err != nil {
		t.Errorf("ancestor alias: %v", err)
	}
	if _, err := OpenRoot(alias); codeOf(err) != "unsafe_state_path" {
		t.Errorf("final alias: got %v", err)
	}

	nsCases := []struct {
		setup func(ns string)
		want  Code
	}{
		{func(ns string) { os.Symlink(good, ns) }, "unsafe_state_path"},
		{func(ns string) { os.WriteFile(ns, nil, 0o600) }, "unsafe_state_path"},
		{func(ns string) { os.Mkdir(ns, 0o700); os.Chmod(ns, 0o755) }, "state_permissions"},
	}
	for i, c := range nsCases {
		dir := newRoot(t)
		c.setup(filepath.Join(dir, Namespace))
		before := snapshot(t, dir)
		if _, err := OpenRoot(dir); codeOf(err) != c.want {
			t.Errorf("namespace case %d: got %v want %s", i, err, c.want)
		}
		if snapshot(t, dir) != before {
			t.Errorf("namespace case %d changed state", i)
		}
	}
}

func TestReadRecordSafety(t *testing.T) {
	dir := newRoot(t)
	ns := filepath.Join(dir, Namespace)
	r, _ := OpenRoot(dir)
	if _, err := r.Read(tID); codeOf(err) != "record_missing" {
		t.Fatalf("absent namespace: %v", err)
	}
	if _, err := os.Lstat(ns); err == nil {
		t.Fatal("status created namespace")
	}
	os.Mkdir(ns, 0o700)
	os.Chmod(ns, 0o700)
	final := filepath.Join(ns, tID+".json")
	outside := filepath.Join(dir, "run.json")
	cases := []struct {
		setup func()
		want  Code
	}{
		{func() {}, "record_missing"},
		{func() { os.Symlink(outside, final) }, "unsafe_state_path"},
		{func() { syscall.Mkfifo(final, 0o600) }, "unsafe_state_path"},
		{func() { os.Mkdir(final, 0o700) }, "unsafe_state_path"},
		{func() { os.WriteFile(final, validData(), 0o600); os.Chmod(final, 0o644) }, "state_permissions"},
		{func() { os.WriteFile(final, validData(), 0o600); os.Chmod(final, 0o400) }, "state_permissions"},
		{func() { os.WriteFile(final, []byte(strings.Repeat(" ", MaxRecordBytes+1)), 0o600) }, "record_too_large"},
		{func() { os.WriteFile(final, []byte("{bad"), 0o600) }, "invalid_record"},
	}
	for i, c := range cases {
		os.RemoveAll(final)
		c.setup()
		r, err := OpenRoot(dir)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := r.Read(tID); done <- err }()
		select {
		case err := <-done:
			if codeOf(err) != c.want {
				t.Errorf("case %d: got %v want %s", i, err, c.want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("case %d blocked", i)
		}
		if b, _ := os.ReadFile(outside); string(b) != "manual receipt\n" {
			t.Fatal("outside marker changed")
		}
	}
	r, _ = OpenRoot(dir)
	// A valid record with a second hard link (retained staging) is accepted.
	os.RemoveAll(final)
	os.WriteFile(final, validData(), 0o600)
	os.Link(final, filepath.Join(ns, ".pending-extra"))
	if _, err := r.Read(tID); err != nil {
		t.Fatalf("hard-linked record: %v", err)
	}
}

// socketStateEnv optionally names a complete existing state root whose tID
// record path is a closed Unix socket, for environments that cannot bind
// sockets. It is read by tests only and never modified.
const socketStateEnv = "BAW_TEST_SOCKET_STATE_DIR"

// TestReadSocketRecordSafety checks that status rejects an actual Unix socket
// at the record path, using the supplied state root if set, otherwise one
// created here. A missing or invalid supplied root fails.
func TestReadSocketRecordSafety(t *testing.T) {
	dir := os.Getenv(socketStateEnv)
	if dir == "" {
		dir = makeSocketState(t)
	}
	ns := filepath.Join(dir, Namespace)
	final := filepath.Join(ns, tID+".json")
	// meta records fixed metadata of the root, namespace and record path.
	meta := func() string {
		t.Helper()
		var lines []string
		for _, p := range []string{dir, ns, final} {
			fi, err := os.Lstat(p)
			if err != nil {
				t.Fatalf("socket state fixture: %v", err)
			}
			st := fi.Sys().(*syscall.Stat_t)
			lines = append(lines, fmt.Sprintf("%s %v uid=%d ino=%d nlink=%d size=%d mtime=%d",
				p, fi.Mode(), st.Uid, st.Ino, st.Nlink, fi.Size(), fi.ModTime().UnixNano()))
		}
		return strings.Join(lines, "\n")
	}
	beforeMeta, before := meta(), snapshot(t, dir)
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("socket state root: %v", err)
	}
	if fi, err := os.Lstat(ns); err != nil || !fi.IsDir() || !private(fi, 0o700) {
		t.Fatalf("socket state namespace is not a private directory: %v", err)
	}
	fi, err := os.Lstat(final)
	if err != nil || fi.Mode().Type() != os.ModeSocket {
		t.Fatalf("socket fixture is not a socket: %v", err)
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; int(uid) != os.Getuid() {
		t.Fatalf("socket fixture owner %d is not the current user", uid)
	}
	done := make(chan error, 1)
	go func() { _, err := r.Read(tID); done <- err }()
	select {
	case err := <-done:
		if codeOf(err) != "unsafe_state_path" {
			t.Errorf("socket record: got %v want unsafe_state_path", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("socket record read blocked")
	}
	if meta() != beforeMeta || snapshot(t, dir) != before {
		t.Error("socket state fixture changed")
	}
}

// makeSocketState returns a new state root whose record path is a closed
// socket, bound by a joined child of this binary using the short relative
// record name (sockaddr paths are length-limited) and left behind on close.
func makeSocketState(t *testing.T) string {
	t.Helper()
	dir := newRoot(t)
	ns := filepath.Join(dir, Namespace)
	if err := os.Mkdir(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := child(ctx, helperEnv+"=socket")
	cmd.Dir = ns
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("socket helper: %v %q", err, out)
	}
	if !cmd.ProcessState.Exited() {
		t.Fatal("socket helper not joined")
	}
	return dir
}

// --- subprocess interruption and concurrency ---

const helperEnv = "BAW_STATE_TEST_HELPER"

// TestHelperProcess is executed only as a child of this test binary.
func TestHelperProcess(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		return
	}
	dir := os.Getenv("BAW_STATE_TEST_DIR")
	switch {
	case strings.HasPrefix(mode, "crash-"):
		stage := strings.TrimPrefix(mode, "crash-")
		hook = func(s, _ string) error {
			if s == stage {
				os.Exit(7)
			}
			return nil
		}
		create(t, dir, validData())
		os.Exit(0)
	case mode == "race":
		data := []byte(obj(set(fields1(), "scope_sha256", `"`+os.Getenv("BAW_STATE_TEST_SCOPE")+`"`)) + "\n")
		// Start together: wait for the go file.
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(time.Millisecond) {
			if _, err := os.Stat(dir + ".go"); err == nil {
				break
			}
		}
		err := create(t, dir, data)
		if err != nil {
			os.Stdout.WriteString(string(codeOf(err)))
		} else {
			os.Stdout.WriteString("ok")
		}
		os.Exit(0)
	case mode == "socket":
		// Relative to the parent-chosen working directory.
		l, err := net.Listen("unix", tID+".json")
		if err != nil {
			os.Stdout.WriteString("listen: " + err.Error())
			os.Exit(3)
		}
		l.(*net.UnixListener).SetUnlinkOnClose(false)
		if err := l.Close(); err != nil {
			os.Exit(4)
		}
		os.Exit(0)
	}
}

func child(ctx context.Context, env ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), env...)
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func TestInterruption(t *testing.T) {
	for _, c := range []struct {
		stage string
		final bool
	}{
		{"root-sync", false}, {"stage-write", false}, {"stage-sync", false}, {"stage-close", false},
		{"link", false}, {"ns-sync", true}, {"deliver", true},
	} {
		t.Run(c.stage, func(t *testing.T) {
			dir := newRoot(t)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			cmd := child(ctx, helperEnv+"=crash-"+c.stage, "BAW_STATE_TEST_DIR="+dir)
			err := cmd.Run()
			var ee *exec.ExitError
			if !errors.As(err, &ee) || ee.ExitCode() != 7 {
				t.Fatalf("child did not stop at %s: %v", c.stage, err)
			}
			r, err := OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			rec, err := r.Read(tID)
			if c.final {
				if err != nil || rec.RunID != tID {
					t.Fatalf("after publication: %v", err)
				}
			} else if codeOf(err) != "record_missing" {
				t.Fatalf("before publication: %v", err)
			}
			if c.stage != "root-sync" && len(pending(t, dir)) != 1 {
				t.Fatal("staging not retained")
			}
		})
	}
}

// race starts one creator per scope, releases them together and joins them
// all, including on a start failure.
func race(t *testing.T, dir string, scopes []string) []*strings.Builder {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var cmds []*exec.Cmd
	var outs []*strings.Builder
	join := func() {
		for _, c := range cmds {
			if err := c.Wait(); err != nil {
				t.Errorf("child: %v", err)
			}
		}
	}
	for _, s := range scopes {
		cmd := child(ctx, helperEnv+"=race", "BAW_STATE_TEST_DIR="+dir, "BAW_STATE_TEST_SCOPE="+s)
		var b strings.Builder
		cmd.Stdout = &b
		if err := cmd.Start(); err != nil {
			cancel()
			join()
			t.Fatal(err)
		}
		cmds, outs = append(cmds, cmd), append(outs, &b)
	}
	os.WriteFile(dir+".go", nil, 0o600)
	join()
	return outs
}

func TestConcurrentCreate(t *testing.T) {
	for _, nsPresent := range []bool{false, true} {
		for round := 0; round < 3; round++ {
			dir := newRoot(t)
			if nsPresent {
				os.Mkdir(filepath.Join(dir, Namespace), 0o700)
			}
			scopes := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
			outs := race(t, dir, scopes)
			res := []string{outs[0].String(), outs[1].String()}
			winner := -1
			for i, s := range res {
				if s == "ok" {
					winner = i
				}
			}
			sorted := append([]string(nil), res...)
			sort.Strings(sorted)
			if winner < 0 || strings.Join(sorted, ",") != "ok,record_exists" {
				t.Fatalf("ns=%v results %v", nsPresent, res)
			}
			r, _ := OpenRoot(dir)
			rec, err := r.Read(tID)
			if err != nil || rec.ScopeSHA256 != scopes[winner] {
				t.Fatalf("winner record %+v %v", rec, err)
			}
		}
	}
}
