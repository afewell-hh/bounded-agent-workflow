package worker

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// MaxOutput caps the saved result JSON and the delivered JSON or text.
const MaxOutput = 65536

// Outcomes, states and fixed literals.
const (
	OutcomeCompleted    = "worker_completed_unverified"
	OutcomeNotCompleted = "worker_not_completed"
	OutcomeUnusable     = "worker_unusable"

	StateNotStarted = "not_started"
	StateExited     = "exited"
	StateUnverified = "unverified"

	operation           = "run_worker"
	notEvaluated        = "not_evaluated"
	recordStateIntent   = "worker_intent"
	receiptRecorded     = "recorded"
	instructionDelivery = "embedded_full_documents"
	coverage            = "complete_enumerated_metadata"
	suppressionBasis    = "documented_mechanisms_and_exposed_native_fields"
)

// Intent is the durable record published before the worker may start.
type Intent struct {
	SchemaVersion             int    `json:"schema_version"`
	RunID                     string `json:"run_id"`
	RecordState               string `json:"record_state"`
	TicketURL                 string `json:"ticket_url"`
	ScopeSHA256               string `json:"scope_sha256"`
	PolicyCommit              string `json:"policy_commit"`
	RepositoryObjectFormat    string `json:"repository_object_format"`
	RepositoryHead            string `json:"repository_head"`
	WorktreeDevice            string `json:"worktree_device"`
	WorktreeInode             string `json:"worktree_inode"`
	PlanSHA256                string `json:"plan_sha256"`
	TaskInputSHA256           string `json:"task_input_sha256"`
	InstructionManifestSHA256 string `json:"instruction_manifest_sha256"`
	AssembledPacketSHA256     string `json:"assembled_packet_sha256"`
	SuppliedInputsSHA256      string `json:"supplied_inputs_sha256"`
	ProfileSHA256             string `json:"profile_sha256"`
	ExecutableSHA256          string `json:"executable_sha256"`
	CLIVersion                string `json:"cli_version"`
	SourceManifestSHA256      string `json:"source_manifest_sha256"`
	InjectionDisposition      string `json:"injection_disposition"`
	Authority                 string `json:"authority"`
	Readiness                 string `json:"readiness"`
	CreatedAt                 string `json:"created_at"`
}

// Worker is the observed native worker process state.
type Worker struct {
	State    string `json:"state"`
	ExitCode *int   `json:"exit_code"`
}

// InputBinding holds the input identities bound before start.
type InputBinding struct {
	TaskInputSHA256           string `json:"task_input_sha256"`
	InstructionManifestSHA256 string `json:"instruction_manifest_sha256"`
	AssembledPacketSHA256     string `json:"assembled_packet_sha256"`
	ProfileSHA256             string `json:"profile_sha256"`
	ExecutableSHA256          string `json:"executable_sha256"`
}

// Repository holds the before and accepted after observations.
type Repository struct {
	ObjectFormat         string  `json:"object_format"`
	BeforeHead           string  `json:"before_head"`
	AfterHead            *string `json:"after_head"`
	ChangeManifestSHA256 *string `json:"change_manifest_sha256"`
}

// StreamSummary is the content-free native stream observation.
type StreamSummary struct {
	InitVerified      bool           `json:"init_verified"`
	TerminalVerified  bool           `json:"terminal_verified"`
	MemoryObservation string         `json:"memory_observation"`
	PluginCount       *int           `json:"plugin_count"`
	InformationCounts map[string]int `json:"information_counts"`
}

// SourceObservation names the finite source matrix coverage. It is not a
// complete loaded-source inventory.
type SourceObservation struct {
	MatrixID         string `json:"matrix_id"`
	ManifestSHA256   string `json:"manifest_sha256"`
	Coverage         string `json:"coverage"`
	SuppressionBasis string `json:"suppression_basis"`
}

// Result is the durable terminal record and the JSON output.
type Result struct {
	SchemaVersion        int               `json:"schema_version"`
	RunID                string            `json:"run_id"`
	Operation            string            `json:"operation"`
	Authority            string            `json:"authority"`
	Readiness            string            `json:"readiness"`
	Outcome              string            `json:"outcome"`
	Worker               Worker            `json:"worker"`
	InputBinding         InputBinding      `json:"input_binding"`
	Repository           Repository        `json:"repository"`
	InstructionCount     int               `json:"instruction_count"`
	InstructionDelivery  string            `json:"instruction_delivery"`
	SourceObservation    SourceObservation `json:"source_observation"`
	StreamSummary        StreamSummary     `json:"stream_summary"`
	InjectionDisposition string            `json:"injection_disposition"`
	ReceiptState         string            `json:"receipt_state"`
	CreatedAt            string            `json:"created_at"`
	CompletedAt          string            `json:"completed_at"`
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
	exitRE  = regexp.MustCompile(`^(0|[1-9][0-9]{0,2})$`)
	labelRE = regexp.MustCompile(`^[a-z_]+(/[a-z_]+)?$`)
	digitRE = regexp.MustCompile(`^(0|[1-9][0-9]{0,19})$`)
)

