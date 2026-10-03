package state

import (
	"errors"
	"fmt"
	"os"
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
			b, _ := os.ReadFile(p)
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

// probe is one Diagnose call with descriptor tracking and recorded stages.
type probe struct {
	d       Diagnosis
	err     error
	stages  []string // "stage path" relative to the namespace
	open    int      // descriptors still open on return
	opened  int
	timeout bool
}

// diagnose runs Diagnose on dir, calling inject at every storage stage.
func diagnose(t *testing.T, dir string, inject func(stage, path string) error) probe {
	t.Helper()
	r, err := OpenRoot(dir)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}
	ns := r.ns
	var p probe
	live := map[*os.File]bool{}
	diagTrack = func(f *os.File, opened bool) {
		if opened {
			p.opened++
			live[f] = true
		} else {
			delete(live, f)
		}
	}
	withHook(t, func(stage, path string) error {
		p.stages = append(p.stages, stage+" "+strings.TrimPrefix(strings.TrimPrefix(path, ns), "/"))
		if inject != nil {
			return inject(stage, path)
		}
		return nil
	})
	defer func() { diagTrack = nil; hook = nil }()
	done := make(chan struct{})
	go func() { p.d, p.err = r.Diagnose(tID); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("diagnose blocked")
	}
	p.open = len(live)
	return p
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
	if p.err != nil {
		t.Fatalf("unexpected error %v", p.err)
	}
	s := p.d.Staging
	got := want{p.d.NamespacePresent, p.d.Final, [6]int{s.Total, s.Valid, s.InvalidRecord, s.RecordTooLarge, s.UnsupportedVersion, s.LinkedToFinal}}
	if got != w {
		t.Fatalf("got %+v want %+v", got, w)
	}
	if p.open != 0 {
		t.Fatalf("%d descriptors left open", p.open)
	}
}

func (p probe) fails(t *testing.T, code Code) {
	t.Helper()
	if codeOf(p.err) != code {
		t.Fatalf("got %v want %s (stages %v)", p.err, code, p.stages)
	}
	if p.d != (Diagnosis{}) {
		t.Fatalf("partial diagnosis %+v", p.d)
	}
	if p.open != 0 {
		t.Fatalf("%d of %d descriptors left open", p.open, p.opened)
	}
}

func padded(n int) []byte {
	body := obj(fields1())
	return []byte(strings.Repeat(" ", n-len(body)) + body)
}

var (
	escDup      = []byte(obj(append(fields1(), field{"run_" + `i` + "d", `"` + tID + `"`})))
	v2          = []byte(obj(drop(set(fields1(), "schema_version", "2"), "run_id")))
	v2dup       = []byte(obj(append(set(fields1(), "schema_version", "2"), field{"run_id", `"x"`})))
	partialData = validData()[:len(validData())/2]
)

