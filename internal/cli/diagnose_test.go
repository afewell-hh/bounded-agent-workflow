package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// diagText is the hand-written terminal oracle.
func diagText(id, ns, final string, c [6]int) string {
	return fmt.Sprintf("BAW run diagnosis\nRun: %s\nNamespace: %s\nFinal record: %s\n"+
		"Staging: total=%d valid=%d invalid_record=%d record_too_large=%d unsupported_record_version=%d linked_to_final=%d\n"+
		"Snapshot: non_atomic\nDurability: unknown\nAuthority: not_evaluated\nRuntime state: unknown\n"+
		"Process ownership: unknown\nRemote freshness: unknown\nReservation ownership: unknown\n",
		id, ns, final, c[0], c[1], c[2], c[3], c[4], c[5])
}

// diagJSON is the hand-written exact JSON oracle.
func diagJSON(id, ns, final string, c [6]int) string {
	return fmt.Sprintf(`{"schema_version":1,"operation":"diagnose","run_id":"%s","namespace":"%s","final_record":"%s",`+
		`"staging":{"total":%d,"valid":%d,"invalid_record":%d,"record_too_large":%d,"unsupported_record_version":%d,"linked_to_final":%d},`+
		`"snapshot":"non_atomic","durability":"unknown","authority":"not_evaluated","runtime_state":"unknown",`+
		`"process_ownership":"unknown","remote_freshness":"unknown","reservation_ownership":"unknown"}`+"\n",
		id, ns, final, c[0], c[1], c[2], c[3], c[4], c[5])
}

// checkDiagJSONShape decodes independently of production types: exact key
// sets, integer counts and fixed enums.
func checkDiagJSONShape(t *testing.T, out string) {
	t.Helper()
	var top map[string]any
	dec := json.NewDecoder(strings.NewReader(out))
	dec.UseNumber()
	if err := dec.Decode(&top); err != nil {
		t.Fatal(err)
	}
	keys := []string{"schema_version", "operation", "run_id", "namespace", "final_record", "staging", "snapshot",
		"durability", "authority", "runtime_state", "process_ownership", "remote_freshness", "reservation_ownership"}
	if len(top) != len(keys) {
		t.Fatalf("keys %v", top)
	}
	for _, k := range keys {
		if _, ok := top[k]; !ok {
			t.Fatalf("missing %s", k)
		}
	}
	st, ok := top["staging"].(map[string]any)
	skeys := []string{"total", "valid", "invalid_record", "record_too_large", "unsupported_record_version", "linked_to_final"}
	if !ok || len(st) != len(skeys) {
		t.Fatalf("staging %v", top["staging"])
	}
	n := map[string]int{}
	for _, k := range skeys {
		num, ok := st[k].(json.Number)
		v, err := num.Int64()
		if !ok || err != nil || v < 0 || v > 32 {
			t.Fatalf("%s = %v", k, st[k])
		}
		n[k] = int(v)
	}
	if n["total"] != n["valid"]+n["invalid_record"]+n["record_too_large"]+n["unsupported_record_version"] ||
		n["linked_to_final"] > n["total"] || (top["final_record"] != "valid" && n["linked_to_final"] != 0) {
		t.Fatalf("inconsistent counts %v", n)
	}
	enums := map[string][]string{"namespace": {"absent", "present"},
		"final_record": {"missing", "valid", "invalid_record", "record_too_large", "unsupported_record_version"}}
	for k, allowed := range enums {
		found := false
		for _, a := range allowed {
			found = found || top[k] == a
		}
		if !found {
			t.Fatalf("%s = %v", k, top[k])
		}
	}
	if top["schema_version"] != json.Number("1") || top["operation"] != "diagnose" {
		t.Fatalf("envelope %v", top)
	}
}

