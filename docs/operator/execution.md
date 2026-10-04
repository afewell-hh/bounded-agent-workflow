# `baw run execute`: one local worker and one verifier

`baw run execute` runs one trusted local worker program and then, only if the
worker's observed result is usable, one verification program in the same Git
worktree. It durably records an intent before any program can start and a
terminal result before any output, refuses every repeat of the same run ID and
reports uncertainty instead of guessing.

It is a local process primitive. It provides no native agent adapter, prompt,
credential handling, approval evaluation, review, acceptance, merge, retry,
resume, cleanup or recovery. A run record and a plan are bookkeeping inputs;
neither proves approval. The coordinator must already have authority under the
[manual workflow](manual-workflow.md) before running it.

## How to run a disposable example

Starting conditions: a built `baw`, Git, a disposable repository with a
committed HEAD, and two harmless programs you compiled yourself (for example a
tiny Go program that writes `dummy.txt` in its working directory, and a second
one that exits 0 only if `dummy.txt` holds the expected bytes). Never use the
original checkout of a real project.

1. Create a private state directory outside the worktree and record the
   current HEAD (see [run records](run-records.md)):

   ```sh
   mkdir -m 700 /path/to/work/state
   baw run create --state-dir /path/to/work/state --run-id 0123456789abcdef0123456789abcdef \
     --repo /path/to/work/project --ticket https://github.com/OWNER/REPO/issues/1 \
     --scope-sha256 <64 hex> --policy-commit <HEAD OID>
   ```

2. Write a nonsecret plan outside the worktree with mode `0600`:

   ```json
   {"schema_version":1,
    "worker":{"executable":"/path/to/work/bin/dummy-worker","arguments":[],"timeout_seconds":60},
    "verification":{"executable":"/path/to/work/bin/dummy-verifier","arguments":[],"timeout_seconds":60}}
   ```

   ```sh
   chmod 600 /path/to/work/plan.json
   ```

3. Run it (add `--json` for the result object):

   ```sh
   baw run execute --repo /path/to/work/project --state-dir /path/to/work/state \
     --run-id 0123456789abcdef0123456789abcdef --plan /path/to/work/plan.json
   ```

   With a worker that wrote `dummy.txt` and a verifier that checked it, the
   report reads `Outcome: verification_passed`, exit status 0. The worktree now
   holds the worker's change; `state/execute-v1/<ID>/` holds `intent.json`,
   `result.json` (both `0600`), their retained `.pending-*` staging names and
   the `worker/` and `verifier/` scratch directories (all `0700`).

4. Running the same command again prints `baw: execution_exists` and starts
   nothing. There is one attempt per run ID.

The built-binary journey `TestExecutionBinaryJourneys` follows these steps with
compiled fake programs (see [verification](../developer/verification.md)).

## Reference

### Syntax

```
baw run execute --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]
baw run execute --help
```

Each valued option is required exactly once (`--opt VALUE` or `--opt=VALUE`);
`--json` at most once without a value; `--help` only alone. Unknown, repeated,
empty or positional arguments, `-h`, mixed help or a decoded NUL give
`baw: invalid_usage` (exit 2) before any filesystem or Git work. `ID` is 32
lowercase hex characters naming an existing run record.

- `--repo` may name a worktree subdirectory or an ancestor alias; the Git top
  level is used.
- `--state-dir` is an existing current-user `0700` directory, not a final
  symlink (the [run record](run-records.md) state contract).
- `--plan` is a current-user `0600` regular file with no special bits and not a
  final symlink, read without following it and without blocking on a FIFO.
- The state directory and plan must be outside the worktree, and the derived
  `DIR/execute-v1` must be neither the worktree, inside it nor contain it
  (path components, not text prefixes): `repo=/state/project` is allowed,
  `repo=/state/execute-v1` is not.

### Plan schema

At most 65,536 bytes of UTF-8, one JSON object, no duplicate member names
(including escaped equivalents), nothing after it. Exactly:

| Key | Value |
|---|---|
| `schema_version` | literal integer `1` |
| `worker`, `verification` | command object |

Command object, exactly: `executable` (nonempty absolute path, no NUL),
`arguments` (0–64 strings, each at most 1,024 bytes, no NUL), `timeout_seconds`
(literal decimal integer 1–300). Anything else is `invalid_execution_plan`.
Each executable must resolve (ancestor aliases allowed, final symlink not) to a
regular file owned by you or root, without setuid/setgid/sticky or group/other
write bits, executable by you, with real and effective IDs equal; otherwise
`executable_unavailable`. There is no PATH lookup, shell or search. The plan
must not contain secrets; it is hashed into the intent but never copied.

### What each program gets

Program runs directly, working directory the Git top level, stdin
`/dev/null`, and an environment built from scratch: `HOME` and `TMPDIR` set to
that program's own new empty `0700` directories
(`execute-v1/<ID>/worker/{home,tmp}` or `verifier/{home,tmp}`), `LANG=C`,
`LC_ALL=C`, nothing else (no `PATH`, Git, GitHub, token or proxy variables).
Limits: the plan timeout, 1 MiB stdout, 64 KiB stderr, 620 seconds overall from
durable intent. Output is drained and discarded; it is never shown or saved.

The separate scratch pairs prevent accidental contamination of the verifier by
worker caches and temp files. They are not isolation: both programs run as you
and can read or change anything you can, including each other's directories,
and can use the network.

### Order

