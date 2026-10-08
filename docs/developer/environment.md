# BAW developer setup

Use a native Go toolchain and a local terminal for BAW's first executable slice.
This guide adapts [execution environments](execution-environments.md) for contributors
to this tooling repository. The module `github.com/afewell-hh/bounded-agent-workflow`
(minimum Go 1.27.0, standard library only) provides `cmd/baw` with the read-only
[`baw inspect`](../operator/inspect.md) command, the local
[run-record commands](../operator/run-records.md), the read-only
[`baw context`](../operator/context.md) command, the
[`baw run execute`](../operator/execution.md) local worker/verifier primitive and the
[`baw run review`](../operator/review.md) reviewer-program primitive and the
[`baw run verify`](../operator/verification.md) verifier-program primitive (all exercised only
with compiled fake programs; no native agent adapter) and their tests. See the last section for
which checks have actually run and what remains unvalidated.

## Selected toolchain

The `native-cli` profile uses an isolated official Go 1.27.1 darwin/arm64 toolchain,
installed after checksum verification into a private run directory and registered as a
persistent project asset (`~/.local/state/baw/toolchain-assets/go1.27.1-darwin-arm64.json`).
Homebrew Go and global Go settings are unchanged. Invoke the toolchain by explicit path or a
per-command `PATH` prefix. The toolchain subtree has **no run-expiry cleanup**: it remains
until the operator explicitly retires or replaces the asset and dependent runs are
reconciled. Run-root cleanup must exclude `toolchain/`. Binary, fixture and evidence
retention is a separate rule (at least 30 days after ticket closure and any hold).

## Inspected facts

Read-only inspection on 30 September 2026 established these development-host facts.
They are observations, not a supported-platform matrix or minimum-version policy.

| Item | Observed result |
|---|---|
| Host | macOS 26.5, build 25F71, arm64 |
| Go executable | `/opt/homebrew/bin/go` |
| Installed Go | `go1.24.1 darwin/arm64`, also confirmed with `GOTOOLCHAIN=local` |
| Git | 2.39.5, Apple Git-154 |
| Source (at inspection time) | Documentation seed; no `go.mod` or Go application source |

Native agent/GitHub CLI versions and exact inspection evidence belong in the relevant
ticket's run evidence, indexed by [project coordination](https://github.com/afewell-hh/bounded-agent-workflow/issues/1),
not a permanent promise about supported adapter versions.

## Native CLI profile

The `native-cli` profile is one isolated source worktree, one writer, local Go
compilation, deterministic tests using disposable Git repositories and fake GitHub
responses, and a terminal demonstration. No application secrets, server, database,
ports, containers, NetBox or nested lab are needed. Native agent login and GitHub
credentials remain in their existing tools; offline fixtures do not need access to them.

