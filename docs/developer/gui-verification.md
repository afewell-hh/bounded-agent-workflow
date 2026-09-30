# GUI delivery: observed rendering, not inferred appearance

**Status:** proposed project method and verification requirements. No browser runner,
visual inspector, Storybook project, Figma connection or baseline store is configured by
this seed. Select and validate the actual tools during project adoption. This method
supports routine autonomous ticket closeout; it does not require human code review.

## Two independent questions

1. **Intent:** Is this the interaction and appearance the operator wants?
2. **Conformance:** Does the actual integrated application deliver that approved experience?

Use the operator for unresolved product/design decisions. Agents own implementation,
real-browser exercise, inspection of rendered output, evidence, and independent review.
Neither a passing API test nor matching code intent establishes the GUI's appearance.
No finite suite or model inspection proves every possible user's experience; specify
supported states/environments and declare what was actually verified.

## Experience first, contracts early, visual implementation separated

Define the user journey, state transitions and failure behavior before large backend or
GUI investments. Refine a low-cost sketch/mockup or interactive component/page prototype
when the experience is new. Agree relevant API/application contracts and representative
fixtures; verify backend semantics through the API/CLI independently of visual refinement.
Then build the visual layer against those contracts and integrate a thin real journey
early. Do not defer all GUI learning until the entire backend is complete.

Storybook can render controlled component states and run interactions [S50]. Use it when
appropriate for the framework; a simple fixture page/prototype is acceptable for projects
where Storybook adds needless infrastructure. Figma or another supplied design can express
intent, but is not evidence of the implemented application. Keep reference revisions and
expected states explicit. Do not require a Figma account/connector to start this workflow.

Mocked component stories support design and focused tests. They do not establish that
actual authentication, navigation, backend calls, persistence or error handling work.
Maintain required end-to-end evidence separately. Storybook's documented visual addon uses
Chromatic [S51]; this workflow does not mandate that hosted service or another paid plan.
Local Playwright screenshot comparison is one alternative [S48].

## The compact visual/interaction contract

Add the needed fields to the existing ticket/maintained UI guide, not a new document per
screen. The lead supplies them and the worker challenges missing criteria before coding:

- Relevant design/reference or existing approved component pattern and its revision;
  intentional differences and any operator decision still required.
- Real entry route, test account/fixture, action sequence, expected visible state, and
  backend/persistence consequences. Include failure and keyboard/focus behavior where relevant.
- Required states: choose the relevant normal/loading/empty/error/success/disabled/long-content
  cases; supported browsers, viewport ranges, theme, locale and accessibility requirements.
- Visual invariants such as no clipped validation text, reachable controls, correct overlay
  order and readable content; required evidence and acceptable nondeterminism handling.
- Whether this is implementation of an approved design (`routine-integrate`) or has a named
  unresolved design hold. Missing intent is not permission to invent an accepted baseline.

Do not request approval for every existing pattern. New subjective choices are resolved
in a focused prototype checkpoint where possible. Once the intent is accepted, ordinary
conforming implementation can be independently verified, merged and closed without another
human GUI or merge ceremony. An operator may voluntarily inspect the live preview anytime.

## Required observation loop for changed user journeys

**Run -> interact -> capture -> inspect -> compare -> repair -> repeat.** Both worker and
independent reviewer must have a verified route to view actual rendered images, not merely
write a screenshot file or read a filename. If an agent/tool cannot inspect images, the
visual-review gate is incomplete: obtain a capable approved reviewer or targeted human
inspection. Never substitute a confident claim based on source, DOM text or a tool summary.

1. **Identify the running build.** Record actual candidate/tree/dirty state, dependencies,
   server instance, safe fixture, configuration, browser/version, viewport and relevant
   rendering settings. Prove the operator preview and tested application refer to that
   build; check stale processes/service workers/caches rather than assuming a restart worked.