func wantDiag(t *testing.T, root, id, ns, final string, c [6]int) {
	t.Helper()
	before := diagMeta(t, root)
	r := runCLI(t, nil, "run", "diagnose", "--state-dir", root, "--run-id", id)
	if r.code != 0 || r.stderr != "" || r.stdout != diagText(id, ns, final, c) {
		t.Fatalf("text: exit %d %q %q", r.code, r.stdout, r.stderr)
	}
	r = runCLI(t, nil, "run", "diagnose", "--state-dir="+root, "--json", "--run-id="+id)
	if r.code != 0 || r.stderr != "" || r.stdout != diagJSON(id, ns, final, c) {
		t.Fatalf("json: exit %d %q %q", r.code, r.stdout, r.stderr)
	}
	checkDiagJSONShape(t, r.stdout)
	if diagMeta(t, root) != before {
		t.Fatal("diagnose changed saved data or metadata")
	}
}

// diagMeta lists paths, modes, owners, inodes, link counts, sizes, mtimes and
// contents under dir (not access times).
func diagMeta(t *testing.T, dir string) string {
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

const (
	secretTicket = "https://github.com/sparrow-secret-owner/indigo-secret-repo/issues/6061"
	secretScope  = "5ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2e75ec2"
)

func TestRunDiagnoseBothFormats(t *testing.T) {
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
			policy := strings.Repeat("7", len(head))
			root := stateRoot(t)
			ns := filepath.Join(root, "records-v1")
			wantDiag(t, root, rrID, "absent", "missing", [6]int{})
			if _, err := os.Lstat(ns); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("diagnose created the namespace")
			}
			c := runCLI(t, nil, "run", "create", "--state-dir", root, "--run-id", rrID, "--repo", repo,
				"--ticket", secretTicket, "--scope-sha256", secretScope, "--policy-commit", policy)
			if c.code != 0 {
				t.Fatalf("create %q", c.stderr)
			}
			// Independently established: one staging name, same inode as the final.
			final := filepath.Join(ns, rrID+".json")
			ents, _ := os.ReadDir(ns)
			if len(ents) != 2 {
				t.Fatalf("entries %v", ents)
			}
			staging := filepath.Join(ns, ents[0].Name())
			a, _ := os.Lstat(final)
			b, _ := os.Lstat(staging)
			if !os.SameFile(a, b) {
				t.Fatal("fixture: staging not linked")
			}
			wantDiag(t, root, rrID, "present", "valid", [6]int{1, 1, 0, 0, 0, 1})
			// Other ID in the same namespace: missing final, no staging.
			wantDiag(t, root, rrID2, "present", "missing", [6]int{})

			// Separate identical copy, mixed categories and ignored names.
			stored, _ := os.ReadFile(final)
			pend := func(rnd string) string { return filepath.Join(ns, ".pending-"+rrID+"-"+rnd) }
			put := func(p string, data []byte) {
				if err := os.WriteFile(p, data, 0o600); err != nil {
					t.Fatal(err)
				}
				os.Chmod(p, 0o600)
			}
			put(pend("ffffffffffffffffffffffffffffff01"), stored)
			put(pend("ffffffffffffffffffffffffffffff02"), stored[:len(stored)/2])
			put(pend("ffffffffffffffffffffffffffffff03"), []byte(strings.Repeat(" ", 16385)))
			put(pend("ffffffffffffffffffffffffffffff04"), []byte(`{"schema_version":2}`))
			os.WriteFile(filepath.Join(ns, ".pending-"+rrID+"-SECRET-sparrow"), []byte(secretTicket), 0o644)
			os.Symlink(filepath.Join(root, "run.json"), filepath.Join(ns, "sparrow-secret-link"))
			syscall.Mkfifo(filepath.Join(ns, ".pending-"+rrID2+"-ffffffffffffffffffffffffffffff05"), 0o600)
			wantDiag(t, root, rrID, "present", "valid", [6]int{5, 2, 1, 1, 1, 1})

			// Leakage: no stored references, names, suffixes, paths or HEAD.
			for _, id := range []string{rrID, rrID2} {
				for _, extra := range [][]string{nil, {"--json"}} {
					r := runCLI(t, nil, append([]string{"run", "diagnose", "--state-dir", root, "--run-id", id}, extra...)...)
					for _, s := range []string{"sparrow", "indigo", "6061", secretScope, policy, head, root, ".pending", "ffffffff",
						strings.TrimPrefix(filepath.Base(staging), ".pending-"+rrID+"-"), "manual receipt", "202"} {
						if strings.Contains(r.stdout+r.stderr, s) {
							t.Fatalf("leaked %q", s)
						}
					}
				}
			}
			// Create and status are unaffected, and diagnose ran no Git/gh
			// with the repository removed.
			r := runCLI(t, nil, "status", "--state-dir", root, "--run-id", rrID)
			if r.code != 0 || !strings.Contains(r.stdout, "Ticket: "+secretTicket+"\n") {
				t.Fatalf("status %q %q", r.stdout, r.stderr)
			}
			bin, markers := t.TempDir(), t.TempDir()
			for _, name := range []string{"git", "gh"} {
				script(t, filepath.Join(bin, name), "touch '"+filepath.Join(markers, name)+"'\nexit 1")
			}
			t.Setenv("PATH", bin)
			os.RemoveAll(repo)
			wantDiag(t, root, rrID, "present", "valid", [6]int{5, 2, 1, 1, 1, 1})
			assertNoMarkers(t, markers)
		})
	}
}

