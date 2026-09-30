# ADR 0001 — one repository, one Go module, one executable

Status: **Accepted** on 30 September 2026 under the operator's delegated bootstrap
authority, recorded in [decision D1](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557).
This is design adoption; no controller is implemented.

## Context

The method, role materials, CLI behavior, and controller rules evolve together. Separate
repositories/releases would create synchronization work before independent consumers or
teams justify it. Consumers want an installed utility, not another Python environment
inside each application.

## Decision

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
The substantive decision was adopted as written in seed revision `10425c4`;
decision D1 records the original instruction source and the lead's delegated choices.
Implementation remains future work; this adoption does not establish a working executable.
