package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// MaxOutput caps the complete JSON or text output before publication.
const MaxOutput = 65536

// Outcomes.
const (
	OutcomeUnverified    = "reviewer_unverified"
	OutcomeFailed        = "reviewer_failed"
	OutcomeChanged       = "candidate_changed"
	OutcomePassed        = "review_passed"
	OutcomeRequiredFixes = "review_required_fixes"
	VerdictPass          = "PASS"
	VerdictRequiredFixes = "REQUIRED_FIXES"
	StateNotStarted      = "not_started"
	StateExited          = "exited"
	StateUnverified      = "unverified"
	recordStateIntent    = "review_intent"
	operationReview      = "run_review"
	notEvaluated         = "not_evaluated"
	receiptStateRecorded = "recorded"
)

// Intent is the durable record published before the reviewer may start.
type Intent struct {
	SchemaVersion          int    `json:"schema_version"`
	RunID                  string `json:"run_id"`
	RecordState            string `json:"record_state"`
	TicketURL              string `json:"ticket_url"`
	ScopeSHA256            string `json:"scope_sha256"`
	PolicyCommit           string `json:"policy_commit"`
	RepositoryObjectFormat string `json:"repository_object_format"`
	RepositoryHead         string `json:"repository_head"`
	CandidateHead          string `json:"candidate_head"`
	PlanSHA256             string `json:"plan_sha256"`
	ExecutionIntentSHA256  string `json:"execution_intent_sha256"`
	ExecutionResultSHA256  string `json:"execution_result_sha256"`
	CreatedAt              string `json:"created_at"`
	Authority              string `json:"authority"`
}

// Reviewer is the observed reviewer program state.
type Reviewer struct {
	State    string `json:"state"`
	ExitCode *int   `json:"exit_code"`
}

// Repository holds the candidate and its accepted post-review observation.
type Repository struct {
	ObjectFormat string  `json:"object_format"`
	BeforeHead   string  `json:"before_head"`
	AfterHead    *string `json:"after_head"`
}