func TestRunDiagnoseUsage(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent") // syntax errors win over filesystem
	d := func(extra ...string) []string { return append([]string{"run", "diagnose"}, extra...) }
	for _, a := range [][]string{
		d(), d("--state-dir", root), d("--run-id", rrID), d("--state-dir", root, "--run-id"),
		d("--state-dir", "", "--run-id", rrID), d("--state-dir", root, "--run-id", strings.ToUpper(rrID)),
		d("--state-dir", root, "--run-id", rrID+"0"), d("--state-dir", root, "--run-id", "../"+rrID[3:]),
		d("--state-dir", root, "--run-id", rrID, "--json=true"), d("--state-dir", root, "--run-id", rrID, "--json", "--json"),
		d("--state-dir", root, "--run-id", rrID, "--state-dir", root), d("--state-dir", root, "--run-id", rrID, "--repo", "/x"),
		d("--state-dir", root, "--run-id", rrID, "extra"), d("--help", "--json"), d("--state-dir", root, "--help"),
		d("-h"), d("help"), d("--checkpoint", strings.Repeat("a", 40)), {"run", "diagnoses"}, {"diagnose"},
	} {
		r := runCLI(t, nil, a...)
		if r.code != 2 || r.stdout != "" || r.stderr != "baw: invalid_usage\n" {
			t.Errorf("%q: exit %d %q %q", a, r.code, r.stdout, r.stderr)
		}
	}
	if _, err := os.Lstat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("usage error touched filesystem")
	}
}

