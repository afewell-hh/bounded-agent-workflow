package verification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// Every Usable fact is checked in both directions through Verify: the fully
// usable observation passes, and each single fact moved to its failing value
// (including Started) is not passed; each positive alone, with every veto
// clear, is unusable, and each veto alone set on an otherwise usable
// observation is unusable.
func TestVerifyUsableEachFlagBothDirections(t *testing.T) {
	good := proc.Observation{Started: true, Exited: true, Joined: true, GroupAbsent: true, StdoutEOF: true, StderrEOF: true}
	if !good.Usable() {
		t.Fatal("usable control is not usable")
	}
	positives := map[string]func(*proc.Observation, bool){
		"started":     func(o *proc.Observation, v bool) { o.Started = v },
		"exited":      func(o *proc.Observation, v bool) { o.Exited = v },
		"joined":      func(o *proc.Observation, v bool) { o.Joined = v },
		"groupabsent": func(o *proc.Observation, v bool) { o.GroupAbsent = v },
		"stdouteof":   func(o *proc.Observation, v bool) { o.StdoutEOF = v },
		"stderreof":   func(o *proc.Observation, v bool) { o.StderrEOF = v },
	}
	vetoes := map[string]func(*proc.Observation, bool){
		"signaled":    func(o *proc.Observation, v bool) { o.Signaled = v },
		"watcher":     func(o *proc.Observation, v bool) { o.WatcherFailed = v },
		"timedout":    func(o *proc.Observation, v bool) { o.TimedOut = v },
		"cancelled":   func(o *proc.Observation, v bool) { o.Cancelled = v },
		"outputlimit": func(o *proc.Observation, v bool) { o.OutputLimit = v },
	}
	if len(positives) != 6 || len(vetoes) != 5 {
		t.Fatal("flag table incomplete")
	}
	defer func() { runner = nil }()
	through := func(name string, o proc.Observation, wantState string) {
		t.Helper()
		f := newFx(t, "sha1", "ok")
		runner = func(context.Context, proc.Spec) ([]byte, proc.Observation, error) { return nil, o, nil }
		out, passed, err := f.run(context.Background(), f.req(f.head, true))
		runner = nil
		want := wantResultJSON(OutcomeUnverified, wantState, "null", "sha1", f.head, "null")
		if err != nil || passed || out != want {
			t.Errorf("%s: err %v passed %v\n%s\nwant\n%s", name, err, passed, out, want)
		}
	}
	for name, set := range positives {
		o := good
		set(&o, false)
		if o.Usable() {
			t.Errorf("%s cleared still usable", name)
		}
		state := "unverified"
		if name == "started" {
			state = "not_started"
		}
		through(name+"-cleared", o, state)
		alone := proc.Observation{}
		set(&alone, true)
		if alone.Usable() {
			t.Errorf("%s alone usable", name)
		}
	}
	for name, set := range vetoes {
		o := good
		set(&o, true)
		if o.Usable() {
			t.Errorf("%s set still usable", name)
		}
		through(name+"-set", o, "unverified")
		// Every positive set and only this veto clear among set vetoes: still
		// unusable while any other veto remains.
		all := good
		for other, s := range vetoes {
			s(&all, other != name)
		}
		if all.Usable() {
			t.Errorf("only %s clear usable", name)
		}
	}
}

// outputFits is the exact bound Verify applies to the saved and delivered
// bytes, exercised with synthetic buffers (not packets).
func TestVerifyOutputFitsBoundary(t *testing.T) {
	at := bytes.Repeat([]byte("x"), MaxOutput)
	over := bytes.Repeat([]byte("x"), MaxOutput+1)
	small := []byte("{}\n")
	for _, c := range []struct {
		name      string
		data, out []byte
		want      bool
	}{
		{"both-at", at, at, true},
		{"empty", nil, nil, true},
		{"data-over", over, small, false},
		{"out-over", small, over, false},
		{"both-over", over, over, false},
		{"data-at-out-small", at, small, true},
	} {
		if got := outputFits(c.data, c.out); got != c.want {
			t.Errorf("%s: %v want %v", c.name, got, c.want)
		}
	}
	if MaxOutput != 65536 {
		t.Errorf("MaxOutput %d", MaxOutput)
	}
}

// recordingShort delivers only the first half and keeps exactly what it was
// given to deliver.
type recordingShort struct{ got []byte }

func (w *recordingShort) Write(b []byte) (int, error) {
	n := len(b) / 2
	w.got = append(w.got, b[:n]...)
	return n, nil
}

func TestVerifyShortWriterPrefixAndRetention(t *testing.T) {
	for _, asJSON := range []bool{true, false} {
		f := newFx(t, "sha256", "ok")
		w := &recordingShort{}
		now = fixedTime
		passed, err := Verify(context.Background(), f.req(f.head, asJSON), w)
		now = timeNow
		if !IsCode(err, CodeUncertain) || passed {
			t.Errorf("json=%v: %v %v", asJSON, err, passed)
		}
		wantJSON := wantResultJSON(OutcomePassed, "exited", "0", "sha256", f.head, f.head)
		want := wantJSON
		if !asJSON {
			want = wantText(OutcomePassed, "exited", "0", "sha256", f.head, f.head)
		}
		if string(w.got) != want[:len(want)/2] {
			t.Errorf("json=%v: delivered prefix %q", asJSON, w.got)
		}
		checkKept(t, f, f.wantIntent(f.head), wantJSON)
		if f.starts() != 1 {
			t.Errorf("starts %d", f.starts())
		}
	}
}

