# Role onboarding references with `baw context`

`baw context` prints a small, read-only manifest for a fresh lead, worker or reviewer
agent: which project documents to read, in which order, and where each one is (as a
committed Git reference and/or a worktree file), together with the same Git observations
as [`baw inspect`](inspect.md). It never prints document contents, starts an agent, picks
a ticket or decides anything. It is a reading list with current observations, not a
complete prompt and not an assignment.

## Usage

```sh
baw context --repo PATH --role ROLE [--json]
baw context --help
```

- `--repo` (required): a Git worktree root or any subdirectory of it; a symlinked path is
  accepted. Bare repositories and non-repositories fail.
- `--role` (required): exactly `lead`, `worker` or `reviewer`, lowercase. Anything else,
  including `helper`, is `invalid_usage`.
- `--json`: one JSON object followed by a newline.
- Options accept `--flag value` or `--flag=value`, in any order. Missing, empty,
  repeated or unknown options, extra arguments and `--json=...` are `invalid_usage`. The
  only help form is a lone `baw context --help`; `-h`, `help` or `--help` mixed with other
  arguments are `invalid_usage`. There are no checkpoint, coordination, state or ticket
  options.
- Syntax and role are fully checked before any file or Git access.

## How to: hand a role its reading list and follow the references

This example uses a disposable dummy repository; nothing else is touched. It runs in a
shell on the machine that holds the checkout.

```sh
WORK=$(mktemp -d)                          # disposable; remove it yourself afterwards
git init -q "$WORK/repo"
mkdir -p "$WORK/repo/workflow/roles" "$WORK/repo/docs"
printf 'dummy\n' > "$WORK/repo/AGENTS.md"
printf 'dummy\n' > "$WORK/repo/workflow/protocol.md"
printf 'dummy\n' > "$WORK/repo/workflow/roles/worker.md"
git -C "$WORK/repo" add -A
git -C "$WORK/repo" -c user.name=Dummy -c user.email=dummy@example.invalid commit -q -m dummy
git -C "$WORK/repo" rev-parse HEAD        # note the HEAD; shown as <HEAD> below
baw context --repo "$WORK/repo/docs" --role worker
```

`--repo` deliberately names a subdirectory: the report is the same as for the top level.
With your HEAD in place of `<HEAD>` (and a 64-digit one if your Git creates SHA-256
repositories), the output is:

```text
BAW context references
Role: worker
Assignment: unassigned
Authority: not_evaluated
Readiness: not_evaluated
Snapshot: non_atomic
Read first: AGENTS.md
Read next: workflow/protocol.md
Read role: workflow/roles/worker.md
BAW inspection
HEAD: <HEAD>
Branch state: attached
Object format: sha1
Changes: staged=0 unstaged=0 untracked=0 conflicted=0
Linked worktrees: 0
Submodules: 0
Checkpoint: not_requested
Source: AGENTS.md presence=present worktree_state=clean head_ref=git:<HEAD>:AGENTS.md worktree_ref=worktree:AGENTS.md
Source: workflow/protocol.md presence=present worktree_state=clean head_ref=git:<HEAD>:workflow/protocol.md worktree_ref=worktree:workflow/protocol.md
Source: workflow/roles/worker.md presence=present worktree_state=clean head_ref=git:<HEAD>:workflow/roles/worker.md worktree_ref=worktree:workflow/roles/worker.md
Source: README.md presence=absent worktree_state=absent head_ref=- worktree_ref=-
Source: docs/design/product-contract.md presence=absent worktree_state=absent head_ref=- worktree_ref=-
Source: docs/architecture/overview.md presence=absent worktree_state=absent head_ref=- worktree_ref=-
Source: docs/developer/environment.md presence=absent worktree_state=absent head_ref=- worktree_ref=-
Source: docs/operator/agent-lifecycle.md presence=absent worktree_state=absent head_ref=- worktree_ref=-
Coordination: none
Remote freshness: unknown
Runtime state: unknown
Process ownership: unknown
Reservation ownership: unknown
```

The five missing documents are observations, not errors: the command exits 0.

Every path in the report is relative to the **top level of the Git worktree containing
`--repo`**, never to the `--repo` argument. The command does not print that top-level
path, so resolve it yourself and read the references from there:

```sh
TOP=$(git -C "$WORK/repo/docs" rev-parse --show-toplevel)
cat "$TOP/workflow/roles/worker.md"                              # worktree:PATH -> current file
git -C "$TOP" cat-file -p <HEAD>:workflow/roles/worker.md       # git:OID:PATH -> committed blob
```

Give the agent the three `Read ...` documents first, then the remaining sources it needs,
together with its actual assignment and run evidence from where those are recorded. This
command supplies none of those.

## Reference

