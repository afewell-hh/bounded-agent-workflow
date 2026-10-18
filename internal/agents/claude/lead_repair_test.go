package claude

import "testing"

// A normal worktree is inside HOME, so user source rows also arise as
// ancestor rows. Deduplication must retain the stricter user owner rule.
func TestMatrixOverlappingUserOwner(t *testing.T) {
	paths, err := Matrix("/h", "/h/work", nil)
	if err != nil {
		t.Fatal(err)
	}
	root, mode, dev, ino := uint32(0), uint32(0o644), uint64(1), uint64(1)
	found := false
	for _, p := range paths {
		if p.Path != "/h/.claude/settings.json" {
			continue
		}
		found = true
		e := SourceEntry{Path: p.Path, Class: p.Class, Kind: p.Kind, Status: StatusSafe, UID: &root, Mode: &mode, Dev: &dev, Ino: &ino}
		if CheckOwners([]SourceEntry{e}, 501) != ErrSourceUnavailable {
			t.Fatal("ancestor row relaxed user ownership")
		}
	}
	if !found {
		t.Fatal("user settings row omitted")
	}
}

func TestMatrixCombinedPathBound(t *testing.T) {
	// Only one nested source base adds14 leaf rows, but its200 intermediate
	// directories also consume the fixed concrete metadata-path allocation.
	nested := ""
	for i := 0; i < 200; i++ {
		nested += "d/"
	}
	nested += "leaf"
	if _, err := Matrix("/h", "/w", []string{nested}); err != ErrSourceUnavailable {
		t.Fatalf("combined matrix bound: %v", err)
	}
}
