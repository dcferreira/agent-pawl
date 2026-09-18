# agent-pawl

`pawl`: a state-machine workflow engine for Claude Code. One YAML file per workflow, pure data,
engine-owned attempt and visit counters, fix-forward semantics, crash-safe resume, and a validator
that refuses dangling edges, undeclared state keys and uncapped cycles before anything runs.
Sequencing lives only in `next:` / `outcomes:` / `catch:`; every step that does something declares
a postcondition the engine — never the agent that did the work — evaluates.

## Status

**It executes.** `pawl run` / `pawl submit` / `pawl status` / `pawl abandon` / `pawl validate` / `pawl list` /
`pawl version` are real, working code with unit and end-to-end test coverage (`go test ./...`), and
`examples/green-tests` runs against `testdata/fixture` — see [docs/dogfood.md](docs/dogfood.md)
for a real, captured transcript of that run.

This is a milestone-1 build, and it is deliberately narrower than the design documents below
describe:

- Only two step kinds exist: `deterministic` and `agentic`. `kind: wait`, `kind: human`, and
  `kind: parallel` all parse but are rejected by `pawl validate`/`pawl run` with a "not implemented in
  this build" (or "reserved for Milestone 3") message — never a silent no-op.
- Top-level `guards:`, `invariants:`, and a step's `retry:` are likewise parsed and rejected, not
  ignored.
- **There is no enforcement layer.** DESIGN.md §5's two static hooks (`PreToolUse`, `Stop`) are
  not implemented. `pawl run` prints `enforcement: off (milestone 1)` instead of refusing to start —
  nothing stops a session from walking away from a live run. See
  [docs/dogfood.md](docs/dogfood.md) for what that means in practice.
- There is no `install.sh` and no release binaries. The `pawl` binary itself is always installed
  with `make install` / `go install ./cmd/pawl` — see [docs/install.md](docs/install.md). There is a
  Claude Code plugin (see Installation below) that ships the `/agent-pawl:pawl` skill, but a
  plugin cannot ship a compiled Go binary, so it still depends on that separate binary install.
- `pawl poll` and `pawl hook` do not exist, because there is no `wait` step kind and no hooks to
  invoke.

The workflows under `examples/` beyond `green-tests` remain authoring exercises rather than
verified-runnable artefacts.

## Installation

### The CLI

The `pawl` binary is a normal Go build; nothing installs it for you automatically (see below for
why the plugin can't):

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
2. **[docs/dogfood.md](docs/dogfood.md)** — run `examples/green-tests` end to end, with a real
   captured transcript and the two traps an author will actually hit.
3. **[skills/pawl/SKILL.md](skills/pawl/SKILL.md)** — the `/agent-pawl:pawl` skill a Claude Code
   session follows to drive a run.
4. **`docs/README.md`** — the user guide written during design; describes the finished system
   (all four step kinds, enforcement, distribution) rather than this build — read it for the
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
