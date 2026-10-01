// Package testfixture builds disposable dummy Git repositories for tests and
// recorded binary journeys. It runs the real git executable with an explicit
// isolated environment and returns facts established by the fixture setup
// itself, independently of the inspector under test.
package testfixture

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// GitOutput runs fixture setup git in dir and returns its stdout and stderr
// separately. Stderr (for example a host Git warning) is diagnostic evidence
// only and never becomes part of a returned value.
func GitOutput(home, dir string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "init.defaultBranch=main", "-c", "protocol.file.allow=always", "-c", "core.excludesFile=/dev/null"}, args...)...)
	cmd.Env = Env(home)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), errb.String(), fmt.Errorf("fixture git %v: %v; stderr: %q", args, err, errb.String())
	}
	return out.String(), errb.String(), nil
}

// Git runs fixture setup git in dir and returns trimmed stdout only.
func Git(home, dir string, args ...string) (string, error) {
	out, _, err := GitOutput(home, dir, args...)
	return strings.TrimSpace(out), err
}

var oidRE = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// OID resolves rev to a full object ID and rejects anything else, so a
// diagnostic can never be mistaken for a fixture fact.
func OID(home, dir, rev string) (string, error) {
	out, err := Git(home, dir, "rev-parse", "--verify", "--end-of-options", rev)
	if err != nil {
		return "", err
	}
	if !oidRE.MatchString(out) {
		return "", fmt.Errorf("fixture rev-parse %s: not a full object ID: %q", rev, out)
	}
	return out, nil
}

// HostWarning is the stderr line the independent reviewer's sandboxed Apple
// Git printed on every invocation.
const HostWarning = "git: warning: confstr() failed with code 5: couldn't get path of DARWIN_USER_TEMP_DIR; using /tmp instead"

// InstallWarningGit writes dir/git, a wrapper that prints HostWarning to
// stderr and then runs the real git found on the current PATH. Prepending dir
// to PATH reproduces that environment for fixtures and the inspector alike.
func InstallWarningGit(dir string) error {
	real, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	if strings.ContainsRune(real, '\'') {
		return fmt.Errorf("unsupported git path")
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"" + HostWarning + "\" >&2\nexec '" + real + "' \"$@\"\n"
	if err := Write(filepath.Join(dir, "git"), script); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(dir, "git"), 0o755)
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
	return OID(home, dir, "HEAD")
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
