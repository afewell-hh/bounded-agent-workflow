package execution

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Namespace is the version 1 attempt directory inside the state root. It is
// disjoint from the run record namespace, which never scans it.
const Namespace = "execute-v1"

// hook is an internal test seam called before each named storage/process
// stage. A non-nil error is treated as that stage's failure; close stages run
// the real close first. Production code never sets it.
var hook func(stage string) error

func at(stage string) error {
	if hook == nil {
		return nil
	}
	return hook(stage)
}

// rootPath re-derives the physical state root exactly as the run record
// contract resolves it (ancestor aliases resolved, final name kept). It is
// called only after state.OpenRoot validated the same directory.
func rootPath(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fail(CodeStateUnavailable)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fail(CodeStateUnavailable)
	}
	if parent == abs {
		return abs, nil
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

// checkPrivateDir classifies an existing path without following it. It
// returns fs.ErrNotExist for an absent path.
func checkPrivateDir(path string) (fs.FileInfo, error) {
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fs.ErrNotExist
	case err != nil:
		return nil, fail(CodeStateUnavailable)
	case fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir():
		return nil, fail(CodeUnsafeStatePath)
	case !private(fi, 0o700):
		return nil, fail(CodeStatePermissions)
	}
	return fi, nil
}

// checkNamespace validates an existing execute-v1 namespace without creating
// anything.
func checkNamespace(ns string) error {
	if _, err := checkPrivateDir(ns); err != nil && err != fs.ErrNotExist {
		return err
	}
	return nil
}

// checkAttempt is the existing-attempt check: any safe existing ID directory
// is execution_exists without opening its contents.
func checkAttempt(dir string) error {
	switch _, err := checkPrivateDir(dir); {
	case err == fs.ErrNotExist:
		return nil
	case err != nil:
		return err
	}
	return fail(CodeExists)
}

// opened is an internal test observer given every plan, directory and
// staging descriptor this package opens, so tests can prove each is closed.
// Production code never sets it.
var opened func(*os.File)

func observe(f *os.File) {
	if opened != nil && f != nil {
		opened(f)
	}
}

func openDir(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	observe(f)
	return f, nil
}

// openChecked opens a directory and rechecks type, owner, mode and identity
// on the descriptor against a fresh Lstat.
func openChecked(path, stage string) (*os.File, error) {
	if err := at(stage + "-open"); err != nil {
		return nil, err
	}
	lfi, err := checkPrivateDir(path)
	if err != nil {
		if err == fs.ErrNotExist {
			return nil, fail(CodeStateChanged)
		}
		return nil, err
	}
	f, err := openDir(path)
	if err != nil {
		return nil, fail(CodeStateUnavailable)
	}
	fi, err := f.Stat()
	if err == nil {
		err = at(stage + "-recheck")
	}
	if err != nil || !fi.IsDir() || !private(fi, 0o700) || !os.SameFile(fi, lfi) {
		f.Close()
		return nil, fail(CodeStateChanged)
	}
	return f, nil
}

func syncClose(f *os.File, stage string, want ...string) error {
	err := at(stage + "-sync")
	if err == nil && want != nil {
		names, rerr := f.Readdirnames(-1)
		sort.Strings(names)
		if rerr != nil || len(names) != len(want) {
			err = fail(CodeStateChanged)
		}
		for i := 0; err == nil && i < len(want); i++ {
			if names[i] != want[i] {
				err = fail(CodeStateChanged)
			}
		}
	} else if err == nil {
		if names, rerr := f.Readdirnames(-1); rerr != nil || len(names) != 0 {
			err = fail(CodeStateChanged)
		}
	}
	if err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if cerr == nil {
		cerr = at(stage + "-close")
	}
	if err != nil {
		return err
	}
	return cerr
}

