# Verification contract

**Initial state:** the seed has no implementation and no software test suite. Packaging
checks are not execution tests. The earlier Python prototype's test count is not evidence
that a new Go implementation behaves correctly. Populate real commands only after they
are implemented, executed, and reviewed in this repository.

## Layers

Use deterministic unit/state-transition tests, real temporary Git repositories and
worktrees, simulated provider failures, and explicit native integration smoke tests.
Keep offline tests separate from authenticated tests that consume quota or contact
GitHub. Never claim a simulated executable proves native authentication compatibility.

Required application regression gates remain required. Optimize feedback with targeted
checks during editing, but do not trade required final verification for speed. Inspect
assertion strength and test-oracle independence, not only passing-case counts.

## Required controller behaviors to test before trusting automation

- Missing/stale approvals, changed scope or policy, wrong candidate, missing evidence,
  interrupted writes, and exhausted budgets fail closed.
- Worker/reviewer replacement preserves the exact approved scope and attempt lineage.
  A lead disappearing cannot create a new ticket or a duplicate writer.
- A new lead can reconstruct state with the old conversation unavailable. It finds
  active work and unresolved decisions without re-explanation from the operator.
- Abrupt loss with dirty files preserves partial work as unverified. Recovery does not
  clean/reset it or assume a possibly completed external action failed.
- Retired lead/helper generations cannot advance state. Late messages and duplicate
  completion events cannot produce duplicate execution.
- Parent replacement identifies/join/cancels children or blocks on unknown ownership.
  Native helper bounds/permissions are actually enforced or the feature is disabled.
- A changed CLI/configuration/policy version is detected before a new dispatch. Unknown
  authentication or quota errors do not cause an API-billing fallback.
- New candidate commits invalidate affected review/verification; acceptance is tied to
  the candidate, not an agent's claim or an earlier test run.
- Live GitHub mutations require the authorized path and handle uncertain completion
  without blind replay. Logs do not expose credentials or masquerade as uploaded links.
- Documentation and executable behavior agree for each delivered interface. Proposed
  commands are not advertised as implemented.

## Adoption, Git recovery, and runtime scenarios

Test a dirty existing repository with existing instructions, CI, and useful setup: the
adoption assessment is non-destructive and its approved delta does not overwrite policy,
change history, remove tests, or create duplicate guides. Cover a new/no-commit repository
without misreporting its missing baseline as a successful application run.

For Git recovery, cover a committed-but-undocumented change, different staged/unstaged
versions of one file, an untracked source file, relevant ignored metadata without secret
contents, another linked worktree/submodule, stale remote tracking, and non-ancestor
checkpoint/history movement. Verify the same operations do not mutate work. A reflog
is not the expected source of arbitrary unsaved content; unresolved gaps remain unknown.

For environment profiles, use deterministic fixtures before live resources. Prove source
and runtime identity agree; stale images/background workers are detected; required
integration/lab gates cannot be replaced by mock results; a retained preview survives
an agent refresh; and acceptance cannot silently follow a moving live-reload checkout.
Lost forwarding must not trigger unnecessary app rebuild or another writer.

Before advertising safe parallel use, demonstrate separate ports, storage, queues, and
external effects for independent stacks. Shared-lab tests must include two projects on
different execution hosts, competing reservations, disconnect while a remote mutation
continues, stale-generation messages, failed reset/quarantine, and operator inspection
holds. Never auto-reassign an expired reservation without stopping/fencing or verifying
the former writer. Unsupported technical exclusion means supervised-only operation.
These are required future tests, not tests already executed by preparing this seed.

## Secret delivery and one-account GitHub scenarios

These are requirements for future implementation/adoption tests, not results established
by this seed. Use dummy credentials and disposable resources before live access.

- Metadata/context/normal logs and artifact packaging exclude values, including sensitive
  untracked files and accidental values in otherwise tracked diffs. Known-pattern matching
  alone is not proof that arbitrary secrets are absent. Missing redaction means withhold,
  not publish then redact afterward.
