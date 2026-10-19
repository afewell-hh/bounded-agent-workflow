# `baw run worker`: one recorded native Claude worker attempt

`baw run worker` runs one native Claude Code worker, through one approved profile, on a
clean committed worktree. It supplies the complete frozen project instructions on stdin,
validates the native stream and the worker's final report, and records the bounded
worktree change. It reports **observations only**. A `worker_completed_unverified`
outcome is not a test result, review, acceptance, commit or authority. The lead still runs
every test, commits the candidate and arranges independent review.

This page is for the lead/operator who prepares the inputs. It describes the
implemented candidate on the development branch, not a release.

## Support boundary

- Host: darwin/arm64 only. Other platforms fail with `profile_unavailable` before any state
  is read.
- One profile: `claude-2.1.285-supervised-v1` with source matrix
  `claude-2.1.285-source-v1`, the pinned Claude Code 2.1.285 executable and the operator's
  existing subscription login. No API-key fallback, login, logout, update or token access.
- Native tools are limited to Read, Glob, Grep, Edit and Write. The worker runs no shell,
  tests, agents, background tasks or GitHub actions, and never commits.
- One attempt per run ID. A repeated or uncertain attempt is never started again, resumed
  or replayed. There is no repair loop or recovery.
- No native Codex integration. `baw run review` keeps its execute-v1 prerequisite; a manual
  Codex review is recorded on the ticket or PR, not imported into BAW.

## How to run one worker attempt

Commands in `sh` blocks run in a terminal on the host. JSON blocks are file templates:
upper-case words such as `RUN_ID` are placeholders you replace, and no placeholder,
digest or saved record is an approval.

### Prerequisites (owned by the lead)

- An approved, frozen ticket scope for this task and the attempt budget it allows. Its
  scope SHA-256 and the approved policy commit are the references you will record.
- A fresh assigned worktree whose HEAD is the committed base for the task, with no staged,
  unstaged, untracked **or ignored** files and no other writer.
- A run ID you choose: 32 lowercase hex characters, with no previous worker attempt
  in this state directory. Its saved run record may already exist. BAW allocates no IDs.
- A private state directory (`0700`, yours, outside every checkout) used for the run
  record and the worker attempt, and a private input directory, also outside the
  worktree, for the plan, task and profile files.
- The already-reviewed, nonsecret profile manifest for the pinned Claude Code executable
  and the operator's existing subscription login. This page does not install, log in,
  update or generate a profile; if none is approved, stop.

The lead has also audited broader and nested instructions for the task. For BAW, the
instruction list starts with `CLAUDE.md`, `AGENTS.md`, `workflow/protocol.md` and
`workflow/roles/worker.md`, followed by the documents required by the task and applicable
nested instructions. Supply full dependencies explicitly: safe mode does not expand
imports, and nothing is fetched or imported automatically. The lead establishes a
different project’s adopted instruction set before use; the command validates supplied
identities, not semantic completeness or adoption.

### Steps

1. Create the [run record](run-records.md), or check an existing one (shell commands):

   ```sh
   baw run create --state-dir /abs/state --run-id RUN_ID --repo /abs/worktree \
     --ticket https://github.com/OWNER/REPO/issues/NUMBER \
     --scope-sha256 SCOPE_SHA256 --policy-commit POLICY_OID --json
   baw status --state-dir /abs/state --run-id RUN_ID --json
   ```

   Create observes the worktree's committed HEAD; a record is never overwritten
   (`record_exists`), so check an existing one with `baw status` instead. Confirm the
   saved `repository_head` is the intended base. The record only stores the references
   you supplied (`authority` is `not_evaluated`); it does not establish approval.