Syntax → state root and existing namespace safety → plan read → run record
read → guarded inspection → object format/HEAD equal to the record and layout
→ executables → existing attempt → namespace → state root opened, its descriptor
rechecked (directory, owner, `0700`, same identity) and synced → exclusive attempt
directory →
all six scratch directories created, checked, synced and closed → intent
published → worker → (usable exit 0 only) fresh inspection → verifier → result
published → output. The first failure wins. Dirty files are allowed; HEAD
equality is not a source freeze. Nothing before the namespace acquisition
changes the state directory.

### Result

`intent.json` keys: `schema_version` 1, `run_id`, `record_state`
`"execution_intent"`, `ticket_url`, `scope_sha256`, `policy_commit`,
`repository_object_format`, `repository_head` (copied from the record),
`plan_sha256`, `created_at`.

`result.json` (also the `--json` output, byte for byte) keys:
`schema_version` 1, `run_id`, `operation` `"run_execute"`, `authority` and
`readiness` `"not_evaluated"`, `outcome`, `worker`, `verification`,
`repository`, `receipt_state` `"recorded"`, `created_at` (the intent time),
`completed_at`. `worker` and `verification` are `{"state", "exit_code"}` with
state `not_started`, `exited` or `unverified`; `exit_code` is 0–255 only for
`exited`, otherwise null. `repository` is `{"object_format", "before_head",
"after_head"}`. No paths, arguments, PIDs, branch names, program output or OS
errors are recorded.

Text output, nine lines (null prints `unknown`):

```
BAW execution observations
Run: ID
Authority: not_evaluated
Readiness: not_evaluated
Outcome: OUTCOME
Worker: state=STATE exit_code=INTEGER_OR_UNKNOWN
Verification: state=STATE exit_code=INTEGER_OR_UNKNOWN
Repository: object_format=FORMAT before_head=OID after_head=OID_OR_UNKNOWN
Receipt: recorded
```

| Observation | worker | verification | outcome |
|---|---|---|---|
| worker never started | not_started | not_started | `worker_unverified` |
| worker started; timeout, output cap, signal, watcher/pipe/cleanup failure or cancellation | unverified | not_started | `worker_unverified` |
| worker exited 1–255, cleanup confirmed | exited/N | not_started | `worker_failed` |
| worker exited 0; post-worker inspection failed or top level/format changed | exited/0 | not_started | `inspection_failed` |
| verifier never started, or started and unverified | exited/0 | not_started or unverified | `verification_unverified` |
| verifier exited 1–255 | exited/0 | exited/N | `verification_failed` |
| verifier exited 0 | exited/0 | exited/0 | `verification_passed` |

"Usable" requires a known normal exit, a synchronous join of the program, no
signalable member left in its own process group and real end-of-file on both
output pipes. A descendant that left the group, or that you cannot signal, is
not observed; no result claims every descendant stopped. Causes such as timeout
versus output cap are deliberately not distinguished.

`after_head` is the HEAD observed after the worker and before the verifier
(null if unborn or if that inspection was not accepted). Nothing inspects the
repository after the verifier, which may itself edit or commit. A worker commit
is allowed; a later repeat then fails with `execution_checkpoint_mismatch`
because earlier validation errors win over `execution_exists`.

`verification_passed` is an observation of your verification program, not a
code review, acceptance, scope approval or provenance proof.

### Exit status and errors

- 0: `verification_passed`, recorded and delivered.
- 1 with the result on stdout and `baw: execution_failed` on stderr: any other
  recorded outcome.
- 1 with empty stdout and `baw: CODE`: `plan_unavailable`,
  `invalid_execution_plan`, `execution_checkpoint_mismatch`,
  `unsafe_execution_layout`, `executable_unavailable`, `execution_exists`,
  `execution_storage_unavailable`, `durability_unavailable`,
  `execution_cancelled`, `execution_uncertain`, plus existing state, record and
  inspection codes.
- 2: `invalid_usage`.

`execution_uncertain` means the attempt directory exists but the intent, the
result or its delivery could not be confirmed. Effects that already happened
are not rolled back and stdout may hold a partial prefix if writing failed.

## Explanation and limits

**Trust model.** Programs, state root and plan are trusted, same-user inputs.
Path checks are observations at one moment; another process running as you can
change them later. Clearing the environment and skipping the shell are not a
sandbox.

**One attempt, not a global fence.** Only the exclusive creation of
`execute-v1/<ID>` admits an attempt; concurrent callers with the same state
root and ID start one worker. A different state root or ID can still target the
same worktree; keeping one writer per worktree remains a manual rule.

**Durability.** Intent and result are written to retained exclusive
`.pending-intent-*`/`.pending-result-*` files, synced, closed and hard-linked to
their final names, then the directory is synced. The staging name stays and
shares the final file's inode; changing one changes the other. This is
exclusive publication, not protection against power loss or tampering.

**Interrupts.** SIGINT and SIGTERM while execute runs cancel it: the running
program's own group is stopped within bounded waits and the published result
reads `worker_unverified` or `verification_unverified` (or no program starts).
stderr still says only `baw: execution_failed`; read the result to see the
cancellation outcome. A second interrupt is absorbed too; it does not force an
exit, and cleanup and publication may wait for the post-worker inspection (up
to its 120-second limit). SIGKILL or a crash runs no cleanup: a program may
still be running, the intent may exist without a result, and nothing records
its process ID.

**No replay or recovery.** An interrupted or uncertain attempt is never
retried. Do not delete the attempt to try again and do not kill processes
based on guesses. Inspect the retained evidence and the worktree under the
manual lifecycle and record what happened; a further attempt needs a new,
separately approved run.
