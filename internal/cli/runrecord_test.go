package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

const (
	rrID     = "00112233445566778899aabbccddeeff"
	rrID2    = "ffeeddccbbaa99887766554433221100"
	rrTicket = "https://github.com/afewell-hh/bounded-agent-workflow/issues/9"
	rrScope  = "64aAbBcCdDeEfF0123456789abcdef0123456789ABCDEF0123456789abcdef01"
)

// nineFields is the independently hand-listed record key set.
var nineFields = []string{"schema_version", "run_id", "record_state", "ticket_url", "scope_sha256",
	"policy_commit", "repository_object_format", "repository_head", "created_at"}

var createdRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

// stateRoot returns a new private root holding unrelated pre-existing entries.
func stateRoot(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0o700)
	mustWrite(t, filepath.Join(dir, "run.json"), "manual receipt\n")
	mustWrite(t, filepath.Join(dir, "runs", "old", "evidence.txt"), "kept\n")
	return dir
}

// tree snapshots every path, mode and file content under dir.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		fi, err := os.Lstat(p)
		if err != nil {
			return err
		}
		line := strings.TrimPrefix(p, dir) + " " + fi.Mode().String()
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

// decodeRecord checks a create/status JSON packet against the hand-written
// oracle and returns its record members.
func decodeRecord(t *testing.T, r result, op string) map[string]any {
	t.Helper()
	if r.code != 0 || r.stderr != "" || !strings.HasSuffix(r.stdout, "}\n") || strings.Count(r.stdout, "\n") != 1 {
		t.Fatalf("exit %d stdout %q stderr %q", r.code, r.stdout, r.stderr)
	}
	var top map[string]any
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	dec.UseNumber()
	if err := dec.Decode(&top); err != nil {
		t.Fatal(err)
	}
	wantTop := map[string]any{"schema_version": json.Number("1"), "operation": op, "authority": "not_evaluated",
		"remote_freshness": "unknown", "runtime_state": "unknown", "process_ownership": "unknown", "reservation_ownership": "unknown"}
	if len(top) != len(wantTop)+1 {
		t.Fatalf("top-level keys %v", top)
	}
	for k, v := range wantTop {
		if top[k] != v {
			t.Fatalf("%s = %#v", k, top[k])
		}
	}
	rec, ok := top["record"].(map[string]any)
	if !ok || len(rec) != len(nineFields) {
		t.Fatalf("record %#v", top["record"])
	}
	for _, k := range nineFields {
		if _, ok := rec[k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
	if rec["schema_version"] != json.Number("1") {
		t.Fatalf("record schema_version %#v", rec["schema_version"])
	}
	for _, k := range nineFields[1:] {
		if _, ok := rec[k].(string); !ok {
			t.Fatalf("%s not a string: %#v", k, rec[k])
		}
	}
	return rec
}

func textReport(op, id, ticket, scope, policy, head, format, created string) string {
	return "BAW run record\nOperation: " + op + "\nRun: " + id + "\nRecord state: recorded\nTicket: " + ticket +
		"\nScope SHA-256: " + scope + "\nPolicy reference: " + policy + "\nRecorded HEAD: " + head +
		"\nObject format: " + format + "\nCreated at: " + created +
		"\nAuthority: not_evaluated\nRemote freshness: unknown\nRuntime state: unknown\nProcess ownership: unknown\nReservation ownership: unknown\n"
}

func createArgs(root, id, repo, policy string, extra ...string) []string {
	return append([]string{"run", "create", "--state-dir", root, "--run-id", id, "--repo", repo,
		"--ticket", rrTicket, "--scope-sha256", rrScope, "--policy-commit", policy}, extra...)
}

// TestRunRecordBothFormats is the mandatory SHA-1 and SHA-256 journey; an
// unavailable SHA-256 fixture fails rather than skips.
func TestRunRecordBothFormats(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			home := env(t)
			repo := filepath.Join(t.TempDir(), "repo")
			if err := tf.Init(home, repo, format); err != nil {
				t.Fatalf("%s fixture unavailable (mandatory gate): %v", format, err)
			}
			head, err := tf.CommitSources(home, repo)
			if err != nil {
				t.Fatal(err)
			}
			width, other := 40, 64
			if format == "sha256" {
				width, other = 64, 40
			}
			if len(head) != width {
				t.Fatalf("fixture head width %d", len(head))
			}
			// Dirty, secret-bearing worktree is accepted and never copied.
			mustWrite(t, filepath.Join(repo, tf.SecretFile), tf.SecretContent+"\n")
			git(t, home, repo, "checkout", "-q", "-b", tf.SecretBranch)
			repoBefore := tree(t, repo)
			root := stateRoot(t)
			rootBefore := tree(t, root)
			// A well-formed policy OID that names no object stays a reference.
			policyUpper := strings.Repeat("AbCdEf0123", 7)[:width]
			policy := strings.ToLower(policyUpper)

			// Early failures leave a root without namespace unchanged.
			wantFail(t, runCLI(t, nil, createArgs(root, rrID, repo, strings.Repeat("a", other))...), 2, "invalid_usage")
			unborn := mustInit(t, home, filepath.Join(t.TempDir(), "unborn"), format)
			wantFail(t, runCLI(t, nil, createArgs(root, rrID, unborn, policy)...), 1, "repository_unborn")
			wantFail(t, runCLI(t, nil, createArgs(root, rrID, t.TempDir(), policy)...), 1, "repository_unavailable")
			// Compound: unborn and opposite width -> unborn wins.
			wantFail(t, runCLI(t, nil, createArgs(root, rrID, unborn, strings.Repeat("a", other))...), 1, "repository_unborn")
			if tree(t, root) != rootBefore {
				t.Fatal("early failure changed state root")
			}

			// Create via a subdirectory with uppercase references.
			sub := filepath.Join(repo, "docs")
			c := runCLI(t, nil, "run", "create", "--state-dir="+root, "--run-id", rrID, "--repo", sub,
				"--ticket", rrTicket, "--scope-sha256", rrScope, "--policy-commit="+policyUpper, "--json")
			rec := decodeRecord(t, c, "create")
			want := map[string]string{"run_id": rrID, "record_state": "recorded", "ticket_url": rrTicket,
				"scope_sha256": strings.ToLower(rrScope), "policy_commit": policy,
				"repository_object_format": format, "repository_head": head}
			for k, v := range want {
				if rec[k] != v {
					t.Fatalf("%s = %v want %v", k, rec[k], v)
				}
			}
			created := rec["created_at"].(string)
			if !createdRE.MatchString(created) {
				t.Fatalf("created_at %q", created)
			}
			for _, s := range []string{tf.SecretBranch, tf.SecretFile, tf.SecretContent, repo} {
				if strings.Contains(c.stdout, s) {
					t.Fatalf("leaked %q", s)
				}
			}
			if tree(t, repo) != repoBefore {
				t.Fatal("repository changed")
			}
			// Stored bytes: exactly the nine fields; modes; retained staging link.
			ns := filepath.Join(root, "records-v1")
			final := filepath.Join(ns, rrID+".json")
			stored, err := os.ReadFile(final)
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(stored, &m); err != nil || len(m) != 9 {
				t.Fatalf("stored %q", stored)
			}
			if fi, _ := os.Lstat(final); fi.Mode() != 0o600 {
				t.Fatalf("record mode %v", fi.Mode())
			}
			if fi, _ := os.Lstat(ns); fi.Mode() != fs.ModeDir|0o700 {
				t.Fatalf("namespace mode %v", fi.Mode())
			}
			ents, _ := os.ReadDir(ns)
			if len(ents) != 2 || !strings.HasPrefix(ents[0].Name(), ".pending-"+rrID+"-") {
				t.Fatalf("namespace entries %v", ents)
			}
			if b, _ := os.ReadFile(filepath.Join(root, "run.json")); string(b) != "manual receipt\n" {
				t.Fatal("pre-existing entry changed")
			}

			// Status repeats identical data, text and JSON.
			text := textReport("status", rrID, rrTicket, strings.ToLower(rrScope), policy, head, format, created)
			for i := 0; i < 2; i++ {
				r := runCLI(t, nil, "status", "--state-dir", root, "--run-id", rrID)
				if r.code != 0 || r.stderr != "" || r.stdout != text {
					t.Fatalf("status text %q %q", r.stdout, r.stderr)
				}
				srec := decodeRecord(t, runCLI(t, nil, "status", "--state-dir", root, "--run-id", rrID, "--json"), "status")
				for _, k := range nineFields {
					if srec[k] != rec[k] {
						t.Fatalf("status %s differs", k)
					}
				}
			}
			// Duplicate (different scope): record_exists, bytes preserved.
			before := tree(t, root)
			dup := createArgs(root, rrID, repo, policy)
			dup[len(dup)-3] = strings.Repeat("0", 64)
			wantFail(t, runCLI(t, nil, dup...), 1, "record_exists")
			if tree(t, root) != before {
				t.Fatal("duplicate changed state")
			}
			// Opposite width with namespace present also writes nothing.
			wantFail(t, runCLI(t, nil, createArgs(root, rrID2, repo, strings.Repeat("a", other))...), 2, "invalid_usage")
			if tree(t, root) != before {
				t.Fatal("width failure changed state")
			}
			// Second record, text output: fields other than run_id/created_at agree.
			c2 := runCLI(t, nil, createArgs(root, rrID2, repo, policy)...)
			if c2.code != 0 || c2.stderr != "" || !strings.HasPrefix(c2.stdout, "BAW run record\nOperation: create\nRun: "+rrID2+"\n") ||
				!strings.Contains(c2.stdout, "\nScope SHA-256: "+strings.ToLower(rrScope)+"\nPolicy reference: "+policy+"\nRecorded HEAD: "+head+"\nObject format: "+format+"\n") {
				t.Fatalf("second create %q %q", c2.stdout, c2.stderr)
			}

			// Status without Git/gh and without the repository.
			bin := t.TempDir()
			markers := t.TempDir()
			for _, name := range []string{"git", "gh"} {
				script(t, filepath.Join(bin, name), "touch '"+filepath.Join(markers, name)+"'\nexit 1")
			}
			t.Setenv("PATH", bin)
			if err := os.RemoveAll(repo); err != nil {
				t.Fatal(err)
			}
			before = tree(t, root)
			r := runCLI(t, nil, "status", "--state-dir", root, "--run-id", rrID)
			if r.code != 0 || r.stdout != text {
				t.Fatalf("git-free status %q %q", r.stdout, r.stderr)
			}
			assertNoMarkers(t, markers)
			if tree(t, root) != before {
				t.Fatal("status changed state")
			}
		})
	}
}

