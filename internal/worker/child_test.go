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
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
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
	if err != nil {
		return 5
	}
	// A task text "FAKE-MODE <mode>" selects a misbehaving fake; it makes no
	// edit. The packet frames the task text on its own line.
	for _, mode := range []string{"hang", "cap", "events", "event-size"} {
		if strings.Contains(string(in), "\nFAKE-MODE "+mode+"\n") {
			return fakeMisbehave(mode, wd, run, task)
		}
	}
	if os.WriteFile(filepath.Join(wd, "target.txt"), []byte("marker: kestrel\n"), 0o644) != nil {
		return 5
	}
	os.Stdout.WriteString(initLine(wd) + "\n" + terminalLine(run, task, "completed", "none") + "\n")
	return 0
}

// statusLine is a known harmless information event padded to n bytes of
// payload.
func statusLine(n int) string {
	return `{"type":"system","subtype":"status","status":null,"pad":"` + strings.Repeat("a", n) + `"}`
}

// fakeMisbehave emits a valid init and then: hang (never exits, like a
// prompt waiting for input), cap (17 valid ~500 KiB events, over the real
// 8 MiB stdout cap), events (10001 valid events, over the real event limit)
// or event-size (one event over the real 512 KiB event bound).
func fakeMisbehave(mode, wd, run, task string) int {
	os.Stdout.WriteString(initLine(wd) + "\n")
	switch mode {
	case "hang":
		time.Sleep(10 * time.Minute)
		return 0
	case "cap":
		for i := 0; i < 17; i++ {
			os.Stdout.WriteString(statusLine(500<<10) + "\n")
		}
	case "events":
		var b strings.Builder
		for i := 0; i < 10001; i++ {
			b.WriteString(statusLine(0) + "\n")
		}
		os.Stdout.WriteString(b.String())
	case "event-size":
		os.Stdout.WriteString(statusLine(512<<10) + "\n")
	}
	os.Stdout.WriteString(terminalLine(run, task, "completed", "none") + "\n")
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

// Real compiled fake children through the real runner: a prompt hang (the
// per-call seam shortens only the 300 s deadline), cancellation once the
// child is running, and the real 8 MiB stdout cap, 10000-event limit and
// 512 KiB event bound. None is completion, every one records an unusable
// attempt with no change manifest, and the attempt is never replayed.
func TestWorkerRealChildBounds(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	selfBytes, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, mode string
		deadline   time.Duration
		cancel     bool
	}{
		{"prompt hang", "hang", 1500 * time.Millisecond, false},
		{"cancelled while running", "hang", 0, true},
		{"stdout cap", "cap", 0, false},
		{"event limit", "events", 0, false},
		{"event size", "event-size", 0, false},
	}
	for _, c := range cases {
		f := newFx(t, "sha1")
		f.setExecutable(selfBytes)
		f.task["task"] = "FAKE-MODE " + c.mode
		f.writeInputs()
		ctx, cancel := context.WithCancel(context.Background())
		var spec proc.Spec
		d := Deps{SourceLstat: hideAdmin}
		d.Run = func(rctx context.Context, s proc.Spec, pkt *os.File, observe func([]byte) error) ([]byte, proc.Observation, error) {
			spec = s
			if c.deadline > 0 {
				s.Timeout = c.deadline
			}
			obs := observe
			if c.cancel {
				var firstLine []byte
				obs = func(b []byte) error {
					err := observe(b)
					// Pipes may split the first write. Cancel only after the
					// fake child's complete init line has actually been observed.
					if len(firstLine) < 4096 {
						firstLine = append(firstLine, b...)
					}
					if bytes.IndexByte(firstLine, '\n') >= 0 {
						cancel()
					}
					return err
				}
			}
			return proc.RunObservedInput(rctx, s, pkt, obs)
		}
		var out bytes.Buffer
		start := time.Now()
		completed, err := RunWith(ctx, f.request(true), &out, d)
		elapsed := time.Since(start)
		cancel()
		if err != nil || completed {
			t.Fatalf("%s: %v %v", c.name, err, completed)
		}
		r := decodeResult(t, out.String())
		if r.Outcome != OutcomeUnusable || r.Worker.State != StateUnverified || r.Worker.ExitCode != nil ||
			!r.StreamSummary.InitVerified || r.StreamSummary.TerminalVerified ||
			r.Repository.AfterHead != nil || r.Repository.ChangeManifestSHA256 != nil {
			t.Errorf("%s: %s", c.name, out.String())
		}
		if spec.Timeout != 300*time.Second || spec.StdoutCap != 8<<20 || spec.StderrCap != 1<<20 {
			t.Errorf("%s: spec bounds %v %d %d", c.name, spec.Timeout, spec.StdoutCap, spec.StderrCap)
		}
		if elapsed > 30*time.Second {
			t.Errorf("%s: took %v", c.name, elapsed)
		}
		if _, err := os.Lstat(filepath.Join(f.attempt(), "result.json")); err != nil {
			t.Errorf("%s: no recorded result: %v", c.name, err)
		}
		replay := Deps{SourceLstat: hideAdmin}
		replay.Run = func(context.Context, proc.Spec, *os.File, func([]byte) error) ([]byte, proc.Observation, error) {
			t.Errorf("%s: attempt replayed", c.name)
			return nil, proc.Observation{}, proc.ErrStart
		}
		if _, err := RunWith(context.Background(), f.request(true), &bytes.Buffer{}, replay); !IsCode(err, CodeExists) {
			t.Errorf("%s: replay %v", c.name, err)
		}
	}
}
