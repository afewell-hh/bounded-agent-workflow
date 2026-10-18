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

Starting state: a fresh assigned worktree with a clean committed HEAD (no staged,
unstaged, untracked **or ignored** files), an existing [run record](run-records.md) whose
HEAD equals that commit, and a private state directory (`0700`). The lead has audited
broader and nested instructions for the task and frozen the profile manifest. For BAW,
that manifest starts with `CLAUDE.md`, `AGENTS.md`, `workflow/protocol.md` and
`workflow/roles/worker.md`, followed by the documents required by the task and applicable
nested instructions. Supply full dependencies explicitly: safe mode does not expand
imports. The lead establishes a different project’s adopted instruction set before use;
the command validates supplied identities, not semantic completeness or adoption.

1. Write three private files outside the worktree, each a current-user `0600` regular file:

   Plan (at most 65,536 bytes):

   ```json
   {"schema_version": 1, "task_file": "/abs/private/task.json", "profile_file": "/abs/private/profile.json"}
   ```

   Task (at most 65,536 bytes; example values):

   ```json
   {
     "schema_version": 1,
     "task_id": "RUN_ID",
     "scope_sha256": "RECORD_SCOPE_SHA256",
     "base_commit": "RECORD_HEAD",
     "task": "Record the marker required by the project rule.",
     "instructions": [
       {"path": "CLAUDE.md", "blob_oid": "BLOB_OID", "sha256": "CONTENT_SHA256"},
       {"path": "AGENTS.md", "blob_oid": "BLOB_OID", "sha256": "CONTENT_SHA256"}
     ],
     "allowed_changes": ["target.txt"]
   }
   ```

   Profile (the coordinator's nonsecret reviewed manifest; synthetic example values):

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

2. Run, in a terminal on the host (shell command):

   ```sh
   baw run worker --repo /abs/worktree --state-dir /abs/state --run-id RUN_ID \
     --plan /abs/private/plan.json --json
   ```

3. Read the outcome. Exit 0 means `worker_completed_unverified`. A delivered other outcome
   exits 1 with `baw: worker_failed`. Inspect the worktree change yourself, then commit
   and verify the candidate under the ticket's gates.

Recovery: on `worker_uncertain`, `input_changed` or any outcome other than
`worker_completed_unverified`, do not rerun the same ID. Preserve the attempt directory
and the worktree (edits are never rolled back) and diagnose. A new attempt needs a new
run record and the ticket's attempt budget.

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
  as real directories; the file itself may be absent or a regular file.
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
ignored files → instructions → allowed paths → packet assembly → executable pin and HOME
→ source matrix → existing attempt → exclusive acquisition. Every failure before
acquisition changes no state and starts nothing.

After acquisition the attempt publishes `supplied-inputs.json`, `packet.txt`,
`source-manifest.json` and `intent.json` (all `0600`). Immediately before start it rereads
the plan, task and profile bytes and repeats the instruction, pin, worktree identity, Git
control (symbolic HEAD, HEAD, staged index listing), source matrix and clean-status
observations. A change publishes a `not_started` `worker_unusable` result, delivers it and
then fails with `input_changed`.

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

After a usable exit 0 with a verified stream, the attempt rereads every input and repeats
the pin, instruction, source-matrix, HEAD and Git control observations, then runs guarded
`git status` including ignored files. Every difference must be a worktree-only change to
an allowed path (no index change, conflict, rename, directory or ignored extra). Each
allowed path is read without following links and hashed: at most 4,096 status entries,
1 MiB per file and 16 MiB in total, with no truncation. The private `changes.json` lists
each allowed path as `unchanged`, `modified`, `added`, `deleted` or `absent`, with mode, size
and content hash when present; the result
binds its SHA-256. Git index or HEAD movement, an instruction or control edit, or any
extra path makes the attempt `worker_unusable`. Edits are never rolled back.

### Source matrix

The `claude-2.1.285-source-v1` matrix inspects, by metadata only (no contents, no
directory listing), the entry, scoped-settings and custom-source paths for the worktree,
every ancestor and each nested directory holding a tracked instruction or `.claude`
source; the user `~/.claude` instruction/config paths; the alternate `~/.config/claude`
paths; the fresh project auto-memory directory; the native state containers
(`~/.claude`, `~/.claude/projects`, `~/.claude.json`); and the admin-managed root
`/Library/Application Support/ClaudeCode`. At most 256 paths. A symlink, wrong type or
owner, group/other-writable or special-bit source, unreadable metadata, any alternate or
admin-managed source, or an existing project memory directory fails with
`source_profile_unavailable`. Present safe user sources are recorded, not read. Before
start and after return the rows are compared by presence, type, owner and mode (never
modification time); the native state row may change.

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
- Large repositories (over 4,096 status entries) and large allowed files are unsupported.
- Package tests use fake executables and synthetic streams. They are not evidence of
  native authentication or native behaviour.