func TestRunRecordUsage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent") // syntax errors win over filesystem
	pol := strings.Repeat("a", 40)
	base := func() []string { return createArgs(root, rrID, "/nonexistent", pol) }
	with := func(i int, v string) []string { a := base(); a[i] = v; return a }
	cases := [][]string{
		{"run"}, {"run", "create"}, {"run", "delete"}, {"status"},
		{"run", "-h"}, {"run", "help"}, {"status", "-h"},
		{"run", "--help", "--json"}, {"run", "create", "--help", "--json"},
		{"status", "--help", "--state-dir", root},
		{"status", "--state-dir", root, "--run-id", rrID, "extra"},
		{"status", "--state-dir", root, "--run-id", rrID, "--json", "--json"},
		{"status", "--state-dir", root, "--run-id", rrID, "--json=true"},
		{"status", "--state-dir", root, "--run-id", rrID, "--state-dir", root},
		{"status", "--state-dir", root, "--run-id", rrID, "--repo", "/x"},
		{"status", "--state-dir", "", "--run-id", rrID},
		{"status", "--state-dir", root, "--run-id"},
		with(5, strings.ToUpper(rrID)), with(5, rrID+"0"), with(5, "../"+rrID[3:]), with(5, rrID[:31]+"g"),
		with(9, "https://github.com/a/b/issues/1/"), with(9, "https://github.com/a/b/issues/0"),
		with(9, "https://github.com/a/b/issues/1?q=1"), with(9, "https://x@github.com/a/b/issues/1"),
		with(11, rrScope[:63]), with(11, rrScope+"0"), with(11, strings.Repeat("g", 64)),
		with(13, strings.Repeat("a", 39)), with(13, strings.Repeat("a", 50)), with(13, strings.Repeat("z", 40)),
		append(base(), "--bogus"), append(base(), "--json", "--json"),
	}
	for _, a := range cases {
		r := runCLI(t, nil, a...)
		if r.code != 2 || r.stdout != "" || r.stderr != "baw: invalid_usage\n" {
			t.Errorf("%q: exit %d %q %q", a, r.code, r.stdout, r.stderr)
		}
	}
	if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("usage error touched filesystem")
	}
}

