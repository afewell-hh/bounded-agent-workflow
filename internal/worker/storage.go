package worker

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

// Namespace is the version 1 worker attempt directory inside the state root,
// disjoint from records-v1, execute-v1, review-v1 and verify-v1.
const Namespace = "worker-v1"

// store performs the attempt's storage steps. fault, when non-nil, is a
// per-call seam called before each named stage; an error is that stage's
// failure.
type store struct {
	fault func(stage string) error
}

func (s store) at(stage string) error {
	if s.fault == nil {
		return nil
	}
	return s.fault(stage)
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

func checkNamespace(ns string) error {
	if _, err := checkPrivateDir(ns); err != nil && err != fs.ErrNotExist {
		return err
	}
	return nil
}

// checkAttempt: any safe existing ID directory is worker_exists without
// opening its contents; other I/O is worker_storage_unavailable.
func checkAttempt(dir string) error {
	switch _, err := checkPrivateDir(dir); {
	case err == fs.ErrNotExist:
		return nil
	case IsCode(err, CodeStateUnavailable):
		return fail(CodeStorageUnavailable)
	case err != nil:
		return err
	}
	return fail(CodeExists)
}

func openDir(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
}

// openChecked opens a private directory and rechecks type, owner, mode and
// identity on the descriptor against a fresh Lstat.
func (s store) openChecked(path, stage string) (*os.File, error) {
	if err := s.at(stage + "-open"); err != nil {
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
	if err != nil || !fi.IsDir() || !private(fi, 0o700) || !os.SameFile(fi, lfi) {
		f.Close()
		return nil, fail(CodeStateChanged)
	}
	return f, nil
}

// syncClose checks the directory holds exactly want, Syncs and closes it.
func (s store) syncClose(f *os.File, stage string, want ...string) error {
	err := s.at(stage + "-sync")
	if err == nil {
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
	}
	if err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err != nil {
		return err
	}
	return cerr
}

func (s store) mkdirOwn(path, stage string) error {
	if err := s.at(stage + "-mkdir"); err != nil {
		return err
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// preID keeps an identified state safety error; anything else before the
// exclusive ID mkdir is worker_storage_unavailable.
func preID(err error) error {
	if IsCode(err, CodeUnsafeStatePath) || IsCode(err, CodeStatePermissions) || IsCode(err, CodeStateChanged) {
		return err
	}
	return fail(CodeStorageUnavailable)
}

// acquire creates the namespace if needed and the exclusive attempt directory
// with worker/tmp, all synced and closed. Failures and cancellation before
// the exclusive ID mkdir change no attempt state; afterwards every failure is
// worker_uncertain and nothing is removed.
func (s store) acquire(root, ns, attempt string, cancelled func() bool) error {
	if err := s.at("ns-mkdir"); err != nil {
		return fail(CodeStorageUnavailable)
	}
	if err := os.Mkdir(ns, 0o700); err == nil {
		if os.Chmod(ns, 0o700) != nil {
			return fail(CodeStorageUnavailable)
		}
	} else if !errors.Is(err, fs.ErrExist) {
		return fail(CodeStorageUnavailable)
	}
	if _, err := checkPrivateDir(ns); err != nil {
		return preID(err)
	}
	for _, d := range []struct{ path, stage string }{{root, "root"}, {ns, "ns"}} {
		f, err := s.openChecked(d.path, d.stage)
		if err != nil {
			return preID(err)
		}
		serr := s.at(d.stage + "-sync")
		if serr == nil {
			serr = f.Sync()
		}
		cerr := f.Close()
		if serr != nil {
			return fail(CodeDurability)
		}
		if cerr != nil {
			return fail(CodeStorageUnavailable)
		}
	}
	if cancelled() {
		return fail(CodeCancelled)
	}
	if err := s.at("id-mkdir"); err != nil {
		return fail(CodeStorageUnavailable)
	}
	if err := os.Mkdir(attempt, 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			if err := checkAttempt(attempt); err != nil {
				return err
			}
		}
		return fail(CodeStorageUnavailable)
	}
	// From here on every failure is uncertain.
	uncertain := fail(CodeUncertain)
	if s.at("id-chmod") != nil || os.Chmod(attempt, 0o700) != nil {
		return uncertain
	}
	nsDir, err := s.openChecked(ns, "ns-after")
	if err != nil {
		return uncertain
	}
	serr := nsDir.Sync()
	if nsDir.Close() != nil || serr != nil {
		return uncertain
	}
	wdir := filepath.Join(attempt, "worker")
	if s.mkdirOwn(wdir, "worker") != nil || s.mkdirOwn(filepath.Join(wdir, "tmp"), "worker-tmp") != nil {
		return uncertain
	}
	for _, d := range []struct {
		path, stage string
		want        []string
	}{{filepath.Join(wdir, "tmp"), "worker-tmp", nil}, {wdir, "worker", []string{"tmp"}}, {attempt, "id", []string{"worker"}}} {
		f, err := s.openChecked(d.path, d.stage)
		if err != nil {
			return uncertain
		}
		if s.syncClose(f, d.stage, d.want...) != nil {
			return uncertain
		}
	}
	return nil
}

// publish exclusively writes data as name in the attempt directory: retained
// staging file, Sync, close, hard link without replacement, directory Sync
// and close. Any failure is returned; the caller reports uncertainty.
func (s store) publish(attempt, name string, data []byte) error {
	var rnd [16]byte
	if err := s.at(name + "-random"); err != nil {
		return err
	}
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	staging := filepath.Join(attempt, ".pending-"+name+"-"+hex.EncodeToString(rnd[:]))
	if err := s.at(name + "-create"); err != nil {
		return err
	}
	f, err := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	werr := f.Chmod(0o600)
	if werr == nil {
		werr = s.at(name + "-write")
	}
	if werr == nil {
		var n int
		n, werr = f.Write(data)
		if werr == nil && n != len(data) {
			werr = errors.New("short write")
		}
	}
	if werr == nil {
		werr = s.at(name + "-sync")
	}
	if werr == nil {
		werr = f.Sync()
	}
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	if cerr != nil {
		return cerr
	}
	if err := s.at(name + "-link"); err != nil {
		return err
	}
	if err := os.Link(staging, filepath.Join(attempt, name)); err != nil {
		return err
	}
	d, err := s.openChecked(attempt, name+"-dir")
	if err != nil {
		return err
	}
	serr := s.at(name + "-dir-sync")
	if serr == nil {
		serr = d.Sync()
	}
	cerr = d.Close()
	if serr != nil {
		return serr
	}
	return cerr
}
