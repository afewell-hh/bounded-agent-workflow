package state

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Namespace is the version 1 record directory inside the state root.
const Namespace = "records-v1"

// hook is an internal test seam called before each named storage stage with
// the path it affects. A non-nil error is treated as that stage's failure.
// Production code never sets it.
var hook func(stage, path string) error

func at(stage, path string) error {
	if hook == nil {
		return nil
	}
	return hook(stage, path)
}

// Root is a validated existing private state directory.
type Root struct {
	path string // lexically clean, ancestor aliases resolved
	ns   string
	nsOK bool // namespace existed and was valid when checked
}

func owned(fi fs.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Uid == uint32(os.Getuid())
}

func private(fi fs.FileInfo, perm fs.FileMode) bool {
	return owned(fi) && fi.Mode().Perm() == perm &&
		fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == 0
}

// OpenRoot validates dir and, only if it already exists, its namespace.
// It never creates, changes or follows a final symlink.
func OpenRoot(dir string) (*Root, error) {
	abs, err := filepath.Abs(dir) // Abs also cleans lexically.
	if err != nil {
		return nil, fail(CodeStateUnavailable)
	}
	fi, err := os.Lstat(abs)
	switch {
	case err != nil:
		return nil, fail(CodeStateUnavailable)
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, fail(CodeUnsafeStatePath)
	case !fi.IsDir():
		return nil, fail(CodeStateUnavailable)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return nil, fail(CodeStateUnavailable)
	}
	path := filepath.Join(parent, filepath.Base(abs))
	if parent == abs { // the filesystem root itself
		path = abs
	}
	fi2, err := os.Lstat(path)
	switch {
	case err != nil:
		return nil, fail(CodeStateUnavailable)
	case fi2.Mode()&fs.ModeSymlink != 0:
		return nil, fail(CodeUnsafeStatePath)
	case !fi2.IsDir() || !os.SameFile(fi, fi2):
		return nil, fail(CodeStateUnavailable)
	case !private(fi2, 0o700):
		return nil, fail(CodeStatePermissions)
	}
	r := &Root{path: path, ns: filepath.Join(path, Namespace)}
	ok, err := checkNamespace(r.ns)
	if err != nil {
		return nil, err
	}
	r.nsOK = ok
	return r, nil
}

// checkNamespace reports whether the namespace exists and is safe.
func checkNamespace(ns string) (bool, error) {
	fi, err := os.Lstat(ns)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, fail(CodeStateUnavailable)
	case fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir():
		return false, fail(CodeUnsafeStatePath)
	case !private(fi, 0o700):
		return false, fail(CodeStatePermissions)
	}
	return true, nil
}

// checkRecord classifies the final record name without reading or following
// it. It returns fs.ErrNotExist for an absent record.
func checkRecord(path string) error {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fs.ErrNotExist
	case err != nil:
		return fail(CodeRecordUnavailable)
	case !fi.Mode().IsRegular():
		return fail(CodeUnsafeStatePath)
	case !private(fi, 0o600):
		return fail(CodeStatePermissions)
	}
	return nil
}

func (r *Root) recordPath(id string) string { return filepath.Join(r.ns, id+".json") }

// CheckAbsent is the existing-ID check before namespace acquisition. A safe
// existing record is record_exists without being parsed.
func (r *Root) CheckAbsent(id string) error {
	if !r.nsOK {
		return nil
	}
	switch err := checkRecord(r.recordPath(id)); {
	case err == fs.ErrNotExist:
		return nil
	case err != nil:
		return err
	}
	return fail(CodeRecordExists)
}

// Read returns the strictly validated stored record. It creates, changes
// and repairs nothing.
func (r *Root) Read(id string) (Record, error) {
	if !r.nsOK {
		return Record{}, fail(CodeRecordMissing)
	}
	path := r.recordPath(id)
	switch err := checkRecord(path); {
	case err == fs.ErrNotExist:
		return Record{}, fail(CodeRecordMissing)
	case err != nil:
		return Record{}, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Record{}, fail(CodeRecordUnavailable)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Record{}, fail(CodeRecordUnavailable)
	}
	if !fi.Mode().IsRegular() {
		return Record{}, fail(CodeUnsafeStatePath)
	}
	if !private(fi, 0o600) {
		return Record{}, fail(CodeStatePermissions)
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxRecordBytes+1))
	if err != nil {
		return Record{}, fail(CodeRecordUnavailable)
	}
	if len(data) > MaxRecordBytes {
		return Record{}, fail(CodeRecordTooLarge)
	}
	return Parse(data, id)
}