type badWriter struct {
	short bool // short count with nil error
	wrote bytes.Buffer
}

func (w *badWriter) Write(p []byte) (int, error) {
	if w.short {
		w.wrote.Write(p[:len(p)/2])
		return len(p) / 2, nil
	}
	return 0, errors.New("sink failed")
}

func TestRunRecordHelpAndSinks(t *testing.T) {
	for _, a := range [][]string{{"run", "--help"}, {"run", "create", "--help"}, {"status", "--help"}} {
		r := runCLI(t, nil, a...)
		if r.code != 0 || r.stdout != Usage || r.stderr != "" {
			t.Fatalf("%v: %d %q", a, r.code, r.stderr)
		}
		for _, short := range []bool{false, true} {
			var errb bytes.Buffer
			if code := Run(a, &badWriter{short: short}, &errb); code != 1 || errb.String() != "baw: output_unavailable\n" {
				t.Fatalf("%v short=%v: %d %q", a, short, code, errb.String())
			}
		}
	}
	// Pre-existing aliases keep their unchecked-write behavior.
	for _, a := range [][]string{{"--help"}, {"-h"}, {"help"}, {"inspect", "--help"}, {"inspect", "-h"}, {"inspect", "help"}} {
		var errb bytes.Buffer
		if code := Run(a, &badWriter{}, &errb); code != 0 || errb.Len() != 0 {
			t.Fatalf("old alias %v: %d %q", a, code, errb.String())
		}
	}
	for _, line := range []string{"baw run create --state-dir DIR --run-id ID --repo PATH --ticket URL --scope-sha256 HASH --policy-commit OID [--json]",
		"baw status --state-dir DIR --run-id ID [--json]"} {
		if !strings.Contains(Usage, line) {
			t.Fatalf("help lacks %q", line)
		}
	}

	home := env(t)
	repo := mustInit(t, home, filepath.Join(t.TempDir(), "repo"), "sha1")
	if _, err := tf.CommitSources(home, repo); err != nil {
		t.Fatal(err)
	}
	root := stateRoot(t)
	pol := strings.Repeat("b", 40)
	// Create delivery failure after publication: commit_uncertain, retained.
	for i, short := range []bool{false, true} {
		id := []string{rrID, rrID2}[i]
		var errb bytes.Buffer
		w := &badWriter{short: short}
		if code := Run(createArgs(root, id, repo, pol), w, &errb); code != 1 || errb.String() != "baw: commit_uncertain\n" {
			t.Fatalf("create sink short=%v: %d %q", short, code, errb.String())
		}
		r := runCLI(t, nil, "status", "--state-dir", root, "--run-id", id)
		if r.code != 0 || !strings.HasPrefix(r.stdout, "BAW run record\nOperation: status\nRun: "+id+"\n") {
			t.Fatalf("record not retained: %q %q", r.stdout, r.stderr)
		}
		if short && !strings.HasPrefix(r.stdout[len("BAW run record\nOperation: status"):], w.wrote.String()[len("BAW run record\nOperation: create"):]) {
			t.Fatalf("short prefix %q", w.wrote.String())
		}
		// Status delivery failure: output_unavailable.
		var errb2 bytes.Buffer
		if code := Run([]string{"status", "--state-dir", root, "--run-id", id, "--json"}, &badWriter{short: short}, &errb2); code != 1 || errb2.String() != "baw: output_unavailable\n" {
			t.Fatalf("status sink: %d %q", code, errb2.String())
		}
	}
	// Failed stderr sink: exit code still exact, no panic.
	var out bytes.Buffer
	if code := Run([]string{"status", "--state-dir", root, "--run-id", strings.Repeat("c", 32)}, &out, &badWriter{}); code != 1 || out.Len() != 0 {
		t.Fatalf("failed stderr: %d", code)
	}
	if code := Run([]string{"run"}, &out, &badWriter{}); code != 2 || out.Len() != 0 {
		t.Fatalf("failed stderr usage: %d", code)
	}
}

