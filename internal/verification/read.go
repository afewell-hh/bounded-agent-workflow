package verification

import (
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

// MaxPlanBytes limits the plan file, including whitespace.
const MaxPlanBytes = 65536

// Command is the validated verifier command.
type Command = execution.Command

// readHook is an internal test seam at a safe file's open ("NAME-open",
// after the positive Lstat and before the open), read ("NAME-read", after
// the bytes were read) and close ("NAME-close", after the actual close)
// boundaries, where NAME is plan. A returned error is
// treated as that operation's failure. Production code never sets it.
var readHook func(stage string) error

// openFile is an internal test seam replacing the reader's open of the
// physical path after the positive Lstat; it receives that path and the exact
// open flags. Production code never sets it and calls os.OpenFile.
var openFile func(path string, flag int) (*os.File, error)

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
// code for an initially absent file; unavailable for other I/O failures;
// closeFailed for a close-only failure; tooLarge for more than limit bytes.
// A parse error is returned unchanged.
func safeRead(path, name string, limit int, missing, unavailable, closeFailed, tooLarge Code, parse func([]byte) error) ([]byte, error) {
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
	flag := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	var f *os.File
	if openFile != nil {
		f, err = openFile(phys, flag)
	} else {
		f, err = os.OpenFile(phys, flag, 0)
	}
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
		return nil, fail(closeFailed)
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

// ParsePlan strictly validates verification plan bytes: UTF-8, one object, no
// duplicate decoded names, exactly schema_version literal 1 and verifier.
func ParsePlan(data []byte) (Command, error) {
	if len(data) > MaxPlanBytes {
		return Command{}, fail(CodeInvalidPlan)
	}
	m, ok := strictObject(data)
	if !ok || !exactKeys(m, "schema_version", "verifier") || !isOne(m["schema_version"]) {
		return Command{}, fail(CodeInvalidPlan)
	}
	c, ok := execution.PlanCommand(m["verifier"])
	if !ok {
		return Command{}, fail(CodeInvalidPlan)
	}
	return c, nil
}

// ReadPlan safely reads and parses the verification plan.
func ReadPlan(path string) ([]byte, Command, error) {
	var c Command
	data, err := safeRead(path, "plan", MaxPlanBytes, CodePlanUnavailable, CodePlanUnavailable, CodePlanUnavailable,
		CodeInvalidPlan, func(b []byte) error {
			var err error
			c, err = ParsePlan(b)
			return err
		})
	if err != nil {
		return nil, Command{}, err
	}
	return data, c, nil
}

// notBefore reports whether canonical timestamp a is not earlier than b.
func notBefore(a, b string) bool {
	ta, err1 := time.Parse(state.TimeLayout, a)
	tb, err2 := time.Parse(state.TimeLayout, b)
	return err1 == nil && err2 == nil && !ta.Before(tb)
}
