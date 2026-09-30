# ADR 0003 — separate temporary work records from durable knowledge

Status: **Accepted** on 30 September 2026 under the operator's delegated bootstrap
authority, recorded in [decision D1](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557).
This is design adoption; no controller is implemented.

## Context

Temporary plans, agent notes, and scattered issue comments can obscure current behavior
and decisions. Both people and fresh agents need a navigable, versioned account of the
product without importing the history of every task.

## Decision

Keep temporal planning/research in GitHub issues and run evidence outside the source
checkout. Promote significant accepted decisions to small versioned ADRs beside code.
Maintain human-readable user/developer/architecture documentation as shared knowledge.
Use thin agent entry points and role/procedure manuals, not a separate agent-only copy
of product facts. Include documentation impact in each work item and its review.

## Alternatives and consequences

Issue-only architectural decisions avoid files but force future readers to reconstruct
accepted rationale from mutable discussions. Duplicated agent manuals drift. Huge formal
specifications create maintenance load without proving quality. The chosen approach
requires active promotion, supersession, and documentation review; it does not require
an ADR for every implementation detail or an empty page for every document category.

## Provenance and implementation

Proposal prepared on 29 September 2026. The substantive decision was adopted as written
in seed revision `10425c4`; decision D1 records the original instruction source and the
lead's delegated choices. Implementation remains future work.
See [documentation policy](../../developer/documentation-policy.md) and its sources.

Use Diataxis as the default reader-oriented documentation framework and maintain
one canonical project environment guide. It does not require a four-folder rewrite,
replace normal ADR/task formats, or turn agent-specific instructions into duplicate
product manuals. Adoption does not establish implemented documentation tooling.
