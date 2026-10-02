# Start here — Bounded Agent Workflow

**Name the GitHub repository `bounded-agent-workflow`.** Use the same name for the local
folder. BAW means **Bounded Agent Workflow**; the future executable will be called `baw`.

This is the complete, consolidated package. No previous ZIPs or separately downloaded
Markdown files are needed. All links below point to files inside this extracted folder.

## Your starting path: develop BAW itself

1. Extract **`bounded-agent-workflow.zip`** on the host where you will develop. The archive
   already contains a top-level **`bounded-agent-workflow/`** directory. Do not unpack it
   over an existing project or create a second nested directory of the same name.
2. Open **[docs/operator/manual-workflow.md](docs/operator/manual-workflow.md)** and start
   at **section 1, "Establish the repository."** It contains the exact local repository
   and GitHub setup instructions. The remote belongs to your chosen account/organization;
   this package has not created it for you.
3. Continue to **section 2** of that same document. Start a fresh Codex session inside
   the repository and paste the provided bootstrap-lead prompt. Its first pass is
   read-only planning, not permission to implement the whole controller. That prompt is
   a preserved historical bootstrap step; this repository has since been established.

If you already created a repository or edited earlier files, preserve that work. Do not
rerun initialization or overwrite files blindly; have the lead inspect and reconcile the
existing repository before continuing.

## What is present and what is not

The package includes the current protocol, role instructions, operator runbook,
architecture proposals, issue/PR templates, and documentation/environment/access policies.
The detailed map is in [README.md](README.md).

**The only implemented `baw` command is the read-only [`baw inspect`](docs/operator/inspect.md);**
it grants no authority, and other controller surfaces do not exist yet. You use your existing
Git/GitHub and native Codex/Claude tools to develop BAW, coordinating messages manually as
the runbook describes. You do not need a Python controller or a Go installation merely to
read the documents; building and testing follow [developer setup](docs/developer/environment.md).

For routine approved tickets, the adopted workflow includes verified non-production merge
and ticket closure without a second human merge approval, then by default stops before
another ticket unless a recorded
[standing delegation](docs/operator/github-single-account.md#standing-program-delegation-opt-in)
applies. Releases, production effects, and unresolved design/policy decisions retain their own gates.

## Reuse in an application later

To adopt the method in an existing or ordinary new application, follow
[project adoption](docs/operator/project-adoption.md) instead. Do not copy this entire
tooling repository over that application.

Packaged 30 September 2026. This is a consolidated documentation snapshot, not a tested
controller release. See [verification](docs/developer/verification.md) for implementation
and live-integration requirements.
