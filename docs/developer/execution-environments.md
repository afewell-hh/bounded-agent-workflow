# Execution environments: project-specific, reproducible, and inspectable

**Status:** proposed requirements for manual adoption and later controller support.
No environment provisioner, lease service, Compose configuration, or preview command
is implemented by this document. The common procedure is portable; actual commands,
permissions, hosts, and fidelity requirements must be discovered in each repository.

## The prerequisite

Every application project needs a validated route to execute the relevant application
and its required tests in a representative environment. A worker cannot report an
integrated feature complete using only inferred behavior or mocked success. For a CLI
or library, a real binary/example/integration harness can be the running application;
there need not be a permanent web server.

Environment-enablement and initial scaffolding are explicit supervised work items when
nothing runs yet. They may create the first runnable slice; they are not permission to
claim an unexercised product feature complete. Read-only research and planning can
continue while an environment is unavailable. Scarce lab tests can be deferred until
their scheduled verification stage, but remain mandatory before acceptance.

A running process or an HTTP 200 is not sufficient: demonstrate the ticket-relevant
interaction through the real services, supported versions, configuration, and state.
Record differences from the target deployment and map each important difference to a
required test/profile. No demand for an unaffordable full production replica is implied.

## Three separate lifetimes

Keep **agent sessions**, **source workspaces**, and **runtime environments** distinct.
A new worker can adopt an existing worktree/environment after ownership checks. A lead
refresh need not stop a useful preview. A test environment is owned by the approved
run/operator and a known supervisor, not merely by a worker's terminal.

Use existing project tools: native processes, a virtual environment, Make targets,
package scripts, Docker Compose, development containers, or approved remote-lab scripts.
The controller should call inspected/versioned operations and track their results. It
should not invent arbitrary infrastructure or introduce a universal deployment language.
A development container for the toolchain and a running application stack are different
things; either or both can be useful.

## The project environment contract

During adoption, adapt [this template](../../workflow/templates/environment-contract.md)
into the existing developer setup guide, or add one maintained `docs/developer/environment.md`
when there is no suitable home. Paths are examples, not a required reorganization.
Link it from the project entry point. Keep normal human and agent instructions on the
same source of truth; tickets select the relevant profile and revision rather than
copying the entire guide. Live endpoints, ownership, job handles, and reservations go
in private run records and the coordination index, not in the canonical template.

The contract identifies supported platform/dependency versions; source-to-runtime
mapping; services and fixtures; normal start/status/logs/test operations; how changes
reach every relevant process; preview access; mandatory fidelity profiles; resource
limits and ownership; allowed destructive operations; and loss/recovery behavior.
Record the actual command, working directory, permitted parameters, side effects,
timeout, expected evidence, and cleanup scope. Unknown required fields are blockers.
A startup recipe is not validated until an independent fresh session exercises it.

A common profile vocabulary can help, without mandating three environments:

| Profile purpose | Typical implementation | What it establishes |
|---|---|---|
| Fast development | Existing native setup or isolated container stack | Editable running app, focused tests, immediate feedback |
| Integrated verification | Real dependencies in a disposable fixture | Cross-service behavior, persistence, authorization, migrations |
| Live-lab acceptance | Reserved remote lab or specialized hardware | Behaviors that lower-fidelity environments cannot establish |

One environment can serve multiple purposes at different times. Each required gate
names its profile; an unavailable mandatory profile means waiting/blocked, never passed.
A simulator can support development but cannot stand in for required real-lab evidence.

## Secret and remote-action bindings

Apply [secrets and access](secrets-and-access.md) using the existing environment guide.
Record aliases, required names, operator-populated source references, consumer delivery,
allowed target/actions, per-instance overrides, rotation and safe cleanup. Repeated setup
binds that approved profile without asking for values again. Existing ignored `.env` files
can be retained during adoption; parallel instances must not share mutable combined files.
Do not include values or raw secret-derived fingerprints in source/runtime identity.

