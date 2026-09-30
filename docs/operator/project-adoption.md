# Adopt the workflow in an existing or new application

**Available now:** an agent-assisted, manually approved procedure. There is no automatic
`baw init`, environment installer, or project migrator in this documentation-only seed.
This guide is for application projects. To develop the BAW tool itself, use
[the manual bootstrap](manual-workflow.md) instead.

## Start from the project you have

Keep the toolkit outside the target repository. Open a native agent in the real target
checkout, giving it the absolute path to this guide and the shared reference material.
Do not extract the complete tooling seed over an application. Its README, Go product
contract, proposed ADRs, and contributor guide describe BAW, not the target application.

Existing projects retain code/history, remote identity, tests, CI, useful docs, current
backlog, and working runtime setup. Merge only missing operating rules and pointers;
never overwrite AGENTS.md/CLAUDE.md, nested instructions, hooks, or security settings.
A new application uses the same discovery/setup sequence, but explicitly creates missing
infrastructure. Neither mode requires a new repository layout or container migration.

The desired experience is the same in both cases: one bounded adoption conversation
and a reviewed setup change. The amount of underlying discovery can differ, especially
with undocumented external services or scarce labs. Do not promise identical effort.

## First pass: read-only discovery

Paste this into the in-repository agent, replacing the toolkit path:

> Act as the project-adoption lead. Read [absolute toolkit path]/docs/operator/project-adoption.md
> and its linked execution-environment, secrets/access, single-account GitHub,
> documentation, role, and protocol references in
> that toolkit. Inspect this repository's own instructions first;
> these toolkit proposals do not silently override its current policy. Your first pass is
> read-only. Reconcile Git history, staged/unstaged/untracked work and known runtime state.
> Inventory current docs, CI/tests, setup scripts, actual app/dependency versions, services,
> previews, remote/shared resources, credential bindings by reference only, effective single-account GitHub rules, and
> outstanding work. Never display secret values, auth stores, or full environment dumps.
> Do not install, change files/settings, start or reset infrastructure, run tests with
> side effects, migrate data, create issues, or dispatch agents. Propose the smallest
> integration that preserves working practices and a project-specific environment contract.
> Identify what can be reused, what is missing, and what you need me to decide. Stop.

Use [lifecycle reconciliation](agent-lifecycle.md) for any uncertainty. Inspect manifests
and declared entry points before executing them; a setup script or test can mutate
external state even when its name looks harmless. If running-process evidence is not
accessible, mark it unknown and ask for only the access/decision that is needed.

The proposed assessment should fit in one issue (or the first response until issue
creation is authorized), with these headings:

- Preserved baseline: actual repository/checkpoint, dirty work, instructions, checks,
  runtime, documentation, current coordination location.
- Adoption delta: exactly which files/rules/pointers are added or merged; no duplicates.
- Environment plan: profiles, real services, preview, fidelity, isolation, shared-resource
  ownership, allowed commands/permissions, budgets, recovery, and verified unknowns.
- Verification and rollback: baseline evidence, fresh-session demonstration, safe recovery
  of the adoption changes, and limitations. Do not reset or destroy unrelated work.

Research newer environment options only when it could materially help. Record applicable
versions and source evidence. The existence of a Docker example is not proof it matches
the plugin or project. Containerization, major CI refactoring, or a lab redesign is a
separate approved work item unless explicitly part of this bounded adoption.

## Review, then stage the minimum integration

Have a separate model review the assessment against the actual repository. Resolve
instruction conflicts explicitly. Preserve stronger existing constraints and record
intentional amendments; do not mechanically choose whichever document is newest.

After approval, use a reviewed branch/worktree and preserve the original dirty workspace.
Do not automatically commit, stash, or clean pre-existing edits just to make adoption
convenient. Git inspection/recovery precedes any baseline choice. A clean worktree is a
useful staging location only after confirming it will not reuse conflicting services.

Normally the integration is a short root instruction map, accessible pinned common/role
rules, a pointer to the existing or newly authorized coordination issue, and a maintained
project setup/environment guide adapted from
[the template](../../workflow/templates/environment-contract.md). Reuse current documentation
paths and issue forms where practical. Keep the controller's own design/docs out of the
application. Existing license/contributor guidance is not replaced. During manual use,
keep referenced toolkit versions locally available; do not point at mutable remote text
and assume future agents receive the same policy.

