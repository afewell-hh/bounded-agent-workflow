package worker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
	"github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

const (
	testRunID = "0123456789abcdef0123456789abcdef"
	testScope = "5555555555555555555555555555555555555555555555555555555555555555"
	ruleText  = "Project rule: when asked to record a marker, write the line 'marker: kestrel' to target.txt.\n"
)

var testPlugins = []any{
	map[string]any{"name": "approved-fixture-a", "path": "/fixture/plugin-a", "version": "fixture"},
}

// fx is one disposable worker fixture: a committed Git worktree, a run
// record, private plan/task/profile files, a fake pinned executable and a
// synthetic auth HOME. Paths are physical (temporary-directory aliases
// resolved).
type fx struct {
	t        *testing.T
	root     string
	gitHome  string
	repo     string
	format   string
	head     string
	stateDir string
	priv     string
	userHome string
	exe      string
	exeBytes []byte
	task     map[string]any
	taskSHA  string
	allowed  []string

	// Fake native behaviour for the per-call runner seam.
	runs   int
	spec   proc.Spec
	stdin  []byte
	edit   func(top string)
	stream func(f *fx) string
	obs    proc.Observation
	fault  func(stage string) error
}

func newFx(t *testing.T, format string) *fx {
	t.Helper()
	return newFxWith(t, format, nil, nil)
}

// newFxWith is newFx with extra files and symbolic links (path -> target)
// committed in the launch-base tree.
func newFxWith(t *testing.T, format string, extra, links map[string]string) *fx {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &fx{t: t, root: root, format: format, gitHome: filepath.Join(root, "githome"), repo: filepath.Join(root, "repo")}
	f.stateDir, f.priv, f.userHome = filepath.Join(root, "state"), filepath.Join(root, "priv"), filepath.Join(root, "authhome")
	f.exe = filepath.Join(root, "bin", "versions", "2.1.285")
	f.mkdir(f.gitHome, 0o755)
	f.mkdir(f.stateDir, 0o700)
	f.mkdir(f.priv, 0o700)
	f.mkdir(f.userHome, 0o755)
	f.mkdir(filepath.Dir(f.exe), 0o755)
	f.ok(testfixture.Init(f.gitHome, f.repo, format))
	files := map[string]string{
		"AGENTS.md":            ruleText,
		"CLAUDE.md":            "@AGENTS.md\n",
		"workflow/protocol.md": "Protocol body.\n",
		"target.txt":           "base\n",
	}
	for p, c := range extra {
		files[p] = c
	}
	for p, c := range files {
		f.ok(testfixture.Write(filepath.Join(f.repo, p), c))
	}
	for p, target := range links {
		f.ok(os.Symlink(target, filepath.Join(f.repo, p)))
	}
	_, err = testfixture.Git(f.gitHome, f.repo, "add", "-A")
	f.ok(err)
	_, err = testfixture.Git(f.gitHome, f.repo, "commit", "-q", "-m", "fixture")
	f.ok(err)
	f.head, err = testfixture.OID(f.gitHome, f.repo, "HEAD")
	f.ok(err)
	root2, err := state.OpenRoot(f.stateDir)
	f.ok(err)
	rec := state.Record{SchemaVersion: 1, RunID: testRunID, RecordState: "recorded", ScopeSHA256: testScope}
	rec.TicketURL, rec.PolicyCommit, rec.RepositoryHead = "https://github.com/example/repo/issues/1", f.head, f.head
	rec.RepositoryObjectFormat, rec.CreatedAt = format, "2026-01-01T00:00:00Z"
	data, err := state.Marshal(rec)
	f.ok(err)
	f.ok(root2.Create(testRunID, data, func() error { return nil }))
	f.setExecutable([]byte("#!/bin/sh\nexit 0\n"))
	f.allowed = []string{"target.txt"}
	f.task = map[string]any{"schema_version": 1, "task_id": testRunID, "scope_sha256": testScope, "base_commit": f.head}
	f.task["task"] = "Record the marker required by the project rule."
	f.task["allowed_changes"] = f.allowed
	f.task["instructions"] = []any{f.instruction("CLAUDE.md"), f.instruction("AGENTS.md"), f.instruction("workflow/protocol.md")}
	f.obs = proc.Observation{Started: true, Exited: true, Joined: true, GroupAbsent: true, StdoutEOF: true, StderrEOF: true}
	f.edit = func(top string) {
		f.ok(os.WriteFile(filepath.Join(top, "target.txt"), []byte("marker: kestrel\n"), 0o644))
	}
	f.stream = completedStream
	f.writeInputs()
	return f
}

func (f *fx) ok(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *fx) mkdir(p string, mode os.FileMode) {
	f.t.Helper()
	f.ok(os.MkdirAll(p, mode))
	f.ok(os.Chmod(p, mode))
}

func hexSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (f *fx) instruction(p string) map[string]any {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.repo, p))
	f.ok(err)
	oid, err := testfixture.OID(f.gitHome, f.repo, "HEAD:"+p)
	f.ok(err)
	return map[string]any{"path": p, "blob_oid": oid, "sha256": hexSum(b)}
}