The module requires Go 1.27.0 or newer and uses only the standard library, with Git as
the fixture tool. The approved toolchain is the persistent Go 1.27.1 darwin/arm64 asset
described under [Selected toolchain](#selected-toolchain); the shared Homebrew Go 1.24.1
listed below is outside [Go's supported releases](https://go.dev/doc/devel/release#policy)
and is not used. Installing, replacing or retiring a toolchain still requires specific
operator approval. Keep toolchain selection local to each command; do not change global
Go settings. `GOTOOLCHAIN=local` prevents automatic toolchain switching/downloads and
fails if the module requires a newer version. See
[Go toolchain selection](https://go.dev/doc/toolchain). Additional dependencies, CGO or
live GitHub access in tests need that ticket's environment approval. Offline success does
not establish authenticated native integration or live GitHub behavior.

## Build and verification commands

Run from the assigned worktree root with the selected toolchain first on `PATH`. Also
run `gofmt -l .` (expect no output). First record the
candidate commit and any dirty-file identities. Allocate a new owner-only artifact
directory outside the checkout under the ticket's approved evidence location; set
`baw_artifact_dir` to that absolute path. Never reuse a held binary's output path.

The command sequence:

```sh
(
  set -e
  : "${baw_artifact_dir:?Set the new run-owned artifact directory first}"
  export GOTOOLCHAIN=local GOWORK=off GOPROXY=off CGO_ENABLED=0
  go test -p 2 -count=1 -timeout=2m ./...
  go vet ./...
  go build -trimpath -o "$baw_artifact_dir/baw" ./cmd/baw
  go version -m "$baw_artifact_dir/baw"
  shasum -a 256 "$baw_artifact_dir/baw"
)
```

These settings apply only inside the subshell. `GOWORK=off` avoids an unrelated Go
workspace; `GOPROXY=off` disables module downloads. They do not prevent arbitrary test
code from making network calls: the approved offline tests must use dummy/local inputs.
The commands write Go cache entries, temporary test fixtures and the named binary;
they do not install BAW globally. See the [Go command reference](https://pkg.go.dev/cmd/go).

Apply a five-minute wall-clock limit per test, vet or build command, supervised by
the invoking agent/operator. The test flag additionally limits each package's test
binary to two minutes, and `-p 2` runs at most two packages' test binaries at once (see
[test timing and scheduling](#test-timing-and-scheduling)). Retain exit codes and sanitized output. On timeout or failure,
preserve evidence, reconcile any child processes and stop before the next gate; do not
automatically reinstall tools, loosen checks or reset the attempt budget.

### Test timing and scheduling

The selected full-suite command is exactly `go test -p 2 -count=1 -timeout=2m ./...`.
[#26](https://github.com/afewell-hh/bounded-agent-workflow/issues/26) chose it in a reviewed
contract revision after the observations below; it is not an automatic fallback. The package
set, every assertion, the focused command and all fixed ceilings are unchanged. Final full
suites on a candidate, its fresh review and the merged tree each use this command once; an
overrun or failure stops, with no automatic rerun and no further change to concurrency or
ceilings.

Observed history (earlier runs by other sessions, recorded on the tickets; none was run to
write this page):

- Under default full-suite load the earlier `TestLimits` failed when its 300 ms timeout
  completed before the fake Git descendant's PID marker existed; ten isolated runs of the
  same test passed (1.80–1.87 s each), and an unchanged-base default full run later
  reproduced the failure. That suggested startup sensitivity but did not prove scheduling as
  the cause. [#24](https://github.com/afewell-hh/bounded-agent-workflow/issues/24) used `-p 2`
  for that ticket only (its `internal/cli` package took 96.9–101.5 s there).
- #26 replaced the timeout fixture. Its first candidate passed `go test` under the default
  command, but `internal/cli` took 111.2 s, over the 110 s acceptance ceiling, so it was not
  accepted.
- For candidate `e245faf`, the one default-parallelism discovery run that #26 required (no
  `-p`, all three supplied fixture variables unset) passed with `internal/cli` at 109.170 s, and
  a run on the clean commit took 108.292 s. That was a one-time requirement of #26's contract;
  later candidates do not repeat it unless their own ticket requires it. The fresh
  independent review's default run then exited 0 but `internal/cli` took 112.128 s, over
  110 s; its later gates did not run and its required-fixes result stands.
- A verification-only default run of the unchanged base `8b5f82a` passed with `internal/cli`
  at 107.540 s (old `TestLimits` 1.97 s, wall time 110.486 s).

Interpretation: `internal/cli` was already near its 110 s ceiling under default parallelism
before this change. The repaired `TestLimits` takes 4.28–4.31 s in focused runs, about 2.3 s
more than the old test, which by that arithmetic leaves roughly 0.2 s of margin, and the same
bytes measured 108.3–112.1 s across environments. These observations motivate reducing package concurrency while preserving all checks:
`TestLimits` passed in the recorded runs, but the package exceeded its timing budget.
Contention is an inference from different runs, not a proof of cause, and the observations
do not bound arbitrary load.
The earlier `-p 2` package times come from #24's bytes; whether the selected command passes
for a #26 candidate is recorded on #26, not here.

`TestLimits` writes no executable for its timeout cases: each invocation runs in a
re-executed test-binary controller with its own arguments, environment, process group and
fixture directory, and the fake Git is the test binary reached through a symlink named `git`.
This reduces fixture setup and is a test-only choice. The fake Git records a fresh leader
nonce with its PID and starts one descendant in the application's process group, which takes an exclusive lock and only then writes a fresh
per-case nonce with its PID and group. A case is accepted only if: that exact nonce arrived
within the ready window and before return; the kernel, while both were live, reported the
leader as leader of its own group and the descendant in exactly that group; the result is exit
1, empty stdout and `baw: command_timeout`; the elapsed time is within [limit, limit + 1 s grace
+ both 1 s join bounds + 250 ms startup allowance]; and within 500 ms of return the lock was free
and the kernel no longer knew the leader, before any rescue (the descendant's own lifetime is
20 s). Clock origins: `Total` starts before discovery; `GitTimeout` starts after the Git child
is started, so readiness may precede it; neither is reset by the acknowledgement. The
restrictive limits are 1 s (`GitTimeout`) and 1.5 s (`Total`), the other limit 60 s; ready
windows are 1.25 s and 1.5 s from invocation start.

Negative controls go through the same oracle and must be rejected for the expected reason
before a cooperative file-based rescue: missing, wrong and stale nonces, a descendant that
leaves the group after being observed, a live descendant acknowledging from another group,
and disabled timeouts (rejected at the 4.25 s upper bound). Finalization is tested too: a
missing acknowledgement, a controller that dies leaving its descendant, and a controller that
ignores the request. The finalizer is registered before any process starts and all fixtures
live under one private root that only it removes, so a test failure at any point cannot delete
fixture evidence first. It asks the controller to exit through a private token, asks a live
descendant to exit and observes it and the leader gone and the invocation returned (or the
controller exited), and only then kills a still-running controller through its owned handle.
Every wait is bounded; if a bound passes first nothing is killed, the case is reported
unreconciled and the root is retained. No stored PID is signalled. A regression re-runs
`TestLimits` in a child test process that fails abruptly during setup once its first
descendant acknowledged a nonce issued by the parent; the parent, holding the descendant's
lock file open, checks that the lock was free and the leader gone while the child's fixture
directory still existed, that the controller finished its own rescue without being killed,
and that the child then removed the root.

Test budgets: the whole `TestLimits`, including setup, controls and finalization, fails above
10 s; acceptance also requires five focused repetitions
(`go test ./internal/cli -run '^TestLimits$' -count=5 -timeout=90s -v`) within 65 s and the
`internal/cli` package within 110 s of the unchanged 2-minute suite limit in each selected
full-suite run. Each candidate repeats the five focused repetitions (90 s test timeout,
150 s external supervision). These are measured bounds on one darwin/arm64 host, not proof
that the suite is free of flakes under other load or platforms; five focused passes and a
passing selected suite do not establish that either. A startup later than the ready window
or an overrun fails honestly.

### Batched unsafe-source children

`TestContextUnsafeSources` was the largest single test in one profiled `internal/cli` run
(19.19 s of 99.197 s, #28). [#29](https://github.com/afewell-hh/bounded-agent-workflow/issues/29)
keeps all of its 63 requests, 18 fixtures and 126 `Run` calls, and changes only how many of
its child processes are in flight:

- Each request still runs in its own child of the test binary, with its own copied argv and
  environment, its own 64 KiB-capped captures, a 10 s context and a 1 s `WaitDelay`. The
  child runs the request twice and lists its descriptors around the second call.
- The three role requests against one completed fixture run together, at most three at a
  time. The legacy `inspect` request for that fixture stays serial and runs after them.
- The parent creates and changes fixtures and evaluates every original assertion itself. It
  does so only after every started child has been joined. Results are matched by request
  index, not completion order. A child error never stops the other queued requests; the
  first error by index is reported. The parent adds no `t.Parallel`, `chdir`, signal handler
  or environment change.
- The children share the fixture read-only. The inspector's Git calls pass
  `--no-optional-locks` (`internal/inspect`). The test checks this: it snapshots each fixture's `.git/index` bytes and modification time
  before a batch and requires both unchanged after all children have joined. That catches
  an index rewrite by the children. It does not isolate the fixture from other same-user
  programs.
- The parent joins each direct child. The test itself does not observe or contain every
  Git descendant. The 10 s context with its 1 s `WaitDelay` is not an absolute wall-clock
  bound on a join. Fixtures use `t.TempDir` cleanup, so a hard-killed test does not
  retain them.

Five controls (`TestContextBatchConcurrency`, `TestContextBatchRoutingAndMatrix`,
`TestContextBatchFailureJoins`, `TestContextChildRunnerCompatibility`,
`TestContextFDObservation`) check the batching and the descriptor observer. They use short
fake controllers: each is a real child of the test binary that exits at once and is joined
by the operation that started it. They check:

- peak concurrency of exactly three, and that a fourth request does not start while three
  are held;
- routing by index and exact multiplicity, checked against a hand-written list of the 63
  request IDs;
- joins before the batch returns, on start, exit, report, descriptor, context and reader
  failures;
- error precedence and the unchanged runner diagnostics.

Serial, unlimited, wrong-index and omit-and-duplicate executors must each be rejected for
their own reason. The reader-failure control proves only that a read error the runner
receives is propagated, not how the OS pipe fails. The context controls trigger
cancellation directly; they do not measure the real 10 s timer.

Descriptor observation. `openFDs` opens `/dev/fd` once, reads the entry names with
`Readdirnames` and always closes the directory. It makes no per-entry metadata call. It
fails on an open, read or close error (a read error is reported in preference to a close
error) and on a non-numeric or duplicate name. It returns the names sorted, with nothing
filtered or retried; the child fails on any observation error. The earlier `os.ReadDir`
form ignored errors, and on this host it dropped standard handles that `fcntl(F_GETFD)`
showed open when children ran concurrently. Exact before/after equality, a non-empty
before list and the repeated-output check are unchanged. `TestContextFDObservation`
checks the observer in two ways. First, through injected directories with hand-written
expectations. Second, in real joined children: it confirms with `fcntl` that 0, 1 and 2
are open and listed. It also holds an owned descriptor duplicated to a number of at least
64, and requires the runner to reject the listing taken while that descriptor is open as
`facts_invalid`. Once the descriptor is closed, the lists must be equal again.

Known limits:

- The listing is not atomic and may include the observer's own directory descriptor.
- A reused descriptor number cannot be distinguished from the original.
- On darwin the process supervisor's exit watcher (`internal/proc/exitwatch_darwin.go`)
  now makes its one `kqueue` close call before it signals exit or watcher failure
  ([#32](https://github.com/afewell-hh/bounded-agent-workflow/issues/32)). Until then it
  signalled first and closed the queue in a deferred call, so `Run` could return while the
  queue was briefly still open. A registered #29 diagnostic of that earlier code (eight
  repetitions, 1,008 snapshots) observed no differing pair; that was one sample, not proof
  that the race was absent.
- The close error is still ignored, as before. If the OS reports one, the signal follows
  the completed close attempt but does not establish that the queue was released. The
  watcher goroutine itself, other descriptors and descendants are not covered by this
  ordering. When `Run` returns without having received a watcher signal (for example a
  leader not seen to exit by the end of cleanup, which is then reaped in the background),
  the queue stays open until the watcher finishes, as before.

Any unequal list fails the test. It is not filtered or retried.

Proposed acceptance for this change, set by #29's contract, has these ceilings:

- each entire `TestContextUnsafeSources` repetition, including setup and finalization, at
  most 12 s;
- five focused repetitions
  (`go test ./internal/cli -run '^TestContextUnsafeSources$' -count=5 -timeout=90s -v`)
  within 65 s;
- the five-control package
  (`go test ./internal/cli -run '^(TestContextBatchConcurrency|TestContextBatchRoutingAndMatrix|TestContextBatchFailureJoins|TestContextChildRunnerCompatibility|TestContextFDObservation)$' -count=1 -timeout=90s -v`)
  within 12 s;
- `internal/cli` at most 100 s in each selected full suite, in addition to the inherited
  110 s and 120 s limits.

These are targets. A pass on the targets is not a performance guarantee. The first batched
attempt failed its five focused repetitions on the old descriptor observer. That failure
and the measured results of later candidates are recorded in #29's run evidence, not here.

## First runnable acceptance

Once the first executable slice exists, the worker runs the implemented tests and
builds the candidate, then exercises the **built binary** against the ticket's accepted
terminal scenarios. The ticket must supply exact invocations, dummy fixture paths,
expected output and exit codes, including failure cases; this guide does not invent
currently unavailable `baw` subcommands.

An independent fresh session follows this guide and verifies the source, toolchain,
fixtures and evidence. The operator then exercises the same binary by its absolute
path, using the documented scenario. Record its SHA-256 before and after both agent
and operator runs, the source identity, build metadata, commands, outputs and observed
results. Package tests alone do not prove the terminal journey. A separately rebuilt
binary is a new artifact requiring its own verification.

The run owns its artifact and fixtures independently of any agent session. Retain them
through review and the operator demonstration under the ticket's retention rule. A
replacement inspects existing artifacts and process ownership before continuing;
rebuilding, deleting fixtures or terminating an unknown process is not implicit recovery.
Only the designated owner cleans up that run's paths after any inspection hold ends.

Tests create disposable Git repositories with an isolated `HOME` and run the real
`git`; GitHub behavior uses a fake `gh` on `PATH`. They make no network calls and need no
credentials. To exercise a built binary against dummy fixtures in a new directory:

```sh
go test ./cmd/baw -run TestBinaryJourneys -count=1 -args \
  -baw-binary="$baw_artifact_dir/baw" -journey-dir=NEW_EVIDENCE_DIR/journeys
```

It writes an operator fixture (staged=1 unstaged=1 untracked=1) and `summary.txt` with
exit codes and outputs of terminal, JSON, invalid-repository, malformed-snapshot, usage,
help and staged-gitlink runs. It also runs fake-`gh` journeys (no network) for the 64 KiB
stderr boundary and for descendants left by a normal exit or an output overflow, and
records whether each descendant was gone when the binary returned.

This oldest journey creates its operator and staged-gitlink fixtures in SHA-1 only
(`testfixture.Operator` and an explicit `sha1` initialization; inspected source). The six later
binary journeys below each run SHA-1 and SHA-256, and the 76 legacy byte comparisons remain
required. For SHA-256 coverage of this journey without editing it, #26 approved one
additional run against the same identified binary with `go test -overlay`. The overlay uses
private copies of exactly `internal/testfixture/fixture.go` and `cmd/baw/journey_test.go`, with
only those two `sha1` literals changed to `sha256`. The run must confirm that both
repositories report object format `sha256` with 64-hex `HEAD`, with RUN/PASS and no skip. It
supplements the unchanged original run and never replaces checkout files. The overlay is a required check, not a result inferred from the original SHA-1 run;
its execution results are recorded in #26's run evidence.

Run-record journeys use the same flags with their own new directory:

```sh
go test ./cmd/baw -run TestRunRecordBinaryJourneys -count=1 -args \
  -baw-binary="$baw_artifact_dir/baw" -journey-dir=NEW_EVIDENCE_DIR/run-record-journeys
```

It creates a new `0700` state root, dummy SHA-1 and SHA-256 repositories (an unavailable
SHA-256 fixture fails the test), and records in `summary.txt` the help, usage, create,
status, duplicate, opposite-width, corrupt-record and documentation-example runs, plus
status with the repositories removed and only marker-writing fake `git`/`gh` on `PATH`.

Diagnose journeys use the same flags with their own new directory:

```sh
go test ./cmd/baw -run TestRunDiagnoseBinaryJourneys -count=1 -args \
  -baw-binary="$baw_artifact_dir/baw" -journey-dir=NEW_EVIDENCE_DIR/diagnose-journeys
```

It creates SHA-1 and SHA-256 records (an unavailable SHA-256 fixture fails the test),
removes the repositories, and runs every diagnose with only marker-writing fake
`git`/`gh` on `PATH`, each as a child bounded to 10 seconds. Text **and** JSON output are
compared with exact hand-written reports for an absent namespace, a created record with
its linked staging name, and mixed staging categories beside ignored unsafe names; dummy
secret references must not appear, and file bytes, listing, modes, owner, inode, link
count, size and modification time (not access time) must be unchanged. It also runs help,
usage, FIFO/symlink/mode failures and the documentation example, recording all in
`summary.txt`.

Context journeys use the same flags with their own new directory:

```sh
go test ./cmd/baw -run '^TestContextBinaryJourneys$' -count=1 -v -args \
  -baw-binary="$baw_artifact_dir/baw" -journey-dir=NEW_EVIDENCE_DIR/context-journeys
```

It compares `baw context` text and JSON for all three roles in SHA-1 and SHA-256 fixtures
(12 success combinations; an unavailable SHA-256 fixture fails the test) with exact
hand-written output, repeats each from a subdirectory, runs help and usage, repository and
symlink failures with a fake `gh` that must never run, and performs the
[documentation example](../operator/context.md#how-to-hand-a-role-its-reading-list-and-follow-the-references),
checking the documented output and following its references, recording all in
`summary.txt`. Without both flags the journey test is skipped, which is not a result:
check that the `-v` output shows `--- PASS: TestContextBinaryJourneys`.

### Socket fixture

`TestReadSocketRecordSafety` in `internal/state` checks that status rejects an actual
Unix socket at the record path; `TestDiagnoseSocketRecord` checks the same for diagnose,
using the same fixture choice below and the same read-only guarantee. Without extra setup, the test creates its own `0700`
state root and `0700` `records-v1` namespace under `TMPDIR`, then runs a child of its own
test binary with the namespace as working directory. The child binds a socket at the
short relative record name `00112233445566778899aabbccddeeff.json`, closes it without
unlinking and exits; the test waits for it under a timeout. No listener remains and the
test process's working directory is unchanged.

Where socket bind is denied, set `BAW_TEST_SOCKET_STATE_DIR` for the test command only,
to a complete existing state root outside the checkout: a `0700` root and `0700`
`records-v1` namespace owned by the current user, with a closed Unix socket owned by the
current user at `records-v1/00112233445566778899aabbccddeeff.json`. The test only reads
it: it opens the root, checks the namespace and the socket's type and owner, reads that
ID expecting `unsafe_state_path`, and confirms the root's listing and the fixed metadata
(mode, owner, inode, link count, size, modification time) of root, namespace and socket
are unchanged. It never binds, links, changes modes, creates or removes anything there.
It fails, never skips or falls back, if the root, namespace or socket is missing,
of another type or mode, or owned by someone else. Only tests read this variable; `baw`
does not. Creating the fixture needs one bind in a directory you own, for example by a
host process outside the restricted environment; it is test data, not a running service.
The Go toolchain, offline settings and sandbox permissions are the same as above.
`TestDiagnoseSocketRecord` runs its diagnose in a child of the test binary bounded to
10 seconds and always waited for.

`TestReadPlanFileSafety` in `internal/execution` uses the same choice for its socket plan
case: with the variable set it reads the socket at that same record path as a plan and
expects `unsafe_state_path`; otherwise a child of the test binary binds the short
relative name `p` in a new `0700` directory under `TMPDIR`. Either way the test checks the
path is actually a socket and that its mode, size, inode and modification time are
unchanged afterwards, and every plan read runs in a child bounded to 10 seconds and
always waited for.

### Special-bit fixture

`TestDiagnoseSpecialBits` in `internal/state` checks that diagnose rejects an actual
setuid final record and staging file with `state_permissions`. Without extra setup, it
creates a new `0700` directory under `TMPDIR` holding two state roots, `final` and
`pending`, each with a `0700` `records-v1` namespace; it writes one file in each
(`00112233445566778899aabbccddeeff.json`, and
`.pending-00112233445566778899aabbccddeeff-0000000000000000000000000000000a`), sets
mode `04600` and checks the mode actually observed. Some restricted environments report
success for that `chmod` but leave the file at `0600`; the test then fails, never skips.

There, set `BAW_TEST_DIAG_SPECIAL_FIXTURES` for the test command only, to a complete
existing directory outside the checkout with exactly that layout: the directory, both
roots and both namespaces `0700` and owned by the current user, each namespace holding
only its one named file, a current-user, single-link, non-executable regular file with
mode exactly `04600`. The test validates every listing, type, owner and mode itself,
fails if any differs, and only reads the fixture: it never writes, changes modes,
creates or removes anything there, and confirms listings, bytes and metadata (mode,
owner, inode, link count, size, modification time) are unchanged. Only tests read this
variable; `baw` does not. Creating the fixture needs one `chmod` by a host process
outside the restricted environment; it is metadata test data, not a program or service.

`TestReadPlanFileSafety` in `internal/execution` reads `final/records-v1/00112233445566778899aabbccddeeff.json`
from the same fixture as a setuid plan when the variable is set, otherwise a plan file it
creates with mode `04600`; it fails unless the observed mode is exactly a regular `04600`
file and expects `state_permissions`.

Its setgid plan case uses a separate test-only variable, `BAW_TEST_EXEC_SETGID_PLAN`,
because some restricted environments likewise leave a requested `02600` file at `0600`.
Unset, the test writes its own plan file under `TMPDIR`, sets mode `02600` and checks
the mode actually observed. Set (for the test command only), it must name an absolute
path to an existing current-user, single-link, non-executable regular file with mode
exactly `02600` (no setuid or sticky bit); its content is irrelevant because the mode
check precedes reading. The test validates type, owner, link count and mode itself and
fails, never skips or falls back, if the file is missing or differs. It only reads the
file in a bounded child expecting `state_permissions`, never changes modes, links,
writes or removes anything, and confirms its bytes, metadata (mode, owner, device,
inode, link count, size, modification and change time) and its directory's listing
are unchanged. Only tests read this variable; `baw` does not. Creating the file needs
one `chmod` by a host process outside the restricted environment. The default route
(variable unset) remains a required host check; the supplied file does not replace it.

## Checks actually executed and remaining validation

For the first `baw inspect` candidate, the implementation worker's own run of gofmt,
tests, vet, build, `go version -m`, SHA-256 and binary journeys passed on the host
described above. The **independent fresh-session review of that candidate returned
required fixes**, and its `go test` reproduction failed in its sandbox: Apple Git printed
a `DARWIN_USER_TEMP_DIR` warning on stderr, which the fixture helper merged into object
IDs. It also found surviving owned descendants, accepted oversized child stderr and
uncounted staged gitlinks.

A repaired candidate addresses those findings. Its worker reran the same gates
(Git 2.39.5, including SHA-256 object-format repositories) with the stderr-warning
wrapper described in [verification](verification.md). For the final candidate
`27c4e17`, the required gates passed, a fresh independent Codex review and the
designated supplementary review passed, the authorized live read-only smoke against
coordination issue #1 passed, and the operator ran the same binary and recorded its
terminal output. The squash merge to `main` (`884e493`) has a tree identical to the
reviewed candidate, and the full post-merge checks passed. Durable records:
[closeout](https://github.com/afewell-hh/bounded-agent-workflow/issues/4#issuecomment-5927721836),
[independent review](https://github.com/afewell-hh/bounded-agent-workflow/issues/4#issuecomment-5927493441),
[operator exercise](https://github.com/afewell-hh/bounded-agent-workflow/issues/4#issuecomment-5927641558).
These results cover that host only; other platforms and Git versions have not been exercised.

For the run-record commands, a filesystem probe on this host's APFS volume first
observed `0700`/`0600` modes, successful `os.File.Sync` on root, namespace and file, and
an exclusive hard link that left an existing name unchanged (link count 2). The
implementation worker's own run of gofmt, tests, vet, build, `go version -m`, SHA-256,
`TestBinaryJourneys` and `TestRunRecordBinaryJourneys` then passed for the uncommitted
candidate. That was the worker's result only, not independent review or acceptance.
The first independent review of the committed candidate then observed the full
`go test` fail in a managed sandbox that denies Unix-socket bind: the socket-rejection
test created its socket by listening at the record path. It also found two tests creating
directories under `/tmp` instead of `TMPDIR`. A second independent review then observed
the full `go test` fail in its sandbox when the test hard-linked a supplied closed socket
inode into its own directory (`operation not permitted`). The fixtures were reworked
again as described in [socket fixture](#socket-fixture). Review, gate and integration
results for this slice are recorded on [ticket #9](https://github.com/afewell-hh/bounded-agent-workflow/issues/9),
which indexes its run evidence.

For `baw run diagnose`, the first independent review of the committed candidate observed
the full `go test` fail in its environment: `chmod` to `04600` reported success but left
the setuid test files at `0600`, so the setuid cases were never exercised. It also found
special-file probes not run in bounded children, a descriptor-closure check that counted
callbacks rather than closed files, an escaped-duplicate fixture without an escape and
identity fixtures without independent inode facts. The tests were reworked as described
in [verification](verification.md#implemented-slice-gates-run-diagnose) and
[special-bit fixture](#special-bit-fixture). Review, gate and integration results are
recorded on [ticket #12](https://github.com/afewell-hh/bounded-agent-workflow/issues/12).

For `baw context`, the gates in [verification](verification.md#implemented-slice-gates-context)
are first run by the implementation worker in a restricted native sandbox using the
supplied socket and special-bit fixtures; that is the worker's claim only. Independent
review, default host gates (both fixture variables unset), integration and closeout
results are recorded on the implementation ticket
([#15](https://github.com/afewell-hh/bounded-agent-workflow/issues/15)), not here.

For `baw run execute`, an independent review of a committed candidate observed the full
`go test` fail in its native sandbox: the plan test's own `chmod` to `02600` reported
success but left `0600`, and that case had no supplied-fixture route. It also found that
a plan close error could mask parser errors (malformed JSON, invalid UTF-8, unsupported
schema), duplicate-key fixtures without any `\u` escape, no opened-file
safety-versus-identity or read-plus-close fixtures, a pipe "read error" seam that only
hid an EOF already reached, and watcher-failure tests that never checked a start
marker. The parser now runs before a close-only error is chosen, and the tests were
reworked as described in [verification](verification.md#implemented-slice-gates-execute)
and [special-bit fixture](#special-bit-fixture). Review, gate and integration results
are recorded on [ticket #18](https://github.com/afewell-hh/bounded-agent-workflow/issues/18), not here.

For `baw run review`, the implementation worker's one mandatory gate run on its first
candidate passed gofmt and the full `go test` (supplied fixture variables set) but failed
`go vet` on a test-only `append` with no values; build, metadata, the six binary journeys
and the 76 legacy comparisons did not run on those bytes. The test line was corrected
afterwards and only focused gofmt/vet ran on it. A later repair added the missing test
oracles described in [verification](verification.md#implemented-slice-gates-review) and
ran the mandatory gates once on its final bytes; that run is the worker's claim only.
A fresh independent review of that committed candidate observed all mandatory gates
pass but reproduced two defects (cancellation at the boundary immediately before the
exclusive ID mkdir consumed the ID and gave `review_uncertain`; a state-root safety error
found while acquiring was reported as `review_storage_unavailable`) and missing oracles
(JSON REQUIRED_FIXES journeys, report fixtures without genuine escapes, and no combined
read-plus-close fault). A further repair changed only `internal/review/storage.go` among
production files and added those tests; its focused review-package and review-journey
runs passed and the tests failed against the previous `storage.go`. It ran the mandatory
gates once on its final bytes; that run is likewise the worker's claim only.
Default host gates (all three fixture variables unset), independent review, integration
and closeout results are recorded on
[ticket #21](https://github.com/afewell-hh/bounded-agent-workflow/issues/21), not here.

This CLI profile applies to BAW development. Future application adopters validate their
own environment using [project adoption](../operator/project-adoption.md), including
a persistent GUI preview or scarce-lab verification when their work requires it.
