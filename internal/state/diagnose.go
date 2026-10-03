package state

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// Diagnosis limits: all namespace names, and matching staging names.
const (
	MaxNamespaceEntries = 1024
	MaxStagingEntries   = 32
)

// Observed final-record states besides the structural Codes
// invalid_record, record_too_large and unsupported_record_version.
const (
	ObservedMissing = "missing"
	ObservedValid   = "valid"
)

// StagingCounts counts matching staging entries by validation category.
// LinkedToFinal overlaps the categories and is never added to Total.
type StagingCounts struct {
	Total, Valid, InvalidRecord, RecordTooLarge, UnsupportedVersion, LinkedToFinal int
}

// Diagnosis is a sequence of structural observations of one run ID. It is
// not an atomic snapshot and says nothing about durability or activity.
type Diagnosis struct {
	NamespacePresent bool
	Final            string // ObservedMissing, ObservedValid or a structural Code
	Staging          StagingCounts
}

// diagTrack is an internal test seam told of every descriptor Diagnose opens
// and closes. Production code never sets it.
var diagTrack func(f *os.File, opened bool)

func track(f *os.File, opened bool) {
	if diagTrack != nil {
		diagTrack(f, opened)
	}
}

// closeFile closes f; an injected or real close error is returned.
func closeFile(f *os.File, path string) error {
	err := at("diag-close", path)
	if e := f.Close(); err == nil {
		err = e
	}
	track(f, false)
	return err
}

// Diagnose observes the final record and the strictly named retained staging
// entries of id. It creates, changes, repairs and executes nothing, and
// returns either a complete Diagnosis or only a fixed error code.
func (r *Root) Diagnose(id string) (Diagnosis, error) {
	if !r.nsOK {
		// The absent observation was made by OpenRoot and is terminal: no
		// later namespace access happens, whatever this boundary sees.
		at("diag-absent", r.ns)
		return Diagnosis{Final: ObservedMissing}, nil
	}
	pending, err := r.scan(id)
	if err != nil {
		return Diagnosis{}, err
	}
	d := Diagnosis{NamespacePresent: true}
	finalPath := r.recordPath(id)
	cat, final, finalFI, err := observe(finalPath, id, false, true)
	if err != nil {
		return Diagnosis{}, err
	}
	d.Final = cat
	for _, name := range pending {
		cat, _, fi, err := observe(filepath.Join(r.ns, name), id, true, false)
		if err != nil {
			if final != nil {
				closeFile(final, finalPath) // the earlier error wins
			}
			return Diagnosis{}, err
		}
		s := &d.Staging
		s.Total++
		switch cat {
		case ObservedValid:
			s.Valid++
		case string(CodeInvalidRecord):
			s.InvalidRecord++
		case string(CodeRecordTooLarge):
			s.RecordTooLarge++
		case string(CodeUnsupportedVersion):
			s.UnsupportedVersion++
		}
		if final != nil && os.SameFile(fi, finalFI) {
			s.LinkedToFinal++
		}
	}
	if final != nil {
		if err := closeFile(final, finalPath); err != nil {
			return Diagnosis{}, fail(CodeRecordUnavailable)
		}
	}
	return d, nil
}

