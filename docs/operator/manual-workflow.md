# Manual workflow: bootstrap the controller without depending on it

**Available now:** the documents and templates in this seed; your already installed
native agent/Git/GitHub tools. **Not available:** any `baw` executable or automatic gate.
The operator still relays worker/reviewer messages until a validated controller exists.
The adopted lead/coordinator performs mechanical closeout checks; routine ticket approval
includes conditional merge and closure, not a second human merge ceremony. Cancellation,
candidate-freezing and budgets remain supervised procedures rather than implemented gates.

## 1. Establish the repository

**Use `bounded-agent-workflow` as the repository name.** This is the repository for the
complete BAW tooling product: workflow assets, documentation, and the future Go controller
and CLI. BAW means Bounded Agent Workflow; `baw` is the planned executable name.

| Item | Name/location |
|---|---|
| Downloaded package | `bounded-agent-workflow.zip` |
| Extracted local directory | `bounded-agent-workflow/` |
| GitHub repository name | `bounded-agent-workflow` |
| GitHub owner | Your chosen account or organization; not preconfigured |
| This runbook inside the extracted folder | `docs/operator/manual-workflow.md` |

The archive contains the full current seed. **No earlier package or separate Markdown
file is required.** Extract it into a parent directory on the host where you will develop;
the archive itself creates the correctly named top-level folder. Do not nest another
`bounded-agent-workflow` directory inside it.

This is the **tooling product** bootstrap. Do not overlay an existing application or
copy a previous controller into it. For an existing or ordinary new application, use
[project adoption](project-adoption.md), not the repository initialization below.
Preserve existing CLI installations and authentication. Review the files and proposed ADRs.

### Initialize the new local repository

Open a terminal **inside the extracted `bounded-agent-workflow` directory**. Confirm that
`README.md`, `START-HERE.md`, `AGENTS.md`, `docs/`, and `workflow/` are directly inside it.
If you already initialized this repository or have edits in an earlier copy, skip new
initialization and have the lead reconcile the existing state; do not delete or overwrite it.

Before staging, inspect the selected files locally for unintended credentials or private
material. The seed contains no credentials; do not add your `.env` or auth stores to it.
Ignore rules do not untrack an already committed secret. For the new, uninitialized seed:

```bash
git init -b main
git status --short
git add -- README.md START-HERE.md AGENTS.md CLAUDE.md CONTRIBUTING.md .gitignore .github docs workflow
git diff --cached --stat
git commit -m "Add workflow bootstrap seed"
```

If Git requests an author name/email, configure your actual identity normally; do not
invent another identity to satisfy an approval workflow. Committing the seed does not
accept its proposed ADRs or authorize implementation.

### Create the GitHub repository only if needed

After reviewing what will be uploaded, check the authenticated account:

```bash
gh auth status
```

To create **`bounded-agent-workflow` under that authenticated personal account**, run:

```bash
gh repo create bounded-agent-workflow --private --source=. --remote=origin --push
```

This command **creates a private remote and pushes the seed**. Run it once, only when
that is what you intend. For an organization or explicit owner, use
`OWNER/bounded-agent-workflow` instead of the bare name, replacing `OWNER` with the actual
GitHub owner. Do not paste `OWNER` literally.

If the remote repository or `origin` already exists, **skip remote creation**. Inspect
`git remote -v`, verify the actual owner/name, and use the existing remote after resolving
any mismatch. Redact credentials if an existing URL contains them; do not paste such a
URL into chat or an issue. Do not initialize over an existing application's history.
Decide licensing separately. References for the GitHub CLI flags are in
[sources](../research/sources.md). Continue with section 2 below.

## 2. Start a fresh Codex lead

Start `codex` from the repository. Do not resume/fork an old conversation for this first
onboarding. Paste:

> Act as the bootstrap lead for this repository. Read AGENTS.md, workflow/protocol.md,
> workflow/roles/lead.md, and the linked product, lifecycle, documentation, architecture,
> verification, execution-environment, secrets/access, single-account closeout, and GUI
> verification documents. Preserve routine full-ticket authority: after approval, verified
> non-production merge and closure need no extra human merge click; pause before the next
> ticket. Identify production/release or unresolved design exceptions explicitly.
> This is a documentation-only seed:
> no baw commands exist.
> Inspect actual recent Git history, staged/unstaged/untracked work, and native tool
> versions read-only. Inspect only secret references/required names, never values, and
> identify effective GitHub rules without changing them. Preserve one GitHub account
> for the operator and agents; do not demand separate approving identities.
> Include a minimal BAW build/test/runtime environment plan; do not
> require NetBox or a virtualization lab to build this tool. Do not install, update,
> change credentials/settings, create issues, spawn agents, or implement
> code in this first pass. Identify contradictions or missing decisions. Propose adoption
> or revision of the three Proposed ADRs, one GitHub coordination issue, and one bounded
> bootstrap work item. The first success criterion is replacing you from durable records
> without losing state, including unrecorded file changes—not implementing the controller.
> Return a concise plan, required environment decisions, and stop.

