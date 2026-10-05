// Package cli implements the `baw` command line.
package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/afewell-hh/bounded-agent-workflow/internal/inspect"
	"github.com/afewell-hh/bounded-agent-workflow/internal/state"
)

// Usage is the fixed help text.
const Usage = `Usage:
  baw inspect --repo PATH [--checkpoint FULL_COMMIT_SHA] [--coordination-file FILE | --github OWNER/REPO#NUMBER] [--json]
  baw run create --state-dir DIR --run-id ID --repo PATH --ticket URL --scope-sha256 HASH --policy-commit OID [--json]
  baw status --state-dir DIR --run-id ID [--json]
  baw run diagnose --state-dir DIR --run-id ID [--json]
  baw context --repo PATH --role ROLE [--json]
  baw run execute --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]
  baw run review --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]
  baw run verify --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]
  baw --help

baw inspect reports read-only Git state counts, fixed maintained-source
references and coordination metadata. Remote freshness, runtime state,
process ownership and reservation ownership are always unknown.

baw run create saves an immutable run record of supplied references and the
observed committed HEAD in an existing private DIR. baw status reads that
saved record without running Git. A record is not an approval, completed
work or live state; authority is not evaluated.

baw run diagnose counts the structural state of that record and its
retained staging files, read-only and without Git. Observations are
sequential, not a snapshot; it gives no recovery advice.

baw context lists fixed onboarding source references for a lead, worker or
reviewer with the same read-only Git observations. It grants no assignment,
readiness or authority and starts no agent.

Exit status: 0 success, 1 failure (stderr "baw: CODE"), 2 invalid usage.

Run execute records one local worker and verification attempt; it evaluates no approval,
provides no native adapter and never retries or recovers an interrupted attempt.

Run review records one trusted reviewer-program verdict for a committed candidate;
it evaluates no approval and supplies no native agent adapter, verification or merge authority.

Run verify records one local verifier-program result on an identified candidate;
it grants no approval, source freeze, complete gate evidence or merge authority.
`

var checkpointRE = regexp.MustCompile(`^(?:[0-9A-Fa-f]{40}|[0-9A-Fa-f]{64})$`)

func isHelp(a string) bool { return a == "--help" || a == "-h" || a == "help" }

func parse(args []string) (inspect.Options, bool, bool, bool) {
	opts := inspect.Options{Limits: inspect.DefaultLimits}
	if len(args) == 1 && isHelp(args[0]) {
		return opts, false, true, true
	}
	if len(args) == 0 || args[0] != "inspect" {
		return opts, false, false, false
	}
	rest := args[1:]
	if len(rest) == 1 && isHelp(rest[0]) {
		return opts, false, true, true
	}
	seen := map[string]bool{}
	asJSON := false
	github := ""
	for i := 0; i < len(rest); i++ {
		name, val, hasVal := strings.Cut(rest[i], "=")
		if seen[name] {
			return opts, false, false, false
		}
		seen[name] = true
		switch name {
		case "--json":
			if hasVal {
				return opts, false, false, false
			}
			asJSON = true
			continue
		case "--repo", "--checkpoint", "--coordination-file", "--github":
		default:
			return opts, false, false, false
		}
		if !hasVal {
			if i+1 >= len(rest) {
				return opts, false, false, false
			}
			i++
			val = rest[i]
		}
		if val == "" {
			return opts, false, false, false
		}
		switch name {
		case "--repo":
			opts.Repo = val
		case "--checkpoint":
			if !checkpointRE.MatchString(val) {
				return opts, false, false, false
			}
			opts.Checkpoint = strings.ToLower(val)
		case "--coordination-file":
			opts.CoordinationFile = val
		case "--github":
			github = val
		}
	}
	if opts.Repo == "" || (opts.CoordinationFile != "" && github != "") {
		return opts, false, false, false
	}
	if github != "" {
		ownerRepo, num, ok := strings.Cut(github, "#")
		owner, repo, ok2 := strings.Cut(ownerRepo, "/")
		n, ok3 := inspect.ParseIssueNumber(num)
		if !ok || !ok2 || !ok3 || !inspect.ValidOwnerRepo(owner, repo) {
			return opts, false, false, false
		}
		opts.GitHubOwner, opts.GitHubRepo, opts.GitHubNumber = owner, repo, n
	}
	return opts, asJSON, false, true
}

// Run executes the CLI and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return run(args, stdout, stderr, nil)
}

// RunWithLimits is Run with test-adjustable limits.
func RunWithLimits(args []string, stdout, stderr io.Writer, limits inspect.Limits) int {
	return run(args, stdout, stderr, &limits)
}

