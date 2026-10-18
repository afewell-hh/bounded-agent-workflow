package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// The Go table is exactly the frozen version-bound classification fixture.
func TestClassificationsMatchFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/claude-stream-classifications.json")
	if err != nil {
		t.Fatal(err)
	}
	m, ok := StrictObject(data)
	if !ok || !ExactKeys(m, "version", "classifications", "unknown") || m["version"] != Version || m["unknown"] != StopUnknown {
		t.Fatalf("fixture header")
	}
	rows := m["classifications"].([]any)
	if len(rows) != len(Classifications) || len(rows) != 92 {
		t.Fatalf("rows %d table %d", len(rows), len(Classifications))
	}
	for i, r := range rows {
		f := r.(map[string]any)
		c := Classifications[i]
		sub, hasSub := f["subtype"].(string)
		stop, _ := f["stop_reason"].(string)
		if f["type"] != c.Type || hasSub != c.HasSubtype || sub != c.Subtype || f["action"] != c.Action || stop != c.StopReason {
			t.Fatalf("row %d: fixture %v table %+v", i, f, c)
		}
		if (f["stop_reason"] == nil) != (c.Action != ActionStop) {
			t.Fatalf("row %d stop reason presence", i)
		}
	}
}

// syntheticProfile is a hand-written canonical profile body (keys sorted,
// compact) without approved_profile_sha256; its SHA-256 is the declared hash.
const syntheticCanonical = `{"allowed_plugins":[{"name":"approved-fixture-a","path":"/fixture/plugin-a","version":"fixture"},` +
	`{"name":"approved-fixture-b","path":"/fixture/plugin-b","version":"fixture"}],` +
	`"executable":"/opt/claude/versions/2.1.285","executable_sha256":"` + exeSHA + `",` +
	`"profile_id":"claude-2.1.285-supervised-v1","schema_version":1,"shell":"/bin/zsh",` +
	`"source_matrix_id":"claude-2.1.285-source-v1","user_home":"/Users/fixture","user_name":"fixture"}`

const exeSHA = "2222222222222222222222222222222222222222222222222222222222222222"

func syntheticSHA() string {
	s := sha256.Sum256([]byte(syntheticCanonical))
	return hex.EncodeToString(s[:])
}

// syntheticProfile is the same profile written with reordered keys and
// whitespace, plus the declared hash.
func syntheticProfile(hash string) string {
	return `{
  "schema_version": 1,
  "source_matrix_id": "claude-2.1.285-source-v1",
  "profile_id": "claude-2.1.285-supervised-v1",
  "approved_profile_sha256": "` + hash + `",
  "executable": "/opt/claude/versions/2.1.285",
  "executable_sha256": "` + exeSHA + `",
  "user_home": "/Users/fixture",
  "user_name": "fixture",
  "shell": "/bin/zsh",
  "allowed_plugins": [
    {"version": "fixture", "path": "/fixture/plugin-a", "name": "approved-fixture-a"},
    {"name": "approved-fixture-b", "path": "/fixture/plugin-b", "version": "fixture"}
  ]
}`
}

