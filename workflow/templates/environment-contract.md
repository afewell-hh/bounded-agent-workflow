# Project environment contract — adaptation template

This is a reusable template, not configured infrastructure. Adapt the useful fields into
one maintained developer environment guide. Delete truly irrelevant sections with a
reason; do not invent commands to fill blanks. Temporary instance state stays in the
run record. The governing method is [execution environments](../../docs/developer/execution-environments.md).

## 1. Supported execution and fidelity

Project/application versions and supported platforms:
Profiles used (one or more; native/container/remote/hybrid):
What each profile reproduces; important differences from target deployment:
Which required gates require each profile, including any mandatory real-lab checks:
Maintainer/authority and contract revision:

## 2. Source, dependencies, and state

Actual execution host and source/worktree mapping:
Tool/runtime/image/dependency versions and lockfiles:
Services, databases, queues, caches, background processes, uploads:
Fixture/seed data, schema/migration state, topology or image baseline:
Secret binding/profile reference and permitted consumers (never secret values):
Existing script/configuration paths that implement these operations:

### Secret bindings and GitHub access (within this same maintained guide)

Use [the secret policy](../../docs/developer/secrets-and-access.md). Record only bindings
actually needed; no separate secret inventory database is required.

| Alias / required key names | Source reference, not value | Consumer / delivery | Target / allowed actions | Sharing / expiry / owner |
|---|---|---|---|---|
| Not configured | Operator-provided location | Not configured | Not configured | Not configured |

Owner-only source/instance-file permissions and approved provisioning operation:
Per-instance nonsecret settings and no-overwrite rule for existing `.env` files:
Missing/expired/wrong-target behavior; sanitized health/evidence and secret version reference:
Rotation, preview-retention and run-owned-copy cleanup; explicit second-host provisioning:
CMS/remote writes: allowed resource IDs, before/after state, jobs/idempotency, recovery authority:
GitHub account/auth reference and effective rule audit (not token contents); apply
[single-account GitHub](../../docs/operator/github-single-account.md). Record conflicts or
approved changes without requiring another account or silently changing existing rules.

## 3. Inspected operations

For each used operation, record an exact existing command/entry point, working directory,
accepted parameters, required permissions, side effects, timeout, outputs and evidence.
Use the project's current Make/package/Compose/scripts instead of duplicating recipes.

| Operation | Actual command/reference | Side effects and authority | Success/evidence |
|---|---|---|---|
| Inspect/health | Not configured | Not configured | Not configured |
| Prepare/create or attach | Not configured | Not configured | Not configured |
| Start/update development app | Not configured | Not configured | Not configured |
| Run required tests | Not configured | Not configured | Not configured |
| Freeze candidate for acceptance | Not configured | Not configured | Not configured |
| Logs/status/reattach | Not configured | Not configured | Not configured |
| Release/stop/cleanup | Not configured | Not configured | Not configured |
| Recover/reconcile | Not configured | Not configured | Not configured |

Commands that create hosts, reset databases, delete volumes, change networks, or run
migrations require explicit recorded authority. A copied template is not that authority.

## 4. Preview and change propagation

Application URL or CLI invocation and source/build identity display:
How the operator reaches it (including remote forwarding), login/fixture reference:
Source/config/template/static asset/dependency/migration reload or rebuild behavior:
How all relevant web/worker processes are updated; checks for stale builds:
Supervisor/owner independent of agent session; SSH-loss and reconnect procedure:
Live-development versus frozen-acceptance transition and expected downtime:
Human inspection hold, idle/retention rule, and safe teardown permission:

## 5. Isolation, capacity, and shared resources

Per-instance names/ports/paths/volumes/databases/queues and network access:
CPU/RAM/disk/GPU/VM limits and permitted parallelism:
Shared resource IDs and authoritative reservation/access-control location:
Who may acquire/mutate/read/release each resource; manual versus enforced controls:
Owner/run/generation and job lookup; stale-owner fencing or manual blocked recovery:
Baseline verification, reset, quarantine, and cross-project contention procedure:

## 6. Evidence and adoption acceptance

Actual baseline/negative-case commands and required report/trace locations:
Current versus frozen candidate identity and environment/config/fixture provenance:
Private retention/redaction rules; distinction between local and uploaded evidence:
Fresh-agent reproduction, operator preview, disconnect, and isolation rehearsal results:
Known gaps and blocking remediation issue links (not invented passing results):

A ticket references the contract/profile revision and actual instance/run. It adds only
its specific fixture, gate, reservation, or preview requirements, not another full copy.

## Integration effects and GUI evidence (when relevant)

Named non-production integration branch and audited downstream actions/consumer behavior:
Release/artifact identity policy; separate production/publication authorities:
Required pre-merge integration and post-merge checks; safe source-only recovery if adopted:
GUI browser/viewport/state coverage; reference design/pattern and baseline review method:
Actual image-viewing route verified for worker/reviewer; user journey and persistence tests:
Controlled capture settings, trace/image retention/privacy, stale-build checks:
Routine closeout versus named design hold; optional operator preview retention after closure:
Use [closeout](../../docs/operator/github-single-account.md) and
[GUI verification](../../docs/developer/gui-verification.md); this template supplies no runtime.
