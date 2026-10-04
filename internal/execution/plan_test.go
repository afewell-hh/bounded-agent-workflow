package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unicode/utf8"
)

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

const okCmd = `{"executable":"/bin/x","arguments":[],"timeout_seconds":1}`

func planWith(worker string) string {
	return `{"schema_version":1,"worker":` + worker + `,"verification":` + okCmd + `}`
}

func TestParsePlanStrict(t *testing.T) {
	valid := []string{
		planWith(okCmd),
		planWith(`{"executable":"/bin/x","arguments":["` + strings.Repeat("a", 1024) + `"],"timeout_seconds":300}`),
		planWith(`{"executable":"/bin/x","arguments":[` + strings.TrimSuffix(strings.Repeat(`"",`, 64), ",") + `],"timeout_seconds":1}`),
		" \n" + planWith(okCmd) + "\n",
	}
	for _, s := range valid {
		if _, err := ParsePlan([]byte(s)); err != nil {
			t.Errorf("valid rejected: %.80s: %v", s, err)
		}
	}
	invalid := []string{
		"", "{", "[]", "null", planWith(okCmd) + "{}", planWith(okCmd) + " 1",
		`{"schema_version":1.0,"worker":` + okCmd + `,"verification":` + okCmd + `}`,
		`{"schema_version":1e0,"worker":` + okCmd + `,"verification":` + okCmd + `}`,
		`{"schema_version":2,"worker":` + okCmd + `,"verification":` + okCmd + `}`,
		`{"schema_version":"1","worker":` + okCmd + `,"verification":` + okCmd + `}`,
		`{"schema_version":1,"worker":` + okCmd + `}`,
		`{"schema_version":1,"worker":` + okCmd + `,"verification":` + okCmd + `,"extra":1}`,
		`{"schema_version":1,"schema_version":1,"worker":` + okCmd + `,"verification":` + okCmd + `}`,
		planWith(`{"executable":"/bin/x","executable":"/bin/y","arguments":[],"timeout_seconds":1}`),
		planWith(`{"executable":"bin/x","arguments":[],"timeout_seconds":1}`),
		planWith(`{"executable":"","arguments":[],"timeout_seconds":1}`),
		planWith(`{"executable":"/bin/x\u0000","arguments":[],"timeout_seconds":1}`),
		planWith(`{"executable":"/bin/x","arguments":["a\u0000"],"timeout_seconds":1}`),
		planWith(`{"executable":"/bin/x","arguments":["` + strings.Repeat("a", 1025) + `"],"timeout_seconds":1}`),
		planWith(`{"executable":"/bin/x","arguments":[` + strings.TrimSuffix(strings.Repeat(`"",`, 65), ",") + `],"timeout_seconds":1}`),
		planWith(`{"executable":"/bin/x","arguments":[1],"timeout_seconds":1}`),
		planWith(`{"executable":"/bin/x","arguments":[],"timeout_seconds":0}`),
		planWith(`{"executable":"/bin/x","arguments":[],"timeout_seconds":301}`),
		planWith(`{"executable":"/bin/x","arguments":[],"timeout_seconds":01}`),
		planWith(`{"executable":"/bin/x","arguments":[],"timeout_seconds":-1}`),
		planWith(`{"executable":"/bin/x","arguments":[],"timeout_seconds":1.0}`),
		planWith(`{"executable":"/bin/x","arguments":[],"timeout_seconds":"1"}`),
		planWith(`{"executable":"/bin/x","arguments":[]}`),
		"{\"schema_version\":1,\"worker\":" + okCmd + ",\"verification\":" + okCmd + ",\"\xff\":1}",
	}
	for _, s := range invalid {
		if _, err := ParsePlan([]byte(s)); !codeIs(err, "invalid_execution_plan") {
			t.Errorf("invalid accepted: %q: %v", s, err)
		}
	}
	// Exact byte cap: 65,536 accepted, 65,537 rejected.
	base := planWith(okCmd)
	at := base + strings.Repeat(" ", MaxPlanBytes-len(base))
	if _, err := ParsePlan([]byte(at)); err != nil {
		t.Fatalf("at cap: %v", err)
	}
	if _, err := ParsePlan([]byte(at + " ")); !codeIs(err, "invalid_execution_plan") {
		t.Fatalf("over cap: %v", err)
	}
}

