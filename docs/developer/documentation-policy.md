# Documentation policy

## Purpose and authority

Documentation should enable a new user to operate the product and a new contributor or
agent to maintain it. It must not become an archive of every development conversation.
This policy uses documentation categories as a coverage guide, not a requirement to
create an empty folder or a long document for every category. See [Diataxis and arc42
sources](../research/sources.md) for background.

| Information | Canonical home | Update trigger |
|---|---|---|
| Backlog, sprint selection, temporary research, investigations, blockers | GitHub issue/project view | Relevant work transition |
| Proposed/accepted scope and human approvals | Specific issue/PR record with revision and provenance | Scope or decision change |
| Current handoff/navigation | One coordination issue indexing actual records | Durable milestone/takeover |
| Significant accepted architectural rationale | Versioned ADR under docs/architecture/decisions | Decision acceptance/supersession |
| Current system structure, boundaries, runtime/deployment behavior, invariants | Versioned architecture/contract docs | Corresponding behavior change |
| User tutorial, task how-to, reference, explanation | User/operator documentation | Interface or behavior change |
| Setup, build/test, extension, contributor conventions | Developer docs and CONTRIBUTING.md | Developer experience change |
| Runtime profiles, change propagation, isolation and preview/lab procedures | One maintained project environment guide linked from agent entry points | Supported setup or runtime operation changes |
| Agent role, onboarding, evidence/stop procedures | workflow/ and thin native entry points | Reviewed process change |
| Full logs, traces, screenshots, run snapshots | External run/approved private artifact store | Run/verification event |

GitHub issues are not the only form of GitHub-hosted knowledge. Accepted ADR files are
also accessible on GitHub, while remaining versioned with the relevant code. Keep
exploration and debate in the issue/PR, then promote the durable decision. Do not force
future maintainers to reconstruct an accepted contract from a long comment thread.

## Named framework: Diataxis, applied to the reader's need

Use **Diataxis** as the default organizing and writing framework for user, operator, and
contributor documentation [S11-S12, S28]. Do not rely only on a model claiming familiarity:
read its short primer and the relevant category/compass guidance when drafting a new
kind of page or resolving a structural question. It is not necessary to reread the
whole framework per ticket or paste it into agent instructions.

Classify the audience and dominant need before drafting: learning a practical skill
(tutorial); completing a known task (how-to); looking up precise behavior (reference);
or understanding rationale (explanation). Keep those purposes distinct and cross-link
supporting material. They are content purposes, not mandatory directory names. Improve
existing useful pages incrementally rather than imposing four empty folders [S12].

Project-specific application rules:

- A tutorial provides a safe, working learning path with concrete prerequisites and
  observable checkpoints. Put optional theory in linked explanation rather than
  interrupting the walkthrough with long architectural digressions.
- A how-to names the actual task, starting state, exact actions, expected outcome, and
  recovery for relevant failure conditions. Mark host/checkout and destructive effects.
- Reference stays precise about implemented options, defaults, errors, schemas, versions,
  and compatibility. Prefer a single canonical/generated source where practical.
- Explanation covers the reasons, boundaries, consequences, and alternatives. It is not
  a substitute for either an executable procedure or an exact interface specification.

Use plain, direct prose and concrete application terms; define unfamiliar acronyms.
Separate shell commands, native-agent slash commands, and natural-language prompts.
State where each command runs, which permissions it needs, and which values are examples.
Avoid vague "standard setup", untested "works", and unsupported claims about releases.
Instructions should minimize operator mechanics, not omit important safety conditions.

Diataxis does not decide workflow authority, approval, retention, or lifecycle. Keep
ADRs in their concise decision format, task state in GitHub, and exact run evidence
outside the checkout. These local rules complement the framework rather than replace it.
A reviewer checks whether a real reader can achieve the intended outcome, not merely
whether the document uses the four names. Linked guidance is a reference, not permission
to change project policy if the external site later changes.

## Minimum useful coverage, grown as needed

Product documentation should eventually cover: a working first-use tutorial; how-to
procedures for normal operation and recovery; exact CLI/configuration/API reference;
and explanations of the workflow, limitations, and tradeoffs. Choose actual pages as
features become implemented, not hypothetical manuals claiming planned commands work.

