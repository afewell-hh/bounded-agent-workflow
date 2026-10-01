// Package testfixture builds disposable dummy Git repositories for tests and
// recorded binary journeys. It runs the real git executable with an explicit
// isolated environment and returns facts established by the fixture setup
// itself, independently of the inspector under test.
package testfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Dummy secret-like values. They are arbitrary, not token-shaped, and must
// never appear in inspector output.
const (
	SecretBranch  = "zebra-quartz-7731-branchsecret"
	SecretFile    = "plover-umbra-5519-filesecret"
	SecretContent = "marmot-velvet-8842-contentsecret"
	SecretMessage = "heron-cobalt-2290-messagesecret"
	SecretRemote  = "lynx-saffron-6604-remotesecret"
	SecretConfig  = "ibis-tundra-3377-configsecret"
	SecretIgnored = "otter-maple-9915-ignoredsecret"
	SecretProse   = "finch-garnet-4418-prosesecret"
)

// Sources mirrors the fixed allowlist.
var Sources = []string{
	"AGENTS.md",
	"workflow/protocol.md",
	"workflow/roles/lead.md",
	"README.md",
	"docs/design/product-contract.md",
	"docs/architecture/overview.md",
	"docs/developer/environment.md",
	"docs/operator/agent-lifecycle.md",
}

// Env is the isolated environment used for fixture setup.
func Env(home string) []string {
	return []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid",
		"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		"LC_ALL=C",
	}
}

// Git runs fixture setup git in dir and returns trimmed stdout.
func Git(home, dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always", "-c", "core.excludesFile=/dev/null"}, args...)...)
	cmd.Env = Env(home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("fixture git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// Write creates a file with parents.
func Write(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// Init creates a repository with the given object format ("sha1"/"sha256").
func Init(home, dir, format string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	_, err := Git(home, dir, "init", "-q", "--object-format="+format)
	return err
}

// CommitSources writes and commits all allowlisted sources and returns HEAD.
func CommitSources(home, dir string) (string, error) {
	for _, s := range Sources {
		if err := Write(filepath.Join(dir, s), "dummy maintained source\n"); err != nil {
			return "", err
		}
	}
	if _, err := Git(home, dir, "add", "-A"); err != nil {
		return "", err
	}
	if _, err := Git(home, dir, "commit", "-q", "-m", "fixture sources"); err != nil {
		return "", err
	}
	return Git(home, dir, "rev-parse", "HEAD")
}

// Operator builds the operator journey fixture: committed sources plus one
// staged file, one unstaged file and one untracked file. Returns HEAD.
func Operator(home, dir string) (string, error) {
	if err := Init(home, dir, "sha1"); err != nil {
		return "", err
	}
	if err := Write(filepath.Join(dir, "staged.txt"), "base\n"); err != nil {
		return "", err
	}
	if err := Write(filepath.Join(dir, "unstaged.txt"), "base\n"); err != nil {
		return "", err
	}
	head, err := CommitSources(home, dir)
	if err != nil {
		return "", err
	}
	if err := Write(filepath.Join(dir, "staged.txt"), "staged change\n"); err != nil {
		return "", err
	}
	if _, err := Git(home, dir, "add", "staged.txt"); err != nil {
		return "", err
	}
	if err := Write(filepath.Join(dir, "unstaged.txt"), "unstaged change\n"); err != nil {
		return "", err
	}
	if err := Write(filepath.Join(dir, "untracked.txt"), "untracked dummy\n"); err != nil {
		return "", err
	}
	return head, nil
}
