package cli

import (
	"bytes"
	"path/filepath"
	"runtime"
	"testing"
)

// Minimal routing and syntax checks for `baw run worker`; behaviour is tested
// in internal/worker.
func TestWorkerRoutingAndSyntax(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"run", "worker", "--help"}, &out, &errb); code != 0 || out.String() != WorkerUsage || errb.Len() != 0 {
		t.Fatalf("help: %d %q %q", code, out.String(), errb.String())
	}
	id := "0123456789abcdef0123456789abcdef"
	bad := [][]string{
		{"run", "worker"},
		{"run", "worker", "--repo", "r", "--state-dir", "s", "--run-id", id},
		{"run", "worker", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "extra"},
		{"run", "worker", "--repo", "r", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p"},
		{"run", "worker", "--repo", "r", "--state-dir", "s", "--run-id", "ABC", "--plan", "p"},
		{"run", "worker", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "--json=yes"},
		{"run", "worker", "--repo", "r", "--state-dir", "s", "--run-id", id, "--plan", "p", "--model", "x"},
		{"run", "worker", "--repo", "", "--state-dir", "s", "--run-id", id, "--plan", "p"},
		{"run", "worker", "--repo", "r\x00", "--state-dir", "s", "--run-id", id, "--plan", "p"},
		{"run", "worker", "--help", "--json"},
	}
	for _, args := range bad {
		out.Reset()
		errb.Reset()
		if code := Run(args, &out, &errb); code != 2 || out.Len() != 0 || errb.String() != "baw: invalid_usage\n" {
			t.Errorf("%q: %d %q %q", args, code, out.String(), errb.String())
		}
	}
	// Valid syntax reaches the operation: a missing state directory fails
	// with the existing state code (or the platform gate elsewhere).
	out.Reset()
	errb.Reset()
	missing := filepath.Join(t.TempDir(), "absent")
	code := Run([]string{"run", "worker", "--repo", t.TempDir(), "--state-dir", missing, "--run-id", id, "--plan", missing, "--json"}, &out, &errb)
	want := "baw: state_unavailable\n"
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		want = "baw: profile_unavailable\n"
	}
	if code != 1 || out.Len() != 0 || errb.String() != want {
		t.Fatalf("routed: %d %q %q", code, out.String(), errb.String())
	}
}