// Candidate changes the verifier can make that are not plain file edits:
// unborn HEAD, diverged HEAD, conflict-stage index entries, a replaced
// physical top (the original directory and inode are kept elsewhere) and an
// in-place object-format change. Each is candidate_changed with the saved
// layout kept.
func TestVerifyStructuralMutations(t *testing.T) {
	for _, mode := range []string{"mutate-unborn", "mutate-diverged", "mutate-conflict", "mutate-top-replace", "mutate-format"} {
		for _, format := range []string{"sha1", "sha256"} {
			f := newFx(t, format, mode)
			topBefore, err := os.Stat(f.repo)
			if err != nil {
				t.Fatal(err)
			}
			out, passed, err := f.run(context.Background(), f.req(f.head, true))
			want := wantResultJSON(OutcomeChanged, "exited", "0", format, f.head, "null")
			if err != nil || passed || out != want {
				t.Errorf("%s %s: err %v\n%s\nwant\n%s", mode, format, err, out, want)
			}
			if f.starts() != 1 {
				t.Errorf("%s: starts %d", mode, f.starts())
			}
			checkKept(t, f, f.wantIntent(f.head), want)
			switch mode {
			case "mutate-top-replace":
				old, err := os.Stat(f.repo + ".old")
				now, nerr := os.Stat(f.repo)
				if err != nil || nerr != nil || !os.SameFile(old, topBefore) || os.SameFile(now, topBefore) {
					t.Errorf("%s: original inode not preserved at .old or not replaced", mode)
				}
			case "mutate-unborn":
				if b := readFile(t, filepath.Join(f.repo, ".git", "HEAD")); string(b) != "ref: refs/heads/unborn-by-verifier\n" {
					t.Errorf("unborn fixture HEAD %q", b)
				}
			case "mutate-diverged":
				if h := strings.TrimSpace(f.git("rev-parse", "HEAD")); h == f.head || len(h) != len(f.head) {
					t.Errorf("diverged fixture HEAD %q", h)
				}
				if _, err := f.gitErr("merge-base", "--is-ancestor", f.head, "HEAD"); err == nil {
					t.Errorf("diverged fixture is a descendant")
				}
			case "mutate-conflict":
				if s := f.git("ls-files", "--stage", "README.md"); len(strings.Split(strings.TrimSpace(s), "\n")) != 3 || strings.Count(s, " 0\t") != 0 {
					t.Errorf("conflict fixture stages %q", s)
				}
			case "mutate-format":
				other := map[string]string{"sha1": "sha256", "sha256": "sha1"}[format]
				if b := strings.ToLower(string(readFile(t, filepath.Join(f.repo, ".git", "config")))); !strings.Contains(b, "objectformat = "+other) {
					t.Errorf("format fixture config %q", b)
				}
			}
		}
	}
}

