package claude

import (
	"bytes"
	"errors"
	"regexp"
)

// Stream bounds.
const (
	MaxEventBytes = 512 << 10
	MaxEvents     = 10000
)

// Fixed parser stop codes beyond the classification table's stop reasons.
// They are retained privately and never published.
const (
	StopMalformed       = "stream_malformed"
	StopEventTooLarge   = "stream_event_too_large"
	StopEventLimit      = "stream_event_limit"
	StopOrder           = "stream_order"
	StopInit            = "native_init_mismatch"
	StopTool            = "native_tool_not_allowed"
	StopCorrelation     = "native_tool_correlation"
	StopUnsolicited     = "native_unsolicited_input"
	StopNativeError     = "native_message_error"
	StopRateLimit       = "native_rate_limit"
	StopReport          = "native_report_invalid"
	StopIncomplete      = "stream_incomplete"
	StopAfterTerminal   = "stream_after_terminal"
	StopCompletionError = stopCompletion
)

// Memory observations.
const (
	MemoryEmptyReported   = "empty_reported"
	MemorySuppressionOnly = "documented_suppression_only"
)

// Report statuses and reasons.
const (
	StatusCompleted    = "completed"
	StatusNotCompleted = "not_completed"
)

// StopError carries one fixed stop code.
type StopError struct{ Code string }

func (e *StopError) Error() string { return e.Code }

func stop(code string) error { return &StopError{Code: code} }

// StopCode returns the fixed stop code of a parser error, or "".
func StopCode(err error) string {
	var e *StopError
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// Expect holds the values a run binds the stream to.
type Expect struct {
	RunID           string
	TaskInputSHA256 string
	Worktree        string
	Plugins         []string // canonical allowed plugin tuples
}

// Summary is the parser's bounded, content-free observation.
type Summary struct {
	InitVerified      bool
	TerminalVerified  bool
	MemoryObservation string
	PluginCount       int
	InformationCounts map[string]int
	Status            string // report status once terminal is verified
	Reason            string
}

// Parser is a deterministic, bounded stream-json parser for one worker run.
// Feed must be called serially (proc calls it from one reader). After the
// first error every later call returns that same error.
type Parser struct {
	exp      Expect
	buf      []byte
	events   int
	err      error
	init     bool
	terminal bool
	calls    map[string]bool // tool_use id -> still open
	sum      Summary
}

// NewParser returns a parser bound to exp.
func NewParser(exp Expect) *Parser {
	return &Parser{exp: exp, calls: map[string]bool{},
		sum: Summary{InformationCounts: map[string]int{}}}
}

// Feed consumes one stdout chunk.
func (p *Parser) Feed(chunk []byte) error {
	if p.err != nil {
		return p.err
	}
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			if len(p.buf)+len(chunk) > MaxEventBytes {
				return p.fail(StopEventTooLarge)
			}
			p.buf = append(p.buf, chunk...)
			return nil
		}
		if len(p.buf)+i > MaxEventBytes {
			return p.fail(StopEventTooLarge)
		}
		line := append(p.buf, chunk[:i]...)
		p.buf = nil
		chunk = chunk[i+1:]
		if err := p.event(line); err != nil {
			return p.fail(StopCode(err))
		}
	}
	return nil
}

// Finish validates the end of the stream: a final newline, one verified init
// and one verified terminal result.
func (p *Parser) Finish() (Summary, error) {
	if p.err != nil {
		return p.sum, p.err
	}
	if len(p.buf) != 0 || !p.init || !p.terminal {
		return p.sum, p.fail(StopIncomplete)
	}
	return p.sum, nil
}

// Err returns the first parser error, if any.
func (p *Parser) Err() error { return p.err }

func (p *Parser) fail(code string) error {
	if p.err == nil {
		p.err = stop(code)
	}
	return p.err
}