Record approvals, current findings, and recovery details in the adoption issue; update
only reusable technical knowledge in the source repository. No parallel local backlog.
Do not retrofit all historical issues or create ADRs for every old implementation choice.
Document material current decisions honestly, including unknown rationale where needed.

## Configure access once, without changing account topology

Use [secrets and access](../developer/secrets-and-access.md). The operator supplies values
locally; the agent maps names/references to actual consumers and implements only the
approved setup delta. Reuse a functioning `.env` initially instead of mandating a vault
migration. Check ignore/tracked-file status without dumping contents. Configure repeatable
per-instance delivery for parallel environments and separate their mutable downstream
resources or serialize writes. A secret source is never task context or a build artifact.

Use [single-account GitHub](github-single-account.md) to inspect all effective rules,
including organization and deployment settings, before proposing changes. Keep PRs,
independent model findings, mandatory verification and scope/next-ticket checkpoints;
preauthorize conditional routine merge/closure through the named integration policy, but do not
create a requirement for another approving GitHub identity. Existing conflicts require
an authorized amendment or an explicit blocker. No automatic bypass or rule removal.
This is one reviewed adoption decision, not another approval conversation each ticket.

## Prove environment readiness

Under the specifically approved commands/permissions, an agent follows the actual setup,
starts or attaches to the relevant application, checks source/build identity, and runs
baseline verification. You reach that same UI/CLI and exercise a small real scenario.
Prove that an ordinary source change reaches the running app through the declared update
path; the operator must not have to rebuild each iteration. Include backend workers,
assets, persistence, and migration behavior where applicable.

Then start a fresh agent to reproduce or safely adopt the environment using only the
maintained guide and run references. For a disposable environment, test clean reproduction;
for a scarce live lab, verify a safe reservation/reattach path and use an approved test
snapshot or window, never reset the shared lab just to demonstrate onboarding.

First exercise secret delivery with dummy credentials: missing/wrong-target profiles,
per-instance isolation, no canonical-file overwrite, and no values in logs/context/artifacts.
Verify one-account review/merge eligibility through a separately authorized disposable
GitHub test when settings are to be changed; offline tests cannot prove live rules.

Required smoke scenarios include a stale or mismatched preview, a lost agent/SSH client,
preserved dirty files, and a busy shared resource. For proposed parallel instances, prove
ports/state/queues and downstream mutations cannot cross-contaminate. Tests/reset must
not interrupt the operator's active preview. Stop if the required fidelity cannot be
established; keep failing baselines visible and remediate them separately.

A genuinely new application may need an environment-enablement/scaffold ticket before
there is a running app. Keep that exception explicit and bounded. No product feature
is accepted solely because a skeleton process starts or mocked tests pass.

## Accept adoption and proceed normally

Adoption is ready when there is a reviewed instruction delta, a navigable current-state
index, an accurate runnable environment contract, actual baseline evidence and declared
gaps, a working human inspection path, approved secret bindings with no value exposure,
single-account-compatible effective GitHub rules (or explicit policy blockers), and a
successful fresh-agent takeover.

The lead records the adopted revisions and readiness evidence in GitHub. Merge/push
still require their own authority. The next feature ticket references the approved
project/environment contract, only its relevant context, and a concrete demonstration.
It does not repeat installation or invent a new environment on every assignment.

Use the same role lifecycle, review, budget, and acceptance boundaries as the manual
workflow. When a validated controller exists, it can automate these known procedures;
adoption does not depend on waiting for that implementation.

## Adopt the completion and GUI evidence policies once

Under [single-account closeout](github-single-account.md), determine the actual non-production
integration target, downstream workflow/webhook/hosting effects, release/consumer policy,
mandatory head/base/post-merge gates, and exceptional human holds. Existing team constraints
are not silently removed. Routine ticket approval then includes push/PR, verified merge
and closure; the next-ticket checkpoint remains mandatory. Do not add another account or
operator merge prompt just to implement independent review. Unknown production effects
must be resolved before delegating routine integration.

For GUI applications, adapt [GUI verification](../developer/gui-verification.md) using the
existing browser/story/fixture tools where suitable. Prove that workers/reviewers can open
actual images, not just create them, and exercise the real integrated user path. Record
supported states/viewports, accepted design references, reviewed-baseline procedure and
safe retained evidence. Separate new design decisions from conforming implementation.
Validate with intentionally broken disposable UI cases before relying on the gate. These
are project-specific setup tasks, not mandatory browser installs for a CLI-only project.
