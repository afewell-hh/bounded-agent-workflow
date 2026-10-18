package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
)

// Assignment is the fixed role line at the start of every packet.
const Assignment = "worker; only this task; no tests/external actions"

// PacketInput holds everything framed into one packet.
type PacketInput struct {
	RunID              string
	TaskInputSHA256    string
	BaseCommit         string
	ScopeSHA256        string
	ProfileSHA256      string
	InstructionsSHA256 string
	Instructions       []Instruction
	Bodies             [][]byte // full instruction bytes, in Instructions order
	Task               string
	AllowedChanges     []string
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// InstructionManifestSHA256 is the SHA-256 of the canonical ordered
// instruction manifest (path, blob_oid, sha256 per entry).
func InstructionManifestSHA256(ins []Instruction) string {
	v := make([]any, len(ins))
	for i, e := range ins {
		v[i] = map[string]any{"path": e.Path, "blob_oid": e.BlobOID, "sha256": e.SHA256}
	}
	b, _ := claude.Canonical(v)
	return sum(b)
}

// AssemblePacket frames the fixed assignment, references, every instruction
// body in order with its path/blob/hash/size framing, the task, the literal
// allowed paths and the report schema. Nothing is truncated: a packet over
// MaxPacketBytes is packet_unavailable.
func AssemblePacket(in PacketInput) ([]byte, error) {
	var b strings.Builder
	n := strconv.Itoa(len(in.Instructions))
	b.WriteString("BAW worker packet v1\n")
	b.WriteString("Assignment: " + Assignment + "\n")
	b.WriteString("Run: " + in.RunID + "\n")
	b.WriteString("Task input SHA-256: " + in.TaskInputSHA256 + "\n")
	b.WriteString("Base commit: " + in.BaseCommit + "\n")
	b.WriteString("Scope SHA-256: " + in.ScopeSHA256 + "\n")
	b.WriteString("Profile: " + claude.ProfileID + " sha256=" + in.ProfileSHA256 + "\n")
	b.WriteString("Instruction manifest SHA-256: " + in.InstructionsSHA256 + "\n")
	b.WriteString("Instructions: the " + n + " documents below are the complete project instructions for this task, in order. " +
		"No other instruction file is loaded automatically. Do not edit any of them.\n")
	for i, e := range in.Instructions {
		k := strconv.Itoa(i + 1)
		b.WriteString("=== BEGIN INSTRUCTION " + k + "/" + n + " path=" + e.Path + " blob=" + e.BlobOID +
			" sha256=" + e.SHA256 + " bytes=" + strconv.Itoa(len(in.Bodies[i])) + " ===\n")
		b.Write(in.Bodies[i])
		b.WriteString("\n=== END INSTRUCTION " + k + "/" + n + " ===\n")
	}
	b.WriteString("=== BEGIN TASK bytes=" + strconv.Itoa(len(in.Task)) + " ===\n")
	b.WriteString(in.Task)
	b.WriteString("\n=== END TASK ===\n")
	b.WriteString("Allowed changes: only these literal repository-relative files may be created or modified; change nothing else.\n")
	for _, p := range in.AllowedChanges {
		b.WriteString("- " + p + "\n")
	}
	b.WriteString("Do not run tests, commands or external actions. Do not commit.\n")
	b.WriteString("Final report: your final message must be exactly one JSON object and nothing else (no prose, no code fence):\n")
	b.WriteString(`{"schema_version":1,"run_id":"` + in.RunID + `","task_input_sha256":"` + in.TaskInputSHA256 +
		`","status":"STATUS","reason":"REASON"}` + "\n")
	b.WriteString("STATUS is completed or not_completed. completed requires REASON none; " +
		"not_completed requires REASON blocked, needs_clarification or failed.\n")
	if b.Len() > MaxPacketBytes {
		return nil, fail(CodePacketUnavailable)
	}
	return []byte(b.String()), nil
}
