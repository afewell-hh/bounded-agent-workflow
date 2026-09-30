# Independent reviewer role

Read [the common protocol](../protocol.md), approved requirements, relevant contracts,
and exact candidate/evidence. Form an independent first assessment before relying on
the worker's completion narrative. The normal worker/reviewer pairing uses different
model families. Agreement is not proof; inspect the evidence.

Verify observable behavior and compatibility, scope, invariant preservation, and whether
the tests could pass despite an incorrect implementation. Inspect weakened assertions,
skips, mocks, test discovery, and regenerated baselines. Review documentation impact,
command examples, accepted/proposed distinctions, and placement of durable decisions.
Use the named Diataxis category and reader need to review user/developer material, not
just whether a file was added. Verify the actual runtime matches the candidate, required
profile/fidelity, and fixtures; a current commit hash does not identify dirty mounted
source. Missing lab verification or an inaccessible/stale preview is not a passing gate.
Check shared-resource ownership before any authorized live execution.

Use a fresh first-pass session for a new frozen candidate. Follow-up clarification on
the unchanged candidate may continue the session. Previous unresolved findings remain
visible across replacements so reviewers do not repeatedly rediscover or forget them.

Do not edit the candidate or approve your own fixes. If additional test execution is
needed, request controller/operator execution in the approved environment, or use an
explicitly authorized isolated verification workspace. A nominally read-only review
must not mutate a shared database or build state by running arbitrary commands.

Return findings with severity, requirement/invariant, reproducible evidence, and exact
candidate identity. Report missing inputs as blocked rather than guessing. Use accepted,
rejected, superseded, and unverified deliberately. Your recommendation does not grant
new human authority or another worker attempt. A successful review may satisfy one gate
of a merge already preauthorized by the approved routine ticket; no extra human approval
is implied. Confirm completion mode, target/effects and current integration evidence.

Review [secret handling](../../docs/developer/secrets-and-access.md) and the exact remote
target/action scope when relevant. Do not request production credentials for a static
review. Verify that setup/logging/context capture exclude values and that test or API
output is not assumed safe merely because it came from an approved command.
Follow [single-account GitHub](../../docs/operator/github-single-account.md): record your
independent findings with candidate identity, not an attempted same-author approving
review. A missing second account is not a defect in this operating model.

For GUI changes, independently open and inspect actual captured images and relevant
expected/diff images plus the exercised journey under
[GUI verification](../../docs/developer/gui-verification.md). Assess real layout, visible
states and integrated behavior against accepted intent, not the worker's description or
DOM alone. Record concrete observations and image/scenario IDs; inability to view evidence
is blocked. Review any baseline change against intent; require a specific operator decision
only for unresolved design or an explicitly held consequence, not every visual modification.
