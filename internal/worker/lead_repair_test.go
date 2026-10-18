package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkerAdditionalControlBoundaries(t *testing.T) {
	for _, name := range []string{"new local ref", "packed refs"} {
		f := newFx(t, "sha1")
		base := f.edit
		f.edit = func(top string) {
			base(top)
			if name == "new local ref" {
				f.ok(os.WriteFile(filepath.Join(top, ".git", "refs", "heads", "unexpected"), []byte(f.head+"\n"), 0o644))
			} else {
				f.ok(os.WriteFile(filepath.Join(top, ".git", "packed-refs"), []byte("# pack-refs with: peeled fully-peeled sorted\n"+f.head+" refs/heads/unexpected\n"), 0o644))
			}
		}
		_, completed, err := f.run(context.Background(), true)
		if err != nil || completed || f.runs != 1 {
			t.Errorf("%s: completed=%v runs=%d err=%v", name, completed, f.runs, err)
		}
	}
	f := newFx(t, "sha1")
	cfg := filepath.Join(f.repo, ".git", "config")
	external := filepath.Join(f.priv, "external-config")
	f.ok(os.Rename(cfg, external))
	f.ok(os.Symlink(external, cfg))
	_, completed, err := f.run(context.Background(), true)
	if completed || f.runs != 0 || !IsCode(err, CodeNotClean) {
		t.Errorf("redirected config: completed=%v runs=%d err=%v", completed, f.runs, err)
	}
}

func TestWorkerScratchIdentityBeforeStart(t *testing.T) {
	for _, rel := range []string{"worker", "worker/tmp"} {
		f := newFx(t, "sha1")
		f.fault = func(stage string) error {
			if stage == "intent.json-dir-sync" {
				f.ok(os.Chmod(filepath.Join(f.attempt(), rel), 0o755))
			}
			return nil
		}
		_, completed, err := f.run(context.Background(), true)
		if completed || f.runs != 0 || !IsCode(err, CodeUncertain) {
			t.Errorf("%s: completed=%v runs=%d err=%v", rel, completed, f.runs, err)
		}
	}
}

func TestWorkerRuleSyntaxInWorkspaceRejected(t *testing.T) {
	f := newFx(t, "sha1")
	old := f.repo
	f.repo += "*"
	f.ok(os.Rename(old, f.repo))
	_, completed, err := f.run(context.Background(), true)
	if completed || f.runs != 0 || !IsCode(err, CodeUnsafeLayout) {
		t.Errorf("rule syntax in workspace: completed=%v runs=%d err=%v", completed, f.runs, err)
	}
}

func TestWorkerSavedRecordIdentity(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		f := newFx(t, "sha1")
		path := filepath.Join(f.stateDir, "records-v1", testRunID+".json")
		if phase == "before" {
			f.fault = func(stage string) error {
				if stage == "intent.json-dir-sync" {
					replaceSame(f, path)
				}
				return nil
			}
		} else {
			base := f.edit
			f.edit = func(top string) { base(top); replaceSame(f, path) }
		}
		_, completed, err := f.run(context.Background(), true)
		if completed || phase == "before" && (f.runs != 0 || !IsCode(err, CodeInputChanged)) || phase == "after" && (f.runs != 1 || err != nil) {
			t.Errorf("record %s: completed=%v runs=%d err=%v", phase, completed, f.runs, err)
		}
	}
}

func TestWorkerUncoveredGitIncludeRejected(t *testing.T) {
	f := newFx(t, "sha1")
	include := filepath.Join(f.priv, "included-config")
	f.ok(os.WriteFile(include, []byte("[core]\n\tfilemode = true\n"), 0o600))
	appendFile(f, filepath.Join(f.repo, ".git", "config"), "\n[include]\n\tpath = "+include+"\n")
	_, completed, err := f.run(context.Background(), true)
	if completed || f.runs != 0 || !IsCode(err, CodeNotClean) {
		t.Errorf("uncovered include: completed=%v runs=%d err=%v", completed, f.runs, err)
	}
}
