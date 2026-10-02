# Save and read run records with `baw run create` and `baw status`

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
migration or automatic ID allocation.

## Usage

```
baw run create --state-dir DIR --run-id ID --repo PATH --ticket URL --scope-sha256 HASH --policy-commit OID [--json]
baw status --state-dir DIR --run-id ID [--json]
baw run --help | baw run create --help | baw status --help
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
  `--help` (not `-h`/`help`) is accepted after `run`, `run create` and `status`.
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

Two concurrent creates of one ID produce one success and one `record_exists`, never a
mixed record. The design assumes trusted ancestor directories and no hostile same-user
changes to the namespace while BAW runs; symlink and permission checks prevent mistakes,
they are not isolation.