// Result is the durable terminal record and the JSON output.
type Result struct {
	SchemaVersion int        `json:"schema_version"`
	RunID         string     `json:"run_id"`
	Operation     string     `json:"operation"`
	Authority     string     `json:"authority"`
	Readiness     string     `json:"readiness"`
	Outcome       string     `json:"outcome"`
	Reviewer      Reviewer   `json:"reviewer"`
	Verdict       *string    `json:"verdict"`
	Repository    Repository `json:"repository"`
	ReceiptState  string     `json:"receipt_state"`
	CreatedAt     string     `json:"created_at"`
	CompletedAt   string     `json:"completed_at"`
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var (
	runIDRE   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex64RE   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	oid40RE   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	exitRE    = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})$`)
	errStrict = errors.New("invalid json")
)

func oidFor(format, s string) bool {
	switch format {
	case "sha1":
		return oid40RE.MatchString(s)
	case "sha256":
		return hex64RE.MatchString(s)
	}
	return false
}

func exactKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func isOne(v any) bool { n, ok := v.(json.Number); return ok && string(n) == "1" }

func str(v any, valid func(string) bool) bool { s, ok := v.(string); return ok && valid(s) }

// strictObject decodes exactly one UTF-8 JSON object with no duplicate
// decoded member names at any level and only trailing whitespace.
func strictObject(data []byte) (map[string]any, bool) {
	if !utf8.Valid(data) {
		return nil, false
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := strictValue(dec)
	if err != nil {
		return nil, false
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

func strictValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, errStrict
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, errStrict
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errStrict
				}
				if _, dup := m[k]; dup {
					return nil, errStrict
				}
				v, err := strictValue(dec)
				if err != nil {
					return nil, errStrict
				}
				m[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, errStrict
			}
			return m, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := strictValue(dec)
				if err != nil {
					return nil, errStrict
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, errStrict
			}
			return a, nil
		}
		return nil, errStrict
	default:
		return t, nil
	}
}

// ParseReport strictly parses reviewer stdout: one object with exactly
// schema_version literal 1 and verdict PASS or REQUIRED_FIXES.
func ParseReport(data []byte) (string, bool) {
	if len(data) > StdoutCap {
		return "", false
	}
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "verdict") || !isOne(m["schema_version"]) {
		return "", false
	}
	v, _ := m["verdict"].(string)
	if v != VerdictPass && v != VerdictRequiredFixes {
		return "", false
	}
	return v, true
}

// validIntent strictly re-reads serialized review intent bytes.
func validIntent(data []byte, id string) bool {
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "run_id", "record_state", "ticket_url", "scope_sha256",
		"policy_commit", "repository_object_format", "repository_head", "candidate_head", "plan_sha256",
		"execution_intent_sha256", "execution_result_sha256", "created_at", "authority") {
		return false
	}
	format, _ := m["repository_object_format"].(string)
	head := func(s string) bool { return oidFor(format, s) }
	return isOne(m["schema_version"]) && m["run_id"] == id && m["record_state"] == recordStateIntent &&
		str(m["ticket_url"], state.ValidTicketURL) && str(m["scope_sha256"], hex64RE.MatchString) &&
		str(m["policy_commit"], func(s string) bool { return oidFor("sha1", s) || oidFor("sha256", s) }) &&
		str(m["repository_head"], head) && str(m["candidate_head"], head) &&
		str(m["plan_sha256"], hex64RE.MatchString) && str(m["execution_intent_sha256"], hex64RE.MatchString) &&
		str(m["execution_result_sha256"], hex64RE.MatchString) && str(m["created_at"], state.ValidTimestamp) &&
		m["authority"] == notEvaluated
}

// validResult strictly re-reads serialized result bytes: exact key sets,
// types, enums and the outcome/reviewer/verdict/after_head combinations.
func validResult(data []byte, id string) bool {
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "run_id", "operation", "authority", "readiness", "outcome",
		"reviewer", "verdict", "repository", "receipt_state", "created_at", "completed_at") {
		return false
	}
	if !isOne(m["schema_version"]) || m["run_id"] != id || m["operation"] != operationReview ||
		m["authority"] != notEvaluated || m["readiness"] != notEvaluated || m["receipt_state"] != receiptStateRecorded ||
		!str(m["created_at"], state.ValidTimestamp) || !str(m["completed_at"], state.ValidTimestamp) ||
		!notBefore(m["completed_at"].(string), m["created_at"].(string)) {
		return false
	}
	rs, rc, ok1 := reviewer(m["reviewer"])
	repo, ok2 := m["repository"].(map[string]any)
	if !ok1 || !ok2 || !exactKeys(repo, "object_format", "before_head", "after_head") {
		return false
	}
	format, _ := repo["object_format"].(string)
	before, _ := repo["before_head"].(string)
	if !oidFor(format, before) {
		return false
	}
	after := repo["after_head"]
	if after != nil && after != before {
		return false
	}
	verdict := m["verdict"]
	if verdict != nil && verdict != VerdictPass && verdict != VerdictRequiredFixes {
		return false
	}
	e0 := rs == StateExited && rc == 0
	switch m["outcome"] {
	case OutcomeUnverified:
		return (rs != StateExited || e0) && verdict == nil && after == nil
	case OutcomeFailed:
		return rs == StateExited && rc != 0 && verdict == nil && after == nil
	case OutcomeChanged:
		return e0 && verdict != nil && after == nil
	case OutcomePassed:
		return e0 && verdict == VerdictPass && after != nil
	case OutcomeRequiredFixes:
		return e0 && verdict == VerdictRequiredFixes && after != nil
	}
	return false
}

func reviewer(v any) (string, int, bool) {
	m, ok := v.(map[string]any)
	if !ok || !exactKeys(m, "state", "exit_code") {
		return "", 0, false
	}
	s, _ := m["state"].(string)
	switch s {
	case StateExited:
		n, ok := m["exit_code"].(json.Number)
		if !ok || !exitRE.MatchString(string(n)) {
			return "", 0, false
		}
		c, _ := strconv.Atoi(string(n))
		return s, c, c <= 255
	case StateNotStarted, StateUnverified:
		return s, 0, m["exit_code"] == nil
	}
	return "", 0, false
}

// RenderText renders the fixed eight-line terminal report.
func RenderText(r Result) []byte {
	var b strings.Builder
	b.WriteString("BAW review observations\n")
	b.WriteString("Run: " + r.RunID + "\n")
	b.WriteString("Authority: not_evaluated\n")
	b.WriteString("Readiness: not_evaluated\n")
	b.WriteString("Outcome: " + r.Outcome + "\n")
	b.WriteString("Reviewer: state=" + r.Reviewer.State + " exit_code=" + unknown(exitText(r.Reviewer.ExitCode)) +
		" verdict=" + unknown(r.Verdict) + "\n")
	b.WriteString("Repository: object_format=" + r.Repository.ObjectFormat + " before_head=" + r.Repository.BeforeHead +
		" after_head=" + unknown(r.Repository.AfterHead) + "\n")
	b.WriteString("Receipt: recorded\n")
	return []byte(b.String())
}

func exitText(c *int) *string {
	if c == nil {
		return nil
	}
	s := strconv.Itoa(*c)
	return &s
}

func unknown(s *string) string {
	if s == nil {
		return "unknown"
	}
	return *s
}
