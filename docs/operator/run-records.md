# Save, read and diagnose run records with `baw run create`, `baw status` and `baw run diagnose`

`baw run create` saves one immutable local record of caller-supplied references (a
ticket URL, a scope SHA-256 and a policy commit) plus the committed HEAD observed in a
repository. `baw status` prints that **saved** record again. It runs no Git or `gh`,
checks nothing live and works even after the repository is gone.

A record is not an approval, a fetched ticket, proof that the policy commit exists or is
accepted, proof of completed work, or a claim about any running process. Every packet says
so: `authority` is `not_evaluated`, and remote freshness, runtime state, process ownership
and reservation ownership are `unknown`. Anyone with write access to the state directory can
edit a record; strict validation detects structural corruption, not authorship.

This is a storage foundation for a future controller. There is no listing, update, cleanup,
migration or automatic ID allocation. [`baw run diagnose`](#diagnose-saved-state-with-baw-run-diagnose)
counts what is saved for one ID, read-only, and recommends nothing.

## Usage

```
baw run create --state-dir DIR --run-id ID --repo PATH --ticket URL --scope-sha256 HASH --policy-commit OID [--json]
baw status --state-dir DIR --run-id ID [--json]
baw run diagnose --state-dir DIR --run-id ID [--json]
baw run --help | baw run create --help | baw status --help | baw run diagnose --help
```

- `DIR` must already exist, be a directory owned by you with mode `0700` and no
  setuid/setgid/sticky bits. BAW never creates or `chmod`s it, and there is no default
  or environment-selected location. A relative `DIR` is made absolute and lexically cleaned
  (trailing `/`, `.` and `..` components) without following symlinks; if the final
  component is a symlink it is refused. Ordinary ancestor aliases such as macOS
  `/tmp` → `/private/tmp` are then resolved. Keep `DIR` outside any checkout: a `DIR`
  inside a repository adds untracked files there.
- `ID` is exactly 32 lowercase hexadecimal characters chosen by the caller.
- `URL` is exactly `https://github.com/OWNER/REPO/issues/NUMBER` with no query,
  fragment, user, port or trailing slash. It is never fetched.
- `HASH` is 64 hexadecimal characters; `OID` is 40 or 64. Uppercase input is stored
  lowercase. `OID` must have the width of the repository's object format but is otherwise
  only a reference: BAW does not look it up.
- Options accept `--flag value` or `--flag=value`. Unknown, repeated, missing or empty
  options, extra arguments and `--json=...` are `invalid_usage`. `--help` combined with any
  other option is `invalid_usage`. The new help forms print the global help; only
  `--help` (not `-h`/`help`) is accepted after `run`, `run create`, `status` and
  `run diagnose`.
- Help, record and failure output never contain paths, repository text or OS error text.

## What create observes

Create runs the full [`baw inspect`](inspect.md) inspection of `--repo` with its default
limits (no checkpoint or coordination input) and keeps only the object format and HEAD.
Every inspect guard and fixed error applies first, so a repository that inspect refuses
(for example configured filters, partial clones, symlinked maintained sources or exceeded
limits) cannot be recorded even if it has commits. A dirty worktree, a subdirectory or a
symlinked path to the repository is fine. After a successful inspection an unborn HEAD is
`repository_unborn`, and only then is a policy OID of the other width `invalid_usage`.

## Record file

The only committed record is `DIR/records-v1/ID.json`, mode `0600`, in a namespace
directory of mode `0700`. It is one UTF-8 JSON object (optional surrounding whitespace,
at most 16384 bytes) with exactly these nine fields:

| Field | Value |
|---|---|
| `schema_version` | integer `1` (record format, unrelated to inspect's schema) |
| `run_id` | the file's `ID` |
| `record_state` | `"recorded"` — the only state |
| `ticket_url` | supplied canonical issue URL |
| `scope_sha256` | supplied 64 lowercase hex |
| `policy_commit` | supplied lowercase 40/64 hex, same width as the HEAD |
| `repository_object_format` | `"sha1"` or `"sha256"` |
| `repository_head` | observed committed HEAD, lowercase, matching width |
| `created_at` | UTC `YYYY-MM-DDTHH:MM:SSZ` |

`baw status` reads at most 16385 bytes and rejects, in order: more than 16384 bytes
(`record_too_large`); invalid UTF-8, anything other than one complete object, duplicate
top-level names (including escaped spellings) or trailing data (`invalid_record`); a
missing or non-literal positive integer `schema_version` such as `"1"`, `1.0`, `1e0` or
`null` (`invalid_record`); any other positive integer version (`unsupported_record_version`,
before the remaining fields are checked); then any missing, extra, case-variant, null or
wrongly typed field, an ID mismatch, uppercase hex, mismatched widths, or a fractional,
offset or impossible timestamp (`invalid_record`). Unknown versions are never migrated.

### Retained staging name

Create writes the record to `records-v1/.pending-ID-RANDOM` and publishes it by creating a
hard link named `ID.json`, which never replaces an existing name. The staging name is
**deliberately kept** in every outcome, so a successful record has two names for the same
file (link count normally 2). Status ignores staging names and accepts that link count.
Editing either name edits the same file. Nothing is ever cleaned up automatically.

## Example from an empty private root

This uses a disposable directory and a dummy repository; nothing else is touched.

```sh
WORK=$(mktemp -d)                         # disposable; remove it yourself afterwards
mkdir -m 700 "$WORK/state"                # new private state root
git init -q "$WORK/repo"
git -C "$WORK/repo" -c user.name=Dummy -c user.email=dummy@example.invalid \
  commit -q --allow-empty -m dummy
git -C "$WORK/repo" rev-parse HEAD        # note the HEAD it should record
baw run create --state-dir "$WORK/state" --run-id 00112233445566778899aabbccddeeff \
  --repo "$WORK/repo" --ticket https://github.com/OWNER/REPO/issues/1 \
  --scope-sha256 6464646464646464646464646464646464646464646464646464646464646464 \
  --policy-commit 5555555555555555555555555555555555555555
baw status --state-dir "$WORK/state" --run-id 00112233445566778899aabbccddeeff --json
```

(Replace `OWNER/REPO` with any valid names; the URL is not contacted. Use a 64-digit
policy reference if your Git creates SHA-256 repositories.) The create report, with your
HEAD and time:

```
BAW run record
Operation: create
Run: 00112233445566778899aabbccddeeff
Record state: recorded
Ticket: https://github.com/OWNER/REPO/issues/1
Scope SHA-256: 6464646464646464646464646464646464646464646464646464646464646464
Policy reference: 5555555555555555555555555555555555555555
Recorded HEAD: <the HEAD printed above>
Object format: sha1
Created at: <UTC time>
Authority: not_evaluated
Remote freshness: unknown
Runtime state: unknown
Process ownership: unknown
Reservation ownership: unknown
```

`--json` prints one object and a newline:
`{"schema_version":1,"operation":"create|status","record":{...nine fields...},"authority":"not_evaluated","remote_freshness":"unknown","runtime_state":"unknown","process_ownership":"unknown","reservation_ownership":"unknown"}`.
Status repeats the same data each time. Running the same create again prints
`baw: record_exists`; a record is never overwritten, even with identical inputs.

## Failures

Failure stdout is empty, stderr is one line `baw: CODE`. `invalid_usage` exits 2;
everything else exits 1. Inspection failures keep [inspect's codes](inspect.md#failures).

| Code | Meaning |
|---|---|
| `invalid_usage` | Bad syntax or value (checked before any filesystem or Git work), or policy width differs from the repository (checked after inspection) |
| `repository_unborn` | The inspected repository has no commit |
| `state_unavailable` | `DIR` missing, unreadable or not a directory; namespace inaccessible or could not be created |
| `state_permissions` | `DIR`, namespace or record not yours, wrong mode or special bits |
| `unsafe_state_path` | `DIR`, namespace or record is a symlink, or namespace/record has the wrong type (FIFO, socket, directory) |
| `record_exists` | A safe record with that ID exists (create); it is not read or changed |
| `record_missing` | No namespace or no record with that ID (status) |
| `record_unavailable` | Record read failed, or a pre-publication staging/link step failed |
| `record_too_large` | Stored record exceeds 16384 bytes |
| `unsupported_record_version` | Well-formed `schema_version` other than 1 |
| `invalid_record` | Stored data fails strict validation |
| `durability_unavailable` | A root or staging-file sync failed before publication |
| `commit_uncertain` | The record was published, but a later namespace sync, close or output write failed; it is kept |
| `output_unavailable` | Status or new help output could not be fully written |
| `output_limit` | Rendered output would exceed 16384 bytes (checked before publication) |

Diagnose has its own [failure list](#diagnose-failures).

After `commit_uncertain`, run `baw status` with the same `--state-dir` and `--run-id` to
see whether the record reads back. Keep the record and its staging name, and do not edit
them. Do not repeat create blindly: the same ID only reports `record_exists`, and a new ID
would create a second record for the same work.

Order of checks for create: syntax; `DIR` and (if present) namespace safety, with no
directory created; full inspection, unborn HEAD, policy width; existing ID. Any of these
failing leaves a `DIR` without a namespace byte-for-byte unchanged. Only then is
`records-v1` created (or an existing one revalidated), `DIR` synced, the staging file
written, synced and closed, the hard link created, the namespace synced, directories closed
and output written. Status never creates or modifies anything.

## Durability and limits

"Sync" is Go's `os.File.Sync` on the root directory, the staging file and the namespace
directory. On macOS Go issues `F_FULLFSYNC` and falls back to `fsync` when that is
unsupported; BAW adds no other fallback and does not report which one ran. A returned
sync error fails at its stage. This was checked on the developer's macOS APFS volume
only; no other platform or filesystem has been tested, and no drive-cache or power-loss
guarantee is claimed. Tests cover process interruption and injected I/O errors: an
interruption before publication leaves no record (`record_missing`) and possibly a
staging file; after publication the record is complete and valid.

If stdout fails part-way, bytes already written cannot be withdrawn: create then reports
`commit_uncertain` (the record exists) and status `output_unavailable`. A failing stderr
may hide the code; the exit status is still set.

When storage operations succeed, two concurrent creates of one ID produce one success
and one `record_exists`, never a mixed record. The design assumes trusted ancestor directories and no hostile same-user
changes to the namespace while BAW runs; symlink and permission checks prevent mistakes,
they are not isolation.

## Diagnose saved state with `baw run diagnose`

`baw run diagnose --state-dir DIR --run-id ID [--json]` reports the structure of what is
saved for one ID: whether the namespace exists, the final record's state and counts of
its matching staging files. It reads only; it creates, repairs, moves, deletes, retries
and executes nothing (no Git or `gh`), and prints no advice. `DIR` and `ID` follow the same
rules as status. A completed diagnosis exits 0 even when data is missing or malformed:
that means the observation finished, not that anything is recoverable or healthy.

What it reads, in order, after the same `DIR` and namespace checks as status:

1. If `records-v1` was absent at that check, it stops: `Namespace: absent`, final
   `missing`, all counts 0. Nothing else is looked up, and a namespace created a moment
   later does not change that report. Absent does not prove it never existed.
2. Otherwise it lists the namespace's direct names, without recursion. The namespace may
   hold at most **1024 names of any kind**, and at most **32** staging names
   `.pending-ID-RANDOM` for this ID (`RANDOM` exactly 32 lowercase hex); more is
   `state_scan_limit`. Every other name, including malformed staging names and other IDs'
   files, is counted toward 1024 but never opened, statted or printed.
3. It looks up `ID.json` by name, even if the listing did not show it.
4. It reads each matching staging file in name order.

Each file read must be a regular file you own with mode `0600`, exactly as status
requires; it is checked by name, opened without following links or blocking, checked again
through the open file and compared with the earlier check. Each is read up to 16385 bytes
and judged with status's [record validation](#record-file).

| Final record | Meaning |
|---|---|
| `missing` | No `ID.json` (or no namespace) |
| `valid` | Passes status's strict validation for this ID |
| `invalid_record`, `record_too_large`, `unsupported_record_version` | The status error it would give |

Staging counts use the same categories: `total` = `valid` + `invalid_record` +
`record_too_large` + `unsupported_record_version`. `linked_to_final` counts staging files
that are the **same file** (device and inode) as a valid final record, as a successful
create leaves them. It overlaps the other counts, is never added to `total`, and is 0
whenever the final record is not valid. An identical copy that is a separate file is not
linked. A valid staging file without a final record is shown as such, not labeled
abandoned or interrupted.

### Example

After the [create example](#example-from-an-empty-private-root) above:

```sh
baw run diagnose --state-dir "$WORK/state" --run-id 00112233445566778899aabbccddeeff
```

```
BAW run diagnosis
Run: 00112233445566778899aabbccddeeff
Namespace: present
Final record: valid
Staging: total=1 valid=1 invalid_record=0 record_too_large=0 unsupported_record_version=0 linked_to_final=1
Snapshot: non_atomic
Durability: unknown
Authority: not_evaluated
Runtime state: unknown
Process ownership: unknown
Remote freshness: unknown
Reservation ownership: unknown
```

`--json` prints one object and a newline with exactly these keys:
`{"schema_version":1,"operation":"diagnose","run_id":"00112233445566778899aabbccddeeff","namespace":"present","final_record":"valid","staging":{"total":1,"valid":1,"invalid_record":0,"record_too_large":0,"unsupported_record_version":0,"linked_to_final":1},"snapshot":"non_atomic","durability":"unknown","authority":"not_evaluated","runtime_state":"unknown","process_ownership":"unknown","remote_freshness":"unknown","reservation_ownership":"unknown"}`.
`schema_version` is the diagnosis format, not the record format. Output never contains
stored ticket/scope/policy/HEAD/time values, file names, staging suffixes, paths, bytes,
inode numbers or OS errors, and is at most 4096 bytes.

### What the fixed fields mean

- `Snapshot: non_atomic`: files are read one after another with no lock. If anything
  writes to the namespace meanwhile (for example a concurrent create), the report can
  mix moments. Rewriting a file in place between reads, or replacing one before it is
  first checked, is not detected.
- `Durability: unknown`: a valid staging file does not show its sync completed; a valid
  final record does not show the namespace sync or output delivery succeeded.
- `Authority: not_evaluated` and `unknown` runtime state, process ownership, remote
  freshness and reservation ownership: diagnose cannot tell whether a process is still
  running, crashed or was approved, and decides nothing about what to do next.
- Read-only means BAW makes no change; the operating system may still update access times.

### Diagnose failures

These exit 1 with empty stdout and `baw: CODE` on stderr (best effort); syntax errors are
`invalid_usage`, exit 2, before any filesystem access. A failure never prints a partial
report.

| Code | Meaning |
|---|---|
| `state_unavailable`, `state_permissions`, `unsafe_state_path` | As for status, for `DIR` and the namespace; also when listing the namespace fails (`state_unavailable`) |
| `unsafe_state_path` | The final or a matching staging name is a symlink, FIFO, socket, directory or other non-regular file |
| `state_permissions` | It is not yours, not mode `0600`, or has setuid/setgid/sticky bits |
| `record_unavailable` | Checking, opening, reading or closing it failed |
| `state_changed` | A listed staging name was gone when checked, or a name vanished, became a symlink or became a different file between its check and its open |
| `state_scan_limit` | More than 1024 namespace names, or more than 32 staging names for this ID |
| `output_limit` | The report would exceed 4096 bytes |
| `output_unavailable` | Report or `run diagnose --help` output could not be fully written; bytes already written stay written |

The type and owner/mode checks come first, so a file swapped for an unsafe one reports the
safety code even though it also changed. A final record that is simply absent is
`missing`; a listed staging name that is absent is `state_changed`. `state_changed` only
records that the namespace changed between two of these sequential observations; nothing
is retried automatically and no next action is implied.

### Limits and choices

- **The 1024-name cap.** Each successful create adds two names (`ID.json` and its
  staging link), so 512 successful creates fill the namespace exactly; a 513th, or
  extra staging/foreign names, makes diagnose refuse with `state_scan_limit` for **every**
  ID in that `DIR`. Create and status are not affected. To keep diagnosis usable, choose
  another new private `0700` `DIR` for future records. Keep the old one and use status
  there; nothing is moved or deleted automatically.
- **Unusual umask.** Create removes umask reductions on new staging files only after
  creating them. If an interruption happens in between under a umask that removes owner
  bits, a staging file can have the wrong mode; diagnose reports `state_permissions` and
  does not repair it.
- Ownership is checked against the current user only; no other platform than this
  project's developer macOS host has been exercised.