func (f *fx) gitErr(args ...string) (string, error) {
	cmd := exec.Command("/usr/bin/git", args...)
	cmd.Dir = f.repo
	cmd.Env = []string{"HOME=" + f.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	b, err := cmd.Output()
	return string(b), err
}

// Namespace creation racing with another creator: the exclusive mkdir sees
// EEXIST. A safe concurrently created namespace is used; an unsafe one is
// refused before any ID with nothing created inside it.
func TestVerifyNamespaceEEXISTRace(t *testing.T) {
	defer func() { hook = nil }()
	for _, c := range []struct {
		name string
		perm os.FileMode
		want string
	}{{"safe", 0o700, ""}, {"unsafe", 0o755, "state_permissions"}} {
		f := newFx(t, "sha1", "ok")
		ns := filepath.Join(f.state, Namespace)
		hits := 0
		hook = func(s string) error {
			if s == "ns-mkdir" {
				hits++
				if err := os.Mkdir(ns, 0o700); err != nil {
					return err
				}
				return os.Chmod(ns, c.perm)
			}
			return nil
		}
		out, passed, err := f.run(context.Background(), f.req(f.head, true))
		hook = nil
		if hits != 1 {
			t.Errorf("%s: hits %d", c.name, hits)
		}
		fi, lerr := os.Lstat(ns)
		if lerr != nil || fi.Mode().Perm() != c.perm {
			t.Errorf("%s: namespace mode %v %v", c.name, fi, lerr)
		}
		if c.want == "" {
			want := wantResultJSON(OutcomePassed, "exited", "0", "sha1", f.head, f.head)
			if err != nil || !passed || out != want || f.starts() != 1 {
				t.Errorf("%s: %v %s", c.name, err, out)
			}
			checkKept(t, f, f.wantIntent(f.head), want)
			continue
		}
		if err == nil || err.Error() != c.want || out != "" || f.starts() != 0 {
			t.Errorf("%s: err %v out %q starts %d", c.name, err, out, f.starts())
		}
		if l := listing(t, ns); l != "" {
			t.Errorf("%s: namespace listing %q", c.name, l)
		}
	}
}

// specialChild runs one Verify in this process for a bounded joined parent
// and prints the outcome; it never runs tests.
func specialChild(repo, stateDir, plan, candidate string) int {
	var out bytes.Buffer
	_, err := Verify(context.Background(), Request{Repo: repo, StateDir: stateDir, RunID: testID, Candidate: candidate, Plan: plan, JSON: true}, &out)
	if err != nil {
		os.Stdout.WriteString("ERR " + err.Error() + "\n")
	}
	os.Stdout.Write(out.Bytes())
	return 0
}

// fixtureTree records every entry below root (root included) with type,
// mode, owner, group, device, inode, link count, size, mtime and regular
// content hash, for complete read-only fixture preservation.
func fixtureTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := fi.Sys().(*syscall.Stat_t)
		h := ""
		if fi.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h = sha(b)
		}
		lines = append(lines, fmt.Sprintf("%s %v %d %d %d %d %d %d %d %s", p, fi.Mode(), st.Uid, st.Gid, st.Dev, st.Ino,
			st.Nlink, fi.Size(), fi.ModTime().UnixNano(), h))
		return nil
	})
	if err != nil {
		t.Fatalf("supplied fixture %s unreadable: %v", root, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// suppliedSpecial validates a supplied read-only fixture tree and returns the
// one entry of the wanted kind. Every entry must be owned by the current
// user, every directory must be 0700 without special bits, and every regular
// file must be 0600 carrying exactly the wanted bit. An invalid or absent
// supplied fixture fails the test; it is never skipped or replaced.
func suppliedSpecial(t *testing.T, env, root string, want fs.FileMode) string {
	t.Helper()
	found := ""
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		st := fi.Sys().(*syscall.Stat_t)
		if st.Uid != uint32(os.Getuid()) {
			return fmt.Errorf("%s owner %d", p, st.Uid)
		}
		m := fi.Mode()
		switch {
		case m.IsDir():
			if m.Perm() != 0o700 || m&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				return fmt.Errorf("%s directory mode %v", p, m)
			}
		case m.IsRegular():
			if want == fs.ModeSocket || m.Perm() != 0o600 || m&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != want {
				return fmt.Errorf("%s file mode %v", p, m)
			}
			if found == "" {
				found = p
			}
		case m.Type() == fs.ModeSocket && want == fs.ModeSocket:
			if found != "" {
				return fmt.Errorf("%s second socket", p)
			}
			found = p
		default:
			return fmt.Errorf("%s unexpected type %v", p, m)
		}
		return nil
	})
	if err != nil || found == "" {
		t.Fatalf("supplied %s fixture %s invalid: %v (found %q)", env, root, err, found)
	}
	return found
}

