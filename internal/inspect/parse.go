package inspect

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
)

type statusKind int

const (
	kindChanged statusKind = iota + 1
	kindConflict
	kindUntracked
)

type statusResult struct {
	staged, unstaged, untracked, conflicted int
	paths                                   map[string]statusKind // only allowlisted paths
	unmerged                                map[string]bool       // every path of a `u` record
}

const gitlinkMode = "160000"

var allowlisted = func() map[string]bool {
	m := map[string]bool{}
	for _, p := range SourcePaths {
		m[p] = true
	}
	return m
}()

// fieldsThenPath splits a record into n space-separated fields and the rest.
func fieldsThenPath(rec string, n int) ([]string, string, bool) {
	parts := strings.SplitN(rec, " ", n+1)
	if len(parts) != n+1 || parts[n] == "" {
		return nil, "", false
	}
	return parts[:n], parts[n], true
}

func validXY(xy string) bool {
	if len(xy) != 2 {
		return false
	}
	for _, c := range xy {
		if !strings.ContainsRune(".MTADRCU", c) {
			return false
		}
	}
	return true
}

// parseStatus parses `git status --porcelain=v2 -z` without headers.
func parseStatus(out []byte) (statusResult, error) {
	r := statusResult{paths: map[string]statusKind{}, unmerged: map[string]bool{}}
	if len(out) == 0 {
		return r, nil
	}
	if out[len(out)-1] != 0 {
		return r, fail(CodeInvalidGitOutput)
	}
	recs := strings.Split(string(out[:len(out)-1]), "\x00")
	for i := 0; i < len(recs); i++ {
		rec := recs[i]
		if len(rec) < 3 || rec[1] != ' ' {
			return r, fail(CodeInvalidGitOutput)
		}
		var path string
		switch rec[0] {
		case '1', '2':
			n := 8
			if rec[0] == '2' {
				n = 9
			}
			f, p, ok := fieldsThenPath(rec, n)
			if !ok || !validXY(f[1]) {
				return r, fail(CodeInvalidGitOutput)
			}
			if rec[0] == '2' {
				// Rename/copy records carry an extra NUL-terminated original path.
				i++
				if i >= len(recs) {
					return r, fail(CodeInvalidGitOutput)
				}
			}
			path = p
			// Gitlink records are excluded here; staged gitlink changes are
			// counted once from index/HEAD metadata (stagedGitlinks) and
			// submodule worktree content is never counted.
			if f[2][0] == 'S' || f[3] == gitlinkMode || f[4] == gitlinkMode {
				continue
			}
			if f[1][0] != '.' {
				r.staged++
			}
			if f[1][1] != '.' {
				r.unstaged++
			}
			if allowlisted[path] {
				r.paths[path] = kindChanged
			}
		case 'u':
			f, p, ok := fieldsThenPath(rec, 10)
			if !ok || !validXY(f[1]) {
				return r, fail(CodeInvalidGitOutput)
			}
			r.conflicted++
			r.unmerged[p] = true
			if allowlisted[p] {
				r.paths[p] = kindConflict
			}
		case '?':
			r.untracked++
			if p := rec[2:]; allowlisted[p] {
				r.paths[p] = kindUntracked
			}
		case '!':
			// Ignored entries are not requested; tolerate and do not count.
		default:
			return r, fail(CodeInvalidGitOutput)
		}
	}
	return r, nil
}

// entry is the mode and object ID of one index or tree entry.
type entry struct{ mode, oid string }

// indexResult holds `git ls-files --stage -z` metadata.
type indexResult struct {
	stage0   map[string]entry
	unmerged map[string]bool // paths with any stage 1-3 entry
	gitlinks int             // stage-0 gitlinks
	indexed  map[string]bool // allowlisted paths with any index entry
}

func validOID(s string, width int) bool { return len(s) == width && hexRE.MatchString(s) }

// parseIndex parses `git ls-files --stage -z` ("MODE OID STAGE\tPATH").
func parseIndex(out []byte, width int) (indexResult, error) {
	r := indexResult{stage0: map[string]entry{}, unmerged: map[string]bool{}, indexed: map[string]bool{}}
	if len(out) == 0 {
		return r, nil
	}
	if out[len(out)-1] != 0 {
		return r, fail(CodeInvalidGitOutput)
	}
	for _, rec := range bytes.Split(out[:len(out)-1], []byte{0}) {
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 || tab == len(rec)-1 {
			return r, fail(CodeInvalidGitOutput)
		}
		meta := strings.Split(string(rec[:tab]), " ")
		if len(meta) != 3 || len(meta[0]) != 6 || !validOID(meta[1], width) || len(meta[2]) != 1 || meta[2] < "0" || meta[2] > "3" {
			return r, fail(CodeInvalidGitOutput)
		}
		p := string(rec[tab+1:])
		if meta[2] == "0" {
			r.stage0[p] = entry{meta[0], meta[1]}
			if meta[0] == gitlinkMode {
				r.gitlinks++
			}
		} else {
			r.unmerged[p] = true
		}
		if allowlisted[p] {
			r.indexed[p] = true
		}
	}
	return r, nil
}

