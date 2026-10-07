package cli

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
)

// The TestLimits timeout cases run each RunWithLimits invocation in its own
// re-executed test-binary controller, with private arguments, environment and
// fixture directory, so the cases overlap without touching this process's
// environment or any package variable. The fake Git leader is this test
// binary again, reached through a symlink named git, so no executable is
// written for the fixture; this reduces fixture setup and is a test-only
// choice. The leader records a leader
// nonce with its PID and starts one descendant (this test binary again). The
// descendant holds an exclusive flock on its private lock file for its whole
// life and only then publishes a nonce acknowledgement with its PID and group.
// Readiness is the controller observing that exact nonce; ownership is the
// kernel reporting, while both are live, that the leader leads its own group
// and that the descendant is in it; cleanup is the controller acquiring the
// lock (the kernel releases it only when the descendant's file is closed by
// exit) and the kernel no longer knowing the leader. No PID is ever signalled
// by the test; the only signal is a last-resort kill of an owned controller
// through its unreaped exec handle, after its fixtures were observed gone.
// All fixtures live under one private root that only the aggregate finalizer
// removes, and only once every case is reconciled; otherwise it is retained.

const (
	limitsControllerFlag = "-baw-limits-controller"
	limitsDescendantFlag = "-baw-limits-descendant"
	limitsFakeGitDir     = "baw-limits-fakegit"
	// The configured restrictive application limits.
	limitsGitRestrictive   = 1 * time.Second
	limitsTotalRestrictive = 1500 * time.Millisecond
	// limitsAlternate is the non-restrictive limit; it exceeds every
	// accepted window below, so it cannot be what ended an accepted case.
	limitsAlternate = 60 * time.Second
	// limitsStartup is the allowance for work before the restrictive clock
	// starts (GitTimeout starts only after the Git child is started) and
	// for scheduling of the return.
	limitsStartup = 250 * time.Millisecond
	// limitsObserve bounds observing descendant absence after return.
	limitsObserve = 500 * time.Millisecond
	// limitsRescue bounds a cooperative rescue and join of a descendant.
	limitsRescue = 1500 * time.Millisecond
	// limitsCoop bounds a controller's cooperative exit once requested.
	limitsCoop = time.Second
	// limitsReap bounds reaping a controller after it exits or is killed.
	limitsReap = 500 * time.Millisecond
	// limitsLifetime is the descendant's natural lifetime. It is beyond the
	// whole test, so natural expiry cannot counterfeit cleanup.
	limitsLifetime = 20 * time.Second
	// limitsDriveReady bounds a finalization case's wait for its
	// descendant; limitsDriveBound bounds the whole case from its start.
	limitsDriveReady = 1500 * time.Millisecond
	limitsDriveBound = limitsDriveReady + limitsCoop + limitsRescue + limitsReap
	// limitsParentBudget is the frozen ceiling for the whole TestLimits.
	limitsParentBudget = 10 * time.Second
	limitsPoll         = 10 * time.Millisecond
)

// limitsUpper is the accepted invocation upper bound for a restrictive limit:
// the limit plus the existing SIGTERM grace, the group join and the pipe join
// bounds, plus the startup allowance.
func limitsUpper(limit time.Duration) time.Duration {
	return limit + proc.GracePeriod + 2*proc.JoinBound + limitsStartup
}

func init() {
	if len(os.Args) == 3 && os.Args[1] == limitsControllerFlag {
		runtime.GOMAXPROCS(1)
		os.Exit(limitsController(os.Args[2]))
	}
	if len(os.Args) > 2 && os.Args[1] == "-C" && filepath.Base(os.Args[0]) == "git" &&
		filepath.Base(filepath.Dir(os.Args[0])) == limitsFakeGitDir {
		runtime.GOMAXPROCS(1)
		os.Exit(limitsFakeGit(os.Args[2]))
	}
	if len(os.Args) == 5 && os.Args[1] == limitsDescendantFlag {
		runtime.GOMAXPROCS(1)
		os.Exit(limitsDescendant(os.Args[2], os.Args[3], os.Args[4]))
	}
}

type limitsSpec struct {
	Name          string
	Nonce         string // expected acknowledgement nonce
	LeaderNonce   string // expected leader nonce
	Token         string // cooperative finalization request
	GitTimeout    time.Duration
	Total         time.Duration
	Lower, Upper  time.Duration
	ReadyWindow   time.Duration
	Observe       time.Duration
	Rescue        time.Duration
	Repo          string
	DescendantArg string // "ack", "silent", "escape" or "foreign"
	SendNonce     string // what the descendant acknowledges
	Foreign       int    // group a "foreign" descendant joins
	Controller    string // "", "crash" or "ignore"
}

type limitsObservation struct {
	Returned         bool
	Code             int
	Stdout, Stderr   string
	Invocation       time.Duration // start to return; -1 if not returned
	Leader           string        // raw leader marker, "" if none
	LeaderKernelPgid int           // kernel's group of the leader while live
	Ack              string        // raw acknowledgement, "" if none
	AckAt            time.Duration // -1 if none
	AckKernelPgid    int           // kernel's group of the descendant while live
	Gone             bool          // lock acquired before rescue
	GoneAt           time.Duration // -1 if not gone
	LeaderGone       bool          // kernel no longer knows the leader
	ControllerPid    int
	ControllerPgid   int
}

type limitsResult struct {
	Obs           limitsObservation
	Verdict       []string // evaluated before any rescue
	Aborted       bool     // finalization was requested before completion
	FinalizeKind  string   // "token" or "eof" when aborted
	Rescued       bool     // a rescue was required
	FinalReturned bool     // invocation joined after rescue
	FinalGone     bool     // descendant absent after rescue/join
}

// limitsLeaderPid parses a leader marker "nonce pid\n" for nonce.
func limitsLeaderPid(raw, nonce string) int {
	f := strings.Fields(raw)
	if len(f) != 2 || f[0] != nonce || !strings.HasSuffix(raw, "\n") || strings.Count(raw, "\n") != 1 {
		return 0
	}
	pid, err := strconv.Atoi(f[1])
	if err != nil || pid <= 1 {
		return 0
	}
	return pid
}