func run(args []string, stdout, stderr io.Writer, limits *inspect.Limits) int {
	if isExecute(args) {
		return runExecute(args, stdout, stderr)
	}
	if isReview(args) {
		return runReview(args, stdout, stderr)
	}
	if isVerify(args) {
		return runVerify(args, stdout, stderr)
	}
	if len(args) > 0 && (args[0] == "run" || args[0] == "status") {
		return runRecord(args, stdout, stderr, limits)
	}
	if len(args) > 0 && args[0] == "context" {
		return runContext(args, stdout, stderr, limits)
	}
	opts, asJSON, help, ok := parse(args)
	if help {
		io.WriteString(stdout, Usage)
		return 0
	}
	if !ok {
		io.WriteString(stderr, "baw: invalid_usage\n")
		return 2
	}
	if limits != nil {
		opts.Limits = *limits
	}
	p, err := inspect.Inspect(opts)
	var out []byte
	if err == nil {
		if asJSON {
			out, err = inspect.RenderJSON(p)
		} else {
			out = inspect.RenderText(p)
		}
		if err == nil && len(out) > opts.Limits.FinalStdout {
			err = &inspect.Error{Code: inspect.CodeOutputLimit}
		}
	}
	if err != nil {
		code := inspect.CodeGitFailed
		var e *inspect.Error
		if errors.As(err, &e) {
			code = e.Code
		} else {
			code = inspect.CodeOutputLimit
		}
		io.WriteString(stderr, "baw: "+string(code)+"\n")
		if code == inspect.CodeUsage {
			return 2
		}
		return 1
	}
	stdout.Write(out)
	return 0
}

// --- run create / status ---

// MaxRecordOutput caps a run create/status packet.
const MaxRecordOutput = 16384

var hashRE = regexp.MustCompile(`^[0-9A-Fa-f]{64}$`)

type recordRequest struct {
	create   bool
	diagnose bool
	asJSON   bool
	vals     map[string]string
}

// parseRecord validates run create/status syntax without touching the
// filesystem. help is set only for a lone new --help alias.
func parseRecord(args []string) (req recordRequest, help, ok bool) {
	var rest, allowed, required []string
	switch {
	case args[0] == "status":
		rest = args[1:]
		allowed = []string{"--state-dir", "--run-id"}
		required = allowed
	case len(args) == 2 && args[1] == "--help":
		return req, true, true
	case len(args) >= 2 && args[1] == "create":
		req.create = true
		rest = args[2:]
		allowed = []string{"--state-dir", "--run-id", "--repo", "--ticket", "--scope-sha256", "--policy-commit"}
		required = allowed
	case len(args) >= 2 && args[1] == "diagnose":
		req.diagnose = true
		rest = args[2:]
		allowed = []string{"--state-dir", "--run-id"}
		required = allowed
	default:
		return req, false, false
	}
	if len(rest) == 1 && rest[0] == "--help" {
		return req, true, true
	}
	req.vals = map[string]string{}
	seen := map[string]bool{}
	for i := 0; i < len(rest); i++ {
		name, val, hasVal := strings.Cut(rest[i], "=")
		if seen[name] {
			return req, false, false
		}
		seen[name] = true
		if name == "--json" {
			if hasVal {
				return req, false, false
			}
			req.asJSON = true
			continue
		}
		known := false
		for _, a := range allowed {
			known = known || a == name
		}
		if !known {
			return req, false, false
		}
		if !hasVal {
			if i+1 >= len(rest) {
				return req, false, false
			}
			i++
			val = rest[i]
		}
		if val == "" {
			return req, false, false
		}
		req.vals[name] = val
	}
	for _, r := range required {
		if req.vals[r] == "" {
			return req, false, false
		}
	}
	v := req.vals
	if !state.ValidRunID(v["--run-id"]) {
		return req, false, false
	}
	if req.create {
		if !state.ValidTicketURL(v["--ticket"]) || !hashRE.MatchString(v["--scope-sha256"]) ||
			!checkpointRE.MatchString(v["--policy-commit"]) {
			return req, false, false
		}
		v["--scope-sha256"] = strings.ToLower(v["--scope-sha256"])
		v["--policy-commit"] = strings.ToLower(v["--policy-commit"])
	}
	return req, false, true
}

// write delivers b and reports whether every byte was accepted.
func write(w io.Writer, b []byte) error {
	n, err := w.Write(b)
	if err == nil && n != len(b) {
		err = io.ErrShortWrite
	}
	return err
}

func failRecord(stderr io.Writer, code string) int {
	write(stderr, []byte("baw: "+code+"\n")) // best effort
	if code == string(inspect.CodeUsage) {
		return 2
	}
	return 1
}

func codeOf(err error) string {
	var se *state.Error
	if errors.As(err, &se) {
		return string(se.Code)
	}
	var ie *inspect.Error
	if errors.As(err, &ie) {
		return string(ie.Code)
	}
	return string(inspect.CodeGitFailed)
}