// Duplicate member names are compared after JSON decoding: a name spelled
// with genuine \u escapes duplicates its literal spelling, at the top level
// and inside both commands. Each fixture is checked to contain the literal
// escape bytes and the literal name only once, the escaped spelling alone is
// independently accepted as that known name, and only the pair is refused.
func TestParsePlanEscapedDuplicates(t *testing.T) {
	top := func(keys ...string) string {
		s := "{"
		for _, k := range keys {
			s += `"` + k + `":1,`
		}
		return s + `"worker":` + okCmd + `,"verification":` + okCmd + `}`
	}
	cmd := func(keys ...string) string {
		s := "{"
		for _, k := range keys {
			s += `"` + k + `":"/bin/x",`
		}
		return s + `"arguments":[],"timeout_seconds":1}`
	}
	worker := func(keys ...string) string { return planWith(cmd(keys...)) }
	verification := func(keys ...string) string {
		return `{"schema_version":1,"worker":` + okCmd + `,"verification":` + cmd(keys...) + `}`
	}
	// The JSON escape introducer is a backslash (0x5c) followed by "u".
	esc := string(rune(0x5c)) + "u"
	cases := []struct {
		name, escaped string
		build         func(...string) string
	}{
		{"schema_version", esc + "0073chema_version", top},
		{"schema_version", "schema" + esc + "005fversion", top},
		{"executable", esc + "0065xecutable", worker},
		{"executable", "executabl" + esc + "0065", verification},
	}
	for _, c := range cases {
		if !strings.Contains(c.escaped, esc) || strings.Contains(c.escaped, c.name) {
			t.Fatalf("fixture %s is not an escaped spelling", c.escaped)
		}
		var decoded string
		if err := json.Unmarshal([]byte(`"`+c.escaped+`"`), &decoded); err != nil || decoded != c.name {
			t.Fatalf("%s decodes to %q, want %q", c.escaped, decoded, c.name)
		}
		alone := c.build(c.escaped)
		if _, err := ParsePlan([]byte(alone)); err != nil {
			t.Errorf("escaped %s alone rejected: %v", c.escaped, err)
		}
		// Each duplicate adds exactly one literal spelling to the object that
		// already holds the escaped one (the other command keeps its own).
		lit := `"` + c.name + `"`
		for _, dup := range []string{c.build(c.name, c.escaped), c.build(c.escaped, c.name)} {
			if !strings.Contains(dup, esc) || strings.Count(dup, lit) != strings.Count(alone, lit)+1 {
				t.Fatalf("fixture %s lacks one literal and one escaped name", dup)
			}
			if _, err := ParsePlan([]byte(dup)); !codeIs(err, "invalid_execution_plan") {
				t.Errorf("escaped duplicate accepted: %s: %v", dup, err)
			}
		}
	}
}

// Plan file safety probes. Each unsafe read runs in a child process that is
// always waited for and bounded by 10 seconds, so a FIFO can never hang the
// suite or leave a blocked reader behind.
func TestReadPlanFileSafety(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(planWith(okCmd)), 0o600)
	os.Chmod(good, 0o600)
	if got := childRead(t, good); got != "ok" {
		t.Fatalf("good plan in child: %s", got)
	}
	link := filepath.Join(dir, "link.json")
	os.Symlink(good, link)
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	loose := filepath.Join(dir, "loose.json")
	os.WriteFile(loose, []byte(planWith(okCmd)), 0o644)
	os.Chmod(loose, 0o644)
	big := filepath.Join(dir, "big.json")
	os.WriteFile(big, []byte(planWith(okCmd)+strings.Repeat(" ", MaxPlanBytes)), 0o600)
	os.Chmod(big, 0o600)
	cases := map[string]string{
		filepath.Join(dir, "missing.json"): "plan_unavailable",
		filepath.Join(dir, "nodir", "p"):   "plan_unavailable",
		link:                               "unsafe_state_path",
		fifo:                               "unsafe_state_path",
		dir:                                "unsafe_state_path",
		loose:                              "state_permissions",
		big:                                "invalid_execution_plan",
	}
	// Socket, setuid and setgid cases are mandatory: either the registered
	// read-only fixtures or ones created here; none is ever silently omitted.
	// Special bits are independently observed, never assumed from chmod.
	sock, sockPreserved := socketPlan(t, dir)
	cases[sock] = "unsafe_state_path"
	setuid, suidPreserved := setuidPlan(t, dir)
	cases[setuid] = "state_permissions"
	setgid, sgidPreserved := setgidPlan(t, dir)
	cases[setgid] = "state_permissions"
	for p, code := range cases {
		if got := childRead(t, p); got != code {
			t.Errorf("%s: %s want %s", filepath.Base(p), got, code)
		}
	}
	sockPreserved()
	suidPreserved()
	sgidPreserved()
}

