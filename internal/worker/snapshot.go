package worker

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// maxControlEntries bounds the Git control entries observed per capture.
const maxControlEntries = 512

// fileID is one bounded filesystem observation. kind is F (regular file),
// D (directory), L (symbolic link) or A (absent, control entries only).
// sha256 and oid cover file bytes or the link target; directories carry
// neither and record no size, because a directory's size changes when an
// allowed file is created in it. Modification times are never recorded.
type fileID struct {
	kind   byte
	mode   fs.FileMode
	uid    uint32
	dev    uint64
	ino    uint64
	size   int64
	sha256 string
	oid    string
}

// snapshot maps relative paths (or labelled control names) to observations.
type snapshot map[string]fileID

var errCapture = errors.New("capture unsupported or over bound")

// walker enumerates real filesystem entries within shared bounds: at most
// maxEntries entries, maxFile bytes per file or link target and maxTotal bytes
// hashed in total. Nothing is truncated; any overflow fails the capture.
type walker struct {
	maxEntries int
	maxFile    int
	maxTotal   int
	format     string // object format for blob IDs; "" computes none
	entries    int
	total      int
}

// takeSnapshot is the bounded full worktree capture: every directory, regular
// file and symbolic link below top, including ignored and untracked entries,
// except the top-level .git entry (observed by the control capture). Any other
// .git component, special file, unreadable entry or bound overflow fails.
// Files are read without following links and checked against their Lstat.
func takeSnapshot(top, format string) (snapshot, error) {
	w := &walker{maxEntries: MaxChangeFiles, maxFile: MaxChangeFile, maxTotal: MaxChangeTotal, format: format}
	root, err := w.observe(top)
	if err != nil || root.kind != 'D' {
		return nil, errCapture
	}
	s := snapshot{}
	if err := w.walk(s, top, "", "", root, true); err != nil {
		return nil, errCapture
	}
	return s, nil
}

// observe records one entry by Lstat without following it. An absent path
// returns the Lstat error unchanged so callers can distinguish absence.
func (w *walker) observe(path string) (fileID, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return fileID{}, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, errCapture
	}
	id := fileID{mode: fi.Mode(), uid: st.Uid, dev: uint64(st.Dev), ino: uint64(st.Ino)}
	var data []byte
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil || len(target) > w.maxFile {
			return fileID{}, errCapture
		}
		id.kind, data = 'L', []byte(target)
	case fi.IsDir():
		id.kind = 'D'
		return id, nil
	case fi.Mode().IsRegular():
		if fi.Size() > int64(w.maxFile) {
			return fileID{}, errCapture
		}
		if data, err = readNoFollow(path, fi, w.maxFile); err != nil {
			return fileID{}, errCapture
		}
		id.kind = 'F'
	default:
		return fileID{}, errCapture
	}
	w.total += len(data)
	if w.total > w.maxTotal {
		return fileID{}, errCapture
	}
	id.size, id.sha256 = int64(len(data)), sum(data)
	if w.format != "" {
		id.oid = blobOID(w.format, data)
	}
	return id, nil
}

// walk records every entry below dir (want is its observation) in sorted
// order under key prefix+rel. In a worktree a top-level ".git" is skipped and
// any other case-insensitive ".git" name is unsupported.
func (w *walker) walk(s snapshot, root, rel, prefix string, want fileID, worktree bool) error {
	dir := root
	if rel != "" {
		dir = filepath.Join(root, filepath.FromSlash(rel))
	}
	names, err := readDirNames(dir, want, w.maxEntries)
	if err != nil {
		return errCapture
	}
	sort.Strings(names)
	for _, n := range names {
		if worktree && strings.EqualFold(n, ".git") {
			if rel == "" && n == ".git" {
				continue
			}
			return errCapture
		}
		r := n
		if rel != "" {
			r = rel + "/" + n
		}
		w.entries++
		if w.entries > w.maxEntries {
			return errCapture
		}
		id, err := w.observe(filepath.Join(root, filepath.FromSlash(r)))
		if err != nil {
			return errCapture
		}
		if !worktree && id.kind == 'L' {
			return errCapture
		}
		s[prefix+r] = id
		if id.kind == 'D' {
			if err := w.walk(s, root, r, prefix, id, worktree); err != nil {
				return err
			}
		}
	}
	return nil
}