// Special plan files, each verified in a bounded (10s) joined child. With a
// supplied fixture variable set, the existing read-only fixture is only read:
// its type, owner and mode are validated and its complete tree (listing,
// metadata, regular bytes) must be unchanged afterwards. With the variables
// unset, a real socket is bound by a bounded joined child at a short relative
// name in a private test directory, and regular files get actual setuid and
// setgid bits; the actual metadata is read back and the test fails, never
// skips, if it is not as wanted. A FIFO is always created privately.
func TestVerifySpecialPlanFilesInChildren(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	created := func(f *fx, p string, bit fs.FileMode) {
		write0600(t, p, []byte(f.planJSON("ok", 20)))
		if err := os.Chmod(p, 0o600|bit); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name, env, want string
		bit             fs.FileMode
		root            func(supplied string) string
	}{
		{"socket", "BAW_TEST_SOCKET_STATE_DIR", "unsafe_state_path", fs.ModeSocket, func(s string) string { return s }},
		{"fifo", "", "unsafe_state_path", fs.ModeNamedPipe, nil},
		{"setuid", "BAW_TEST_DIAG_SPECIAL_FIXTURES", "state_permissions", fs.ModeSetuid, func(s string) string { return s }},
		{"setgid", "BAW_TEST_EXEC_SETGID_PLAN", "state_permissions", fs.ModeSetgid, filepath.Dir},
	}
	for _, c := range cases {
		f := newFx(t, "sha1", "ok")
		p, fixtureRoot, fixtureBefore := filepath.Join(f.base, "special-plan"), "", ""
		supplied := ""
		if c.env != "" {
			supplied = os.Getenv(c.env)
		}
		switch {
		case supplied != "":
			fixtureRoot = c.root(supplied)
			if c.bit == fs.ModeSetgid {
				if fi, err := os.Lstat(supplied); err != nil || !fi.Mode().IsRegular() {
					t.Fatalf("supplied %s %s invalid: %v", c.env, supplied, err)
				}
			}
			fixtureBefore = fixtureTree(t, fixtureRoot)
			p = suppliedSpecial(t, c.env, fixtureRoot, c.bit)
			if c.bit == fs.ModeSetgid {
				a, aerr := os.Lstat(p)
				b, berr := os.Lstat(supplied)
				if aerr != nil || berr != nil || !os.SameFile(a, b) {
					t.Fatalf("supplied %s: %s is not the setgid plan", c.env, p)
				}
			}
		case c.bit == fs.ModeSocket:
			// A bounded joined child binds the short relative name "s" in the
			// private fixture directory, so no shared /tmp path is used.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, self, "-baw-verify-sockbind", "s")
			cmd.Dir = f.base
			cmd.WaitDelay = time.Second
			out, err := cmd.Output()
			cancel()
			if err != nil || string(out) != "bound\n" || cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
				t.Fatalf("socket child %v %q", err, out)
			}
			p = filepath.Join(f.base, "s")
		case c.bit == fs.ModeNamedPipe:
			if err := syscall.Mkfifo(p, 0o600); err != nil {
				t.Fatal(err)
			}
		default:
			created(f, p, c.bit)
		}
		fi, err := os.Lstat(p)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if st := fi.Sys().(*syscall.Stat_t); st.Uid != uint32(os.Getuid()) {
			t.Fatalf("%s: actual owner %d", c.name, st.Uid)
		}
		switch c.bit {
		case fs.ModeSocket, fs.ModeNamedPipe:
			if fi.Mode().Type() != c.bit {
				t.Fatalf("%s: actual type %v", c.name, fi.Mode())
			}
		default:
			if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != c.bit {
				t.Fatalf("%s: actual mode %v lacks only %v", c.name, fi.Mode(), c.bit)
			}
		}
		t.Logf("%s: supplied=%v observed mode %v", c.name, supplied != "", fi.Mode())
		before := snapshot(t, f.state)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, self, "-baw-verify-special", f.repo, f.state, p, f.head)
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		cancel()
		if err != nil || string(out) != "ERR "+c.want+"\n" {
			t.Errorf("%s: child %v %q want %s", c.name, err, out, c.want)
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			t.Errorf("%s: child not joined", c.name)
		}
		if after := snapshot(t, f.state); after != before {
			t.Errorf("%s: state changed", c.name)
		}
		if f.starts() != 0 {
			t.Errorf("%s: starts %d", c.name, f.starts())
		}
		if fi2, err := os.Lstat(p); err != nil || fi2.Mode() != fi.Mode() || !os.SameFile(fi, fi2) {
			t.Errorf("%s: special file changed %v -> %v (%v)", c.name, fi.Mode(), fi2, err)
		}
		if fixtureRoot != "" {
			if after := fixtureTree(t, fixtureRoot); after != fixtureBefore {
				t.Errorf("%s: supplied fixture changed\nbefore:\n%s\nafter:\n%s", c.name, fixtureBefore, after)
			}
		}
	}
}

