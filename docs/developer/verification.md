# Verification contract

**Current state:** the read-only `baw inspect` slice and the local
[run-record](../operator/run-records.md) `baw run create`/`baw status`/`baw run diagnose`
foundation are implemented. Packaging checks are not execution tests. The earlier Python prototype's
test count is not evidence that a new Go implementation behaves correctly. Everything
below those slices remains a future requirement.

## Implemented slice gates: `baw inspect`

Run with the [developer setup](environment.md) toolchain and commands:

- `gofmt -l .` empty; `go test -count=1 -timeout=2m ./...`; `go vet ./...`;
  `go build -trimpath -o NEW_ARTIFACT_DIR/baw ./cmd/baw`; `go version -m`; SHA-256.
- Binary journeys (`TestBinaryJourneys`) against the built artifact.
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
create/status journeys are mandatory in `internal/cli` and in the binary journey; an
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
  16384/16385-byte files, escaped duplicate keys and version precedence; ignored
  malformed/foreign/unsafe names never touched; symlink, FIFO, directory, socket (via the
  [socket fixture](environment.md#socket-fixture)), wrong mode and setuid entries;
  exactly 1024/1025 names and 32/33 staging names. A test-only stage callback injects
  faults at namespace open/stat/read/close and entry Lstat/open (generic, `ENOENT`,
  `ELOOP`)/descriptor stat/read/close, and a descriptor tracker asserts every opened
  descriptor, including the held final, is closed, no partial result is returned, earlier
  errors win over close errors, and scan/final failures stop later lookups. The same
  callbacks place deterministic changes before an entry's first `Lstat`, between `Lstat`
  and open (safe replacement, removal, symlink, FIFO, directory, wrong mode) and between
  reads of one inode, and create the namespace right after its absent observation.
  Saved bytes and metadata (not access time) are compared before and after.
- `internal/cli` (`diagnose_test.go`): exact hand-written text and JSON bytes plus an
  independent key/type/enum/count check, both object formats via real creates with
  dummy-secret references and the repository removed under marker-writing `git`/`gh`,
  usage precedence before filesystem access, root/namespace/entry failures, every fatal
  code's exit/stdout/stderr (through a test-only replacement of the storage call),
  failing/short/prefix-then-error stdout for report and help, failing stderr, and the
  shared 4096/4097-byte bounded-output helper with synthetic buffers.

Not covered: other users' files (ownership tests use the current user only), other
platforms, hostile same-user changes beyond the deterministic boundaries above, and any
claim of an atomic snapshot.

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