// limitsVerdict is the single acceptance oracle used by every case and
// negative control. An empty result accepts.
func limitsVerdict(s limitsSpec, o limitsObservation) []string {
	if !o.Returned {
		return []string{"not_completed"}
	}
	var r []string
	if o.Code != 1 || o.Stdout != "" || o.Stderr != "baw: command_timeout\n" {
		r = append(r, "result_mismatch")
	}
	if o.Invocation < s.Lower || o.Invocation > s.Upper {
		r = append(r, "elapsed_out_of_bounds")
	}
	leader := limitsLeaderPid(o.Leader, s.LeaderNonce)
	if o.Ack == "" {
		r = append(r, "readiness_missing")
	} else {
		f := strings.Fields(o.Ack)
		if len(f) != 3 || f[0] != s.Nonce || !strings.HasSuffix(o.Ack, "\n") || strings.Count(o.Ack, "\n") != 1 {
			r = append(r, "readiness_mismatch")
		} else {
			pid, err1 := strconv.Atoi(f[1])
			pgid, err2 := strconv.Atoi(f[2])
			// The leader this invocation started leads its own group, and
			// the descendant is a non-leader member of exactly that group,
			// both as observed from the kernel while live.
			if leader == 0 || o.LeaderKernelPgid != leader || leader == o.ControllerPid || leader == o.ControllerPgid ||
				err1 != nil || err2 != nil || pid <= 1 || pid == leader || pgid != leader || o.AckKernelPgid != leader {
				r = append(r, "descendant_not_owned")
			}
		}
		if o.AckAt <= 0 || o.AckAt > s.ReadyWindow || o.AckAt >= o.Invocation {
			r = append(r, "readiness_out_of_window")
		}
	}
	if !o.Gone || o.GoneAt > o.Invocation+s.Observe {
		r = append(r, "descendant_live")
	}
	if leader != 0 && !o.LeaderGone {
		r = append(r, "leader_live")
	}
	return r
}

func limitsController(dir string) int {
	data, err := os.ReadFile(filepath.Join(dir, "spec.json"))
	if err != nil {
		return 2
	}
	var s limitsSpec
	if err := json.Unmarshal(data, &s); err != nil {
		return 2
	}
	limits := inspect.DefaultLimits
	limits.GitTimeout, limits.Total = s.GitTimeout, s.Total
	o := limitsObservation{Invocation: -1, AckAt: -1, GoneAt: -1, LeaderKernelPgid: -1, AckKernelPgid: -1,
		ControllerPid: os.Getpid(), ControllerPgid: syscall.Getpgrp()}
	path := func(n string) string { return filepath.Join(dir, n) }
	// The parent requests cooperative finalization by writing the private
	// token to stdin and closing it; end of input without it also ends the
	// case, since the parent is then gone.
	finalize := make(chan string, 1)
	if s.Controller != "ignore" {
		go func() {
			b, _ := io.ReadAll(io.LimitReader(os.Stdin, 4096))
			if strings.TrimSpace(string(b)) == s.Token {
				finalize <- "token"
			} else {
				finalize <- "eof"
			}
		}()
	}
	type ret struct {
		code           int
		stdout, stderr string
	}
	done := make(chan ret, 1)
	// Without this marker the finalizer knows no Git child can have started.
	if os.WriteFile(path("invoked"), nil, 0o600) != nil {
		return 2
	}
	// Invocation timing starts immediately before RunWithLimits.
	start := time.Now()
	go func() {
		var out, errb bytes.Buffer
		code := RunWithLimits([]string{"inspect", "--repo", s.Repo}, &out, &errb, limits)
		_ = os.WriteFile(path("returned"), nil, 0o600)
		done <- ret{code, out.String(), errb.String()}
	}()
	leader := 0
	// poll reads the markers; the kernel is asked for the groups only while
	// the invocation has not been seen to return.
	poll := func(live bool) {
		if leader == 0 {
			if b, err := os.ReadFile(path("leader")); err == nil {
				if leader = limitsLeaderPid(string(b), s.LeaderNonce); leader != 0 || strings.HasSuffix(string(b), "\n") {
					o.Leader = string(b)
					if live && leader != 0 {
						if g, err := syscall.Getpgid(leader); err == nil {
							o.LeaderKernelPgid = g
						}
					}
				}
			}
		}
		if o.Ack != "" {
			return
		}
		b, err := os.ReadFile(path("ack"))
		if err != nil {
			return
		}
		o.AckAt, o.Ack = time.Since(start), string(b)
		if f := strings.Fields(o.Ack); live && len(f) == 3 {
			if pid, err := strconv.Atoi(f[1]); err == nil && pid > 1 {
				if g, err := syscall.Getpgid(pid); err == nil {
					o.AckKernelPgid = g
				}
			}
		}
		_ = os.WriteFile(path("observed"), nil, 0o600)
		if s.Controller == "crash" {
			// Simulated controller failure: the invocation and its
			// descendant are left behind for the parent to reconcile.
			os.Exit(5)
		}
	}
	res := limitsResult{}
	upper := time.NewTimer(s.Upper)
	tick := time.NewTicker(limitsPoll)
	defer tick.Stop()
wait:
	for {
		select {
		case r := <-done:
			o.Returned, o.Invocation = true, time.Since(start)
			o.Code, o.Stdout, o.Stderr = r.code, r.stdout, r.stderr
			break wait
		case <-upper.C:
			break wait
		case k := <-finalize:
			res.Aborted, res.FinalizeKind = true, k
			break wait
		case <-tick.C:
			poll(true)
		}
	}
	upper.Stop()
	if s.Controller == "ignore" {
		// Simulated hung controller: it neither exits nor answers.
		time.Sleep(time.Until(start.Add(s.Upper)))
	}
	poll(false)
	if o.Returned {
		for {
			if !o.Gone && limitsLockState(path("live.lock")) == limitsFree {
				o.Gone, o.GoneAt = true, time.Since(start)
			}
			if !o.LeaderGone && leader != 0 && limitsPidGone(leader) {
				o.LeaderGone = true
			}
			if o.Gone && (o.LeaderGone || leader == 0) || time.Since(start) > o.Invocation+s.Observe {
				break
			}
			<-tick.C
		}
	}
	res.Obs, res.Verdict = o, limitsVerdict(s, o)
	// Cooperative rescue happens only after the verdict is fixed.
	res.FinalReturned, res.FinalGone = o.Returned, o.Gone
	if !o.Returned || !o.Gone || res.Aborted {
		res.Rescued = true
		_ = os.WriteFile(path("exit"), nil, 0o600)
		deadline := time.Now().Add(s.Rescue)
		for time.Now().Before(deadline) && !(res.FinalReturned && res.FinalGone) {
			select {
			case <-done:
				res.FinalReturned = true
			case <-tick.C:
			}
			st := limitsLockState(path("live.lock"))
			res.FinalGone = res.FinalGone || st == limitsFree || st == limitsAbsent && res.FinalReturned
		}
	}
	out, err := json.Marshal(res)
	if err != nil {
		return 2
	}
	if err := os.WriteFile(path("result.tmp"), out, 0o600); err != nil {
		return 2
	}
	if err := os.Rename(path("result.tmp"), path("result.json")); err != nil {
		return 2
	}
	if !res.FinalReturned || !res.FinalGone {
		return 3
	}
	return 0
}

