package claude

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// Fixed identities of the single supported profile.
const (
	ProfileID      = "claude-2.1.285-supervised-v1"
	SourceMatrixID = "claude-2.1.285-source-v1"
	Version        = "2.1.285"
	Model          = "claude-opus-5-5"
	MaxProfile     = 65536
	MaxPlugins     = 64

	// InjectionDisposition is the fixed per-run same-account disposition.
	InjectionDisposition = "same_account_no_quiescence_stream_stops_unstreamed_unknown"
)

// ErrProfile reports an invalid profile manifest. It carries no detail.
var ErrProfile = errors.New("profile invalid")

// Profile is a validated schema version 1 profile manifest. It is nonsecret
// coordinator-reviewed configuration, not authentication material.
type Profile struct {
	Executable       string
	ExecutableSHA256 string
	ApprovedSHA256   string
	UserHome         string
	UserName         string
	Shell            string
	Plugins          []string // canonical encoding of each allowed plugin tuple
}

var (
	hex64RE = regexp.MustCompile(`^[0-9a-f]{64}$`)
	userRE  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

var profileKeys = []string{"schema_version", "profile_id", "executable", "executable_sha256",
	"approved_profile_sha256", "user_home", "user_name", "shell", "allowed_plugins", "source_matrix_id"}

// cleanAbs reports whether p is an absolute, lexically clean path without
// NUL or control characters, at most 1024 bytes.
func cleanAbs(p string) bool {
	if p == "" || len(p) > 1024 || !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// versioned reports whether some path component names the pinned version.
func versioned(p string) bool {
	for _, c := range strings.Split(p, "/") {
		if strings.Contains(c, Version) {
			return true
		}
	}
	return false
}

// CanonicalProfileSHA256 is the SHA-256 of the canonical encoding of a decoded
// profile object without its approved_profile_sha256 member.
func CanonicalProfileSHA256(m map[string]any) (string, error) {
	c := make(map[string]any, len(m))
	for k, v := range m {
		if k != "approved_profile_sha256" {
			c[k] = v
		}
	}
	b, err := Canonical(c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ParseProfile strictly validates profile bytes: exact keys and literal
// identities, absolute clean paths, a versioned executable path, duplicate-free
// object plugin tuples and the declared canonical approved_profile_sha256.
func ParseProfile(data []byte) (Profile, error) {
	var p Profile
	if len(data) > MaxProfile {
		return p, ErrProfile
	}
	m, ok := StrictObject(data)
	if !ok || !ExactKeys(m, profileKeys...) || !IsOne(m["schema_version"]) ||
		m["profile_id"] != ProfileID || m["source_matrix_id"] != SourceMatrixID {
		return p, ErrProfile
	}
	strs := map[string]*string{"executable": &p.Executable, "executable_sha256": &p.ExecutableSHA256,
		"approved_profile_sha256": &p.ApprovedSHA256, "user_home": &p.UserHome, "user_name": &p.UserName, "shell": &p.Shell}
	for k, dst := range strs {
		s, ok := m[k].(string)
		if !ok {
			return Profile{}, ErrProfile
		}
		*dst = s
	}
	if !cleanAbs(p.Executable) || !versioned(p.Executable) || !hex64RE.MatchString(p.ExecutableSHA256) ||
		!hex64RE.MatchString(p.ApprovedSHA256) || !cleanAbs(p.UserHome) || p.UserHome == "/" ||
		!userRE.MatchString(p.UserName) || !cleanAbs(p.Shell) {
		return Profile{}, ErrProfile
	}
	plugins, ok := m["allowed_plugins"].([]any)
	if !ok || len(plugins) > MaxPlugins {
		return Profile{}, ErrProfile
	}
	seen := map[string]bool{}
	p.Plugins = []string{}
	for _, v := range plugins {
		if _, ok := v.(map[string]any); !ok {
			return Profile{}, ErrProfile
		}
		b, err := Canonical(v)
		if err != nil || seen[string(b)] {
			return Profile{}, ErrProfile
		}
		seen[string(b)] = true
		p.Plugins = append(p.Plugins, string(b))
	}
	sum, err := CanonicalProfileSHA256(m)
	if err != nil || sum != p.ApprovedSHA256 {
		return Profile{}, ErrProfile
	}
	return p, nil
}

type permissions struct {
	Allow []string `json:"allow"`
	Deny  []string `json:"deny"`
}

type settings struct {
	Permissions permissions `json:"permissions"`
}

func compactJSON(v any) string {
	b, _ := Canonical(v) // fixed struct shapes cannot fail to encode
	return string(b)
}

// Args returns the frozen argv after the executable for canonical worktree w.
// The Edit/Write rules are generated from w only ('/' + w + "/**"); no caller
// rules, prompt or schema flag are accepted. The packet is supplied on stdin.
func Args(w string) []string {
	rule := "/" + w + "/**"
	s := settings{Permissions: permissions{Allow: []string{"Edit(" + rule + ")", "Write(" + rule + ")"}, Deny: []string{}}}
	return []string{
		"--print",
		"--safe-mode",
		"--restricted",
		"--setting-sources", "",
		"--permission-mode", "dontAsk",
		"--permission-prompts", "none",
		"--strict-mcp-config",
		"--mcp-config", `{"mcpServers":{}}`,
		"--tools", "Read,Glob,Grep,Edit,Write",
		"--settings", compactJSON(s),
		"--no-chrome",
		"--no-session-persistence",
		"--output-format", "stream-json",
		"--verbose",
		"--model", Model,
		"--disable-slash-commands",
		"--include-hook-events",
	}
}

// Env returns the complete child environment, built from scratch. Nothing is
// inherited from the parent.
func Env(p Profile, tmp string) []string {
	return []string{
		"HOME=" + p.UserHome,
		"TMPDIR=" + tmp,
		"PATH=/usr/bin:/bin:/opt/homebrew/bin",
		"LANG=C",
		"LC_ALL=C",
		"USER=" + p.UserName,
		"LOGNAME=" + p.UserName,
		"SHELL=" + p.Shell,
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1",
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY=1",
		"DISABLE_AUTOUPDATER=1",
	}
}

// pluginKey canonicalizes one decoded init plugin value.
func pluginKey(v any) (string, bool) {
	if _, ok := v.(map[string]any); !ok {
		return "", false
	}
	b, err := Canonical(v)
	return string(b), err == nil
}
