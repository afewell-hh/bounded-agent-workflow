# Shared execution protocol

**Purpose:** support the same bounded workflow manually now and through a controller
later. The procedure is adopted only within the scope the operator authorizes. A
future executable must enforce its specified checks; prose alone is not enforcement.

This repository adopted the seed baseline at `10425c4` for bounded manual bootstrap
under [decision D1](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557).
Use [the single-account authority-recording convention](../docs/operator/github-single-account.md#record-operator-authority-with-one-account)
to distinguish original operator instructions, delegated choices, model findings and
verified observations. Adoption does not establish implemented enforcement.

## Authority and durable state

Git records code and versioned product/architecture/process documents. GitHub issues
record work proposals, research, approvals, progress, and outstanding decisions; pull
requests record changes and review. Runtime records identify actual attempts, processes,
verification, and candidates. The coordination issue indexes these sources. None may
silently override a conflict in another category: flag the conflict and reconcile it.

Keep proposals separate from accepted decisions and verified observations. A summary
must name its sources and last verified code/run revision. Material scope changes need
renewed approval. Do not mutate a frozen ticket body as a progress mechanism.

## Normal lifecycle

1. The lead prepares one phase/ticket with enough context for a new agent.
2. A readiness check confirms inputs, observable acceptance, scope, and verification.
3. The operator approves that particular scope, budgets and completion mode. For routine
   implementation this includes the defined push/PR, merge and closeout authority. Under
   a valid explicit [standing delegation](../docs/operator/github-single-account.md#standing-program-delegation-opt-in),
   the lead instead selects and freezes the ticket and records its chain (operator
   instruction -> delegation -> lead-selected ticket) before dispatch.
4. The worker implements only the authorized phase in its assigned workspace.
5. Required verification runs on an identified candidate. Independent review assesses
   the original requirements, code/artifact, tests, documentation, and evidence.
6. In-scope fixes stay in the same run lineage and finite budget. Changed requirements,
   missing authority or unsafe state stop for the operator. An exhausted cap stops
   dispatch; only a valid standing delegation lets the lead record a finite extension
   instead of asking. Repeated failure without progress needs diagnosis, not retries.
7. For routine implementation, the coordinator confirms all gates and the current
   integration context, merges through the approved protected path, verifies actual
   closeout, updates records, and closes the ticket. No per-merge human approval is needed.
8. Report the completed result and, by default, STOP before another ticket starts. The operator may
   authorize the identified next scope in one response; under valid standing delegation
   the lead may select the next ready in-scope ticket after recording this outcome.
   Reserved decisions still go to the client. Unresolved design holds and
   consequential exceptions stop earlier for the specific decision, not a ritual sign-off.

Use [the closeout policy](../docs/operator/github-single-account.md) for exact conditions.
Research/architecture/design artifacts retain their agreed decision gates; an issue
containing a proposal is not an adopted product decision. Releases, deployment and live
external changes do not follow from routine integration permission.

For the pilot, implementation allows one initial attempt plus at most two repair
attempts. This is an adjustable operating choice, not an established optimum. A crash,
replacement, rename or successor ticket does not erase attempts or uncertainties. High-risk
work and changes to the governing process remain explicitly supervised; a candidate
policy never governs its own review, budget or merge.

## Durable checkpoint rule

Record accepted decisions before acting on them. Update a compact receipt at scope
readiness, dispatch, candidate/review completion, blockage, acceptance/merge, and planned
handoff. Do not save full conversations. An abrupt crash may lose unrecorded reasoning;
the replacement must reconstruct or mark it unknown rather than inventing it.

## Runtime readiness and project adoption

Use [the adoption procedure](../docs/operator/project-adoption.md) without replacing an
existing project's working standards. Before feature execution, establish the relevant
[environment contract](../docs/developer/execution-environments.md) and actual runnable
application path. Environment-enablement/scaffold tasks are explicit bounded exceptions
when the application does not exist yet; they do not establish feature completeness.
Tickets select profiles and required gates. Mocked/local success cannot substitute for
required integrated or live-lab verification. Resource waiting is not a passed gate.

Runtime ownership belongs to the run/operator, not an agent conversation. Preserve
previews and shared-resource reservations across replacements. Do not conflate a Git
worktree, a container, and a security boundary. Multiple approved workstreams must obey
one-writer-per-workspace and one-mutating-owner-per-shared-resource rules. A ticket
approval never implicitly approves destructive reset, new spending, or public exposure.

## Secrets and GitHub authority

Use [secret bindings](../docs/developer/secrets-and-access.md) in the existing environment
contract. Supply only the approved profile to its consumers; references and availability
belong in task context, never values. Changes to a credential's target, privileges, or
sharing need explicit authorization. Direct CMS/API changes require named remote scope,
pre/post-state evidence, and a recovery procedure; Git alone cannot roll them back.

Use [one GitHub account](../docs/operator/github-single-account.md) without requiring a
second reviewer identity. Human scope approval can preauthorize conditional routine
merge; independent model findings and verified completion are not claimed human inspection.
Preserve mandatory tests; do not invent self-approval, change Git author names to evade review, or silently amend inherited protections. Routine issue updates may
use standing scoped authorization. A model-written approval comment is not human approval.
Same-user local gates are procedural unless an inaccessible authority boundary enforces them.

## Replacement and helpers

Use [the lifecycle contract](../docs/operator/agent-lifecycle.md). Replacing a role
changes its session identity, not the project intent or execution authority. Old
sessions must be retired or made advisory; only the active role generation may direct
new controlled work. Until a controller enforces generation checks, this is manual
single-owner discipline, not a claimed security boundary.

Helpers get explicit bounded questions, input revisions, permissions, and output
requirements. They return findings and evidence, not approvals. Record their existence
before dispatch, collect results, and stop/join them before their parent retires. Never
assume one provider's subagent lifetime or inheritance rules match another's.

## GUI evidence

Follow [GUI verification](../docs/developer/gui-verification.md). Separate approved intent
from actual conformance: real browser journeys, pixel inspection by worker and independent
reviewer, controlled visual baselines, integrated behavior and candidate provenance. Put
human design input early where useful; do not add a mandatory human approval to every GUI
merge. Missing visual access/evidence is blocked, not inferred success.

## Quality and learning

Use [documentation policy](../docs/developer/documentation-policy.md) and
[verification requirements](../docs/developer/verification.md). A failed review or human
scenario is not fixed by changing its definition of success. Durable lessons become a
reviewed correction to an existing contract, guide, or test—not a new memory dump.