- The approved consumer receives exactly its profile; other services/helpers do not inherit
  it. Missing, expired, wrong-target or unauthorized profiles block without broad fallback.
  Verify actual dotenv syntax/precedence; do not execute a dotenv file as shell code.
- Two workspaces keep distinct ports/data/queues and per-instance files, do not overwrite
  the canonical source, and cannot change the other's settings. Cleanup removes only owned
  copies; a held preview retains required access. Rotation invalidates affected evidence.
- A CMS timeout after a write is reconciled against remote state; it does not blindly repeat
  publishing/deletion. External action permission is narrower than token capability.
- A single GitHub identity can complete the intended PR/check path with independent model
  findings and preauthorized conditional merge/closeout, without a second approving-review
  account or per-merge human approval. It stops before the next ticket.
  Check last-pusher, CODEOWNERS and deployment self-review conflicts explicitly.
- No model comment, changed Git author name, new session, skipped mandatory suite, or forged
  local approval becomes human authorization. Required-check source and inherited-rule
  behavior need live verification; offline fixtures cannot prove GitHub enforcement.
- Adoption never edits secrets or GitHub protections during its read-only pass. A rule conflict
  yields a proposed approved amendment or a blocker, not blanket disabling or admin bypass.

For a same-user/full-credential deployment, tests can establish ordinary control flow, not
resistance to an adversarial worker with equivalent access. State that limitation in results.
See [access](secrets-and-access.md) and [GitHub](../operator/github-single-account.md).

## Manual continuity rehearsal

Before implementing automation, complete a small approved planning/documentation task,
record its decisions and state in GitHub, retire the lead without giving the replacement
its chat history, and ask the replacement to reconstruct the next permitted action.
Repeat once with the previous lead unavailable and once with unverified partial work.
Check concrete facts and human re-explanation required, not only a self-rated score.

## Self-hosting acceptance

First run a candidate controller against disposable fixture repositories with fake
agents. Next supervise a small live ticket and exercise stop/recovery failures. Only
then allow an independently accepted release to govern selected low-risk work in this
project. It remains external to the candidate checkout; its executable and policy do
not change during a run. Controller/safety-policy changes require separate scrutiny.

## Complete-ticket autonomy and GUI conformance tests

These are future required behaviors, not tests already run by generating the seed.

- Given approved routine scope, valid independent review and all required evidence, the
  coordinator can push/open a PR, conditionally merge, confirm post-merge gates, update
  records and close it with no intermediate human merge/demonstration approval. It cannot
  dispatch the next ticket without its identified scope approval.
- A changed head, base/integration race, untrusted/skipped mandatory check, policy change,
  out-of-scope edit or missing evidence does not reach merge. Benign in-scope revalidation
  uses the recorded budget without demanding a ritual human approval. Exceptions block.
- The audited non-production integration path does not publish a release, move an existing
  release tag/artifact, trigger production/CMS mutation or bypass inherited rules. Simulated
  unsafe/unknown downstream hooks are detected; live audits need actual environment evidence.
- A queue request is not a completed merge. Disconnect-after-merge recovery queries actual
  state instead of replaying the write. Squash/rebase identity and resulting tree/build
  mapping are recorded. Failed post-merge health keeps the ticket blocked/not completed.
- New design intent requires its named decision. Implementation matching approved intent
  can complete without a human merge click. The worker cannot waive a design hold or make
  an unintended change acceptable by rewriting test expectations or screenshot baselines.
- GUI verification catches a stale preview, covered/hidden/disabled control, clipped error,
  success toast after a failed save, missing persistence and unsupported viewport claim.
  A screenshot file that no reviewer can view does not pass visual inspection. A DOM-only
  report or mocked story does not pass pixel or integrated-journey gates.
- Baseline changes are independently reviewed against intent; bulk regeneration, excessive
  tolerances/masking and post-capture code changes cannot silently pass. Important successful
  acceptance journeys retain inspectable evidence, not only failing/retried tests.

Follow [closeout](../operator/github-single-account.md) and [GUI verification](gui-verification.md).
The fixture tests will establish control behavior, not universal visual correctness or
resistance to an adversarial worker with equivalent privileges.