func TestParseProfile(t *testing.T) {
	p, err := ParseProfile([]byte(syntheticProfile(syntheticSHA())))
	if err != nil {
		t.Fatal(err)
	}
	if p.Executable != "/opt/claude/versions/2.1.285" || p.UserHome != "/Users/fixture" || p.UserName != "fixture" ||
		p.Shell != "/bin/zsh" || p.ApprovedSHA256 != syntheticSHA() || len(p.Plugins) != 2 ||
		p.Plugins[0] != `{"name":"approved-fixture-a","path":"/fixture/plugin-a","version":"fixture"}` {
		t.Fatalf("%+v", p)
	}
	good := syntheticProfile(syntheticSHA())
	bad := map[string]string{
		"hash mismatch":       syntheticProfile(strings.Repeat("3", 64)),
		"profile id":          strings.Replace(good, `supervised-v1"`, `supervised-v2"`, 1),
		"matrix id":           strings.Replace(good, `"claude-2.1.285-source-v1"`, `"claude-2.1.285-source-v2"`, 1),
		"float version":       strings.Replace(good, `"schema_version": 1`, `"schema_version": 1.0`, 1),
		"unknown key":         strings.Replace(good, `"shell"`, `"extra": 1, "shell"`, 1),
		"duplicate key":       strings.Replace(good, `"shell": "/bin/zsh"`, `"shell": "/bin/zsh", "shell": "/bin/zsh"`, 1),
		"trailing value":      good + " {}",
		"relative home":       strings.Replace(good, `"/Users/fixture"`, `"Users/fixture"`, 1),
		"unversioned exe":     strings.Replace(good, `/versions/2.1.285"`, `/versions/latest"`, 1),
		"duplicate plugin":    strings.Replace(good, `"name": "approved-fixture-b", "path": "/fixture/plugin-b"`, `"name": "approved-fixture-a", "path": "/fixture/plugin-a"`, 1),
		"non-object plugin":   strings.Replace(good, `"allowed_plugins": [`, `"allowed_plugins": ["x",`, 1),
		"string version type": strings.Replace(good, `"schema_version": 1`, `"schema_version": "1"`, 1),
	}
	for name, body := range bad {
		// Re-declare a matching hash wherever the body still decodes, so each
		// case fails for its own reason rather than for the hash.
		if m, ok := StrictObject([]byte(body)); ok && name != "hash mismatch" {
			if h, err := CanonicalProfileSHA256(m); err == nil {
				body = strings.Replace(body, syntheticSHA(), h, 1)
			}
		}
		if _, err := ParseProfile([]byte(body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The canonical hash binds every non-hash field: changing a plugin value
	// type (string "1" versus number 1) changes it.
	m1, _ := StrictObject([]byte(`{"a":[{"v":"1"}],"approved_profile_sha256":"x"}`))
	m2, _ := StrictObject([]byte(`{"a":[{"v":1}]}`))
	h1, _ := CanonicalProfileSHA256(m1)
	h2, _ := CanonicalProfileSHA256(m2)
	if h1 == h2 {
		t.Fatal("type change did not change canonical hash")
	}
}

// The argv is exactly the frozen D1 template; only the worktree rule varies.
func TestArgsExactTemplate(t *testing.T) {
	want := []string{"--print", "--safe-mode", "--restricted", "--setting-sources", "", "--permission-mode", "dontAsk",
		"--permission-prompts", "none", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`,
		"--tools", "Read,Glob,Grep,Edit,Write",
		"--settings", `{"permissions":{"allow":["Edit(//Users/op/git/wt/**)","Write(//Users/op/git/wt/**)"],"deny":[]}}`,
		"--no-chrome", "--no-session-persistence", "--output-format", "stream-json", "--verbose",
		"--model", "claude-opus-5-5", "--disable-slash-commands", "--include-hook-events"}
	got := Args("/Users/op/git/wt")
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") || len(got) != len(want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	// A worktree path needing JSON escaping is escaped, not injected.
	got = Args(`/tmp/a"b`)
	if got[15] != `{"permissions":{"allow":["Edit(//tmp/a\"b/**)","Write(//tmp/a\"b/**)"],"deny":[]}}` {
		t.Fatalf("escaped %q", got[15])
	}
}

func TestEnvExact(t *testing.T) {
	p := Profile{UserHome: "/Users/fixture", UserName: "fixture", Shell: "/bin/zsh"}
	want := []string{"HOME=/Users/fixture", "TMPDIR=/s/worker-v1/id/worker/tmp", "PATH=/usr/bin:/bin:/opt/homebrew/bin",
		"LANG=C", "LC_ALL=C", "USER=fixture", "LOGNAME=fixture", "SHELL=/bin/zsh", "ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "DISABLE_AUTOUPDATER=1"}
	got := Env(p, "/s/worker-v1/id/worker/tmp")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%q", got)
	}
}