func runRecord(args []string, stdout, stderr io.Writer, limits *inspect.Limits) int {
	req, help, ok := parseRecord(args)
	if help && req.diagnose {
		if code := deliverBounded(stdout, []byte(Usage), MaxDiagnoseOutput); code != "" {
			return failRecord(stderr, code)
		}
		return 0
	}
	if help {
		if write(stdout, []byte(Usage)) != nil {
			return failRecord(stderr, string(state.CodeOutputUnavailable))
		}
		return 0
	}
	if !ok {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	id := req.vals["--run-id"]
	root, err := state.OpenRoot(req.vals["--state-dir"])
	if err != nil {
		return failRecord(stderr, codeOf(err))
	}
	if req.diagnose {
		return runDiagnose(root, id, req.asJSON, stdout, stderr)
	}
	if !req.create {
		rec, err := root.Read(id)
		if err != nil {
			return failRecord(stderr, codeOf(err))
		}
		out, err := renderRecord("status", rec, req.asJSON)
		if err != nil {
			return failRecord(stderr, codeOf(err))
		}
		if write(stdout, out) != nil {
			return failRecord(stderr, string(state.CodeOutputUnavailable))
		}
		return 0
	}

	opts := inspect.Options{Repo: req.vals["--repo"], Limits: inspect.DefaultLimits}
	if limits != nil {
		opts.Limits = *limits
	}
	p, err := inspect.Inspect(opts)
	if err != nil {
		return failRecord(stderr, codeOf(err))
	}
	repo := p.Repository
	if repo.HeadState != "present" || repo.Head == nil {
		return failRecord(stderr, string(state.CodeRepositoryUnborn))
	}
	if len(req.vals["--policy-commit"]) != len(*repo.Head) {
		return failRecord(stderr, string(inspect.CodeUsage))
	}
	if err := root.CheckAbsent(id); err != nil {
		return failRecord(stderr, codeOf(err))
	}
	rec := state.Record{
		SchemaVersion:          1,
		RunID:                  id,
		RecordState:            "recorded",
		TicketURL:              req.vals["--ticket"],
		ScopeSHA256:            req.vals["--scope-sha256"],
		PolicyCommit:           req.vals["--policy-commit"],
		RepositoryObjectFormat: repo.ObjectFormat,
		RepositoryHead:         *repo.Head,
		CreatedAt:              time.Now().UTC().Format(state.TimeLayout),
	}
	data, err := state.Marshal(rec)
	if err == nil {
		// Never store what status would refuse to read back.
		_, err = state.Parse(data, id)
	}
	if err != nil {
		return failRecord(stderr, string(state.CodeRecordUnavailable))
	}
	out, err := renderRecord("create", rec, req.asJSON)
	if err != nil {
		return failRecord(stderr, codeOf(err))
	}
	if err := root.Create(id, data, func() error { return write(stdout, out) }); err != nil {
		return failRecord(stderr, codeOf(err))
	}
	return 0
}

type recordPacket struct {
	SchemaVersion        int          `json:"schema_version"`
	Operation            string       `json:"operation"`
	Record               state.Record `json:"record"`
	Authority            string       `json:"authority"`
	RemoteFreshness      string       `json:"remote_freshness"`
	RuntimeState         string       `json:"runtime_state"`
	ProcessOwnership     string       `json:"process_ownership"`
	ReservationOwnership string       `json:"reservation_ownership"`
}

// renderRecord renders the complete create/status output, capped before
// any publication or delivery.
func renderRecord(op string, r state.Record, asJSON bool) ([]byte, error) {
	var out []byte
	if asJSON {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(recordPacket{1, op, r, "not_evaluated", "unknown", "unknown", "unknown", "unknown"}); err != nil {
			return nil, &inspect.Error{Code: inspect.CodeOutputLimit}
		}
		out = buf.Bytes()
	} else {
		var b strings.Builder
		b.WriteString("BAW run record\n")
		fmt.Fprintf(&b, "Operation: %s\n", op)
		fmt.Fprintf(&b, "Run: %s\n", r.RunID)
		fmt.Fprintf(&b, "Record state: %s\n", r.RecordState)
		fmt.Fprintf(&b, "Ticket: %s\n", r.TicketURL)
		fmt.Fprintf(&b, "Scope SHA-256: %s\n", r.ScopeSHA256)
		fmt.Fprintf(&b, "Policy reference: %s\n", r.PolicyCommit)
		fmt.Fprintf(&b, "Recorded HEAD: %s\n", r.RepositoryHead)
		fmt.Fprintf(&b, "Object format: %s\n", r.RepositoryObjectFormat)
		fmt.Fprintf(&b, "Created at: %s\n", r.CreatedAt)
		b.WriteString("Authority: not_evaluated\n")
		b.WriteString("Remote freshness: unknown\n")
		b.WriteString("Runtime state: unknown\n")
		b.WriteString("Process ownership: unknown\n")
		b.WriteString("Reservation ownership: unknown\n")
		out = []byte(b.String())
	}
	if len(out) > MaxRecordOutput {
		return nil, &inspect.Error{Code: inspect.CodeOutputLimit}
	}
	return out, nil
}
