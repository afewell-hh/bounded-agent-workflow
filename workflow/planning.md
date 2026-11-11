# Planning and progress discipline

Audience: leads, workers and independent reviewers. Use this procedure to keep bounded
work connected to an observable product outcome. These are manual responsibilities;
the existing BAW commands do not enforce plan freshness or select work.

## One shared delivery plan

Keep one current, versioned plan in a GitHub issue, linked from the coordination issue.
Use the [project-plan template](templates/project-plan.md). Reuse an existing adequate
plan rather than creating another status system. Keep durable product requirements and
technical knowledge beside the code; keep temporal sequencing and forecasts in GitHub.
For a small standalone task, its issue may contain the complete plan.

The plan records:
- A north star: who benefits, what they can do, and observable success evidence.
- Explicit non-goals, supported cases, compatibility obligations and reserved decisions.
- The verified product baseline, with missing evidence distinguished from failure.
- A short dependency-ordered path to the next usable result and eventual acceptance.
  Separate mandatory prerequisites from optional improvements; explain why each
  prerequisite blocks the result. Prefer an early representative real journey over
  accumulating infrastructure before testing the product.
- The next real proof: inputs, environment, expected outputs, evidence owner and dated
  checkpoint. Include regression/known-good comparisons where an existing product works.
- Finite effort/attempt limits, cumulative enabling work, forecast range and uncertainty,
  plus review ownership. A forecast is not a promise or permission to exceed a cap.

Do not create a detailed speculative ticket forest. Describe later milestones briefly;
freeze the next bounded ticket only when its dependencies and acceptance are ready.
Planning effort must itself have a time box and a decision/output. More documentation,
test counts, accepted reviews and closed tickets alone do not establish product progress.

## Before dispatch or extension

The lead records the current plan revision, the milestone advanced, and why this ticket
is the shortest credible permitted step toward the next proof. Name its observable
result, maximum effort/attempts, checkpoint and stop conditions. Explain any required
enablement and what work is deferred. Respect the existing scope/authority chain.

Check actual Git, candidate, environment and evidence against the plan. Unknown ownership,
exhausted budget, unresolved holds and missing mandatory fidelity remain blockers.
A blocked critical path can justify bounded, useful parallel work only within existing
authority; record why it will matter and its cap. Do not fill waiting time with unrelated
hardening. A planning review cannot waive gates, authorize a retry or change requirements.

## Progress reviews

Use the existing coordination/ticket receipts, not a second diary. Review at dispatch,
ticket closeout, before any extension, and at least once per active workday. A long ticket
also has a checkpoint no later than half its planned effort budget. Record the next
checkpoint with a date/time; inactive projects need no background activity.

At each review compare actual evidence with the prior checkpoint:
1. What product behavior is newly demonstrated, at what fidelity and candidate?
2. What blocker was removed, and what remains on the critical path?
3. What effort/attempt budget was consumed, including predecessor and successor tickets?
4. Is the next proof still achievable within its forecast and authority?
5. Continue, change the plan, or escalate: name one next action, owner and checkpoint.

Use a short, time-boxed review (default 15 minutes; record a justified different bound).
The lead owns the record. A fresh independent reviewer challenges the initial plan for
a multi-ticket project, material replans, and the drift triggers below. Give that reviewer
the objective, prior plan, actual evidence and cumulative effort, not only a completion
narrative. Review is independent cross-model assessment under the existing role rules;
it does not require another GitHub identity or grant approval. Routine reviews and
in-scope resequencing use existing delegation without repeated human confirmation.
Missing reviewer capacity blocks the affected new dispatch, not safe preservation or
completion of already authorized work.

## Drift triggers and required disposition

Pause new dispatch for a plan review when:
- A dated real-proof checkpoint is missed, a forecast materially slips, or a ticket is
  projected to exhaust its cap before producing its promised result.
- Two consecutive completed tickets provide only enabling/fixture evidence and no new
  product evidence at the required fidelity.
- The same blocker or repair recurs without measurable improvement, including across
  renamed tickets, agents, sessions or orchestration layers.
- New evidence contradicts the plan's assumptions, supported cases or known-good behavior.

These are default review triggers, not automatic cancellation or permission to run two
unnecessary tickets. Projects may adopt tighter triggers; changing them is an explicit
plan decision within authority, never an unrecorded bypass after failure.

The independent challenge asks whether the next step removes a demonstrated blocker,
whether existing tooling or a simpler path can produce the proof sooner, and whether
optional hardening has displaced it. Retain mandatory safety/verification requirements.
Record a concrete disposition: shorten/resequence scope, test a risky assumption first,
use a known-good comparison, pursue an authorized alternative, or escalate a specific
decision. Necessary prerequisites can continue with a reasoned dependency explanation,
a finite cumulative cap and a new dated real-proof checkpoint. Do not merely rename the
same work, reset consumed budget or move a deadline without explaining the evidence.
If no credible bounded path remains, report that fact and the alternatives to the operator;
do not keep dispatching to appear busy.

## Completion and handoff

Closeout distinguishes product evidence, enabling evidence and remaining unverified
acceptance. An accepted ticket is not a qualified workbook, supported product family,
release or deployment. Record the delta against the north star, remaining critical path
and revised forecast; do not convert fixture passes into integrated success.

Handoff includes the plan revision, last and next review, missed checkpoints, cumulative
effort/attempts, independent findings and permitted next action. A replacement preserves
all caps, holds and uncommitted work. Material changes to intent, scope, policy, spending,
release/production effects or reserved decisions follow existing operator authority;
a fresh plan or review supplies none of it.