2. **Exercise the real route.** Navigate/click/type/scroll/use keyboard through the rendered
   interface. Playwright recommends testing user-visible behavior [S46]; its normal actions
   check visibility, stability, event reception and enabled state [S47]. Do not use forced
   clicks, DOM mutations, direct handler invocation, hidden CSS removal or direct API calls
   as the sole acceptance evidence for an action users must perform through the UI. APIs
   may prepare unrelated fixtures or confirm persistence without replacing that action.
3. **Verify behavior and state.** Assert the expected messages/data transitions and relevant
   failure cases. Check persistence after reload/new session when required. A success toast
   with a failed write is not success. Check unexpected console/network failures; scope
   legitimate errors explicitly rather than swallowing them.
4. **Capture actual pixels.** Retain screenshots of the important states and a trace or
   recording for the changed acceptance journey, including successful runs. Playwright
   trace defaults may retain only retry/failure traces; configure the small acceptance set
   deliberately, not all passing tests in the entire suite [S49]. Use supported viewports
   and fixtures, not whichever viewport happened to make the layout fit.
5. **Inspect and compare.** The worker views the captures. The independent reviewer also
   opens relevant actual/expected/diff images and checks the sequence against the contract,
   including layout, clipping, overlays, focus, loading/error states and unintended visual
   changes. A DOM/accessibility snapshot is useful additional evidence, not pixel evidence.
   Reviewer output links specific observations to scenario/artifact IDs; it does not just
   repeat the worker's description. This is still fallible independent evaluation.
6. **Freeze and revalidate.** Freeze the candidate before final evidence/review. Changes
   afterward invalidate affected observations. After merge, verify the integration/build
   mapping and configured final smoke checks before ticket completion. Keep a known preview
   available under its hold policy; the operator should not need to rebuild it separately.

Capture in controlled test data with appropriate private retention. Traces/network output
can include credentials or sensitive data. Follow the access policy; do not publish raw
traces automatically. Redaction/masking must not hide the feature being evaluated. Failed
retention or unavailable evidence is a blocker, not permission to assert visual verification.

## Baselines are reviewed expectations, not disposable test output

Playwright compares actual screenshots with references and provides explicit baseline
update options. Rendering varies with OS/browser/settings/hardware, so create/compare
baselines in controlled matched environments and test other supported configurations as
specified [S48]. Stabilize fonts/data/animations and declare thresholds/masks in advance.
Do not raise tolerance, hide the changed control or regenerate all images to obtain green.

A snapshot proves similarity to a reference, not that the reference was ever correct.
Initial or deliberately changed baselines need an independent comparison to the accepted
intent. The worker may propose new baselines; the reviewer explicitly approves only those
consistent with that intent. A new subjective design choice goes to the operator. This
permits ordinary baseline maintenance without making every image change a human approval,
and prevents implementation plus regenerated expected output from self-certifying.

Regression images complement semantic assertions, real integration and accessibility
checks. They do not establish persistence, keyboard usability or an unspecified viewport.
The fact that a tool produced a PNG does not establish that either agent inspected it.

## Completion evidence and exceptions

For each changed important journey, map: requirement -> build/environment -> actual user
steps -> assertions -> images/trace -> independent observation -> result. A concise report
can link the evidence instead of embedding many screenshots. Separate visually observed,
functionally tested, automated-baseline-compared and untested claims. Do not write "looks
right" when only an API assertion or source review ran.

A routine ticket completes via [verified integration and closeout](../operator/github-single-account.md).
Human input is for unresolved intent, unapproved consequences or a real evidence gap, not
automatic approval of every merge. Production/release publication remains separate.

## Adoption rehearsals before relying on this gate

Use a harmless UI fixture with intentionally injected defects: hidden/covered or disabled
control, clipped error text, stale preview build, false success after backend failure,
missing persistence, wrong viewport, a screenshot recorded but not viewable by the reviewer,
and an unreviewed baseline update. The chosen tools/process must flag those defects before
claiming the visual gate is ready. These are required future rehearsals, not tests executed
by preparing this documentation seed. No universal guarantee of visual correctness is made.
