# Source register

Source notes carried forward from the documentation seed dated 29 September 2026.
This packaging consolidation did not re-verify external URLs or vendor capabilities.
This is a curated reference for external mechanisms that
shape the method, not a transcript or evolving sprint investigation. Project-specific
research goes into its GitHub issue; promote only lasting rationale/constraints here
or into the relevant contract. Recheck installed-version behavior during bootstrap.

All lifecycle defaults, numeric helper/refresh budgets, and repository choices in this
seed are design recommendations, not benchmarks or universal vendor guarantees.

## Native agent behavior

[S1] OpenAI, Subagents:
https://learn.chatgpt.com/docs/agent-configuration/subagents
Documents separate delegated work, summary returns, read-heavy starting use cases,
permissions, custom agents, and current concurrency configuration. Do not assume a
legacy configuration key or another provider's lifecycle rules are interchangeable.

[S2] OpenAI, Codex App Server:
https://learn.chatgpt.com/docs/app-server
Documents new/resumed/forked threads, token-usage notifications, and compaction events.
This is a possible later adapter interface, not an implemented dependency of the seed.
Do not add an app-server integration merely to obtain a refresh heuristic.

[S3] Anthropic, How Claude Code works:
https://code.claude.com/docs/en/how-claude-code-works
Describes context management and the possibility that early detailed instructions are
lost during summarization. Persistent instructions and external records remain useful.

[S4] Anthropic, Create custom subagents:
https://code.claude.com/docs/en/sub-agents
Documents provider-specific delegation, resume, context inheritance, foreground/background
behavior, and permission differences. A fork copies history; a new scoped child need not.

[S5] Anthropic, Hooks reference:
https://code.claude.com/docs/en/hooks
Documents PreCompact/PostCompact and session lifecycle hooks. They can support telemetry
or loading current state; a hook is not a guarantee of a complete farewell or crash safety.

[S6] Anthropic, Explore the context window:
https://code.claude.com/docs/en/context-window
Documents `/context`, compaction/reload behavior, and distinct persistent inputs. Context
measurements must be interpreted by their actual semantics, not cumulative usage totals.

[S7] Anthropic, How Claude remembers your project:
https://code.claude.com/docs/en/memory
Documents instructions versus auto memory and native AGENTS.md loading from v2.1.277,
including cases in which CLAUDE.md takes precedence. Verify actual loading before
removing the compatibility shim. Text instructions are not hard permission enforcement.

[S8] Anthropic, Advanced setup:
https://code.claude.com/docs/en/setup
Documents installation-method-specific updates and version controls. Native auto-updates
may take effect on a subsequent launch; a restart does not imply a fixed version.

[S9] Anthropic, Run Claude Code programmatically:
https://code.claude.com/docs/en/headless
Documents native `-p` execution and structured modes. Its `--bare` mode does not use
subscription login; do not enable that as an unreviewed startup optimization.

## Documentation and source layout

[S10] Michael Nygard, Documenting Architecture Decisions (2011):
https://cognitect.com/blog/2011/11/15/documenting-architecture-decisions
Primary rationale for small, versioned decisions, status, consequences, and supersession.

[S11] Diataxis, Start here:
https://www.diataxis.fr/start-here/
Tutorial, how-to, reference, and explanation address different documentation needs.

[S12] Diataxis as a guide to work:
https://www.diataxis.fr/how-to-use-diataxis/
Recommends incremental improvement rather than imposing empty documentation structures.

[S13] arc42 documentation:
https://docs.arc42.org/home/
Coverage guide for architecture goals, constraints, context, runtime/deployment,
quality scenarios, decisions, risks, and terminology.

[S14] C4 model:
https://c4model.com/
A notation/tool-independent set of software architecture views, useful when a diagram
makes a boundary or interaction easier to understand.

[S15] Go, Organizing a Go module:
https://go.dev/doc/modules/layout
Documents cmd/internal layout patterns and keeping implementation packages internal.

[S16] GitHub CLI, gh repo create:
https://cli.github.com/manual/gh_repo_create
Documents creating a private remote from a local repository and explicitly pushing it.

## Recovery, execution environments, and writing guidance

The following primary references were recorded during development of the seed. Recommendations about
leases, preview modes, profile admission, and adoption are design rules derived for this
workflow, not a claim that these vendors implement BAW or guarantee its behavior. Earlier
native-agent references above are carried forward; installation/version-specific behavior
still requires a fresh compatibility check on the actual host.