// Each named post-ID storage fault keeps an independently expected retained
// state: the attempt and verifier listings, each staging or final file's
// exact bytes, mode and link count, and the 0700 directories. Nothing is
// removed after the exclusive ID mkdir.
func TestVerifyStageFaultRetainedState(t *testing.T) {
	type want struct {
		attempt, verifier string // "-" means absent
		intent            string // "", "empty", "staged" (link 1), "final" (link 2)
		result            string
	}
	none := want{"", "-", "", ""}
	vdir := want{"verifier", "", "", ""}
	home := want{"verifier", "home", "", ""}
	both := want{"verifier", "home,tmp", "", ""}
	stages := []struct {
		stage string
		w     want
	}{
		{"id-chmod", none}, {"ns-sync", none}, {"ns-close", none}, {"id-open", none}, {"id-recheck", none},
		{"verifier-mkdir", none}, {"verifier-chmod", vdir}, {"verifier-home-mkdir", vdir},
		{"verifier-home-chmod", home}, {"verifier-home-open", home}, {"verifier-home-recheck", home},
		{"verifier-home-sync", home}, {"verifier-home-close", home}, {"verifier-tmp-mkdir", home},
		{"verifier-tmp-chmod", both}, {"verifier-tmp-open", both}, {"verifier-tmp-recheck", both},
		{"verifier-tmp-sync", both}, {"verifier-tmp-close", both}, {"verifier-open", both},
		{"verifier-recheck", both}, {"verifier-sync", both}, {"verifier-close", both}, {"id-sync", both},
		{"id-close", both}, {"intent-random", both}, {"intent-create", both},
		{"intent-chmod", want{".pending-intent-X,verifier", "home,tmp", "empty", ""}},
		{"intent-write", want{".pending-intent-X,verifier", "home,tmp", "empty", ""}},
		{"intent-sync", want{".pending-intent-X,verifier", "home,tmp", "staged", ""}},
		{"intent-close", want{".pending-intent-X,verifier", "home,tmp", "staged", ""}},
		{"intent-link", want{".pending-intent-X,verifier", "home,tmp", "staged", ""}},
		{"intent-dir-open", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"intent-dir-recheck", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"intent-dir-sync", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"intent-dir-close", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"output-limit", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"result-random", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"result-create", want{".pending-intent-X,intent.json,verifier", "home,tmp", "final", ""}},
		{"result-chmod", want{".pending-intent-X,.pending-result-X,intent.json,verifier", "home,tmp", "final", "empty"}},
		{"result-write", want{".pending-intent-X,.pending-result-X,intent.json,verifier", "home,tmp", "final", "empty"}},
		{"result-sync", want{".pending-intent-X,.pending-result-X,intent.json,verifier", "home,tmp", "final", "staged"}},
		{"result-close", want{".pending-intent-X,.pending-result-X,intent.json,verifier", "home,tmp", "final", "staged"}},
		{"result-link", want{".pending-intent-X,.pending-result-X,intent.json,verifier", "home,tmp", "final", "staged"}},
		{"result-dir-open", want{".pending-intent-X,.pending-result-X,intent.json,result.json,verifier", "home,tmp", "final", "final"}},
		{"result-dir-recheck", want{".pending-intent-X,.pending-result-X,intent.json,result.json,verifier", "home,tmp", "final", "final"}},
		{"result-dir-sync", want{".pending-intent-X,.pending-result-X,intent.json,result.json,verifier", "home,tmp", "final", "final"}},
		{"result-dir-close", want{".pending-intent-X,.pending-result-X,intent.json,result.json,verifier", "home,tmp", "final", "final"}},
		{"deliver", want{".pending-intent-X,.pending-result-X,intent.json,result.json,verifier", "home,tmp", "final", "final"}},
	}
	defer func() { hook = nil }()
	checkFile := func(stage, a, name, kind, data string) {
		t.Helper()
		var staging string
		es, _ := os.ReadDir(a)
		for _, e := range es {
			if strings.HasPrefix(e.Name(), ".pending-"+name+"-") {
				staging = filepath.Join(a, e.Name())
			}
		}
		if kind == "" {
			if staging != "" {
				t.Errorf("%s: unexpected %s staging", stage, name)
			}
			return
		}
		wantBytes, wantLinks := data, uint16(1)
		if kind == "empty" {
			wantBytes = ""
		}
		if kind == "final" {
			wantLinks = 2
		}
		paths := []string{staging}
		if kind == "final" {
			paths = append(paths, filepath.Join(a, name+".json"))
		}
		for _, p := range paths {
			fi, err := os.Lstat(p)
			if err != nil {
				t.Errorf("%s: %s missing", stage, filepath.Base(p))
				continue
			}
			st := fi.Sys().(*syscall.Stat_t)
			if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 ||
				uint16(st.Nlink) != wantLinks {
				t.Errorf("%s: %s mode %v links %d", stage, filepath.Base(p), fi.Mode(), st.Nlink)
			}
			if got := string(readFile(t, p)); got != wantBytes {
				t.Errorf("%s: %s bytes %q want %q", stage, filepath.Base(p), got, wantBytes)
			}
		}
	}
	for _, c := range stages {
		f := newFx(t, "sha1", "ok")
		hits := 0
		hook = func(s string) error {
			if s == c.stage {
				hits++
				return errors.New("injected")
			}
			return nil
		}
		_, _, err := f.run(context.Background(), f.req(f.head, true))
		hook = nil
		if !IsCode(err, CodeUncertain) || hits != 1 {
			t.Errorf("%s: %v hits %d", c.stage, err, hits)
		}
		a := f.attempt()
		if got := listing(t, a); got != c.w.attempt {
			t.Errorf("%s: attempt listing %q want %q", c.stage, got, c.w.attempt)
		}
		v := filepath.Join(a, "verifier")
		gotV := listing(t, v)
		if gotV == "error" {
			gotV = "-"
		}
		if gotV != c.w.verifier {
			t.Errorf("%s: verifier listing %q want %q", c.stage, gotV, c.w.verifier)
		}
		dirs := []string{a}
		if c.w.verifier != "-" {
			dirs = append(dirs, v)
			for _, sub := range strings.Split(c.w.verifier, ",") {
				if sub != "" {
					dirs = append(dirs, filepath.Join(v, sub))
				}
			}
		}
		for _, d := range dirs {
			if fi, err := os.Lstat(d); err != nil || !fi.IsDir() || fi.Mode().Perm() != 0o700 {
				// A failed chmod stage leaves the mkdir mode, which under the
				// test umask is already 0700.
				t.Errorf("%s: dir %s %v %v", c.stage, d, fi, err)
			}
		}
		for _, sub := range []string{"home", "tmp"} {
			if strings.Contains(c.w.verifier, sub) && listing(t, filepath.Join(v, sub)) != "" {
				t.Errorf("%s: verifier/%s not empty", c.stage, sub)
			}
		}
		checkFile(c.stage, a, "intent", c.w.intent, f.wantIntent(f.head))
		checkFile(c.stage, a, "result", c.w.result, wantResultJSON(OutcomePassed, "exited", "0", "sha1", f.head, f.head))
		wantStarts := 0
		if c.w.intent == "final" && (c.stage == "output-limit" || strings.HasPrefix(c.stage, "result-") || c.stage == "deliver") {
			wantStarts = 1
		}
		if f.starts() != wantStarts {
			t.Errorf("%s: starts %d want %d", c.stage, f.starts(), wantStarts)
		}
	}
}

