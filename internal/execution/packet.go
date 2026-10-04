package execution

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// MaxOutput caps the complete JSON or text output before delivery.
const MaxOutput = 65536

// Outcomes.
const (
	OutcomeWorkerFailed           = "worker_failed"
	OutcomeWorkerUnverified       = "worker_unverified"
	OutcomeInspectionFailed       = "inspection_failed"
	OutcomeVerificationFailed     = "verification_failed"
	OutcomeVerificationUnverified = "verification_unverified"
	OutcomeVerificationPassed     = "verification_passed"
)

// Program states.
const (
	StateNotStarted = "not_started"
	StateExited     = "exited"
	StateUnverified = "unverified"
)

// Intent is the durable record published before the worker may start.
type Intent struct {
	SchemaVersion          int    `json:"schema_version"`
	RunID                  string `json:"run_id"`
	RecordState            string `json:"record_state"`
	TicketURL              string `json:"ticket_url"`
	ScopeSHA256            string `json:"scope_sha256"`
	PolicyCommit           string `json:"policy_commit"`
	RepositoryObjectFormat string `json:"repository_object_format"`
	RepositoryHead         string `json:"repository_head"`
	PlanSHA256             string `json:"plan_sha256"`
	CreatedAt              string `json:"created_at"`
}

// Program is the observed state of the worker or verification program.
type Program struct {
	State    string `json:"state"`
	ExitCode *int   `json:"exit_code"`
}

// Repository holds the copied initial and post-worker observations.
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
	Worker        Program    `json:"worker"`
	Verification  Program    `json:"verification"`
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
	runIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex64RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	oid40RE = regexp.MustCompile(`^[0-9a-f]{40}$`)
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

// validIntent strictly re-reads serialized intent bytes.
func validIntent(data []byte, id string) bool {
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "run_id", "record_state", "ticket_url", "scope_sha256",
		"policy_commit", "repository_object_format", "repository_head", "plan_sha256", "created_at") {
		return false
	}
	format, _ := m["repository_object_format"].(string)
	return isOne(m["schema_version"]) && m["run_id"] == id && m["record_state"] == "execution_intent" &&
		str(m["ticket_url"], state.ValidTicketURL) && str(m["scope_sha256"], hex64RE.MatchString) &&
		str(m["policy_commit"], func(s string) bool { return oidFor("sha1", s) || oidFor("sha256", s) }) &&
		str(m["repository_head"], func(s string) bool { return oidFor(format, s) }) &&
		str(m["plan_sha256"], hex64RE.MatchString) && str(m["created_at"], state.ValidTimestamp)
}

// validResult strictly re-reads serialized result bytes: exact key sets,
// types, enums and the §7 state/exit/outcome combinations.
func validResult(data []byte, id string) bool {
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "run_id", "operation", "authority", "readiness", "outcome",
		"worker", "verification", "repository", "receipt_state", "created_at", "completed_at") {
		return false
	}
	if !isOne(m["schema_version"]) || m["run_id"] != id || m["operation"] != "run_execute" ||
		m["authority"] != "not_evaluated" || m["readiness"] != "not_evaluated" || m["receipt_state"] != "recorded" ||
		!str(m["created_at"], state.ValidTimestamp) || !str(m["completed_at"], state.ValidTimestamp) {
		return false
	}
	ws, wc, ok1 := program(m["worker"])
	vs, vc, ok2 := program(m["verification"])
	repo, ok3 := m["repository"].(map[string]any)
	if !ok1 || !ok2 || !ok3 || !exactKeys(repo, "object_format", "before_head", "after_head") {
		return false
	}
	format, _ := repo["object_format"].(string)
	if !str(repo["before_head"], func(s string) bool { return oidFor(format, s) }) {
		return false
	}
	after := repo["after_head"]
	if after != nil && !str(after, func(s string) bool { return oidFor(format, s) }) {
		return false
	}
	w0 := ws == StateExited && wc == 0
	switch m["outcome"] {
	case OutcomeWorkerUnverified:
		return ws != StateExited && vs == StateNotStarted && after == nil
	case OutcomeWorkerFailed:
		return ws == StateExited && wc != 0 && vs == StateNotStarted && after == nil
	case OutcomeInspectionFailed:
		return w0 && vs == StateNotStarted && after == nil
	case OutcomeVerificationUnverified:
		return w0 && vs != StateExited
	case OutcomeVerificationFailed:
		return w0 && vs == StateExited && vc != 0
	case OutcomeVerificationPassed:
		return w0 && vs == StateExited && vc == 0
	}
	return false
}

func program(v any) (string, int, bool) {
	m, ok := v.(map[string]any)
	if !ok || !exactKeys(m, "state", "exit_code") {
		return "", 0, false
	}
	s, _ := m["state"].(string)
	switch s {
	case StateExited:
		n, ok := m["exit_code"].(json.Number)
		if !ok || !regexp.MustCompile(`^(0|[1-9][0-9]{0,2})$`).MatchString(string(n)) {
			return "", 0, false
		}
		c, _ := strconv.Atoi(string(n))
		return s, c, c <= 255
	case StateNotStarted, StateUnverified:
		return s, 0, m["exit_code"] == nil
	}
	return "", 0, false
}

func isOne(v any) bool { n, ok := v.(json.Number); return ok && string(n) == "1" }

func str(v any, valid func(string) bool) bool { s, ok := v.(string); return ok && valid(s) }

func strictObject(data []byte) (map[string]any, bool) {
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

// RenderText renders the fixed nine-line terminal report.
func RenderText(r Result) []byte {
	var b strings.Builder
	b.WriteString("BAW execution observations\n")
	b.WriteString("Run: " + r.RunID + "\n")
	b.WriteString("Authority: not_evaluated\n")
	b.WriteString("Readiness: not_evaluated\n")
	b.WriteString("Outcome: " + r.Outcome + "\n")
	b.WriteString("Worker: state=" + r.Worker.State + " exit_code=" + exitText(r.Worker.ExitCode) + "\n")
	b.WriteString("Verification: state=" + r.Verification.State + " exit_code=" + exitText(r.Verification.ExitCode) + "\n")
	after := "unknown"
	if r.Repository.AfterHead != nil {
		after = *r.Repository.AfterHead
	}
	b.WriteString("Repository: object_format=" + r.Repository.ObjectFormat + " before_head=" + r.Repository.BeforeHead +
		" after_head=" + after + "\n")
	b.WriteString("Receipt: recorded\n")
	return []byte(b.String())
}

func exitText(c *int) string {
	if c == nil {
		return "unknown"
	}
	return strconv.Itoa(*c)
}
