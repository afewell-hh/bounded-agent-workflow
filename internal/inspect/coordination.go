package inspect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/proc"
)

var (
	ownerRE  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9]|-[A-Za-z0-9]){0,38}$`)
	repoRE   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
	numberRE = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	urlRE    = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/issues/([1-9][0-9]{0,9})$`)
)

// MaxIssueNumber is the largest accepted issue number.
const MaxIssueNumber = 2147483647

// ValidOwnerRepo reports whether owner/repo are allowed ASCII segments.
func ValidOwnerRepo(owner, repo string) bool {
	return ownerRE.MatchString(owner) && repoRE.MatchString(repo) && repo != "." && repo != ".."
}

// ParseIssueNumber validates a decimal issue number.
func ParseIssueNumber(s string) (int, bool) {
	if !numberRE.MatchString(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n > MaxIssueNumber {
		return 0, false
	}
	return int(n), true
}

type issue struct {
	owner, repo string
	number      int
	state       string
	updatedAt   string
	url         string
}

func parseIssue(data []byte) (issue, error) {
	bad := fail(CodeCoordinationInvalid)
	var is issue
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil || raw == nil {
		return is, bad
	}
	if _, err := dec.Token(); err != io.EOF {
		return is, bad
	}
	if _, ok := raw["pull_request"]; ok {
		return is, bad
	}
	var num json.Number
	var state, updated, url string
	// json.Number also accepts a quoted string; require a bare JSON number.
	if rn := raw["number"]; len(rn) == 0 || rn[0] < '0' || rn[0] > '9' {
		return is, bad
	}
	if json.Unmarshal(raw["number"], &num) != nil ||
		json.Unmarshal(raw["state"], &state) != nil ||
		json.Unmarshal(raw["updated_at"], &updated) != nil ||
		json.Unmarshal(raw["html_url"], &url) != nil {
		return is, bad
	}
	n, ok := ParseIssueNumber(num.String())
	if !ok {
		return is, bad
	}
	switch state {
	case "open":
		is.state = "OPEN"
	case "closed":
		is.state = "CLOSED"
	default:
		return is, bad
	}
	t, err := time.Parse(time.RFC3339, updated)
	if err != nil {
		return is, bad
	}
	is.updatedAt = t.UTC().Truncate(time.Second).Format("2006-01-02T15:04:05Z")
	m := urlRE.FindStringSubmatch(url)
	if m == nil || !ValidOwnerRepo(m[1], m[2]) || m[3] != num.String() {
		return is, bad
	}
	is.owner, is.repo, is.number, is.url = m[1], m[2], n, url
	return is, nil
}

func (is issue) coordination(mode string) Coordination {
	n, st, up, u := is.number, is.state, is.updatedAt, is.url
	return Coordination{Mode: mode, State: "observed", Number: &n, IssueState: &st, UpdatedAt: &up, SourceURL: &u}
}

func readSnapshot(path string, limit int) (Coordination, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Coordination{}, fail(CodeCoordinationUnavail)
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return Coordination{}, fail(CodeCoordinationUnavail)
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return Coordination{}, fail(CodeCoordinationUnavail)
	}
	if len(data) > limit {
		return Coordination{}, fail(CodeInputLimit)
	}
	is, err := parseIssue(data)
	if err != nil {
		return Coordination{}, err
	}
	return is.coordination("snapshot"), nil
}

var ghEnvAllow = []string{"HOME", "PATH", "TMPDIR", "USER", "LOGNAME", "LANG", "LC_ALL", "SSL_CERT_FILE", "SSL_CERT_DIR"}

// GHArgs returns the exact provider argv after the executable.
func GHArgs(owner, repo string, number int) []string {
	return []string{"api", "--method", "GET", "--hostname", "github.com",
		"repos/" + owner + "/" + repo + "/issues/" + strconv.Itoa(number)}
}

func readLive(ctx context.Context, opts Options) (Coordination, error) {
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return Coordination{}, fail(CodeCoordinationUnavail)
	}
	env := append(allowEnv(ghEnvAllow),
		"GH_NO_UPDATE_NOTIFIER=1",
		"GH_NO_EXTENSION_UPDATE_NOTIFIER=1",
		"GH_PROMPT_DISABLED=1",
		"GH_PAGER=cat",
		"NO_COLOR=1",
	)
	out, err := proc.Run(ctx, proc.Spec{
		Path: ghPath, Args: GHArgs(opts.GitHubOwner, opts.GitHubRepo, opts.GitHubNumber), Env: env,
		Timeout: opts.Limits.GHTimeout, StdoutCap: opts.Limits.GHStdout, StderrCap: opts.Limits.ChildStderr,
	})
	if err != nil {
		switch {
		case errors.Is(err, proc.ErrTimeout), errors.Is(err, proc.ErrCleanup):
			return Coordination{}, fail(CodeCommandTimeout)
		case errors.Is(err, proc.ErrOutputLimit):
			return Coordination{}, fail(CodeCommandOutputLimit)
		}
		return Coordination{}, fail(CodeCoordinationUnavail)
	}
	is, err := parseIssue(out)
	if err != nil {
		return Coordination{}, err
	}
	if is.number != opts.GitHubNumber || !strings.EqualFold(is.owner, opts.GitHubOwner) || !strings.EqualFold(is.repo, opts.GitHubRepo) {
		return Coordination{}, fail(CodeCoordinationInvalid)
	}
	return is.coordination("live"), nil
}
