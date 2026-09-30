# ADR 0003 — separate temporary work records from durable knowledge

Status: **Proposed**. Adoption requires an operator-reviewed decision record.

## Context

Temporary plans, agent notes, and scattered issue comments can obscure current behavior
and decisions. Both people and fresh agents need a navigable, versioned account of the
product without importing the history of every task.

## Proposed decision

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

Proposal prepared on 29 September 2026. Link the actual approval and implementation.
See [documentation policy](../../developer/documentation-policy.md) and its sources.

Use Diataxis as the default reader-oriented documentation framework and maintain
one canonical project environment guide. It does not require a four-folder rewrite,
replace normal ADR/task formats, or turn agent-specific instructions into duplicate
product manuals. This remains Proposed pending adoption.