func TestRunDiagnoseStateErrors(t *testing.T) {
	base := t.TempDir()
	open := filepath.Join(base, "open")
	os.Mkdir(open, 0o755)
	os.Chmod(open, 0o755)
	link := filepath.Join(base, "link")
	os.Symlink(stateRoot(t), link)
	file := filepath.Join(base, "file")
	os.WriteFile(file, nil, 0o600)
	for _, c := range []struct{ dir, code string }{
		{filepath.Join(base, "missing"), "state_unavailable"}, {file, "state_unavailable"},
		{open, "state_permissions"}, {link, "unsafe_state_path"}, {link + "/", "unsafe_state_path"},
	} {
		for _, extra := range [][]string{nil, {"--json"}} {
			wantFail(t, runCLI(t, nil, append([]string{"run", "diagnose", "--state-dir", c.dir, "--run-id", rrID}, extra...)...), 1, c.code)
		}
	}
	// An ancestor alias is resolved while the final component is checked.
	target := stateRoot(t)
	alias := filepath.Join(base, "alias")
	os.Symlink(filepath.Dir(target), alias)
	wantDiag(t, filepath.Join(alias, filepath.Base(target)), rrID, "absent", "missing", [6]int{})

	// Namespace and entry safety.
	nsCase := func(setup func(ns string)) string {
		root := stateRoot(t)
		setup(filepath.Join(root, "records-v1"))
		return root
	}
	mkNS := func(ns string) { os.Mkdir(ns, 0o700); os.Chmod(ns, 0o700) }
	for _, c := range []struct {
		setup func(ns string)
		code  string
	}{
		{func(ns string) { os.Symlink(t.TempDir(), ns) }, "unsafe_state_path"},
		{func(ns string) { os.WriteFile(ns, nil, 0o600) }, "unsafe_state_path"},
		{func(ns string) { os.Mkdir(ns, 0o700); os.Chmod(ns, 0o750) }, "state_permissions"},
		{func(ns string) { mkNS(ns); syscall.Mkfifo(filepath.Join(ns, rrID+".json"), 0o600) }, "unsafe_state_path"},
		{func(ns string) {
			mkNS(ns)
			syscall.Mkfifo(filepath.Join(ns, ".pending-"+rrID+"-00000000000000000000000000000001"), 0o600)
		}, "unsafe_state_path"},
		{func(ns string) {
			mkNS(ns)
			os.WriteFile(filepath.Join(ns, rrID+".json"), []byte("dummy-secret-xyz"), 0o644)
			os.Chmod(filepath.Join(ns, rrID+".json"), 0o644)
		}, "state_permissions"},
		{func(ns string) {
			mkNS(ns)
			for i := 0; i < 1025; i++ {
				os.WriteFile(filepath.Join(ns, fmt.Sprintf("n%04d", i)), nil, 0o600)
			}
		}, "state_scan_limit"},
		{func(ns string) {
			mkNS(ns)
			for i := 0; i < 33; i++ {
				os.WriteFile(filepath.Join(ns, fmt.Sprintf(".pending-%s-%032x", rrID, i)), nil, 0o600)
			}
		}, "state_scan_limit"},
	} {
		root := nsCase(c.setup)
		before := diagMeta(t, root)
		r := runCLI(t, nil, "run", "diagnose", "--state-dir", root, "--run-id", rrID, "--json")
		wantFail(t, r, 1, c.code)
		if strings.Contains(r.stderr, "dummy-secret") || diagMeta(t, root) != before {
			t.Fatal("leak or change")
		}
	}
}

// TestRunDiagnoseFatalCodes replaces only the storage observation to check
// every fatal code's exit, empty stdout and best-effort stderr.
func TestRunDiagnoseFatalCodes(t *testing.T) {
	root := stateRoot(t)
	defer func() { diagnose = (*state.Root).Diagnose }()
	for _, code := range []string{"state_unavailable", "state_permissions", "unsafe_state_path", "record_unavailable",
		"state_changed", "state_scan_limit"} {
		diagnose = func(*state.Root, string) (state.Diagnosis, error) {
			return state.Diagnosis{}, &state.Error{Code: state.Code(code)}
		}
		for _, extra := range [][]string{nil, {"--json"}} {
			args := append([]string{"run", "diagnose", "--state-dir", root, "--run-id", rrID}, extra...)
			wantFail(t, runCLI(t, nil, args...), 1, code)
			// A failing stderr keeps the exit status.
			var out bytes.Buffer
			if c := Run(args, &out, &badWriter{}); c != 1 || out.Len() != 0 {
				t.Fatalf("failing stderr: %d %q", c, out.String())
			}
		}
	}
}

// prefixWriter accepts some bytes and then fails.
type prefixWriter struct{ wrote bytes.Buffer }

