package execution

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
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
	setgid := filepath.Join(dir, "setgid.json")
	os.WriteFile(setgid, []byte(planWith(okCmd)), 0o600)
	os.Chmod(setgid, 0o600|os.ModeSetgid)
	// The special bit is independently observed, never assumed from chmod.
	if fi, err := os.Lstat(setgid); err != nil || fi.Mode()&os.ModeSetgid == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("setgid plan fixture not observed: %v", err)
	}
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
		setgid:                             "state_permissions",
		big:                                "invalid_execution_plan",
	}
	// Socket and setuid cases are mandatory: either the registered read-only
	// fixtures or ones created here; neither is ever silently omitted.
	sock, sockPreserved := socketPlan(t, dir)
	cases[sock] = "unsafe_state_path"
	setuid, suidPreserved := setuidPlan(t, dir)
	cases[setuid] = "state_permissions"
	for p, code := range cases {
		if got := childRead(t, p); got != code {
			t.Errorf("%s: %s want %s", filepath.Base(p), got, code)
		}
	}
	sockPreserved()
	suidPreserved()
	// Close error precedence: alone it is plan_unavailable, after an earlier
	// failure the earlier code is kept. These regular files cannot block, so
	// they run in-process where the seam is visible.
	planHook = func(string) error { return errors.New("close") }
	defer func() { planHook = nil }()
	if _, _, err := ReadPlan(good); !codeIs(err, "plan_unavailable") {
		t.Fatalf("close alone: %v", err)
	}
	if _, _, err := ReadPlan(big); !codeIs(err, "invalid_execution_plan") {
		t.Fatalf("close after earlier failure: %v", err)
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