type limitsLock int

const (
	limitsAbsent limitsLock = iota
	limitsHeld
	limitsFree
)

// limitsLockState reports whether the descendant's lock file is absent, held,
// or free; it is free only after the descendant's file was closed by its exit.
func limitsLockState(path string) limitsLock {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return limitsAbsent
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return limitsHeld
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return limitsFree
}

// limitsPidGone asks the kernel about pid without signalling it.
func limitsPidGone(pid int) bool {
	_, err := syscall.Getpgid(pid)
	return errors.Is(err, syscall.ESRCH)
}

func limitsExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// limitsFakeGit is the fake Git leader for the repository repo inside a case
// directory. Only the first invocation records the leader marker and starts
// the descendant in its own (the application's owned) group; it then waits.
func limitsFakeGit(repo string) int {
	if filepath.Base(repo) != "repo" {
		return 1
	}
	path := func(n string) string { return filepath.Join(filepath.Dir(repo), n) }
	if limitsExists(path("exit")) {
		return 1
	}
	f, err := os.OpenFile(path("launched"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 1
	}
	f.Close()
	data, err := os.ReadFile(path("spec.json"))
	if err != nil {
		return 1
	}
	var s limitsSpec
	if json.Unmarshal(data, &s) != nil {
		return 1
	}
	marker := fmt.Sprintf("%s %d\n", s.LeaderNonce, os.Getpid())
	if os.WriteFile(path("leader.tmp"), []byte(marker), 0o600) != nil || os.Rename(path("leader.tmp"), path("leader")) != nil {
		return 1
	}
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	cmd := exec.Command(exe, limitsDescendantFlag, s.DescendantArg, s.SendNonce, strconv.Itoa(s.Foreign))
	cmd.Dir = filepath.Dir(repo)
	if cmd.Start() != nil {
		return 1
	}
	_ = cmd.Wait()
	return 0
}

// limitsDescendant runs in the fixture directory. It locks first, then
// acknowledges, then lives until the exit request or its natural lifetime.
func limitsDescendant(mode, nonce, foreign string) int {
	if mode == "foreign" {
		// Join a group the application does not own before acknowledging.
		g, err := strconv.Atoi(foreign)
		if err != nil || syscall.Setpgid(0, g) != nil {
			return 3
		}
	}
	f, err := os.OpenFile("live.lock", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 3
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return 3
	}
	if limitsExists("exit") {
		return 0
	}
	if mode != "silent" {
		ack := fmt.Sprintf("%s %d %d\n", nonce, os.Getpid(), syscall.Getpgrp())
		if os.WriteFile("ack.tmp", []byte(ack), 0o600) != nil || os.Rename("ack.tmp", "ack") != nil {
			return 3
		}
	}
	deadline := time.Now().Add(limitsLifetime)
	if mode == "escape" {
		// Once observed in the owned group, leave it; the application can
		// no longer identify it.
		for time.Now().Before(deadline) && !limitsExists("observed") && !limitsExists("exit") {
			time.Sleep(limitsPoll)
		}
		if syscall.Setpgid(0, 0) != nil {
			return 3
		}
	}
	for time.Now().Before(deadline) && !limitsExists("exit") {
		time.Sleep(20 * time.Millisecond)
	}
	runtime.KeepAlive(f)
	return 0
}

func limitsNonce(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

type limitsCase struct {
	spec    limitsSpec
	want    []string
	final   string // finalization case kind, "" for an oracle case
	dir     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	started time.Time
	done    chan struct{}
	waitErr error
	mu      sync.Mutex
	drv     chan limitsDrive
	drvDone chan struct{} // closed when the finalization driver returned
}

// limitsFinal is what one finalization established.
type limitsFinal struct {
	Requested      bool // cooperative exit was requested
	Cooperative    bool // the controller exited within the bound after it
	ParentRescue   bool // the descendant was still live and the parent asked it to exit
	Returned       bool // RunWithLimits had returned before any kill
	DescendantGone bool
	LeaderGone     bool
	Killed         bool // last resort: the owned controller handle was killed
	Deadline       bool // the rescue bound passed without establishing the above
	Reaped         bool
}

func (f limitsFinal) reconciled() bool {
	return f.Reaped && f.DescendantGone && f.LeaderGone && !f.Deadline
}

type limitsCases struct {
	mu    sync.Mutex
	root  string // private fixture root, removed only after reconciliation
	cs    []*limitsCase
	early *limitsEarly // early-failure regression, nil inside it
	probe string       // the regression's handshake directory, inside it only
}

func limitsWait(ch <-chan struct{}, d time.Duration) bool {
	if d <= 0 {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ch:
		return true
	case <-timer.C:
		return false
	}
}

// finalize brings the case to a reconciled stop: it requests cooperative
// controller exit, asks a still-live descendant to exit and observes it and
// the leader gone, waits for RunWithLimits to have returned (or the
// controller to have exited), and only then kills a still-running controller
// through its owned handle as a last resort. Every wait is bounded; an
// unestablished fact stays false. If the bound passes first, nothing is
// killed and Deadline records it. It is idempotent.
func (c *limitsCase) finalize() limitsFinal {
	c.mu.Lock()
	defer c.mu.Unlock()
	var f limitsFinal
	path := func(n string) string { return filepath.Join(c.dir, n) }
	if !limitsWait(c.done, 0) {
		f.Requested = true
		_, _ = io.WriteString(c.stdin, c.spec.Token+"\n")
		_ = c.stdin.Close()
		f.Cooperative = limitsWait(c.done, limitsCoop)
	}
	f.ParentRescue = limitsLockState(path("live.lock")) == limitsHeld
	// Always request exit, so that a descendant starting late exits too.
	_ = os.WriteFile(path("exit"), nil, 0o600)
	deadline := time.Now().Add(limitsRescue)
	for {
		invoked, launched := limitsExists(path("invoked")), limitsExists(path("launched"))
		f.Returned = limitsExists(path("returned"))
		exited := limitsWait(c.done, 0)
		if launched {
			f.DescendantGone = limitsLockState(path("live.lock")) == limitsFree
			b, _ := os.ReadFile(path("leader"))
			pid := limitsLeaderPid(string(b), c.spec.LeaderNonce)
			f.LeaderGone = pid != 0 && limitsPidGone(pid)
		} else {
			// Nothing was launched, and nothing can be any more.
			none := f.Returned || exited && !invoked
			f.DescendantGone, f.LeaderGone = none, none
		}
		if f.DescendantGone && f.LeaderGone && (f.Returned || exited) {
			break
		}
		if !time.Now().Before(deadline) {
			f.Deadline = true
			break
		}
		time.Sleep(limitsPoll)
	}
	if !f.Deadline && !limitsWait(c.done, 0) {
		f.Killed = true
		_ = c.cmd.Process.Kill()
	}
	f.Reaped = limitsWait(c.done, limitsReap)
	return f
}

// limitsDrive is what a finalization case observed.
type limitsDrive struct {
	AckSeen        bool // the exact nonce was acknowledged
	LiveBefore     bool // the descendant held its lock just before finalization
	ControllerExit int  // controller exit code seen before finalization, -1 if running
	Final          limitsFinal
	LockFreeAfter  bool
	Result         *limitsResult
}

// drive runs one finalization case: it waits (bounded) for the descendant's
// lock and nonce, produces the case's failure, and finalizes.
func (c *limitsCase) drive() limitsDrive {
	d := limitsDrive{ControllerExit: -1}
	lock := filepath.Join(c.dir, "live.lock")
	ackOK := func() bool {
		b, _ := os.ReadFile(filepath.Join(c.dir, "ack"))
		f := strings.Fields(string(b))
		return len(f) == 3 && f[0] == c.spec.Nonce
	}
	deadline := c.started.Add(limitsDriveReady)
	if c.final == "ready_missing" {
		// Readiness is awaited until its window ends; it must stay missing.
		deadline = c.started.Add(c.spec.ReadyWindow)
	}
	for time.Now().Before(deadline) {
		d.AckSeen = ackOK()
		if c.final != "ready_missing" && d.AckSeen {
			break
		}
		time.Sleep(limitsPoll)
	}
	if c.final == "controller_failure" {
		// The controller fails once it saw the acknowledgement.
		if limitsWait(c.done, time.Until(deadline)+limitsCoop) {
			d.ControllerExit = -2
			var ee *exec.ExitError
			if errors.As(c.waitErr, &ee) {
				d.ControllerExit = ee.ExitCode()
			}
		}
	}
	d.LiveBefore = limitsLockState(lock) == limitsHeld
	d.Final = c.finalize()
	d.LockFreeAfter = limitsLockState(lock) == limitsFree
	if b, err := os.ReadFile(filepath.Join(c.dir, "result.json")); err == nil {
		var r limitsResult
		if json.Unmarshal(b, &r) == nil {
			d.Result = &r
		}
	}
	return d
}

// startLimitsTimeoutCases starts every timeout case, negative control,
// finalization case and the early-failure regression in its own process.
// The aggregate finalizer is registered before any process starts, and every
// fixture lives under a private root that only it removes, after
// reconciliation; no testing temporary-directory cleanup can remove fixture
// evidence first, whatever the order of failure. Each process joins the
// finalization set immediately after Start.
func startLimitsTimeoutCases(t *testing.T) *limitsCases {
	t.Helper()
	probe, earlyNonce := limitsEarlyArgs()
	root, err := os.MkdirTemp("", "baw-limits-")
	if err != nil {
		t.Fatal(err)
	}
	cs := &limitsCases{root: root, probe: probe}
	t.Cleanup(func() { cs.finalizeAll(t) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, limitsFakeGitDir)
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(exe, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	gitUpper, totalUpper := limitsUpper(limitsGitRestrictive), limitsUpper(limitsTotalRestrictive)
	if limitsAlternate <= totalUpper+limitsObserve+limitsRescue || limitsLifetime <= limitsParentBudget {
		t.Fatal("alternate limit or descendant lifetime is not beyond the acceptance window")
	}
	mk := func(name string, git, total time.Duration, mode string, want ...string) *limitsCase {
		c := &limitsCase{spec: limitsSpec{Name: name, Nonce: limitsNonce(t), LeaderNonce: limitsNonce(t), Token: limitsNonce(t),
			GitTimeout: git, Total: total, Lower: limitsGitRestrictive, Upper: gitUpper,
			ReadyWindow: limitsGitRestrictive + limitsStartup, Observe: limitsObserve, Rescue: limitsRescue,
			DescendantArg: mode, Foreign: syscall.Getpgrp()}, want: want}
		c.spec.SendNonce = c.spec.Nonce
		return c
	}
	gitCase := mk("git_timeout", limitsGitRestrictive, limitsAlternate, "ack")
	totalCase := mk("total_budget", limitsAlternate, limitsTotalRestrictive, "ack")
	totalCase.spec.Lower, totalCase.spec.Upper = limitsTotalRestrictive, totalUpper
	totalCase.spec.ReadyWindow = limitsTotalRestrictive
	wrong := mk("control_wrong_ack", limitsGitRestrictive, limitsAlternate, "ack", "readiness_mismatch")
	wrong.spec.SendNonce = limitsNonce(t) // never issued to any case
	stale := mk("control_stale_ack", limitsGitRestrictive, limitsAlternate, "ack", "readiness_mismatch")
	stale.spec.SendNonce = gitCase.spec.Nonce // genuine nonce of another invocation
	fin := func(kind, mode, controller string) *limitsCase {
		c := mk("final_"+kind, limitsAlternate, limitsAlternate, mode)
		c.final, c.spec.Controller, c.spec.Upper = kind, controller, 2*limitsParentBudget
		return c
	}
	// The disabled-timeout control decides at the upper deadline; it starts
	// first because it is the longest.
	disabled := mk("control_timeout_disabled", limitsAlternate, limitsAlternate, "ack", "not_completed")
	if probe != "" {
		// Inside the regression its nonce is the one the observer issued.
		disabled.spec.Nonce, disabled.spec.SendNonce = earlyNonce, earlyNonce
	} else {
		cs.startEarly(t, exe)
	}
	for _, c := range []*limitsCase{
		disabled, gitCase, totalCase, wrong, stale,
		mk("control_missing_ack", limitsGitRestrictive, limitsAlternate, "silent", "readiness_missing"),
		mk("control_still_live", limitsGitRestrictive, limitsAlternate, "escape", "descendant_live"),
		mk("control_wrong_group", limitsGitRestrictive, limitsAlternate, "foreign", "descendant_not_owned", "descendant_live"),
		fin("ready_missing", "silent", ""),
		fin("controller_failure", "ack", "crash"),
		fin("overrun", "ack", "ignore"),
	} {
		c.start(t, exe, bin, cs)
		if probe != "" {
			cs.failEarly(t, c)
		}
	}
	return cs
}

func (c *limitsCase) start(t *testing.T, exe, bin string, cs *limitsCases) {
	t.Helper()
	dir, err := os.MkdirTemp(cs.root, c.spec.Name+"-")
	if err != nil {
		t.Fatal(err)
	}
	c.dir = dir
	c.spec.Repo = filepath.Join(c.dir, "repo")
	home := filepath.Join(c.dir, "home")
	for _, d := range []string{c.spec.Repo, home} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	spec, err := json.Marshal(c.spec)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(c.dir, "spec.json"), string(spec))
	logf, err := os.Create(filepath.Join(c.dir, "controller.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	c.cmd = exec.Command(exe, limitsControllerFlag, c.dir)
	c.cmd.Dir = c.dir
	c.cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + c.dir}
	c.cmd.Stdout, c.cmd.Stderr = logf, logf
	// Its own group, so that the parent's group is foreign to it.
	c.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if c.stdin, err = c.cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	c.started = time.Now()
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	c.done = make(chan struct{})
	if c.final != "" {
		c.drv, c.drvDone = make(chan limitsDrive, 1), make(chan struct{})
	}
	cs.mu.Lock()
	cs.cs = append(cs.cs, c)
	cs.mu.Unlock()
	go func() {
		c.waitErr = c.cmd.Wait()
		close(c.done)
	}()
	if c.final != "" {
		go func() {
			defer close(c.drvDone)
			c.drv <- c.drive()
		}()
	}
}

// finalizeAll finalizes every started case and the regression concurrently,
// joins every driver, and reports anything not reconciled. It removes the
// fixture root only when everything was reconciled within its bound;
// otherwise the evidence is retained and its location reported.
func (cs *limitsCases) finalizeAll(t *testing.T) {
	cs.mu.Lock()
	list := append([]*limitsCase(nil), cs.cs...)
	early := cs.early
	cs.mu.Unlock()
	bound := limitsCoop + limitsRescue + limitsReap + time.Second + limitsDriveBound
	deadline := time.Now().Add(bound)
	type rep struct {
		name string
		f    limitsFinal
	}
	reps := make(chan rep, len(list)+1)
	n := len(list)
	for _, c := range list {
		go func() { reps <- rep{c.spec.Name, c.finalize()} }()
	}
	if early != nil {
		n++
		go func() { reps <- rep{"early_failure_regression", early.finalize()} }()
	}
	var finals []limitsFinal
	ok := true
	for range n {
		select {
		case r := <-reps:
			finals = append(finals, r.f)
			if !r.f.reconciled() {
				ok = false
				t.Errorf("%s: unreconciled after finalization: %+v", r.name, r.f)
			}
		case <-time.After(time.Until(deadline)):
			t.Errorf("finalization did not complete within %v; fixture evidence retained in %s", bound, cs.root)
			return
		}
	}
	for _, c := range list {
		if c.drvDone != nil && !limitsWait(c.drvDone, time.Until(deadline)) {
			ok = false
			t.Errorf("%s: finalization driver not joined within %v", c.spec.Name, bound)
		}
	}
	if early != nil && !limitsWait(early.drvDone, time.Until(deadline)) {
		ok = false
		t.Errorf("early_failure_regression: observer not joined within %v", bound)
	}
	if cs.probe != "" {
		cs.handshake(t, finals)
	}
	if !ok {
		t.Errorf("fixture evidence retained in %s", cs.root)
		return
	}
	if err := os.RemoveAll(cs.root); err != nil {
		t.Errorf("removing fixture root: %v", err)
	}
}

// The early-failure regression runs TestLimits again in a child test process
// told, by positional arguments, to fail abruptly during partial setup once
// its first controller's descendant acknowledged a nonce this observer issued.
// The observer independently sees that descendant live in the leader's group
// and keeps its lock file open. When the child's aggregate finalizer has
// finished and waits for release, the observer sees the lock free and the
// leader unknown to the kernel while the fixture directory still exists, and
// the controller's own result written; only then may the child remove it.

const (
	limitsEarlyArg     = "baw-limits-early-failure"
	limitsEarlyFailure = "limits early-failure regression: deliberate failure during partial setup"
	// limitsEarlyReady bounds the child's start to its acknowledgement.
	limitsEarlyReady = 3 * time.Second
	// limitsEarlyFinal bounds the child's finalization after observation.
	limitsEarlyFinal = limitsCoop + limitsRescue + limitsReap + time.Second
	// limitsEarlyExit bounds the child's exit after release.
	limitsEarlyExit  = limitsCoop
	limitsEarlyBound = limitsEarlyReady + limitsEarlyFinal + limitsEarlyExit
)

// limitsEarlyArgs returns the regression's handshake directory and nonce
// inside the child, and empty strings otherwise.
func limitsEarlyArgs() (probe, nonce string) {
	if a := flag.Args(); len(a) == 3 && a[0] == limitsEarlyArg {
		return a[1], a[2]
	}
	return "", ""
}

type limitsEarly struct {
	nonce, dir, probe string
	cmd               *exec.Cmd
	started           time.Time
	done, drvDone     chan struct{}
	waitErr           error
	rep               limitsEarlyReport
	mu                sync.Mutex
	lock              *os.File // the descendant's lock file, kept open
	leader            int
}

type limitsEarlyReport struct {
	AckAt, FinalizedAt, ExitAt time.Duration // from start, -1 if not seen
	Problems                   []string
}

// limitsFdFree reports whether the lock on the open file f is free.
func limitsFdFree(f *os.File) bool {
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return false
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return true
}

func (cs *limitsCases) startEarly(t *testing.T, exe string) {
	t.Helper()
	e := &limitsEarly{nonce: limitsNonce(t)}
	dir, err := os.MkdirTemp(cs.root, "early_failure-")
	if err != nil {
		t.Fatal(err)
	}
	e.dir, e.probe = dir, filepath.Join(dir, "probe")
	home := filepath.Join(dir, "home")
	for _, d := range []string{e.probe, home} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	logf, err := os.Create(filepath.Join(dir, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()
	e.cmd = exec.Command(exe, "-test.run=^TestLimits$", "-test.count=1", "-test.v=true",
		"-test.timeout="+(2*limitsParentBudget).String(), limitsEarlyArg, e.probe, e.nonce)
	e.cmd.Dir = dir
	// The child's fixture root is created under this directory.
	e.cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + dir}
	e.cmd.Stdout, e.cmd.Stderr = logf, logf
	e.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	e.started = time.Now()
	if err := e.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	e.done, e.drvDone = make(chan struct{}), make(chan struct{})
	cs.mu.Lock()
	cs.early = e
	cs.mu.Unlock()
	go func() {
		e.waitErr = e.cmd.Wait()
		close(e.done)
	}()
	go func() {
		defer close(e.drvDone)
		e.rep = e.observe()
	}()
}

// observe is the regression's independent observer. Every wait is bounded and
// the child is always released.
func (e *limitsEarly) observe() limitsEarlyReport {
	r := limitsEarlyReport{AckAt: -1, FinalizedAt: -1, ExitAt: -1}
	fail := func(p string) { r.Problems = append(r.Problems, p) }
	probe := func(n string) string { return filepath.Join(e.probe, n) }
	caseDir, desc, pgid := "", 0, 0
	for deadline := e.started.Add(limitsEarlyReady); caseDir == "" && time.Now().Before(deadline); time.Sleep(limitsPoll) {
		m, _ := filepath.Glob(filepath.Join(e.dir, "baw-limits-*", "control_timeout_disabled-*", "ack"))
		for _, p := range m {
			b, _ := os.ReadFile(p)
			f := strings.Fields(string(b))
			if len(f) == 3 && f[0] == e.nonce && strings.HasSuffix(string(b), "\n") && strings.Count(string(b), "\n") == 1 {
				caseDir, r.AckAt = filepath.Dir(p), time.Since(e.started)
				desc, _ = strconv.Atoi(f[1])
				pgid, _ = strconv.Atoi(f[2])
			}
		}
	}
	if caseDir == "" {
		fail("readiness_missing")
	} else {
		b, _ := os.ReadFile(filepath.Join(caseDir, "leader"))
		leader := 0
		if f := strings.Fields(string(b)); len(f) == 2 {
			leader, _ = strconv.Atoi(f[1])
		}
		lg, err1 := syscall.Getpgid(leader)
		dg, err2 := syscall.Getpgid(desc)
		if leader <= 1 || desc <= 1 || desc == leader || pgid != leader || err1 != nil || lg != leader || err2 != nil || dg != leader {
			fail("descendant_not_owned")
		}
		lock, err := os.OpenFile(filepath.Join(caseDir, "live.lock"), os.O_RDWR, 0)
		if err != nil {
			fail("lock_unavailable")
		} else {
			e.mu.Lock()
			e.lock, e.leader = lock, leader
			e.mu.Unlock()
			if limitsFdFree(lock) {
				fail("descendant_not_live")
			}
		}
	}
	_ = os.WriteFile(probe("observed"), nil, 0o600)
	var finals []limitsFinal
	found := false
	for deadline := time.Now().Add(limitsEarlyFinal); !found && time.Now().Before(deadline) && !limitsWait(e.done, 0); time.Sleep(limitsPoll) {
		if b, err := os.ReadFile(probe("finalized")); err == nil {
			found, r.FinalizedAt = true, time.Since(e.started)
			if json.Unmarshal(b, &finals) != nil {
				fail("finalizer_record_unreadable")
			}
		}
	}
	if !found {
		fail("finalizer_not_seen")
	} else if caseDir != "" {
		// The child waits for release here. Gone first, then still present.
		e.mu.Lock()
		lock, leader := e.lock, e.leader
		e.mu.Unlock()
		if lock == nil || !limitsFdFree(lock) {
			fail("descendant_live_at_finalization")
		}
		if leader <= 1 || !limitsPidGone(leader) {
			fail("leader_live_at_finalization")
		}
		if !limitsExists(filepath.Join(caseDir, "spec.json")) || !limitsExists(filepath.Join(caseDir, "live.lock")) {
			fail("fixture_removed_before_finalization")
		}
		var res limitsResult
		if b, err := os.ReadFile(filepath.Join(caseDir, "result.json")); err != nil || json.Unmarshal(b, &res) != nil ||
			!res.Aborted || res.FinalizeKind != "token" || !res.Rescued || !res.FinalReturned || !res.FinalGone {
			fail("controller_not_completed")
		}
		if b, err := os.ReadFile(filepath.Join(caseDir, "controller.log")); err != nil || len(b) != 0 {
			fail("controller_output")
		}
		if len(finals) != 1 || !finals[0].reconciled() || !finals[0].Cooperative || finals[0].Killed {
			fail("finalizer_record")
		}
	}
	_ = os.WriteFile(probe("release"), nil, 0o600)
	if !limitsWait(e.done, limitsEarlyExit) {
		fail("child_not_exited")
		return r
	}
	r.ExitAt = time.Since(e.started)
	var ee *exec.ExitError
	if !errors.As(e.waitErr, &ee) || ee.ExitCode() != 1 {
		fail("child_exit")
	}
	log, _ := os.ReadFile(filepath.Join(e.dir, "child.log"))
	if !bytes.Contains(log, []byte(limitsEarlyFailure)) || !bytes.Contains(log, []byte("--- FAIL: TestLimits")) {
		fail("no_deliberate_failure")
	}
	for _, bad := range []string{"unreconciled", "retained", "not joined", "did not complete", "panic:"} {
		if bytes.Contains(log, []byte(bad)) {
			fail("child_reported_" + strings.ReplaceAll(bad, " ", "_"))
		}
	}
	if caseDir != "" && limitsExists(filepath.Dir(caseDir)) {
		fail("fixture_root_not_removed")
	}
	return r
}

// finalize releases the child, lets it run its own finalization, and kills it
// through its owned handle only after its descendant and leader were observed
// gone; otherwise Deadline records the unreconciled state.
func (e *limitsEarly) finalize() limitsFinal {
	var f limitsFinal
	_ = os.WriteFile(filepath.Join(e.probe, "observed"), nil, 0o600)
	_ = os.WriteFile(filepath.Join(e.probe, "release"), nil, 0o600)
	if !limitsWait(e.done, 0) {
		f.Requested = true
		f.Cooperative = limitsWait(e.done, limitsEarlyFinal+limitsEarlyExit)
	}
	e.mu.Lock()
	lock, leader := e.lock, e.leader
	e.mu.Unlock()
	f.DescendantGone = lock != nil && limitsFdFree(lock)
	f.LeaderGone = leader > 1 && limitsPidGone(leader)
	if !limitsWait(e.done, 0) {
		if f.DescendantGone && f.LeaderGone {
			f.Killed = true
			_ = e.cmd.Process.Kill()
		} else {
			f.Deadline = true
		}
	}
	f.Reaped = limitsWait(e.done, limitsReap)
	if f.reconciled() {
		e.mu.Lock()
		e.lock = nil
		e.mu.Unlock()
		lock.Close()
	}
	return f
}

// failEarly is the regression child's deliberate abrupt failure: once its
// first controller's descendant acknowledged the observer's nonce and the
// observer saw it, the test fails with the remaining cases never started.
func (cs *limitsCases) failEarly(t *testing.T, c *limitsCase) {
	t.Helper()
	ack := false
	for deadline := c.started.Add(limitsEarlyReady); !ack && time.Now().Before(deadline); time.Sleep(limitsPoll) {
		b, _ := os.ReadFile(filepath.Join(c.dir, "ack"))
		f := strings.Fields(string(b))
		ack = len(f) == 3 && f[0] == c.spec.Nonce
	}
	observed := false
	for deadline := time.Now().Add(limitsCoop); !observed && time.Now().Before(deadline); time.Sleep(limitsPoll) {
		observed = limitsExists(filepath.Join(cs.probe, "observed"))
	}
	t.Fatalf("%s (ack=%t observed=%t)", limitsEarlyFailure, ack, observed)
}

// handshake, in the regression child, publishes the finalization records and
// waits, bounded, for the observer's release before the root may be removed.
func (cs *limitsCases) handshake(t *testing.T, finals []limitsFinal) {
	b, err := json.Marshal(finals)
	if err != nil {
		t.Error(err)
		return
	}
	tmp := filepath.Join(cs.probe, "finalized.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		t.Error(err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(cs.probe, "finalized")); err != nil {
		t.Error(err)
		return
	}
	for deadline := time.Now().Add(limitsCoop); !limitsExists(filepath.Join(cs.probe, "release")) && time.Now().Before(deadline); {
		time.Sleep(limitsPoll)
	}
}

// finish joins every controller within its bound and applies the shared
// acceptance oracle to the observation each controller fixed before rescue,
// then checks every finalization case.
func (cs *limitsCases) finish(t *testing.T) {
	t.Helper()
	cs.mu.Lock()
	list := append([]*limitsCase(nil), cs.cs...)
	cs.mu.Unlock()
	ackPids := map[string]bool{}
	for _, c := range list {
		if c.final != "" {
			continue
		}
		bound := c.spec.Upper + c.spec.Observe + c.spec.Rescue + time.Second
		if !limitsWait(c.done, time.Until(c.started.Add(bound))) {
			t.Errorf("%s: controller exceeded %v: %+v", c.spec.Name, bound, c.finalize())
		}
		if c.waitErr != nil {
			log, _ := os.ReadFile(filepath.Join(c.dir, "controller.log"))
			t.Errorf("%s: controller failed: %v %q", c.spec.Name, c.waitErr, log)
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.dir, "result.json"))
		if err != nil {
			t.Errorf("%s: no controller result: %v", c.spec.Name, err)
			continue
		}
		var res limitsResult
		if err := json.Unmarshal(data, &res); err != nil {
			t.Errorf("%s: %v", c.spec.Name, err)
			continue
		}
		o := res.Obs
		verdict := limitsVerdict(c.spec, o)
		t.Logf("%s: configured GitTimeout=%v Total=%v; accepted elapsed [%v, %v], ready window %v, absence observation %v; invocation=%v ready=%v gone=%v ack_present=%t leader_present=%t verdict_before_rescue=%v rescued=%t",
			c.spec.Name, c.spec.GitTimeout, c.spec.Total, c.spec.Lower, c.spec.Upper, c.spec.ReadyWindow, c.spec.Observe,
			o.Invocation, o.AckAt, o.GoneAt, o.Ack != "", o.Leader != "", res.Verdict, res.Rescued)
		if !reflect.DeepEqual(verdict, res.Verdict) {
			t.Errorf("%s: controller verdict %v differs from oracle %v", c.spec.Name, res.Verdict, verdict)
		}
		if len(c.want) == 0 && len(verdict) != 0 || len(c.want) != 0 && !reflect.DeepEqual(verdict, c.want) {
			t.Errorf("%s: verdict %v, want %v", c.spec.Name, verdict, c.want)
		}
		if res.Aborted {
			t.Errorf("%s: finalization was requested before completion", c.spec.Name)
		}
		if len(c.want) == 0 {
			if res.Rescued {
				t.Errorf("%s: accepted case needed rescue", c.spec.Name)
			}
			if f := strings.Fields(o.Ack); len(f) == 3 {
				if ackPids[f[1]] {
					t.Errorf("%s: descendant identity reused", c.spec.Name)
				}
				ackPids[f[1]] = true
			}
		} else if (!o.Returned || !o.Gone) && !res.Rescued {
			t.Errorf("%s: live control was not rescued", c.spec.Name)
		}
		if !res.FinalReturned || !res.FinalGone {
			t.Errorf("%s: not joined: returned=%t gone=%t", c.spec.Name, res.FinalReturned, res.FinalGone)
		}
	}
	for _, c := range list {
		if c.final == "" {
			continue
		}
		var d limitsDrive
		timer := time.NewTimer(time.Until(c.started.Add(limitsDriveBound + limitsReap)))
		select {
		case d = <-c.drv:
		case <-timer.C:
			timer.Stop()
			t.Errorf("%s: finalization case did not complete", c.spec.Name)
			continue
		}
		timer.Stop()
		f := d.Final
		t.Logf("%s: ack_seen=%t live_before=%t controller_exit=%d final=%+v lock_free_after=%t result_present=%t",
			c.spec.Name, d.AckSeen, d.LiveBefore, d.ControllerExit, f, d.LockFreeAfter, d.Result != nil)
		var want limitsFinal
		ok := d.LiveBefore && d.LockFreeAfter && f.reconciled()
		switch c.final {
		case "ready_missing":
			// Readiness never arrived; the controller exits cooperatively
			// after rescuing and joining its own invocation.
			want = limitsFinal{Requested: true, Cooperative: true, Returned: true, DescendantGone: true, LeaderGone: true, Reaped: true}
			ok = ok && !d.AckSeen && d.Result != nil && d.Result.Aborted && d.Result.FinalizeKind == "token" &&
				d.Result.Rescued && d.Result.FinalReturned && d.Result.FinalGone
		case "controller_failure":
			// The controller died leaving its descendant live; the parent
			// rescues it and observes it and the leader gone.
			want = limitsFinal{ParentRescue: true, DescendantGone: true, LeaderGone: true, Reaped: true}
			ok = ok && d.AckSeen && d.ControllerExit == 5 && d.Result == nil
		case "overrun":
			// The controller ignores the request; the descendant is rescued
			// and RunWithLimits returns before the controller is killed.
			want = limitsFinal{Requested: true, ParentRescue: true, Returned: true, DescendantGone: true, LeaderGone: true, Killed: true, Reaped: true}
			ok = ok && d.AckSeen && d.Result == nil
		}
		if !ok || f != want {
			t.Errorf("%s: finalization %+v, want %+v (observations %+v)", c.spec.Name, f, want, d)
		}
	}
	if e := cs.early; e != nil {
		if !limitsWait(e.drvDone, time.Until(e.started.Add(limitsEarlyBound+limitsReap))) {
			t.Errorf("early_failure_regression: observer did not complete within %v", limitsEarlyBound+limitsReap)
			return
		}
		r := e.rep
		t.Logf("early_failure_regression: ack=%v finalized=%v child_exit=%v problems=%v", r.AckAt, r.FinalizedAt, r.ExitAt, r.Problems)
		if len(r.Problems) != 0 {
			t.Errorf("early_failure_regression: %v", r.Problems)
		}
	}
}

// checkLimitsBudget enforces the frozen ceiling for the whole test,
// including setup, controls, rescue and every registered finalization.
func checkLimitsBudget(t *testing.T) {
	start := time.Now()
	t.Cleanup(func() {
		d := time.Since(start)
		t.Logf("TestLimits elapsed %v (ceiling %v)", d, limitsParentBudget)
		if d > limitsParentBudget {
			t.Errorf("TestLimits took %v, over the %v ceiling", d, limitsParentBudget)
		}
	})
}
