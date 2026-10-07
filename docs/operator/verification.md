# `baw run verify`: one recorded verifier-program observation

`baw run verify` runs one trusted local verifier program you choose against an
explicit clean committed candidate, inspects the candidate before and after,
and records the observed result in its own `verify-v1` attempt. It is a
standalone command: it needs an existing [run record](run-records.md) for its
references, but no [`baw run execute`](execution.md) or
[`baw run review`](review.md) attempt, and neither of those commands reads its
receipts.

A passed observation means exactly this: one delivered verifier observation
with the identified commit clean before and after it. It is not complete
project checks, independent review, approval, an artifact or source freeze,
merge authority or proof that the verifier tests anything. No native agent
adapter, credential, provider or network consumer is involved.

## How to run a disposable example

Starting conditions: a built `baw`, Git, a disposable repository with a
committed HEAD, a private (`0700`) state directory outside it, and a harmless
verifier program you compiled yourself. Never use the original checkout of a
real project.

1. Record the run (see [run records](run-records.md)):

   ```sh
   baw run create --state-dir /path/to/work/state --run-id 0123456789abcdef0123456789abcdef \
     --repo /path/to/work/project --ticket https://github.com/OWNER/REPO/issues/N \
     --scope-sha256 <64 hex> --policy-commit <full OID>
   ```

2. Commit the candidate you want verified. It may be the recorded HEAD or a
   later descendant; the worktree must be clean.

3. Write a nonsecret plan outside the worktree with mode `0600`:

   ```json
   {"schema_version":1,
    "verifier":{"executable":"/path/to/work/bin/dummy-verifier","arguments":[],"timeout_seconds":60}}
   ```

4. Run it with the full candidate object ID (add `--json` for the result):

   ```sh
   baw run verify --repo /path/to/work/project --state-dir /path/to/work/state \
     --run-id 0123456789abcdef0123456789abcdef --candidate <full HEAD OID> \
     --plan /path/to/work/verify-plan.json
   ```

   With a verifier that exits 0 and leaves the candidate unchanged, the report
   reads `Outcome: candidate_verification_passed`, exit status 0, and
   `state/verify-v1/<ID>/` holds `intent.json`, `result.json` (both `0600`),
   their retained `.pending-*` staging names and the `verifier/` scratch
   directory with `home/` and `tmp/` (all `0700`).

5. Running the same command again prints `baw: verification_exists` and starts
   nothing. A later candidate needs a new run record and ID.