Developer documentation should cover reproducible setup and tests, architecture and
module boundaries, interface/state schemas, adapter extension, invariants and failure
modes, release/compatibility procedures, and security assumptions. Add deployment,
operations, accessibility, migration, and performance guidance when applicable. A
maintained glossary is useful when domain vocabulary is material to implementation.

The architecture overview can use arc42 concerns and simple C4-style context/component
views without copying a full consulting template. Include quality scenarios and known
risk/technical-debt links where consequential. Do not count pages as a quality metric.

## Agent-specific material is an operating layer

Keep shared product facts in normal human-readable documentation. AGENTS.md is a short
map and a set of always-applicable rules. Role manuals describe authority, onboarding,
work products, evidence, handoff, and stopping—not a second description of the app.
Native wrappers/configuration adapt this canonical material to actual installed clients.
Audit what is loaded, including ancestor/user files and optional auto memory.

No hidden agent memory is an authority for requirements, approval, policy, or project
status. Useful lessons are proposed in an issue and reviewed into an existing test,
contract, guide, or rule. Remove duplication instead of accumulating new lesson files.

## Documentation is part of definition of done

Each ticket includes documentation impact by audience and names likely pages. The
worker updates affected docs alongside the code; the reviewer checks actual behavior,
examples, placement, and proposed/implemented distinctions. Not applicable is permitted
with a concrete reason, not a blanket checkbox. No extra human gate is needed for an
ordinary documentation correction already within the approved feature scope.

For CLI/API/schema changes, update the authoritative contract and derived reference
material. Test executable examples in an isolated fixture when practical. Documentation
builds/link checks help but cannot prove a description is true. Browser acceptance must
exercise the real UI path; screenshots alone do not verify persistence or backend work.

Documentation for the development branch can describe that candidate; public release
documentation must identify the released version. Unreleased proposals belong in marked
design documents or issues, never disguised as currently working user commands.

## ADR lifecycle

Use small records for significant structural, interface, dependency, quality, security,
or construction choices. Include status, context, decision, alternatives/tradeoffs,
consequences, and provenance. Proposed is not Accepted. The operator/design authority
explicitly adopts it. Link its implementation and any evidence separately.

Never recycle ADR identifiers. Supersede decisions explicitly and link replacements;
preserve the old rationale rather than rewriting history. An accepted ADR is a design
decision, not proof its implementation exists or works. Keep the current architecture
view coherent with the accepted decisions and actual implementation status.

## Hygiene and ownership

The worker owns ticket documentation changes; the reviewer checks them; the lead checks
cross-ticket consistency. A separate permanent documentation agent is unnecessary.
Complex or specialist documentation can be its own approved assignment.

New durable pages require a clear audience/purpose and a link from the relevant index.
Do not add TODO.md, session diaries, sprint notes, or generated progress reports to the
repo. Reusable empty templates in workflow/templates are allowed; filled instances go
to the issue/run records. Local scratch belongs outside the source checkout.

Periodically consolidate stale/duplicate material and check links. Do not delete old
ADRs merely because they are superseded. Keep historical decision records distinct from
current operational instructions and from obsolete throwaway planning.

## Access information belongs in references, not prose values

Use [secrets and access](secrets-and-access.md). Explain required names, profiles, approved
consumers, target scope, setup and recovery without values or real authentication headers.
Examples use conspicuous placeholders/dummy data. Link transient private remote-state
records rather than embedding live CMS contents, session cookies or tokens in docs.
Single-account [GitHub rules](../operator/github-single-account.md) are an explicit operating
constraint; do not turn independent model review into a second-user approval requirement.

## Completion language must match the evidence

Routine development tickets may close after verified merge under the adopted policy without
human demo/merge approval. Do not write "operator accepted" or "human visually verified"
when that did not happen. Distinguish scope-authorized, technically verified, independently
reviewed, merged/completed, awaiting next scope, and released/deployed. Document GUI claims
against actual inspected images and exercised paths using [GUI verification](gui-verification.md).
A component prototype/mockup is not an integrated or released feature.