func (p *Parser) event(line []byte) error {
	p.events++
	if p.events > MaxEvents {
		return stop(StopEventLimit)
	}
	if p.terminal {
		return stop(StopAfterTerminal)
	}
	m, ok := StrictObject(line)
	if !ok {
		return stop(StopMalformed)
	}
	typ, ok := m["type"].(string)
	if !ok {
		return stop(StopMalformed)
	}
	sub, hasSub := "", false
	if v, present := m["subtype"]; present {
		s, ok := v.(string)
		if !ok {
			return stop(StopMalformed)
		}
		sub, hasSub = s, true
	}
	c, ok := classify(typ, sub, hasSub)
	if !ok {
		return stop(StopUnknown)
	}
	switch c.Action {
	case ActionStop:
		return stop(c.StopReason)
	case ActionInformation:
		if err := information(c, m); err != nil {
			return err
		}
		p.sum.InformationCounts[c.label()]++
		return nil
	}
	switch {
	case typ == "system" && sub == "init":
		return p.initEvent(m)
	case !p.init:
		return stop(StopOrder)
	case typ == "assistant":
		return p.assistant(m)
	case typ == "user":
		return p.user(m)
	case typ == "result":
		return p.result(m, sub)
	}
	return stop(StopUnknown)
}

// information applies the conditions of conditional information rows. A
// rate-limit event is information only when its status is plainly allowed
// and no overage is in use; any other shape stops.
func information(c Classification, m map[string]any) error {
	if c.Type != "rate_limit_event" {
		if c.Type == "system" && c.Subtype == "status" {
			if v, present := m["status"]; !present || (v != nil && v != "compacting" && v != "requesting") {
				return stop(StopMalformed)
			}
			if v, present := m["permissionMode"]; present && v != "dontAsk" {
				return stop(StopInit)
			}
			if m["compact_result"] == "failed" || (m["compact_error"] != nil && m["compact_error"] != false && m["compact_error"] != "") {
				return stop(StopNativeError)
			}
		}
		if c.Type == "system" && c.Subtype == "permission_denied" {
			name, _ := m["tool_name"].(string)
			if !toolAllowed(name) {
				return stop(StopTool)
			}
		}
		return nil
	}
	info, ok := m["rate_limit_info"].(map[string]any)
	if !ok {
		return stop(StopRateLimit)
	}
	if s, _ := info["status"].(string); s != "allowed" && s != "allowed_warning" {
		return stop(StopRateLimit)
	}
	for _, key := range []string{"isUsingOverage", "overageInUse"} {
		if v, present := info[key]; present && v != false {
			return stop(StopRateLimit)
		}
	}
	if info["rateLimitType"] == "overage" {
		return stop(StopRateLimit)
	}
	return nil
}

var allowedTools = []string{"Read", "Glob", "Grep", "Edit", "Write"}

func toolAllowed(name string) bool {
	for _, t := range allowedTools {
		if t == name {
			return true
		}
	}
	return false
}

func emptyArray(v any) bool { a, ok := v.([]any); return ok && len(a) == 0 }

func (p *Parser) initEvent(m map[string]any) error {
	if p.init {
		return stop(StopOrder)
	}
	if m["apiKeySource"] != "none" || m["claude_code_version"] != Version || m["model"] != Model ||
		m["permissionMode"] != "dontAsk" || m["output_style"] != "default" ||
		!emptyArray(m["skills"]) || !emptyArray(m["mcp_servers"]) {
		return stop(StopInit)
	}
	tools, ok := m["tools"].([]any)
	if !ok || len(tools) != len(allowedTools) {
		return stop(StopInit)
	}
	seen := map[string]bool{}
	for _, t := range tools {
		s, ok := t.(string)
		if !ok || !toolAllowed(s) || seen[s] {
			return stop(StopInit)
		}
		seen[s] = true
	}
	allowed := map[string]bool{}
	for _, k := range p.exp.Plugins {
		allowed[k] = true
	}
	plugins, ok := m["plugins"].([]any)
	if !ok {
		return stop(StopInit)
	}
	used := map[string]bool{}
	for _, v := range plugins {
		k, ok := pluginKey(v)
		if !ok || !allowed[k] || used[k] {
			return stop(StopInit)
		}
		used[k] = true
	}
	for _, k := range []string{"plugin_errors", "mcp_errors", "errors", "warnings"} {
		if v, present := m[k]; present && !emptyArray(v) {
			return stop(StopInit)
		}
	}
	if v, present := m["slash_commands"]; present && !emptyArray(v) {
		return stop(StopInit)
	}
	if v, present := m["cwd"]; present && v != p.exp.Worktree {
		return stop(StopInit)
	}
	p.sum.MemoryObservation = MemorySuppressionOnly
	if v, present := m["memory_paths"]; present {
		if !emptyArray(v) {
			return stop(StopInit)
		}
		p.sum.MemoryObservation = MemoryEmptyReported
	}
	p.sum.PluginCount = len(plugins)
	p.init, p.sum.InitVerified = true, true
	return nil
}

