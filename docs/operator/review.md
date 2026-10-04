# `baw run review`: one recorded reviewer-program attempt

`baw run review` runs one trusted reviewer program against an explicit, clean,
committed candidate, after a saved [`baw run execute`](execution.md) attempt
reported `verification_passed`. It durably records an intent before the
program can start and a terminal result before any output, refuses every
repeat of the same run ID and reports uncertainty instead of guessing.

It records a program verdict. It is not a code review, approval, acceptance,
quality judgment or merge decision, and it supplies no native agent adapter,
prompt, credential handling, model independence check, verification rerun,
repair, retry, resume, cleanup or recovery. The run record, execution receipts
and plan are saved claims and bookkeeping inputs; none proves authority. Only
compiled fake reviewer programs in disposable repositories have been exercised.

## How to run a disposable example

Starting conditions: a built `baw`, Git, a disposable repository with a
committed HEAD, a private state directory outside it, and harmless programs you
compiled yourself: a worker, a verifier and a reviewer that prints exactly
`{"schema_version":1,"verdict":"PASS"}` and exits 0. Never use the original
checkout of a real project.

1. Record the run and execute one worker/verifier attempt as described in
   [`baw run execute`](execution.md#how-to-run-a-disposable-example). Keep its
   `--json` output; it must say `"outcome":"verification_passed"`.

2. Commit the candidate you want reviewed. If the worker left uncommitted
   changes, the coordinator commits them now; the candidate is then a later
   descendant of the execute `after_head`. The worktree must be clean.

3. Write a nonsecret plan outside the worktree with mode `0600`:

   ```json
   {"schema_version":1,
    "reviewer":{"executable":"/path/to/work/bin/dummy-reviewer","arguments":[],"timeout_seconds":60}}
   ```

4. Run it with the full candidate object ID (add `--json` for the result):

   ```sh
   baw run review --repo /path/to/work/project --state-dir /path/to/work/state \
     --run-id 0123456789abcdef0123456789abcdef --candidate <full HEAD OID> \
     --plan /path/to/work/review-plan.json
   ```

   The report reads `Outcome: review_passed`, exit status 0, and
   `state/review-v1/<ID>/` holds `intent.json`, `result.json` (both `0600`),
   their retained `.pending-*` staging names and the `reviewer/` scratch
   directory with `home/` and `tmp/` (all `0700`).

5. Running the same command again prints `baw: review_exists` and starts
   nothing.

6. Compare the two packets as described in
   [what a review does not cover](#what-a-review-does-not-cover-d1).

The built-binary journey `TestReviewBinaryJourneys` follows these steps with
compiled fake programs, for SHA-1 and SHA-256 repositories, once with a
candidate equal to `after_head` and once with a later committed descendant
(see [verification](../developer/verification.md)).

## What a review does not cover (D1)

Neither `review_passed` nor a pair of execute and review packets proves the
candidate's quality, approval, acceptance or that the identified candidate was
verified. Use the packets' repository fields, never file names, to see what
lines up:

1. Read `repository.after_head` from the execute result and
   `repository.before_head` from the review result (the reviewed candidate).
2. **Same ID** — for example both are
   `3f2a9c1d4b5e6f708192a3b4c5d6e7f801234567`. The ID comparison lines up, but
   this is still not a source freeze or independent proof: execute allows a
   dirty worktree, observes `after_head` before the verifier runs, and records
   no final source digest, so the verifier may have seen uncommitted changes
   that are not in the commit, or changed things itself.
3. **Different IDs** — for example execute `after_head`
   `3f2a9c1d4b5e6f708192a3b4c5d6e7f801234567` and review `before_head`
   `9b8c7d6e5f4a3b2c1d0e9f8a7b6c5d4e3f2a1b0c`. The candidate is a later
   committed descendant. Execute's `verification_passed` does **not** cover the
   further changes. Before routine integration, run verification of that exact
   candidate under separately approved scope.

The program verdict packet contains no findings and is not a substantive review
record. Real findings, independent model reviews and their evidence stay in the
ticket and the [manual workflow](manual-workflow.md). Nothing here performs or
proves a cross-model review.

## Reference

### Syntax

```
baw run review --repo PATH --state-dir DIR --run-id ID --candidate OID --plan FILE [--json]
baw run review --help
```

Each valued option is required exactly once (`--opt VALUE` or `--opt=VALUE`);
`--json` at most once without a value; `--help` only alone. Unknown, repeated,
empty or positional arguments, `-h`, `help`, mixed help or a NUL give
`baw: invalid_usage` (exit 2) before any filesystem or Git work. `ID` is 32
lowercase hex characters. `OID` is exactly 40 or 64 lowercase hex characters;
abbreviations and uppercase are refused. A width that does not match the
discovered repository format is also `invalid_usage`, after inspection.

Relative `--repo`, `--state-dir` and `--plan` resolve from your working
directory. The reviewer's working directory is the physical Git top level, so
relative reviewer arguments resolve there; the executable must be absolute.

### Plan schema

At most 65,536 bytes of UTF-8, one JSON object, no duplicate member names at
any level (including escaped equivalents), only whitespace after it. Exactly
`schema_version` (literal integer `1`) and `reviewer`, a command object with
the same rules as [execute's](execution.md#plan-schema): absolute `executable`,
0–64 `arguments` of at most 1,024 bytes without NUL, literal `timeout_seconds`
1–300. Anything else is `invalid_review_plan`. The plan must be a current-user
`0600` regular file without special bits, not a final symlink, read without
following it or blocking; the opened file's type, owner and mode are checked
before its identity. A read or close failure is `plan_unavailable` (a close
failure only when nothing earlier failed). The executable gets execute's checks
(`executable_unavailable`). The plan is hashed into the intent, never copied.

### Order of checks

1. syntax;
2. state root and existing `review-v1` namespace safety (nothing created);
3. plan safety, read and parse;
4. run record read;
5. `execute-v1` and `execute-v1/<ID>` (current-user `0700`, no symlink), then
   `intent.json` and `result.json` read with the plan's file rules, at most
   65,536 bytes each, a second hard link allowed;
6. receipt consistency and eligibility;
7. guarded inspection with the execute `after_head` as checkpoint (default
   limits, cleaned Git environment, no `gh`), then candidate width, HEAD equal
   to the candidate and zero staged, unstaged, untracked and conflicted entries;
8. layout (state root and plan outside the top level; `review-v1` neither
   equal to, inside nor containing it) and the reviewer executable;
9. existing `review-v1/<ID>`: a safe existing directory is `review_exists`
   without reading it.

The first failure wins, and cancellation before acquisition is
`review_cancelled`. None of these steps changes the state directory.

Receipt rules: each receipt's own `run_id` must be 32 lowercase hex characters
and pass the unchanged execute validator for that ID (`invalid_execution_receipt`
otherwise, including size, UTF-8, duplicates, schema and a `completed_at`
earlier than `created_at`). Then the record, intent and result run IDs must
equal the requested ID, and the record's ticket, scope, policy, format and head
must equal the intent's, the result's format and `before_head` the intent's and
both `created_at` values equal (`execution_receipt_mismatch`). Eligibility
needs `verification_passed`, worker and verification `exited`/0 and a non-null
`after_head` (`review_not_eligible`). A missing namespace, directory or receipt
is `execution_receipt_unavailable`. Unsafe paths and modes give the existing
`unsafe_state_path`/`state_permissions`; a file replaced while being read gives
`state_changed`.

Inspector errors are returned unchanged: `checkpoint_missing`,
`checkpoint_diverged` (the candidate does not descend from `after_head`) and
`checkpoint_unborn`, which wins over the width, equality and cleanliness
checks. Then `review_checkpoint_mismatch` (HEAD is not the candidate) and
`candidate_not_clean`.

### What the reviewer gets

One direct start: no shell, PATH lookup or stdin input (`/dev/null`). The
environment is built from scratch: `HOME` and `TMPDIR` set to the new empty
`review-v1/<ID>/reviewer/home` and `reviewer/tmp`, `LANG=C`, `LC_ALL=C`, nothing
else. Limits: the plan timeout, 2,048 bytes stdout, 65,536 bytes stderr, 300
seconds overall from durable intent. stderr and any stdout are never shown or
saved; stdout is parsed only as the report.

The report is exactly one UTF-8 JSON object with `schema_version` literal `1`
and `verdict` `"PASS"` or `"REQUIRED_FIXES"`, whitespace allowed after it.
Duplicates (including escaped ones), unknown keys, other number forms or
anything else is invalid. The verdict counts only for a usable exit 0. After
that, a fresh guarded inspection with the candidate as checkpoint must see the
same physical top level, format and HEAD, and zero staged, unstaged, untracked
and conflicted entries.

### Result

`intent.json` keys: `schema_version` 1, `run_id`, `record_state`
`"review_intent"`, `ticket_url`, `scope_sha256`, `policy_commit`,
`repository_object_format`, `repository_head` (from the record),
`candidate_head`, `plan_sha256`, `execution_intent_sha256`,
`execution_result_sha256` (hashes of the exact bytes read), `created_at`,
`authority` `"not_evaluated"`.

`result.json` (the `--json` output, byte for byte) keys: `schema_version` 1,
`run_id`, `operation` `"run_review"`, `authority` and `readiness`
`"not_evaluated"`, `outcome`, `reviewer` (`{"state","exit_code"}`), `verdict`
(`"PASS"`, `"REQUIRED_FIXES"` or null), `repository`
(`{"object_format","before_head","after_head"}`; `before_head` is the
candidate), `receipt_state` `"recorded"`, `created_at` (intent time),
`completed_at`.

Text output, eight lines (null prints `unknown`):

```
BAW review observations
Run: ID
Authority: not_evaluated
Readiness: not_evaluated
Outcome: OUTCOME
Reviewer: state=STATE exit_code=INTEGER_OR_UNKNOWN verdict=VERDICT_OR_UNKNOWN
Repository: object_format=FORMAT before_head=OID after_head=OID_OR_UNKNOWN
Receipt: recorded
```

| Observation | reviewer | verdict | after_head | outcome |
|---|---|---|---|---|
| never started | not_started/null | null | null | `reviewer_unverified` |
| started; timeout, output cap, signal, watcher/pipe/cleanup failure | unverified/null | null | null | `reviewer_unverified` |
| usable exit 0, invalid report | exited/0 | null | null | `reviewer_unverified` |
| usable exit 1–255 (even if stdout says PASS) | exited/N | null | null | `reviewer_failed` |
| exit 0, valid report, candidate changed or post-inspection failed | exited/0 | parsed | null | `candidate_changed` |
| exit 0, valid report, candidate unchanged | exited/0 | PASS / REQUIRED_FIXES | candidate | `review_passed` / `review_required_fixes` |
| interrupted or deadline after intent, before classification | not_started or unverified/null | null | null | `reviewer_unverified` |

### Exit status and errors

- 0: `review_passed`, recorded and delivered.
- 1 with the result on stdout and `baw: review_failed`: any other recorded
  outcome.
- 1 with empty stdout and `baw: CODE`: `plan_unavailable`,
  `invalid_review_plan`, `execution_receipt_unavailable`,
  `invalid_execution_receipt`, `execution_receipt_mismatch`,
  `review_not_eligible`, `review_checkpoint_mismatch`, `candidate_not_clean`,
  `unsafe_execution_layout`, `executable_unavailable`, `review_exists`,
  `review_storage_unavailable`, `durability_unavailable`, `review_cancelled`,
  `review_uncertain`, `output_unavailable` (help only), plus existing state,
  record and inspection codes.
- 2: `invalid_usage`.

`review_uncertain` means the attempt directory exists but the intent, the
result or its delivery could not be confirmed; stdout may hold a partial
prefix. Nothing is rolled back or removed.

Before the attempt directory is created, acquiring rechecks the state root and
`review-v1`: an unsafe path, mode or replacement found then keeps its
`unsafe_state_path`, `state_permissions` or `state_changed` code; other storage
failures are `review_storage_unavailable` and a failed sync
`durability_unavailable`. An interrupt seen up to the exclusive directory
creation is `review_cancelled` with no attempt directory.

## Explanation and limits

**Trust model.** The reviewer, repository, plan and state are trusted
same-user inputs. Path checks are observations at one moment. Clearing the
environment is not a sandbox: the reviewer runs as you and can read or change
anything you can, and can use the network.

**What the post-check cannot see.** Git-ignored files (including globally
ignored ones under the accepted inspection rules), submodule worktree
contents, a change reverted before the post-inspection and descendants that
escaped the reviewer's process group are not observed. A reviewer that edits or
commits tracked source, or replaces the top-level directory or its object format,
cannot produce `review_passed`; nothing else is claimed.

**One attempt.** Only the exclusive creation of `review-v1/<ID>` admits an
attempt; concurrent callers with the same ID start one reviewer. Keeping one
writer per worktree remains a manual rule.

**Durability.** Intent and result use retained exclusive staging files, sync,
close, a hard link without replacement and a directory sync, as in
[execute](execution.md#explanation-and-limits). No power-loss guarantee.

**Interrupts.** SIGINT and SIGTERM after valid syntax cancel the review: before
the attempt directory exists the result is `review_cancelled` (a namespace
already created stays); after it exists but before the intent is confirmed,
`review_uncertain`; after the intent, a recorded `reviewer_unverified` unless
the outcome was already classified. A second interrupt is absorbed. SIGKILL
runs no cleanup and may leave an intent without a result.

**No replay or recovery.** Never delete an attempt to try again or kill
processes by guess. Inspect the retained evidence under the manual lifecycle; a
further review needs a new, separately approved run.
