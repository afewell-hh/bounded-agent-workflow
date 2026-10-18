package worker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
)

// When the test binary is started with the frozen worker argv and the
// profile's from-scratch environment, it acts as a fake native worker: it
// dumps argv, environment and stdin into its TMPDIR, makes the allowed edit
// in its working directory and emits a valid stream. It is a fixture, not
// evidence of native Claude behaviour.
func init() {
	n := len(os.Args)
	if n > 1 && os.Args[n-1] == "--include-hook-events" && os.Getenv("DISABLE_AUTOUPDATER") == "1" {
		os.Exit(fakeNative())
	}
}

type dump struct {
	Args  []string `json:"args"`
	Env   []string `json:"env"`
	Stdin string   `json:"stdin"`
}

func fakeNative() int {
	in, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 3
	}
	var run, task string
	sc := bufio.NewScanner(bytes.NewReader(in))
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "Run: "); ok && run == "" {
			run = v
		}
		if v, ok := strings.CutPrefix(line, "Task input SHA-256: "); ok && task == "" {
			task = v
		}
	}
	b, _ := json.Marshal(dump{os.Args[1:], os.Environ(), string(in)})
	if os.WriteFile(filepath.Join(os.Getenv("TMPDIR"), "dump.json"), b, 0o600) != nil {
		return 4
	}
	wd, err := os.Getwd()
	if err != nil || os.WriteFile(filepath.Join(wd, "target.txt"), []byte("marker: kestrel\n"), 0o644) != nil {
		return 5
	}
	os.Stdout.WriteString(initLine(wd) + "\n" + terminalLine(run, task, "completed", "none") + "\n")
	return 0
}

func TestWorkerRealChildStdinArgvEnv(t *testing.T) {
	f := newFx(t, "sha256")
	self, err := os.Executable()
	f.ok(err)
	b, err := os.ReadFile(self)
	f.ok(err)
	f.setExecutable(b)
	f.writeInputs()
	var out bytes.Buffer
	completed, err := RunWith(context.Background(), f.request(true), &out, Deps{SourceLstat: hideAdmin})
	if err != nil || !completed {
		t.Fatalf("%v %v %s", err, completed, out.String())
	}
	raw, err := os.ReadFile(filepath.Join(f.attempt(), "worker", "tmp", "dump.json"))
	f.ok(err)
	var d dump
	f.ok(json.Unmarshal(raw, &d))
	if strings.Join(d.Args, "\x00") != strings.Join(claude.Args(f.repo), "\x00") {
		t.Fatalf("argv %q", d.Args)
	}
	wantEnv := claude.Env(claude.Profile{UserHome: f.userHome, UserName: "fixture", Shell: "/bin/zsh"},
		filepath.Join(f.attempt(), "worker", "tmp"))
	if strings.Join(d.Env, "\n") != strings.Join(wantEnv, "\n") {
		t.Fatalf("env %q", d.Env)
	}
	pkt, err := os.ReadFile(filepath.Join(f.attempt(), "packet.txt"))
	f.ok(err)
	if d.Stdin != string(pkt) || d.Stdin != expectedPacket(f) {
		t.Fatal("stdin differs from the published packet")
	}
	if r := decodeResult(t, out.String()); r.Outcome != OutcomeCompleted || r.Worker.State != StateExited {
		t.Fatal(out.String())
	}
}