// signalChild is the direct pipeline signal child, not the CLI handler (the
// CLI handler's own signal tests are in internal/cli and unchanged): it
// drives Verify under its own signal.NotifyContext for SIGINT and SIGTERM.
// A test-only signal.Notify channel acknowledges each actual signal receipt
// by publishing MARKER-ack-N holding NONCE-ack-N. At STAGE it publishes NONCE
// to MARKER and holds until the context ends and receipt 1 is acknowledged;
// with SECOND "1" it then publishes NONCE-2 to MARKER-2 and keeps holding
// until receipt 2 is acknowledged, so the second interrupt is received while
// the first is held. Each wait is bounded. It prints "ERR CODE" or the
// delivered bytes.
func signalChild(stage, marker, nonce, second, repo, stateDir, plan, candidate string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	received := make(chan os.Signal, 2)
	signal.Notify(received, syscall.SIGINT, syscall.SIGTERM)
	acked := make(chan int, 2)
	go func() {
		n := 0
		for range received {
			n++
			publishFile(fmt.Sprintf("%s-ack-%d", marker, n), fmt.Sprintf("%s-ack-%d", nonce, n))
			acked <- n
		}
	}()
	waitAck := func(want int) {
		select {
		case n := <-acked:
			if n == want {
				return
			}
		case <-time.After(8 * time.Second):
		}
		os.Stdout.WriteString(fmt.Sprintf("NO-ACK-%d\n", want))
		os.Exit(70)
	}
	hook = func(s string) error {
		if s != stage {
			return nil
		}
		publishFile(marker, nonce)
		select {
		case <-ctx.Done():
		case <-time.After(8 * time.Second):
			os.Stdout.WriteString("NO-SIGNAL\n")
			os.Exit(70)
		}
		waitAck(1)
		if second == "1" {
			publishFile(marker+"-2", nonce+"-2")
			waitAck(2)
		}
		return nil
	}
	var out bytes.Buffer
	_, err := Verify(ctx, Request{Repo: repo, StateDir: stateDir, RunID: testID, Candidate: candidate, Plan: plan, JSON: true}, &out)
	if err != nil {
		os.Stdout.WriteString("ERR " + err.Error() + "\n")
	}
	os.Stdout.Write(out.Bytes())
	return 0
}

