# Bounded Agent Workflow

**Repository name: `bounded-agent-workflow`**  
**Project abbreviation: BAW (Bounded Agent Workflow)**  
**Planned executable name: `baw`**

**Package:** `bounded-agent-workflow.zip` — consolidated documentation seed, packaged
30 September 2026. Extracting it produces one `bounded-agent-workflow/` directory.
This archive is self-contained and replaces all previously supplied seed downloads.
You do not need an earlier ZIP, a separate Markdown attachment, or the old Python kit.

**Status: early implementation.** The implemented commands, built from source with Go,
are the read-only [`baw inspect`](docs/operator/inspect.md) and
[`baw run create` / `baw status` / `baw run diagnose`](docs/operator/run-records.md), which save,
display and read-only diagnose a local record of supplied references (not an approval or live state), and the
read-only [`baw context`](docs/operator/context.md), which lists a lead, worker or reviewer's
onboarding document references (not an assignment or authority), and
[`baw run execute`](docs/operator/execution.md), which runs one trusted local worker and one
verification program in a recorded, never-repeated attempt (no native agent adapter, approval
evaluation or recovery). There is no
controller, release, installed plugin or configured application. Other `baw` command
examples remain proposed interfaces, not available commands.

The intended product combines a lightweight, human-gated development method with one
standalone Go executable. It coordinates subscription-authenticated native Codex and
Claude Code tools without creating another project-management system.

## Start here

Open [START-HERE.md](START-HERE.md), then follow
[the manual operating procedure](docs/operator/manual-workflow.md), beginning at section 1.
That procedure includes the repository name, setup commands, first lead prompt, bounded
bootstrap assignment, normal worker/reviewer handoffs, and replacement prompts. Other
files are references for agents and reviewers, not extra operator setup chores.

Create the tooling repository as **`bounded-agent-workflow`** under your chosen GitHub
account or organization. The extracted folder is already named correctly; do not create
another nested folder with the same name. Start in a new directory, not by overlaying an
existing application. If you already initialized the tooling repository, preserve it and
reconcile the contents rather than reinitializing or overwriting its work.

ADRs 0001–0003 are **Accepted** for this repository under the operator's delegated
[bootstrap decision](https://github.com/afewell-hh/bounded-agent-workflow/issues/2#issuecomment-5907403557).
Their substantive decisions were adopted as written in seed revision `10425c4`.
Adoption does not implement the controller or authorize the next implementation ticket.

## Current workflow: complete the ticket, then (by default) ask what is next

Once adopted, routine scope approval includes in-scope commit/push/PR, verified merge to
the named non-production integration branch, record updates, and ticket closure. By
default, the operator's checkpoint is **after a completed ticket, before another starts**,
not another code-review or merge-button task. Only an explicit, recorded
[standing program delegation](docs/operator/github-single-account.md#standing-program-delegation-opt-in)
lets the lead select the next ready in-scope ticket and record finite routine repair
extensions within its stated bounds. See
[single-account closeout](docs/operator/github-single-account.md). Release/deployment,
production effects, governing policy, and unresolved design decisions always retain specific gates.

For GUI work, [rendered-interface verification](docs/developer/gui-verification.md) requires
real interactions plus actual image inspection and independent evidence. Implementing an
approved design can close autonomously; human attention goes to intent and visual choices,
not checking every commit. No browser tools, merge automation, or controller are installed.

The package includes recovery from recent commits and dirty work, non-destructive project
adoption, project-specific runnable environments, held previews, shared-lab coordination,
secret bindings, Diataxis documentation, and one-account GitHub operation. No prior
package needs to be copied into this one to supply these requirements.

For existing or ordinary new applications, use
[project adoption](docs/operator/project-adoption.md) to merge selected operating rules.
Do not overlay this tooling seed or silently replace a project's governing policy.

## Navigation and ownership

| Need | Canonical location |
|---|---|
| Operate manually, bootstrap, or recover | [Manual workflow](docs/operator/manual-workflow.md) |
| Adopt an existing/new application | [Project adoption](docs/operator/project-adoption.md) |
| Configure development, previews, and shared labs | [Execution environments](docs/developer/execution-environments.md) |
| Inspect a repository's state read-only | [`baw inspect`](docs/operator/inspect.md) |
| Save, read and diagnose a local run record | [Run records](docs/operator/run-records.md) |
| List a role's onboarding document references | [`baw context`](docs/operator/context.md) |
| Run one trusted local worker and verifier attempt | [`baw run execute`](docs/operator/execution.md) |
| Prepare BAW's native Go development environment | [Developer setup](docs/developer/environment.md) — toolchain, build and test route |
| Supply app/API secrets and control external writes | [Secrets and access](docs/developer/secrets-and-access.md) |
| Complete routine tickets without per-merge human approvals | [GitHub single-account procedure](docs/operator/github-single-account.md) |
| Verify actual GUI behavior and appearance | [GUI verification](docs/developer/gui-verification.md) |
| Replace agents and govern subagents | [Agent lifecycle](docs/operator/agent-lifecycle.md) |
| Shared agent rules | [Protocol](workflow/protocol.md), entered through [AGENTS.md](AGENTS.md) |
| Role-specific onboarding | [Lead](workflow/roles/lead.md), [worker](workflow/roles/worker.md), [reviewer](workflow/roles/reviewer.md), [helper](workflow/roles/helper.md) |
| Intended product behavior | [Product contract](docs/design/product-contract.md) |
| Structure and design rationale | [Architecture](docs/architecture/overview.md) and its linked ADRs |
| Documentation placement and acceptance | [Documentation policy](docs/developer/documentation-policy.md) |
| Quality requirements and validation gaps | [Verification](docs/developer/verification.md) |
| Current plan, active lead, tickets, blockers | [Project coordination #1](https://github.com/afewell-hh/bounded-agent-workflow/issues/1) |
| External capability evidence | [Sources](docs/research/sources.md) |

There is intentionally no `CURRENT_STATE.md`, sprint directory, agent transcript store,
or local backlog in the repository. The coordination issue is a bounded index, not a
second specification or an approval source. Per-run evidence belongs outside the source
checkout and must not be presented as remotely accessible until actually published.

## Developing versus using the product

Developing the controller will require Go in **this tooling repository**. Using a
released controller in other applications should not require Go or Python. The planned
CLI front end and controller engine are packages in one program, not separate services.

This seed does not select a license, create a remote, change credentials, install a
runtime, or modify CLI settings. Those are explicit operator decisions.
