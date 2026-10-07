# Verification contract

**Current state:** the read-only `baw inspect` slice, the local
[run-record](../operator/run-records.md) `baw run create`/`baw status`/`baw run diagnose`
foundation, the read-only [`baw context`](../operator/context.md) role reference list and
the [`baw run execute`](../operator/execution.md) local worker/verifier primitive and the
[`baw run review`](../operator/review.md) reviewer-program primitive and the
[`baw run verify`](../operator/verification.md) verifier-program primitive are implemented. Packaging checks are not execution tests. The earlier Python prototype's
test count is not evidence that a new Go implementation behaves correctly. Everything
below those slices remains a future requirement.

## Implemented slice gates: `baw inspect`

Run with the [developer setup](environment.md) toolchain and commands:

- `gofmt -l .` empty; `go test -p 2 -count=1 -timeout=2m ./...`; `go vet ./...`;
  `go build -trimpath -o NEW_ARTIFACT_DIR/baw ./cmd/baw`; `go version -m`; SHA-256.
- Binary journeys (`TestBinaryJourneys`) against the built artifact. This journey uses
  SHA-1 fixtures only. A supplementary SHA-256 run of it through a private Go overlay is
  described in [developer setup](environment.md#first-runnable-acceptance). That supplement
  is required by #26; its execution results are recorded in the ticket's run evidence.
- Independent fresh reproduction and review, an authorized live read-only smoke against
  the coordination issue, and an operator exercise of the same binary. Package tests and
  fake `gh` do not prove native authentication or live GitHub behavior.

Offline tests in `internal/cli` use real Git fixtures with independently established
facts: clean/recovery/conflict/unborn/detached repositories, distinct staged and unstaged
versions of one file, linked worktree, gitlink, ancestor/equal/diverged/missing
checkpoints for SHA-1 and SHA-256, source symlink rejection, subdirectory and symlinked
repository input, marker-writing filter/fsmonitor/pager/hook/external-diff/textconv/gpg
helpers, partial-clone config, poisoned inherited `GIT_*`/`GH_*` variables, a conflicting
user-level ignore file, dummy secrets in branch/file/content/message/remote/config/ignored
files/coordination prose, malformed and boundary-size snapshots, fake `gh` argv/environment/
failure/timeout/output-cap, and fake control-bearing Git output.

Every Git call in those tests, from fixture setup and from the inspector, goes through a
wrapper that prints the stderr warning one host's sandboxed Apple Git emitted
(`DARWIN_USER_TEMP_DIR`). Fixture helpers return stdout only, validate object IDs and keep
stderr as separate diagnostics; `internal/testfixture` checks that separation directly.

Staged gitlinks (`gitlink_test.go`): separate add, update, delete and file-to-gitlink
cases, all combined with a staged file, an unborn repository and a gitlink conflict, each
with counts fixed by the fixture operations. The same cases repeat with
`diff.ignoreSubmodules=all` and `.gitmodules`/config `ignore=all`, with a control showing
ordinary `git diff --cached` hides the change. An initialized submodule whose own
repository sets marker-writing clean/smudge/fsmonitor/textconv helpers and has dirty
content is inspected without any marker appearing; a control shows recursive
`git status` does run those helpers.

Process supervision (`internal/proc`, plus fake `gh`/`git` cases): stdout and stderr
exact-limit acceptance and one-byte-over `command_output_limit`, including the real
64 KiB stderr limit. Owned descendants that ignore SIGTERM, with detached or inherited
stdio, are gone after a normal leader exit, a stdout or stderr overflow (leader still
running) and a timeout. A descendant that leaves the group with `setsid` while holding
stdout makes the command fail within the join bound and is not signalled. These shapes
were also run against the first candidate's process code, where they failed.

Inspect timeouts (`TestLimits`): the Git-timeout and total-budget cases use restrictive
limits of 1 s and 1.5 s with the other at 60 s, a fresh nonce acknowledged by a lock-holding
descendant whose group the kernel reports as that of the fake Git leader started for this
invocation, elapsed and readiness bounds from the actual clock origins, and lock-based proof
that the descendant (and the leader) is gone before any rescue. Missing, wrong and stale
acknowledgements, an escaped live descendant, a live descendant in another group and disabled
timeouts are rejected by the same oracle; bounded finalization is tested for missing readiness,
a failed controller and an unresponsive controller, and a child-process regression checks that
an abrupt test failure during setup reconciles a live descendant before its fixture directory
is removed. The selected full suite is exactly
`go test -p 2 -count=1 -timeout=2m ./...`, chosen in #26's reviewed contract revision. It is
not an automatic fallback. Under default parallelism `internal/cli` was already close to its
110 s ceiling on the unchanged base (107.540 s). The repaired test adds about 2.3 s, and
runs of the same candidate measured 108.292–112.128 s with `go test` exiting 0. That points to
package runtime growth near the budget plus observed contention, not a remaining `TestLimits`
failure, and it is not proof of cause. The single required default-parallelism discovery
run already passed for `e245faf` (109.170 s) and is not repeated. Each candidate, its fresh
review and the merged tree run the selected suite once, plus the five focused repetitions.
Earlier failures stay recorded, and the ceilings are unchanged: 10 s test, 65 s for five
focused runs, 110 s package, 120 s package timeout. The history and clock details are in
[developer setup](environment.md#test-timing-and-scheduling), and run results are on
[#26](https://github.com/afewell-hh/bounded-agent-workflow/issues/26). No universal flake
freedom is claimed.

Not covered: descendants that leave the group and release their pipes (they cannot be
observed; see the [inspect guide](../operator/inspect.md#safety-properties-and-limits)),
Git versions other than 2.39.5, and platforms other than darwin/arm64.

The first candidate's independent review reported required fixes and a failing
`go test` reproduction in its environment. The final repaired candidate then passed all
of these gates, fresh independent and designated supplementary review, the live smoke
and the operator exercise, and post-merge checks on `main`; see
[developer setup](environment.md#checks-actually-executed-and-remaining-validation)
for the durable record links. Detailed results belong in the ticket's run evidence.

## Implemented slice gates: run records

The same gofmt/test/vet/build/metadata/SHA-256 gates apply, plus both
`TestBinaryJourneys` and `TestRunRecordBinaryJourneys` against the built artifact
([developer setup](environment.md#first-runnable-acceptance)). SHA-1 **and** SHA-256
create/status journeys are mandatory in `internal/cli` and in `TestRunRecordBinaryJourneys`
(the older `TestBinaryJourneys` is SHA-1 only); an
unavailable SHA-256 fixture fails rather than skips.

- `internal/state`: a hand-written nine-field oracle; exact 16384-byte whitespace-padded
  valid and 16385-byte oversized records; invalid UTF-8, trailing/duplicate (including
  escaped) keys, case variants, quoted/fractional/exponent/null/huge versions and their
  precedence, timestamps and width mismatches; root, namespace and record symlink, FIFO,
  socket, directory, owner/mode cases without blocking or touching outside markers; an
  ancestor symlink inside the test's own directory resolved while the same symlink as
  the final root is refused; injected failures at each storage stage with their codes,
  retained staging and pre/post-publication results; stage order (root sync after
  namespace acquisition, namespace sync after publication); child-process interruption
  before and after publication; and bounded concurrent same-ID creators from absent and
  present namespaces (one success, one `record_exists`, winner's data intact), all
  joined, including after a failed child start. Escaped-duplicate fixtures contain actual
  JSON `\u` escapes. A separate socket test reads a real closed socket at the record
  path of a state root that a joined child creates, or of a complete read-only root
  supplied by `BAW_TEST_SOCKET_STATE_DIR`
  ([socket fixture](environment.md#socket-fixture)), and checks the supplied root is
  unchanged; a missing or invalid supplied root fails.
- `internal/cli`: both object formats via a subdirectory with uppercase input, dirty and
  secret-bearing worktrees, nonexistent policy references, opposite widths, unborn and
  inspection failures leaving the root unchanged, repeat status, duplicates, status with
  the repository removed and marker-writing `git`/`gh` as the only `PATH`, usage
  precedence before any filesystem access, new help sink checks beside unchanged old
  help aliases, failing/short stdout and failing stderr, corrupt records without content
  leaks, and inspector helper suppression.

Not covered: hardware or power-loss durability, platforms/filesystems other than this
host's macOS APFS, and hostile same-user changes to the state directory during a run.
The first committed candidate's independent review observed the full suite fail where
socket bind was denied, and two tests writing under `/tmp`; the next review observed it
fail where hard-linking a supplied socket was denied. The fixtures were reworked.
Gate and review history for this slice is summarized in
[developer setup](environment.md#checks-actually-executed-and-remaining-validation),
with detailed results in the ticket's run evidence.

## Implemented slice gates: run diagnose

The run-record gates apply, plus `TestRunDiagnoseBinaryJourneys` against the same built
artifact (SHA-1 and SHA-256, text and JSON, no skips).

- `internal/state` (`diagnose_test.go`): hand-written expected states and six counts for
  an absent namespace, empty namespace, partial/complete staging only, a real create
  (linked inode established with `Lstat`), a separate identical copy (not linked),
  valid/invalid/oversized/unsupported finals and staging independently, exact
  16384/16385-byte files, and literal and JSON-escaped (a `run_` key spelled with a
  unicode escape for `i`, checked in the fixture bytes) duplicate keys in finals and staging, including their
  precedence over an unsupported version; ignored malformed/foreign/unsafe names never
  touched; symlink, FIFO, directory, socket (via the
  [socket fixture](environment.md#socket-fixture)), wrong mode and actual setuid entries
  (via the [special-bit fixture](environment.md#special-bit-fixture)); exactly 1024/1025
  names and 32/33 staging names. A test-only stage callback injects faults at namespace
  open/stat/read/close and entry Lstat/open (generic, `ENOENT`, `ELOOP`)/descriptor
  stat/read/close. A test-only tracker retains every `*os.File` diagnose acquires; after
  it returns, `Stat` and `Read` on each must fail with `os.ErrClosed`, including the held
  final and descriptors whose close had an injected error, and during every staging
  lookup and link comparison the held valid final descriptor must still be open. No
  partial result is returned, earlier errors win over close errors, and scan/final
  failures stop later lookups. The same callbacks place deterministic changes before an
  entry's first `Lstat`, between `Lstat` and open (removal, or installing a replacement
  regular file, symlink, FIFO, directory or wrong-mode file that was prepared, and checked
  to be a different device/inode, while the original existed; the installed identity and
  mode are checked) and between reads of one inode (same inode asserted before and
  after), and create the namespace right after its absent observation. Special-file,
  special-bit, socket and boundary-change cases run diagnose in a child of the test
  binary bounded to 10 seconds and always waited for; a timeout fails with the child's
  captured output. Saved bytes and metadata (not access time) are compared before and
  after.
- `internal/cli` (`diagnose_test.go`): exact hand-written text and JSON bytes plus an
  independent key/type/enum/count check, both object formats via real creates with
  dummy-secret references and the repository removed under marker-writing `git`/`gh`,
  usage precedence before filesystem access, root/namespace/entry failures, every fatal
  code's exit/stdout/stderr (through a test-only replacement of the storage call),
  failing/short/prefix-then-error stdout for report and help, failing stderr, and the
  shared 4096/4097-byte bounded-output helper with synthetic buffers. Every diagnosis of
  saved data, including the FIFO cases, runs in a child of the test binary bounded to
  10 seconds and always waited for; a timeout fails with the child's captured output.

Not covered: other users' files (ownership tests use the current user only), other
platforms, hostile same-user changes beyond the deterministic boundaries above, and any
claim of an atomic snapshot.

## Implemented slice gates: context

The run-diagnose gates apply, plus `TestContextBinaryJourneys` against the same built
artifact, and a byte comparison of legacy `baw inspect` output (stdout, stderr, exit;
text and JSON; SHA-1 and SHA-256) between the previously accepted binary and the
candidate on one unchanged fixture corpus. The binary journey must actually run: all
three roles in both object formats and both outputs (12 success combinations) with
hand-written expected bytes, the [documentation example](../operator/context.md#how-to-hand-a-role-its-reading-list-and-follow-the-references)
compared with the documented output and its six references followed from the worktree
top level, subdirectory input, usage/role/repository/symlink failures and a fake `gh`
that must never run. An unavailable SHA-256 fixture fails rather than skips.

## Implemented slice gates: execute

The context gates apply, plus `TestExecutionBinaryJourneys` (same `-baw-binary` and
`-journey-dir` flags, `-v`, must show RUN/PASS and no skip) against the same built artifact.
It uses a private copy of its own compiled test binary as the fake worker and verifier: both
object formats and both outputs succeed with hand-written packets, independently counted
starts and the worker's file bytes checked by the verifier; a nonzero worker never admits the
verifier; repeats give `execution_exists`; a refused stdout gives `execution_uncertain` with
the result retained; real SIGINT and SIGTERM during the worker give the published
`worker_unverified` packet and the harness confirms its recorded fake process is gone; the
[documentation example](../operator/execution.md#how-to-run-a-disposable-example) is followed.

- `internal/execution`: strict plan parser (byte cap, raw integers, argument limits) and
  duplicate names after decoding: each fixture spells `schema_version` (top level) or
  `executable` (worker and verification commands) once literally and once with a genuine
  JSON `\u` escape, checked in the fixture bytes and decoded to the known name; the escaped
  spelling alone is accepted and only the pair is refused. Plan file safety with each read
  in a child of the test binary bounded to 10 seconds and always waited for (symlink,
  FIFO, directory, an actual socket and observed setuid and setgid modes via the
  [socket](environment.md#socket-fixture) and
  [special-bit](environment.md#special-bit-fixture) fixture choices). In-process plan
  precedence on regular files with test-only open/read/close seams: with an injected
  close error, malformed JSON, invalid UTF-8, schema version 2 and oversize plans still
  give `invalid_execution_plan`; an injected non-EOF read error on unparseable bytes gives
  `plan_unavailable` with or without a close error; a close error alone gives
  `plan_unavailable`; the read and close boundaries are reached in every case and the one
  opened `*os.File` reports `os.ErrClosed`. Between the safe `Lstat` and the open, the
  plan is renamed aside (the original stays, so its inode cannot be reused) and replaced
  by a file whose device/inode is independently observed to differ: a `0644` replacement
  gives `state_permissions` and a directory `unsafe_state_path` (safety before identity),
  a safe `0600` replacement `state_changed`, each also with a close error. Then
  layout/alias/prefix-sibling and executable checks, every §7 row with real fake programs
  or in-package seams, fault injection at every scratch/intent/result/delivery stage (no
  start before intent, retained evidence after the verifier started), deterministic
  cancellation at each admission and receipt boundary, and concurrent same-ID calls
  starting one worker. `probe_test.go` adds a hand-written stage table covering every
  namespace, root, ID, scratch (create, mode, open, recheck, Sync, close for all six
  directories), intent, result and delivery boundary with its expected code, attempt
  presence and independently counted starts; at each stage every plan, directory and
  staging `*os.File` the package opened is retained by a test-only observer and must
  report `os.ErrClosed` from `Stat` afterwards (a successful run opens exactly 14). A
  replaced or loosened state root between its `Lstat` and open is refused before any
  attempt exists. A controller child killed with SIGKILL while its worker holds leaves
  the intent and no result, and a repeat refuses with no added start; the harness joins
  the controller through its own handle, releases the worker it launched and observes
  it finish, and never signals a stored ID. These are tests of one host's behavior,
  not a power-loss or containment claim. `boundary_test.go` drives the real
  `Execute`/`RunObserved` path for both object formats and both outputs with a fake
  worker that starts one finite compiled child (the test binary again, empty
  environment) and returns 0 only after the outer harness has observed the child alive
  through an exclusive lock it holds (no process ID is stored or signalled), with a
  per-run nonce in the ready and release records and a 30 second child deadline. A child
  in the worker's group is terminated by cleanup and the verifier is admitted
  (`verification_passed`); a `setsid` child holding the output pipe gives
  `worker_unverified` with no verifier start; a `setsid` child that closed its pipes before
  the worker returned is still running when `verification_passed` is published, which is
  the documented observation limit, not containment. The harness releases each escaped
  child and observes it finish on every path, including failed assertions.
- `internal/cli` `TestExecuteWithCancelledContext` sets its own state root to 0700 and
  checks the observed mode and owner first, because temporary subdirectories follow the
  umask and a looser root correctly fails `state_permissions` before cancellation is
  observed.
- `internal/inspect` (`top_test.go`): `InspectTop` returns the same packet or error as
  `Inspect` and the physical top level for both object formats, a subdirectory and a
  symlinked alias.
- `internal/proc` (`observed_test.go`): observed facts for normal exit, signal, timeout,
  caps, cancellation before Start, Start failure, forced close by an escaped pipe holder,
  and `Spec.Dir`. A test-only reader seam fails one stream's reader with a non-EOF error
  after real bytes arrived and before EOF: that stream's EOF is not observed and the
  result is unusable, while the other stream's EOF and the exit, join and group facts are
  observed, and legacy `Run` returns the same bytes and no error as before. Watcher
  setup and runtime failures are injected only after the leader, the test binary itself
  with an explicit environment, is independently visible as started (a nonce ready file
  plus an exclusive lock it holds); setup failure keeps `Started`, reports `ErrStart` for
  both runners and leaves the leader joined only in the background, runtime failure is
  unusable without waiting for the timeout. On every path the harness releases the
  fixture and waits for its lock to be free, so its end is not inferred from any wait
  returning; no process ID is used. The existing `proc_test.go` regressions keep covering
  legacy `Run`.
- `internal/cli` (`execute_test.go`): frozen global and execute help bytes, syntax errors and
  restoration of the caller's signal handling after execute returns.

## Implemented slice gates: review

The execute gates apply, plus `TestReviewBinaryJourneys` (same flags, `-v`, RUN/PASS and no
skip) against the same built artifact, with compiled fake programs only. For SHA-1 and
SHA-256 it follows the [operator example](../operator/review.md#how-to-run-a-disposable-example):
run create, fake worker/verifier execute, then review of a candidate equal to `after_head`
(JSON PASS, exit 0, `result.json` equal to stdout) and of a later committed descendant (text
PASS), writing both packets' HEAD fields to the journey summary as the D1 comparison; text
and JSON REQUIRED_FIXES (exit 1, `review_failed`; stdout and the raw saved `result.json`
compared with hand-written packets whose timestamps come from the saved intent and result
and must lie inside the wall-clock window observed around the command), replay
(`review_exists`, no start), a dirty candidate
(no start), nonzero exit overriding stdout PASS, reviewer prose and stderr carrying a dummy
secret that never appears in output, and a refused stdout giving `review_uncertain` with the
result retained. Expected packets are hand-written; starts are counted by the fakes.

- `internal/review`: exact hand-written intent/result bytes and text for both formats and
  outputs, with plan and receipt hashes computed independently from the fixture files;
  retained staging with link count 2. Candidate rules (later descendant accepted;
  mismatch, wrong width, diverged, missing, unborn winning over width, and unstaged,
  staged, untracked and conflicted entries refused with zero starts and an unchanged state
  tree, atime excluded). Reviewer rows through real processes: nonzero over PASS, escaped
  duplicate names at top level and nested (the nested report is also invalid for its
  unknown key alone, so it is not the evidence for duplicate refusal), one escaped control
  character, raw control,
  invalid UTF-8, version/number forms, unknown keys, prose, 2,048 versus 2,049 stdout bytes,
  stderr over 65,536 bytes, and tracked/untracked/staged/committed changes giving
  `candidate_changed`. Escaped reports (`TestReviewEscapedReports`): the fixture bytes are
  checked to contain backslash-u escapes and to decode to the literal names and values;
  single escaped keys and an escaped verdict value are accepted, and escaped duplicates
  refused, through real reviewers in both formats and outputs, with direct `ParseReport`
  and nested strict-decoder controls. Each false usability fact through the review-local
  runner seam (nil in production) and the exact reviewer spec. Plan parser boundaries (65,536/65,537 bytes,
  arguments, timeouts, duplicates). Plan and receipt safety: symlink, FIFO, directory,
  modes, a socket and setgid plan (the supplied read-only fixtures when their variables are
  set, otherwise created here and observed), replacement between `Lstat` and open with the
  original inode retained (safety before identity), read/parser-before-close precedence
  (including a read fault and a distinct close fault on the same plan, intent or result:
  the read error code is returned, each seam is hit once and the descriptor held at the
  read fault is closed; under C2 both faults map to the same code), and
  every opened `*os.File` reporting `os.ErrClosed`. Receipt own-ID, different-ID, reversed
  interval with mismatching linkage, linkage, eligibility and byte boundaries; validation
  order; a stage fault table for every namespace, root, ID, scratch, intent, result and
  delivery boundary (55 rows, each hit exactly once) with expected code, ID and namespace
  presence, counted starts, unchanged run record and execution receipts, which of
  `intent.json`, `result.json` and their staging files are retained, every descriptor
  that was open at the injected stage (and every other opened `*os.File`) reporting
  `os.ErrClosed` afterwards, and refused replay leaving the state tree unchanged;
  real state-root and namespace changes at the acquisition rechecks before the ID (mode
  0755, symlink, removal, replacement, and a replaced 0755 root showing safety before
  identity) returning `state_permissions`, `unsafe_state_path` or `state_changed`, each
  confirmed afterwards by `Lstat`, with no owned ID, zero starts and closed descriptors;
  cancellation before acquisition, before and after the ID (including at the final
  boundary immediately before the exclusive ID mkdir: `review_cancelled`, no ID, an empty
  retained namespace and its held descriptor closed), after intent, during the real
  reviewer (after its nonce marker) and late; concurrent same-ID calls starting one
  reviewer; and, for both formats, a controller child killed with SIGKILL before and after
  reviewer start, joined through its own handle, leaving intent and staging without a
  result. No PID is signalled or polled: before start, no reviewer lock or marker appears;
  after start, the orphaned finite fake is observed to finish by its end marker and the
  kernel releasing the lock it held for its whole life. FIFO plans and receipts and the
  socket plan are reviewed in a child test binary with a 10-second deadline and
  `WaitDelay`, always joined, and the special files are left in place. Physical
  replacement of the top level by a clean copy at the same path (original retained, post
  inspection otherwise identical) and replacement by a repository of the other object
  format give `candidate_changed`; the existing `inspectTop` seam also alters exactly one
  post-inspection field (path or format) against an unchanged control.
- `internal/cli` (`review_test.go`): frozen review help, help sink failure, every syntax
  error before filesystem access, the global help delta, and restoration of default
  SIGINT/SIGTERM behavior after review returns (child test binaries, bounded and joined,
  with a control whose leaked handler absorbs the signal).
- `TestReviewBinaryJourneys` also sends SIGINT (JSON) and SIGTERM (text) to the built
  binary after the fake reviewer's start marker is visible, for both formats: exit 1,
  `baw: review_failed`, a recorded `reviewer_unverified` packet with reviewer `unverified`,
  the reviewer's lock released, and the candidate still clean at the same HEAD.

- `internal/inspect` (`profile_test.go`, `state_table_test.go`): hand-written source
  lists per role and the unchanged legacy list; parsers given synthetic Git records keep
  counts for every path but path states only for the request's members; the
  tracked-but-missing-without-status state (`unknown`) driven through the source
  classification step, since real Git does not produce it. With real Git, for each role
  and both formats, the selected role file committed, modified, untracked, never tracked
  and absent, deleted, conflicted, HEAD regular with an index gitlink, HEAD gitlink with
  an index regular file and a further edit, under a regular-file `workflow/roles`, and
  in an unborn repository. Each fixture holds one change, so its exact counts are known;
  every role is requested on every fixture, and legacy inspect equals the lead request.
  Inherited `GIT_*` variables and a user ignore/config are poisoned.
- `internal/cli` (`context_test.go`): exact text and generic-JSON oracles (key sets,
  integer literals, nulls, order) for each role and format; identical bytes from
  subdirectory and ancestor-symlink inputs; the exact help and the unchanged old usage
  text and aliases; usage and role validation before any Git run (a marker-writing `git`
  must not run); unborn, missing-document and detached/dirty observations exiting 0;
  selected, common and ancestor symlinks, directories and FIFOs failing while the same
  unselected role files are not probed (legacy inspect still probes its lead file), each
  in a child of the test binary bounded to 10 seconds and always waited for, whose
  open-descriptor list is identical before and after a repeated request; filter and
  partial-clone guards and helper markers for every role; dummy secrets in role files,
  names, branch, message, remote and config never printed; repository bytes and metadata
  (not access time) unchanged; the 65,536/65,537-byte output helper; failing, short and
  prefix-written stdout for packet and help; failing stderr; independence from inspect's
  32 KiB cap; and alternating and 48 concurrent legacy/lead/worker/reviewer requests with
  exact per-call source lists. Concurrency runs in the `CGO_ENABLED=0` profile; no race
  detector result is claimed.

Not covered: hostile same-user changes during a run, a hard filesystem time limit, and
platforms other than darwin/arm64.

## Implemented slice gates: verify

The review gates apply, plus `TestVerificationBinaryJourneys` (same flags, `-v`, RUN/PASS and
no skip; SHA-1 and SHA-256 are both required) against the same built artifact, with a compiled
fake verifier only. For each format it follows the
[operator example](../operator/verification.md#how-to-run-a-disposable-example): run create,
then text and JSON passing (exit 0) and failing (exit 1, `verification_failed`) verifiers and a
verifier that edits a tracked file (`candidate_changed`). Stdout and the separately read raw
saved `intent.json` and `result.json` are compared with hand-written packets whose
timestamps must be canonical, equal between intent and result, ordered, and inside the
wall-clock window observed around the command; fixed impossible, malformed, stale, late,
reversed and mismatched controls must be rejected by that oracle first. It also checks a
replay (`verification_exists`), a dirty precondition (no attempt), a later descendant under a
new ID, and that a dummy secret printed by the verifier never appears in output or receipts.

Package tests in `internal/verification` and `internal/cli` (`verify_test.go`) cover plan
parsing boundaries, safe plan reads and combined read/parse/close precedence, admission
order, cross-format records (exit 2, unchanged state root, no start), layout, existing ID,
each injected storage stage, cancellation boundaries, same-ID races, every usability fact,
real program rows, packet validators, signal handler restoration and real SIGINT, SIGTERM
and SIGKILL during an attempt. Report which of these actually ran on the identified candidate.
For an unsafe inode opened after the safe plan `Lstat`, `0644`, `0400` and directory
replacements rename the original aside; the setuid row instead keeps the original in place
and a test-only open seam (nil in production) opens a different genuine setuid file with the
reader's flags: the supplied read-only special-bit fixture when its variable is set,
otherwise a file created and observed here. Each expects the safety code before identity,
also with a close fault, and the opened descriptor closed.

Direct pipeline tests in `internal/verification` (not the CLI handler) run `Verify` in a
bounded (10s), joined child of the test binary. Real SIGINT and SIGTERM are sent only after
the child publishes a per-run nonce at the named boundary. The child acknowledges each signal
it actually receives through a test-only `signal.Notify` channel, and a second interrupt is
sent only after the first is acknowledged while the boundary is still held. For SIGKILL after
the verifier actually starts, a finite fake verifier publishes a nonce while holding an
exclusive fixture lock. The parent kills only its own controller child and checks the retained
intent, no result, no output and one start. It then releases the lock and observes the
verifier exit within a bound. No stored PID is signalled, and nothing is replayed. Special
plan files are opened in joined children. With all three supplied fixture variables unset,
a child binds a socket at a short relative name in a private test directory, and setuid and
setgid bits are set on files the test creates, then observed. With a variable set, only that
existing read-only fixture is read. Its type, owner and mode are validated, and its complete
tree metadata and bytes must be unchanged. An absent or invalid supplied fixture fails.

## Layers

Use deterministic unit/state-transition tests, real temporary Git repositories and
worktrees, simulated provider failures, and explicit native integration smoke tests.
Keep offline tests separate from authenticated tests that consume quota or contact
GitHub. Never claim a simulated executable proves native authentication compatibility.

Required application regression gates remain required. Optimize feedback with targeted
checks during editing, but do not trade required final verification for speed. Inspect
assertion strength and test-oracle independence, not only passing-case counts.

## Required controller behaviors to test before trusting automation

- Missing/stale approvals, changed scope or policy, wrong candidate, missing evidence,
  interrupted writes, and exhausted budgets fail closed.
- Worker/reviewer replacement preserves the exact approved scope and attempt lineage.
  A lead disappearing cannot create a new ticket or a duplicate writer.
- A new lead can reconstruct state with the old conversation unavailable. It finds
  active work and unresolved decisions without re-explanation from the operator.
- Abrupt loss with dirty files preserves partial work as unverified. Recovery does not
  clean/reset it or assume a possibly completed external action failed.
- Retired lead/helper generations cannot advance state. Late messages and duplicate
  completion events cannot produce duplicate execution.
- Parent replacement identifies/join/cancels children or blocks on unknown ownership.
  Native helper bounds/permissions are actually enforced or the feature is disabled.
- A changed CLI/configuration/policy version is detected before a new dispatch. Unknown
  authentication or quota errors do not cause an API-billing fallback.
- New candidate commits invalidate affected review/verification; acceptance is tied to
  the candidate, not an agent's claim or an earlier test run.
- Live GitHub mutations require the authorized path and handle uncertain completion
  without blind replay. Logs do not expose credentials or masquerade as uploaded links.
- Documentation and executable behavior agree for each delivered interface. Proposed
  commands are not advertised as implemented.

## Adoption, Git recovery, and runtime scenarios

Test a dirty existing repository with existing instructions, CI, and useful setup: the
adoption assessment is non-destructive and its approved delta does not overwrite policy,
change history, remove tests, or create duplicate guides. Cover a new/no-commit repository
without misreporting its missing baseline as a successful application run.

For Git recovery, cover a committed-but-undocumented change, different staged/unstaged
versions of one file, an untracked source file, relevant ignored metadata without secret
contents, another linked worktree/submodule, stale remote tracking, and non-ancestor
checkpoint/history movement. Verify the same operations do not mutate work. A reflog
is not the expected source of arbitrary unsaved content; unresolved gaps remain unknown.

For environment profiles, use deterministic fixtures before live resources. Prove source
and runtime identity agree; stale images/background workers are detected; required
integration/lab gates cannot be replaced by mock results; a retained preview survives
an agent refresh; and acceptance cannot silently follow a moving live-reload checkout.
Lost forwarding must not trigger unnecessary app rebuild or another writer.

Before advertising safe parallel use, demonstrate separate ports, storage, queues, and
external effects for independent stacks. Shared-lab tests must include two projects on
different execution hosts, competing reservations, disconnect while a remote mutation
continues, stale-generation messages, failed reset/quarantine, and operator inspection
holds. Never auto-reassign an expired reservation without stopping/fencing or verifying
the former writer. Unsupported technical exclusion means supervised-only operation.
These are required future tests, not tests already executed by preparing this seed.

## Secret delivery and one-account GitHub scenarios

These are requirements for future implementation/adoption tests, not results established
by this seed. Use dummy credentials and disposable resources before live access.

- Metadata/context/normal logs and artifact packaging exclude values, including sensitive
  untracked files and accidental values in otherwise tracked diffs. Known-pattern matching
  alone is not proof that arbitrary secrets are absent. Missing redaction means withhold,
  not publish then redact afterward.
- The approved consumer receives exactly its profile; other services/helpers do not inherit
  it. Missing, expired, wrong-target or unauthorized profiles block without broad fallback.
  Verify actual dotenv syntax/precedence; do not execute a dotenv file as shell code.
- Two workspaces keep distinct ports/data/queues and per-instance files, do not overwrite
  the canonical source, and cannot change the other's settings. Cleanup removes only owned
  copies; a held preview retains required access. Rotation invalidates affected evidence.
- A CMS timeout after a write is reconciled against remote state; it does not blindly repeat
  publishing/deletion. External action permission is narrower than token capability.
- A single GitHub identity can complete the intended PR/check path with independent model
  findings and preauthorized conditional merge/closeout, without a second approving-review
  account or per-merge human approval. It stops before the next ticket unless a valid
  recorded [standing delegation](../operator/github-single-account.md#standing-program-delegation-opt-in)
  applies; its informative closeout report is never recorded as human approval.
  Check last-pusher, CODEOWNERS and deployment self-review conflicts explicitly.
- No model comment, changed Git author name, new session, skipped mandatory suite, or forged
  local approval becomes human authorization. Required-check source and inherited-rule
  behavior need live verification; offline fixtures cannot prove GitHub enforcement.
- Adoption never edits secrets or GitHub protections during its read-only pass. A rule conflict
  yields a proposed approved amendment or a blocker, not blanket disabling or admin bypass.

For a same-user/full-credential deployment, tests can establish ordinary control flow, not
resistance to an adversarial worker with equivalent access. State that limitation in results.
See [access](secrets-and-access.md) and [GitHub](../operator/github-single-account.md).

## Manual continuity rehearsal

Before implementing automation, complete a small approved planning/documentation task,
record its decisions and state in GitHub, retire the lead without giving the replacement
its chat history, and ask the replacement to reconstruct the next permitted action.
Repeat once with the previous lead unavailable and once with unverified partial work.
Check concrete facts and human re-explanation required, not only a self-rated score.

## Self-hosting acceptance

First run a candidate controller against disposable fixture repositories with fake
agents. Next supervise a small live ticket and exercise stop/recovery failures. Only
then allow an independently accepted release to govern selected low-risk work in this
project. It remains external to the candidate checkout; its executable and policy do
not change during a run. Controller/safety-policy changes require separate scrutiny.

## Complete-ticket autonomy and GUI conformance tests

These are future required behaviors, not tests already run by generating the seed.

- Given approved routine scope, valid independent review and all required evidence, the
  coordinator can push/open a PR, conditionally merge, confirm post-merge gates, update
  records and close it with no intermediate human merge/demonstration approval. It cannot
  dispatch the next ticket without its identified scope approval or, where adopted, a valid
  recorded standing delegation; missing, revoked or out-of-scope delegation, an exhausted
  cap without a finite recorded extension, or a worker self-extension fails closed.
- A changed head, base/integration race, untrusted/skipped mandatory check, policy change,
  out-of-scope edit or missing evidence does not reach merge. Benign in-scope revalidation
  uses the recorded budget without demanding a ritual human approval. Exceptions block.
- The audited non-production integration path does not publish a release, move an existing
  release tag/artifact, trigger production/CMS mutation or bypass inherited rules. Simulated
  unsafe/unknown downstream hooks are detected; live audits need actual environment evidence.
- A queue request is not a completed merge. Disconnect-after-merge recovery queries actual
  state instead of replaying the write. Squash/rebase identity and resulting tree/build
  mapping are recorded. Failed post-merge health keeps the ticket blocked/not completed.
- New design intent requires its named decision. Implementation matching approved intent
  can complete without a human merge click. The worker cannot waive a design hold or make
  an unintended change acceptable by rewriting test expectations or screenshot baselines.
- GUI verification catches a stale preview, covered/hidden/disabled control, clipped error,
  success toast after a failed save, missing persistence and unsupported viewport claim.
  A screenshot file that no reviewer can view does not pass visual inspection. A DOM-only
  report or mocked story does not pass pixel or integrated-journey gates.
- Baseline changes are independently reviewed against intent; bulk regeneration, excessive
  tolerances/masking and post-capture code changes cannot silently pass. Important successful
  acceptance journeys retain inspectable evidence, not only failing/retried tests.

Follow [closeout](../operator/github-single-account.md) and [GUI verification](gui-verification.md).
The fixture tests will establish control behavior, not universal visual correctness or
resistance to an adversarial worker with equivalent privileges.
