# GitHub with one account: finish the ticket, then stop

**Status:** adopted for this repository's bounded manual bootstrap under
[decision D1](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557),
which records operator-delegated authority and its source. Adoption elsewhere remains
explicit. No setting, merge automation, release guard, or executable is installed by this seed. The operator
and authorized agents may share one GitHub identity. A second approving account is not
required. Retain independent cross-model review and every mandatory verification gate.

## The ordinary authorization is a complete ticket

After project adoption, approving a routine implementation ticket includes permission to
make its in-scope commits, push its branch, open/update its pull request, run approved
checks, merge the verified result into the named **non-production integration branch**,
update documentation/status/evidence, and close the ticket. No separate human code review,
per-commit approval, final demonstration approval, or merge-button click is required.
The ticket records the adopted policy revision, repository/target, and exceptions; the
lead fills these in. The operator is not asked to retype the policy for every ticket.
Reading or committing this seed does not itself grant repository write permission.

The normal sequence is:

```text
Human approves one ready ticket (including its routine closeout authority)
  -> implementation + documentation
  -> required candidate verification + independent cross-model review
  -> bounded repairs and revalidation when needed
  -> integration-context checks + conditional merge to the approved target
  -> confirm actual merge and required post-merge health
  -> update evidence/status, close the completed ticket, report
  -> STOP before executing another ticket (unless valid standing delegation applies)
```

