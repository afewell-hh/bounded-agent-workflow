// Package state stores and reads immutable local run records under an
// explicit, caller-owned private state directory.
//
// A record saves caller-supplied references (ticket URL, scope hash, policy
// commit) and one observed committed HEAD. It is not an approval, a proof of
// completed work, or a statement about any live process or remote state.
package state

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
)

// Code is a fixed, safe failure code published as `baw: CODE`.
type Code string

const (
	CodeRepositoryUnborn   Code = "repository_unborn"
	CodeStateUnavailable   Code = "state_unavailable"
	CodeStatePermissions   Code = "state_permissions"
	CodeUnsafeStatePath    Code = "unsafe_state_path"
	CodeRecordExists       Code = "record_exists"
	CodeRecordMissing      Code = "record_missing"
	CodeRecordUnavailable  Code = "record_unavailable"
	CodeRecordTooLarge     Code = "record_too_large"
	CodeUnsupportedVersion Code = "unsupported_record_version"
	CodeInvalidRecord      Code = "invalid_record"
	CodeDurability         Code = "durability_unavailable"
	CodeCommitUncertain    Code = "commit_uncertain"
	CodeOutputUnavailable  Code = "output_unavailable"
	CodeStateChanged       Code = "state_changed"
	CodeStateScanLimit     Code = "state_scan_limit"
)

// Error carries only a fixed code; no paths, values or OS error text.
type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }

func fail(c Code) error { return &Error{Code: c} }

// MaxRecordBytes caps a stored record file.
const MaxRecordBytes = 16384

// TimeLayout is the only accepted created_at form.
const TimeLayout = "2006-01-02T15:04:05Z"

// Record is schema version 1 of a run record.
type Record struct {
	SchemaVersion          int    `json:"schema_version"`
	RunID                  string `json:"run_id"`
	RecordState            string `json:"record_state"`
	TicketURL              string `json:"ticket_url"`
	ScopeSHA256            string `json:"scope_sha256"`
	PolicyCommit           string `json:"policy_commit"`
	RepositoryObjectFormat string `json:"repository_object_format"`
	RepositoryHead         string `json:"repository_head"`
	CreatedAt              string `json:"created_at"`
}

var (
	runIDRE    = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex64RE    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	oidRE      = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	ticketRE   = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/issues/([^/]+)$`)
	timeRE     = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	versionRE  = regexp.MustCompile(`^[1-9][0-9]*$`)
	recordKeys = []string{"schema_version", "run_id", "record_state", "ticket_url", "scope_sha256",
		"policy_commit", "repository_object_format", "repository_head", "created_at"}
)

// ValidRunID reports whether id is exactly 32 lowercase hexadecimal characters.
func ValidRunID(id string) bool { return runIDRE.MatchString(id) }

// ValidTicketURL reports whether u is a canonical GitHub issue URL.
func ValidTicketURL(u string) bool {
	m := ticketRE.FindStringSubmatch(u)
	if m == nil || !inspect.ValidOwnerRepo(m[1], m[2]) {
		return false
	}
	_, ok := inspect.ParseIssueNumber(m[3])
	return ok
}

// ValidTimestamp reports whether s is a real UTC calendar time in TimeLayout.
func ValidTimestamp(s string) bool {
	if !timeRE.MatchString(s) {
		return false
	}
	t, err := time.Parse(TimeLayout, s)
	return err == nil && t.Format(TimeLayout) == s
}

func formatWidth(f string) int {
	switch f {
	case "sha1":
		return 40
	case "sha256":
		return 64
	}
	return 0
}

// valid checks every field value of a version 1 record named id.
func (r *Record) valid(id string) bool {
	w := formatWidth(r.RepositoryObjectFormat)
	return r.SchemaVersion == 1 && r.RunID == id && ValidRunID(r.RunID) &&
		r.RecordState == "recorded" && ValidTicketURL(r.TicketURL) &&
		hex64RE.MatchString(r.ScopeSHA256) && w != 0 &&
		oidRE.MatchString(r.PolicyCommit) && len(r.PolicyCommit) == w &&
		oidRE.MatchString(r.RepositoryHead) && len(r.RepositoryHead) == w &&
		ValidTimestamp(r.CreatedAt)
}

// Parse strictly validates data as the record stored for id. Precedence:
// UTF-8, single object, unique decoded top-level names and no trailing value;
// then a lexical positive schema_version; then version 1's exact nine fields.
func Parse(data []byte, id string) (Record, error) {
	var r Record
	bad := fail(CodeInvalidRecord)
	if len(data) > MaxRecordBytes {
		return r, fail(CodeRecordTooLarge)
	}
	if !utf8.Valid(data) {
		return r, bad
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return r, bad
	}
	fields := map[string]json.RawMessage{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return r, bad
		}
		key, ok := tok.(string)
		if !ok {
			return r, bad
		}
		if _, dup := fields[key]; dup {
			return r, bad
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return r, bad
		}
		fields[key] = raw
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return r, bad
	}
	if _, err := dec.Token(); err != io.EOF {
		return r, bad
	}
	v, ok := fields["schema_version"]
	if !ok || !versionRE.Match(v) {
		return r, bad
	}
	if string(v) != "1" {
		return r, fail(CodeUnsupportedVersion)
	}
	if len(fields) != len(recordKeys) {
		return r, bad
	}
	strs := map[string]*string{
		"run_id": &r.RunID, "record_state": &r.RecordState, "ticket_url": &r.TicketURL,
		"scope_sha256": &r.ScopeSHA256, "policy_commit": &r.PolicyCommit,
		"repository_object_format": &r.RepositoryObjectFormat,
		"repository_head":          &r.RepositoryHead, "created_at": &r.CreatedAt,
	}
	for _, k := range recordKeys[1:] {
		raw, ok := fields[k]
		if !ok || len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, strs[k]) != nil {
			return Record{}, bad
		}
	}
	r.SchemaVersion = 1
	if !r.valid(id) {
		return Record{}, bad
	}
	return r, nil
}

// Marshal renders a validated record as stored bytes: one object and newline.
func Marshal(r Record) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
