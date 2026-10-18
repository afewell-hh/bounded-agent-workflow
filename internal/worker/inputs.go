package worker

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
)

// Input limits.
const (
	MaxPlanBytes        = 65536
	MaxTaskBytes        = 65536
	MaxTaskText         = 16384
	MaxInstructions     = 32
	MaxInstructionBytes = 65536
	MaxAllowedChanges   = 128
	MaxPathBytes        = 1024
	MaxPacketBytes      = 262144
)

// Plan is a validated schema version 1 worker plan.
type Plan struct {
	TaskFile    string
	ProfileFile string
}

// Instruction is one task instruction entry.
type Instruction struct {
	Path    string `json:"path"`
	BlobOID string `json:"blob_oid"`
	SHA256  string `json:"sha256"`
}

// Task is a validated schema version 1 task file.
type Task struct {
	TaskID         string
	ScopeSHA256    string
	BaseCommit     string
	Text           string
	Instructions   []Instruction
	AllowedChanges []string
}

var (
	runIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hex64RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	oid40RE = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

func oidFor(format, s string) bool {
	switch format {
	case "sha1":
		return oid40RE.MatchString(s)
	case "sha256":
		return hex64RE.MatchString(s)
	}
	return false
}

// ParsePlan strictly validates plan bytes.
func ParsePlan(data []byte) (Plan, error) {
	var p Plan
	if len(data) > MaxPlanBytes {
		return p, fail(CodeInvalidPlan)
	}
	m, ok := claude.StrictObject(data)
	if !ok || !claude.ExactKeys(m, "schema_version", "task_file", "profile_file") || !claude.IsOne(m["schema_version"]) {
		return p, fail(CodeInvalidPlan)
	}
	p.TaskFile, _ = m["task_file"].(string)
	p.ProfileFile, _ = m["profile_file"].(string)
	if !absPath(p.TaskFile) || !absPath(p.ProfileFile) {
		return Plan{}, fail(CodeInvalidPlan)
	}
	return p, nil
}

func absPath(p string) bool {
	return p != "" && len(p) <= MaxPathBytes && filepath.IsAbs(p) && !strings.ContainsRune(p, 0)
}

var controlBase = map[string]bool{"claude.md": true, "claude.local.md": true, "agents.md": true, ".mcp.json": true,
	".gitignore": true, ".gitattributes": true, ".gitmodules": true}

const globChars = "*?[]{}\\"

// RelPath reports whether p is a literal repository-relative file path: valid
// UTF-8, at most 1024 bytes, no NUL/control or glob characters, not absolute,
// no empty, "." or ".." component and no ".git" component.
func RelPath(p string) bool {
	if p == "" || len(p) > MaxPathBytes || !utf8.ValidString(p) || strings.HasPrefix(p, "/") ||
		strings.ContainsAny(p, globChars) {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") {
			return false
		}
	}
	return true
}

