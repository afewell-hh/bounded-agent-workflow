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
}

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
	r := statusResult{paths: map[string]statusKind{}}
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
			if f[1][0] != '.' {
				r.staged++
			}
			if f[1][1] != '.' {
				r.unstaged++
			}
			path = p
			if allowlisted[path] {
				r.paths[path] = kindChanged
			}
		case 'u':
			f, p, ok := fieldsThenPath(rec, 10)
			if !ok || !validXY(f[1]) {
				return r, fail(CodeInvalidGitOutput)
			}
			r.conflicted++
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

// parseIndex counts stage-0 gitlinks and records which allowlisted paths
// have any index entry.
func parseIndex(out []byte) (int, map[string]bool, error) {
	indexed := map[string]bool{}
	gitlinks := 0
	if len(out) == 0 {
		return 0, indexed, nil
	}
	if out[len(out)-1] != 0 {
		return 0, nil, fail(CodeInvalidGitOutput)
	}
	for _, rec := range bytes.Split(out[:len(out)-1], []byte{0}) {
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			return 0, nil, fail(CodeInvalidGitOutput)
		}
		meta := strings.Split(string(rec[:tab]), " ")
		if len(meta) != 3 || len(meta[0]) != 6 || len(meta[2]) != 1 {
			return 0, nil, fail(CodeInvalidGitOutput)
		}
		if meta[0] == "160000" && meta[2] == "0" {
			gitlinks++
		}
		if p := string(rec[tab+1:]); allowlisted[p] {
			indexed[p] = true
		}
	}
	return gitlinks, indexed, nil
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

// parseTree returns the allowlisted paths that are regular files in HEAD.
func parseTree(out []byte) (map[string]bool, error) {
	files := map[string]bool{}
	if len(out) == 0 {
		return files, nil
	}
	if out[len(out)-1] != 0 {
		return nil, fail(CodeInvalidGitOutput)
	}
	for _, rec := range bytes.Split(out[:len(out)-1], []byte{0}) {
		tab := bytes.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fail(CodeInvalidGitOutput)
		}
		meta := strings.Split(string(rec[:tab]), " ")
		if len(meta) != 3 {
			return nil, fail(CodeInvalidGitOutput)
		}
		p := string(rec[tab+1:])
		if allowlisted[p] && meta[1] == "blob" && (meta[0] == "100644" || meta[0] == "100755") {
			files[p] = true
		}
	}
	return files, nil
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
