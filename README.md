# agent-pawl

`pawl`: a state-machine workflow engine for Claude Code. One YAML file per workflow, pure data,
engine-owned attempt and visit counters, fix-forward semantics, crash-safe resume, and a validator
that refuses dangling edges, undeclared state keys and uncapped cycles before anything runs.
Sequencing lives only in `next:` / `outcomes:` / `catch:`; every step that does something declares
a postcondition the engine — never the agent that did the work — evaluates.

## Status

**It executes.** `pawl run` / `pawl submit` / `pawl status` / `pawl abandon` / `pawl validate` / `pawl list` /
`pawl version` / `pawl update` are real, working code with unit and end-to-end test coverage (`go test ./...`), and
`docs/examples/green-tests` runs against `testdata/fixture` — see [docs/dogfood.md](docs/dogfood.md)
for a real, captured transcript of that run.

This is a milestone-1 build, and it is deliberately narrower than the design documents below
describe:

- All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, and `parallel`
  (single-group, all-or-nothing `branches:` join — [design/format-spec.md](design/format-spec.md)
  §B.15). `foreach:` fan-out with a *partial*-success join remains a later milestone.
- Top-level `guards:` is now parsed and validated (id required+unique, `match:` required, must
  compile as a Go RE2 regexp, and must not be able to match zero characters; `only_in:` required —
  rule 15 checks every entry names a declared step; `only_in: []` denies everywhere — see
  [internal/guard](internal/guard)), and is now enforced by the `PreToolUse` hook, advisory and
  pattern-matched: `pawl run`'s banner prints `guards: N advisory (pattern-matched)` when the hooks
  are on, or `guards: N declared, NOT enforced (enforcement off)` when enforcement is off, and
  `pawl validate` (which has no run) prints `guards: N declared (enforced only when a run starts
  with the pawl hooks installed)`. Top-level `invariants:` is still parsed and rejected outright,
  not ignored. A step's `retry:` (deterministic and wait only) is implemented: it retries a
  hard-failed body — a non-zero exit, the wall-clock timeout, or unintelligible stdout — before any
  outcome is resolved, and is distinct from `attempts:`, which re-runs a step on a postcondition
  failure (see [design/format-spec.md](design/format-spec.md) §B.16).
- **The enforcement layer is built.** `pawl hook pre|stop` (`internal/hook`, wired by the plugin's
  `hooks/hooks.json` → `bin/pawl-hook`) backs a `PreToolUse`/`Stop` pair. `pawl run` refuses to
  start (exit 4) unless a fresh PreToolUse heartbeat (≤5 minutes old) exists for the working copy, unless
  `--no-enforcement` or `PAWL_ENFORCEMENT=off` is passed; a resume needs no heartbeat and keeps the
  mode bound at run start. The `Stop` hook refuses to end the driving
  session's turn while its run's cursor is at an `agentic`/`parallel` step awaiting `pawl submit`
  (`wait`/`human` cursors and `BLOCKED` runs are exempt); a subagent may not mutate VCS while any run
  is live. See [docs/dogfood.md](docs/dogfood.md) for what that means in practice.
- `install.sh` and the release pipeline behind it exist (`.goreleaser.yaml`,
  `.github/workflows/release.yml`, cross-compiling `pawl` for linux/darwin × amd64/arm64), and
  tagged releases are published on GitHub for `install.sh` to fetch — see
  [docs/install.md](docs/install.md). There is also a Claude Code plugin (see Installation below)
  that ships the `/agent-pawl:pawl` skill, but a plugin cannot ship a compiled Go binary, so it
  still depends on installing the binary separately (`install.sh` or a source build).
- `pawl poll --run … --step …` drives a `wait` step; `pawl hook pre|stop` is the `PreToolUse`/`Stop`
  hook entry point (see above).

The workflows under `docs/examples/` beyond `green-tests` remain authoring exercises rather than
verified-runnable artefacts.

## Installation

### The CLI

The recommended install is `install.sh`, which fetches a prebuilt `pawl` binary from GitHub
Releases — no Go toolchain required:

```
curl -fsSL https://raw.githubusercontent.com/dcferreira/agent-pawl/main/install.sh | sh
```

It installs to `$HOME/.local/bin` by default (override with `INSTALL_DIR`), verifies the download
against the release's `checksums.txt`, and prints a `PATH` reminder if needed. See
[docs/install.md](docs/install.md) for the full walkthrough, including pinning a version with
`PAWL_VERSION`.

If you'd rather build from source, the `pawl` binary is also a normal Go build:

```
go install github.com/dcferreira/agent-pawl/cmd/pawl@latest
```

or, from a clone of this repo:

```
make install
```

Either way, make sure `$(go env GOPATH)/bin` is on your `PATH`. See
[docs/install.md](docs/install.md) for the full walkthrough, including `make build`'s
`./dist/pawl` for a build that doesn't touch `$GOPATH/bin`.

### The Claude Code plugin

This repo is also a Claude Code plugin (`.claude-plugin/plugin.json` + `.claude-plugin/marketplace.json`)
that ships the `/agent-pawl:pawl` skill:

```
claude plugin marketplace add dcferreira/agent-pawl
```

then use the in-session `/plugin` UI to install `agent-pawl` from that marketplace, or run
`claude plugin install agent-pawl@agent-pawl` directly.

**The plugin does not and cannot ship the `pawl` binary** — a plugin distributes Claude Code
components (skills, commands, agents, hooks), not compiled Go binaries. Installing the plugin
gives you the skill and `bin/pawl`, a wrapper script that execs a real `pawl` from your `PATH` if
one exists, and otherwise fails with an install message pointing back at the CLI instructions
above — it is not a substitute for installing the CLI.

### Working in this repo itself

A Claude Code session opened in a checkout of this repo picks up the skill via
`.claude/skills/pawl`, a relative symlink to `skills/pawl` — no plugin install needed for local
development.

## Reading order

1. **[docs/install.md](docs/install.md)** — build and install the binary that actually exists
   today.
2. **[docs/dogfood.md](docs/dogfood.md)** — run `docs/examples/green-tests` end to end, with a real
   captured transcript and the two traps an author will actually hit.
3. **[skills/pawl/SKILL.md](skills/pawl/SKILL.md)** — the `/agent-pawl:pawl` skill a Claude Code
   session follows to drive a run.
4. **`docs/README.md`** — the user guide written during design; describes the finished system
   (full distribution, `foreach:` fan-out) rather than this build — read it for the
   target shape, not for what runs today.
5. **`DESIGN.md`** — the engine design: handshake, execution, journal and resume, enforcement,
   testing, distribution, open questions.
6. **`design/format-spec.md`** — the normative format: nouns, kinds, fields, validator, roadmap.

## Implementation

Go, module `github.com/dcferreira/agent-pawl`, single binary built from `cmd/pawl`, stdlib
plus `gopkg.in/yaml.v3` only. Package layout: `internal/spec` (YAML types, loader, validator),
`internal/render` (`${key}` substitution), `internal/emit` (stdout grammar), `internal/journal`
(run directory, event log, replay), `internal/engine` (cursor, outcomes, counters), `internal/cli`
(the commands above). DESIGN.md §9's fuller distribution story (self-installing pinned release
binaries via two static hooks) is not built; what exists is the plugin described in Installation
above, plus the `pawl` binary you still build or `go install` yourself — see the Status section
above.