// ruleWorkspace rejects characters interpreted as native permission-rule syntax.
// The frozen argv requires a literal workspace prefix, not a caller pattern.
func ruleWorkspace(p string) bool {
	if !utf8.ValidString(p) || strings.ContainsAny(p, globChars+"()") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// controlPath reports a native-source or Git control path that a worker may
// never be allowed to change.
func controlPath(p string) bool {
	parts := strings.Split(p, "/")
	for _, c := range parts {
		if strings.EqualFold(c, ".claude") {
			return true
		}
	}
	return controlBase[strings.ToLower(parts[len(parts)-1])]
}

// ParseTask strictly validates task bytes against the run ID and record.
func ParseTask(data []byte, runID, scope, base, format string) (Task, error) {
	var t Task
	bad := fail(CodeInvalidPlan)
	if len(data) > MaxTaskBytes {
		return t, bad
	}
	m, ok := claude.StrictObject(data)
	if !ok || !claude.ExactKeys(m, "schema_version", "task_id", "scope_sha256", "base_commit", "task",
		"instructions", "allowed_changes") || !claude.IsOne(m["schema_version"]) {
		return t, bad
	}
	t.TaskID, _ = m["task_id"].(string)
	t.ScopeSHA256, _ = m["scope_sha256"].(string)
	t.BaseCommit, _ = m["base_commit"].(string)
	t.Text, _ = m["task"].(string)
	if t.TaskID != runID || t.ScopeSHA256 != scope || t.BaseCommit != base || !oidFor(format, base) ||
		t.Text == "" || len(t.Text) > MaxTaskText {
		return Task{}, bad
	}
	ins, ok := m["instructions"].([]any)
	if !ok || len(ins) < 1 || len(ins) > MaxInstructions {
		return Task{}, bad
	}
	seen := map[string]bool{}
	for _, v := range ins {
		e, ok := v.(map[string]any)
		if !ok || !claude.ExactKeys(e, "path", "blob_oid", "sha256") {
			return Task{}, bad
		}
		var i Instruction
		i.Path, _ = e["path"].(string)
		i.BlobOID, _ = e["blob_oid"].(string)
		i.SHA256, _ = e["sha256"].(string)
		if !RelPath(i.Path) || seen[i.Path] || !oidFor(format, i.BlobOID) || !hex64RE.MatchString(i.SHA256) {
			return Task{}, bad
		}
		seen[i.Path] = true
		t.Instructions = append(t.Instructions, i)
	}
	ac, ok := m["allowed_changes"].([]any)
	if !ok || len(ac) < 1 || len(ac) > MaxAllowedChanges {
		return Task{}, bad
	}
	allowed := map[string]bool{}
	for _, v := range ac {
		p, ok := v.(string)
		if !ok || !RelPath(p) || allowed[p] || seen[p] || controlPath(p) {
			return Task{}, bad
		}
		allowed[p] = true
		t.AllowedChanges = append(t.AllowedChanges, p)
	}
	return t, nil
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

func owned(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}

func private(fi fs.FileInfo, perm fs.FileMode) bool {
	return owned(fi) && fi.Mode().Perm() == perm &&
		fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0
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

// readPrivateInfo safely reads one private 0600 regular file outside the
// worktree without following a final link or blocking on a FIFO. Safety is
// checked on the Lstat and on the opened descriptor before identity. A close
// failure is reported only when everything earlier succeeded. unavailable is
// the code for absent or unreadable input; tooLarge for more than limit bytes.
// It also returns the opened file's metadata, so a later recheck can compare
// its identity (device and inode) and mode independently of its bytes.
func readPrivateInfo(path string, limit int, unavailable, tooLarge Code) ([]byte, string, fs.FileInfo, error) {
	phys, err := physical(path)
	if err != nil {
		return nil, "", nil, fail(unavailable)
	}
	lfi, err := os.Lstat(phys)
	if err != nil {
		return nil, "", nil, fail(unavailable)
	}
	if err := fileSafety(lfi); err != nil {
		return nil, "", nil, err
	}
	f, err := os.OpenFile(phys, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ELOOP) {
			return nil, "", nil, fail(CodeStateChanged)
		}
		return nil, "", nil, fail(unavailable)
	}
	var ofi fs.FileInfo
	data, rerr := func() ([]byte, error) {
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
		ofi = fi
		data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
		if err != nil {
			return nil, fail(unavailable)
		}
		if len(data) > limit {
			return nil, fail(tooLarge)
		}
		return data, nil
	}()
	cerr := f.Close()
	if rerr != nil {
		return nil, "", nil, rerr
	}
	if cerr != nil {
		return nil, "", nil, fail(unavailable)
	}
	return data, phys, ofi, nil
}

// readWorktreeFile reads one regular file below the canonical worktree w
// without following any link on its repository-relative chain. ok is false
// for absence; err is non-nil for anything unsafe, unreadable or oversized.
func readWorktreeFile(w, rel string, limit int) (data []byte, fi fs.FileInfo, ok bool, err error) {
	parts := strings.Split(rel, "/")
	cur := w
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		lfi, err := os.Lstat(cur)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, false, nil
		}
		if err != nil || lfi.Mode()&fs.ModeSymlink != 0 {
			return nil, nil, false, errUnsafe
		}
		if i < len(parts)-1 {
			if !lfi.IsDir() {
				return nil, nil, false, errUnsafe
			}
			continue
		}
		if !lfi.Mode().IsRegular() || lfi.Size() > int64(limit) {
			return nil, nil, false, errUnsafe
		}
		fi = lfi
	}
	f, err := os.OpenFile(cur, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, false, errUnsafe
	}
	defer f.Close()
	ofi, err := f.Stat()
	if err != nil || !ofi.Mode().IsRegular() || !os.SameFile(ofi, fi) {
		return nil, nil, false, errUnsafe
	}
	data, err = io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, nil, false, errUnsafe
	}
	return data, ofi, true, nil
}

var errUnsafe = errors.New("unsafe worktree file")
