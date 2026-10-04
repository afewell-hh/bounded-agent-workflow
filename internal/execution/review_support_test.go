package execution

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The forwarders must give exactly the unexported validators' answers.
func TestReviewSupportForwarders(t *testing.T) {
	id := strings.Repeat("a", 32)
	other := strings.Repeat("b", 32)
	intent := `{"schema_version":1,"run_id":"` + id + `","record_state":"execution_intent",` +
		`"ticket_url":"https://github.com/o/r/issues/1","scope_sha256":"` + strings.Repeat("c", 64) + `",` +
		`"policy_commit":"` + strings.Repeat("d", 40) + `","repository_object_format":"sha1",` +
		`"repository_head":"` + strings.Repeat("e", 40) + `","plan_sha256":"` + strings.Repeat("f", 64) + `",` +
		`"created_at":"2026-10-04T00:00:00Z"}`
	if !ValidIntentReceipt([]byte(intent), id) || ValidIntentReceipt([]byte(intent), other) {
		t.Fatal("intent forwarder")
	}
	result := `{"schema_version":1,"run_id":"` + id + `","operation":"run_execute","authority":"not_evaluated",` +
		`"readiness":"not_evaluated","outcome":"verification_passed","worker":{"state":"exited","exit_code":0},` +
		`"verification":{"state":"exited","exit_code":0},"repository":{"object_format":"sha1","before_head":"` +
		strings.Repeat("e", 40) + `","after_head":"` + strings.Repeat("e", 40) + `"},"receipt_state":"recorded",` +
		`"created_at":"2026-10-04T00:00:00Z","completed_at":"2026-10-04T00:00:01Z"}`
	if !ValidResultReceipt([]byte(result), id) || ValidResultReceipt([]byte(result), other) ||
		ValidResultReceipt([]byte(strings.Replace(result, `"exit_code":0},"repo`, `"exit_code":1},"repo`, 1)), id) {
		t.Fatal("result forwarder")
	}
	var v any
	d := json.NewDecoder(strings.NewReader(`{"executable":"/bin/true","arguments":["x"],"timeout_seconds":300}`))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if c, ok := PlanCommand(v); !ok || c.Executable != "/bin/true" || c.TimeoutSeconds != 300 || len(c.Arguments) != 1 {
		t.Fatalf("command forwarder %+v %v", c, ok)
	}
	if _, ok := PlanCommand(map[string]any{"executable": "rel", "arguments": []any{}, "timeout_seconds": json.Number("1")}); ok {
		t.Fatal("relative executable accepted")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "tool")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.Chmod(exe, 0o700)
	if _, err := CheckExecutable(exe); err != nil {
		t.Fatalf("executable: %v", err)
	}
	os.Chmod(exe, 0o722)
	if _, err := CheckExecutable(exe); !IsCode(err, CodeExecUnavailable) {
		t.Fatalf("group-writable executable: %v", err)
	}
}