This gives the lead its first phase. It does not authorize implementing the whole design.

## 3. Review and establish the baseline

Open a separate Claude session with the reviewer role. Give it the same proposed scope
and files, not only the lead's explanation. Ask it to challenge state authority, restart
behavior, documentation placement, missing context, and unnecessary operator overhead.
No source edits or real automation in that review.

Return findings to the lead. After resolving material questions, explicitly authorize
only the accepted bootstrap changes and GitHub writes, including the adopted ordinary
closeout policy if desired. This does not grant blanket implementation or release authority.
The lead may then create the
coordination issue from the reusable template, create the bootstrap issue using the
work-item form, and record approved decisions. It must report the actual created IDs;
there are no preassigned issue numbers in this seed.

Keep the coordination pointer discoverable from a short project entry link. Record the
actual commit and policy revision. Proposed ADRs become Accepted only with recorded
operator adoption; implementation status remains separate. Do not rewrite this manual
with current sprint status.

### Establish the tooling environment without overbuilding

For this documentation-only stage, no app exists to run. Inspect the actual host/tool
versions and agree the minimal Go build/test/terminal demonstration route for the first
implementation slice. Use [execution environments](../developer/execution-environments.md)
and adapt only relevant fields into a maintained developer setup guide. The guide must
separate inspected facts, proposed commands, and checks actually executed. Install or
change a toolchain only after specific approval. Do not add a container platform, NetBox
instance, or nested lab merely to satisfy a generic template.

After the first executable slice exists, run it and its tests in the documented environment
and have the operator exercise the same binary. That is its initial runnable acceptance
path. Future application adopters validate their own required environment, including a
persistent GUI preview or scarce-lab access when appropriate.

### Secrets and GitHub are setup choices, not extra per-ticket chores

Adopt [secret bindings](../developer/secrets-and-access.md) and
[single-account GitHub](github-single-account.md). Preserve native subscription and GitHub
login; do not export their credentials into project `.env` files. The documentation stage
and first read-only Go fixtures need no real CMS or lab secrets. Test access plumbing with
dummy inputs before binding a live project profile. The operator supplies real values only
through the approved host-local path when actually needed.

The lead proposes the minimal environment bindings and effective rule changes, if any.
You approve consequential changes once; routine authorized setup then reuses the selected
profile. Do not remove inherited required controls without permission. Verify the same
account can use the intended PR flow without a separate approving review, while retaining
mandatory checks, independent review, conditional routine merge, and the next-ticket gate. A read-only audit need not change any rule.
Prove live configuration only in an explicitly authorized test, not by merging a real feature.

## 4. Rehearse a lead replacement before coding

Give the lead one small planning/documentation assignment with a real decision or
blocker. Have it record the accepted facts and update the coordination issue. Then ask:

> Prepare a lead handoff under docs/operator/agent-lifecycle.md. Pause new dispatch,
> reconcile recent commits, staged/unstaged/untracked files, GitHub and live runtime
> state, preserve preview/reservation ownership, update the coordination index,
> and record a transition receipt. Resolve your helper lifetimes. Do not grant new scope,
> reset budgets, clean uncommitted work, or start another ticket. Stop directing work.

Exit the old lead after checking that its own children are finished/stopped. Start a
fresh Codex session in the same repository and paste:

> Take over the lead role using AGENTS.md and workflow/roles/lead.md. Use the project's
> actual coordination issue and authoritative records, not the previous conversation.
> Reconcile the goal, accepted decisions, recent Git commits and uncommitted work,
> current candidate, runtime/preview/resource state, active assignments,
> budgets, blockers, and next permitted action. Report sources and unknowns. Do not
> dispatch or change project direction until I activate your new lead assignment.

Do not paste the old chat as the test input. Confirm the replacement reconstructs the
important facts without re-explanation. Also rehearse an unavailable old lead. Preserve
unknowns and partial work rather than rewarding a confident invented reconstruction.

In a disposable fixture/worktree, rehearse a committed change absent from the handoff,
a file with different staged/unstaged content, an untracked source file, and a still-live
remote/preview operation represented initially by a harmless fixture. The incoming lead
must find the actual delta without modifying it or resetting a budget. Do not create a
real lab outage, discard useful edits, or publish secret fixture contents for this test.
Recorded rehearsal results belong in the bootstrap issue, not a new source diary.

## 5. First implementation assignment

After the methodology baseline and takeover rehearsal are accepted, ask the lead for a
single small implementation ticket:

> Specify a read-only Go CLI slice that inventories the repository and assembles a
> source-linked lead context packet from maintained files and the coordination record.
> Start with deterministic local fixtures and fake GitHub responses, then an explicitly
> authorized read-only live smoke test. Do not dispatch model workers, approve work,
> mutate GitHub, implement retries, build a daemon, or manage arbitrary background tasks.
> Include read-only committed/uncommitted state reconciliation and an explicit unknown
> result for unavailable runtime state; exclude secrets from context capture and avoid
> echoing raw diffs or environment output. Do not build an environment provisioner,
> credential store, GitHub approval service, or rules editor.
> Include the exact proposed CLI contract, failure behavior, tests, documentation impact,
> and a terminal demonstration. Stop for scope approval before implementation.

