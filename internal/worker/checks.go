package worker

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/afewell-hh/bounded-agent-workflow/internal/agents/claude"
)

func regularBlob(e treeEntry) bool {
	return e.typ == "blob" && (e.mode == "100644" || e.mode == "100755")
}

// blobOID is the Git blob object ID of data in the given object format.
func blobOID(format string, data []byte) string {
	var h hash.Hash = sha1.New()
	if format == "sha256" {
		h = sha256.New()
	}
	h.Write([]byte("blob " + strconv.Itoa(len(data)) + "\x00"))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// verifyInstructions checks every instruction against the launch-base tree
// (a regular blob with the named ID) and the worktree bytes (no link on the
// chain, regular, bounded, matching both the content SHA-256 and the blob
// ID). It returns the full bodies in order.
func verifyInstructions(top string, ins []Instruction, tree map[string]treeEntry, format string) ([][]byte, error) {
	bodies := make([][]byte, 0, len(ins))
	for _, i := range ins {
		e, ok := tree[i.Path]
		if !ok || !regularBlob(e) || e.oid != i.BlobOID {
			return nil, fail(CodeInstructionsUnavailable)
		}
		data, _, present, err := readWorktreeFile(top, i.Path, MaxInstructionBytes)
		if err != nil || !present || sum(data) != i.SHA256 || blobOID(format, data) != i.BlobOID {
			return nil, fail(CodeInstructionsUnavailable)
		}
		bodies = append(bodies, data)
	}
	return bodies, nil
}

// checkAllowed requires every allowed path's parents to exist as real
// directories without links, and the path itself to be absent or a regular
// file; a tracked path must be a regular blob.
func checkAllowed(top string, allowed []string, tree map[string]treeEntry) error {
	for _, p := range allowed {
		if e, ok := tree[p]; ok && !regularBlob(e) {
			return fail(CodeInvalidPlan)
		}
		parts := strings.Split(p, "/")
		cur := top
		for i, part := range parts {
			cur = filepath.Join(cur, part)
			fi, err := os.Lstat(cur)
			last := i == len(parts)-1
			switch {
			case last && errors.Is(err, fs.ErrNotExist):
			case err != nil, fi.Mode()&fs.ModeSymlink != 0:
				return fail(CodeInvalidPlan)
			case !last && !fi.IsDir(), last && !fi.Mode().IsRegular():
				return fail(CodeInvalidPlan)
			}
		}
	}
	return nil
}

// nestedDirs returns the sorted repository-relative directories below the
// worktree root that contain a tracked instruction entry file or .claude
// source, or an instruction document, in the launch-base tree.
func nestedDirs(tree map[string]treeEntry, ins []Instruction) []string {
	set := map[string]bool{}
	add := func(dir string) {
		if dir != "." && dir != "" {
			set[dir] = true
		}
	}
	for p := range tree {
		parts := strings.Split(p, "/")
		for i, c := range parts[:len(parts)-1] {
			if c == ".claude" {
				add(strings.Join(parts[:i], "/"))
			}
		}
		switch parts[len(parts)-1] {
		case "CLAUDE.md", "CLAUDE.local.md", "AGENTS.md", ".mcp.json":
			add(filepath.Dir(p))
		}
	}
	for _, i := range ins {
		add(filepath.Dir(i.Path))
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

// checkPin validates the pinned executable without following any alias:
// physical path equal to the declared path, regular, owned by the operator
// or root, not group/other writable, no special bits, executable, and the
// declared SHA-256 of its bytes. It returns the observed SHA-256.
func checkPin(p claude.Profile) (string, error) {
	bad := fail(CodeProfileUnavailable)
	if phys, err := filepath.EvalSymlinks(p.Executable); err != nil || phys != p.Executable {
		return "", bad
	}
	lfi, err := os.Lstat(p.Executable)
	if err != nil || !execSafe(lfi) || syscall.Access(p.Executable, 1) != nil {
		return "", bad
	}
	f, err := os.OpenFile(p.Executable, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", bad
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !execSafe(fi) || !os.SameFile(fi, lfi) {
		return "", bad
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxExecutable+1))
	got := hex.EncodeToString(h.Sum(nil))
	if err != nil || n > maxExecutable || got != p.ExecutableSHA256 {
		return "", bad
	}
	return got, nil
}

func execSafe(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && fi.Mode().IsRegular() && (st.Uid == uint32(os.Getuid()) || st.Uid == 0) &&
		fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0 && fi.Mode().Perm()&0o022 == 0
}

// checkHome requires the approved auth HOME to be a canonical real directory
// owned by the operator and not group/other writable. Its contents are never
// read.
func checkHome(h string) error {
	bad := fail(CodeProfileUnavailable)
	if phys, err := filepath.EvalSymlinks(h); err != nil || phys != h {
		return bad
	}
	fi, err := os.Lstat(h)
	if err != nil || !fi.IsDir() || !owned(fi) || fi.Mode().Perm()&0o022 != 0 {
		return bad
	}
	return nil
}

type changeEntry struct {
	Path   string  `json:"path"`
	State  string  `json:"state"`
	SHA256 *string `json:"sha256"`
	Size   *int    `json:"size"`
	Mode   *uint32 `json:"mode"`
}

type changeManifest struct {
	SchemaVersion int           `json:"schema_version"`
	RunID         string        `json:"run_id"`
	Changes       []changeEntry `json:"changes"`
}

// captureChanges is the bounded full worktree capture: every tracked,
// index, untracked and ignored difference reported by guarded Git must be a
// worktree-only change to an allowed path (no index change, conflict or
// directory entry), and every allowed path is read through no-follow
// handling and hashed within the file and total bounds. ok is false on any
// violation or overflow; nothing is truncated or rolled back.
func captureChanges(g *git, top, runID string, allowed []string, tree map[string]treeEntry, format string) ([]byte, bool) {
	es, err := g.status()
	if err != nil || len(es) > MaxChangeFiles {
		return nil, false
	}
	ok := map[string]bool{}
	for _, p := range allowed {
		ok[p] = true
	}
	for _, e := range es {
		switch {
		case e.kind == 'u', e.kind == '1' && (len(e.xy) != 2 || e.xy[0] != '.'):
			return nil, false
		case !ok[e.path]:
			return nil, false
		}
	}
	m := changeManifest{SchemaVersion: 1, RunID: runID, Changes: []changeEntry{}}
	total := 0
	for _, p := range allowed {
		data, fi, present, err := readWorktreeFile(top, p, MaxChangeFile)
		if err != nil {
			return nil, false
		}
		t, tracked := tree[p]
		c := changeEntry{Path: p}
		switch {
		case !present && tracked:
			c.State = "deleted"
		case !present:
			c.State = "absent"
		default:
			total += len(data)
			if total > MaxChangeTotal {
				return nil, false
			}
			h, n := sum(data), len(data)
			mode := uint32(fi.Mode())
			c.SHA256, c.Size, c.Mode = &h, &n, &mode
			switch {
			case tracked && blobOID(format, data) == t.oid && (fi.Mode().Perm()&0o111 != 0) == (t.mode == "100755"):
				c.State = changeStateSame
			case tracked:
				c.State = "modified"
			default:
				c.State = "added"
			}
		}
		m.Changes = append(m.Changes, c)
	}
	b, err := marshal(m)
	return b, err == nil
}

type suppliedInstruction struct {
	Path    string `json:"path"`
	BlobOID string `json:"blob_oid"`
	SHA256  string `json:"sha256"`
	Bytes   int    `json:"bytes"`
}

type supplied struct {
	SchemaVersion             int                   `json:"schema_version"`
	RunID                     string                `json:"run_id"`
	PlanSHA256                string                `json:"plan_sha256"`
	TaskInputSHA256           string                `json:"task_input_sha256"`
	ProfileFileSHA256         string                `json:"profile_file_sha256"`
	ProfileSHA256             string                `json:"profile_sha256"`
	ExecutableSHA256          string                `json:"executable_sha256"`
	InstructionManifestSHA256 string                `json:"instruction_manifest_sha256"`
	AssembledPacketSHA256     string                `json:"assembled_packet_sha256"`
	Instructions              []suppliedInstruction `json:"instructions"`
	AllowedChanges            []string              `json:"allowed_changes"`
	NestedDirectories         []string              `json:"nested_directories"`
}

func suppliedInputs(runID string, in *inputs) supplied {
	s := supplied{SchemaVersion: 1, RunID: runID, PlanSHA256: in.planSHA, TaskInputSHA256: in.taskSHA}
	s.ProfileFileSHA256, s.ProfileSHA256, s.ExecutableSHA256 = in.profileFileSHA, in.profile.ApprovedSHA256, in.exeSHA
	s.InstructionManifestSHA256, s.AssembledPacketSHA256 = in.manifestSHA, sum(in.packet)
	for i, e := range in.task.Instructions {
		s.Instructions = append(s.Instructions, suppliedInstruction{e.Path, e.BlobOID, e.SHA256, len(in.bodies[i])})
	}
	s.AllowedChanges, s.NestedDirectories = in.task.AllowedChanges, append([]string{}, in.nested...)
	return s
}

type sourceManifest struct {
	SchemaVersion int                  `json:"schema_version"`
	MatrixID      string               `json:"matrix_id"`
	CapturedAt    string               `json:"captured_at"`
	Entries       []claude.SourceEntry `json:"entries"`
}