2. Write three private files in the input directory, each a current-user `0600` regular
   file. Copy these task fields from the saved record:

   | Task field | Record field |
   |---|---|
   | `task_id` | `run_id` |
   | `scope_sha256` | `scope_sha256` |
   | `base_commit` | `repository_head` |

   Plan (at most 65,536 bytes):

   ```json
   {"schema_version": 1, "task_file": "/abs/private/task.json", "profile_file": "/abs/private/profile.json"}
   ```

   Task (at most 65,536 bytes; placeholder values):

   ```json
   {
     "schema_version": 1,
     "task_id": "RUN_ID",
     "scope_sha256": "RECORD_SCOPE_SHA256",
     "base_commit": "RECORD_HEAD",
     "task": "Record the marker required by the project rule.",
     "instructions": [
       {"path": "CLAUDE.md", "blob_oid": "BLOB_OID", "sha256": "CONTENT_SHA256"},
       {"path": "AGENTS.md", "blob_oid": "BLOB_OID", "sha256": "CONTENT_SHA256"},
       {"path": "workflow/protocol.md", "blob_oid": "BLOB_OID", "sha256": "CONTENT_SHA256"},
       {"path": "workflow/roles/worker.md", "blob_oid": "BLOB_OID", "sha256": "CONTENT_SHA256"}
     ],
     "allowed_changes": ["target.txt"]
   }
   ```

   Append every further applicable document after these four. Take each `blob_oid` and
   `sha256` from the same committed base as `base_commit`; because the worktree is clean,
   its bytes must match both. For example (shell commands, run in the worktree):

   ```sh
   git rev-parse RECORD_HEAD:workflow/protocol.md            # blob_oid
   git cat-file blob RECORD_HEAD:workflow/protocol.md | shasum -a 256   # sha256
   ```

   Profile (the coordinator's nonsecret reviewed manifest; synthetic example values that
   no real attempt accepts — use the approved manifest unchanged):

   ```json
   {
     "schema_version": 1,
     "profile_id": "claude-2.1.285-supervised-v1",
     "source_matrix_id": "claude-2.1.285-source-v1",
     "executable": "/abs/path/versions/2.1.285",
     "executable_sha256": "EXECUTABLE_SHA256",
     "approved_profile_sha256": "CANONICAL_SHA256",
     "user_home": "/Users/OPERATOR",
     "user_name": "OPERATOR",
     "shell": "/bin/zsh",
     "allowed_plugins": [{"name": "example-plugin", "path": "/example", "version": "example"}]
   }
   ```

3. Run the worker with the same state directory and run ID (shell command):

   ```sh
   baw run worker --repo /abs/worktree --state-dir /abs/state --run-id RUN_ID \
     --plan /abs/private/plan.json --json
   ```

4. Read the outcome. Exit 0 means `worker_completed_unverified`. A delivered other outcome
   exits 1 with `baw: worker_failed`. The exit status and the worker's report are
   observations, not a result you can accept.

5. Hand the change on. The lead examines the full worktree change, including untracked
   and ignored files, against the ticket scope; runs the ticket's verification and tests;
   commits the candidate and records its identity; obtains an independent review of that
   candidate (manual, outside BAW, recorded on the ticket or PR); and integrates it only
   under the ticket's authority.

Recovery: on `worker_uncertain`, `input_changed` or any outcome other than
`worker_completed_unverified`, do not rerun the same ID. Preserve the attempt directory
and the worktree (edits are never rolled back; do not reset or clean them) and diagnose.
A new attempt needs a new run record and the ticket's attempt budget.

Use `baw --help` to find the worker command and `baw run worker --help` for its syntax and support boundary.

## Reference

### Syntax

```text
baw run worker --repo PATH --state-dir DIR --run-id ID --plan FILE [--json]
baw run worker --help
```

Each valued option exactly once; `--json` at most once and without a value. Positional,
unknown, duplicated, empty or NUL-containing arguments are `invalid_usage` (exit 2). The
run ID is 32 lowercase hex characters.

### Inputs

