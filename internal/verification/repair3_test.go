package verification

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Cancellation after the exclusive ID mkdir and before the intent directory
// Sync has completed is verification_uncertain: no result, no output, no
// verifier start, the exact owned ID and every retained effect kept. Each row
// cancels at its boundary hook without injecting an error. The hand-written
// effects are: attempt listing; intent "" (no staging), "empty" (staging,
// zero bytes, one link), "staged" (full bytes, one link) or "final" (staging
// and intent.json, full bytes, two links each).
func TestVerifyCancelBeforeDurableIntentRetained(t *testing.T) {
	defer func() { hook = nil }()
	for _, c := range []struct{ stage, attempt, intent string }{
		{"id-chmod", "verifier", ""},
		{"ns-sync", "verifier", ""},
		{"verifier-home-sync", "verifier", ""},
		{"verifier-sync", "verifier", ""},
		{"id-close", "verifier", ""},
		{"intent-random", "verifier", ""},
		{"intent-create", "verifier", ""},
		{"intent-chmod", ".pending-intent-X,verifier", "empty"},
		{"intent-write", ".pending-intent-X,verifier", "empty"},
		{"intent-sync", ".pending-intent-X,verifier", "staged"},
		{"intent-close", ".pending-intent-X,verifier", "staged"},
		{"intent-link", ".pending-intent-X,verifier", "staged"},
		{"intent-dir-open", ".pending-intent-X,intent.json,verifier", "final"},
		{"intent-dir-recheck", ".pending-intent-X,intent.json,verifier", "final"},
		{"intent-dir-sync", ".pending-intent-X,intent.json,verifier", "final"},
	} {
		f := newFx(t, "sha1", "ok")
		ctx, cancel := context.WithCancel(context.Background())
		hits := 0
		hook = func(s string) error {
			if s == c.stage {
				hits++
				cancel()
			}
			return nil
		}
		out, passed, err := f.run(ctx, f.req(f.head, true)) // asserts every descriptor closed
		hook = nil
		cancel()
		if hits != 1 || !IsCode(err, CodeUncertain) || passed || out != "" || f.starts() != 0 {
			t.Errorf("%s: hits %d err %v passed %v out %q starts %d", c.stage, hits, err, passed, out, f.starts())
		}
		if got := listing(t, filepath.Join(f.state, Namespace)); got != testID {
			t.Errorf("%s: namespace %q", c.stage, got)
		}
		a := f.attempt()
		if got := listing(t, a); got != c.attempt {
			t.Errorf("%s: attempt listing %q want %q", c.stage, got, c.attempt)
		}
		dirs := []string{a}
		if c.attempt != "" {
			v := filepath.Join(a, "verifier")
			dirs = append(dirs, v, filepath.Join(v, "home"), filepath.Join(v, "tmp"))
			if got := listing(t, v); got != "home,tmp" {
				t.Errorf("%s: verifier listing %q", c.stage, got)
			}
		}
		for i, d := range dirs {
			fi, err := os.Lstat(d)
			if err != nil || !fi.IsDir() || fi.Mode()&fs.ModeType != fs.ModeDir || fi.Mode().Perm() != 0o700 ||
				fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				t.Errorf("%s: dir %s %v %v", c.stage, d, fi, err)
			}
			if i >= 2 && listing(t, d) != "" {
				t.Errorf("%s: %s not empty", c.stage, d)
			}
		}
		if _, err := os.Lstat(filepath.Join(a, "result.json")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: result.json present", c.stage)
		}
		var staging string
		es, _ := os.ReadDir(a)
		for _, e := range es {
			if strings.HasPrefix(e.Name(), ".pending-intent-") {
				staging = filepath.Join(a, e.Name())
			}
		}
		if (staging == "") != (c.intent == "") {
			t.Errorf("%s: staging %q want %q", c.stage, staging, c.intent)
			continue
		}
		if c.intent == "" {
			continue
		}
		want, links := f.wantIntent(f.head), uint64(1)
		if c.intent == "empty" {
			want = ""
		}
		paths := []string{staging}
		if c.intent == "final" {
			links = 2
			paths = append(paths, filepath.Join(a, "intent.json"))
		}
		var first fs.FileInfo
		for _, p := range paths {
			fi, err := os.Lstat(p)
			if err != nil {
				t.Errorf("%s: %s missing", c.stage, p)
				continue
			}
			st := fi.Sys().(*syscall.Stat_t)
			if !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 ||
				uint64(st.Nlink) != links || st.Uid != uint32(os.Getuid()) {
				t.Errorf("%s: %s mode %v links %d", c.stage, filepath.Base(p), fi.Mode(), st.Nlink)
			}
			if got := string(readFile(t, p)); got != want {
				t.Errorf("%s: %s bytes %q want %q", c.stage, filepath.Base(p), got, want)
			}
			if first == nil {
				first = fi
			} else if !os.SameFile(first, fi) {
				t.Errorf("%s: intent.json is not the staging inode", c.stage)
			}
		}
	}
}