func TestDiagnoseAbsentNamespace(t *testing.T) {
	dir := newRoot(t)
	before := meta(t, dir)
	p := diagnose(t, dir, nil)
	p.check(t, want{false, "missing", [6]int{}})
	if meta(t, dir) != before || p.opened != 0 {
		t.Fatal("absent diagnosis changed state or opened a file")
	}
	if strings.Join(p.stages, "|") != "diag-absent " {
		t.Fatalf("stages %v", p.stages)
	}
	// (f) A namespace with a valid record and staging created right after the
	// absent observation changes nothing and triggers no further access.
	ns := filepath.Join(dir, Namespace)
	p = diagnose(t, dir, func(stage, _ string) error {
		if stage == "diag-absent" {
			os.Mkdir(ns, 0o700)
			put(t, filepath.Join(ns, tID+".json"), validData())
			os.Link(filepath.Join(ns, tID+".json"), filepath.Join(ns, pend(rndA)))
		}
		return nil
	})
	p.check(t, want{false, "missing", [6]int{}})
	if strings.Join(p.stages, "|") != "diag-absent " {
		t.Fatalf("further namespace access after absence: %v", p.stages)
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
			a, _ := os.Lstat(filepath.Join(ns, tID+".json"))
			b, _ := os.Lstat(filepath.Join(ns, pend(rndA)))
			if os.SameFile(a, b) {
				t.Fatal("fixture copy shares an inode")
			}
		}, want{true, "valid", [6]int{1, 1, 0, 0, 0, 0}}},
		{"valid final with no staging", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), validData()) },
			want{true, "valid", [6]int{}}},
		{"valid final plus mixed pending", func(t *testing.T, ns string) {
			final := filepath.Join(ns, tID+".json")
			put(t, final, validData())
			if err := os.Link(final, filepath.Join(ns, pend(rndA))); err != nil {
				t.Fatal(err)
			}
			put(t, filepath.Join(ns, pend(rndB)), padded(MaxRecordBytes))
			put(t, filepath.Join(ns, pend(rndC)), padded(MaxRecordBytes+1))
			put(t, filepath.Join(ns, pend(rndD)), escDup)
			put(t, filepath.Join(ns, pend(rndE)), v2)
			put(t, filepath.Join(ns, pend(rndF)), v2dup)
		}, want{true, "valid", [6]int{6, 2, 2, 1, 1, 1}}},
		{"invalid final with linked staging", func(t *testing.T, ns string) {
			final := filepath.Join(ns, tID+".json")
			put(t, final, escDup)
			os.Link(final, filepath.Join(ns, pend(rndA)))
		}, want{true, "invalid_record", [6]int{1, 0, 1, 0, 0, 0}}},
		{"oversized final with linked staging", func(t *testing.T, ns string) {
			final := filepath.Join(ns, tID+".json")
			put(t, final, padded(MaxRecordBytes+1))
			os.Link(final, filepath.Join(ns, pend(rndA)))
		}, want{true, "record_too_large", [6]int{1, 0, 0, 1, 0, 0}}},
		{"unsupported final, valid pending", func(t *testing.T, ns string) {
			put(t, filepath.Join(ns, tID+".json"), v2)
			put(t, filepath.Join(ns, pend(rndA)), validData())
		}, want{true, "unsupported_record_version", [6]int{1, 1, 0, 0, 0, 0}}},
		{"exact-size valid final", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), padded(MaxRecordBytes)) },
			want{true, "valid", [6]int{}}},
		{"version 2 duplicate final is invalid", func(t *testing.T, ns string) { put(t, filepath.Join(ns, tID+".json"), v2dup) },
			want{true, "invalid_record", [6]int{}}},
		{"other-ID final is invalid", func(t *testing.T, ns string) {
			put(t, filepath.Join(ns, tID+".json"), []byte(obj(set(fields1(), "run_id", `"ffeeddccbbaa99887766554433221100"`))))
		}, want{true, "invalid_record", [6]int{}}},
		{"ignored malformed, foreign and unsafe names", func(t *testing.T, ns string) {
			outside := filepath.Join(filepath.Dir(ns), "run.json")
			put(t, filepath.Join(ns, tID+".json"), validData())
			os.Symlink(outside, filepath.Join(ns, pend(rndA[:31])))
			syscall.Mkfifo(filepath.Join(ns, pend(strings.ToUpper("abcdef")+rndA[6:])), 0o600)
			os.WriteFile(filepath.Join(ns, pend(rndA)+"x"), []byte("dummy-secret-xyz"), 0o644)
			os.Mkdir(filepath.Join(ns, ".pending-ffeeddccbbaa99887766554433221100-"+rndA), 0o755)
			os.Symlink(outside, filepath.Join(ns, "ffeeddccbbaa99887766554433221100.json"))
			syscall.Mkfifo(filepath.Join(ns, ".pending-"+tID+rndA), 0o600)
			os.WriteFile(filepath.Join(ns, "notes.txt"), []byte("dummy-secret-xyz"), 0o666)
		}, want{true, "valid", [6]int{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, ns := nsRoot(t)
			c.setup(t, ns)
			before := meta(t, dir)
			p := diagnose(t, dir, nil)
			p.check(t, c.want)
			if meta(t, dir) != before {
				t.Fatal("diagnosis changed saved data or metadata")
			}
			// Only the final name and strictly matching names are looked at.
			for _, s := range p.stages {
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
	a, _ := os.Lstat(filepath.Join(dir, Namespace, tID+".json"))
	b, _ := os.Lstat(ps[0])
	if len(ps) != 1 || !os.SameFile(a, b) {
		t.Fatal("fixture: staging is not linked")
	}
	before := meta(t, dir)
	diagnose(t, dir, nil).check(t, want{true, "valid", [6]int{1, 1, 0, 0, 0, 1}})
	if meta(t, dir) != before {
		t.Fatal("diagnosis changed state")
	}
}

func TestDiagnoseUnsafeEntries(t *testing.T) {
	type mk func(t *testing.T, path, outside string)
	kinds := []struct {
		name string
		make mk
		want Code
	}{
		{"symlink", func(t *testing.T, p, o string) { os.Symlink(o, p) }, "unsafe_state_path"},
		{"fifo", func(t *testing.T, p, o string) { syscall.Mkfifo(p, 0o600) }, "unsafe_state_path"},
		{"directory", func(t *testing.T, p, o string) { os.Mkdir(p, 0o700) }, "unsafe_state_path"},
		{"mode 0644", func(t *testing.T, p, o string) { put(t, p, validData()); os.Chmod(p, 0o644) }, "state_permissions"},
		{"mode 0400", func(t *testing.T, p, o string) { put(t, p, validData()); os.Chmod(p, 0o400) }, "state_permissions"},
		{"setuid", func(t *testing.T, p, o string) { put(t, p, validData()); os.Chmod(p, 0o600|os.ModeSetuid) }, "state_permissions"},
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
				k.make(t, filepath.Join(ns, name), outside)
				before := meta(t, dir)
				diagnose(t, dir, nil).fails(t, k.want)
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
			os.Chmod(ns, 0o755)
		}
		return nil
	}).fails(t, "state_permissions")
}

// TestDiagnoseSocketRecord rejects an actual closed socket at the final
// record path, using the read-only supplied root when set.
func TestDiagnoseSocketRecord(t *testing.T) {
	dir := os.Getenv(socketStateEnv)
	if dir == "" {
		dir = makeSocketState(t)
	}
	if fi, err := os.Lstat(filepath.Join(dir, Namespace, tID+".json")); err != nil || fi.Mode().Type() != os.ModeSocket {
		t.Fatalf("socket fixture is not a socket: %v", err)
	}
	before := meta(t, dir)
	diagnose(t, dir, nil).fails(t, "unsafe_state_path")
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
	// 1024 names in total (final + 32 staging + 991 others) is accepted.
	dir, ns := nsRoot(t)
	put(t, filepath.Join(ns, tID+".json"), validData())
	for i := 0; i < 32; i++ {
		put(t, filepath.Join(ns, pend(fmt.Sprintf("%032x", i))), validData())
	}
	fill(t, ns, 991)
	diagnose(t, dir, nil).check(t, want{true, "valid", [6]int{32, 32, 0, 0, 0, 0}})
	// The 1025th name of any kind refuses before the final lookup.
	fill(t, ns, 992)
	p := diagnose(t, dir, nil)
	p.fails(t, "state_scan_limit")
	for _, s := range p.stages {
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
	os.Remove(filepath.Join(ns, pend(fmt.Sprintf("%032x", 32))))
	diagnose(t, dir, nil).check(t, want{true, "missing", [6]int{32, 32, 0, 0, 0, 0}})
}

// faultFixture: a valid final linked to staging A, and a separate valid B.
func faultFixture(t *testing.T) (dir, ns string) {
	dir, ns = nsRoot(t)
	final := filepath.Join(ns, tID+".json")
	put(t, final, validData())
	if err := os.Link(final, filepath.Join(ns, pend(rndA))); err != nil {
		t.Fatal(err)
	}
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
			if strings.HasPrefix(stage, "diag-scan-") {
				stage = map[string]string{"diag-scan-open": "diag-scan-open", "diag-scan-stat": "diag-scan-stat",
					"diag-scan-read": "diag-scan-read", "diag-scan-close": "diag-close"}[stage]
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
			joined := strings.Join(p.stages, "|")
			// Scan faults precede the final lookup; final faults precede staging.
			if c.target == "" && strings.Contains(joined, "diag-lstat") {
				t.Fatalf("lookup after scan failure: %s", joined)
			}
			if c.target == "final" && c.stage != "diag-close" && strings.Contains(joined, ".pending-") {
				t.Fatalf("staging read after final failure: %s", joined)
			}
			if c.target == "pendingB" && !strings.Contains(joined, "diag-close "+tID+".json") {
				t.Fatalf("held final descriptor not closed: %s", joined)
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
// the first fatal code.
func TestDiagnoseEarlierErrorWins(t *testing.T) {
	dir, ns := faultFixture(t)
	final, b := filepath.Join(ns, tID+".json"), filepath.Join(ns, pend(rndB))
	diagnose(t, dir, func(s, path string) error {
		switch {
		case s == "diag-open" && path == b:
			return syscall.ENOENT
		case s == "diag-close" && path == final:
			return syscall.EIO
		}
		return nil
	}).fails(t, "state_changed")
	// Unsafe descriptor plus its own close error: the safety code wins.
	dir, ns = faultFixture(t)
	b = filepath.Join(ns, pend(rndB))
	diagnose(t, dir, func(s, path string) error {
		switch {
		case s == "diag-open" && path == b:
			os.Remove(b)
			syscall.Mkfifo(b, 0o600)
		case s == "diag-close" && path == b:
			return syscall.EIO
		}
		return nil
	}).fails(t, "unsafe_state_path")
	// Unsafe namespace descriptor plus a scan close failure: the safety code wins.
	dir, ns = faultFixture(t)
	diagnose(t, dir, func(s, path string) error {
		switch {
		case s == "diag-scan-stat":
			os.Chmod(ns, 0o750)
		case s == "diag-close" && path == ns:
			return syscall.EIO
		}
		return nil
	}).fails(t, "state_permissions")
}

// TestDiagnoseIdentity covers C1 observation boundaries (a)-(e); (f) is in
// TestDiagnoseAbsentNamespace.
func TestDiagnoseIdentity(t *testing.T) {
	replace := func(t *testing.T, p string, data []byte, mode os.FileMode) {
		os.Remove(p)
		put(t, p, data)
		os.Chmod(p, mode)
	}
	cases := []struct {
		name   string
		target string // final or pendingB
		stage  string
		act    func(t *testing.T, p string)
		want   *want
		code   Code
	}{
		// (a) Replacement before the first Lstat is undetectable: later bytes count.
		{"a pre-Lstat safe replacement", "pendingB", "diag-lstat", func(t *testing.T, p string) { replace(t, p, escDup, 0o600) },
			&want{true, "valid", [6]int{2, 1, 1, 0, 0, 1}}, ""},
		// (b) A listed staging name gone at its Lstat.
		{"b listed name removed", "pendingB", "diag-lstat", func(t *testing.T, p string) { os.Remove(p) }, nil, "state_changed"},
		// (c) Safe different inode between Lstat and open.
		{"c pending replaced after Lstat", "pendingB", "diag-open", func(t *testing.T, p string) { replace(t, p, validData(), 0o600) }, nil, "state_changed"},
		{"c final replaced after Lstat", "final", "diag-open", func(t *testing.T, p string) { replace(t, p, validData(), 0o600) }, nil, "state_changed"},
		{"c final removed after Lstat", "final", "diag-open", func(t *testing.T, p string) { os.Remove(p) }, nil, "state_changed"},
		{"c pending symlink after Lstat", "pendingB", "diag-open", func(t *testing.T, p string) {
			os.Remove(p)
			os.Symlink(filepath.Join(filepath.Dir(p), tID+".json"), p)
		}, nil, "state_changed"},
		// (d) The observed descriptor's safety wins over the identity change.
		{"d fifo after Lstat", "pendingB", "diag-open", func(t *testing.T, p string) { os.Remove(p); syscall.Mkfifo(p, 0o600) }, nil, "unsafe_state_path"},
		{"d directory after Lstat", "final", "diag-open", func(t *testing.T, p string) { os.Remove(p); os.Mkdir(p, 0o700) }, nil, "unsafe_state_path"},
		{"d wrong mode new inode", "pendingB", "diag-open", func(t *testing.T, p string) { replace(t, p, validData(), 0o644) }, nil, "state_permissions"},
		{"d wrong mode same inode", "final", "diag-open", func(t *testing.T, p string) { os.Chmod(p, 0o640) }, nil, "state_permissions"},
		// (e) Same-inode content change between reads is not detected: the
		// final and its linked staging name are observed at different times.
		{"e same inode rewritten between reads", "pendingA", "diag-read", func(t *testing.T, p string) {
			os.WriteFile(p, partialData, 0o600)
		}, &want{true, "valid", [6]int{2, 1, 1, 0, 0, 1}}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir, ns := faultFixture(t)
			target := map[string]string{"final": filepath.Join(ns, tID+".json"), "pendingA": filepath.Join(ns, pend(rndA)),
				"pendingB": filepath.Join(ns, pend(rndB))}[c.target]
			done := false
			p := diagnose(t, dir, func(s, path string) error {
				if s == c.stage && path == target && !done {
					done = true
					c.act(t, path)
				}
				return nil
			})
			if !done {
				t.Fatal("boundary callback not reached")
			}
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
	r, _ := OpenRoot(dir)
	if _, err := r.Diagnose(tID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, Namespace)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("namespace created")
	}
}
