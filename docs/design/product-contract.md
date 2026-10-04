# Product contract — proposed target

**Status: proposed design requirements; no controller is implemented by this seed.**
This is durable intended behavior, not a development backlog. Actual tickets and their
status belong in GitHub. The operator adopts/revises the proposal during bootstrap.

## Goal

Reduce human message-relay work while keeping bounded scope, independent cross-model
review, strong verification, understandable acceptance, and frequent human checkpoints.
Normal use should require a few clear commands, not managing run paths, hashes, or agent
session internals. Retain authenticated native Codex/Claude CLI subscription paths.

## Boundaries

One repository owns the product method, source, templates, tests, operator/developer
docs, and release artifacts. One Go module builds one platform-specific `baw` executable.
The CLI is the user interface; the controller is its internal execution logic. There is
no separate daemon/service requirement, hosted database, backlog, multi-repo plugin
marketplace, or generic agent-company framework in the initial scope.

## Proposed operator surfaces

The initial useful slice is read-only environment/state inspection and assembly of a
source-linked role context packet. Exact names are subject to the approved CLI contract.
Illustrative eventual commands are `baw lead`, `baw lead --fresh`, `baw work 123`,
`baw status 123`, and `baw recover 123`. None is currently implemented; the existing
`baw status --state-dir DIR --run-id ID` only displays a saved
[run record](../operator/run-records.md), not live ticket status. A hold-resolution
operation may be added for exceptions; a mandatory `baw accept` after every routine ticket
is not part of the target operator experience.

Starting/refreshing a lead should discover the project, reconcile current state, and
load the assigned role plus bounded context. The old directing generation is retired
before a new one is activated. A native interactive terminal may remain the interface;
do not build a terminal emulator merely to launch a lead with instructions.