The source belongs to the operator; copied instance material belongs to the named run.
Agent exit does not revoke a still-needed preview credential or delete the source. A
credential/configuration change must be recorded and the affected behavior reverified.
A second host needs explicitly provisioned access, not copied native model token stores.
Shared CMS/API write targets follow the same reservation/recovery discipline as labs;
separate local stacks or tokens alone do not prevent conflicting external changes.

## Container-friendly projects and limited parallelism

Prefer a proof of usefulness over a mandatory migration. Keep a working native setup
until an approved container profile demonstrates equal required fidelity and a better
operator experience. Pin supported versions rather than blindly selecting `latest`.

For parallel stacks, use a unique environment identity and source checkout per approved
workstream. Audit names, published ports, writable volumes/bind paths, database/schema,
queues, caches, uploads, background workers, fixtures, and external side effects. A
unique Compose project name helps separate default resources [S20]; hard-coded names,
external volumes, shared bind paths, fixed ports, or shared downstream services can
still collide. Different Git worktrees alone do not isolate runtime state [S18].

Use one writer per workspace and one mutating owner per shared resource. The initial
pilot remains one approved implementation ticket at a time per project. Separately
approved parallel tickets/workstreams require proven isolation and explicit capacity;
this requirement does not grant automatic next-ticket or merge authority, which follows
[the closeout policy](../operator/github-single-account.md) (per-ticket by default, or an
explicit [standing delegation](../operator/github-single-account.md#standing-program-delegation-opt-in)
that still selects one ticket at a time). Multiple
projects must share the same resource-ownership mechanism for any common lab.

For remote container engines, record where the source really lives. Bind mounts refer
to the daemon host, not an arbitrary path on the client [S22]. A simple default is to
run the agents and worktrees on that development VM and forward the preview to the
operator. Other synchronization routes require explicit configuration and validation.

Set resource ceilings and bounded retention. Do not give an agent unrestricted Docker
socket, host mounts, privileged mode, or production credentials and call it isolated;
Docker daemon control can grant powerful host access [S24]. Existing access and approved
commands are not permission to change host networking or install arbitrary tooling.

### NetBox plugin profile: a candidate to validate, not a ready-made recipe

The NetBox Community Docker project documents custom images and enabling plugins [S25].
Its plugin-development tutorial also describes a local NetBox development installation
[S26]. A non-container setup is therefore not by itself evidence of a bad setup.

For an actual plugin, inspect its supported NetBox/Python/database/cache versions and
current installation first. A candidate container profile should load the checked-out
plugin, run its dependencies, and update every relevant web/background process. Prove
Python/template changes, static assets, jobs, configuration, dependency changes, and
migrations take effect through the declared reload/restart/rebuild operations. Test
plugin installation, required journeys, and independent data/ports before parallel use.
The Docker plugin-install guide alone does not prove a hot-reloading development setup.
Do not transplant old version-specific virtualenv paths or automatically run migrations
on a shared database as a convenience.

## Scarce remote or nested-virtualization labs

Treat a mutable lab as a reserved test instrument. Parallel approved work can use
isolated source and inexpensive fixtures; live integration is serialized unless actual
lab tenancy/isolation is proven. Do not clone/rebuild an expensive lab per ticket by
default. A remote lab profile records topology/image versions, host capabilities,
resource budget, baseline/snapshot references, access, and reset/health procedures.

There must be **one authoritative reservation point per physical/logical shared resource**,
visible to all projects, hosts, workers, and human users that may mutate it. In the
manual pilot this is an operator-owned single-writer schedule and resource record.
That is coordination, not technical fencing. Do not enable unattended concurrent access
until a validated host-side broker/lock or equivalent access control covers all writers.
A local lock in each checkout or an editable GitHub comment is not a distributed mutex.

An exclusive reservation covers baseline check, deployment, verification, evidence,
and required cleanup, not just the command that begins a remote job. Persist resource,
owner/run/generation, job handles, candidate, fixture/topology state, permitted actions,
health, and release conditions outside the agent conversation. Read-only observation
may be shared only when proven noninterfering; probes can have effects too.

An SSH disconnect or expired heartbeat does not establish that a remote writer stopped.
Reassignment requires checking the actual host/jobs and, where possible, fencing the
old owner (making its stale operations unable to write). Without fencing, block and
have the operator reconcile; do not steal the lab on a timer. Cleanup after a crash
must not destroy another user's work or evidence. An uncertain/failed reset quarantines
the resource until its known baseline is restored and verified.

Waiting for capacity consumes elapsed time but does not silently spend or replenish a
repair allowance. A lost job with uncertain side effects is recovery, not a blind retry.
Holding a lab for human inspection occupies its slot; queue other users or explicitly
release/recreate the candidate later. Record that tradeoff instead of hiding it.

## Fast, persistent human previews

For UI work, establish a reachable preview before judging UI progress. The operator
should not rebuild or launch a second instance after each edit. Configure the actual
framework's reload path, source mount/sync, or incremental rebuild/restart automation;
Docker Compose Watch offers sync/rebuild/restart mechanisms, but application behavior
and supported tool versions still need validation [S21]. Database migrations, dependencies,
and backend workers are not assumed to hot-reload merely because the browser does.

Keep one stable address per active preview where practical. Use a loopback-bound port
and authenticated port forwarding or an approved private route; do not expose a debug
server publicly [S23]. Record both the runtime endpoint and how the operator reaches it.
A broken tunnel may require reconnecting without rebuilding the application.

**Live development mode:** current editable source, clearly labelled unverified. Show
workspace, source revision/dirty fingerprint, relevant dependency/configuration identity,
fixture state, and last successful update/health. The operator can inspect frequently.
Avoid conflicting destructive tests, resets, or other writers during that inspection.

**Frozen acceptance mode:** freeze source/build/configuration for the reviewed candidate,
run required verification, prepare the documented inspection fixture, and stop automatic
code updates until required review/verification and any named human design hold resolve.
Routine candidate closeout does not wait for optional operator inspection. Preserve evidence
and make the displayed build
identity trustworthy. A commit hash alone cannot identify a dirty bind-mounted preview.
An operator action can intentionally change fixture data; record what was exercised.
A new code/config/build change invalidates the affected review and evidence.

These are two modes, not a requirement for two simultaneously funded instances. Reuse
the same instance with an explicit freeze or deploy the frozen artifact automatically.
Declare when packaging/production-mode differences require an additional check. Never
make the operator infer which branch or build is behind the URL.

Give previews an operator hold and documented retention policy. Finishing a worker or
refreshing a lead must not implicitly remove its preview or lab reservation. When cost
or capacity requires release, ask/notify according to the approved retention rule and
preserve an exact, reproducible restart path; do not promise an indefinitely free host.

## Admission test before normal feature execution

An independently started agent must follow the maintained setup, execute the real app,
run the required baseline checks, and explain its actual source/environment identity.
The operator must reach and exercise the same UI/CLI journey. Where parallel use is
proposed, prove a second workspace/stack cannot overwrite the first. Rehearse session
loss, preserved preview, lab contention and loss of ownership on disposable resources.

Record environment readiness with evidence in the adoption issue. Separate pre-existing
failures from new ones, but do not relabel failing required tests as passed or loosen
requirements to qualify. Missing infrastructure becomes a scoped enablement/remediation
item. Local success without required lab evidence is incomplete, not an acceptance.

## Visual evidence and integration effects

For GUI profiles follow [GUI verification](gui-verification.md): validate actual screenshot
inspection by the assigned agents, real rendered-interface actions, controlled browser/state
coverage and evidence access. Story/component fixtures do not replace required integrated
backend/persistence tests. Keep image/trace capture private and secret-safe.

The [closeout policy](../operator/github-single-account.md) requires auditing the actual
integration branch's downstream effects. Non-production merge is not a release or live CMS
mutation. Test/preview workflows must not secretly change shared production assets. Record
resulting merge/build identity and configured post-merge health; a changed integration
result needs applicable verification before closing the ticket. A retained preview may
remain after routine closure for optional operator inspection under its hold policy.
