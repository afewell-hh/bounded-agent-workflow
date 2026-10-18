package claude

import (
	"strings"
	"testing"
)

const (
	tRun  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tTask = "1111111111111111111111111111111111111111111111111111111111111111"
	tWT   = "/fixture/wt"
)

// Lines from the sanitized synthetic init/terminal fixtures.
const (
	initLine = `{"type":"system","subtype":"init","apiKeySource":"none","claude_code_version":"2.1.285",` +
		`"mcp_servers":[],"memory_paths":[],"model":"claude-opus-5-5","output_style":"default","permissionMode":"dontAsk",` +
		`"plugins":[{"name":"approved-fixture-a","path":"/fixture/plugin-a","version":"fixture"},` +
		`{"name":"approved-fixture-b","path":"/fixture/plugin-b","version":"fixture"}],` +
		`"skills":[],"slash_commands":[],"tools":["Read","Glob","Grep","Edit","Write"],"session_id":"s","cwd":"/fixture/wt"}`
	toolUse    = `{"type":"assistant","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t1","name":"Edit","input":{}}]},"parent_tool_use_id":null}`
	toolResult = `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"done"}]},"parent_tool_use_id":null}`
	terminal   = `{"type":"result","subtype":"success","is_error":false,"result":"{\"reason\": \"none\", \"run_id\": \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\", \"schema_version\": 1, \"status\": \"completed\", \"task_input_sha256\": \"1111111111111111111111111111111111111111111111111111111111111111\"}"}`
)

var fixturePlugins = []string{
	`{"name":"approved-fixture-a","path":"/fixture/plugin-a","version":"fixture"}`,
	`{"name":"approved-fixture-b","path":"/fixture/plugin-b","version":"fixture"}`,
}

func newTestParser() *Parser {
	return NewParser(Expect{RunID: tRun, TaskInputSHA256: tTask, Worktree: tWT, Plugins: fixturePlugins})
}

// feed sends lines in small uneven chunks, as a pipe may deliver them.
func feed(p *Parser, lines ...string) (Summary, error) {
	s := strings.Join(lines, "\n") + "\n"
	for len(s) > 0 {
		n := 7
		if n > len(s) {
			n = len(s)
		}
		if err := p.Feed([]byte(s[:n])); err != nil {
			return Summary{}, err
		}
		s = s[n:]
	}
	return p.Finish()
}

func TestStreamCompleted(t *testing.T) {
	info := `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","isUsingOverage":false}}`
	denied := `{"type":"system","subtype":"permission_denied","tool_name":"Edit"}`
	s, err := feed(newTestParser(), initLine, info, toolUse, denied, toolResult, terminal)
	if err != nil {
		t.Fatal(err)
	}
	if !s.InitVerified || !s.TerminalVerified || s.Status != StatusCompleted || s.Reason != "none" ||
		s.MemoryObservation != MemoryEmptyReported || s.PluginCount != 2 ||
		len(s.InformationCounts) != 2 || s.InformationCounts["rate_limit_event"] != 1 ||
		s.InformationCounts["system/permission_denied"] != 1 {
		t.Fatalf("%+v", s)
	}
}

