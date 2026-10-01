package testfixture

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A host Git that warns on stderr must not corrupt fixture facts: returned
// OIDs come from stdout only and are validated; the warning stays available
// as separate diagnostic output.
func TestWarningGitDoesNotCorruptFixtureFacts(t *testing.T) {
	bin := t.TempDir()
	if err := InstallWarningGit(bin); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "r")

	if err := Init(home, dir, "sha1"); err != nil {
		t.Fatal(err)
	}
	head, err := CommitSources(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(head) != 40 || strings.Contains(head, "warning") {
		t.Fatalf("fixture HEAD %q", head)
	}
	stdout, stderr, err := GitOutput(home, dir, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(stdout) != head || !strings.Contains(stderr, HostWarning) {
		t.Fatalf("separation: stdout %q stderr %q err %v", stdout, stderr, err)
	}
	// The wrapper really is the git being run (positive control).
	if _, stderr, _ := GitOutput(home, dir, "--version"); !strings.Contains(stderr, "DARWIN_USER_TEMP_DIR") {
		t.Fatal("warning wrapper not in effect")
	}
	// A validated OID is usable as a gitlink argument.
	if _, err := Git(home, dir, "update-index", "--add", "--cacheinfo", "160000,"+head+",sub"); err != nil {
		t.Fatal(err)
	}
	if _, err := OID(home, dir, "HEAD:nonexistent"); err == nil {
		t.Fatal("missing object accepted")
	}
}
