# Agent lifecycle and replacement

**Status:** adopted for this repository's bounded manual bootstrap under
[decision D1](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557).
Runtime enforcement is a future controller requirement. Native product capabilities are summarized in
[the source register](../research/sources.md). Numeric limits here are pilot heuristics,
not vendor guarantees or experimentally established optima.

## Stable roles, disposable sessions

A role is a responsibility. A session is one temporary conversation/process executing
that responsibility. Tickets, approvals, budgets, candidate identity, and evidence
belong to durable project/run records, not to a session. Replacing a session must not
implicitly reapprove, merge, restart, or expand work.

Distinguish three operations: native compaction summarizes an existing conversation;
resume continues a recorded conversation; replacement starts fresh from verified
external state. A new process or fork is not necessarily a fresh context if it resumes
or copies history. Record the actual session/thread identity where supported.

## Default session lifetimes

| Role | Default |
|---|---|
| Lead | Keep across related tickets; checkpoint at each state boundary. Suggest a refresh after first compaction at the next safe planning boundary; reassess after three completed tickets even without compaction. This is advisory, not an automatic kill. |
| Worker | Fresh session for a new ticket. Same-ticket repair may continue an exact known session only after adapter validation; otherwise reconstruct a fresh worker from code and findings. |
| Reviewer | Fresh independent first pass per frozen candidate; clarification may continue on the identical candidate. |
| Helper | Fresh per bounded question; finish or cancel before parent retirement. |

Replace sooner for explicit context loss, conflicting reconstructions, repeated ignored
constraints, or a substantial unrelated planning task. Preserve a productive worker
through a safe boundary rather than aborting halfway through a file/database operation.
If compaction becomes frequent, first investigate oversized tool output or overly broad
tickets; repeatedly refreshing cannot fix those inputs.

## Planned lead replacement

1. Pause the old lead's ability to dispatch new work. Record current decisions and
   update the coordination issue from actual Git/GitHub/run evidence.
2. Distinguish lead-owned helpers from controller-owned execution. Join/cancel the old
   lead's helpers. A separately running controller-owned worker may continue if its
   ownership/status are known and it has no dependence on the old lead conversation.
3. Record a transition receipt. Retire the old lead's directing assignment. During
   manual bootstrap, stop its process or make it explicitly advisory and prevent two
   directing leads. A future controller uses assignment-generation checks.
4. Start a fresh native agent, not a resume/fork, with the lead role and coordination
   pointer. It reconciles live state before proposing any action.
5. The incoming lead reports its reconstructed objective, accepted decisions, active
   work, budgets, blockers, and next permitted action. Activate it after that receipt
   is consistent. Reapproval is needed only for a real scope/policy change, not for
   bookkeeping adoption of the unchanged approved ticket.

An operator can request replacement at any time. Safe takeover may have to wait for a
known process boundary. This is recoverable role replacement, not a promise of migrating
an arbitrary live agent's private state or a partially completed external side effect.

## Abrupt loss or worker/reviewer replacement

Do not depend on a farewell summary. Reconcile the last durable checkpoint with current
Git state, issue/PR state, run records, and live process ownership. Preserve uncommitted
work explicitly; do not assume every relevant edit was committed or use destructive
Git cleanup. Unrecorded reasoning may need to be repeated. Unknown execution or missing
evidence blocks the affected transition.

Before a replacement writer starts, verify the previous writer and its descendants no
longer own the workspace. A process ID alone can be stale or reused. Do not replay a
possibly completed external write automatically. Mark it uncertain and reconcile it.
Reviewer replacement reads the exact candidate, original criteria, and outstanding
findings; worker replacement inherits the same scope and consumed budget. A fresh
review session cannot approve a different commit using previous evidence.

## Repository and runtime reconciliation: a small normal check, deeper on loss

Every incoming role first checks the actual assigned workspace and a concise recent
history, even at a clean handoff. Read only the relevant delta; do not load the entire
repository history into context. At a clean boundary, confirm the checkpoint commit,
workspace cleanliness, candidate, and known live assignments agree with the records.
After loss, inspect what changed since the last verified checkpoint, including edits
that are neither committed nor documented. Commits show recorded changes, not approval.