// A truthful noncompletion is a verified terminal with its own status.
func TestStreamTruthfulNotCompleted(t *testing.T) {
	r := strings.Replace(strings.Replace(terminal, `\"completed\"`, `\"not_completed\"`, 1), `\"none\"`, `\"blocked\"`, 1)
	s, err := feed(newTestParser(), initLine, r)
	if err != nil || s.Status != StatusNotCompleted || s.Reason != "blocked" || !s.TerminalVerified {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestStreamMemoryAbsentIsSuppressionOnly(t *testing.T) {
	line := strings.Replace(strings.Replace(initLine, `"memory_paths":[],`, ``, 1), `"slash_commands":[],`, ``, 1)
	s, err := feed(newTestParser(), line, terminal)
	if err != nil || s.MemoryObservation != MemorySuppressionOnly {
		t.Fatalf("%+v %v", s, err)
	}
}

// Every negative case names its exact expected stop code.
func TestStreamStops(t *testing.T) {
	rep := func(old, new string) string { return strings.Replace(terminal, old, new, 1) }
	ini := func(old, new string) string { return strings.Replace(initLine, old, new, 1) }
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"unknown type", []string{initLine, `{"type":"mystery"}`}, StopUnknown},
		{"unknown system subtype", []string{initLine, `{"type":"system","subtype":"brand_new"}`}, StopUnknown},
		{"hook event", []string{`{"type":"system","subtype":"hook_started"}`}, "native_profile_boundary_event"},
		{"auth transition", []string{initLine, `{"type":"auth_status"}`}, "native_auth_transition"},
		{"api retry", []string{initLine, `{"type":"system","subtype":"api_retry"}`}, "native_api_retry"},
		{"refusal", []string{initLine, `{"type":"system","subtype":"model_refusal_no_fallback"}`}, "native_model_or_refusal_stop"},
		{"background task", []string{initLine, `{"type":"system","subtype":"task_started"}`}, "native_profile_boundary_event"},
		{"max turns", []string{initLine, `{"type":"result","subtype":"error_max_turns","is_error":true}`}, StopCompletionError},
		{"result is_error", []string{initLine, rep(`"is_error":false`, `"is_error":true`)}, StopCompletionError},
		{"result null subtype", []string{initLine, rep(`"subtype":"success",`, ``)}, StopCompletionError},
		{"malformed", []string{initLine, `{"type":`}, StopMalformed},
		{"duplicate key", []string{initLine, `{"type":"user","type":"user"}`}, StopMalformed},
		{"empty line", []string{initLine, ``}, StopMalformed},
		{"non-string subtype", []string{`{"type":"system","subtype":1}`}, StopMalformed},
		{"assistant before init", []string{toolUse}, StopOrder},
		{"second init", []string{initLine, initLine}, StopOrder},
		{"disallowed tool", []string{initLine, strings.Replace(toolUse, `"Edit"`, `"Bash"`, 1)}, StopTool},
		{"agent tool", []string{initLine, strings.Replace(toolUse, `"Edit"`, `"Task"`, 1)}, StopTool},
		{"duplicate tool id", []string{initLine, toolUse, toolUse}, StopCorrelation},
		{"unmatched result", []string{initLine, toolResult}, StopCorrelation},
		{"double result", []string{initLine, toolUse, toolResult, toolResult}, StopCorrelation},
		{"open call at terminal", []string{initLine, toolUse, terminal}, StopCorrelation},
		{"unsolicited user text", []string{initLine, `{"type":"user","message":{"content":"hello"}}`}, StopUnsolicited},
		{"user non-result block", []string{initLine, `{"type":"user","message":{"content":[{"type":"text","text":"x"}]}}`}, StopUnsolicited},
		{"subagent message", []string{initLine, strings.Replace(toolUse, `"parent_tool_use_id":null`, `"parent_tool_use_id":"t0"`, 1)}, StopUnsolicited},
		{"assistant error", []string{initLine, `{"type":"assistant","error":"authentication_failed","message":{"content":[]}}`}, StopNativeError},
		{"unknown block", []string{initLine, `{"type":"assistant","message":{"content":[{"type":"server_tool_use"}]}}`}, StopUnknown},
		{"after terminal", []string{initLine, terminal, `{"type":"system","subtype":"status"}`}, StopAfterTerminal},
		{"rate limit rejected", []string{initLine, `{"type":"rate_limit_event","rate_limit_info":{"status":"rejected"}}`}, StopRateLimit},
		{"overage", []string{initLine, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","isUsingOverage":true}}`}, StopRateLimit},
		{"overage in use", []string{initLine, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","overageInUse":true}}`}, StopRateLimit},
		{"overage route", []string{initLine, `{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","rateLimitType":"overage"}}`}, StopRateLimit},
		{"permission mode changed", []string{initLine, `{"type":"system","subtype":"status","status":null,"permissionMode":"bypassPermissions"}`}, StopInit},
		{"forbidden tool denial", []string{initLine, `{"type":"system","subtype":"permission_denied","tool_name":"Bash"}`}, StopTool},
		{"report wrong run", []string{initLine, rep(`aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, `bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb`)}, StopReport},
		{"report wrong task", []string{initLine, rep(`1111111111111111111111111111111111111111111111111111111111111111`, `2222222222222222222222222222222222222222222222222222222222222222`)}, StopReport},
		{"report completed with reason", []string{initLine, rep(`\"none\"`, `\"failed\"`)}, StopReport},
		{"report not_completed none", []string{initLine, rep(`\"completed\"`, `\"not_completed\"`)}, StopReport},
		{"report extra key", []string{initLine, rep(`{\"reason\"`, `{\"x\": 1, \"reason\"`)}, StopReport},
		{"report fenced", []string{initLine, rep(`"result":"{`, "\"result\":\"```json\\n{")}, StopReport},
		{"init api key", []string{ini(`"apiKeySource":"none"`, `"apiKeySource":"ANTHROPIC_API_KEY"`)}, StopInit},
		{"init version", []string{ini(`"2.1.285"`, `"2.1.286"`)}, StopInit},
		{"init model", []string{ini(`"claude-opus-5-5"`, `"claude-sonnet-5-5"`)}, StopInit},
		{"init permission mode", []string{ini(`"dontAsk"`, `"default"`)}, StopInit},
		{"init memory", []string{ini(`"memory_paths":[]`, `"memory_paths":["/m"]`)}, StopInit},
		{"init mcp", []string{ini(`"mcp_servers":[]`, `"mcp_servers":[{"name":"x"}]`)}, StopInit},
		{"init skills", []string{ini(`"skills":[]`, `"skills":["s"]`)}, StopInit},
		{"init slash commands", []string{ini(`"slash_commands":[]`, `"slash_commands":["x"]`)}, StopInit},
		{"init style", []string{ini(`"output_style":"default"`, `"output_style":"custom"`)}, StopInit},
		{"init missing tool", []string{ini(`"Grep",`, ``)}, StopInit},
		{"init extra tool", []string{ini(`"Write"]`, `"Write","Bash"]`)}, StopInit},
		{"init duplicate tool", []string{ini(`"Glob"`, `"Read"`)}, StopInit},
		{"init unapproved plugin", []string{ini(`"approved-fixture-b"`, `"other"`)}, StopInit},
		{"init duplicate plugin", []string{ini(`"approved-fixture-b","path":"/fixture/plugin-b"`, `"approved-fixture-a","path":"/fixture/plugin-a"`)}, StopInit},
		{"init plugin error", []string{ini(`"skills":[]`, `"skills":[],"plugin_errors":["e"]`)}, StopInit},
		{"init cwd", []string{ini(`"cwd":"/fixture/wt"`, `"cwd":"/elsewhere"`)}, StopInit},
		{"no terminal", []string{initLine}, StopIncomplete},
	}
	for _, c := range cases {
		_, err := feed(newTestParser(), c.lines...)
		if got := StopCode(err); got != c.want {
			t.Errorf("%s: stop %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStreamBounds(t *testing.T) {
	p := newTestParser()
	if err := p.Feed([]byte(initLine + "\n" + terminal)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Finish(); StopCode(err) != StopIncomplete {
		t.Fatalf("missing final newline: %v", err)
	}
	p = newTestParser()
	big := strings.Repeat("x", MaxEventBytes)
	if err := p.Feed([]byte(`{"type":"user","x":"` + big)); StopCode(err) != StopEventTooLarge {
		t.Fatalf("oversized event: %v", err)
	}
	if err := p.Feed([]byte("\n")); StopCode(err) != StopEventTooLarge {
		t.Fatal("parser continued after error")
	}
	p = newTestParser()
	if err := p.Feed([]byte(initLine + "\n")); err != nil {
		t.Fatal(err)
	}
	status := []byte(`{"type":"system","subtype":"status","status":null}` + "\n")
	var err error
	for i := 1; i < MaxEvents && err == nil; i++ {
		err = p.Feed(status)
	}
	if err != nil {
		t.Fatalf("within event cap: %v", err)
	}
	if err := p.Feed(status); StopCode(err) != StopEventLimit {
		t.Fatalf("event cap: %v", err)
	}
}