func (w *prefixWriter) Write(p []byte) (int, error) {
	n := len(p) / 3
	w.wrote.Write(p[:n])
	return n, errors.New("sink failed after prefix")
}

func TestRunDiagnoseHelpAndSinks(t *testing.T) {
	r := runCLI(t, nil, "run", "diagnose", "--help")
	if r.code != 0 || r.stdout != Usage || r.stderr != "" {
		t.Fatalf("help %d %q", r.code, r.stderr)
	}
	if !strings.Contains(Usage, "\n  baw run diagnose --state-dir DIR --run-id ID [--json]\n") {
		t.Fatal("help lacks diagnose usage")
	}
	root := stateRoot(t)
	for _, args := range [][]string{
		{"run", "diagnose", "--help"},
		{"run", "diagnose", "--state-dir", root, "--run-id", rrID},
		{"run", "diagnose", "--state-dir", root, "--run-id", rrID, "--json"},
	} {
		full := runCLI(t, nil, args...).stdout
		for i, w := range []interface {
			Write([]byte) (int, error)
		}{&badWriter{}, &badWriter{short: true}, &prefixWriter{}} {
			var errb bytes.Buffer
			if code := Run(args, w, &errb); code != 1 || errb.String() != "baw: output_unavailable\n" {
				t.Fatalf("%v writer %d: %d %q", args, i, code, errb.String())
			}
			var wrote string
			switch w := w.(type) {
			case *badWriter:
				wrote = w.wrote.String()
			case *prefixWriter:
				wrote = w.wrote.String()
			}
			if !strings.HasPrefix(full, wrote) {
				t.Fatalf("emitted bytes are not a prefix: %q", wrote)
			}
			// Failing stdout and stderr together: exit status unchanged.
			if code := Run(args, w, &badWriter{}); code != 1 {
				t.Fatalf("double failure exit %d", code)
			}
		}
	}
	// Existing help aliases keep their existing behavior.
	for _, a := range [][]string{{"--help"}, {"inspect", "--help"}, {"-h"}} {
		var errb bytes.Buffer
		if code := Run(a, &badWriter{}, &errb); code != 0 || errb.Len() != 0 {
			t.Fatalf("old alias %v: %d", a, code)
		}
	}
	if code := Run([]string{"run", "diagnose"}, &bytes.Buffer{}, &badWriter{}); code != 2 {
		t.Fatalf("usage with failing stderr: %d", code)
	}
}

// TestDeliverBounded is the shared bounded-output unit oracle with synthetic
// buffers; production packets stay far below the cap.
func TestDeliverBounded(t *testing.T) {
	if MaxDiagnoseOutput != 4096 {
		t.Fatalf("cap %d", MaxDiagnoseOutput)
	}
	at := bytes.Repeat([]byte("x"), 4096)
	var w bytes.Buffer
	if code := deliverBounded(&w, at, MaxDiagnoseOutput); code != "" || !bytes.Equal(w.Bytes(), at) {
		t.Fatalf("4096 bytes: %q len %d", code, w.Len())
	}
	over := bytes.Repeat([]byte("x"), 4097)
	sink := &badWriter{short: true}
	var w2 bytes.Buffer
	if code := deliverBounded(&w2, over, MaxDiagnoseOutput); code != "output_limit" || w2.Len() != 0 {
		t.Fatalf("4097 bytes: %q wrote %d", code, w2.Len())
	}
	if code := deliverBounded(sink, over, MaxDiagnoseOutput); code != "output_limit" || sink.wrote.Len() != 0 {
		t.Fatalf("4097 bytes to a failing sink: %q", code)
	}
	if code := deliverBounded(&badWriter{}, at, MaxDiagnoseOutput); code != "output_unavailable" {
		t.Fatalf("failing sink: %q", code)
	}
	if len(Usage) > MaxDiagnoseOutput {
		t.Fatalf("help %d bytes exceeds the cap", len(Usage))
	}
}
