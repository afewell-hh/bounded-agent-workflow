# ADR 0002 — roles survive; native agent sessions are replaceable

Status: **Proposed**. Adoption requires an operator-reviewed decision record.

## Context

A lead may coordinate several tickets but eventually needs replacement or may disappear.
Native compaction, resume, fork, and subagent lifecycle differ across clients/versions.
Session continuity must not be required to preserve approved scope or prevent retries.

## Proposed decision

Store authoritative scope, approvals, budgets, candidates, evidence, and role assignment
generations outside conversations. Maintain a small GitHub coordination index linking
actual records. Replace a role by reconciliation and activation of a fresh generation;
retire the old director and resolve child/workspace ownership. Native handles can assist
but are not the portable state model. A cold takeover must work without a final summary.

## Alternatives and consequences

Native resume is useful for a known same-ticket continuation, but retains history and
cannot substitute for freshness or be assumed cross-provider portable. A continually
summarized lead diary creates another drifting authority. Frequent durable checkpoints
cost some work, but reduce dependence on a departing session. Unknown external side
effects require a blocked reconciliation; instantaneous transparent recovery is not
promised. Same-account session checks remain procedural without stronger isolation.

## Provenance and implementation

Proposal prepared on 29 September 2026. Link the actual approval and implementation.
See the maintained [lifecycle procedure](../../operator/agent-lifecycle.md).

Takeover reconciles recent commits, staged/unstaged/untracked
work, and runtime/resource ownership. Agent, workspace, and environment lifetimes are
distinct. Saved-but-uncommitted changes and a still-running remote job are not erased
by losing a session. See the lifecycle and execution-environment contracts. This remains
Proposed pending adoption; it does not assert implemented recovery or fencing.