### JSON packet

Exactly nine top-level fields:

| Field | Value |
|---|---|
| `schema_version` | integer `1` (context packet format) |
| `operation` | `"context"` |
| `role` | the validated `--role` |
| `assignment` | always `"unassigned"` |
| `authority` | always `"not_evaluated"` |
| `readiness` | always `"not_evaluated"` |
| `snapshot` | always `"non_atomic"` |
| `reading_order` | `["AGENTS.md", "workflow/protocol.md", "workflow/roles/ROLE.md"]`, paths relative to the worktree top level |
| `inspection` | an [inspect schema-v1 packet](inspect.md) with exactly the nine inspect keys, no checkpoint (`"not_requested"`, `null`, `null`) and no coordination (`"none"`, `"absent"`, four `null`s) |

`inspection.status` `"complete"` means the inspection finished, not that the project or
agent is ready. Field order inside objects is not significant; array order is.

### Terminal report

The nine fixed lines shown in the example (only `ROLE` varies), immediately followed by
the unchanged [inspect terminal report](inspect.md#example-terminal-report) for the
context sources.

### Sources

Exactly eight, in this order, with only the selected role file. Every `path` and the
path part of each `head_ref`/`worktree_ref` is relative to the top level of the Git
worktree containing `--repo`, which is never printed:

1. `AGENTS.md`
2. `workflow/protocol.md`
3. `workflow/roles/ROLE.md`
4. `README.md`
5. `docs/design/product-contract.md`
6. `docs/architecture/overview.md`
7. `docs/developer/environment.md`
8. `docs/operator/agent-lifecycle.md`

Each has the [inspect source fields](inspect.md#example-terminal-report) with the same
meaning: `presence` `present|absent`; `worktree_state`
`clean|modified|untracked|deleted|absent|unknown`; `head_ref` `git:FULL_HEAD_OID:PATH`
only when HEAD records a regular file there; `worktree_ref` `worktree:PATH` only when a
regular file is currently there; `-`/`null` otherwise. Other files under
`workflow/roles/` are not examined as sources, so an unusual unselected role file
neither appears nor fails the report. Links inside the documents are not followed. The
repository-wide Git guards and counts still cover every file.

A committed reference identifies content, not accepted policy; a worktree reference is a
mutable location, not frozen content.

### Failures

Exit 1 prints nothing on stdout and `baw: CODE` on stderr (best effort; a failing stderr
keeps the exit status). Exit 2 is `baw: invalid_usage`. No partial packet is printed.
Repository, Git, source and limit failures use [inspect's codes and order](inspect.md#failures),
for example `repository_unavailable`, `unsupported_filters`, `unsupported_partial_clone`,
`source_symlink` (a context source or one of its directories is a symlink) and
`source_unavailable` (a context source is a directory, FIFO or other non-regular file).
Context adds:

| Code | Meaning |
|---|---|
| `output_limit` | The packet or help would exceed 65,536 bytes (checked before writing) |
| `output_unavailable` | stdout did not accept every byte; bytes already written stay written |

### Limits and boundaries

- The same read-only Git commands, cleaned environment (inherited `GIT_*` removed, no
  system/global configuration or user ignore file), filter/partial-clone guards and
  process limits as [`baw inspect`](inspect.md#safety-properties-and-limits): 10 s per
  Git command, 120 s overall, 8 MiB Git stdout, 64 KiB child stderr. `gh` is never run.
- Output, including help, is capped at 65,536 bytes; inspect's 32 KiB report cap does not
  apply to the context packet.
- Observations are sequential, not an atomic snapshot (`Snapshot: non_atomic`). A
  document can change after it is reported. Source checks assume trusted directories and
  have no hard filesystem time limit.
- No branch names, file contents, messages, configuration values, repository paths or
  OS errors are printed.

## Why it works this way

- **No authority.** `assignment`, `authority` and `readiness` are fixed. Choosing a role
  does not assign it or change any policy: the work an agent may do comes from the
  approved ticket and run records described in [the agent lifecycle](agent-lifecycle.md),
  which this command does not read. A clean, complete packet does not mean a project is
  adopted or ready.
- **No helper role.** Helpers receive a bounded question and inputs from the parent role
  that assigns them, as described in [the protocol](../../workflow/protocol.md#replacement-and-helpers);
  a standalone helper reading list would bypass that scoping.
- **References, not contents.** Documents can contain private or untrusted text and can
  change. Printing locations and Git references keeps the packet small, avoids copying
  prose into another context, and lets the reader choose committed or current content.
- **Relation to inspect.** `baw inspect` keeps its fixed lead source list and output
  unchanged; `baw context` runs the same inspection with the selected role's list. A
  complete prompt or native agent launcher would be a separate feature.