Before forwarding inspection output to a model, apply the approved sensitive-output policy.
Even a commit message or filename may contain private material. Operator-only local review
can be necessary when there is no validated safe-output path. For a known trusted checkout,
this is a useful read-only starting set (omit history when there are no commits):

```bash
git --no-optional-locks status --short --branch --untracked-files=normal --ignore-submodules=none
git worktree list --porcelain
git --no-pager log -n 12 --date=iso-strict --format='%h %ad %s' --name-status
git --no-pager diff --no-ext-diff --no-textconv --stat
git --no-pager diff --cached --no-ext-diff --no-textconv --stat
```

Twelve commits are an initial window, not a completeness rule. Compare the recorded
checkpoint with actual HEAD and inspect the relevant full patches/callers/tests. If the
checkpoint is not an ancestor, investigate divergence/rewrite rather than assuming a
simple range. Inspect staged and unstaged patches separately: their intended states can
differ. Enumerate relevant untracked directories/files, not only a directory name.
When an unexplained branch movement, reset, or stash matters, inspect `git reflog` and
`git stash list` without applying or altering anything. Reflog records local reference
movements; it is not a backup of arbitrary uncommitted/unsaved content [S17-S19].

Repeat the relevant inspection in actual linked worktrees and submodules. Git worktree
listing is not a global list of every clone on every host. Use declared workspace/remote
records to locate additional clones. A cached remote-tracking reference may be stale;
do not claim it is live remote state without a current read/fetch. Any fetch must use
the approved network path and does not authorize pull/rebase/reset. For a future machine
parser use stable porcelain and NUL delimiters, not human-formatted filename parsing.

Check declared ignored/local runtime files by safe metadata and approved access; do not
load .env values, credentials, raw database dumps, or large generated trees into prompts
or publish them in GitHub. Unsaved editor buffers, lost files never preserved anywhere,
and external DB/lab state are not recoverable from commit history alone. Mark gaps.

Then reconcile processes/containers, background jobs, deployed revision, databases or
migration state, preview/forwarding, and shared-lab reservations from the
[environment contract](../developer/execution-environments.md). Lost SSH reachability is
**unknown ownership**, not proof that a process died. Do not start a replacement writer
or lab reset while the previous mutator may still operate. Preserve a healthy preview.

Return a concise delta classification: committed and documented; committed but not yet
documented; staged/unstaged/untracked and unverified; external effects verified/uncertain;
and evidence missing. Map changes to the approved scope where possible, flag unrelated
or unattributed work, and preserve attempts. Summaries and commit messages can be wrong;
actual observed changes do not retrospectively grant approval.

Snapshot/preserve the actual relevant work under an approved recovery plan before any
repair. A tracked-file patch alone does not preserve untracked or all binary/local data.
Do not automatically stash, commit, reset, clean, abort a merge, prune a worktree, restore
a lab snapshot, or delete a container/volume merely to manufacture a clean baseline.
Keep preservation artifacts private and outside the source checkout; do not mix secrets
into a convenient bulk archive.

## Reducing SSH-related interruption without depending on it

Where already installed and approved, run remote interactive agents in named tmux sessions
on the execution host; tmux supports detach/reattach across dropped clients [S27]. Check
for an existing live session before launching another. A reconnect may only need a new
port-forward, not a new agent or application. tmux does not survive every host/process
failure and does not provide workflow approval or lab fencing. Use the same recovery
procedure when state is uncertain; never infer success from a detached terminal.

## Bounded native subagents

Begin with read-only exploration, contract inspection, and retained-log analysis. The
pilot cap is two simultaneous helpers, at most four helper starts per approved ticket
or planning phase, and one delegation level. These limits are separate from—but share
the accountability of—the worker's repair/time budget. No replacement resets them.
A different approved budget is allowed when justified before execution.