func str(v any, valid func(string) bool) bool { s, ok := v.(string); return ok && valid(s) }

func hex64(v any) bool { return str(v, hex64RE.MatchString) }

func notBefore(a, b string) bool {
	ta, err1 := time.Parse(state.TimeLayout, a)
	tb, err2 := time.Parse(state.TimeLayout, b)
	return err1 == nil && err2 == nil && !ta.Before(tb)
}

// ValidIntent strictly re-reads serialized intent bytes for id.
func ValidIntent(data []byte, id string) bool {
	m, ok := claude.StrictObject(data)
	if !ok || !claude.ExactKeys(m, "schema_version", "run_id", "record_state", "ticket_url", "scope_sha256",
		"policy_commit", "repository_object_format", "repository_head", "worktree_device", "worktree_inode",
		"plan_sha256", "task_input_sha256", "instruction_manifest_sha256", "assembled_packet_sha256",
		"supplied_inputs_sha256", "profile_sha256", "executable_sha256", "cli_version", "source_manifest_sha256",
		"injection_disposition", "authority", "readiness", "created_at") {
		return false
	}
	format, _ := m["repository_object_format"].(string)
	for _, k := range []string{"scope_sha256", "plan_sha256", "task_input_sha256", "instruction_manifest_sha256",
		"assembled_packet_sha256", "supplied_inputs_sha256", "profile_sha256", "executable_sha256", "source_manifest_sha256"} {
		if !hex64(m[k]) {
			return false
		}
	}
	return claude.IsOne(m["schema_version"]) && runIDRE.MatchString(id) && m["run_id"] == id &&
		m["record_state"] == recordStateIntent && str(m["ticket_url"], state.ValidTicketURL) &&
		str(m["policy_commit"], func(s string) bool { return oidFor("sha1", s) || oidFor("sha256", s) }) &&
		str(m["repository_head"], func(s string) bool { return oidFor(format, s) }) &&
		str(m["worktree_device"], digitRE.MatchString) && str(m["worktree_inode"], digitRE.MatchString) &&
		m["cli_version"] == claude.Version && m["injection_disposition"] == claude.InjectionDisposition &&
		m["authority"] == notEvaluated && m["readiness"] == notEvaluated && str(m["created_at"], state.ValidTimestamp)
}

// ValidResult strictly re-reads serialized result bytes: exact key sets,
// types, literals and the outcome/worker/stream/repository combinations.
func ValidResult(data []byte, id string) bool {
	m, ok := claude.StrictObject(data)
	if !ok || !claude.ExactKeys(m, "schema_version", "run_id", "operation", "authority", "readiness", "outcome",
		"worker", "input_binding", "repository", "instruction_count", "instruction_delivery", "source_observation",
		"stream_summary", "injection_disposition", "receipt_state", "created_at", "completed_at") {
		return false
	}
	if !claude.IsOne(m["schema_version"]) || !runIDRE.MatchString(id) || m["run_id"] != id || m["operation"] != operation ||
		m["authority"] != notEvaluated || m["readiness"] != notEvaluated || m["receipt_state"] != receiptRecorded ||
		m["instruction_delivery"] != instructionDelivery || m["injection_disposition"] != claude.InjectionDisposition ||
		!str(m["created_at"], state.ValidTimestamp) || !str(m["completed_at"], state.ValidTimestamp) ||
		!notBefore(m["completed_at"].(string), m["created_at"].(string)) {
		return false
	}
	n, ok := m["instruction_count"].(json.Number)
	if c, err := strconv.Atoi(string(n)); !ok || err != nil || c < 1 || c > MaxInstructions || string(n) != strconv.Itoa(c) {
		return false
	}
	ib, ok := m["input_binding"].(map[string]any)
	if !ok || !claude.ExactKeys(ib, "task_input_sha256", "instruction_manifest_sha256", "assembled_packet_sha256",
		"profile_sha256", "executable_sha256") {
		return false
	}
	for _, v := range ib {
		if !hex64(v) {
			return false
		}
	}
	so, ok := m["source_observation"].(map[string]any)
	if !ok || !claude.ExactKeys(so, "matrix_id", "manifest_sha256", "coverage", "suppression_basis") ||
		so["matrix_id"] != claude.SourceMatrixID || !hex64(so["manifest_sha256"]) || so["coverage"] != coverage ||
		so["suppression_basis"] != suppressionBasis {
		return false
	}
	ws, wc, ok := workerState(m["worker"])
	if !ok {
		return false
	}
	init, terminal, ok := streamSummary(m["stream_summary"])
	if !ok {
		return false
	}
	repo, ok := m["repository"].(map[string]any)
	if !ok || !claude.ExactKeys(repo, "object_format", "before_head", "after_head", "change_manifest_sha256") {
		return false
	}
	format, _ := repo["object_format"].(string)
	before, _ := repo["before_head"].(string)
	after, change := repo["after_head"], repo["change_manifest_sha256"]
	if !oidFor(format, before) || (after != nil && after != before) || (change != nil && !hex64(change)) {
		return false
	}
	switch m["outcome"] {
	case OutcomeCompleted, OutcomeNotCompleted:
		return ws == StateExited && wc == 0 && init && terminal && after != nil && change != nil
	case OutcomeUnusable:
		return !(ws == StateNotStarted && (init || terminal || after != nil || change != nil))
	}
	return false
}