// Post-durable controls: cancellation at the intent directory close (its hook
// runs after the actual close) is a recorded not_started result, and late
// cancellation at each result publication boundary does not fail publication
// of the already classified passed outcome.
func TestVerifyCancelAfterDurableIntentControls(t *testing.T) {
	defer func() { hook = nil }()
	for _, c := range []struct{ stage, want string }{
		{"intent-dir-close", wantResultJSON(OutcomeUnverified, "not_started", "null", "sha1", "H", "null")},
		{"result-random", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
		{"result-create", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
		{"result-write", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
		{"result-sync", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
		{"result-link", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
		{"result-dir-open", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
		{"result-dir-sync", wantResultJSON(OutcomePassed, "exited", "0", "sha1", "H", "H")},
	} {
		f := newFx(t, "sha1", "ok")
		want := strings.ReplaceAll(c.want, `"H"`, `"`+f.head+`"`)
		ctx, cancel := context.WithCancel(context.Background())
		hits := 0
		hook = func(s string) error {
			if s == c.stage {
				hits++
				cancel()
			}
			return nil
		}
		out, passed, err := f.run(ctx, f.req(f.head, true))
		hook = nil
		cancel()
		wantStarts, wantPassed := 1, true
		if c.stage == "intent-dir-close" {
			wantStarts, wantPassed = 0, false
		}
		if hits != 1 || err != nil || out != want || passed != wantPassed || f.starts() != wantStarts {
			t.Errorf("%s: hits %d err %v passed %v starts %d out %s", c.stage, hits, err, passed, f.starts(), out)
		}
		a := f.attempt()
		if got := listing(t, a); got != ".pending-intent-X,.pending-result-X,intent.json,result.json,verifier" {
			t.Errorf("%s: listing %q", c.stage, got)
		}
		if got := string(readFile(t, filepath.Join(a, "result.json"))); got != want {
			t.Errorf("%s: saved result %s", c.stage, got)
		}
		if got := string(readFile(t, filepath.Join(a, "intent.json"))); got != f.wantIntent(f.head) {
			t.Errorf("%s: saved intent %s", c.stage, got)
		}
	}
}

// Both validators require the matching run_id to be a valid run ID itself,
// not only equal to the caller's id.
func TestVerifyValidatorsRejectInvalidMatchingRunID(t *testing.T) {
	h := strings.Repeat("a", 40)
	g := &fx{t: t, format: "sha1", head: h, plan: filepath.Join(t.TempDir(), "p")}
	write0600(t, g.plan, []byte("x"))
	intent := g.wantIntent(h)
	result := wantResultJSON(OutcomePassed, "exited", "0", "sha1", h, h)
	with := func(s, id string) []byte {
		return []byte(strings.Replace(s, `"run_id":"`+testID+`"`, `"run_id":"`+id+`"`, 1))
	}
	for _, id := range []string{"", "bad", strings.ToUpper("0123456789abcdef0123456789abcdef"), strings.Repeat("a", 31),
		strings.Repeat("a", 33), "0123456789abcdef0123456789abcdeg", " " + strings.Repeat("a", 31)} {
		if !strings.Contains(string(with(intent, id)), `"run_id":"`+id+`"`) {
			t.Fatalf("%q: substitution failed", id)
		}
		if ValidIntent(with(intent, id), id) {
			t.Errorf("ValidIntent accepted matching run_id %q", id)
		}
		if ValidResult(with(result, id), id) {
			t.Errorf("ValidResult accepted matching run_id %q", id)
		}
	}
	other := strings.Repeat("0", 32)
	if !ValidIntent([]byte(intent), testID) || !ValidResult([]byte(result), testID) ||
		!ValidIntent(with(intent, other), other) || !ValidResult(with(result, other), other) {
		t.Error("valid matching run_id rejected")
	}
	if ValidIntent(with(intent, other), testID) || ValidResult(with(result, other), testID) ||
		ValidIntent([]byte(intent), other) || ValidResult([]byte(result), other) {
		t.Error("valid but different run_id accepted")
	}
}

// The safely Lstatted plan is moved to a retained private name and replaced
// by an unsafe file or directory before the open. The opened descriptor's
// safety code wins over its distinct identity (state_changed), also with a
// simultaneous close failure; the read and parse are never reached, the
// descriptor is closed and the original inode is retained unchanged.
//
// The setuid row cannot rely on a chmod in every environment, so the original
// plan stays in place and the reader-open seam opens a different, genuine
// setuid inode with the reader's exact flags: the supplied read-only
// BAW_TEST_DIAG_SPECIAL_FIXTURES file when set (validated, only opened, its
// complete tree unchanged), otherwise a file created here whose actual setuid
// bit is observed. A missing or invalid fixture or bit fails, never skips.
func TestVerifyPlanUnsafeReplacementBeforeOpen(t *testing.T) {
	defer func() { readHook, openFile = nil, nil }()
	for _, c := range []struct {
		name, want string
		replace    func(t *testing.T, p string)
	}{
		{"mode-0644", "state_permissions", func(t *testing.T, p string) {
			write0600(t, p, []byte(`{}`))
			os.Chmod(p, 0o644)
		}},
		{"mode-0400", "state_permissions", func(t *testing.T, p string) {
			write0600(t, p, []byte(`{}`))
			os.Chmod(p, 0o400)
		}},
		{"directory", "unsafe_state_path", func(t *testing.T, p string) { mkdir0700(t, p) }},
	} {
		for _, closeFault := range []bool{false, true} {
			name := c.name
			if closeFault {
				name += "+close"
			}
			f := newFx(t, "sha1", "ok")
			original := readFile(t, f.plan)
			origFi, err := os.Lstat(f.plan)
			if err != nil {
				t.Fatal(err)
			}
			retained := filepath.Join(f.base, "retained-original.json")
			var hits []string
			var replFi fs.FileInfo
			readHook = func(s string) error {
				hits = append(hits, s)
				switch {
				case s == "plan-open":
					if err := os.Rename(f.plan, retained); err != nil {
						t.Fatal(err)
					}
					c.replace(t, f.plan)
					replFi, _ = os.Lstat(f.plan)
				case s == "plan-close" && closeFault:
					return errors.New("close fault")
				}
				return nil
			}
			f.assertPre(t, name, context.Background(), f.req(f.head, true), c.want) // asserts descriptors closed
			readHook = nil
			if got := strings.Join(hits, ","); got != "plan-open,plan-close" {
				t.Errorf("%s: hits %s", name, got)
			}
			// The replacement is really unsafe as observed, not as intended.
			if replFi == nil || fileSafety(replFi) == nil || fileSafety(replFi).Error() != c.want {
				t.Fatalf("%s: replacement metadata %v", name, replFi)
			}
			retFi, err := os.Lstat(retained)
			if err != nil || !os.SameFile(origFi, retFi) || os.SameFile(origFi, replFi) || os.SameFile(retFi, replFi) ||
				!retFi.Mode().IsRegular() || retFi.Mode().Perm() != 0o600 || string(readFile(t, retained)) != string(original) {
				t.Errorf("%s: identities original/retained/replacement not distinct as required (%v)", name, err)
			}
		}
	}

	const env = "BAW_TEST_DIAG_SPECIAL_FIXTURES"
	var setuid, fixtureRoot, fixtureBefore string
	if supplied := os.Getenv(env); supplied != "" {
		fixtureRoot = supplied
		fixtureBefore = fixtureTree(t, fixtureRoot)
		setuid = suppliedSpecial(t, env, fixtureRoot, fs.ModeSetuid)
	} else {
		setuid = filepath.Join(resolvedTemp(t), "setuid-plan.json")
		write0600(t, setuid, []byte(`{}`))
		if err := os.Chmod(setuid, 0o600|fs.ModeSetuid); err != nil {
			t.Fatal(err)
		}
	}
	setFi, err := os.Lstat(setuid)
	if err != nil || !setFi.Mode().IsRegular() || setFi.Mode().Perm() != 0o600 ||
		setFi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != fs.ModeSetuid ||
		setFi.Sys().(*syscall.Stat_t).Uid != uint32(os.Getuid()) {
		t.Fatalf("setuid file %s (supplied=%v) actual metadata %v: %v", setuid, fixtureRoot != "", setFi, err)
	}
	t.Logf("opened-setuid: supplied=%v observed mode %v", fixtureRoot != "", setFi.Mode())
	wantFlag := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	for _, closeFault := range []bool{false, true} {
		name := "opened-setuid"
		if closeFault {
			name += "+close"
		}
		f := newFx(t, "sha1", "ok")
		original := readFile(t, f.plan)
		origFi, err := os.Lstat(f.plan)
		if err != nil {
			t.Fatal(err)
		}
		var hits []string
		var fd *os.File
		var fdFi fs.FileInfo
		opens := 0
		readHook = func(s string) error {
			hits = append(hits, s)
			if s == "plan-close" && closeFault {
				return errors.New("close fault")
			}
			return nil
		}
		openFile = func(path string, flag int) (*os.File, error) {
			opens++
			if pfi, err := os.Lstat(path); err != nil || !os.SameFile(pfi, origFi) || flag != wantFlag {
				t.Errorf("%s: open of %s flag %#x is not the Lstatted plan with the reader flags (%v)", name, path, flag, err)
			}
			var err error
			if fd, err = os.OpenFile(setuid, flag, 0); err != nil {
				return nil, err
			}
			fdFi, err = fd.Stat()
			return fd, err
		}
		f.assertPre(t, name, context.Background(), f.req(f.head, true), "state_permissions") // asserts descriptors closed
		readHook, openFile = nil, nil
		if got := strings.Join(hits, ","); got != "plan-open,plan-close" || opens != 1 {
			t.Errorf("%s: hits %s opens %d", name, got, opens)
		}
		if fd == nil || fdFi == nil {
			t.Fatalf("%s: setuid inode not opened", name)
		}
		if _, err := fd.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("%s: opened setuid descriptor still open (%v)", name, err)
		}
		// The opened inode is the genuine setuid file, unsafe as observed
		// through the descriptor, and distinct from the original.
		if fileSafety(fdFi) == nil || fileSafety(fdFi).Error() != "state_permissions" || fdFi.Mode()&fs.ModeSetuid == 0 ||
			!os.SameFile(fdFi, setFi) || os.SameFile(fdFi, origFi) {
			t.Errorf("%s: opened metadata %v not the distinct unsafe setuid inode", name, fdFi.Mode())
		}
		after, err := os.Lstat(f.plan)
		if err != nil || !os.SameFile(origFi, after) || after.Mode() != origFi.Mode() || string(readFile(t, f.plan)) != string(original) {
			t.Errorf("%s: original plan inode, mode or bytes changed (%v)", name, err)
		}
	}
	if fi, err := os.Lstat(setuid); err != nil || fi.Mode() != setFi.Mode() || !os.SameFile(fi, setFi) {
		t.Errorf("setuid file changed %v -> %v (%v)", setFi.Mode(), fi, err)
	}
	if fixtureRoot != "" {
		if after := fixtureTree(t, fixtureRoot); after != fixtureBefore {
			t.Errorf("supplied fixture changed\nbefore:\n%s\nafter:\n%s", fixtureBefore, after)
		}
	}
}

// Error selection on the actual reader with distinguishable codes and a
// direct parse callback: an earlier safety, read, size or parse failure wins
// over a simultaneous close failure; a close-only failure uses its own code.
// Hits, parse calls and the actually closed descriptor are checked.
func TestVerifySafeReadErrorSelection(t *testing.T) {
	defer func() { readHook = nil; opened = nil }()
	const (
		missing, readFail, closeFail, large Code = "t_missing", "t_read", "t_close", "t_large"
	)
	parseErr := errors.New("parse sentinel")
	for _, c := range []struct {
		name, data string
		fail       map[string]bool
		parseFails bool
		want       string // code, or "parse" for the sentinel, or "" for success
		hits       string
		parses     int
		limit      int
		mode       fs.FileMode
	}{
		{"read+close", "abc", map[string]bool{"plan-read": true, "plan-close": true}, false, "t_read", "plan-open,plan-read,plan-close", 0, 8, 0o600},
		{"read-only", "abc", map[string]bool{"plan-read": true}, false, "t_read", "plan-open,plan-read,plan-close", 0, 8, 0o600},
		{"close-only", "abc", map[string]bool{"plan-close": true}, false, "t_close", "plan-open,plan-read,plan-close", 1, 8, 0o600},
		{"parse+close", "abc", map[string]bool{"plan-close": true}, true, "parse", "plan-open,plan-read,plan-close", 1, 8, 0o600},
		{"parse-only", "abc", nil, true, "parse", "plan-open,plan-read,plan-close", 1, 8, 0o600},
		{"large+close", "abcdefghi", map[string]bool{"plan-close": true}, false, "t_large", "plan-open,plan-read,plan-close", 0, 8, 0o600},
		{"limit-ok", "abcdefgh", nil, false, "", "plan-open,plan-read,plan-close", 1, 8, 0o600},
		{"open", "abc", map[string]bool{"plan-open": true}, false, "t_read", "plan-open", 0, 8, 0o600},
		{"lstat-unsafe", "abc", map[string]bool{"plan-close": true}, false, "state_permissions", "", 0, 8, 0o640},
	} {
		p := filepath.Join(resolvedTemp(t), "plan.json")
		write0600(t, p, []byte(c.data))
		os.Chmod(p, c.mode)
		var hits []string
		readHook = func(s string) error {
			hits = append(hits, s)
			if c.fail[s] {
				return errors.New(c.name + " " + s)
			}
			return nil
		}
		d := &descriptors{}
		opened = d.add
		parses := 0
		var parsed string
		data, err := safeRead(p, "plan", c.limit, missing, readFail, closeFail, large, func(b []byte) error {
			parses++
			parsed = string(b)
			if c.parseFails {
				return parseErr
			}
			return nil
		})
		opened, readHook = nil, nil
		d.assertClosed(t, c.name)
		// Exactly one descriptor whenever the open happened (close was reached).
		if wantFiles := strings.Count(c.hits, "plan-close"); len(d.files) != wantFiles {
			t.Errorf("%s: %d descriptors observed", c.name, len(d.files))
		}
		switch c.want {
		case "":
			if err != nil || string(data) != c.data {
				t.Errorf("%s: %v %q", c.name, err, data)
			}
		case "parse":
			if err != parseErr || data != nil {
				t.Errorf("%s: got %v want parse sentinel", c.name, err)
			}
		default:
			if err == nil || err.Error() != c.want || data != nil {
				t.Errorf("%s: got %v want %s", c.name, err, c.want)
			}
		}
		if got := strings.Join(hits, ","); got != c.hits || parses != c.parses || (parses == 1 && parsed != c.data) {
			t.Errorf("%s: hits %q parses %d parsed %q", c.name, got, parses, parsed)
		}
	}
	// An initially absent file uses the missing code.
	absent := filepath.Join(resolvedTemp(t), "absent.json")
	if _, err := safeRead(absent, "plan", 8, missing, readFail, closeFail, large,
		func([]byte) error { return nil }); err == nil || err.Error() != "t_missing" {
		t.Errorf("missing: %v", err)
	}
}