// mkdirOwn exclusively creates a new 0700 directory and removes any umask
// reduction from that new directory only.
func mkdirOwn(path, stage string) error {
	if err := at(stage + "-mkdir"); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	if err := at(stage + "-chmod"); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// acquire creates the namespace if needed and the exclusive attempt
// directory with its six scratch directories, all synced and closed. Errors
// before the exclusive ID mkdir are storage/durability codes; afterwards
// every failure is execution_uncertain. owned reports that this call
// created the attempt directory.
func acquire(root, ns, attempt string, cancelled func() bool) (owned bool, err error) {
	// Namespace.
	if err := at("ns-mkdir"); err != nil {
		return false, fail(CodeStorageUnavailable)
	}
	if err := os.Mkdir(ns, 0o700); err == nil {
		if at("ns-chmod") != nil || os.Chmod(ns, 0o700) != nil {
			return false, fail(CodeStorageUnavailable)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return false, fail(CodeStorageUnavailable)
	}
	if err := checkNamespace(ns); err != nil {
		return false, err
	}
	if _, err := checkPrivateDir(ns); err != nil {
		return false, fail(CodeStorageUnavailable)
	}
	// The root is synced even when the namespace already existed (a losing
	// creator may otherwise rely on an unsynced entry). Its open descriptor
	// is rechecked for type, owner, mode and identity before the Sync.
	rlfi, err := checkPrivateDir(root)
	if err != nil || at("root-lstat") != nil {
		return false, fail(CodeStorageUnavailable)
	}
	rootDir, err := openDir(root)
	if err != nil || at("root-open") != nil {
		if rootDir != nil {
			rootDir.Close()
		}
		return false, fail(CodeStorageUnavailable)
	}
	rfi, err := rootDir.Stat()
	if err == nil {
		err = at("root-recheck")
	}
	if err != nil || !rfi.IsDir() || !private(rfi, 0o700) || !os.SameFile(rfi, rlfi) {
		rootDir.Close()
		return false, fail(CodeStorageUnavailable)
	}
	serr := at("root-sync")
	if serr == nil {
		serr = rootDir.Sync()
	}
	cerr := rootDir.Close()
	if cerr == nil {
		cerr = at("root-close")
	}
	if serr != nil {
		return false, fail(CodeDurability)
	}
	if cerr != nil {
		return false, fail(CodeStorageUnavailable)
	}
	nsDir, err := openChecked(ns, "ns")
	if err != nil {
		return false, fail(CodeStorageUnavailable)
	}
	if cancelled() {
		nsDir.Close()
		return false, fail(CodeCancelled)
	}

	// Exclusive attempt directory: only this mkdir grants ownership.
	if err := at("id-mkdir"); err != nil {
		nsDir.Close()
		return false, fail(CodeStorageUnavailable)
	}
	if err := os.Mkdir(attempt, 0o700); err != nil {
		nsDir.Close()
		if errors.Is(err, fs.ErrExist) {
			if err := checkAttempt(attempt); err != nil {
				return false, err
			}
			return false, fail(CodeStorageUnavailable)
		}
		return false, fail(CodeStorageUnavailable)
	}
	// From here on every failure is uncertain; nothing is removed.
	uncertain := fail(CodeUncertain)
	if at("id-chmod") != nil || os.Chmod(attempt, 0o700) != nil {
		nsDir.Close()
		return true, uncertain
	}
	serr = at("ns-sync")
	if serr == nil {
		serr = nsDir.Sync()
	}
	cerr = nsDir.Close()
	if cerr == nil {
		cerr = at("ns-close")
	}
	if serr != nil || cerr != nil {
		return true, uncertain
	}
	idDir, err := openChecked(attempt, "id")
	if err != nil {
		return true, uncertain
	}
	for _, prog := range []string{"worker", "verifier"} {
		if err := scratch(filepath.Join(attempt, prog), prog); err != nil {
			idDir.Close()
			return true, uncertain
		}
	}
	if err := syncClose(idDir, "id", "verifier", "worker"); err != nil {
		return true, uncertain
	}
	return true, nil
}

// scratch creates one program directory with its new empty home and tmp.
func scratch(dir, prog string) error {
	if err := mkdirOwn(dir, prog); err != nil {
		return err
	}
	for _, sub := range []string{"home", "tmp"} {
		stage := prog + "-" + sub
		path := filepath.Join(dir, sub)
		if err := mkdirOwn(path, stage); err != nil {
			return err
		}
		f, err := openChecked(path, stage)
		if err != nil {
			return err
		}
		if err := syncClose(f, stage); err != nil {
			return err
		}
	}
	f, err := openChecked(dir, prog)
	if err != nil {
		return err
	}
	return syncClose(f, prog, "home", "tmp")
}

// publish exclusively writes data as name in the attempt directory: retained
// staging file, Sync, close, hard link without replacement, directory Sync
// and close. Any failure is returned; the caller reports uncertainty.
func publish(attempt, name string, data []byte) error {
	var rnd [16]byte
	if err := at(name + "-random"); err != nil {
		return err
	}
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	staging := filepath.Join(attempt, ".pending-"+name+"-"+hex.EncodeToString(rnd[:]))
	if err := at(name + "-create"); err != nil {
		return err
	}
	f, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	observe(f)
	werr := at(name + "-chmod")
	if werr == nil {
		werr = f.Chmod(0o600)
	}
	if werr == nil {
		werr = at(name + "-write")
	}
	if werr == nil {
		var n int
		n, werr = f.Write(data)
		if werr == nil && n != len(data) {
			werr = errors.New("short write")
		}
	}
	if werr == nil {
		werr = at(name + "-sync")
	}
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if cerr == nil {
		cerr = at(name + "-close")
	}
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	if err := at(name + "-link"); err != nil {
		return err
	}
	if err := os.Link(staging, filepath.Join(attempt, name+".json")); err != nil {
		return err
	}
	d, err := openChecked(attempt, name+"-dir")
	if err != nil {
		return err
	}
	serr := at(name + "-dir-sync")
	if serr == nil {
		serr = d.Sync()
	}
	cerr = d.Close()
	if cerr == nil {
		cerr = at(name + "-dir-close")
	}
	if serr != nil {
		return serr
	}
	return cerr
}
