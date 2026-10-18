package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// MaxSourcePaths bounds the concrete deduplicated matrix path list.
const MaxSourcePaths = 256

// AdminRoot is the admin-managed Claude Code root.
const AdminRoot = "/Library/Application Support/ClaudeCode"

// Source matrix classes (rows of claude-2.1.285-source-v1).
const (
	ClassEntry     = "entry"
	ClassScoped    = "scoped_settings"
	ClassCustom    = "scoped_custom"
	ClassUser      = "user"
	ClassAlternate = "alternate_config"
	ClassMemory    = "project_memory"
	ClassMutable   = "native_state"
	ClassAdmin     = "admin_managed"
)

// Kinds and statuses.
const (
	KindFile     = "F"
	KindDir      = "D"
	StatusAbsent = "absent"
	StatusSafe   = "present_safe"
)

// ErrSourceUnavailable is a fixed matrix failure: an unsafe, unsupported,
// unavailable or over-bound source.
var ErrSourceUnavailable = errors.New("source_profile_unavailable")

// SourcePath is one concrete matrix path. Base is the directory from which
// the suffix chain must contain no symlink.
type SourcePath struct {
	Path  string
	Base  string
	Class string
	Kind  string
}

// SourceEntry is one observed matrix path. Dev and Ino are private.
type SourceEntry struct {
	Path   string  `json:"path"`
	Class  string  `json:"class"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	UID    *uint32 `json:"uid"`
	Mode   *uint32 `json:"mode"`
	Dev    *uint64 `json:"dev"`
	Ino    *uint64 `json:"ino"`
}

var (
	entryFiles  = []string{"CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", ".claude/CLAUDE.md"}
	scopedFiles = []string{".claude/settings.json", ".claude/settings.local.json", ".mcp.json"}
	customDirs  = []string{".claude/rules", ".claude/skills", ".claude/commands", ".claude/agents", ".claude/plugins", ".claude/memory"}
	userFiles   = []string{".claude/CLAUDE.md", ".claude/CLAUDE.local.md", ".claude/settings.json", ".claude/settings.local.json", ".claude/.mcp.json"}
	altFiles    = []string{".config/claude/CLAUDE.md", ".config/claude/settings.json", ".config/claude/settings.local.json"}
	altDirs     = []string{".config/claude/rules", ".config/claude/skills", ".config/claude/plugins", ".config/claude/memory"}
	adminFiles  = []string{"managed-settings.json", "managed-mcp.json", "CLAUDE.md"}
	adminDirs   = []string{"managed-settings.d", "rules", "skills", "plugins", "agents", "memory"}
)

// Ancestors returns w and every physical ancestor up to "/", root first.
func Ancestors(w string) []string {
	var a []string
	for d := w; ; d = filepath.Dir(d) {
		a = append(a, d)
		if d == "/" {
			break
		}
	}
	for i, j := 0, len(a)-1; i < j; i, j = i+1, j-1 {
		a[i], a[j] = a[j], a[i]
	}
	return a
}

// EncodedProject is the project auto-memory directory name for w: the
// absolute path with each '/' replaced by '-'.
func EncodedProject(w string) string { return strings.ReplaceAll(w, "/", "-") }

// Matrix generates the deduplicated concrete path list for home h, canonical
// worktree w and nested directories n (repository-relative, from the launch
// tree). The first occurrence of a duplicate path keeps its row. It fails
// above MaxSourcePaths.
func Matrix(h, w string, n []string) ([]SourcePath, error) {
	var out []SourcePath
	seen := map[string]bool{}
	add := func(base, suffix, class, kind string) {
		p := filepath.Join(base, suffix)
		if !seen[p] {
			seen[p] = true
			out = append(out, SourcePath{Path: p, Base: base, Class: class, Kind: kind})
		}
	}
	dirs := Ancestors(w)
	for _, rel := range n {
		dirs = append(dirs, filepath.Join(w, rel))
	}
	for _, d := range dirs {
		for _, f := range entryFiles {
			add(d, f, ClassEntry, KindFile)
		}
		for _, f := range scopedFiles {
			add(d, f, ClassScoped, KindFile)
		}
		add(d, ".claude/hooks", ClassScoped, KindDir)
		for _, s := range customDirs {
			add(d, s, ClassCustom, KindDir)
		}
	}
	for _, f := range userFiles {
		add(h, f, ClassUser, KindFile)
	}
	for _, s := range customDirs {
		add(h, s, ClassUser, KindDir)
	}
	for _, f := range altFiles {
		add(h, f, ClassAlternate, KindFile)
	}
	for _, s := range altDirs {
		add(h, s, ClassAlternate, KindDir)
	}
	add(h, ".claude/projects/"+EncodedProject(w)+"/memory", ClassMemory, KindDir)
	add(h, ".claude", ClassMutable, KindDir)
	add(h, ".claude/projects", ClassMutable, KindDir)
	add(h, ".claude.json", ClassMutable, KindFile)
	for _, f := range adminFiles {
		add(AdminRoot, f, ClassAdmin, KindFile)
	}
	for _, s := range adminDirs {
		add(AdminRoot, s, ClassAdmin, KindDir)
	}
	if len(out) > MaxSourcePaths {
		return nil, ErrSourceUnavailable
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Lstat is the metadata observer; tests pass their own per call.
type Lstat func(string) (fs.FileInfo, error)

// Observe inspects every matrix path by metadata only (no contents, no
// directory listing) and applies each row's disposition. uid is the operator.
// Any unsafe, unavailable or unsupported result fails the whole observation.
func Observe(paths []SourcePath, uid uint32, lstat Lstat) ([]SourceEntry, error) {
	if lstat == nil {
		lstat = os.Lstat
	}
	out := make([]SourceEntry, 0, len(paths))
	for _, sp := range paths {
		e, err := observeOne(sp, uid, lstat)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func observeOne(sp SourcePath, uid uint32, lstat Lstat) (SourceEntry, error) {
	e := SourceEntry{Path: sp.Path, Class: sp.Class, Kind: sp.Kind}
	fi, err := lstat(sp.Base)
	if err != nil {
		if sp.Class == ClassAdmin && errors.Is(err, fs.ErrNotExist) {
			// The admin root itself is absent: validate its parent chain.
			return absentBelow(e, filepath.Dir(sp.Base), sp.Path, uid, lstat)
		}
		return e, ErrSourceUnavailable
	}
	if fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return e, ErrSourceUnavailable
	}
	return absentBelow(e, sp.Base, sp.Path, uid, lstat)
}

// absentBelow walks from the existing directory base to path one component
// at a time without following links. ENOENT is absence only below a real
// directory with no symlink on the inspected chain; any other error,
// symlink or non-directory intermediate is unavailable.
func absentBelow(e SourceEntry, base, path string, uid uint32, lstat Lstat) (SourceEntry, error) {
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return e, ErrSourceUnavailable
	}
	parts := strings.Split(rel, "/")
	cur := base
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := lstat(cur)
		if errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			e.Status = StatusAbsent
			return disposition(e)
		}
		if err != nil {
			return e, ErrSourceUnavailable
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return e, ErrSourceUnavailable
		}
		if i < len(parts)-1 {
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !fi.IsDir() || !ok || (st.Uid != uid && st.Uid != 0) ||
				fi.Mode().Perm()&0o022 != 0 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
				return e, ErrSourceUnavailable
			}
			continue
		}
		return leaf(e, fi)
	}
	return e, ErrSourceUnavailable
}

func leaf(e SourceEntry, fi fs.FileInfo) (SourceEntry, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return e, ErrSourceUnavailable
	}
	switch {
	case e.Kind == KindFile && !fi.Mode().IsRegular(), e.Kind == KindDir && !fi.IsDir():
		return e, ErrSourceUnavailable
	case fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0, fi.Mode().Perm()&0o022 != 0:
		return e, ErrSourceUnavailable
	}
	uid, mode, dev, ino := st.Uid, uint32(fi.Mode()), uint64(st.Dev), uint64(st.Ino)
	e.Status, e.UID, e.Mode, e.Dev, e.Ino = StatusSafe, &uid, &mode, &dev, &ino
	return disposition(e)
}

// disposition applies the present/absent rule of each row. Owners are
// checked by Owners after observation so tests can assert rows directly.
func disposition(e SourceEntry) (SourceEntry, error) {
	present := e.Status == StatusSafe
	switch e.Class {
	case ClassAlternate, ClassMemory, ClassAdmin:
		if present {
			return e, ErrSourceUnavailable
		}
	}
	return e, nil
}

// CheckOwners applies the owner rule: user, memory and native-state rows
// belong to the operator; project rows to the operator or root.
func CheckOwners(entries []SourceEntry, uid uint32) error {
	for _, e := range entries {
		if e.Status != StatusSafe {
			continue
		}
		switch e.Class {
		case ClassEntry, ClassScoped, ClassCustom:
			if *e.UID != uid && *e.UID != 0 {
				return ErrSourceUnavailable
			}
		default:
			if *e.UID != uid {
				return ErrSourceUnavailable
			}
		}
	}
	return nil
}

// SourcesChanged compares two observations of the same matrix: presence,
// type, owner and mode of every row except the mutable native-state row.
// Modification times and inode numbers are never compared.
func SourcesChanged(before, after []SourceEntry) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		b, a := before[i], after[i]
		if b.Path != a.Path || b.Class != a.Class {
			return true
		}
		if b.Class == ClassMutable {
			continue
		}
		if b.Status != a.Status {
			return true
		}
		if b.Status == StatusSafe && (*b.UID != *a.UID || *b.Mode != *a.Mode) {
			return true
		}
	}
	return false
}
