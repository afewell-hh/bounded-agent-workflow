# Inspect a repository with `baw inspect`

`baw inspect` prints a read-only context packet for a lead: Git state counts, the
fixed maintained onboarding documents, and optional coordination-issue metadata. It
reports observations and unknowns. It does not approve work, select a ticket, activate a
role, reconcile authority, or recover lost work.

## Usage

```sh
baw inspect --repo PATH [--checkpoint FULL_COMMIT_SHA] [--coordination-file FILE | --github OWNER/REPO#NUMBER] [--json]
baw --help
```

- `--repo` (required): a Git worktree root or any subdirectory. A symlinked input path
  (such as macOS `/tmp`) is accepted. Bare repositories and non-repositories fail.
- `--checkpoint`: a full 40-hex (SHA-1) or 64-hex (SHA-256) commit ID matching the
  repository's object format. Abbreviations, revision expressions and wrong widths are
  usage errors.
- `--coordination-file`: a local JSON snapshot in GitHub issue API shape, at most 1 MiB.
  It is reported as `snapshot`, never as current live state.
- `--github`: runs exactly one `gh api --method GET --hostname github.com
  repos/OWNER/REPO/issues/NUMBER` through your existing `gh` login (30-second limit, no
  retry). Without this flag `gh` is never resolved or run.
- `--json`: schema version 1, one JSON object followed by a newline.

## Example terminal report

```text
BAW inspection
HEAD: 5500558ea1cbd26694f90801ecebc2f5de451ba4
Branch state: attached
Object format: sha1
Changes: staged=1 unstaged=1 untracked=1 conflicted=0
Linked worktrees: 0
Submodules: 0
Checkpoint: not_requested
Source: AGENTS.md presence=present worktree_state=clean head_ref=git:5500558ea1cbd26694f90801ecebc2f5de451ba4:AGENTS.md worktree_ref=worktree:AGENTS.md
...one Source line for each of the eight maintained files...
Coordination: none
Remote freshness: unknown
Runtime state: unknown
Process ownership: unknown
Reservation ownership: unknown
```

The eight maintained files, in order: `AGENTS.md`, `workflow/protocol.md`,
`workflow/roles/lead.md`, `README.md`, `docs/design/product-contract.md`,
`docs/architecture/overview.md`, `docs/developer/environment.md`,
`docs/operator/agent-lifecycle.md`. `head_ref`/`worktree_ref` are references for you to
read separately; `-` means none. Document bodies are never printed.

## Meaning of the fields

- `Branch state` is `attached`, `detached` or `unborn`. Branch names are never printed.
- `staged`/`unstaged` count status entries with a change in that column; one file with
  different staged and unstaged versions counts in both. `untracked` counts individual
  files. `conflicted` counts unmerged entries only. `Submodules` counts gitlinks in the
  index; submodule contents are not inspected. `Linked worktrees` excludes the one inspected.
- Counts deliberately replace filenames, diffs and commit messages, which can contain
  private material. **Counts do not replace detailed recovery**: inspect the work itself
  before acting on it.
- `Remote freshness`, `Runtime state`, `Process ownership` and `Reservation ownership`
  are always `unknown` in this version. No remote is contacted and no processes are
  examined.
- Inspection is a sequence of reads, not an atomic snapshot. If the repository is being
  edited concurrently, rerun it.

## Failures

Exit 1 prints nothing on stdout and exactly `baw: CODE` on stderr. Exit 2 is
`baw: invalid_usage`. No partial packet is printed. Codes:

| Code | Meaning |
|---|---|
| `repository_unavailable` | Path missing, not a worktree, or a bare repository |
| `unsupported_filters` | Repository config names a clean/process filter driver; inspection stops before status |
| `unsupported_partial_clone` | Partial-clone/promisor config present; stops before any object read that could fetch |
| `git_config_invalid` | Repository configuration could not be read |
| `git_failed`, `invalid_git_output` | A Git read failed or returned unexpected output |
| `checkpoint_missing` | Checkpoint is not a commit in this repository |
| `checkpoint_diverged` | Checkpoint is not an ancestor of HEAD |
| `checkpoint_unborn` | Checkpoint requested but HEAD has no commit |
| `source_symlink` | A maintained-source path component is a symlink (not followed) |
| `source_unavailable` | A maintained source exists but is not a readable regular file |
| `coordination_unavailable` | Snapshot unreadable or `gh` failed (provider text is withheld) |
| `coordination_invalid` | Snapshot/provider JSON malformed, mismatched, or not an issue |
| `input_limit` | Snapshot over 1 MiB |
| `command_timeout`, `command_output_limit` | A Git/gh child exceeded its time or output limit |
| `output_limit` | Final report over 32 KiB |

With several problems, only the first in this order is reported: usage/path, Git safety
and state, checkpoint, sources, coordination, output.

After `checkpoint_diverged`, rerun without `--checkpoint` to obtain current
observations. That does not reconcile the divergence or approve any action.

## Safety properties and limits

- Read-only Git commands only, run without a shell, with optional locks disabled, no
  pager, hooks, fsmonitor, external diff, textconv, signature checks or submodule
  recursion. Your global/system Git configuration and user-level ignore file are not
  read, so untracked counts can include files that only your personal ignore file hides,
  and results may differ from your own `git status`. Repository `.gitignore` and
  `.git/info/exclude` still apply.
- Inherited `GIT_*` and `GH_*`/token variables are not passed to children.
- Limits: 10 s per Git command, 30 s for `gh`, 120 s overall; 8 MiB Git output per
  command, 1 MiB `gh` output, 64 KiB child stderr (never printed). Each child runs in its
  own process group; on timeout or output overflow that group is terminated and joined.
- The `git` and `gh` found on `PATH` are trusted. This is not a sandbox against a
  same-user adversary. Only darwin/arm64 has been exercised.
- Git 2.39 does not support `GIT_NO_LAZY_FETCH`; the inspector sets it only as defense
  in depth. Refusing partial-clone/promisor repositories is the actual guard.