// scan reads at most MaxNamespaceEntries+1 direct names and returns the sorted
// strictly matching staging names of id. Other names are never statted.
func (r *Root) scan(id string) ([]string, error) {
	err := at("diag-scan-open", r.ns)
	var dir *os.File
	if err == nil {
		dir, err = os.OpenFile(r.ns, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		return nil, fail(CodeStateUnavailable)
	}
	track(dir, true)
	names, err := readNames(dir, r.ns)
	if cerr := closeFile(dir, r.ns); err == nil && cerr != nil {
		err = fail(CodeStateUnavailable)
	}
	if err != nil {
		return nil, err
	}
	if len(names) > MaxNamespaceEntries {
		return nil, fail(CodeStateScanLimit)
	}
	prefix := ".pending-" + id + "-"
	var matching []string
	for _, n := range names {
		// RANDOM has the same form as a run ID: 32 lowercase hex.
		if rnd, ok := strings.CutPrefix(n, prefix); ok && runIDRE.MatchString(rnd) {
			matching = append(matching, n)
		}
	}
	if len(matching) > MaxStagingEntries {
		return nil, fail(CodeStateScanLimit)
	}
	sort.Strings(matching)
	return matching, nil
}

// readNames checks the namespace descriptor, then reads names until the end
// or one past the cap.
func readNames(dir *os.File, ns string) ([]string, error) {
	err := at("diag-scan-stat", ns)
	var fi fs.FileInfo
	if err == nil {
		fi, err = dir.Stat()
	}
	switch {
	case err != nil:
		return nil, fail(CodeStateUnavailable)
	case !fi.IsDir():
		return nil, fail(CodeUnsafeStatePath)
	case !private(fi, 0o700):
		return nil, fail(CodeStatePermissions)
	}
	var names []string
	for len(names) <= MaxNamespaceEntries {
		if err := at("diag-scan-read", ns); err != nil {
			return nil, fail(CodeStateUnavailable)
		}
		batch, err := dir.Readdirnames(MaxNamespaceEntries + 1 - len(names))
		names = append(names, batch...)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fail(CodeStateUnavailable)
		}
	}
	return names, nil
}

// observe classifies one relevant entry. listed marks an enumerated staging
// name, whose absence at its first Lstat means the namespace changed. With
// keepValid a valid file's descriptor is returned open; otherwise every
// descriptor is closed before return. fi is the descriptor's metadata.
func observe(path, id string, listed, keepValid bool) (cat string, f *os.File, fi fs.FileInfo, err error) {
	err = at("diag-lstat", path)
	var lfi fs.FileInfo
	if err == nil {
		lfi, err = os.Lstat(path)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist) && listed:
		return "", nil, nil, fail(CodeStateChanged)
	case errors.Is(err, fs.ErrNotExist):
		return ObservedMissing, nil, nil, nil
	case err != nil:
		return "", nil, nil, fail(CodeRecordUnavailable)
	case !lfi.Mode().IsRegular():
		return "", nil, nil, fail(CodeUnsafeStatePath)
	case !private(lfi, 0o600):
		return "", nil, nil, fail(CodeStatePermissions)
	}
	err = at("diag-open", path)
	if err == nil {
		f, err = os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	}
	if err != nil {
		// After a positive Lstat, a missing name or a symlink means change.
		if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ELOOP) {
			return "", nil, nil, fail(CodeStateChanged)
		}
		return "", nil, nil, fail(CodeRecordUnavailable)
	}
	track(f, true)
	cat, fi, err = classifyOpen(f, path, lfi, id)
	if err != nil {
		closeFile(f, path) // the earlier error wins
		return "", nil, nil, err
	}
	if cat == ObservedValid && keepValid {
		return cat, f, fi, nil
	}
	if err := closeFile(f, path); err != nil {
		return "", nil, nil, fail(CodeRecordUnavailable)
	}
	return cat, nil, fi, nil
}

// classifyOpen checks descriptor safety before identity against the Lstat
// baseline, then reads at most MaxRecordBytes+1 and strictly parses.
func classifyOpen(f *os.File, path string, lfi fs.FileInfo, id string) (string, fs.FileInfo, error) {
	err := at("diag-fstat", path)
	var fi fs.FileInfo
	if err == nil {
		fi, err = f.Stat()
	}
	switch {
	case err != nil:
		return "", nil, fail(CodeRecordUnavailable)
	case !fi.Mode().IsRegular():
		return "", nil, fail(CodeUnsafeStatePath)
	case !private(fi, 0o600):
		return "", nil, fail(CodeStatePermissions)
	case !os.SameFile(lfi, fi):
		return "", nil, fail(CodeStateChanged)
	}
	err = at("diag-read", path)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(f, MaxRecordBytes+1))
	}
	if err != nil {
		return "", nil, fail(CodeRecordUnavailable)
	}
	if _, err := Parse(data, id); err != nil {
		var e *Error
		if !errors.As(err, &e) {
			return "", nil, fail(CodeRecordUnavailable)
		}
		return string(e.Code), fi, nil
	}
	return ObservedValid, fi, nil
}