Working a ticket should validate and snapshot its scope, present approval, create an
isolated workspace, run a worker, execute required gates, review a frozen candidate,
allow finite repair, confirm current integration requirements, merge to the approved
non-production target, verify closeout, update records and close the ticket. Then stop
before the next ticket by default. A project may opt into an explicit bounded
[standing delegation](../operator/github-single-account.md#standing-program-delegation-opt-in)
letting the lead select the next ready ticket and finite in-scope repair extensions with a
recorded authority chain; missing or unclear delegation fails closed. The tool manages paths, evidence and conditional merge internally;
no per-merge human approval is needed for adopted routine scope. Named design/consequence
holds stop for the specific decision. Releases/deployment remain separately authorized.

## First-class adoption and environment support

The same bounded operator workflow must support existing and new projects. Existing
projects are inventoried read-only, then receive a minimal reviewed integration; no
blanket overwrite, history rewrite, forced layout migration, containerization, or test
weakening. See [project adoption](../operator/project-adoption.md). A repeated adoption
assessment should identify differences, not duplicate files or silently change policy.

Execution uses a project-specific [environment contract](../developer/execution-environments.md)
covering native, container, remote-lab, or hybrid profiles through existing inspected
commands. The minimum product tracks source/runtime identity, readiness, required
verification fidelity, ownership, preview access/hold, and safe recovery. It is not a
universal provisioner, VM platform, or distributed infrastructure scheduler. Initially
support manually verified profiles; automate providers only through separate validated
slices. Unimplemented profile automation remains supervised, never assumed.

Human preview should stay available across agent replacement and update without manual
rebuild for each edit where the project supports that. Distinguish live unverified
source from a frozen acceptance candidate. Scarce mutating resources are acquired at
the stage needing them through one authoritative ownership point across projects.
An expired lease or disconnected client does not justify replay/reassignment of an
unknown remote operation. Controlled teardown cannot affect another run or held preview.

## Secret delivery and single-account GitHub

[Secret bindings](../developer/secrets-and-access.md) are selected through existing
project profiles. The first implementation need not ship a vault or general credential
broker. Reuse native authenticated tools and operator-provisioned files/stores; supply
only required app/API consumers, not every agent. Multiple environments reuse approved
sources without sharing mutable per-instance settings or exposing values in context,
Git, ordinary evidence, build layers, or logs. Fail closed for missing/invalid access;
no fallback to broader credentials. A same-user process is not a strong isolation boundary.

The default [GitHub workflow](../operator/github-single-account.md) must operate with one
account for operator and agents. Independent model review remains mandatory, but is not a
required approving review by a distinct GitHub identity. Preserve actual required tests,
human scope/next-ticket boundaries (or recorded standing delegation) and preauthorized
conditional routine merge. No separate
human code review or approving identity is needed for each PR. Rule changes need specific approval;
existing unchangeable conflicts remain visible blockers. Do not ship automatic admin
bypass, synthetic identities, or user.name-based approval tricks. Technically isolated
human authority is optional infrastructure, not a property of a same-user confirmation.

Direct CMS/API implementation is possible when a ticket authorizes the exact target and
operations. Preserve remote revision/state evidence and reconcile uncertain completion;
a Git commit alone cannot identify or restore a remote content deployment.

## Durable state and recovery

Persist immutable input/candidate/evidence references, human approval provenance,
role assignment generations, provider session IDs when available, processes, attempt
budgets, and transition receipts. GitHub indexes planning and approved outcomes; local
execution state is not a competing task manager. Conflict resolution must reconcile
actual records rather than treating the newest summary as truth.

Implemented so far: only an immutable local [run record](../operator/run-records.md) of
supplied ticket/scope/policy references and one observed HEAD, and the recorded
[`baw run execute`](../operator/execution.md) and [`baw run review`](../operator/review.md)
attempts of trusted local programs; a recorded review verdict is not approval, findings or
verification of the reviewed candidate. Approval provenance,
generations, sessions, processes, budgets, receipts and recovery remain future work.

The controller can execute in a separate terminal from the lead, so replacing the lead
need not kill controller-owned work. Persist state at meaningful boundaries so losing
the controller itself has a recovery path. Do not promise exactly-once external side
effects after a crash; uncertain completion must be reconciled before another write.

Recovery must retain budgets and partial work. Reconcile checkpoint-to-HEAD changes,
staged/unstaged diffs, untracked files, relevant worktrees, and live environment state.
Use reflog/stash inspection when needed; do not equate Git history with all saved work.
Agent sessions may be discarded; their unverified code is preserved and classified. Provider-specific live state migration
is optional, not a correctness dependency.

## Context and helpers

Use concise role-based onboarding, a maintained project map, accepted ADRs/contracts,
and ticket-specific references. Native history/auto memory are optional caches, never
approval or policy authority. Implemented so far: [`baw context`](../operator/context.md)
lists a lead, worker or reviewer's fixed onboarding source references with current Git
observations; ticket references, prompts and agent launch are not part of it. Support compact event metadata where documented, without
requiring a particular telemetry protocol or inventing missing measurements.

Helpers are bounded, normally read-only, and tracked by parent/assignment/input
revision. Validate per-provider restrictions and lifecycle. Preserve cross-model
independent review. Disable unsupported automated delegation rather than pretending
instruction text enforces cancellation, depth, or permissions.

## Packaging and compatibility

Runtime consumers need the released executable and native tools, not a Go or Python
development environment. Ship default workflow material with a version/digest and make
it inspectable. Projects pin/review policy; upgrading a global executable must not
silently replace a project's governing instructions. Avoid uncontrolled global symlinks
to mutable policy. Record resolved native tool versions before each dispatch.

## Quality, documentation, and authority

Documentation is part of acceptance, following the documentation policy. Design for
source-linked human reports, not raw transcripts. Native account permissions and a
same-user process can defeat procedural gates; stronger identity/isolation is separate
infrastructure and must not be implied by hashes or local confirmation prompts.

When developing this controller, a candidate must not become its own governing judge.
Keep the accepted controller/policy external and fixed. The candidate is test input until
independent verification and explicit promotion. Avoid a self-update command in the
initial product; deliberate replacement of the binary is sufficient.

## Routine completion and observed GUI conformance

[The closeout policy](../operator/github-single-account.md) defines complete-ticket authority,
non-production integration versus release, exact head/base/result evidence, actual merge
confirmation and post-merge failure handling. Persist completion mode and any human holds.
A queue request is not a completed merge. After `COMPLETED_AWAITING_NEXT_SCOPE`, by default
no new implementation is dispatched until the operator approves its identified scope. Under
a valid [standing delegation](../operator/github-single-account.md#standing-program-delegation-opt-in)
the lead may instead dispatch a ready, frozen, bounded in-scope ticket after recording the
prior closeout and that ticket's scope, finite budget and authority chain; progress reports
stay informative, not approval. Missing, revoked or out-of-scope delegation fails closed. Keep budget
and merge/record-recovery lineage across replacements. Git rollback is not external rollback.

[GUI verification](../developer/gui-verification.md) is an evidence requirement, not an
assumption that code review predicts appearance. Support actual image-viewing capability,
real interaction paths, representative integrated environments, reviewed baseline changes,
source/build provenance and private retained evidence. Use operator design feedback early
when needed; ordinary conformance to approved designs can complete autonomously. The first
Go read-only slice does not need a GUI; do not install browser infrastructure merely to
populate this generic requirement.
