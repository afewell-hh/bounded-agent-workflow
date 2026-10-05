package review

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/execution"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// File limits.
const (
	MaxPlanBytes    = 65536
	MaxReceiptBytes = 65536
)

// Command is the validated reviewer command.
type Command = execution.Command

// readHook is an internal test seam at a safe file's open ("NAME-open",
// after the positive Lstat and before the open), read ("NAME-read", after
// the bytes were read) and close ("NAME-close", after the actual close)
// boundaries, where NAME is plan, intent or result. A returned error is
// treated as that operation's failure. Production code never sets it.
var readHook func(stage string) error

func readAt(stage string) error {
	if readHook != nil {
		return readHook(stage)
	}
	return nil
}

// physical resolves ancestor aliases and keeps the final name.
func physical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	if parent == abs {
		return abs, nil
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

func fileSafety(fi fs.FileInfo) error {
	switch {
	case fi.Mode()&fs.ModeSymlink != 0 || !fi.Mode().IsRegular():
		return fail(CodeUnsafeStatePath)
	case !private(fi, 0o600):
		return fail(CodeStatePermissions)
	}
	return nil
}

func owned(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}

func private(fi fs.FileInfo, perm fs.FileMode) bool {
	return owned(fi) && fi.Mode().Perm() == perm &&
		fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0
}

// safeRead reads one private regular file and applies parse before the
// descriptor is closed. The Lstat and the opened descriptor's type, owner and
// mode are checked before its identity; the first applicable failure wins.
// The descriptor is always closed, and a close failure is reported only when
// every earlier step (safety, read, size and parse) succeeded. missing is the
// code for an initially absent file; unavailable for other I/O and close
// failures; tooLarge for more than limit bytes.
func safeRead(path, name string, limit int, missing, unavailable, tooLarge Code, parse func([]byte) error) ([]byte, error) {
	phys, err := physical(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fail(missing)
		}
		return nil, fail(unavailable)
	}
	lfi, err := os.Lstat(phys)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fail(missing)
		}
		return nil, fail(unavailable)
	}
	if err := fileSafety(lfi); err != nil {
		return nil, err
	}
	if err := readAt(name + "-open"); err != nil {
		return nil, fail(unavailable)
	}
	f, err := os.OpenFile(phys, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) {
			return nil, fail(CodeStateChanged)
		}
		return nil, fail(unavailable)
	}
	observe(f)
	data, perr := readOpened(f, lfi, name, limit, unavailable, tooLarge)
	if perr == nil {
		perr = parse(data)
	}
	cerr := f.Close()
	if cerr == nil {
		cerr = readAt(name + "-close")
	}
	if perr != nil {
		return nil, perr
	}
	if cerr != nil {
		return nil, fail(unavailable)
	}
	return data, nil
}

func readOpened(f *os.File, lfi fs.FileInfo, name string, limit int, unavailable, tooLarge Code) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, fail(unavailable)
	}
	if err := fileSafety(fi); err != nil {
		return nil, err
	}
	if !os.SameFile(fi, lfi) {
		return nil, fail(CodeStateChanged)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err == nil {
		err = readAt(name + "-read")
	}
	if err != nil {
		return nil, fail(unavailable)
	}
	if len(data) > limit {
		return nil, fail(tooLarge)
	}
	return data, nil
}

// ParsePlan strictly validates review plan bytes: UTF-8, one object, no
// duplicate decoded names, exactly schema_version literal 1 and reviewer.
func ParsePlan(data []byte) (Command, error) {
	if len(data) > MaxPlanBytes {
		return Command{}, fail(CodeInvalidPlan)
	}
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "reviewer") || !isOne(m["schema_version"]) {
		return Command{}, fail(CodeInvalidPlan)
	}
	c, ok := execution.PlanCommand(m["reviewer"])
	if !ok {
		return Command{}, fail(CodeInvalidPlan)
	}
	return c, nil
}

// ReadPlan safely reads and parses the review plan.
func ReadPlan(path string) ([]byte, Command, error) {
	var c Command
	data, err := safeRead(path, "plan", MaxPlanBytes, CodePlanUnavailable, CodePlanUnavailable, CodeInvalidPlan,
		func(b []byte) error {
			var err error
			c, err = ParsePlan(b)
			return err
		})
	if err != nil {
		return nil, Command{}, err
	}
	return data, c, nil
}

// execIntent and execResult are the fields of the execution receipts used
// for consistency and eligibility, decoded only after strict validation.
type execIntent struct {
	RunID                  string `json:"run_id"`
	TicketURL              string `json:"ticket_url"`
	ScopeSHA256            string `json:"scope_sha256"`
	PolicyCommit           string `json:"policy_commit"`
	RepositoryObjectFormat string `json:"repository_object_format"`
	RepositoryHead         string `json:"repository_head"`
	CreatedAt              string `json:"created_at"`
}

type execProgram struct {
	State    string `json:"state"`
	ExitCode *int   `json:"exit_code"`
}

type execResult struct {
	RunID        string      `json:"run_id"`
	Outcome      string      `json:"outcome"`
	Worker       execProgram `json:"worker"`
	Verification execProgram `json:"verification"`
	Repository   struct {
		ObjectFormat string  `json:"object_format"`
		BeforeHead   string  `json:"before_head"`
		AfterHead    *string `json:"after_head"`
	} `json:"repository"`
	CreatedAt   string `json:"created_at"`
	CompletedAt string `json:"completed_at"`
}

// ownID extracts and intrinsically validates a receipt's own run_id. Size
// and UTF-8 validity are checked first, then the strict single-object
// decoding with duplicate refusal.
func ownID(data []byte) (string, bool) {
	if len(data) > MaxReceiptBytes {
		return "", false
	}
	m, ok := strictObject(data)
	if !ok {
		return "", false
	}
	id, ok := m["run_id"].(string)
	return id, ok && runIDRE.MatchString(id)
}

// parseExecIntent validates the execution intent intrinsically against its
// own run_id with the unchanged execute validator.
func parseExecIntent(data []byte) (execIntent, error) {
	var in execIntent
	id, ok := ownID(data)
	if !ok || !execution.ValidIntentReceipt(data, id) || json.Unmarshal(data, &in) != nil {
		return in, fail(CodeInvalidReceipt)
	}
	return in, nil
}

// parseExecResult validates the execution result intrinsically against its
// own run_id with the unchanged execute validator, then requires
// completed_at not to precede created_at chronologically.
func parseExecResult(data []byte) (execResult, error) {
	var r execResult
	id, ok := ownID(data)
	if !ok || !execution.ValidResultReceipt(data, id) || json.Unmarshal(data, &r) != nil ||
		!notBefore(r.CompletedAt, r.CreatedAt) {
		return r, fail(CodeInvalidReceipt)
	}
	return r, nil
}

// notBefore reports whether canonical timestamp a is not earlier than b.
func notBefore(a, b string) bool {
	ta, err1 := time.Parse(state.TimeLayout, a)
	tb, err2 := time.Parse(state.TimeLayout, b)
	return err1 == nil && err2 == nil && !ta.Before(tb)
}

// readReceipt safely reads one execution receipt and parses it before close.
func readReceipt(path, name string, parse func([]byte) error) ([]byte, error) {
	return safeRead(path, name, MaxReceiptBytes, CodeReceiptUnavailable, CodeReceiptUnavailable, CodeInvalidReceipt, parse)
}
