# Contributing

Start with [AGENTS.md](AGENTS.md) and the [manual workflow](docs/operator/manual-workflow.md).
The source contains documentation, templates, proposed contracts and the implemented
commands: the read-only [`baw inspect`](docs/operator/inspect.md) and
[`baw run create` / `baw status` / `baw run diagnose`](docs/operator/run-records.md) for a local saved run record,
the read-only [`baw context`](docs/operator/context.md) role reference list,
[`baw run execute`](docs/operator/execution.md) for one trusted local worker and verifier attempt, and
[`baw run review`](docs/operator/review.md) for one recorded reviewer-program verdict on a committed candidate; other controller surfaces do not exist yet. Build and test with [developer setup](docs/developer/environment.md), and
report only checks actually run on the identified candidate.

Use a reviewed work item with a bounded outcome, base commit, relevant context,
acceptance cases, documentation impact, and explicit stop conditions. Use an isolated
branch/worktree for changes. Keep the main checkout available for planning and inspection.
Only one authorized implementation writer may own a candidate workspace.

Research and architectural proposals are reviewed separately when necessary. Testing
and documentation requirements are described in [verification](docs/developer/verification.md)
and [documentation policy](docs/developer/documentation-policy.md). A pull request must
identify its actual evidence, limitations, and documentation changes. The operator approves
scope and completion policy; routine scope approval includes verified merge and closeout.
Follow [the closeout policy](docs/operator/github-single-account.md), then by default stop
before the next task unless a recorded
[standing delegation](docs/operator/github-single-account.md#standing-program-delegation-opt-in)
applies. Consequential exceptions and unresolved design decisions retain specific gates.

The [developer setup guide](docs/developer/environment.md) records inspected host/tool
facts, the native Go build/test/terminal route and the checks actually executed for the
first executable slice on one host; no supported-version matrix is established yet. Prefer a single Go module and internal
packages, with tests next to the implementation and deterministic fixtures. Validate
native CLI adapters against documented versions; do not scrape a terminal UI when a
supported structured interface serves the requirement.

Do not install a locally built candidate over the trusted controller used to govern
its development. Test candidates against disposable repositories and preserve the
previous accepted executable and policy. See [the product contract](docs/design/product-contract.md).

Adopt runtime operations through [execution environments](docs/developer/execution-environments.md).
For this tooling project, configure a real Go build/test/terminal-demonstration route as
code is introduced; no NetBox stack or virtualization lab is required merely to bootstrap
BAW. Environment tests initially use disposable fixtures and fake providers. The
application [adoption guide](docs/operator/project-adoption.md) is a separate reuse path.

For access-dependent work, follow [secrets and access](docs/developer/secrets-and-access.md)
and [single-account GitHub](docs/operator/github-single-account.md). Use dummy fixtures for
tooling tests; never package local `.env`, credential stores, or live customer data. Native
subscription authentication stays outside application configuration. Do not install a
required second-account review workflow as a contributor convenience.