Without [standing program delegation](#standing-program-delegation-opt-in), this is the
default: a concise completed-ticket report and a proposed next scope form the normal human
checkpoint. A single response such as "proceed with the proposed next ticket" may approve
that identified next scope; it is not retroactive technical approval of the prior commit.
No response means no next-ticket implementation. The lead may summarize/prepare the next
proposal, not launch its research, implementation, or helpers without that scope's authority.

## Standing program delegation (opt-in)

This is the canonical rule; other documents link here. Per-ticket operator approval stays
the default for every project. An operator may instead give an explicit, bounded standing
delegation for a project or program. Record it as an attributed receipt (see
[recording authority](#record-operator-authority-with-one-account)) naming:

- the operator source and the lead's announced interpretation;
- the product goal and scope boundaries;
- allowed routine actions, repository/target and environment profiles;
- reserved decisions (below) and revocation/stop conditions.

Under a valid delegation the lead may, without asking the client again:

1. **Select the next ticket.** Only after the previous ticket's outcome and closeout are
   recorded, select one ready, bounded, in-scope ticket, freeze it, and record its
   authority chain before dispatch: original operator instruction -> standing
   delegation receipt -> lead-selected ticket, scope revision and budget. The ticket
   and budget are lead-selected and are never presented as operator-authored approval.
   Never dispatch an unbounded multi-ticket worker.
2. **Manage ordinary repairs.** Run in-scope repair rounds within the ticket's finite
   budget. Before any dispatch beyond the current cap, the lead may record a finite
   in-scope extension stating the original and current caps, cumulative counts,
   rationale and delegation receipt. All spent attempts stay in the same lineage across
   replacement, renaming or a successor ticket; nothing resets them. An exhausted current
   cap STOPS dispatch until the lead reconciles and records such an extension or
   escalates. Workers never extend their own budget. Repeated failure without progress
   requires diagnosis and a changed plan, not another extension of the same strategy.
   Review findings never waive a cap or a gate.

Each changed candidate is still frozen, reverified and freshly reviewed. Progress or
milestone reports are informative; they are not claimed human approval.

Delegation never covers these reserved decisions, which need specific client authority:
material scope or product-intent changes; meaningful spending; public exposure, releases
or deployments; destructive, production, shared-lab or live CMS effects; governing-policy
changes; protection/settings or credential/permission changes; toolchain changes. Ask in
plain language. Existing native subscriptions, local toolchain and GitHub account remain
the consumers; delegation adds no API-billing fallback, new account or admin bypass.

Missing, ambiguous, conflicting, revoked or out-of-scope delegation fails closed: stop and
use the per-ticket default. A new session, label, model finding or ticket closure grants
nothing, and a restarted lead inherits the same counts and ownership, not new authority.
Without applicable delegation an exhausted budget and the next ticket both need the
operator. These rules are manual procedure; `baw inspect` neither grants nor enforces them.

## Record operator authority with one account

Operator instructions, agent-selected choices, model review, and observed results are
different records even when all GitHub actions use the same account. Every agent-posted
issue body, comment, review finding, or receipt begins with
`AGENT | role | generation | native-session`. Use `unavailable` when a native session
identifier is not exposed; a PID is an observation, not that identifier.

Reserve the `OPERATOR DECISION` form for the operator. Agents never post that form or
claim that their GitHub comment was written by the operator. The operator may instead
approve a bounded scope or expressly delegate bounded choices in the active native
conversation. In that case an agent posts an attributed receipt, retaining:

- The exact relevant operator instruction and an original message/event reference,
  timestamp and native session identifier where available. Preserve only a minimal
  nonsecret excerpt privately; do not copy a full conversation or imply local evidence
  is remotely available.
- The exact issue/scope revision and source-policy revision; selected completion mode,
  target, budget, exceptions and expiry; and which choices were delegated to the agent.
- Separate observations, unresolved facts, model recommendations and agent-selected
  decisions. Delegated judgment cannot establish an unknown process owner or prove
  that an external consumer is absent.

A receiving lead reconciles the original source, receipt and current records. An agent
paraphrase, another model's review, an unlabelled comment or a matching GitHub username
alone does not establish human authority. Missing or conflicting source evidence blocks
the affected transition. These conventions and same-user source records are procedural;
they are not tamper-proof authentication or a new permission boundary.

Freeze an approved scope body. Record its creation/last-edit state and content identity
with the decision; use progress comments afterward. A later body edit requires scope
reconciliation and any material amendment needs authority. The coordination index links
the original records instead of reproducing an approval as if it were a new one.

Keep temporary protection exceptions and their precise expiry in GitHub decision records,
not this maintained policy. Expiry blocks affected work and cannot be extended by agent
replacement. Independent review and required verification remain required, as does the
next-ticket stop unless a valid [standing delegation](#standing-program-delegation-opt-in)
applies. Routine merge authority follows the approved scope across replacement; it is
not renewed merely because the lead changes.

## Decide exceptional checkpoints before execution

| Work | Normal disposition |
|---|---|
| In-scope implementation with established criteria; no production effects | Agents/controller verify, merge, close, then wait (or apply a valid standing delegation) |
| GUI implementation of an approved interaction/design | Same, with actual rendered-image and integrated-journey evidence |
| New/ambiguous UX, subjective design choice or unresolved acceptance | Obtain the specific design decision, preferably at prototype/story stage |
| Publish a release, deploy/change production, mutate a live CMS, destructive data operation, change credentials/permissions or governing policy | Separate explicit scope/decision for the consequential action; never inferred from routine merge authority |
| Missing evidence, exhausted repairs, unexpected side effect, conflicting rule or uncertain operation | Block/reconcile; do not ask for a ritual approval that waives the missing check |

A ticket may specify `operator-design-hold` or a narrower completion rule when needed.
Routine tickets use `routine-integrate`. These names describe proposed policy, not an
implemented configuration schema. A check failing does not let the agent relabel its
own ticket or remove the hold. Existing stricter constraints need an approved amendment.
The operator can inspect a live preview at any time without making that inspection a
mandatory checkpoint on every GUI change. See [GUI verification](../developer/gui-verification.md).

## Audit the integration/release boundary during adoption

Do not assume that a branch named `main` is non-production. Inspect effective repository
rules, workflows and external integrations: branch pushes/PRs, reusable or chained jobs,
package/container publication, auto-release/tag tooling, hosting hooks, GitOps watchers,
installed consumers following a branch or mutable `latest`, CMS actions, and shared
services/databases. Unknown downstream effects block routine merge authorization until
resolved. The audit is read-only first; changing automation needs explicit approval.

Keep ordinary integration separate from release publication and deployment. Use fixed
release tags/artifact identities for consumers. GitHub immutable releases protect the
associated tag and uploaded assets after publication; their title/notes and latest marker
remain editable [S45]. Therefore "use releases" also needs a defined consumer/update
policy, not just a release page. Audit target-specific availability before configuring it.
A release does not freeze a shared API/database or protect an external service from an
authorized development operation; environment isolation still matters.

Git reverts record new commits reversing specified source changes [S52]. They do not
undo CMS publication, database mutations, sent notifications, exposed credentials, or
other external effects. Retain backups/recovery where needed. Do not force-reset a shared
branch, rewrite history or move release tags as routine rollback. A narrowly defined,
source-only revert may be preauthorized by project policy; otherwise propose bounded
recovery and pause. Required quality gates remain required even on unreleased code.

## Independent review is not another GitHub identity

The reviewer assesses original criteria, exact code, tests, documentation and evidence.
Its findings identify the candidate and are recorded as model review, not claimed human
acceptance. Do not call `gh pr review --approve` on a PR authored by the same account:
GitHub disallows author self-approval [S36]. Changing `git config user.name` changes commit
metadata, not the authenticated GitHub user [S37].

The reviewer reports a technical disposition; it does not grant new authority. The
controller, or the lead acting as manual closeout coordinator, checks the adopted ticket
authority plus completed gates. This permits a merge already authorized by the operator
without inventing a fresh human approval. Workers do not substitute self-review for this.

## Target solo-account configuration

Where supported and explicitly adopted, require pull requests and actual mandatory checks,
with zero required GitHub approving reviews. Do not introduce code-owner/last-pusher or
deployment self-review requirements that demand a second account. Audit all effective
organization/repository rules and plan limits; do not silently remove inherited controls,
forge approvals, or use administrative bypass as the normal path [S39,S40,S41].

Prefer controller-coordinated conditional merge after all BAW gates pass. GitHub native
auto-merge is an optional mechanism [S43], not a substitute for independent review, GUI
evidence or project-specific gates. Do not arm unattended auto-merge while a mandatory
BAW condition is represented only by an unchecked comment or pending local test. Retaining
native auto-merge across changes requires every relevant gate to be bound to the current
candidate; otherwise cancel/reconcile and use the explicit conditional-merge path.

Strict up-to-date checks or an appropriate merge queue address target-branch integration
changes [S40]. Do not add a queue merely for a one-ticket pilot. Required-check names,
sources, permissions and actual suite execution must be audited: GitHub may accept a
skipped/neutral status, which is not BAW evidence that a mandatory test ran [S40].

## Closeout details handled by agents/controller, not the operator

1. Confirm approved scope/policy/repository/target, unresolved holds, review disposition,
   test evidence and GUI evidence when applicable. Freeze the candidate being finalized.
2. Confirm actual PR head and current target/integration context. A changed head or
   integration result invalidates affected evidence; reverify/review as required within
   the existing budget. In-scope revalidation is not a fresh human approval requirement.
   Scope changes, ambiguous conflict resolution or exhaustion require a decision
   (for exhaustion, a delegated finite extension where the canonical section allows it).
3. Merge through the normal protected PR path. `gh pr merge --match-head-commit` supports
   an expected head check [S44]; it alone does not pin the target branch or prove all BAW
   gates. Use effective strict checks/queue or another validated integration strategy.
   Never use `--admin` to evade failure. Preserve policy/CI independence from the candidate.
4. Record the reviewed head, verified integration/tree/build, actual resulting merge commit
   and merge method. Squash/rebase may create different commit identities: verify the
   relationship and resultant content rather than falsely claiming identical hashes.
5. Confirm server-side merge and configured post-merge health/verification. An accepted
   queue request or successful HTTP submission is not proof of a completed merge. Avoid
   auto-closing issue keywords when post-merge gates could still fail. If already closed
   early, correct the status; `MERGED_BLOCKED` is not `COMPLETED`.
6. Update the issue/coordination index and retain evidence and any operator-held preview.
   Report the behavior, evidence links, actual merge, exclusions/risks, and proposed next
   ticket. Record `COMPLETED_AWAITING_NEXT_SCOPE`; it does not imply human inspection,
   a release, deployment, or permission to start the next task. Only a valid
   [standing delegation](#standing-program-delegation-opt-in) lets the lead select the
   next ticket after this record.

On disconnect/timeout, query the actual PR/target/check state before retrying. Preserve
attempts and uncertain effects. A merged result does not need another merge; incomplete
post-merge validation or record publication must be recovered rather than called complete.
Waiting on CI or a queue must be bounded and observable, never an infinite retry allowance.

## Manual operation now; technical guarantees later

During bootstrap the operator still relays worker/reviewer messages. Once ordinary
closeout authority is adopted for a ticket, the lead can perform that sequence without
another merge approval. Initial methodology adoption, live smoke tests and consequential
policy changes remain explicitly supervised. The implemented commands are the
read-only [`baw inspect`](inspect.md) and [`baw run create` / `baw status` / `baw run diagnose`](run-records.md),
which save, display and read-only diagnose a local record of supplied references, and the read-only
[`baw context`](context.md) role reference list, and [`baw run execute`](execution.md), one
recorded trusted local worker and verifier attempt; they grant no authority.
Controller, dispatch, recovery, gate and merge surfaces remain future work.

Native checks and explicit scopes reduce ordinary drift, but full shared-account/OS
access may allow bypass. A separate terminal or writable approval file is not a strong
human-identity boundary. Stronger deployment separates credentials, policy, trusted checks
and approval channels from workers while still allowing the same GitHub account to own
the action path. Do not add a hosted broker, bot or new account as a bootstrap requirement.

Future tests must cover one-account successful closeout, zero intermediate human merge
prompts, next-ticket blocking, current-head and base races, missing checks, missing visual
evidence, exception holds, live-target detection, post-merge failures, interrupted merges,
and unchanged published release identity. Start with fixtures; use an explicitly authorized
disposable PR to verify actual settings. Offline fixtures do not prove live enforcement.

Use [adoption](project-adoption.md), [manual bootstrap](manual-workflow.md), and
[secrets and access](../developer/secrets-and-access.md) for their respective boundaries.
