package review

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
	tf "github.com/afewell-hh/bounded-agent-workflow/internal/testfixture"
)

// TestMain doubles as the compiled fake reviewer and as the controller used
// by the SIGKILL tests: with argv "-baw-review-fake MODE COUNTER [EXTRA...]"
// or "-baw-review-controller DIR" it never runs tests. Every reviewer start
// appends one line to COUNTER/reviewer, so tests count starts independently
// of review output.
func TestMain(m *testing.M) {
	if len(os.Args) > 3 && os.Args[1] == "-baw-review-fake" {
		os.Exit(fakeReviewer(os.Args[2], os.Args[3], os.Args[4:]))
	}
	if len(os.Args) == 3 && os.Args[1] == "-baw-review-controller" {
		os.Exit(controller(os.Args[2]))
	}
	if len(os.Args) == 3 && os.Args[1] == "-baw-review-probe" {
		os.Exit(probe(os.Args[2]))
	}
	proc.GracePeriod, proc.JoinBound = 300*time.Millisecond, 300*time.Millisecond
	os.Exit(m.Run())
}

const (
	reportPass  = `{"schema_version":1,"verdict":"PASS"}` + "\n"
	reportFixes = `{"schema_version":1,"verdict":"REQUIRED_FIXES"}` + "\n"
	dummySecret = "DUMMY-SECRET-7f3a91c2-not-a-real-token"
)

func padReport(n int) string {
	s := `{"schema_version":1,"verdict":"PASS"}`
	return s + strings.Repeat(" ", n-len(s))
}

// marker atomically publishes a nonce-named marker file.
func marker(counter, name string) {
	tmp := filepath.Join(counter, "."+name+".tmp")
	os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0o600)
	os.Rename(tmp, filepath.Join(counter, name))
}

