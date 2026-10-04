// Package execution implements `baw run execute`: one trusted local worker
// followed by one verification program in an exclusive, durably recorded
// attempt. It is a local process primitive. It evaluates no approval, holds
// no credentials, provides no native agent adapter and never retries,
// resumes or recovers an attempt.
package execution

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"
)

// Code is a fixed, safe failure code published as `baw: CODE`.
type Code string

const (
	CodeUsage              Code = "invalid_usage"
	CodePlanUnavailable    Code = "plan_unavailable"
	CodeInvalidPlan        Code = "invalid_execution_plan"
	CodeCheckpointMismatch Code = "execution_checkpoint_mismatch"
	CodeUnsafeLayout       Code = "unsafe_execution_layout"
	CodeExecUnavailable    Code = "executable_unavailable"
	CodeExists             Code = "execution_exists"
	CodeStorageUnavailable Code = "execution_storage_unavailable"
	CodeUncertain          Code = "execution_uncertain"
	CodeFailed             Code = "execution_failed"
	CodeOutputLimit        Code = "output_limit"
	CodeOutputUnavailable  Code = "output_unavailable"
	CodeCancelled          Code = "execution_cancelled"
	// Existing state-contract codes reused with their existing meaning.
	CodeUnsafeStatePath  Code = "unsafe_state_path"
	CodeStatePermissions Code = "state_permissions"
	CodeStateChanged     Code = "state_changed"
	CodeStateUnavailable Code = "state_unavailable"
	CodeDurability       Code = "durability_unavailable"
)

// Error carries only a fixed code; no paths, values or OS error text.
type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }

func fail(c Code) error { return &Error{Code: c} }

// Plan limits.
const (
	MaxPlanBytes    = 65536
	MaxArguments    = 64
	MaxArgumentSize = 1024
	MaxTimeout      = 300
)

// Command is one validated plan command.
type Command struct {
	Executable     string
	Arguments      []string
	TimeoutSeconds int
}

// Plan is a validated schema version 1 execution plan.
type Plan struct {
	Worker       Command
	Verification Command
}

var rawPositiveRE = regexp.MustCompile(`^[1-9][0-9]*$`)

// ParsePlan strictly validates plan bytes: valid UTF-8, one JSON object, no
// duplicate decoded member names at any level, no trailing value, exact keys.
func ParsePlan(data []byte) (Plan, error) {
	var p Plan
	if len(data) > MaxPlanBytes || !utf8.Valid(data) {
		return p, fail(CodeInvalidPlan)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := strictValue(dec)
	if err != nil {
		return p, fail(CodeInvalidPlan)
	}
	if _, err := dec.Token(); err != io.EOF {
		return p, fail(CodeInvalidPlan)
	}
	top, ok := v.(map[string]any)
	if !ok || !exactKeys(top, "schema_version", "worker", "verification") {
		return p, fail(CodeInvalidPlan)
	}
	if n, ok := top["schema_version"].(json.Number); !ok || string(n) != "1" {
		return p, fail(CodeInvalidPlan)
	}
	if p.Worker, ok = command(top["worker"]); !ok {
		return p, fail(CodeInvalidPlan)
	}
	if p.Verification, ok = command(top["verification"]); !ok {
		return p, fail(CodeInvalidPlan)
	}
	return p, nil
}

func exactKeys(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}

func command(v any) (Command, bool) {
	var c Command
	m, ok := v.(map[string]any)
	if !ok || !exactKeys(m, "executable", "arguments", "timeout_seconds") {
		return c, false
	}
	exe, ok := m["executable"].(string)
	if !ok || exe == "" || !filepath.IsAbs(exe) || strings.ContainsRune(exe, 0) {
		return c, false
	}
	args, ok := m["arguments"].([]any)
	if !ok || len(args) > MaxArguments {
		return c, false
	}
	c.Arguments = []string{}
	for _, a := range args {
		s, ok := a.(string)
		if !ok || len(s) > MaxArgumentSize || strings.ContainsRune(s, 0) {
			return c, false
		}
		c.Arguments = append(c.Arguments, s)
	}
	n, ok := m["timeout_seconds"].(json.Number)
	if !ok || !rawPositiveRE.MatchString(string(n)) || len(n) > 3 {
		return c, false
	}
	t, err := strconv.Atoi(string(n))
	if err != nil || t < 1 || t > MaxTimeout {
		return c, false
	}
	c.Executable, c.TimeoutSeconds = exe, t
	return c, true
}

var errPlan = errors.New("invalid plan")

// strictValue decodes one JSON value, refusing duplicate decoded names.
func strictValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, errPlan
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			m := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, errPlan
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errPlan
				}
				if _, dup := m[k]; dup {
					return nil, errPlan
				}
				v, err := strictValue(dec)
				if err != nil {
					return nil, errPlan
				}
				m[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, errPlan
			}
			return m, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := strictValue(dec)
				if err != nil {
					return nil, errPlan
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, errPlan
			}
			return a, nil
		}
		return nil, errPlan
	default:
		return t, nil
	}
}

// planHook is an internal test seam at the plan file open ("open", after the
// safe Lstat and before the open), read ("read", after the bytes were read)
// and close ("close", after the actual close) boundaries; a returned error is
// treated as that operation's failure. Production code never sets it.
var planHook func(stage string) error

func planAt(stage string) error {
	if planHook != nil {
		return planHook(stage)
	}
	return nil
}

// ReadPlan safely reads and parses the plan file. Safety checks precede the
// identity check on the opened file; the first applicable failure wins. The
// descriptor is always closed, and a close error is reported only when the
// safety checks, the read and the parser all succeeded.
func ReadPlan(path string) ([]byte, Plan, error) {
	var p Plan
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, p, fail(CodePlanUnavailable)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, p, fail(CodePlanUnavailable)
	}
	phys := filepath.Join(parent, filepath.Base(abs))
	lfi, err := os.Lstat(phys)
	if err != nil {
		return nil, p, fail(CodePlanUnavailable)
	}
	if err := planSafety(lfi); err != nil {
		return nil, p, err
	}
	if err := planAt("open"); err != nil {
		return nil, p, fail(CodePlanUnavailable)
	}
	f, err := os.OpenFile(phys, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) {
			return nil, p, fail(CodeStateChanged)
		}
		return nil, p, fail(CodePlanUnavailable)
	}
	observe(f)
	data, perr := readOpened(f, lfi)
	if perr == nil {
		p, perr = ParsePlan(data)
	}
	cerr := f.Close()
	if cerr == nil {
		cerr = planAt("close")
	}
	if perr != nil {
		return nil, Plan{}, perr
	}
	if cerr != nil {
		return nil, Plan{}, fail(CodePlanUnavailable)
	}
	return data, p, nil
}

func readOpened(f *os.File, lfi fs.FileInfo) ([]byte, error) {
	fi, err := f.Stat()
	if err != nil {
		return nil, fail(CodePlanUnavailable)
	}
	if err := planSafety(fi); err != nil {
		return nil, err
	}
	if !os.SameFile(fi, lfi) {
		return nil, fail(CodeStateChanged)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxPlanBytes+1))
	if err == nil {
		err = planAt("read")
	}
	if err != nil {
		return nil, fail(CodePlanUnavailable)
	}
	if len(data) > MaxPlanBytes {
		return nil, fail(CodeInvalidPlan)
	}
	return data, nil
}

func planSafety(fi fs.FileInfo) error {
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