[S17] Git, git-status:
https://git-scm.com/docs/git-status
Distinguishes index, worktree, and untracked changes; supports stable porcelain formats
and suppressing optional index-refresh locks for observation. Inspect actual contents
where needed rather than equating a short summary with complete recovery.

[S18] Git, git-worktree:
https://git-scm.com/docs/git-worktree
Describes linked worktrees, their independent HEAD/index state, and inventory. Runtime
services, queues, credentials, and remote resources are outside worktree isolation.

[S19] Git, git-reflog:
https://git-scm.com/docs/git-reflog
Local reference movements can help investigate lost commit/branch context. Reflog is not
an arbitrary saved-file or editor-buffer backup.

[S20] Docker Compose, Specify a project name:
https://docs.docker.com/compose/how-tos/project-name/
Project names support separate branch/build environments. Audit explicitly shared or
fixed-name resources as well; a naming convention alone is not proof of isolation.

[S21] Docker Compose, Use Compose Watch:
https://docs.docker.com/compose/how-tos/file-watch/
Documents source synchronization, rebuild/restart behavior and version prerequisites.
The actual framework, background workers, dependencies and migration behavior require
project-specific validation; watch is not a universal hot-reload guarantee.

[S22] Docker Engine, Bind mounts:
https://docs.docker.com/engine/storage/bind-mounts/
Mounts bind paths on the daemon host, not an arbitrary remote client. Writable mounts
can change host files and need careful scope and permissions.

[S23] Docker Engine, Port publishing and mapping:
https://docs.docker.com/engine/network/port-publishing/
Documents exposure and localhost binding. Use an approved private/forwarded route for
operator access rather than unintentionally exposing development debug servers.

[S24] Docker Engine security:
https://docs.docker.com/engine/security/
Explains powerful daemon access and isolation limitations. A rootful socket and broad
host mounts are not a safe sandbox merely because a container is involved.

[S25] NetBox Community, Using Netbox Plugins with netbox-docker:
https://github.com/netbox-community/netbox-docker/wiki/Using-Netbox-Plugins
Documents custom images, plugin installation/enabling, and associated services. This is
evidence of a containerized plugin path, not proof of a validated developer hot-reload
profile for an unknown plugin/version.

[S26] NetBox Community, Plugin tutorial initial setup:
https://github.com/netbox-community/netbox-plugin-tutorial/blob/main/tutorial/step00-initial-setup.md
Describes a local NetBox development environment and developer settings. Read the
applicable version's instructions rather than transplanting current examples blindly.

[S27] tmux, Getting Started:
https://github.com/tmux/tmux/wiki/Getting-Started
Describes detach/reattach and protecting remote terminal programs from connection loss.
Does not establish application/host crash recovery or authority for a duplicate writer.

[S28] Diataxis, The compass:
https://www.diataxis.fr/compass/
Classifies documentation by reader need and purpose. Use with the primer [S11] and
incremental workflow [S12], not as a mandatory directory structure or approval policy.

## Secrets and single-account operation

The references below are carried forward from the source seed. Earlier references are carried
forward; real setup still requires installed-version and actual-account verification.
Binding layouts, authorization scopes and isolation recommendations are proposed BAW
policy, not guarantees of the linked products or functionality implemented by this seed.

[S29] OWASP, Secrets Management Cheat Sheet:
https://cheatsheetseries.owasp.org/cheatsheets/Secrets_Management_Cheat_Sheet.html
Least privilege, lifecycle, credential delivery, exposure, logging and revocation guidance.

[S30] Linux man-pages project, chmod(2):
https://man7.org/linux/man-pages/man2/chmod.2.html
Owner/group/other mode bits and privileged operations. Restrictive file modes do not
isolate processes operating as the same owner or replace encrypted storage.

[S31] Git, gitignore:
https://git-scm.com/docs/gitignore
Ignore rules govern untracked paths; files already tracked remain affected by Git.

[S32] Docker Compose, Use secrets:
https://docs.docker.com/compose/how-tos/use-secrets/
Per-service file delivery and local bind mounts. `_FILE` is image/application-specific.

[S33] Docker Compose, Environment interpolation and service env_file:
https://docs.docker.com/compose/how-tos/environment-variables/variable-interpolation/
https://docs.docker.com/reference/compose-file/services/#env_file
Distinguish CLI interpolation from actual service bindings and precedence. Avoid dumping
resolved secret values while inspecting configuration.