func fakeGit(args ...string) error {
	cmd := exec.Command("/usr/bin/git", append([]string{"-c", "user.name=Fake", "-c", "user.email=fake@example.invalid",
		"-c", "core.excludesFile=/dev/null"}, args...)...)
	cmd.Env = []string{"HOME=" + os.Getenv("HOME"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "LC_ALL=C"}
	return cmd.Run()
}

func fakeReviewer(mode, counter string, extra []string) int {
	f, err := os.OpenFile(filepath.Join(counter, "reviewer"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return 90
	}
	f.WriteString(strconv.Itoa(os.Getpid()) + "\n")
	f.Close()
	out := os.Stdout.WriteString
	switch mode {
	case "pass":
		out(reportPass)
	case "fixes":
		out(reportFixes)
	case "nonzero-pass":
		out(reportPass)
		return 3
	case "dup-escaped":
		out(`{"schema_version":1,"verdict":"PASS","verdict":"PASS"}`)
	case "dup-nested-escaped":
		out(`{"schema_version":1,"verdict":"PASS","x":{"a":1,"a":2}}`)
	case "escaped-control":
		out(`{"schema_version":1,"verdict":"PA\u0001SS"}`)
	case "raw-control":
		out("{\"schema_version\":1,\"verdict\":\"PA\x01SS\"}")
	case "bad-utf8":
		out("{\"schema_version\":1,\"verdict\":\"PASS\",\"\xff\":1}")
	case "version-2":
		out(`{"schema_version":2,"verdict":"PASS"}`)
	case "version-float":
		out(`{"schema_version":1.0,"verdict":"PASS"}`)
	case "version-string":
		out(`{"schema_version":"1","verdict":"PASS"}`)
	case "unknown-key":
		out(`{"schema_version":1,"verdict":"PASS","findings":[]}`)
	case "lower-verdict":
		out(`{"schema_version":1,"verdict":"pass"}`)
	case "two-objects":
		out(reportPass + reportPass)
	case "prose":
		out("Looks good to me. " + dummySecret + "\n")
	case "exact-2048":
		out(padReport(2048))
	case "over-2048":
		out(padReport(2049))
	case "stderr-secret":
		os.Stderr.WriteString("reviewer prose " + dummySecret + "\n")
		out(reportPass)
	case "stderr-over":
		os.Stderr.WriteString(strings.Repeat("e", 65537))
		out(reportPass)
	case "modify":
		os.WriteFile("README.md", []byte("reviewer changed tracked source\n"), 0o644)
		out(reportPass)
	case "untracked":
		os.WriteFile("new-file.txt", []byte("x\n"), 0o644)
		out(reportPass)
	case "stage":
		os.WriteFile("README.md", []byte("reviewer staged change\n"), 0o644)
		if fakeGit("add", "README.md") != nil {
			return 95
		}
		out(reportPass)
	case "commit":
		os.WriteFile("README.md", []byte("reviewer committed change\n"), 0o644)
		if fakeGit("commit", "-q", "-am", "reviewer commit") != nil {
			return 95
		}
		out(reportPass)
	case "env":
		// extra: expected top level.
		env := os.Environ()
		sort.Strings(env)
		home, tmp := os.Getenv("HOME"), os.Getenv("TMPDIR")
		if len(env) != 4 || os.Getenv("LANG") != "C" || os.Getenv("LC_ALL") != "C" ||
			!strings.HasSuffix(home, "/reviewer/home") || !strings.HasSuffix(tmp, "/reviewer/tmp") || home == tmp {
			return 97
		}
		for _, d := range []string{home, tmp} {
			fi, err := os.Lstat(d)
			es, err2 := os.ReadDir(d)
			if err != nil || err2 != nil || len(es) != 0 || fi.Mode().Perm() != 0o700 || !fi.IsDir() {
				return 98
			}
		}
		if wd, err := os.Getwd(); err != nil || len(extra) != 1 || wd != extra[0] {
			return 96
		}
		in, err1 := os.Stdin.Stat()
		null, err2 := os.Stat("/dev/null")
		if err1 != nil || err2 != nil || !os.SameFile(in, null) {
			return 94
		}
		out(reportPass)
	case "sleep":
		// extra: nonce. The reviewer holds an exclusive lock on lock-NONCE
		// for its whole life (released by the kernel when it exits, however
		// it ends), then publishes the start marker before the test acts and
		// an end marker only if it finishes its finite sleep.
		if len(extra) != 1 {
			return 93
		}
		lf, err := os.OpenFile(filepath.Join(counter, "lock-"+extra[0]), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil || syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			return 92
		}
		marker(counter, "started-"+extra[0])
		time.Sleep(3 * time.Second)
		marker(counter, "end-"+extra[0])
		out(reportPass)
	case "replace-top":
		// Physically replaces the top level with a clean copy at the same
		// path; the original directory is retained beside it, never removed.
		wd, err := os.Getwd()
		if err != nil || os.Rename(wd, wd+".orig") != nil {
			return 89
		}
		if exec.Command("/bin/cp", "-Rp", wd+".orig", wd).Run() != nil {
			return 88
		}
		out(reportPass)
	case "replace-format":
		// extra: the other object format. Retains the original top level and
		// creates a committed repository of that format at the same path.
		wd, err := os.Getwd()
		if err != nil || len(extra) != 1 || os.Rename(wd, wd+".orig") != nil {
			return 87
		}
		os.Setenv("PATH", "/usr/bin:/bin")
		home := os.Getenv("HOME")
		if tf.Init(home, wd, extra[0]) != nil {
			return 86
		}
		if _, err := tf.CommitSources(home, wd); err != nil {
			return 85
		}
		out(reportPass)
	default:
		return 99
	}
	return 0
}

// controllerConfig is the request a controller child runs.
type controllerConfig struct {
	Request Request `json:"request"`
	Counter string  `json:"counter"`
	Nonce   string  `json:"nonce"`
	Before  bool    `json:"before"`
}

// controller runs one Review; with Before it publishes a ready marker at
// the reviewer-start boundary (after durable intent, before Start) and then
// blocks until it is killed.
func controller(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	var c controllerConfig
	if err != nil || json.Unmarshal(b, &c) != nil {
		return 80
	}
	if c.Before {
		hook = func(stage string) error {
			if stage == "reviewer-start" {
				marker(c.Counter, "ready-"+c.Nonce)
				time.Sleep(60 * time.Second)
			}
			return nil
		}
	}
	if _, err := Review(context.Background(), c.Request, os.Stdout); err != nil {
		return 81
	}
	return 0
}

// probeResult is what a probe child reports about its one Review call.
type probeResult struct {
	Code     string `json:"code"`
	Out      string `json:"out"`
	Observed int    `json:"observed"`
	Open     int    `json:"open"`
}

// probe runs one Review in a separate test-binary process, so a FIFO or
// socket that could block an open or read can only stall this child, which
// the parent bounds and always joins. Every descriptor the package opened is
// checked for actual closure (os.ErrClosed) before the child reports.
func probe(dir string) int {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	var c controllerConfig
	if err != nil || json.Unmarshal(b, &c) != nil {
		return 70
	}
	var files []*os.File
	opened = func(f *os.File) { files = append(files, f) }
	var out strings.Builder
	_, err = Review(context.Background(), c.Request, &out)
	r := probeResult{Out: out.String(), Observed: len(files)}
	if err != nil {
		r.Code = err.Error()
	}
	for _, f := range files {
		if _, err := f.Stat(); !errors.Is(err, os.ErrClosed) {
			r.Open++
		}
	}
	data, _ := json.Marshal(r)
	os.Stdout.Write(data)
	return 0
}
