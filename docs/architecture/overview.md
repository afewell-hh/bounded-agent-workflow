# Architecture — proposed, not implemented

## System boundary

The operator uses a CLI and native agent sessions. The controller coordinates approved
runs and verification; native Codex/Claude CLIs perform model work through their own
supported authentication. Git/GitHub retain code and work records; private artifact
storage retains detailed evidence. No model-provider credential broker is introduced.

The CLI and controller are two responsibilities inside one executable. Keep operator
interaction separate from a deterministic, independently testable transition engine.
One interactive lead can be restarted while a separately owned bounded run continues;
manual bootstrap performs the same role separation without claiming runtime enforcement.

## Intended source layout as code is introduced

```text
cmd/baw/                 CLI entry point
internal/controller/     State transitions and permitted actions
internal/agents/         Native Codex/Claude adapters and capability checks
internal/state/          Durable run/session records and recovery
internal/git/            Repository, candidate, and worktree operations
internal/github/         Issue/PR context and explicitly authorized writes
internal/verification/   Gate execution and evidence validation
internal/environment/    Project profile operations, runtime identity, resource ownership
workflow/                Canonical protocol, roles, and reusable templates
docs/                    Operator, developer, design, and architecture documentation
```

Implemented so far: `cmd/baw`, `internal/cli`, `internal/inspect`, `internal/proc` and
`internal/state`, which currently holds only the immutable local
[run record](../operator/run-records.md) store; run/session recovery is still future.
`internal/inspect` serves both the fixed lead source list of `baw inspect` and the
per-request role source list of [`baw context`](../operator/context.md).
`internal/execution` implements [`baw run execute`](../operator/execution.md): one recorded
local worker and verifier attempt in its own `execute-v1` namespace, using the observed
runner of `internal/proc`; it has no native agent adapter, approval evaluation or recovery.
`internal/review` implements [`baw run review`](../operator/review.md): one recorded
reviewer-program attempt in its own `review-v1` namespace after a saved `verification_passed`
execute result, reading the execute receipts through read-only forwarders to the unchanged
execute validators and re-inspecting the candidate with `internal/inspect`; a program verdict
is an observation, not approval or verification of the candidate.

Do not create all empty packages before they are needed. Begin with the smallest tested
read-only slice. Interface/state choices require an approved specification before
writing mutating orchestration. The Go module-layout source is in
[the source register](../research/sources.md).

## Application execution boundary

Use one application-specific contract pointing to existing native/container/lab tooling,
not a universal deployment DSL. Sessions, workspaces, and runtime instances have distinct
identities/lifetimes. The environment integration tracks health, source/build/fixture
identity, required gates, human preview holds, and shared resource ownership. Resource
coordination must cover all projects/hosts using a lab; a checkout-local lock is not
sufficient. Provider automation is introduced only after the manual profile is validated.
See [execution environments](../developer/execution-environments.md).

## Access boundary

[Secret binding](../developer/secrets-and-access.md) belongs at the consuming operation,
using an inspected project mechanism and a nonsecret profile reference. It does not add
a provider API credential service or require all agents to inherit an app's environment.
Source/configuration identity and context generation must exclude secret values. Templates
contain names/placeholders, never credentials. Stronger isolation cannot be implied when
workers can read operator files, modify secret-consuming code, or use the host Docker socket.

[Single-account GitHub](../operator/github-single-account.md) is a product constraint:
model findings and operator authorization are distinct events, not separate GitHub principals.
Adopted routine scope can authorize verified merge/closure without a later human approval.
Keep PR/check integration independent from formal approving-review counts and stop before
the next ticket unless an explicit recorded standing delegation applies (see the single-account
policy). Release/production consequences and specific design holds remain separate.
An optional protected approval/execution context may later enforce human-only operations
without another GitHub account; it is not part of this seed or first read-only slice.

## Significant decisions

- [ADR 0001: one repository and executable](decisions/0001-one-repository-one-executable.md)
- [ADR 0002: durable state and replacement](decisions/0002-durable-state-and-replacement.md)
- [ADR 0003: documentation authority](decisions/0003-documentation-authority.md)

All three are Accepted under the operator's delegated
[bootstrap decision](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557).
Accepted design does not imply implementation; the remaining interface and runtime
details below still require their own specifications and decisions.

## Quality scenarios

A lead can disappear between tickets and a replacement reconstructs the next permitted
action from durable records. A lost worker cannot cause a concurrent replacement writer.
A stale review cannot accept changed code. A CLI update cannot silently alter an active
run's compatibility assumptions. A user can verify the documented CLI behavior without
understanding the internal Go packages. See [verification](../developer/verification.md).

## Unresolved design choices

Storage implementation, exact CLI/schema contracts, authentication/permission isolation,
minimum supported CLI versions, release platforms, and installed-policy materialization
must be decided through explicit issues and ADRs when significant. This page is not a
placeholder claim that these implementation details have already been solved.

## Completion and visual evidence boundaries

The controller finalizer uses existing scope authority plus actual independent review,
mandatory tests and current integration evidence; it does not ask a model to invent an
approval. Persist candidate/base/result identity, merge uncertainty, post-merge failures,
closure and the next-ticket stop or standing-delegation chain. Never install the candidate controller/policy as its own
judge. [Closeout](../operator/github-single-account.md) defines the operating conditions.

GUI evidence is produced by a project-specific validated browser/test path and consumed by
actual image-capable worker/reviewer tools. Document-only or DOM-only observations cannot
satisfy pixel inspection. This is an adapter/evidence contract, not a requirement to build
another browser automation framework. See [GUI verification](../developer/gui-verification.md).
