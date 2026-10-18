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

// The index itself is immutable, in addition to its staged entries/flags.
// Guarded read-only Git commands must leave its identity and bytes untouched.
func TestWorkerGitControlIdentity(t *testing.T) {
	for _, name := range []string{"unchanged", "index mode", "index symlink", "identical index replacement", "index version", "shallow"} {
		t.Run(name, func(t *testing.T) {
			f := newFx(t, "sha1")
			index := filepath.Join(f.repo, ".git", "index")
			before, err := os.ReadFile(index)
			f.ok(err)
			beforeInfo, err := os.Lstat(index)
			f.ok(err)
			base := f.edit
			f.edit = func(top string) {
				base(top)
				switch name {
				case "index mode":
					f.ok(os.Chmod(index, 0o666))
				case "index symlink":
					target := filepath.Join(f.priv, "outside-index")
					f.ok(os.Rename(index, target))
					f.ok(os.Symlink(target, index))
				case "identical index replacement":
					replaceSame(f, index)
				case "index version":
					gitIn(f, "update-index", "--index-version=4")
				case "shallow":
					f.ok(os.WriteFile(filepath.Join(top, ".git", "shallow"), []byte(f.head+"\n"), 0o600))
				}
			}
			out, completed, err := f.run(context.Background(), true)
			if err != nil || f.runs != 1 || completed != (name == "unchanged") {
				t.Fatalf("completed=%v runs=%d err=%v", completed, f.runs, err)
			}
			want := OutcomeUnusable
			if name == "unchanged" {
				want = OutcomeCompleted
				after, err := os.ReadFile(index)
				f.ok(err)
				afterInfo, err := os.Lstat(index)
				f.ok(err)
				if !os.SameFile(beforeInfo, afterInfo) || string(before) != string(after) {
					t.Fatal("guarded read-only commands changed index")
				}
			}
			if decodeResult(t, out).Outcome != want {
				t.Fatal("wrong outcome")
			}
			edit, err := os.ReadFile(filepath.Join(f.repo, "target.txt"))
			f.ok(err)
			if string(edit) != "marker: kestrel\n" {
				t.Fatal("worker edit was rolled back")
			}
		})
	}
	for _, name := range []string{"mode", "symlink"} {
		t.Run("unsafe before start "+name, func(t *testing.T) {
			f := newFx(t, "sha1")
			index := filepath.Join(f.repo, ".git", "index")
			if name == "mode" {
				f.ok(os.Chmod(index, 0o666))
			} else {
				target := filepath.Join(f.priv, "outside-index")
				f.ok(os.Rename(index, target))
				f.ok(os.Symlink(target, index))
			}
			_, completed, err := f.run(context.Background(), true)
			if completed || f.runs != 0 || !IsCode(err, CodeNotClean) {
				t.Fatalf("completed=%v runs=%d err=%v", completed, f.runs, err)
			}
		})
	}
}
