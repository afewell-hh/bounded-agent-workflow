package worker

import (
	"errors"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
)

// ReportObservationName is the private companion published beside the
// result of an otherwise fully usable worker_not_completed attempt. Completed
// and unusable attempts never have one.
const ReportObservationName = "report-observation.json"

// MaxReportObservation caps the companion bytes.
const MaxReportObservation = 1024

// ReportObservation is the content-free companion: the model-reported
// noncompletion category bound to the run, the task input and the exact
// serialized result.json bytes (including their trailing newline). The
// reason is what the worker reported, not a verified cause; it grants no
// acceptance, retry, recovery or authority and does not alter the result.
type ReportObservation struct {
	SchemaVersion   int    `json:"schema_version"`
	RunID           string `json:"run_id"`
	TaskInputSHA256 string `json:"task_input_sha256"`
	ResultSHA256    string `json:"result_sha256"`
	Status          string `json:"status"`
	Reason          string `json:"reason"`
}

// nativeReport is the parser's final report pair, returned by launch only
// from its usable worker_not_completed success point.
type nativeReport struct{ status, reason string }

var notCompletedReasons = map[string]bool{"blocked": true, "needs_clarification": true, "failed": true}

var errReportObservation = errors.New("report observation invalid")

// NewReportObservation builds the companion for the exact valid result bytes
// of run runID. It validates the status/reason pair itself rather than
// trusting it, requires a worker_not_completed result and re-validates its
// own serialization against those bytes.
func NewReportObservation(result []byte, runID, status, reason string) ([]byte, error) {
	if status != claude.StatusNotCompleted || !notCompletedReasons[reason] {
		return nil, errReportObservation
	}
	task, ok := notCompletedResult(result, runID)
	if !ok {
		return nil, errReportObservation
	}
	data, err := marshal(ReportObservation{1, runID, task, sum(result), status, reason})
	if err != nil || !ValidReportObservation(data, result, runID) {
		return nil, errReportObservation
	}
	return data, nil
}

// ValidReportObservation strictly re-reads companion bytes against the exact
// result bytes of run runID: at most 1024 bytes holding exactly one JSON
// object (insignificant JSON whitespace permitted), exact keys without
// duplicates, integer 1, the same run ID and task input hash as the result,
// the SHA-256 of the exact result bytes, the literal status not_completed and
// one of the three reasons. The result must itself be a valid
// worker_not_completed result. A missing (nil) result, as for an orphan
// companion, is never valid.
func ValidReportObservation(data, result []byte, runID string) bool {
	if len(data) > MaxReportObservation {
		return false
	}
	m, ok := claude.StrictObject(data)
	if !ok || !claude.ExactKeys(m, "schema_version", "run_id", "task_input_sha256", "result_sha256", "status", "reason") ||
		!claude.IsOne(m["schema_version"]) {
		return false
	}
	task, ok := notCompletedResult(result, runID)
	return ok && m["run_id"] == runID && hex64(m["task_input_sha256"]) && m["task_input_sha256"] == task &&
		hex64(m["result_sha256"]) && m["result_sha256"] == sum(result) && m["status"] == claude.StatusNotCompleted &&
		str(m["reason"], func(s string) bool { return notCompletedReasons[s] })
}

// notCompletedResult returns the task input hash of a valid
// worker_not_completed result for runID.
func notCompletedResult(result []byte, runID string) (string, bool) {
	if !ValidResult(result, runID) {
		return "", false
	}
	m, _ := claude.StrictObject(result)
	ib, _ := m["input_binding"].(map[string]any)
	task, _ := ib["task_input_sha256"].(string)
	return task, m["outcome"] == OutcomeNotCompleted
}