func workerState(v any) (string, int, bool) {
	m, ok := v.(map[string]any)
	if !ok || !claude.ExactKeys(m, "state", "exit_code") {
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

func streamSummary(v any) (init, terminal, ok bool) {
	m, ok := v.(map[string]any)
	if !ok || !claude.ExactKeys(m, "init_verified", "terminal_verified", "memory_observation", "plugin_count", "information_counts") {
		return false, false, false
	}
	init, ok1 := m["init_verified"].(bool)
	terminal, ok2 := m["terminal_verified"].(bool)
	if !ok1 || !ok2 || (terminal && !init) {
		return false, false, false
	}
	switch m["memory_observation"] {
	case claude.MemoryEmptyReported, claude.MemorySuppressionOnly:
		if !init {
			return false, false, false
		}
	case notEvaluated:
		if init {
			return false, false, false
		}
	default:
		return false, false, false
	}
	if pc := m["plugin_count"]; pc == nil {
		if init {
			return false, false, false
		}
	} else if n, ok := pc.(json.Number); !ok || !exitRE.MatchString(string(n)) || !init {
		return false, false, false
	}
	counts, ok := m["information_counts"].(map[string]any)
	if !ok {
		return false, false, false
	}
	for k, v := range counts {
		n, ok := v.(json.Number)
		if !labelRE.MatchString(k) || !ok || !digitRE.MatchString(string(n)) || string(n) == "0" {
			return false, false, false
		}
	}
	return init, terminal, true
}

// RenderText renders the fixed terminal report: run, outcome, worker exit,
// input hashes, authority, readiness and receipt only.
func RenderText(r Result) []byte {
	var b strings.Builder
	b.WriteString("BAW worker observations\n")
	b.WriteString("Run: " + r.RunID + "\n")
	b.WriteString("Authority: not_evaluated\n")
	b.WriteString("Readiness: not_evaluated\n")
	b.WriteString("Outcome: " + r.Outcome + "\n")
	exit := "unknown"
	if r.Worker.ExitCode != nil {
		exit = strconv.Itoa(*r.Worker.ExitCode)
	}
	b.WriteString("Worker: state=" + r.Worker.State + " exit_code=" + exit + "\n")
	ib := r.InputBinding
	b.WriteString("Inputs: task_input_sha256=" + ib.TaskInputSHA256 + " instruction_manifest_sha256=" +
		ib.InstructionManifestSHA256 + " assembled_packet_sha256=" + ib.AssembledPacketSHA256 + "\n")
	b.WriteString("Profile: profile_sha256=" + ib.ProfileSHA256 + " executable_sha256=" + ib.ExecutableSHA256 + "\n")
	b.WriteString("Sources: matrix_id=" + r.SourceObservation.MatrixID + " manifest_sha256=" +
		r.SourceObservation.ManifestSHA256 + " coverage=" + r.SourceObservation.Coverage + "\n")
	b.WriteString("Receipt: recorded\n")
	return []byte(b.String())
}