// stagedGitlinks counts paths whose stage-0 index entry differs from HEAD
// where either side is a gitlink: additions, updates, deletions and type
// changes, each once. Unmerged paths are conflicts, not staged changes. Only
// recorded object IDs are compared; no submodule is read.
func stagedGitlinks(head map[string]entry, idx indexResult) int {
	n := 0
	for p, e := range idx.stage0 {
		if e.mode == gitlinkMode && head[p] != e {
			n++
		}
	}
	for p, h := range head {
		if h.mode != gitlinkMode || idx.unmerged[p] {
			continue
		}
		if e, ok := idx.stage0[p]; !ok || e.mode != gitlinkMode {
			n++
		}
	}
	return n
}

// countWorktrees counts non-bare worktree records other than the current one.
func countWorktrees(out []byte) (int, error) {
	total := 0
	inRecord := false
	bare := false
	flush := func() {
		if inRecord && !bare {
			total++
		}
		inRecord, bare = false, false
	}
	for _, line := range strings.Split(string(out), "\x00") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			inRecord = true
		case line == "bare":
			bare = true
		case line == "":
			flush()
		}
	}
	flush()
	if total < 1 {
		return 0, fail(CodeInvalidGitOutput)
	}
	return total - 1, nil
}

// parseTree parses `git ls-tree -r -z --full-tree` ("MODE TYPE OID\tPATH").
// It returns every entry and the allowlisted paths that are regular files.
func parseTree(out []byte, width int) (map[string]entry, map[string]bool, error) {
	entries := map[string]entry{}
	files := map[string]bool{}
	if len(out) == 0 {
		return entries, files, nil
	}
	if out[len(out)-1] != 0 {
		return nil, nil, fail(CodeInvalidGitOutput)
	}
	for _, rec := range bytes.Split(out[:len(out)-1], []byte{0}) {
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 || tab == len(rec)-1 {
			return nil, nil, fail(CodeInvalidGitOutput)
		}
		meta := strings.Split(string(rec[:tab]), " ")
		if len(meta) != 3 || len(meta[0]) != 6 || !validOID(meta[2], width) {
			return nil, nil, fail(CodeInvalidGitOutput)
		}
		p := string(rec[tab+1:])
		entries[p] = entry{meta[0], meta[2]}
		if allowlisted[p] && meta[1] == "blob" && (meta[0] == "100644" || meta[0] == "100755") {
			files[p] = true
		}
	}
	return entries, files, nil
}

// probeSource lstats each component of rel under top without following links.
// It returns whether a regular file is present.
func probeSource(top, rel string) (bool, error) {
	parts := strings.Split(rel, "/")
	cur := top
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, fail(CodeSourceUnavailable)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return false, fail(CodeSourceSymlink)
		}
		if i < len(parts)-1 {
			if !fi.IsDir() {
				return false, nil
			}
			continue
		}
		if !fi.Mode().IsRegular() {
			return false, fail(CodeSourceUnavailable)
		}
	}
	return true, nil
}

func inspectSources(top, head string, headFiles, indexed map[string]bool, status map[string]statusKind) ([]Source, error) {
	var out []Source
	for _, rel := range SourcePaths {
		present, err := probeSource(top, rel)
		if err != nil {
			return nil, err
		}
		s := Source{Path: rel, Presence: "absent"}
		if present {
			s.Presence = "present"
			ref := "worktree:" + rel
			s.WorktreeRef = &ref
		}
		inHead := headFiles[rel]
		if inHead {
			ref := "git:" + head + ":" + rel
			s.HeadRef = &ref
		}
		tracked := inHead || indexed[rel]
		kind := status[rel]
		switch {
		case kind == kindUntracked && present:
			s.WorktreeState = "untracked"
		case kind == kindChanged || kind == kindConflict:
			if present {
				s.WorktreeState = "modified"
			} else {
				s.WorktreeState = "deleted"
			}
		case kind == 0 && present && indexed[rel]:
			s.WorktreeState = "clean"
		case kind == 0 && present && !tracked:
			s.WorktreeState = "untracked" // e.g. ignored by repository rules
		case kind == 0 && !present && !tracked:
			s.WorktreeState = "absent"
		default:
			s.WorktreeState = "unknown"
		}
		out = append(out, s)
	}
	return out, nil
}
