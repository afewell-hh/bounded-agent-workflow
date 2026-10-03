package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Hand-written staging RANDOM suffixes (32 lowercase hex).
const (
	rndA = "0000000000000000000000000000000a"
	rndB = "0000000000000000000000000000000b"
	rndC = "0000000000000000000000000000000c"
	rndD = "0000000000000000000000000000000d"
	rndE = "0000000000000000000000000000000e"
	rndF = "0000000000000000000000000000000f"
)

func pend(rnd string) string { return ".pending-" + tID + "-" + rnd }

// nsRoot returns a fresh root with an empty private namespace.
func nsRoot(t *testing.T) (dir, ns string) {
	t.Helper()
	dir = newRoot(t)
	ns = filepath.Join(dir, Namespace)
	if err := os.Mkdir(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ns, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir, ns
}

func put(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func link(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Link(from, to); err != nil {
		t.Fatal(err)
	}
}

// meta lists every path under dir with mode, owner, inode, link count, size,
// modification time and content; access time is deliberately excluded.
func meta(t *testing.T, dir string) string {
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
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			line += " " + string(b)
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

// probe is the observed outcome of one Diagnose call. It is JSON so that a
// child of this test binary can report it to its parent.
type probe struct {
	D      Diagnosis
	Code   Code     // "" when Diagnose returned no error
	Stages []string // "stage path" relative to the namespace
	Opened int      // descriptors Diagnose acquired
	// Open names acquired descriptors whose Stat or Read did not fail with
	// os.ErrClosed after Diagnose returned.
	Open []string
	// At each staging-entry stage, the held final descriptor was found open
	// (HeldOpen) or closed (HeldClosed); HeldLinked counts staging entries
	// whose name, at their close stage, is the same file as that open
	// descriptor.
	HeldOpen, HeldClosed, HeldLinked int
}

// diagnose runs Diagnose on dir in this process, calling inject at every
// storage stage. Every *os.File Diagnose acquires is retained and, after it
// returns, must report os.ErrClosed for both Stat and Read.
func diagnose(t *testing.T, dir string, inject func(stage, path string) error) probe {
	t.Helper()
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	ns := r.ns
	rel := func(p string) string { return strings.TrimPrefix(strings.TrimPrefix(p, ns), "/") }
	final := filepath.Join(ns, tID+".json")
	var p probe
	var files []*os.File
	diagTrack = func(f *os.File, opened bool) {
		if opened {
			files = append(files, f)
		}
	}
	withHook(t, func(stage, path string) error {
		p.Stages = append(p.Stages, stage+" "+rel(path))
		if strings.HasPrefix(rel(path), ".pending-") {
			for _, f := range files {
				if f.Name() != final {
					continue
				}
				fi, err := f.Stat()
				switch {
				case errors.Is(err, os.ErrClosed):
					p.HeldClosed++
				case err != nil:
					t.Errorf("held final descriptor: %v", err)
				default:
					p.HeldOpen++
					if lfi, err := os.Lstat(path); stage == "diag-close" && err == nil && os.SameFile(fi, lfi) {
						p.HeldLinked++
					}
				}
			}
		}
		if inject != nil {
			return inject(stage, path)
		}
		return nil
	})
	defer func() { diagTrack = nil; hook = nil }()
	d, err := r.Diagnose(tID)
	p.D = d
	if err != nil {
		p.Code = codeOf(err)
	}
	p.Opened = len(files)
	buf := make([]byte, 1)
	for _, f := range files {
		_, serr := f.Stat()
		_, rerr := f.Read(buf)
		if !errors.Is(serr, os.ErrClosed) || !errors.Is(rerr, os.ErrClosed) {
			p.Open = append(p.Open, rel(f.Name()))
			f.Close()
		}
	}
	return p
}

// Child processes: unsafe-entry, special-bit, socket and boundary-change
// probes run Diagnose in a child of this test binary, bounded to 10 seconds
// and always waited for, so a blocked open cannot outlive the test.
const (
	diagChildEnv     = "BAW_STATE_TEST_DIAG_CHILD" // callback spec, "-" for none
	diagChildDirEnv  = "BAW_STATE_TEST_DIAG_DIR"
	diagResultPrefix = "DIAG-RESULT "
)

// TestDiagnoseChildProcess is executed only as a child of this test binary.
func TestDiagnoseChildProcess(t *testing.T) {
	spec := os.Getenv(diagChildEnv)
	if spec == "" {
		return
	}
	dir := os.Getenv(diagChildDirEnv)
	inject := childInject(t, filepath.Join(mustEval(t, dir), Namespace), spec)
	p := diagnose(t, dir, inject)
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("\n%s%s\n", diagResultPrefix, b)
}

// capped keeps at most 64 KiB and never blocks or fails the writer.
type capped struct{ b bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := 1<<16 - c.b.Len(); room > 0 {
		c.b.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// diagnoseChild runs Diagnose on dir in a joined child with the callbacks
// named by spec (see childInject) and returns its reported probe.
func diagnoseChild(t *testing.T, dir, spec string) probe {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDiagnoseChildProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), diagChildEnv+"="+spec, diagChildDirEnv+"="+dir)
	cmd.WaitDelay = time.Second
	var out, errb capped
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	err := cmd.Run() // waits for the child also when it is killed at the deadline
	elapsed := time.Since(start)
	if ctx.Err() != nil {
		t.Fatalf("diagnose child %q not finished within 10s; killed and joined after %v (%v)\nstdout %q\nstderr %q",
			spec, elapsed, err, out.b.String(), errb.b.String())
	}
	if err != nil {
		t.Fatalf("diagnose child %q: %v after %v\nstdout %q\nstderr %q", spec, err, elapsed, out.b.String(), errb.b.String())
	}
	_, res, ok := strings.Cut(out.b.String(), diagResultPrefix)
	res, _, _ = strings.Cut(res, "\n")
	var p probe
	if !ok || json.Unmarshal([]byte(res), &p) != nil {
		t.Fatalf("diagnose child %q reported no result: %q", spec, out.b.String())
	}
	return p
}

// entryName maps a fixture role to its namespace name.
func entryName(role string) string {
	return map[string]string{"final": tID + ".json", "pendingA": pend(rndA), "pendingB": pend(rndB)}[role]
}

// Replacements are prepared beside the namespace while the original exists;
// a displaced original is kept there so its inode stays in use.
func replacementPath(ns string) string { return filepath.Join(filepath.Dir(ns), "replacement") }
func displacedPath(ns string) string   { return filepath.Join(filepath.Dir(ns), "displaced") }

// childInject builds the stage callbacks for spec: "-" for none, or
// ";"-separated "OP STAGE ROLE" items, each acting once at the first STAGE on
// ROLE's name. OP is swap (install the prepared replacement), remove (move the
// name aside), rewrite (same-inode content change to partialData), chmod
// (same-inode mode 0640) or eio (return EIO). Every fixture fact is checked
// here; an unreached item fails the test.
func childInject(t *testing.T, ns, spec string) func(stage, path string) error {
	t.Helper()
	if spec == "-" {
		return nil
	}
	var steps []func(stage, path string) error
	for _, item := range strings.Split(spec, ";") {
		f := strings.Fields(item)
		if len(f) != 3 || entryName(f[2]) == "" {
			t.Fatalf("bad callback spec %q", item)
		}
		op, at, name := f[0], f[1], entryName(f[2])
		var act func(path string) error
		switch op {
		case "swap":
			old, err := os.Lstat(filepath.Join(ns, name))
			if err != nil {
				t.Fatalf("swap original: %v", err)
			}
			repl, err := os.Lstat(replacementPath(ns))
			if err != nil {
				t.Fatalf("swap replacement: %v", err)
			}
			if os.SameFile(old, repl) {
				t.Fatal("replacement has the original's device and inode")
			}
			act = func(path string) error {
				if err := os.Rename(path, displacedPath(ns)); err != nil {
					t.Errorf("displace original: %v", err)
					return nil
				}
				if err := os.Rename(replacementPath(ns), path); err != nil {
					t.Errorf("install replacement: %v", err)
					return nil
				}
				got, err := os.Lstat(path)
				if err != nil || !os.SameFile(got, repl) || os.SameFile(got, old) || got.Mode() != repl.Mode() {
					t.Errorf("installed entry is not the prepared replacement: %v", err)
				}
				return nil
			}
		case "remove":
			act = func(path string) error {
				if err := os.Rename(path, displacedPath(ns)); err != nil {
					t.Errorf("remove: %v", err)
				}
				if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("name still present: %v", err)
				}
				return nil
			}
		case "rewrite", "chmod":
			act = func(path string) error {
				before, err := os.Lstat(path)
				if err != nil {
					t.Errorf("before: %v", err)
					return nil
				}
				want := before.Mode()
				if op == "chmod" {
					want = 0o640
					err = os.Chmod(path, want)
				} else {
					err = rewrite(path, partialData)
				}
				if err != nil {
					t.Errorf("%s: %v", op, err)
				}
				after, err := os.Lstat(path)
				if err != nil || !os.SameFile(before, after) || after.Mode() != want {
					t.Errorf("%s did not keep the same inode with mode %v: %v", op, want, err)
				}
				if b, err := os.ReadFile(path); op == "rewrite" && (err != nil || !bytes.Equal(b, partialData)) {
					t.Errorf("rewrite content not established: %v", err)
				}
				return nil
			}
		case "eio":
			act = func(string) error { return syscall.EIO }
		default:
			t.Fatalf("bad callback op %q", op)
		}
		done := false
		t.Cleanup(func() {
			if !done {
				t.Errorf("boundary callback %q not reached", item)
			}
		})
		steps = append(steps, func(stage, path string) error {
			if done || stage != at || filepath.Base(path) != name {
				return nil
			}
			done = true
			return act(path)
		})
	}
	return func(stage, path string) error {
		for _, s := range steps {
			if err := s(stage, path); err != nil {
				return err
			}
		}
		return nil
	}
}

// rewrite truncates and rewrites path in place, keeping its inode.
func rewrite(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// prepare creates the replacement used by a swap, of the given kind, and
// checks its actual type and mode.
func prepare(t *testing.T, ns, kind string, data []byte, mode os.FileMode) {
	t.Helper()
	p := replacementPath(ns)
	var err error
	want := mode
	switch kind {
	case "file":
		if err = os.WriteFile(p, data, 0o600); err == nil {
			err = os.Chmod(p, mode)
		}
	case "fifo":
		if err = syscall.Mkfifo(p, 0o600); err == nil {
			err = os.Chmod(p, 0o600)
		}
		want = os.ModeNamedPipe | 0o600
	case "dir":
		if err = os.Mkdir(p, 0o700); err == nil {
			err = os.Chmod(p, 0o700)
		}
		want = os.ModeDir | 0o700
	case "symlink":
		err = os.Symlink(filepath.Join(ns, tID+".json"), p)
		want = os.ModeSymlink
	}
	if err != nil {
		t.Fatalf("prepare %s: %v", kind, err)
	}
	fi, err := os.Lstat(p)
	if err != nil || (kind == "symlink" && fi.Mode().Type() != want) || (kind != "symlink" && fi.Mode() != want) {
		t.Fatalf("prepared %s is not %v: %v", kind, want, err)
	}
	if !owned(fi) {
		t.Fatal("prepared replacement is not owned by the current user")
	}
}

// want is a hand-written expected diagnosis:
// namespace, final, total, valid, invalid, too large, unsupported, linked.
type want struct {
	ns    bool
	final string
	c     [6]int
}

func (p probe) check(t *testing.T, w want) {
	t.Helper()
	if p.Code != "" {
		t.Fatalf("unexpected error %s (stages %v)", p.Code, p.Stages)
	}
	s := p.D.Staging
	got := want{p.D.NamespacePresent, p.D.Final, [6]int{s.Total, s.Valid, s.InvalidRecord, s.RecordTooLarge, s.UnsupportedVersion, s.LinkedToFinal}}
	if got != w {
		t.Fatalf("got %+v want %+v", got, w)
	}
	if len(p.Open) != 0 {
		t.Fatalf("descriptors not closed: %v", p.Open)
	}
	// Only a valid final is held, and it stays open through every staging
	// lookup and link comparison.
	switch {
	case w.final == "valid" && w.c[0] > 0:
		if p.HeldOpen == 0 || p.HeldClosed != 0 || p.HeldLinked != w.c[5] {
			t.Fatalf("held final open=%d closed=%d linked=%d", p.HeldOpen, p.HeldClosed, p.HeldLinked)
		}
	case w.final != "valid" && p.HeldOpen != 0:
		t.Fatalf("non-valid final held open during staging reads")
	}
}

func (p probe) fails(t *testing.T, code Code) {
	t.Helper()
	if p.Code != code {
		t.Fatalf("got %q want %s (stages %v)", p.Code, code, p.Stages)
	}
	if p.D != (Diagnosis{}) {
		t.Fatalf("partial diagnosis %+v", p.D)
	}
	if len(p.Open) != 0 {
		t.Fatalf("descriptors not closed: %v of %d", p.Open, p.Opened)
	}
}

func padded(n int) []byte {
	body := obj(fields1())
	return []byte(strings.Repeat(" ", n-len(body)) + body)
}

// jsonEsc starts a JSON unicode escape in raw record bytes: run_ jsonEsc 0069d
// is the raw key spelling that decodes to run_id.
const jsonEsc = `\` + "u"

var (
	escDup      = []byte(obj(append(fields1(), field{"run_" + jsonEsc + "0069d", `"` + tID + `"`})))
	escDupV2    = []byte(obj(append(set(fields1(), "schema_version", "2"), field{"schema_" + jsonEsc + "0076ersion", "2"})))
	v2          = []byte(obj(drop(set(fields1(), "schema_version", "2"), "run_id")))
	v2dup       = []byte(obj(append(set(fields1(), "schema_version", "2"), field{"run_id", `"x"`})))
	partialData = validData()[:len(validData())/2]
)

// TestDiagnoseEscapedFixtures guards the escaped-duplicate fixtures: the raw
// bytes carry the JSON escape and the literal name only once.
func TestDiagnoseEscapedFixtures(t *testing.T) {
	for _, c := range []struct {
		data             []byte
		escaped, literal string
	}{
		{escDup, `"run_` + `\` + `u0069d":`, `"run_id":`},
		{escDupV2, `"schema_` + `\` + `u0076ersion":2`, `"schema_version":2`},
	} {
		if !bytes.Contains(c.data, []byte(c.escaped)) || bytes.Count(c.data, []byte(c.literal)) != 1 ||
			len(c.escaped) != len(c.literal)+5 {
			t.Fatalf("fixture lacks escape %s or has a literal duplicate: %s", c.escaped, c.data)
		}
	}
	if _, err := Parse(v2, tID); codeOf(err) != "unsupported_record_version" {
		t.Fatalf("version 2 without duplicates: %v", err)
	}
}

func TestDiagnoseAbsentNamespace(t *testing.T) {
	dir := newRoot(t)
	before := meta(t, dir)
	p := diagnose(t, dir, nil)
	p.check(t, want{false, "missing", [6]int{}})
	if meta(t, dir) != before || p.Opened != 0 {
		t.Fatal("absent diagnosis changed state or opened a file")
	}
	if strings.Join(p.Stages, "|") != "diag-absent " {
		t.Fatalf("stages %v", p.Stages)
	}
	// (f) A namespace with a valid record and staging created right after the
	// absent observation changes nothing and triggers no further access.
	ns := filepath.Join(dir, Namespace)
	final := filepath.Join(ns, tID+".json")
	created := false
	p = diagnose(t, dir, func(stage, _ string) error {
		if stage == "diag-absent" {
			created = true
			for _, err := range []error{os.Mkdir(ns, 0o700), os.Chmod(ns, 0o700), os.WriteFile(final, validData(), 0o600),
				os.Chmod(final, 0o600), os.Link(final, filepath.Join(ns, pend(rndA)))} {
				if err != nil {
					t.Errorf("create after absence: %v", err)
				}
			}
		}
		return nil
	})
	p.check(t, want{false, "missing", [6]int{}})
	if !created || strings.Join(p.Stages, "|") != "diag-absent " || p.Opened != 0 {
		t.Fatalf("further namespace access after absence: %v", p.Stages)
	}
	if fi, err := os.Lstat(final); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("namespace not created after absence: %v", err)
	}
}

func TestDiagnoseCategories(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, ns string)
		want  want
	}{
		{"no names", func(t *testing.T, ns string) {}, want{true, "missing", [6]int{}}},
		{"partial pending only", func(t *testing.T, ns string) { put(t, filepath.Join(ns, pend(rndA)), partialData) },
			want{true, "missing", [6]int{1, 0, 1, 0, 0, 0}}},
		{"complete pending only", func(t *testing.T, ns string) { put(t, filepath.Join(ns, pend(rndA)), validData()) },
			want{true, "missing", [6]int{1, 1, 0, 0, 0, 0}}},
		{"valid final, separate identical copy", func(t *testing.T, ns string) {
			put(t, filepath.Join(ns, tID+".json"), validData())
			put(t, filepath.Join(ns, pend(rndA)), validData())
			a, err1 := os.Lstat(filepath.Join(ns, tID+".json"))
			b, err2 := os.Lstat(filepath.Join(ns, pend(rndA)))
			if err1 != nil || err2 != nil || os.SameFile(a, b) {
				t.Fatal("fixture copy shares an inode")
			}
		}, want{true, "valid", [6]int{1, 1, 0, 0, 0, 0}}},
		{"valid final with no staging", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), validData()) },
			want{true, "valid", [6]int{}}},
		{"valid final plus mixed pending", func(t *testing.T, ns string) {
			final := filepath.Join(ns, tID+".json")
			put(t, final, validData())
			link(t, final, filepath.Join(ns, pend(rndA)))
			put(t, filepath.Join(ns, pend(rndB)), padded(MaxRecordBytes))
			put(t, filepath.Join(ns, pend(rndC)), padded(MaxRecordBytes+1))
			put(t, filepath.Join(ns, pend(rndD)), escDup)
			put(t, filepath.Join(ns, pend(rndE)), v2)
			put(t, filepath.Join(ns, pend(rndF)), v2dup)
		}, want{true, "valid", [6]int{6, 2, 2, 1, 1, 1}}},
		{"escaped duplicates in staging", func(t *testing.T, ns string) {
			put(t, filepath.Join(ns, tID+".json"), validData())
			put(t, filepath.Join(ns, pend(rndA)), escDup)
			put(t, filepath.Join(ns, pend(rndB)), escDupV2)
			put(t, filepath.Join(ns, pend(rndC)), v2)
		}, want{true, "valid", [6]int{3, 0, 2, 0, 1, 0}}},
		{"escaped duplicate final", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), escDup) },
			want{true, "invalid_record", [6]int{}}},
		{"escaped duplicate version 2 final is invalid", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), escDupV2) },
			want{true, "invalid_record", [6]int{}}},
		{"invalid final with linked staging", func(t *testing.T, ns string) {
			final := filepath.Join(ns, tID+".json")
			put(t, final, escDup)
			link(t, final, filepath.Join(ns, pend(rndA)))
		}, want{true, "invalid_record", [6]int{1, 0, 1, 0, 0, 0}}},
		{"oversized final with linked staging", func(t *testing.T, ns string) {
			final := filepath.Join(ns, tID+".json")
			put(t, final, padded(MaxRecordBytes+1))
			link(t, final, filepath.Join(ns, pend(rndA)))
		}, want{true, "record_too_large", [6]int{1, 0, 0, 1, 0, 0}}},
		{"unsupported final, valid pending", func(t *testing.T, ns string) {
			put(t, filepath.Join(ns, tID+".json"), v2)
			put(t, filepath.Join(ns, pend(rndA)), validData())
		}, want{true, "unsupported_record_version", [6]int{1, 1, 0, 0, 0, 0}}},
		{"exact-size valid final", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), padded(MaxRecordBytes)) },
			want{true, "valid", [6]int{}}},
		{"version 2 literal duplicate final is invalid", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), v2dup) },
			want{true, "invalid_record", [6]int{}}},
		{"other-ID final is invalid", func(t *testing.T, ns string) {
			put(t, filepath.Join(ns, tID+".json"), []byte(obj(set(fields1(), "run_id", `"ffeeddccbbaa99887766554433221100"`))))
		}, want{true, "invalid_record", [6]int{}}},
		{"ignored malformed, foreign and unsafe names", func(t *testing.T, ns string) {
			outside := filepath.Join(filepath.Dir(ns), "run.json")
			put(t, filepath.Join(ns, tID+".json"), validData())
			for _, err := range []error{
				os.Symlink(outside, filepath.Join(ns, pend(rndA[:31]))),
				syscall.Mkfifo(filepath.Join(ns, pend(strings.ToUpper("abcdef")+rndA[6:])), 0o600),
				os.WriteFile(filepath.Join(ns, pend(rndA)+"x"), []byte("dummy-secret-xyz"), 0o644),
				os.Mkdir(filepath.Join(ns, ".pending-ffeeddccbbaa99887766554433221100-"+rndA), 0o755),
				os.Symlink(outside, filepath.Join(ns, "ffeeddccbbaa99887766554433221100.json")),
				syscall.Mkfifo(filepath.Join(ns, ".pending-"+tID+rndA), 0o600),
				os.WriteFile(filepath.Join(ns, "notes.txt"), []byte("dummy-secret-xyz"), 0o666),
			} {
				if err != nil {
					t.Fatal(err)
				}
			}
		}, want{true, "valid", [6]int{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, ns := nsRoot(t)
			c.setup(t, ns)
			before := meta(t, dir)
			p := diagnoseChild(t, dir, "-")
			p.check(t, c.want)
			if meta(t, dir) != before {
				t.Fatal("diagnosis changed saved data or metadata")
			}
			// Only the final name and strictly matching names are looked at.
			for _, s := range p.Stages {
				_, path, _ := strings.Cut(s, " ")
				if path != "" && path != tID+".json" && !(strings.HasPrefix(path, ".pending-"+tID+"-") && len(path) == len(pend(rndA))) {
					t.Fatalf("touched unrelated name %q", s)
				}
			}
		})
	}
}

// TestDiagnoseCreatedRecord uses a successful Create: its retained staging
// file is the same inode, established here independently with Lstat.
func TestDiagnoseCreatedRecord(t *testing.T) {
	dir := newRoot(t)
	if err := create(t, dir, validData()); err != nil {
		t.Fatal(err)
	}
	hook = nil
	ps := pending(t, dir)
	if len(ps) != 1 {
		t.Fatalf("fixture: staging %v", ps)
	}
	a, err1 := os.Lstat(filepath.Join(dir, Namespace, tID+".json"))
	b, err2 := os.Lstat(ps[0])
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Fatal("fixture: staging is not linked")
	}
	before := meta(t, dir)
	diagnose(t, dir, nil).check(t, want{true, "valid", [6]int{1, 1, 0, 0, 0, 1}})
	if meta(t, dir) != before {
		t.Fatal("diagnosis changed state")
	}
}

func TestDiagnoseUnsafeEntries(t *testing.T) {
	kinds := []struct {
		name string
		make func(p, outside string) error
		mode os.FileMode // established type and permission bits
		want Code
	}{
		{"symlink", func(p, o string) error { return os.Symlink(o, p) }, os.ModeSymlink, "unsafe_state_path"},
		{"fifo", func(p, o string) error { return syscall.Mkfifo(p, 0o600) }, os.ModeNamedPipe | 0o600, "unsafe_state_path"},
		{"directory", func(p, o string) error { return os.Mkdir(p, 0o700) }, os.ModeDir | 0o700, "unsafe_state_path"},
		{"mode 0644", func(p, o string) error { return os.Chmod(p, 0o644) }, 0o644, "state_permissions"},
		{"mode 0400", func(p, o string) error { return os.Chmod(p, 0o400) }, 0o400, "state_permissions"},
	}
	for _, k := range kinds {
		for _, which := range []string{"final", "pending"} {
			t.Run(which+" "+k.name, func(t *testing.T) {
				dir, ns := nsRoot(t)
				outside := filepath.Join(dir, "run.json")
				name := tID + ".json"
				if which == "pending" {
					put(t, filepath.Join(ns, name), validData())
					name = pend(rndA)
				}
				p := filepath.Join(ns, name)
				if k.mode.Type() == 0 {
					put(t, p, validData())
				}
				if err := k.make(p, outside); err != nil {
					t.Fatal(err)
				}
				fi, err := os.Lstat(p)
				if err != nil || (k.mode == os.ModeSymlink && fi.Mode().Type() != os.ModeSymlink) ||
					(k.mode != os.ModeSymlink && fi.Mode() != k.mode) || !owned(fi) {
					t.Fatalf("fixture not established: %v %v", fi, err)
				}
				before := meta(t, dir)
				diagnoseChild(t, dir, "-").fails(t, k.want)
				if meta(t, dir) != before {
					t.Fatal("changed state")
				}
			})
		}
	}
	// Unsafe namespace descriptor (checked after open): mode changed after OpenRoot.
	dir, ns := nsRoot(t)
	diagnose(t, dir, func(stage, _ string) error {
		if stage == "diag-scan-open" {
			if err := os.Chmod(ns, 0o755); err != nil {
				t.Errorf("chmod namespace: %v", err)
			}
		}
		return nil
	}).fails(t, "state_permissions")
}

// diagSpecialEnv optionally names a complete existing fixture for the
// special-bit cases, for environments where chmod cannot set a setuid bit:
// DIR/final and DIR/pending are state roots each holding one current-user
// mode 04600 regular file, the final record or staging name A respectively.
// It is read by tests only and never modified; baw never reads it.
const diagSpecialEnv = "BAW_TEST_DIAG_SPECIAL_FIXTURES"

// TestDiagnoseSpecialBits rejects an actual setuid final record and staging
// file, using the supplied read-only fixture when set, otherwise one created
// and checked here. An invalid fixture fails, never skips.
func TestDiagnoseSpecialBits(t *testing.T) {
	base := os.Getenv(diagSpecialEnv)
	if base == "" {
		base = makeSpecialBits(t)
	}
	before := meta(t, base)
	checkSpecialBits(t, base)
	for _, kind := range []string{"final", "pending"} {
		t.Run(kind, func(t *testing.T) {
			diagnoseChild(t, filepath.Join(base, kind), "-").fails(t, "state_permissions")
		})
	}
	if meta(t, base) != before {
		t.Fatal("special-bit fixture changed")
	}
}

func makeSpecialBits(t *testing.T) string {
	t.Helper()
	base := filepath.Join(t.TempDir(), "special-bits")
	for _, d := range []string{"", "final", "final/" + Namespace, "pending", "pending/" + Namespace} {
		p := filepath.Join(base, d)
		if err := os.Mkdir(p, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{filepath.Join(base, "final", Namespace, tID+".json"), filepath.Join(base, "pending", Namespace, pend(rndA))} {
		put(t, p, validData())
		if err := os.Chmod(p, 0o600|os.ModeSetuid); err != nil {
			t.Fatal(err)
		}
	}
	return base
}

// checkSpecialBits validates the complete fixture: exact listings, private
// current-user directories and single-link regular files with mode 04600.
func checkSpecialBits(t *testing.T, base string) {
	t.Helper()
	list := func(dir string, want ...string) {
		ents, err := os.ReadDir(dir)
		var got []string
		for _, e := range ents {
			got = append(got, e.Name())
		}
		if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("special-bit fixture %s lists %v, want %v: %v", dir, got, want, err)
		}
	}
	privateDir := func(dir string) {
		fi, err := os.Lstat(dir)
		if err != nil || !fi.IsDir() || !private(fi, 0o700) {
			t.Fatalf("special-bit fixture %s is not a current-user 0700 directory: %v", dir, err)
		}
	}
	privateDir(base)
	list(base, "final", "pending")
	for kind, name := range map[string]string{"final": tID + ".json", "pending": pend(rndA)} {
		root, ns := filepath.Join(base, kind), filepath.Join(base, kind, Namespace)
		privateDir(root)
		list(root, Namespace)
		privateDir(ns)
		list(ns, name)
		fi, err := os.Lstat(filepath.Join(ns, name))
		if err != nil || fi.Mode() != 0o600|os.ModeSetuid || !owned(fi) || fi.Sys().(*syscall.Stat_t).Nlink != 1 {
			t.Fatalf("special-bit fixture %s is not a current-user single-link 04600 regular file: %v %v", name, fi, err)
		}
	}
}

// TestDiagnoseSocketRecord rejects an actual closed socket at the final
// record path, using the read-only supplied root when set.
func TestDiagnoseSocketRecord(t *testing.T) {
	dir := os.Getenv(socketStateEnv)
	if dir == "" {
		dir = makeSocketState(t)
	}
	if fi, err := os.Lstat(filepath.Join(dir, Namespace)); err != nil || !fi.IsDir() || !private(fi, 0o700) {
		t.Fatalf("socket state namespace is not a private directory: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(dir, Namespace, tID+".json"))
	if err != nil || fi.Mode().Type() != os.ModeSocket || !owned(fi) {
		t.Fatalf("socket fixture is not a current-user socket: %v", err)
	}
	before := meta(t, dir)
	diagnoseChild(t, dir, "-").fails(t, "unsafe_state_path")
	if meta(t, dir) != before {
		t.Fatal("socket state fixture changed")
	}
}

func TestDiagnoseScanLimits(t *testing.T) {
	fill := func(t *testing.T, ns string, n int) {
		for i := 0; i < n; i++ {
			if err := os.WriteFile(filepath.Join(ns, fmt.Sprintf("other-%04d", i)), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(t *testing.T, ns string, n int) {
		if ents, err := os.ReadDir(ns); err != nil || len(ents) != n {
			t.Fatalf("fixture has %d names, want %d: %v", len(ents), n, err)
		}
	}
	// 1024 names in total (final + 32 staging + 991 others) is accepted.
	dir, ns := nsRoot(t)
	put(t, filepath.Join(ns, tID+".json"), validData())
	for i := 0; i < 32; i++ {
		put(t, filepath.Join(ns, pend(fmt.Sprintf("%032x", i))), validData())
	}
	fill(t, ns, 991)
	count(t, ns, 1024)
	diagnose(t, dir, nil).check(t, want{true, "valid", [6]int{32, 32, 0, 0, 0, 0}})
	// The 1025th name of any kind refuses before the final lookup.
	fill(t, ns, 992)
	count(t, ns, 1025)
	p := diagnose(t, dir, nil)
	p.fails(t, "state_scan_limit")
	for _, s := range p.Stages {
		if strings.HasPrefix(s, "diag-lstat") {
			t.Fatal("final looked up after scan limit")
		}
	}
	// The 33rd matching staging name refuses.
	dir, ns = nsRoot(t)
	for i := 0; i < 33; i++ {
		put(t, filepath.Join(ns, pend(fmt.Sprintf("%032x", i))), validData())
	}
	diagnose(t, dir, nil).fails(t, "state_scan_limit")
	if err := os.Remove(filepath.Join(ns, pend(fmt.Sprintf("%032x", 32)))); err != nil {
		t.Fatal(err)
	}
	count(t, ns, 32)
	diagnose(t, dir, nil).check(t, want{true, "missing", [6]int{32, 32, 0, 0, 0, 0}})
}

// faultFixture: a valid final linked to staging A, and a separate valid B.
func faultFixture(t *testing.T) (dir, ns string) {
	dir, ns = nsRoot(t)
	final := filepath.Join(ns, tID+".json")
	put(t, final, validData())
	link(t, final, filepath.Join(ns, pend(rndA)))
	put(t, filepath.Join(ns, pend(rndB)), validData())
	return dir, ns
}

func TestDiagnoseFaults(t *testing.T) {
	eio := syscall.EIO
	cases := []struct {
		stage, target string // target "" is the namespace
		err           error
		want          Code
	}{
		{"diag-scan-open", "", eio, "state_unavailable"},
		{"diag-scan-stat", "", eio, "state_unavailable"},
		{"diag-scan-read", "", eio, "state_unavailable"},
		{"diag-scan-close", "", eio, "state_unavailable"},
		{"diag-lstat", "final", eio, "record_unavailable"},
		{"diag-lstat", "pendingB", eio, "record_unavailable"},
		{"diag-lstat", "pendingB", syscall.ENOENT, "state_changed"},
		{"diag-open", "final", syscall.EACCES, "record_unavailable"},
		{"diag-open", "final", syscall.ENOENT, "state_changed"},
		{"diag-open", "final", syscall.ELOOP, "state_changed"},
		{"diag-open", "pendingB", eio, "record_unavailable"},
		{"diag-open", "pendingB", syscall.ENOENT, "state_changed"},
		{"diag-open", "pendingB", syscall.ELOOP, "state_changed"},
		{"diag-fstat", "final", eio, "record_unavailable"},
		{"diag-fstat", "pendingB", eio, "record_unavailable"},
		{"diag-read", "final", eio, "record_unavailable"},
		{"diag-read", "pendingB", eio, "record_unavailable"},
		{"diag-close", "final", eio, "record_unavailable"},
		{"diag-close", "pendingB", eio, "record_unavailable"},
	}
	for _, c := range cases {
		t.Run(c.stage+" "+c.target+" "+c.err.Error(), func(t *testing.T) {
			dir, ns := faultFixture(t)
			target := map[string]string{"": ns, "final": filepath.Join(ns, tID+".json"), "pendingB": filepath.Join(ns, pend(rndB))}[c.target]
			stage := c.stage
			if stage == "diag-scan-close" {
				stage = "diag-close"
			}
			before := meta(t, dir)
			p := diagnose(t, dir, func(s, path string) error {
				if s == stage && path == target {
					return c.err
				}
				return nil
			})
			p.fails(t, c.want)
			if meta(t, dir) != before {
				t.Fatal("fault changed state")
			}
			joined := strings.Join(p.Stages, "|")
			// Scan faults precede the final lookup; final faults precede staging.
			if c.target == "" && strings.Contains(joined, "diag-lstat") {
				t.Fatalf("lookup after scan failure: %s", joined)
			}
			if c.target == "final" && c.stage != "diag-close" && strings.Contains(joined, ".pending-") {
				t.Fatalf("staging read after final failure: %s", joined)
			}
			// A staging failure happens while the valid final is genuinely
			// held open; afterwards that descriptor reports os.ErrClosed.
			if c.target == "pendingB" && (p.HeldOpen == 0 || p.HeldClosed != 0 || p.Opened < 3) {
				t.Fatalf("held final open=%d closed=%d opened=%d: %s", p.HeldOpen, p.HeldClosed, p.Opened, joined)
			}
		})
	}
	// The final name missing at its first Lstat is an observation, not an error.
	dir, ns := faultFixture(t)
	final := filepath.Join(ns, tID+".json")
	diagnose(t, dir, func(s, path string) error {
		if s == "diag-lstat" && path == final {
			return syscall.ENOENT
		}
		return nil
	}).check(t, want{true, "missing", [6]int{2, 2, 0, 0, 0, 0}})
}

// TestDiagnoseEarlierErrorWins: a close error while unwinding never replaces
// the first fatal code, and the descriptor is still actually closed.
func TestDiagnoseEarlierErrorWins(t *testing.T) {
	dir, ns := faultFixture(t)
	final, b := filepath.Join(ns, tID+".json"), filepath.Join(ns, pend(rndB))
	p := diagnose(t, dir, func(s, path string) error {
		switch {
		case s == "diag-open" && path == b:
			return syscall.ENOENT
		case s == "diag-close" && path == final:
			return syscall.EIO
		}
		return nil
	})
	p.fails(t, "state_changed")
	if p.HeldOpen == 0 || p.HeldClosed != 0 {
		t.Fatalf("held final open=%d closed=%d", p.HeldOpen, p.HeldClosed)
	}
	// Unsafe descriptor plus its own close error: the safety code wins.
	dir, ns = faultFixture(t)
	prepare(t, ns, "fifo", nil, 0)
	diagnoseChild(t, dir, "swap diag-open pendingB;eio diag-close pendingB").fails(t, "unsafe_state_path")
	// Unsafe namespace descriptor plus a scan close failure: the safety code wins.
	dir, ns = faultFixture(t)
	diagnose(t, dir, func(s, path string) error {
		switch {
		case s == "diag-scan-stat":
			if err := os.Chmod(ns, 0o750); err != nil {
				t.Errorf("chmod namespace: %v", err)
			}
		case s == "diag-close" && path == ns:
			return syscall.EIO
		}
		return nil
	}).fails(t, "state_permissions")
}

// TestDiagnoseIdentity covers C1 observation boundaries (a)-(e); (f) is in
// TestDiagnoseAbsentNamespace. Each case runs in a joined child whose
// callbacks install a replacement prepared, and checked as a different
// file, while the original existed.
func TestDiagnoseIdentity(t *testing.T) {
	cases := []struct {
		name string
		kind string // prepared replacement: file, fifo, dir, symlink or ""
		data []byte
		mode os.FileMode
		spec string
		want *want
		code Code
	}{
		// (a) Replacement before the first Lstat is undetectable: later bytes count.
		{"a pre-Lstat safe replacement", "file", escDup, 0o600, "swap diag-lstat pendingB",
			&want{true, "valid", [6]int{2, 1, 1, 0, 0, 1}}, ""},
		// (b) A listed staging name gone at its Lstat.
		{"b listed name removed", "", nil, 0, "remove diag-lstat pendingB", nil, "state_changed"},
		// (c) Safe different inode between Lstat and open.
		{"c pending replaced after Lstat", "file", validData(), 0o600, "swap diag-open pendingB", nil, "state_changed"},
		{"c final replaced after Lstat", "file", validData(), 0o600, "swap diag-open final", nil, "state_changed"},
		{"c final removed after Lstat", "", nil, 0, "remove diag-open final", nil, "state_changed"},
		{"c pending symlink after Lstat", "symlink", nil, 0, "swap diag-open pendingB", nil, "state_changed"},
		// (d) The observed descriptor's safety wins over the identity change.
		{"d fifo after Lstat", "fifo", nil, 0, "swap diag-open pendingB", nil, "unsafe_state_path"},
		{"d directory after Lstat", "dir", nil, 0, "swap diag-open final", nil, "unsafe_state_path"},
		{"d wrong mode new inode", "file", validData(), 0o644, "swap diag-open pendingB", nil, "state_permissions"},
		{"d wrong mode same inode", "", nil, 0, "chmod diag-open final", nil, "state_permissions"},
		// (e) Same-inode content change between reads is not detected: the
		// final and its linked staging name are observed at different times.
		{"e same inode rewritten between reads", "", nil, 0, "rewrite diag-read pendingA",
			&want{true, "valid", [6]int{2, 1, 1, 0, 0, 1}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, ns := faultFixture(t)
			if c.kind != "" {
				prepare(t, ns, c.kind, c.data, c.mode)
			}
			p := diagnoseChild(t, dir, c.spec)
			if c.want != nil {
				p.check(t, *c.want)
			} else {
				p.fails(t, c.code)
			}
		})
	}
}

func TestDiagnoseNeverCreatesNamespace(t *testing.T) {
	dir := newRoot(t)
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Diagnose(tID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, Namespace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("namespace created")
	}
}
