package worker

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
)

// git runs guarded read-only Git commands in the canonical worktree with the
// inspector's fixed option prefix and configuration isolation. No external
// diff, filter, hook, pager or helper is configured to run.
type git struct {
	path string
	dir  string
	ctx  context.Context
}

var gitEnvAllow = []string{"HOME", "PATH", "TMPDIR", "USER", "LOGNAME", "LANG"}

func gitEnv() []string {
	var env []string
	for _, k := range gitEnvAllow {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
}

func (g *git) run(args ...string) ([]byte, error) {
	argv := append(append([]string{"-C", g.dir}, inspect.GitPrefix...), args...)
	return proc.Run(g.ctx, proc.Spec{Path: g.path, Args: argv, Env: gitEnv(), Timeout: 10 * time.Second,
		StdoutCap: 8 << 20, StderrCap: 64 << 10})
}

// treeEntry is one launch-base tree entry.
type treeEntry struct {
	mode, typ, oid string
}

// tree lists the full launch-base tree of rev by path.
func (g *git) tree(rev string, width int) (map[string]treeEntry, error) {
	out, err := g.run("ls-tree", "-r", "-z", "--full-tree", rev)
	if err != nil {
		return nil, errGit
	}
	t := map[string]treeEntry{}
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, path, ok := strings.Cut(string(rec), "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || len(f[2]) != width || path == "" {
			return nil, errGit
		}
		t[path] = treeEntry{mode: f[0], typ: f[1], oid: f[2]}
	}
	return t, nil
}

// statusEntry is one porcelain v2 status record.
type statusEntry struct {
	kind byte   // '1', 'u', '?', '!'
	xy   string // for '1' and 'u'
	path string
}

// status lists every tracked, untracked and ignored difference.
func (g *git) status() ([]statusEntry, error) {
	out, err := g.run("status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignored=matching",
		"--ignore-submodules=none", "--no-renames")
	if err != nil {
		return nil, errGit
	}
	var es []statusEntry
	for _, rec := range bytes.Split(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		s := string(rec)
		switch s[0] {
		case '?', '!':
			if len(s) < 3 || s[1] != ' ' {
				return nil, errGit
			}
			es = append(es, statusEntry{kind: s[0], path: s[2:]})
		case '1':
			// 1 XY sub mH mI mW hH hI path
			f := strings.SplitN(s, " ", 9)
			if len(f) != 9 {
				return nil, errGit
			}
			es = append(es, statusEntry{kind: '1', xy: f[1], path: f[8]})
		case 'u':
			f := strings.SplitN(s, " ", 11)
			if len(f) != 11 {
				return nil, errGit
			}
			es = append(es, statusEntry{kind: 'u', xy: f[1], path: f[10]})
		default:
			// '2' (renames) cannot occur with --no-renames; anything else is unknown.
			return nil, errGit
		}
	}
	return es, nil
}

// head returns the resolved HEAD commit.
func (g *git) head() (string, error) {
	out, err := g.run("rev-parse", "--verify", "-q", "HEAD^{commit}")
	if err != nil {
		return "", errGit
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// control returns a content-free identity of the Git control state: the
// symbolic HEAD, the resolved HEAD and the full staged index listing.
func (g *git) control() ([]byte, error) {
	var b bytes.Buffer
	sym, err := g.run("symbolic-ref", "-q", "HEAD")
	if err != nil {
		var ee *proc.ExitError
		if !asExit(err, &ee) || ee.Code != 1 {
			return nil, errGit
		}
		sym = []byte("detached\n")
	}
	b.Write(sym)
	h, err := g.head()
	if err != nil {
		return nil, err
	}
	b.WriteString(h + "\n")
	idx, err := g.run("ls-files", "--stage", "-z")
	if err != nil {
		return nil, errGit
	}
	b.Write(idx)
	return b.Bytes(), nil
}