- **Task.** `task_id` equals the run ID, `scope_sha256` and `base_commit` equal the run
  record. `task` is 1–16,384 bytes. `instructions` holds 1–32 entries, each a regular blob
  at the launch commit with matching `blob_oid`, and worktree bytes (at most 65,536 per
  file, read without following links) matching both `sha256` and the blob ID.
  `allowed_changes` holds 1–128 unique literal repository-relative files: no glob,
  absolute, `.`/`..`, empty or `.git` component, no instruction path, no `.claude`
  component, and not `CLAUDE.md`, `CLAUDE.local.md`, `AGENTS.md`, `.mcp.json`,
  `.gitignore`, `.gitattributes` or `.gitmodules`. Parent directories must already exist
  as real directories; the file itself may be absent or a regular file. Workspace names
  containing permission-pattern characters (`*?[]{}\()`), invalid UTF-8 or control characters
  are unsupported (`unsafe_execution_layout`), keeping the native rules literal.
- **Profile.** Exact keys above. `approved_profile_sha256` is the SHA-256 of the canonical
  encoding of every other member (members sorted by name, no insignificant whitespace,
  no HTML escaping, number literals unchanged). Plugin tuples compare by that canonical
  encoding, so member order does not matter but strings, list order and types do. The
  executable path must name the version in a path component, be its own physical path
  (no alias), a regular file owned by you or root, not group/other writable, with the
  declared SHA-256. The auth HOME must be a canonical real directory you own.

### Order of checks

Syntax → platform → state root and `worker-v1` namespace → plan, task and profile files →
run record → guarded inspection (HEAD, object format, clean counts) → task against the
record → layout (worktree disjoint from the state root, private files and auth HOME) →
ignored files → index flags (any assume-unchanged or skip-worktree entry is
`worker_not_clean`) → Git control capture → full worktree capture, which must equal the
launch commit exactly (below) → instructions → allowed paths → packet assembly →
executable pin and HOME → source matrix and base directories → existing attempt →
exclusive acquisition. Every failure before acquisition changes no state and starts
nothing.

After acquisition the attempt publishes `supplied-inputs.json`, `packet.txt`,
`source-manifest.json` and `intent.json` (all `0600`). Immediately before start it checks
that the state root, `records-v1`/`worker-v1` namespaces, attempt directory and
its `worker`/`worker/tmp` directories are still the same
(device and inode) private `0700` directories you own; if not, nothing more is published,
nothing starts and the command fails with `worker_uncertain`. It then rereads the plan,
task, profile and saved record files, requiring the same file (device and inode), mode and bytes, so a
byte-identical substitute counts as a change, and repeats the instruction, pin, worktree
identity, Git control, source matrix, base directory, clean-status and full worktree
observations. A change publishes a `not_started` `worker_unusable` result, delivers it and
then fails with `input_changed`. The state directories are checked again before the result
is published; a change there leaves the attempt blocked without a result and fails with
`worker_uncertain`. Edits are never rolled back and the attempt is never reused.

### Packet

The packet is stored before the intent and supplied as the child's stdin (never argv). It
contains the fixed assignment line, run/task/base/scope/profile/instruction-manifest
references, every instruction body in order framed with its path, blob ID, SHA-256 and
size, the task, the literal allowed paths and the report schema. It is at most 262,144
bytes including framing; nothing is truncated (`packet_unavailable`). The worker's final
message must be exactly
`{"schema_version":1,"run_id":ID,"task_input_sha256":HASH,"status":S,"reason":R}` with
`completed`/`none`, or `not_completed` with `blocked`, `needs_clarification` or `failed`.

### Launch

The pinned executable runs directly in the worktree with this argv (only the worktree path
varies; the packet is stdin):

```text
--print --safe-mode --restricted --setting-sources "" --permission-mode dontAsk
--permission-prompts none --strict-mcp-config --mcp-config {"mcpServers":{}}
--tools Read,Glob,Grep,Edit,Write
--settings {"permissions":{"allow":["Edit(//W/**)","Write(//W/**)"],"deny":[]}}
--no-chrome --no-session-persistence --output-format stream-json --verbose
--model claude-opus-5-5 --disable-slash-commands --include-hook-events
```

