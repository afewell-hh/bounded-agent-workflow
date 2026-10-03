package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// MaxDiagnoseOutput caps a rendered diagnose packet or diagnose help.
const MaxDiagnoseOutput = 4096

// deliverBounded writes b only if it fits in max bytes, and returns "" or the
// fixed failure code. An oversized b writes nothing.
func deliverBounded(w io.Writer, b []byte, max int) string {
	if len(b) > max {
		return string(inspect.CodeOutputLimit)
	}
	if write(w, b) != nil {
		return string(state.CodeOutputUnavailable)
	}
	return ""
}

// diagnose is an internal test seam for the storage observation.
var diagnose = (*state.Root).Diagnose

func runDiagnose(root *state.Root, id string, asJSON bool, stdout, stderr io.Writer) int {
	d, err := diagnose(root, id)
	if err != nil {
		return failRecord(stderr, codeOf(err))
	}
	out, err := renderDiagnosis(id, d, asJSON)
	if err != nil {
		return failRecord(stderr, string(inspect.CodeOutputLimit))
	}
	if code := deliverBounded(stdout, out, MaxDiagnoseOutput); code != "" {
		return failRecord(stderr, code)
	}
	return 0
}

type stagingPacket struct {
	Total              int `json:"total"`
	Valid              int `json:"valid"`
	InvalidRecord      int `json:"invalid_record"`
	RecordTooLarge     int `json:"record_too_large"`
	UnsupportedVersion int `json:"unsupported_record_version"`
	LinkedToFinal      int `json:"linked_to_final"`
}

type diagnosisPacket struct {
	SchemaVersion        int           `json:"schema_version"`
	Operation            string        `json:"operation"`
	RunID                string        `json:"run_id"`
	Namespace            string        `json:"namespace"`
	FinalRecord          string        `json:"final_record"`
	Staging              stagingPacket `json:"staging"`
	Snapshot             string        `json:"snapshot"`
	Durability           string        `json:"durability"`
	Authority            string        `json:"authority"`
	RuntimeState         string        `json:"runtime_state"`
	ProcessOwnership     string        `json:"process_ownership"`
	RemoteFreshness      string        `json:"remote_freshness"`
	ReservationOwnership string        `json:"reservation_ownership"`
}

// renderDiagnosis renders fixed labels, enums, counts and the validated ID.
func renderDiagnosis(id string, d state.Diagnosis, asJSON bool) ([]byte, error) {
	ns := "absent"
	if d.NamespacePresent {
		ns = "present"
	}
	s := d.Staging
	if asJSON {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		err := enc.Encode(diagnosisPacket{1, "diagnose", id, ns, d.Final,
			stagingPacket{s.Total, s.Valid, s.InvalidRecord, s.RecordTooLarge, s.UnsupportedVersion, s.LinkedToFinal},
			"non_atomic", "unknown", "not_evaluated", "unknown", "unknown", "unknown", "unknown"})
		return buf.Bytes(), err
	}
	n := strconv.Itoa
	return []byte("BAW run diagnosis\nRun: " + id + "\nNamespace: " + ns + "\nFinal record: " + d.Final +
		"\nStaging: total=" + n(s.Total) + " valid=" + n(s.Valid) + " invalid_record=" + n(s.InvalidRecord) +
		" record_too_large=" + n(s.RecordTooLarge) + " unsupported_record_version=" + n(s.UnsupportedVersion) +
		" linked_to_final=" + n(s.LinkedToFinal) +
		"\nSnapshot: non_atomic\nDurability: unknown\nAuthority: not_evaluated\nRuntime state: unknown" +
		"\nProcess ownership: unknown\nRemote freshness: unknown\nReservation ownership: unknown\n"), nil
}