func openDir(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
}

// Create publishes data as the immutable record id and then calls deliver.
// Order: acquire namespace, sync root, write/sync/close a retained exclusive
// staging file, hard-link it to the final name without replacement, sync the
// namespace, close directories, deliver. Every failure after publication is
// commit_uncertain; the record and staging name are always retained.
func (r *Root) Create(id string, data []byte, deliver func() error) error {
	// Acquire the namespace.
	if err := at("mkdir", r.ns); err != nil {
		return fail(CodeStateUnavailable)
	}
	if err := os.Mkdir(r.ns, 0o700); err == nil {
		// A new namespace is ours; remove any umask reduction.
		if err := os.Chmod(r.ns, 0o700); err != nil {
			return fail(CodeStateUnavailable)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return fail(CodeStateUnavailable)
	}
	if _, err := checkNamespace(r.ns); err != nil {
		return err
	}
	rootDir, err := openDir(r.path)
	if err != nil {
		return fail(CodeStateUnavailable)
	}
	nsDir, err := openDir(r.ns)
	if err != nil {
		rootDir.Close()
		return fail(CodeStateUnavailable)
	}
	published := false
	closeDirs := func() error {
		e1 := at("dir-close", r.path)
		if e := rootDir.Close(); e1 == nil {
			e1 = e
		}
		e2 := at("dir-close", r.ns)
		if e := nsDir.Close(); e2 == nil {
			e2 = e
		}
		if e1 != nil || e2 != nil {
			if published {
				return fail(CodeCommitUncertain)
			}
			return fail(CodeRecordUnavailable)
		}
		return nil
	}
	preFail := func(c Code) error {
		closeDirs()
		return fail(c)
	}

	if err := at("root-sync", r.path); err != nil {
		return preFail(CodeDurability)
	}
	if err := rootDir.Sync(); err != nil {
		return preFail(CodeDurability)
	}

	// Stage.
	var rnd [16]byte
	if err := at("random", r.ns); err != nil {
		return preFail(CodeRecordUnavailable)
	}
	if _, err := rand.Read(rnd[:]); err != nil {
		return preFail(CodeRecordUnavailable)
	}
	staging := filepath.Join(r.ns, ".pending-"+id+"-"+hex.EncodeToString(rnd[:]))
	if err := at("stage-create", staging); err != nil {
		return preFail(CodeRecordUnavailable)
	}
	f, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return preFail(CodeRecordUnavailable)
	}
	if err := f.Chmod(0o600); err != nil { // our new file; undo umask
		f.Close()
		return preFail(CodeRecordUnavailable)
	}
	toWrite := data
	if err := at("stage-write", staging); err != nil {
		toWrite = data[:len(data)/2] // simulate a partial write
	}
	n, err := f.Write(toWrite)
	if err != nil || n != len(data) {
		f.Close()
		return preFail(CodeRecordUnavailable)
	}
	if err := at("stage-sync", staging); err != nil {
		f.Close()
		return preFail(CodeDurability)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return preFail(CodeDurability)
	}
	cerr := at("stage-close", staging)
	if err := f.Close(); cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return preFail(CodeRecordUnavailable)
	}

	// Publish without replacement.
	final := r.recordPath(id)
	lerr := at("link", final)
	if lerr == nil {
		lerr = os.Link(staging, final)
	}
	if lerr != nil {
		if errors.Is(lerr, fs.ErrExist) {
			switch err := checkRecord(final); {
			case err == fs.ErrNotExist:
				return preFail(CodeRecordUnavailable)
			case err != nil:
				closeDirs()
				return err
			}
			return preFail(CodeRecordExists)
		}
		return preFail(CodeRecordUnavailable)
	}
	published = true

	if err := at("ns-sync", r.ns); err != nil {
		closeDirs()
		return fail(CodeCommitUncertain)
	}
	if err := nsDir.Sync(); err != nil {
		closeDirs()
		return fail(CodeCommitUncertain)
	}
	if err := closeDirs(); err != nil {
		return err
	}
	if err := at("deliver", final); err != nil {
		return fail(CodeCommitUncertain)
	}
	if err := deliver(); err != nil {
		return fail(CodeCommitUncertain)
	}
	return nil
}
