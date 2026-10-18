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
// tree). Overlapping rows keep the strictest disposition and owner rule. It fails
// above MaxSourcePaths.
func Matrix(h, w string, n []string) ([]SourcePath, error) {
	var out []SourcePath
	seen := map[string]int{}
	priority := func(class string) int {
		switch class {
		case ClassAdmin, ClassAlternate, ClassMemory:
			return 3 // required absent
		case ClassUser:
			return 2 // operator-only ownership
		case ClassEntry, ClassScoped, ClassCustom:
			return 1
		default:
			return 0 // mutable native state cannot relax a source row
		}
	}
	add := func(base, suffix, class, kind string) {
		p := filepath.Join(base, suffix)
		if i, exists := seen[p]; exists {
			if priority(class) > priority(out[i].Class) {
				out[i] = SourcePath{Path: p, Base: base, Class: class, Kind: kind}
			}
			return
		}
		seen[p] = len(out)
		out = append(out, SourcePath{Path: p, Base: base, Class: class, Kind: kind})
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
	bases, err := Bases(h, w, n)
	if err != nil {
		return nil, err
	}
	count := len(out)
	for _, b := range bases {
		if _, exists := seen[b]; !exists {
			count++
		}
	}
	if count > MaxSourcePaths {
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

// dirSafe reports a real directory (not a link) owned by uid or root, not
// group/other writable and without setuid, setgid or sticky bits. No host
// path is exempt.
func dirSafe(fi fs.FileInfo, uid uint32) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && fi.Mode()&fs.ModeSymlink == 0 && fi.IsDir() && (st.Uid == uid || st.Uid == 0) &&
		fi.Mode().Perm()&0o022 == 0 && fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0
}

// observeOne requires the row's base directory itself to be safe (dirSafe)
// before walking the suffix chain.
func observeOne(sp SourcePath, uid uint32, lstat Lstat) (SourceEntry, error) {
	e := SourceEntry{Path: sp.Path, Class: sp.Class, Kind: sp.Kind}
	fi, err := lstat(sp.Base)
	if err != nil {
		if sp.Class == ClassAdmin && errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			// The admin root itself is absent: validate its parent and chain.
			parent := filepath.Dir(sp.Base)
			pfi, perr := lstat(parent)
			if perr != nil || !dirSafe(pfi, uid) {
				return e, ErrSourceUnavailable
			}
			return absentBelow(e, parent, sp.Path, uid, lstat)
		}
		return e, ErrSourceUnavailable
	}
	if !dirSafe(fi, uid) {
		return e, ErrSourceUnavailable
	}
	return absentBelow(e, sp.Base, sp.Path, uid, lstat)
}

// ClassBase labels a base directory observation.
const ClassBase = "base"

// Bases lists the directories the matrix rows hang from: w and every
// ancestor (root first), each nested directory with every intermediate
// directory between w and it, and h. Duplicates are removed and the list is
// bounded by MaxSourcePaths. The admin root is checked by Observe.
func Bases(h, w string, n []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, a := range Ancestors(w) {
		add(a)
	}
	for _, rel := range n {
		parts := strings.Split(rel, "/")
		for i := range parts {
			add(filepath.Join(w, strings.Join(parts[:i+1], "/")))
		}
	}
	add(h)
	if len(out) > MaxSourcePaths {
		return nil, ErrSourceUnavailable
	}
	return out, nil
}

// ObserveBases observes each base directory by metadata only. Every base must
// exist and be dirSafe; absence, an error or anything else is unavailable.
func ObserveBases(bases []string, uid uint32, lstat Lstat) ([]SourceEntry, error) {
	if lstat == nil {
		lstat = os.Lstat
	}
	out := make([]SourceEntry, 0, len(bases))
	for _, b := range bases {
		fi, err := lstat(b)
		if err != nil || !dirSafe(fi, uid) {
			return nil, ErrSourceUnavailable
		}
		st := fi.Sys().(*syscall.Stat_t)
		u, mode, dev, ino := st.Uid, uint32(fi.Mode()), uint64(st.Dev), uint64(st.Ino)
		out = append(out, SourceEntry{Path: b, Class: ClassBase, Kind: KindDir, Status: StatusSafe,
			UID: &u, Mode: &mode, Dev: &dev, Ino: &ino})
	}
	return out, nil
}

// BasesChanged compares two base observations: path, owner, mode, device and
// inode of every base. Modification times are never compared.
func BasesChanged(before, after []SourceEntry) bool {
	if len(before) != len(after) {
		return true
	}
	for i := range before {
		b, a := before[i], after[i]
		if b.Path != a.Path || *b.UID != *a.UID || *b.Mode != *a.Mode || *b.Dev != *a.Dev || *b.Ino != *a.Ino {
			return true
		}
	}
	return false
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
			if !dirSafe(fi, uid) {
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