// readDirNames lists a directory opened without following links, checking
// the descriptor is the observed directory, and fails above limit names.
func readDirNames(dir string, want fileID, limit int) ([]string, error) {
	f, err := openDir(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.IsDir() || uint64(st.Dev) != want.dev || uint64(st.Ino) != want.ino {
		return nil, errCapture
	}
	var names []string
	for {
		batch, err := f.Readdirnames(256)
		names = append(names, batch...)
		if len(names) > limit {
			return nil, errCapture
		}
		if err == io.EOF {
			return names, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// readNoFollow reads a regular file without following a final link or
// blocking on a FIFO, requiring the descriptor to be the Lstat-observed file.
func readNoFollow(path string, lfi fs.FileInfo, limit int) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || !os.SameFile(fi, lfi) {
		return nil, errCapture
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil || len(data) > limit {
		return nil, errCapture
	}
	return data, nil
}

// matchesTree requires the launch snapshot to equal the launch-base tree
// independently of Git's status cache and index flags: every tracked path
// present with its recorded blob ID, type and executable bit, and no other
// file or link. A submodule or other unsupported entry is an unsafe layout.
func matchesTree(s snapshot, tree map[string]treeEntry) error {
	for _, e := range tree {
		if e.mode != "100644" && e.mode != "100755" && e.mode != "120000" {
			return fail(CodeUnsafeLayout)
		}
	}
	for p, e := range tree {
		id, ok := s[p]
		switch {
		case !ok:
			return fail(CodeNotClean)
		case e.mode == "120000":
			if id.kind != 'L' || id.oid != e.oid {
				return fail(CodeNotClean)
			}
		case id.kind != 'F' || id.oid != e.oid || (id.mode&0o100 != 0) != (e.mode == "100755"):
			return fail(CodeNotClean)
		}
	}
	for p, id := range s {
		if _, tracked := tree[p]; id.kind != 'D' && !tracked {
			return fail(CodeNotClean)
		}
	}
	return nil
}

// unchangedExcept reports whether after equals before on every path outside
// allowed: same presence, kind, mode, owner, device, inode, size and content.
func unchangedExcept(before, after snapshot, allowed map[string]bool) bool {
	for p, b := range before {
		if a, ok := after[p]; !allowed[p] && (!ok || a != b) {
			return false
		}
	}
	for p := range after {
		if _, ok := before[p]; !allowed[p] && !ok {
			return false
		}
	}
	return true
}

// Control entries relative to the per-worktree Git directory and the common
// directory. Trees are walked within the control bounds. The index file's raw
// bytes are not compared (a read-only Git refresh may rewrite its stat cache);
// its staged listing and per-entry flags are compared instead.
var (
	gitDirFiles = []string{"HEAD", "config.worktree", "commondir", "gitdir"}
	gitDirTrees = []string{"info", "refs"}
	commonFiles = []string{"packed-refs", "config", "objects/info/alternates", "objects/info/http-alternates"}
	commonTrees = []string{"info", "hooks", "refs"}
)

// controlFiles observes the worktree's top-level .git entry and the fixed
// Git control entries by metadata and content hash. It returns a private,
// deterministic encoding that is compared, never published.
func controlFiles(top, gitDir, common string) ([]byte, error) {
	w := &walker{maxEntries: maxControlEntries, maxFile: MaxChangeFile, maxTotal: MaxChangeTotal}
	s := snapshot{}
	seen := map[string]bool{}
	add := func(key, path string, tree bool) error {
		if seen[path] {
			return nil
		}
		seen[path] = true
		w.entries++
		if w.entries > w.maxEntries {
			return errCapture
		}
		id, err := w.observe(path)
		if errors.Is(err, fs.ErrNotExist) {
			if key == "top:.git" {
				return errCapture
			}
			s[key] = fileID{kind: 'A'}
			return nil
		}
		if err != nil {
			return errCapture
		}
		if key == "top:.git" {
			if id.kind != 'F' && id.kind != 'D' {
				return errCapture
			}
		} else if (tree && id.kind != 'D') || (!tree && id.kind != 'F') {
			return errCapture
		}
		s[key] = id
		if tree && id.kind == 'D' {
			return w.walk(s, path, "", key+"/", id, false)
		}
		return nil
	}
	type entry struct {
		key, path string
		tree      bool
	}
	list := []entry{{"top:.git", filepath.Join(top, ".git"), false}}
	for _, n := range gitDirFiles {
		list = append(list, entry{"gitdir:" + n, filepath.Join(gitDir, n), false})
	}
	for _, n := range gitDirTrees {
		list = append(list, entry{"gitdir:" + n, filepath.Join(gitDir, n), true})
	}
	for _, n := range commonFiles {
		list = append(list, entry{"common:" + n, filepath.Join(common, filepath.FromSlash(n)), false})
	}
	for _, n := range commonTrees {
		list = append(list, entry{"common:" + n, filepath.Join(common, n), true})
	}
	for _, e := range list {
		if err := add(e.key, e.path, e.tree); err != nil {
			return nil, errCapture
		}
	}
	return s.encode(), nil
}

// encode serializes a snapshot deterministically for private comparison.
func (s snapshot) encode() []byte {
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		id := s[k]
		b.WriteString(strconv.Quote(k) + " " + string(id.kind) + " " + strconv.FormatUint(uint64(id.mode), 8) + " " +
			strconv.FormatUint(uint64(id.uid), 10) + " " + strconv.FormatUint(id.dev, 10) + " " +
			strconv.FormatUint(id.ino, 10) + " " + strconv.FormatInt(id.size, 10) + " " + id.sha256 + "\n")
	}
	return []byte(b.String())
}
