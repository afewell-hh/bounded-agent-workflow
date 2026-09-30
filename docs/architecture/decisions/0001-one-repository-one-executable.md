# ADR 0001 — one repository, one Go module, one executable

Status: **Proposed**. Adoption requires an operator-reviewed decision record.

## Context

The method, role materials, CLI behavior, and controller rules evolve together. Separate
repositories/releases would create synchronization work before independent consumers or
teams justify it. Consumers want an installed utility, not another Python environment
inside each application.

## Proposed decision

Keep method, controller/CLI source, templates, tests, and documentation in one tooling
repository. Build one platform-specific Go executable; keep the CLI front end and engine
as internal packages. Embed/export versioned shared assets, with explicit project policy
pins and reviewed migrations. Ordinary applications retain only their own configuration
and maintained knowledge, not controller source.

## Alternatives and consequences

A separate service/UI/repository could become justified by independent deployment or
ownership; it is unnecessary initially. Rust is a viable binary-delivery alternative,
but no requirement currently motivates choosing it over Go. A Python prototype can remain
external reference, not a dependency or an assumed specification. Developing the product
requires Go; using the released executable does not. A single repository does not permit
an unreviewed global policy update.

## Provenance and implementation

Proposal prepared for the operator's workflow requirements on 29 September 2026.
Link the actual discussion, acceptance record, and implementation PR when they exist.