[S34] Docker Build, Build secrets:
https://docs.docker.com/build/building/secrets/
Build ARG/ENV are not appropriate storage for build secrets; temporary mounts still
require care to avoid copying credentials into outputs.

[S35] GitHub Actions, Using secrets in a workflow:
https://docs.github.com/en/actions/how-tos/write-workflows/choose-what-workflows-do/use-secrets
CI secret scopes, limitations and risks of passing values in command arguments. CI
storage does not automatically deliver secrets to local development worktrees.

[S36] GitHub, Reviewing proposed changes in a pull request:
https://docs.github.com/en/pull-requests/how-tos/review-pull-requests/reviewing-proposed-changes-in-a-pull-request
PR authors cannot approve their own pull requests. Ordinary comments can carry findings.

[S37] GitHub, Setting your username in Git:
https://docs.github.com/en/get-started/git-basics/setting-your-username-in-git
Git authorship names and GitHub account identity are different concepts.

[S38] GitHub CLI, gh auth login:
https://cli.github.com/manual/gh_auth_login
Native browser/token login and credential-store behavior, including plaintext fallback.

[S39] GitHub, Available rules for rulesets:
https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets
PR requirement can be separate from approving-review count; inspect code-owner, latest
push, status-check and other effective restrictions before setting solo defaults.

[S40] GitHub, About protected branches:
https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches/about-protected-branches
Plan-dependent availability, checks, bypass and review rules. Some skipped/neutral
statuses satisfy GitHub checks; BAW still requires evidence of mandatory test execution.

[S41] GitHub Actions, Deployments and environments:
https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments
Required reviewers and the prevent-self-review setting have plan/visibility conditions.
Do not make the sole operator ineligible without an explicitly chosen alternative.

[S42] GitHub, Managing personal access tokens:
https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens
Fine-grained tokens restrict repositories/permissions while retaining the same user
identity. This alone does not establish an arbitrary push-but-never-merge boundary.

## Complete-ticket integration and visual conformance

The following primary documentation was checked 29 September 2026. These capabilities
support the proposed method; they do not establish that BAW is implemented or validated.

[S43] GitHub, Automatically merging a pull request:
https://docs.github.com/en/pull-requests/how-tos/merge-and-close-pull-requests/automatically-merging-a-pull-request
Native auto-merge waits for configured requirements; BAW-only conditions must still be enforced.

[S44] GitHub CLI, gh pr merge:
https://cli.github.com/manual/gh_pr_merge
Expected-head matching, merge methods and auto-merge. Head matching alone does not pin base.

[S45] GitHub, Immutable releases:
https://docs.github.com/en/code-security/concepts/supply-chain-security/immutable-releases
Locks associated tag/assets; title/notes and latest designation remain editable.

[S46] Playwright, Best practices:
https://playwright.dev/docs/best-practices
Test user-visible behavior and isolated, controlled scenarios rather than internal implementation.

[S47] Playwright, Auto-waiting/actionability:
https://playwright.dev/docs/actionability
Normal actions verify visible/stable/enabled/event-receiving conditions; force bypasses checks.

[S48] Playwright, Visual comparisons:
https://playwright.dev/docs/test-snapshots
Actual-to-reference image comparison, intentional updates and rendering-environment differences.

[S49] Playwright, Trace viewer:
https://playwright.dev/docs/trace-viewer
Actions, image frames, DOM snapshots, network and console evidence; trace retention options.

[S50] Storybook, Interaction tests:
https://storybook.js.org/docs/writing-tests/interaction-testing
Controlled stories, userEvent actions, assertions and mocked dependencies; not full app proof.

[S51] Storybook, Visual tests:
https://storybook.js.org/docs/writing-tests/visual-testing
Rendered-pixel comparisons and reviewed baselines; documented addon uses hosted Chromatic.

[S52] Git, git-revert:
https://git-scm.com/docs/git-revert
Creates new source-history commits reversing specified changes, not external-state recovery.

## Validation boundary

This seed contains no executable controller and has not been installed in the operator's
host or GitHub account. Local document/link/YAML/package checks, when reported alongside
the archive, do not establish live agent compatibility, semantic correctness, security,
or improved development throughput. Those remain explicit bootstrap/pilot work.