// waitNonce waits (bounded) for marker to hold exactly nonce.
func waitNonce(t *testing.T, marker, nonce string) bool {
	t.Helper()
	for i := 0; i < 800; i++ {
		if b, err := os.ReadFile(marker); err == nil {
			return string(b) == nonce
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Direct pipeline tests (not the CLI handler): real SIGINT and SIGTERM, each
// delivered only after an independently visible per-run nonce at the
// boundary, to a bounded (10s) joined child, and each actual receipt
// acknowledged by the child: the three pre-ownership acquisition boundaries,
// prestart (after durable intent), postinspection, late classification,
// after classification and a second interrupt sent only after the first was
// acknowledged and received while the first is held. SIGKILL before the
// verifier starts keeps the durable intent with no result and no start.
func TestVerifyPipelineRealSignalsAtBoundaries(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		stage, want string
		second      bool
	}{
		{"ns-mkdir", "ERR verification_cancelled", false},
		{"root-lstat", "ERR verification_cancelled", false},
		{"id-mkdir", "ERR verification_cancelled", false},
		{"intent-dir-close", "not_started", false},
		{"post-inspection", "unverified", false},
		{"classification", "unverified", false},
		{"output-limit", "passed", false},
		{"output-limit", "passed", true},
	}
	run := 0
	for _, c := range cases {
		for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL} {
			if sig == syscall.SIGKILL && c.stage != "intent-dir-close" {
				continue
			}
			run++
			f := newFx(t, "sha1", "ok")
			nonce := fmt.Sprintf("nonce-%d-%d-%d", os.Getpid(), run, time.Now().UnixNano())
			marker := filepath.Join(f.base, "nonce")
			second := "-"
			if c.second {
				second = "1"
			}
			started := time.Now()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			cmd := exec.CommandContext(ctx, self, "-baw-verify-signal", c.stage, marker, nonce, second, f.repo, f.state, f.plan, f.head)
			cmd.WaitDelay = time.Second
			var out bytes.Buffer
			cmd.Stdout = &out
			if err := cmd.Start(); err != nil {
				cancel()
				t.Fatal(err)
			}
			name := fmt.Sprintf("%s/%v/second=%v", c.stage, sig, c.second)
			if !waitNonce(t, marker, nonce) {
				t.Errorf("%s: nonce not observed", name)
			}
			cmd.Process.Signal(sig)
			if sig != syscall.SIGKILL {
				if !waitNonce(t, marker+"-ack-1", nonce+"-ack-1") {
					t.Errorf("%s: first signal receipt not acknowledged", name)
				}
			}
			if c.second {
				// Sent only after receipt 1 is acknowledged and the child
				// reports it is still holding the first interrupt.
				if !waitNonce(t, marker+"-2", nonce+"-2") {
					t.Errorf("%s: second nonce not observed", name)
				}
				cmd.Process.Signal(sig)
				if !waitNonce(t, marker+"-ack-2", nonce+"-ack-2") {
					t.Errorf("%s: second signal receipt not acknowledged", name)
				}
			}
			werr := cmd.Wait()
			deadline := ctx.Err()
			cancel()
			if deadline != nil || cmd.ProcessState == nil {
				t.Fatalf("%s: child not joined within bound", name)
			}
			_, ierr := os.Lstat(filepath.Join(f.attempt(), "intent.json"))
			_, rerr := os.Lstat(filepath.Join(f.attempt(), "result.json"))
			if sig == syscall.SIGKILL {
				ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !ws.Signaled() || ws.Signal() != syscall.SIGKILL || out.Len() != 0 {
					t.Errorf("%s: not killed or output %q", name, out.String())
				}
				if ierr != nil || rerr == nil || f.starts() != 0 {
					t.Errorf("%s: intent %v result %v starts %d", name, ierr, rerr, f.starts())
				}
				// The child's actual clock: the saved creation time is canonical
				// and within this invocation's window; the remaining bytes
				// are the hand-written intent.
				b, err := os.ReadFile(filepath.Join(f.attempt(), "intent.json"))
				var got struct {
					CreatedAt string `json:"created_at"`
				}
				if err != nil || json.Unmarshal(b, &got) != nil || !ValidIntent(b, testID) {
					t.Fatalf("%s: intent unreadable", name)
				}
				ct, perr := time.Parse(state.TimeLayout, got.CreatedAt)
				if perr != nil || ct.UTC().Format(state.TimeLayout) != got.CreatedAt ||
					ct.Before(started.Truncate(time.Second)) || ct.After(time.Now()) {
					t.Errorf("%s: created_at %q outside window", name, got.CreatedAt)
				}
				if string(b) != strings.Replace(f.wantIntent(f.head), fixedNow, got.CreatedAt, 1) {
					t.Errorf("%s: intent bytes %s", name, b)
				}
				continue
			}
			if werr != nil || !cmd.ProcessState.Exited() || cmd.ProcessState.ExitCode() != 0 {
				t.Errorf("%s: child %v out %q", name, werr, out.String())
			}
			got := out.String()
			switch c.want {
			case "not_started", "unverified", "passed":
				// Actual creation times are from the child's clock; compare
				// the saved bytes and the row, not invented times.
				saved, err := os.ReadFile(filepath.Join(f.attempt(), "result.json"))
				if err != nil || string(saved) != got {
					t.Errorf("%s: saved result differs from delivered %q", name, got)
				}
				row := map[string]string{
					"not_started": `"outcome":"verification_unverified","verification":{"state":"not_started","exit_code":null}`,
					"unverified":  `"outcome":"verification_unverified","verification":{"state":"unverified","exit_code":null}`,
					"passed":      `"outcome":"candidate_verification_passed","verification":{"state":"exited","exit_code":0}`,
				}[c.want]
				if !strings.Contains(got, row) || !ValidResult([]byte(got), testID) {
					t.Errorf("%s: delivered %q", name, got)
				}
				starts := 1
				if c.want == "not_started" {
					starts = 0
				}
				if f.starts() != starts {
					t.Errorf("%s: starts %d want %d", name, f.starts(), starts)
				}
			default:
				if got != c.want+"\n" || f.starts() != 0 {
					t.Errorf("%s: %q starts %d", name, got, f.starts())
				}
				if _, err := os.Lstat(f.attempt()); err == nil {
					t.Errorf("%s: ID created", name)
				}
			}
		}
	}
}

// Candidate admission against unborn, diverged and conflicted repositories:
// the unchanged inspector codes (unborn, diverged) pass through, and an
// index with conflict-stage entries is not clean; nothing is created and
// nothing starts, in both formats.
func TestVerifyAdmissionUnbornDivergedConflict(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		// Unborn: HEAD names a branch with no commit.
		f := newFx(t, format, "ok")
		f.git("symbolic-ref", "HEAD", "refs/heads/unborn-at-admission")
		f.assertPre(t, format+" unborn", context.Background(), f.req(f.head, true), string(inspect.CodeCheckpointUnborn))
		// Diverged: HEAD is a sibling of the record HEAD.
		f = newFx(t, format, "ok")
		f.git("commit", "-q", "--amend", "--allow-empty", "-m", "sibling")
		sib := strings.TrimSpace(f.git("rev-parse", "HEAD"))
		if sib == f.head || len(sib) != len(f.head) {
			t.Fatalf("%s: diverged fixture", format)
		}
		f.assertPre(t, format+" diverged", context.Background(), f.req(sib, true), string(inspect.CodeCheckpointDiverged))
		// Conflict stages in the index.
		f = newFx(t, format, "ok")
		oid := strings.TrimSpace(f.git("hash-object", "-w", "README.md"))
		info := "0 " + strings.Repeat("0", len(oid)) + "\tREADME.md\n"
		for _, s := range []string{"1", "2", "3"} {
			info += "100644 " + oid + " " + s + "\tREADME.md\n"
		}
		cmd := exec.Command("/usr/bin/git", "update-index", "--index-info")
		cmd.Dir, cmd.Stdin = f.repo, strings.NewReader(info)
		cmd.Env = []string{"HOME=" + f.home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		if s := f.git("ls-files", "--stage", "README.md"); len(strings.Split(strings.TrimSpace(s), "\n")) != 3 {
			t.Fatalf("%s: conflict fixture %q", format, s)
		}
		f.assertPre(t, format+" conflict", context.Background(), f.req(f.head, true), string(CodeNotClean))
	}
}

// SIGKILL of the direct pipeline controller after the verifier has actually
// started. The finite fake verifier publishes a unique nonce only after it
// recorded its start and holds the exclusive fixture lock; the parent waits
// for exactly that nonce, kills only its own live controller child and joins
// it. The durable intent is retained with no result, no output and one start.
// The verifier is then reconciled through its own lock: the parent publishes
// the release file and observes the lock removed and the exited nonce within
// a bound; no stored PID is read or signalled. Nothing is replayed.
func TestVerifyPipelineSIGKILLAfterVerifierStart(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for i, format := range []string{"sha1", "sha256"} {
		f := newFx(t, format, "ok")
		nonce := fmt.Sprintf("started-%d-%d-%d", os.Getpid(), i, time.Now().UnixNano())
		os.Remove(f.plan)
		write0600(t, f.plan, []byte(`{"schema_version":1,"verifier":{"executable":"`+f.tool+
			`","arguments":["-baw-verify-lockhold","`+f.counter+`","`+nonce+`"],"timeout_seconds":20}}`+"\n"))
		lock, ready := filepath.Join(f.counter, "lock"), filepath.Join(f.counter, "ready")
		started := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, self, "-baw-verify-special", f.repo, f.state, f.plan, f.head)
		cmd.WaitDelay = time.Second
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		if !waitNonce(t, ready, nonce) {
			t.Errorf("%s: verifier start nonce not observed", format)
		}
		if b, err := os.ReadFile(lock); err != nil || string(b) != nonce {
			t.Errorf("%s: fixture lock not held: %q %v", format, b, err)
		}
		cmd.Process.Kill()
		cmd.Wait()
		deadline := ctx.Err()
		cancel()
		if deadline != nil || cmd.ProcessState == nil {
			t.Fatalf("%s: controller not joined within bound", format)
		}
		ws, _ := cmd.ProcessState.Sys().(syscall.WaitStatus)
		if !ws.Signaled() || ws.Signal() != syscall.SIGKILL || out.Len() != 0 {
			t.Errorf("%s: controller not killed or output %q", format, out.String())
		}
		// The verifier outlives its killed controller: lock still held.
		if _, err := os.Lstat(filepath.Join(f.counter, "exited")); err == nil {
			t.Errorf("%s: verifier exited before release", format)
		}
		intentPath := filepath.Join(f.attempt(), "intent.json")
		intent, err := os.ReadFile(intentPath)
		var got struct {
			CreatedAt string `json:"created_at"`
		}
		if err != nil || json.Unmarshal(intent, &got) != nil || !ValidIntent(intent, testID) {
			t.Fatalf("%s: intent not retained: %v", format, err)
		}
		ct, perr := time.Parse(state.TimeLayout, got.CreatedAt)
		if perr != nil || ct.UTC().Format(state.TimeLayout) != got.CreatedAt ||
			ct.Before(started.Truncate(time.Second)) || ct.After(time.Now()) {
			t.Errorf("%s: created_at %q outside window", format, got.CreatedAt)
		}
		if string(intent) != strings.Replace(f.wantIntent(f.head), fixedNow, got.CreatedAt, 1) {
			t.Errorf("%s: intent bytes %s", format, intent)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err == nil {
			t.Errorf("%s: result written", format)
		}
		if f.starts() != 1 {
			t.Errorf("%s: starts %d", format, f.starts())
		}
		kept := snapshot(t, f.state)
		// Reconcile the finite verifier through its own fixture lock.
		publishFile(filepath.Join(f.counter, "release"), nonce)
		if !waitNonce(t, filepath.Join(f.counter, "exited"), nonce) {
			t.Errorf("%s: verifier did not exit within bound", format)
		}
		if _, err := os.Lstat(lock); err == nil {
			t.Errorf("%s: fixture lock not released", format)
		}
		if after := snapshot(t, f.state); after != kept {
			t.Errorf("%s: state changed after kill\nbefore:\n%s\nafter:\n%s", format, kept, after)
		}
		if f.starts() != 1 {
			t.Errorf("%s: replayed: starts %d", format, f.starts())
		}
	}
}