func TestRunRecordStateFailures(t *testing.T) {
	home := env(t)
	repo := mustInit(t, home, filepath.Join(t.TempDir(), "repo"), "sha1")
	if _, err := tf.CommitSources(home, repo); err != nil {
		t.Fatal(err)
	}
	pol := strings.Repeat("c", 40)
	base := t.TempDir()
	open := filepath.Join(base, "open")
	os.Mkdir(open, 0o755)
	os.Chmod(open, 0o755)
	link := filepath.Join(base, "link")
	os.Symlink(stateRoot(t), link)
	// Root safety outranks Git discovery errors.
	for _, c := range []struct{ dir, code string }{
		{filepath.Join(base, "missing"), "state_unavailable"}, {open, "state_permissions"}, {link, "unsafe_state_path"}, {link + "/", "unsafe_state_path"},
	} {
		wantFail(t, runCLI(t, nil, createArgs(c.dir, rrID, "/nonexistent-repo", pol)...), 1, c.code)
		wantFail(t, runCLI(t, nil, "status", "--state-dir", c.dir, "--run-id", rrID), 1, c.code)
	}
	if _, err := os.Lstat(filepath.Join(open, "records-v1")); err == nil {
		t.Fatal("namespace created in rejected root")
	}
	// Status on a root with no namespace: record_missing, nothing created.
	root := stateRoot(t)
	before := tree(t, root)
	wantFail(t, runCLI(t, nil, "status", "--state-dir", root, "--run-id", rrID), 1, "record_missing")
	if tree(t, root) != before {
		t.Fatal("status created state")
	}
	// Corrupt record: fixed code, empty stdout, no content leak.
	if r := runCLI(t, nil, createArgs(root, rrID, repo, pol)...); r.code != 0 {
		t.Fatalf("create %q", r.stderr)
	}
	final := filepath.Join(root, "records-v1", rrID+".json")
	secret := `{"schema_version":1,"run_id":"sparrow-indigo-6061-recordsecret` + "\x1b[2J" + `"}`
	// Editing either name changes the same inode (procedural immutability).
	if err := os.WriteFile(final, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	r := runCLI(t, nil, "status", "--state-dir", root, "--run-id", rrID, "--json")
	wantFail(t, r, 1, "invalid_record")
	if strings.Contains(r.stderr, "sparrow") || strings.Contains(r.stderr, "\x1b") {
		t.Fatal("leak")
	}
	// Existing unsafe record during create: classified, never parsed/overwritten.
	os.Chmod(final, 0o644)
	wantFail(t, runCLI(t, nil, createArgs(root, rrID, repo, pol)...), 1, "state_permissions")
	if b, _ := os.ReadFile(final); string(b) != secret {
		t.Fatal("record overwritten")
	}
	// Hostile helpers remain suppressed through the reused inspector.
	dir, markers := helperFixture(t, home, false)
	if r := runCLI(t, nil, createArgs(stateRoot(t), rrID, dir, pol)...); r.code != 0 {
		t.Fatalf("helper fixture create %q", r.stderr)
	}
	assertNoMarkers(t, markers)
	dir2, markers2 := helperFixture(t, home, true)
	root2 := stateRoot(t)
	before2 := tree(t, root2)
	wantFail(t, runCLI(t, nil, createArgs(root2, rrID, dir2, pol)...), 1, "unsupported_filters")
	assertNoMarkers(t, markers2)
	if tree(t, root2) != before2 {
		t.Fatal("inspection failure changed root")
	}
}