6. Compare the packets as described in [what a verification does not cover](#what-a-verification-does-not-cover).

A dirty candidate is refused before anything is created
(`baw: candidate_not_clean`). The built-binary journey
`TestVerificationBinaryJourneys` follows these steps with a compiled fake
verifier for SHA-1 and SHA-256 repositories: passing and failing verifiers in
text and JSON, a verifier that edits a tracked file, a replay, a dirty
precondition and a later descendant under its own ID (see
[verification](../developer/verification.md)).

## What a verification does not cover

- Compare `verify.repository.before_head` and, when passed, `after_head` with
  the `repository.before_head` of any [review](review.md) result you rely on,
  and the saved intent `plan_sha256` with the SHA-256 of the plan you intended.
  Matching values do not authorize integration and do not bind the verifier's
  executed binary bytes; no binary digest is checked.
- The committed tree is identified, but ignored files, submodule worktree
  contents, a transient edit restored before exit and external inputs (network,
  other directories, the original `HOME` through absolute paths) are not
  observed or frozen. A tracked, staged, untracked or conflicted change, a new
  commit, a different top level or object format after the verifier makes the
  outcome `candidate_changed`.
- The verifier is trusted. It can be vacuous or print anything; its stdout and
  stderr are discarded and never shown or saved, so printing `PASS` means
  nothing. The trusted plan and real required gate evidence remain manual
  coordinator obligations, and findings remain manual ticket records.
- Unlike [`baw run execute`](execution.md), verify refuses an observed dirty
  candidate and inspects after the program. It does not change the execute or
  review schemas, eligibility or consumers, and it does not resolve every D1
  proof limitation listed in [review](review.md#what-a-review-does-not-cover-d1).

## Reference

### Syntax

```
baw run verify --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]
```

The five valued flags are each required exactly once, as `--flag VALUE` or
`--flag=VALUE`; `--json` at most once without a value. Unknown flags,
positionals, empty or NUL-containing values and duplicates are
`invalid_usage` (exit 2). `ID` is 32 lowercase hex; `OID` is 40 or 64
lowercase hex. Only `baw run verify --help` prints the command help. Relative
paths resolve from the caller's working directory; a subdirectory or
symlinked-ancestor `--repo` is normalized to the physical Git top level.

### Plan schema

At most 65536 bytes including whitespace, UTF-8, one JSON object with only
trailing whitespace, no duplicate decoded names at any depth (including names
written with Unicode escapes), exactly:

```json
{"schema_version":1,"verifier":{"executable":"/abs/path","arguments":["..."],"timeout_seconds":60}}
```

`schema_version` must be the raw token `1`. The command uses the unchanged
execute command rules: absolute executable, 0–64 string arguments of at most
1024 bytes without NUL, integer timeout 1–300. No shell, `PATH` lookup or
stdin. Anything else is `invalid_verification_plan`. The plan file must be a
current-user regular `0600` file without special bits, not a symlink.

### Order of checks

Nothing is created before all of these pass, and the first failure wins:

1. Syntax; then SIGINT/SIGTERM become cancellation for this call only.
2. State root (`state.OpenRoot` rules) and, if it exists, `verify-v1`
   (current-user `0700`, no special bits). Nothing is created.
3. Plan: `plan_unavailable`, `unsafe_state_path`, `state_permissions`,
   `state_changed` or `invalid_verification_plan`.
4. Run record, with its unchanged errors.
5. Inspection with the record's `repository_head` as checkpoint, with the
   inspector's unchanged errors. A SHA-1 record used with a SHA-256 repository
   (or the reverse) is the inspector's `invalid_usage` (exit 2). Then the
   candidate width (`invalid_usage`), HEAD equal to the candidate
   (`verification_checkpoint_mismatch`) and zero staged, unstaged, untracked
   and conflicted counts (`candidate_not_clean`).
6. Layout: neither the state root nor the repository top level may contain
   the other, and the plan may not be under the repository
   (`unsafe_execution_layout`). This verify-only layout constraint keeps the
   observed repository outside the state root even where execute or review
   would accept it; it is not an access or security boundary.
7. Executable (`executable_unavailable`).
8. Existing `verify-v1/<ID>`: any safe directory, even empty, is
   `verification_exists`; a symlink or non-directory `unsafe_state_path`,
   wrong owner/mode `state_permissions`, other I/O
   `verification_storage_unavailable`.

Cancellation is checked after each stage and before acquisition
(`verification_cancelled`, empty stdout, nothing started, no ID created).

### Storage and durability

The namespace is created `0700` if absent, the state root and namespace are
rechecked and synced, and `verify-v1/<ID>` is created exclusively; only that
mkdir owns the ID. Before it, state safety codes are kept, other failures are
`verification_storage_unavailable` and a returned root Sync error is
`durability_unavailable`. After it, any failure is `verification_uncertain`
and the directory is kept; nothing is removed and the ID is never reused.
Intent and result are each written to a retained `.pending-*` file, synced,
hard-linked to their final name and the directory synced. Sync is Go's
`os.File.Sync`; there is no power-loss guarantee.

### What the verifier gets

Working directory the physical Git top level, stdin `/dev/null`, the exact
arguments and only `HOME=<attempt>/verifier/home`,
`TMPDIR=<attempt>/verifier/tmp`, `LANG=C` and `LC_ALL=C` (both directories new
and empty). This prevents accidental configuration inheritance, not access.
Stdout and stderr are each capped at 65536 bytes and discarded. A 300-second
overall deadline starts after the intent is durable.

### Result

Intent (`intent.json`, 13 keys): `schema_version` 1, `run_id`,
`record_state` `verification_intent`, `ticket_url`, `scope_sha256`,
`policy_commit`, `repository_object_format`, `repository_head` (the record's),
`candidate_head`, `plan_sha256`, `created_at`, `authority` and `readiness`
`not_evaluated`.

Result (`result.json` and `--json` output, 11 keys in this order):
`schema_version`, `run_id`, `operation` `run_verify`, `authority`,
`readiness`, `outcome`, `verification` `{state, exit_code}`, `repository`
`{object_format, before_head, after_head}`, `receipt_state` `recorded`,
`created_at` (equal to the intent's), `completed_at`.

| Observation | outcome | verification | after_head |
| --- | --- | --- | --- |
| not started | `verification_unverified` | `not_started`/null | null |
| unusable, cancelled or deadline | `verification_unverified` | `unverified`/null | null |
| usable nonzero exit N | `verification_failed` | `exited`/N | null |
| usable exit 0, candidate changed | `candidate_changed` | `exited`/0 | null |
| usable exit 0, candidate unchanged | `candidate_verification_passed` | `exited`/0 | candidate |

Usable means every fact of the observed runner: started, exited normally,
joined, no owned group member left and actual EOF on both pipes, with no
signal, watcher failure, timeout, cancellation or output cap.

Text output is eight lines:

```
BAW verification observations
Run: ID
Authority: not_evaluated
Readiness: not_evaluated
Outcome: OUTCOME
Verification: state=STATE exit_code=EXIT
Repository: object_format=FORMAT before_head=CANDIDATE after_head=AFTER
Receipt: recorded
```

A null value prints `unknown`. No path, plan argument, program output or
error text appears.

### Exit status and errors

Exit 0 only for a delivered `candidate_verification_passed`. Any other
delivered result exits 1 with `baw: verification_failed`. Other failures exit
1 with `baw: CODE` and empty stdout, except `invalid_usage` (exit 2). A help
write failure is `output_unavailable`. A failed or short output write after
publication is `verification_uncertain` and may leave a prefix.

## Explanation and limits

Ctrl-C or SIGTERM before the ID is owned cancels with nothing created. After
the ID is owned and until the intent's directory Sync completes, it is
`verification_uncertain` with empty stdout, no verifier start and the ID and
any partial files kept. After the intent is durable it yields a recorded
`verification_unverified`; once
classified, the outcome is kept. A second Ctrl-C is absorbed while
publication or inspection finishes. SIGKILL can leave an intent without a
result, or partial staging files; that ID is not retried, replayed or cleaned
up. Escaped verifier descendants and same-user writers are not contained, and
no process ID is stored.