The environment is built from scratch: `HOME` (approved auth HOME), `TMPDIR` (the
attempt's own `worker/tmp`), `PATH=/usr/bin:/bin:/opt/homebrew/bin`, `LANG=C`, `LC_ALL=C`,
`USER`, `LOGNAME`, `SHELL`, `ENABLE_CLAUDEAI_MCP_SERVERS=false`,
`CLAUDE_CODE_DISABLE_BACKGROUND_TASKS=1`, `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` and
`DISABLE_AUTOUPDATER=1`. Nothing else is inherited. There is no native turn ceiling.
Bounds: 300 s wall time, 8 MiB stdout, 1 MiB stderr (counted and discarded), at most
10,000 events of at most 512 KiB each.

### Stream and report checks

Each stdout line must be one strict JSON object (no duplicate keys) classified by the
frozen 2.1.285 table; unknown or stop-classified events (hooks, auth transitions, API
retry, model fallback or refusal, background tasks, notifications and others) stop the
worker through the existing process cleanup. Exactly one `system/init` comes before any
assistant, user or result event, with API key source `none`, version 2.1.285, model
`claude-opus-5-5`, `dontAsk`, exactly the five tools, empty skills and MCP servers,
default output style, an approved duplicate-free plugin subset, no plugin/MCP errors,
empty `slash_commands` and `memory_paths` when present, and `cwd` equal to the worktree
when present. Tool calls must use the five tools with unique IDs; user messages may only
carry results for earlier open calls, each once; no call may be open at the result. A
rate-limit event counts as information only when plainly allowed with no overage. The
terminal result must be `success` with `is_error` false, and its text must be exactly the
report bound to the run ID and task input hash. No event may follow it, and the stream
must end with a newline.

### Change capture

The full worktree capture enumerates the actual filesystem below the worktree, not Git
status: every directory, regular file and symbolic link, including ignored and untracked
entries, except the top-level `.git` entry. Each is observed without following links (type,
mode, owner, device and inode; a file's bytes or a link's target are hashed; a directory
records no size or time). Bounds: at most 4,096 entries (files, directories and links),
1 MiB per file and 16 MiB hashed in total; an overflow, a nested `.git` name, a special
file or unreadable entry fails (`unsafe_execution_layout` at launch) with no truncation.
At launch every tracked path must be present with its committed blob ID, type and
executable bit, and no other file or link may exist (`worker_not_clean`); a submodule
entry is `unsafe_execution_layout`. Larger projects or files are unsupported.

The Git control capture records the symbolic and resolved HEAD, the staged index listing,
each index entry's flags, the Git directories and, by metadata and content hash, the
top-level `.git` entry, `HEAD`, `index`, `shallow`, `config.worktree`, `commondir` and `gitdir` of the
worktree's Git directory, its `info/` and `refs/` trees, and the common directory's `config`, `packed-refs`, `shallow`,
`objects/info/alternates`, `objects/info/http-alternates`, `info/`, `hooks/` and `refs/` trees
(at most 512 entries). Linked worktrees are supported. When present, the index must be a regular file owned by the operator
or root, without group/other write or special permission bits. Its identity and raw
bytes are compared alongside its staged listing and flags: guarded Git commands use
`--no-optional-locks`, so they do not refresh the index on disk. The capture is private and never published. Redirected control files or directories
(symlinks) and local Git `include`/`includeIf` configuration are unsupported; the command
does not resolve those additional configuration sources.

After a usable exit 0 with a verified stream, the attempt rereads every input (same file,
mode and bytes) and repeats the pin, instruction, source-matrix, base directory, HEAD and
Git control observations, then takes the full worktree capture again. Every path outside
`allowed_changes` must be identical to the launch capture (presence, type, mode, owner,
device, inode and content), so an edit hidden from Git status, for example behind an
index flag, and a byte-identical replacement or mode change of an instruction or other
file are differences. Guarded `git status` including ignored files must also show only
worktree changes to allowed paths. An allowed path must afterwards be absent or a regular
file within the same bounds. The private `changes.json` lists each allowed path as
`unchanged`, `modified`, `added`, `deleted` or `absent`, with mode, size and content hash
when present; the result binds its SHA-256. Any other difference, a Git control change or
an overflow makes the attempt `worker_unusable`. Edits are never rolled back.

### Source matrix

The `claude-2.1.285-source-v1` matrix inspects, by metadata only (no contents, no
directory listing), the entry, scoped-settings and custom-source paths for the worktree,
every ancestor and each nested directory holding a tracked instruction or `.claude`
source; the user `~/.claude` instruction/config paths; the alternate `~/.config/claude`
paths; the fresh project auto-memory directory; the native state containers
(`~/.claude`, `~/.claude/projects`, `~/.claude.json`); and the admin-managed root
`/Library/Application Support/ClaudeCode`. At most 256 distinct paths including base directories. Overlapping user/ancestor rows
retain the stricter user-owner rule; required-absent rows remain required absent. A symlink, wrong type or
owner, group/other-writable or special-bit source, unreadable metadata, any alternate or
admin-managed source, or an existing project memory directory fails with
`source_profile_unavailable`. Present safe user sources are recorded, not read. The base
directories the rows hang from (the worktree and every ancestor, each nested directory
with every directory between it and the worktree, and the auth HOME; the admin root or,
when absent, its parent) must themselves be real directories owned by you or root, not
group/other writable and without setuid, setgid or sticky bits. No host path is exempt,
so a worktree under a world-writable sticky directory such as `/tmp` is unsupported.
Before start and after return the rows are compared by presence, type, owner and mode, and
the base directories by owner, mode, device and inode (never modification time); the
native state row may change. `source-manifest.json` records both lists.

This is complete coverage of a finite matrix plus documented suppression (`--safe-mode`,
empty `--setting-sources`, strict empty MCP, disabled slash commands) and exposed native
init fields. It is **not** a complete inventory of what the native client loaded:
account-delivered or non-file policy is not observable, and an absent `memory_paths` is
recorded as `documented_suppression_only`, not as observed absence.

### Results

`result.json` and the JSON output hold `schema_version`, `run_id`, `operation`
(`run_worker`), `authority` and `readiness` (`not_evaluated`), `outcome`, `worker`
(`state` `not_started`/`exited`/`unverified`, `exit_code`), `input_binding` (task input,
instruction manifest, assembled packet, profile and executable SHA-256), `repository`
(object format, before/after HEAD, change manifest SHA-256), `instruction_count`,
`instruction_delivery` (`embedded_full_documents`), `source_observation` (matrix ID,
manifest SHA-256, `complete_enumerated_metadata`,
`documented_mechanisms_and_exposed_native_fields`), `stream_summary` (init/terminal
verified, memory observation, plugin count, information counts by fixed label),
`injection_disposition` (`same_account_no_quiescence_stream_stops_unstreamed_unknown`),
`receipt_state`, `created_at` and `completed_at`. Unknown values are `null` or
`not_evaluated`. No paths, native IDs, raw output, tool data or report text appear in
output or errors. Text output prints only the run, outcome, worker state/exit, input and
source hashes, authority, readiness and receipt. Output is at most 65,536 bytes.

| Outcome | Meaning |
|---|---|
| `worker_completed_unverified` | Every observation above was usable and the report says `completed`. Still not tests, review or acceptance. |
| `worker_not_completed` | Usable observations, but the worker truthfully reported noncompletion. Downstream work stops. |
| `worker_unusable` | Anything else, including not started. |

**Report observation companion.** Only for a `worker_not_completed` result whose
observations were all usable, the attempt directory also holds
`report-observation.json`, a private owner-only file of at most 1,024 bytes. It is a
strict JSON object with exactly six keys: `schema_version` (integer `1`), `run_id`,
`task_input_sha256` (equal to the result's `input_binding.task_input_sha256`),
`result_sha256` (SHA-256 of the exact `result.json` bytes, including the trailing
newline), `status` (`not_completed`) and `reason` (`blocked`, `needs_clarification` or
`failed`). It holds no paths, prompts, tool data, native IDs, free text or raw events.
The reason is the category the **model reported**, not a verified cause. The companion
does not change `result.json`, the JSON or text output, exit codes or error codes, and it
is written the same way with or without `--json`. Completed and unusable attempts never
have one, including an attempt whose report parsed but whose stream, process or a later
check was unusable.

To read it, open it at `worker-v1/<run-id>/report-observation.json` in the state root
under the same rules as other private inputs: an owner-only `0600` regular file, not a
symbolic link, with the same bounded read. Trust the reason only when the companion passes
strict validation and the `result.json` beside it is a valid version 1 result for the same
run with outcome `worker_not_completed` whose exact bytes hash to `result_sha256`. A
missing companion (for example, from an attempt recorded before this file existed) means
the reason is unavailable. It is not a reason to reject an otherwise valid result or to
supply a default. A missing, invalid, mismatched or orphan companion supplies no trusted
claim. Consumers that read only `result.json` can ignore the file. The companion grants no
acceptance, retry, recovery or authority, and `baw` has no command that reads it.

Publication order is `changes.json`, `report-observation.json` (when eligible),
`result.json`, then output. This is not a transaction. If saving the companion fails, the
attempt and worktree edits are kept, no result is published, nothing is delivered and the
command returns `worker_uncertain`. This is the one case where an attempt that would
otherwise deliver `worker_not_completed` is now uncertain. The native worker is never
retried. A later result or delivery failure behaves as before and can leave an orphan
companion, which has no valid meaning without its bound result. Nothing is deleted or
rolled back, and `.pending-` files remain under the existing storage rules.

Cancellation uses the existing observation taken when the native process returns.
Cancellation seen there makes the attempt `worker_unusable` with no companion.
Cancellation after that point does not by itself change the outcome or the companion,
and later checks still have to pass. Once publication starts, already published files are
kept. This is an observation cutoff, not a guarantee that the rest of the run was free of
cancellation.

Errors (`baw: CODE`, exit 1 unless noted): `invalid_usage` (2), `plan_unavailable`,
`invalid_worker_plan`, `worker_checkpoint_mismatch`, `worker_not_clean`,
`unsafe_execution_layout`, `instructions_unavailable`, `packet_unavailable`,
`profile_unavailable`, `source_profile_unavailable`, `worker_exists`,
`worker_storage_unavailable`, `worker_uncertain`, `input_changed`, `worker_cancelled`,
`worker_failed` (a delivered non-completed outcome) and the existing state and inspection
codes.

### Known limits

- Same-account, same-host operation: admitted native file-tool restrictions are not OS
  isolation from other programs running as the operator. Invisible native input that is
  never streamed remains unobserved. Other sessions must not deliberately message the
  worker; no per-task pause is required. Text instructions and file modes do not isolate
  hostile same-account processes.
- Checks are sequential observations, not a filesystem transaction; a concurrent same-UID
  writer can race them. New content hidden behind unchanged source metadata is not
  detected.
- `GroupAbsent` means no signalable owned-group member remained before reaping, not that
  every descendant is gone; escaped descendants are not signalled.
- Large worktrees (over 4,096 entries, or over 16 MiB of file content) and files over
  1 MiB are unsupported. Git objects and reflogs are outside the fixed control capture; this is not a
  complete Git database integrity audit. Included configuration is rejected, and captured
  refs/packed refs must remain unchanged.
- Package tests use fake executables and synthetic streams. They are not evidence of
  native authentication or native behaviour.