The developer needs a reviewed Go installation/module setup in this tooling repository.
Do not add Go or Python requirements to unrelated application repositories. Once code
exists, record the real build/test commands and actual evidence.

## 6. Repeated manual ticket loop

Keep the lead in the main checkout for planning. Put the approved worker in one isolated
branch/worktree. The reviewer examines the frozen candidate or a separate review
workspace. You can reuse terminal windows while starting fresh role sessions as needed.

Have the lead output one dispatch message containing the actual issue, approved revision,
base, workspace, policy, context references, selected environment-contract/profile revision,
resource ownership, preview/demonstration, approved credential bindings and remote action
scope by reference only, acceptance criteria, documentation impact, budget, completion
mode, exact integration target, exception holds and any required visual evidence.
Paste it to a fresh Claude worker. After readiness, approve that single scope once. Under
adopted `routine-integrate`, this also authorizes in-scope commit/push/PR, gate-checked merge
to the specified non-production branch, record updates and ticket closure. Native tool
permission dialogs may still require configuration; do not suppress them with blanket access.
The worker returns a candidate and evidence, not permission to proceed. It uses approved
start/update/verification operations so the relevant running app is inspectable without
asking the operator to rebuild each change. A held preview outlives a worker session;
shared labs stay serialized under their authoritative reservation.

Use the documented live-to-frozen transition for the identified acceptance candidate;
reuse the same instance when its fidelity and isolation permit. Freeze edits and
run/inspect mandatory verification. Start a fresh Codex reviewer with
the original criteria, candidate, and evidence. Its review should assess tests and docs,
not simply agree with the worker. Relay in-scope findings to the worker within the same
recorded budget. Maintain one writer and reverify the resulting candidate. Stop on
changed requirements, unknown state, or exhaustion.

When review and all mandatory gates pass, relay the reviewer result to the lead. The lead
acts as closeout coordinator under [the adopted policy](github-single-account.md): confirm
current PR head/base/integration evidence, merge through the normal protected path, confirm
the actual merge and required post-merge checks, update records and close the ticket. You
do not review the commit or approve the merge again. The reviewer records model findings,
not same-account GitHub approving reviews or a fabricated claim that you inspected the app.

The lead then reports what changed, actual verification and merge, known limits, a usable
preview/demonstration, and one proposed next scope. It STOPS. You can inspect or request a
repair, or approve that identified next ticket in a single response. No response means no
next-ticket execution. A failed post-merge check is `MERGED_BLOCKED`, not completed work.

For new GUI design/interaction decisions, get your input early in a prototype/story; the
implementation of an accepted design can follow ordinary closeout. Require actual rendered
image inspection and real integrated journeys under [GUI verification](../developer/gui-verification.md).
Unresolved design, production/release consequences, governing policy changes and exhausted
budgets need their specific decisions. Do not treat every GUI edit as a new human hold.

This is still manual message passing while building BAW; it is not implemented orchestration.
The removal of redundant merge approval applies now once adopted, not only after the tool exists.

## 7. Upgrade or interruption

A refresh need not include an upgrade. Apply updates only when no affected run on the
host is using the shared toolchain, then record versions and smoke-test the native
interfaces and subscription login. See [lifecycle](agent-lifecycle.md).

For a crash, do not immediately launch another writer. Ask a fresh lead for a read-only
reconciliation using the transition-receipt fields: recent commits, staged/unstaged and
untracked work, relevant worktrees, actual live processes and previews, resource reservations,
completed operations, uncertain external effects, evidence, and budget. Authorize
only a bounded recovery. Do not reset attempts or delete locks just to make it run.
Use the Git/runtime inspection procedure in the lifecycle guide. A lost SSH session
may leave the agent, app, or remote job alive. Where already approved, named tmux
sessions on the execution host reduce avoidable disconnect loss but do not replace
reconciliation or lab ownership checks.

## 8. Grow the controller in independently accepted slices

The lead keeps the detailed backlog in GitHub, not a new local plan file. The intended
dependency order is: read-only context/inspection; durable identities/records and crash
reconciliation; one worker with verification; independent review and bounded repairs;
guided scope approval, conditional integration/closeout, next-ticket stopping and reliable
recovery; then validated helper lifecycle and refresh conveniences. Each slice needs a terminal-visible demonstration and negative
cases. Do not combine all slices into one ticket.

Prove orchestration first with fake tools and disposable repositories. Supervise live
subscription smoke tests. Only an independently accepted release may govern selected
work in this repository. Keep that released binary and governing policy external to
and unchanged by the candidate under development. Keep this manual path as the fallback.