Each helper record contains parent/run ID, role generation, question, input revision,
allowed actions, time/budget limit, result location, and lifecycle state. Native session
handles are optional provider-specific data, not the portable source of truth.

A helper returns distilled findings with evidence. Do not fork the entire parent
conversation by default when a scoped packet will do. Helpers do not replace cross-model
independent review. Do not enable unrestricted agent teams or a second automatic repair
loop as a side effect of enabling helpers.

Validate the installed provider's spawn, permissions, budget, completion, cancellation,
and resume behavior. Where the required controls cannot be enforced/observed, keep
that capability out of automated execution and use supervised exploration instead.
A capability flag is not sufficient evidence; exercise failure cases in an adapter test.
Late responses for retired assignments are quarantined for inspection, not executed.
No promise of cross-provider live child adoption is made.

## Metrics without a monitoring project

Record, where actually exposed: session identity/version, current context occupancy,
compaction count/duration, completed tickets, helpers, takeover duration, missing facts,
and operator corrections. Cumulative usage tokens are not current context occupancy.
For unsupported telemetry record unavailable, not zero, and do not guess from terminal
appearance. Native events can assist; the manual workflow does not depend on them.

Evaluate refresh policy by human re-explanation time, successful takeover, defects,
false-completion claims, and compaction delay—not simply the number of resets. Do not
retire a lead merely because it crossed an arbitrary token percentage. Revisit the
three-ticket advisory after several comparable tickets.

## Updates versus refresh

A fresh session need not upgrade its CLI. Check for updates at convenient handoffs, but
apply them only when all affected runs using the shared executable are quiescent.
Record exact CLI/plugin/instruction/config versions and configured model selectors.
Native auto-update behavior varies with installation method. Recheck versions before
dispatch; a change mid-run requires an explicit compatibility/recovery decision.

After an update, run harmless native startup/structured-output/instruction-loading and
cancellation checks in a disposable project. Confirm the intended subscription login.
Retain a known-good route for rollback where supported. Do not claim that pinning a CLI
freezes the provider's server-side model behavior.

## Credential continuity during replacement

Follow [secrets and access](../developer/secrets-and-access.md). A transition records only
profile/version aliases, permitted targets, consumer/instance ownership, and availability
or expiry concerns. It never copies values, authenticated URLs, cookies, or dotenv content.
Rebind approved sources without new authority or per-agent re-entry; do not rotate/revoke
credentials or delete a source just because the agent was replaced. Preserve credentials
needed by a held preview under its existing retention rule.

Git/runtime reconciliation must not dump potentially sensitive diffs, process environments,
request logs, auth stores, or ignored `.env` files into the incoming model's context. Use
an approved metadata/sanitization path, including for secrets accidentally placed in
normally tracked code; record withheld/unknown details and request safe operator handling
when needed. Do not claim broad raw-diff capture is safe because `.env` is ignored.
An interrupted CMS write needs remote pre/post-state and job reconciliation, not just Git.
Resume single-account GitHub operation without inventing another reviewer identity or
claiming that a prior model-authored approval was a human decision.

## Replace during integration or after a completed ticket

Carry the adopted completion mode/target and holds into takeover. Routine merge permission
is inherited from the approved scope, not renewed because the lead changed. Inspect actual
PR head/base, merge/queue state, resultant commit/tree, post-merge gates and closure. A
network loss during merge means unknown until queried; never blindly replay the merge or
claim a queued PR has completed. Preserve attempt and recovery lineage.

After verified closeout, the state is `COMPLETED_AWAITING_NEXT_SCOPE`. A new lead may report
that result and propose the next ticket, but cannot infer approval to execute it. Only a
recorded, still-valid [standing delegation](github-single-account.md#standing-program-delegation-opt-in)
lets it select the next ticket; the takeover itself grants nothing, and spent attempt counts,
caps and ownership carry over unchanged. Human
release/production/design decisions are only present when explicitly recorded; do not
invent a human acceptance that ordinary closeout intentionally did not require. Use
[the closeout policy](github-single-account.md).