// subagent reports whether a message belongs to a nested agent, which the
// worker profile never enables.
func subagent(m map[string]any) bool {
	v, present := m["parent_tool_use_id"]
	return present && v != nil
}

func content(m map[string]any) ([]any, bool) {
	msg, ok := m["message"].(map[string]any)
	if !ok {
		return nil, false
	}
	if v, present := msg["error"]; present && v != nil {
		return nil, false
	}
	blocks, ok := msg["content"].([]any)
	return blocks, ok
}

func (p *Parser) assistant(m map[string]any) error {
	if subagent(m) {
		return stop(StopUnsolicited)
	}
	if v, present := m["error"]; present && v != nil {
		return stop(StopNativeError)
	}
	blocks, ok := content(m)
	if !ok {
		return stop(StopNativeError)
	}
	for _, b := range blocks {
		blk, ok := b.(map[string]any)
		if !ok {
			return stop(StopMalformed)
		}
		switch blk["type"] {
		case "text", "thinking", "redacted_thinking":
		case "tool_use":
			id, _ := blk["id"].(string)
			name, _ := blk["name"].(string)
			if !toolAllowed(name) {
				return stop(StopTool)
			}
			if id == "" || len(id) > 256 {
				return stop(StopCorrelation)
			}
			if _, dup := p.calls[id]; dup {
				return stop(StopCorrelation)
			}
			p.calls[id] = true
		default:
			return stop(StopUnknown)
		}
	}
	return nil
}

func (p *Parser) user(m map[string]any) error {
	if subagent(m) {
		return stop(StopUnsolicited)
	}
	blocks, ok := content(m)
	if !ok || len(blocks) == 0 {
		return stop(StopUnsolicited)
	}
	for _, b := range blocks {
		blk, ok := b.(map[string]any)
		if !ok || blk["type"] != "tool_result" {
			return stop(StopUnsolicited)
		}
		id, _ := blk["tool_use_id"].(string)
		if open, known := p.calls[id]; !known || !open {
			return stop(StopCorrelation)
		}
		p.calls[id] = false
	}
	return nil
}

var reportStatus = map[string]map[string]bool{
	StatusCompleted:    {"none": true},
	StatusNotCompleted: {"blocked": true, "needs_clarification": true, "failed": true},
}

var runIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

func (p *Parser) result(m map[string]any, sub string) error {
	if sub != "success" || m["is_error"] != false {
		return stop(StopCompletionError)
	}
	for _, open := range p.calls {
		if open {
			return stop(StopCorrelation)
		}
	}
	text, ok := m["result"].(string)
	if !ok {
		return stop(StopReport)
	}
	status, reason, ok := ParseReport([]byte(text), p.exp.RunID, p.exp.TaskInputSHA256)
	if !ok {
		return stop(StopReport)
	}
	p.terminal, p.sum.TerminalVerified = true, true
	p.sum.Status, p.sum.Reason = status, reason
	return nil
}

// ParseReport strictly validates a native final report bound to runID and
// taskSHA: exactly one object (surrounding whitespace only) with exact keys,
// and a status/reason pair from the fixed enum.
func ParseReport(data []byte, runID, taskSHA string) (status, reason string, ok bool) {
	m, ok := StrictObject(data)
	if !ok || !ExactKeys(m, "schema_version", "run_id", "task_input_sha256", "status", "reason") || !IsOne(m["schema_version"]) {
		return "", "", false
	}
	id, _ := m["run_id"].(string)
	sha, _ := m["task_input_sha256"].(string)
	status, _ = m["status"].(string)
	reason, _ = m["reason"].(string)
	if !runIDRE.MatchString(id) || id != runID || !hex64RE.MatchString(sha) || sha != taskSHA ||
		!reportStatus[status][reason] {
		return "", "", false
	}
	return status, reason, true
}