func (f *fx) setExecutable(b []byte) {
	f.t.Helper()
	f.exeBytes = b
	f.ok(os.WriteFile(f.exe, b, 0o755))
	f.ok(os.Chmod(f.exe, 0o755))
}

func (f *fx) writePrivate(name string, v any) string {
	f.t.Helper()
	b, err := json.Marshal(v)
	f.ok(err)
	p := filepath.Join(f.priv, name)
	f.ok(os.WriteFile(p, b, 0o600))
	f.ok(os.Chmod(p, 0o600))
	return p
}

func (f *fx) profile() map[string]any {
	m := map[string]any{"schema_version": 1, "profile_id": claude.ProfileID, "source_matrix_id": claude.SourceMatrixID}
	m["executable"], m["executable_sha256"] = f.exe, hexSum(f.exeBytes)
	m["user_home"], m["user_name"], m["shell"], m["allowed_plugins"] = f.userHome, "fixture", "/bin/zsh", testPlugins
	b, _ := json.Marshal(m)
	dm, ok := claude.StrictObject(b)
	if !ok {
		f.t.Fatal("profile fixture")
	}
	h, err := claude.CanonicalProfileSHA256(dm)
	f.ok(err)
	m["approved_profile_sha256"] = h
	return m
}

// writeInputs (re)writes task, profile and plan files from the fixture.
func (f *fx) writeInputs() {
	f.writeProfile(f.profile())
	tp := f.writePrivate("task.json", f.task)
	b, err := os.ReadFile(tp)
	f.ok(err)
	f.taskSHA = hexSum(b)
	f.writePrivate("plan.json", map[string]any{"schema_version": 1, "task_file": tp, "profile_file": filepath.Join(f.priv, "profile.json")})
}

func (f *fx) writeProfile(m map[string]any) { f.writePrivate("profile.json", m) }

func (f *fx) request(asJSON bool) Request {
	return Request{Repo: f.repo, StateDir: f.stateDir, RunID: testRunID, Plan: filepath.Join(f.priv, "plan.json"), JSON: asJSON}
}

// hideAdmin presents the admin-managed root as absent so tests never depend
// on the host's admin policy; every other path uses the real metadata.
func hideAdmin(p string) (fs.FileInfo, error) {
	if p == claude.AdminRoot || strings.HasPrefix(p, claude.AdminRoot+"/") {
		return nil, &fs.PathError{Op: "lstat", Path: p, Err: syscall.ENOENT}
	}
	return os.Lstat(p)
}

// run invokes the operation with per-call seams: the fake native worker
// records its spec and stdin, applies the edit and streams its lines.
func (f *fx) run(ctx context.Context, asJSON bool) (string, bool, error) {
	var out bytes.Buffer
	d := Deps{SourceLstat: hideAdmin, Fault: f.fault}
	d.Run = func(ctx context.Context, spec proc.Spec, pkt *os.File, observe func([]byte) error) ([]byte, proc.Observation, error) {
		f.runs++
		f.spec = spec
		b, err := io.ReadAll(pkt)
		f.ok(err)
		f.stdin = b
		if f.edit != nil {
			f.edit(spec.Dir)
		}
		s := f.stream(f)
		for len(s) > 0 {
			n := 64
			if n > len(s) {
				n = len(s)
			}
			if observe([]byte(s[:n])) != nil {
				break
			}
			s = s[n:]
		}
		return nil, f.obs, nil
	}
	ok, err := RunWith(ctx, f.request(asJSON), &out, d)
	return out.String(), ok, err
}

func (f *fx) top() string { return f.repo }

func (f *fx) attempt() string { return filepath.Join(f.stateDir, Namespace, testRunID) }

func initLine(top string) string {
	return `{"type":"system","subtype":"init","apiKeySource":"none","claude_code_version":"2.1.285","mcp_servers":[],` +
		`"memory_paths":[],"model":"claude-opus-5-5","output_style":"default","permissionMode":"dontAsk",` +
		`"plugins":[{"name":"approved-fixture-a","path":"/fixture/plugin-a","version":"fixture"}],"skills":[],` +
		`"slash_commands":[],"tools":["Read","Glob","Grep","Edit","Write"],"cwd":` + quote(top) + `}`
}

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

func terminalLine(runID, taskSHA, status, reason string) string {
	report := `{"schema_version":1,"run_id":"` + runID + `","task_input_sha256":"` + taskSHA + `","status":"` + status +
		`","reason":"` + reason + `"}`
	return `{"type":"result","subtype":"success","is_error":false,"result":` + quote(report) + `}`
}

func completedStream(f *fx) string {
	return strings.Join([]string{initLine(f.top()),
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"u1","name":"Edit","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"u1","content":"ok"}]}}`,
		terminalLine(testRunID, f.taskSHA, "completed", "none")}, "\n") + "\n"
}

func notCompletedStream(f *fx) string {
	return initLine(f.top()) + "\n" + terminalLine(testRunID, f.taskSHA, "not_completed", "needs_clarification") + "\n"
}

func decodeResult(t *testing.T, out string) Result {
	t.Helper()
	var r Result
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("%v: %q", err, out)
	}
	return r
}