// readFaults runs ReadPlan in-process with the given stages failing at the
// plan seam, swap (if any) run at the open boundary, and proves every
// descriptor it opened is actually closed (Stat on the retained *os.File
// fails with os.ErrClosed). It returns the code and the stages reached.
// Only regular files and directories are opened in-process; O_NONBLOCK and
// the safety checks mean none of these opens or reads can block.
func readFaults(t *testing.T, path string, swap func(), faults ...string) (string, []string) {
	t.Helper()
	var stages []string
	planHook = func(s string) error {
		stages = append(stages, s)
		if s == "open" && swap != nil {
			swap()
		}
		for _, f := range faults {
			if f == s {
				return errors.New("injected " + s)
			}
		}
		return nil
	}
	defer func() { planHook = nil }()
	var err error
	if n := track(t, func() { _, _, err = ReadPlan(path) }); n != 1 {
		t.Fatalf("%s: opened %d descriptors, want 1", filepath.Base(path), n)
	}
	var e *Error
	if err == nil {
		return "ok", stages
	}
	if !errors.As(err, &e) {
		t.Fatalf("untyped error %v", err)
	}
	return string(e.Code), stages
}

func writePlan(t *testing.T, p, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

// C2 §8 precedence on an opened plan: safety, then identity, then the read,
// then the parser; a close error only when all of them succeeded. The
// descriptor is always closed. Parser errors (malformed JSON, invalid UTF-8,
// unsupported schema, oversize) keep invalid_execution_plan when close also
// fails; an injected non-EOF read failure on bytes that would not parse is
// plan_unavailable, proving the parser never ran after it.
func TestReadPlanErrorPrecedence(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	files := map[string]string{
		"good":      planWith(okCmd),
		"malformed": "{",
		"utf8":      "{\"schema_version\":1,\"worker\":" + okCmd + ",\"verification\":" + okCmd + ",\"\xff\":1}",
		"schema2":   `{"schema_version":2,"worker":` + okCmd + `,"verification":` + okCmd + `}`,
		"big":       planWith(okCmd) + strings.Repeat(" ", MaxPlanBytes),
	}
	for name, body := range files {
		writePlan(t, filepath.Join(dir, name), body, 0o600)
	}
	if utf8.ValidString(files["utf8"]) {
		t.Fatal("utf8 fixture is valid UTF-8")
	}
	all := []string{"open", "read", "close"}
	cases := []struct {
		file   string
		faults []string
		want   string
	}{
		{"good", nil, "ok"},
		{"good", []string{"close"}, "plan_unavailable"},
		{"good", []string{"read"}, "plan_unavailable"},
		{"malformed", nil, "invalid_execution_plan"},
		{"malformed", []string{"close"}, "invalid_execution_plan"},
		{"utf8", nil, "invalid_execution_plan"},
		{"utf8", []string{"close"}, "invalid_execution_plan"},
		{"schema2", nil, "invalid_execution_plan"},
		{"schema2", []string{"close"}, "invalid_execution_plan"},
		{"big", []string{"close"}, "invalid_execution_plan"},
		{"malformed", []string{"read"}, "plan_unavailable"},
		{"malformed", []string{"read", "close"}, "plan_unavailable"},
	}
	for _, c := range cases {
		got, stages := readFaults(t, filepath.Join(dir, c.file), nil, c.faults...)
		if got != c.want {
			t.Errorf("%s faults=%v: %s want %s", c.file, c.faults, got, c.want)
		}
		// The read and close boundaries are always reached: the close happens
		// whatever failed first.
		if strings.Join(stages, ",") != strings.Join(all, ",") {
			t.Errorf("%s faults=%v: stages %v", c.file, c.faults, stages)
		}
	}
	// A failed open boundary never opens a descriptor.
	planHook = func(s string) error { return errors.New("injected " + s) }
	n := track(t, func() {
		if _, _, err := ReadPlan(filepath.Join(dir, "good")); !codeIs(err, "plan_unavailable") {
			t.Errorf("open fault: %v", err)
		}
	})
	planHook = nil
	if n != 0 {
		t.Fatalf("open fault opened %d", n)
	}
}

// Opened-file safety precedes identity. Between the safe Lstat and the open
// the plan is renamed aside (so its inode stays allocated and cannot be
// reused) and replaced by a file whose device/inode independently differs.
// A wrong-mode or non-regular replacement reports its safety code, a safe
// replacement reports state_changed; a close fault never overrides either.
func TestReadPlanOpenedSafetyBeforeIdentity(t *testing.T) {
	cases := []struct {
		name    string
		replace func(t *testing.T, p string)
		want    string
	}{
		{"wrong-mode", func(t *testing.T, p string) { writePlan(t, p, planWith(okCmd), 0o644) }, "state_permissions"},
		{"directory", func(t *testing.T, p string) {
			if err := os.Mkdir(p, 0o700); err != nil {
				t.Fatal(err)
			}
		}, "unsafe_state_path"},
		{"safe-distinct", func(t *testing.T, p string) { writePlan(t, p, planWith(okCmd), 0o600) }, "state_changed"},
	}
	for _, c := range cases {
		for _, faults := range [][]string{nil, {"close"}} {
			dir, _ := filepath.EvalSymlinks(t.TempDir())
			p, aside := filepath.Join(dir, "plan.json"), filepath.Join(dir, "aside.json")
			writePlan(t, p, planWith(okCmd), 0o600)
			orig, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			var repl os.FileInfo
			swap := func() {
				if err := os.Rename(p, aside); err != nil {
					t.Fatal(err)
				}
				c.replace(t, p)
				repl, err = os.Lstat(p)
				if err != nil {
					t.Fatal(err)
				}
			}
			got, _ := readFaults(t, p, swap, faults...)
			if repl == nil {
				t.Fatalf("%s: replacement never installed", c.name)
			}
			// Independently established facts: the original still exists with
			// its identity, and the replacement differs from it.
			kept, err := os.Lstat(aside)
			if err != nil || !os.SameFile(kept, orig) {
				t.Fatalf("%s: original not retained aside: %v", c.name, err)
			}
			o, r := orig.Sys().(*syscall.Stat_t), repl.Sys().(*syscall.Stat_t)
			if os.SameFile(repl, orig) || (o.Dev == r.Dev && o.Ino == r.Ino) {
				t.Fatalf("%s: replacement shares the original identity", c.name)
			}
			if c.name == "wrong-mode" && (!repl.Mode().IsRegular() || repl.Mode().Perm() != 0o644) {
				t.Fatalf("wrong-mode replacement observed as %v", repl.Mode())
			}
			if got != c.want {
				t.Errorf("%s faults=%v: %s want %s", c.name, faults, got, c.want)
			}
		}
	}
}

// The plan is read through a symlinked ancestor; the attempt then runs.
func TestPlanAncestorAlias(t *testing.T) {
	f := newFx(t, "sha1")
	link := filepath.Join(f.base, "alias")
	os.Symlink(f.base, link)
	r := f.req(true)
	r.Plan = filepath.Join(link, "plan.json")
	r.StateDir = filepath.Join(link, "state")
	if ok, err := Execute(context.Background(), r, &failWriter{n: 1 << 20}); err != nil || !ok {
		t.Fatal(err)
	}
}
