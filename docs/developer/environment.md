# BAW developer setup

Use a native Go toolchain and a local terminal for BAW's first executable slice.
This guide adapts [execution environments](execution-environments.md) for contributors
to this tooling repository. The module `github.com/afewell-hh/bounded-agent-workflow`
(minimum Go 1.27.0, standard library only) provides `cmd/baw` with the read-only
[`baw inspect`](../operator/inspect.md) command and its tests. See the last section for
which checks have actually run and what remains unvalidated.

## Selected toolchain

The `native-cli` profile uses an isolated official Go 1.27.1 darwin/arm64 toolchain,
installed after checksum verification into a private run directory and registered as a
persistent project asset (`~/.local/state/baw/toolchain-assets/go1.27.1-darwin-arm64.json`).
Homebrew Go and global Go settings are unchanged. Invoke the toolchain by explicit path or a
per-command `PATH` prefix. The toolchain subtree has **no run-expiry cleanup**: it remains
until the operator explicitly retires or replaces the asset and dependent runs are
reconciled. Run-root cleanup must exclude `toolchain/`. Binary, fixture and evidence
retention is a separate rule (at least 30 days after ticket closure and any hold).

## Inspected facts

Read-only inspection on 30 September 2026 established these development-host facts.
They are observations, not a supported-platform matrix or minimum-version policy.

| Item | Observed result |
|---|---|
| Host | macOS 26.5, build 25F71, arm64 |
| Go executable | `/opt/homebrew/bin/go` |
| Installed Go | `go1.24.1 darwin/arm64`, also confirmed with `GOTOOLCHAIN=local` |
| Git | 2.39.5, Apple Git-154 |
| Source (at inspection time) | Documentation seed; no `go.mod` or Go application source |

Native agent/GitHub CLI versions and exact inspection evidence belong in the relevant
ticket's run evidence, indexed by [project coordination](https://github.com/afewell-hh/bounded-agent-workflow/issues/1),
not a permanent promise about supported adapter versions.

## Proposed native CLI profile

The first implementation ticket should select this guide's revision and a `native-cli`
profile: one isolated source worktree, one writer, local Go compilation, deterministic
tests using disposable Git repositories and fake GitHub responses, and a terminal
demonstration. No application secrets, server, database, ports, containers, NetBox or
nested lab are needed for that local slice. Native agent login and GitHub credentials
remain in their existing tools; offline fixtures do not need access to them.

Before implementation, the ticket must select the Go version supported by its module,
confirm compatibility with the available toolchain, and define exact CLI arguments and
expected results. As of 30 September 2026, installed Go 1.24.1 is outside
[Go's supported releases](https://go.dev/doc/devel/release#policy); the first code ticket
will likely need explicit operator approval to install a supported newer toolchain.
Installing or changing a toolchain requires specific approval.
Keep toolchain selection local to each command; do not change global Go settings.
`GOTOOLCHAIN=local` prevents automatic toolchain switching/downloads and fails if the
module requires a newer version. See [Go toolchain selection](https://go.dev/doc/toolchain).

For this first local profile, propose standard-library Go dependencies with Git as the
fixture tool. A need for additional dependencies, CGO or live GitHub access must be
resolved in that ticket's environment requirements. Offline success does not establish
authenticated native integration or live GitHub behavior.

## Build and verification commands

Run from the assigned worktree root with the selected toolchain first on `PATH`. Also
run `gofmt -l .` (expect no output). First record the
candidate commit and any dirty-file identities. Allocate a new owner-only artifact
directory outside the checkout under the ticket's approved evidence location; set
`baw_artifact_dir` to that absolute path. Never reuse a held binary's output path.

The command sequence:

```sh
(
  set -e
  : "${baw_artifact_dir:?Set the new run-owned artifact directory first}"
  export GOTOOLCHAIN=local GOWORK=off GOPROXY=off CGO_ENABLED=0
  go test -count=1 -timeout=2m ./...
  go vet ./...
  go build -trimpath -o "$baw_artifact_dir/baw" ./cmd/baw
  go version -m "$baw_artifact_dir/baw"
  shasum -a 256 "$baw_artifact_dir/baw"
)
```

These settings apply only inside the subshell. `GOWORK=off` avoids an unrelated Go
workspace; `GOPROXY=off` disables module downloads. They do not prevent arbitrary test
code from making network calls: the approved offline tests must use dummy/local inputs.
The commands write Go cache entries, temporary test fixtures and the named binary;
they do not install BAW globally. See the [Go command reference](https://pkg.go.dev/cmd/go).

Propose a five-minute wall-clock limit per test, vet or build command, supervised by
the invoking agent/operator. The test flag additionally limits each package's test
binary to two minutes. Retain exit codes and sanitized output. On timeout or failure,
preserve evidence, reconcile any child processes and stop before the next gate; do not
automatically reinstall tools, loosen checks or reset the attempt budget.

## First runnable acceptance

Once the first executable slice exists, the worker runs the implemented tests and
builds the candidate, then exercises the **built binary** against the ticket's accepted
terminal scenarios. The ticket must supply exact invocations, dummy fixture paths,
expected output and exit codes, including failure cases; this guide does not invent
currently unavailable `baw` subcommands.

An independent fresh session follows this guide and verifies the source, toolchain,
fixtures and evidence. The operator then exercises the same binary by its absolute
path, using the documented scenario. Record its SHA-256 before and after both agent
and operator runs, the source identity, build metadata, commands, outputs and observed
results. Package tests alone do not prove the terminal journey. A separately rebuilt
binary is a new artifact requiring its own verification.

The run owns its artifact and fixtures independently of any agent session. Retain them
through review and the operator demonstration under the ticket's retention rule. A
replacement inspects existing artifacts and process ownership before continuing;
rebuilding, deleting fixtures or terminating an unknown process is not implicit recovery.
Only the designated owner cleans up that run's paths after any inspection hold ends.

Tests create disposable Git repositories with an isolated `HOME` and run the real
`git`; GitHub behavior uses a fake `gh` on `PATH`. They make no network calls and need no
credentials. To exercise a built binary against dummy fixtures in a new directory:

```sh
go test ./cmd/baw -run TestBinaryJourneys -count=1 -args \
  -baw-binary="$baw_artifact_dir/baw" -journey-dir=NEW_EVIDENCE_DIR/journeys
```

It writes an operator fixture (staged=1 unstaged=1 untracked=1) and `summary.txt` with
exit codes and outputs of terminal, JSON, invalid-repository, malformed-snapshot, usage
and help runs.

## Checks actually executed and remaining validation

The implementation worker for the first `baw inspect` slice ran gofmt, the test suite,
vet, the build above, `go version -m`, SHA-256 and the binary journeys with the selected
Go 1.27.1 toolchain on the host described above (Git 2.39.5, which also exercised
SHA-256 object-format repositories). Exact results belong in that ticket's run evidence.
**Independent fresh-session reproduction, the live read-only GitHub smoke and the
operator same-binary exercise have not run as part of that work.** Until they do, treat
this procedure as worker-verified only. Other platforms have not been exercised.

This CLI profile applies to BAW development. Future application adopters validate their
own environment using [project adoption](../operator/project-adoption.md), including
a persistent GUI preview or scarce-lab verification when their work requires it.
