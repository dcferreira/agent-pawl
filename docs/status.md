# Status

What this build actually runs. This page is the authority on that: `DESIGN.md` and
`docs/README.md` describe the target system in places. For what's left before v1, see
[roadmap.md](roadmap.md).

## What runs today

**It executes.** `pawl run` / `pawl submit` / `pawl status` / `pawl abandon` / `pawl validate` / `pawl list` /
`pawl version` / `pawl update` are real, working code with unit and end-to-end test coverage (`go test ./...`), and
`docs/examples/green-tests` runs against `testdata/fixture` — see [docs/dogfood.md](dogfood.md)
for a real, captured transcript of that run.

This is a milestone-1 build, and it is deliberately narrower than the design documents
describe:

- All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, and `parallel`
  (single-group, all-or-nothing `branches:` join — [design/format-spec.md](../design/format-spec.md)
  §B.15), plus `foreach:` fan-out over a runtime-discovered json list with per-item postconditions
  and a `success`/`partial`/`failure` join, over `deterministic` or `agentic` bodies. An agentic body's items are dispatched together in one
  `DISPATCH_PARALLEL` and answered with `pawl submit --item N`, with a per-item `attempts:` budget that
  re-dispatches only the failed item; concurrent *execution* of deterministic items
  (`concurrency:`) remains a later slice. The examples are `docs/examples/foreach-fanout`
  (deterministic) and `docs/examples/foreach-agentic` (agentic).
- Top-level `guards:` is now parsed and validated (id required+unique, `match:` required, must
  compile as a Go RE2 regexp, and must not be able to match zero characters; `only_in:` required —
  rule 15 checks every entry names a declared step; `only_in: []` denies everywhere — see
  [internal/guard](../internal/guard)), and is now enforced by the `PreToolUse` hook, advisory and
  pattern-matched: `pawl run`'s banner prints `guards: N advisory (pattern-matched)` when the hooks
  are on, or `guards: N declared, NOT enforced (enforcement off)` when enforcement is off, and
  `pawl validate` (which has no run) prints `guards: N declared (enforced only when a run starts
  with the pawl hooks installed)`. A step's `retry:` (deterministic and wait only) is implemented: it retries a
  hard-failed body — a non-zero exit, the wall-clock timeout, or unintelligible stdout — before any
  outcome is resolved, and is distinct from `attempts:`, which re-runs a step on a postcondition
  failure (see [design/format-spec.md](../design/format-spec.md) §B.16).
- **The enforcement layer is built.** `pawl hook pre|stop` (`internal/hook`, wired by the plugin's
  `hooks/hooks.json` → `bin/pawl-hook`) backs a `PreToolUse`/`Stop` pair. `pawl run` refuses to
  start (exit 4) unless a fresh PreToolUse heartbeat (≤5 minutes old) exists for the session (`CLAUDE_CODE_SESSION_ID`) or, failing that, the working copy, unless
  `--no-enforcement` or `PAWL_ENFORCEMENT=off` is passed; a resume needs no heartbeat and keeps the
  mode bound at run start. The `Stop` hook refuses to end the driving
  session's turn while its run's cursor is at an `agentic`/`parallel` step awaiting `pawl submit`
  (`wait`/`human` cursors and `BLOCKED` runs are exempt); a subagent may not mutate VCS while any run
  is live. See [docs/dogfood.md](dogfood.md) for what that means in practice.
- Top-level `invariants:` IS engine-checked now: the engine evaluates every declared invariant, in
  order, after every step completion and after every `pawl submit`/`pawl poll`, stopping at the
  first violated one; a violation blocks the run with the invariant's `message:` as the reason
  (`pawl run`'s banner prints a separate `invariants: N (engine-checked after every step)` line
  whenever a workflow declares any). For a `kind: parallel` step, invariants are evaluated once at
  the group join rather than after each branch's own submit.
- `install.sh` and the release pipeline behind it exist (`.goreleaser.yaml`,
  `.github/workflows/release.yml`, cross-compiling `pawl` for linux/darwin × amd64/arm64), and
  tagged releases are published on GitHub for `install.sh` to fetch — see
  [docs/install.md](install.md). There is also a Claude Code plugin (see [install.md](install.md))
  that ships the `/agent-pawl:pawl` skill and a `bin/pawl` launcher. When the plugin bundles a
  binary at `libexec/<os>_<arch>/pawl`, the launcher execs it. If that directory exists without a
  binary for this platform, it fails clearly, with no `PATH` fallback. Only a plugin without
  `libexec/` searches `PATH`: a source checkout, `--plugin-dir`, or a v0.6.0 install from before the
  marketplace gained its archive source. Those need the binary installed separately (`install.sh` or
  a source build). From v0.6.0 on, each release publishes the plugin with the bundle, and the repo's own
  `.claude-plugin/marketplace.json` points at the latest release's zip (`/plugin marketplace add
  dcferreira/agent-pawl`), so that install needs no separate binary.
- Releases are cut through a bot-opened **Release PR**: per-PR fragments under `.changes/` are
  batched into `CHANGELOG.md` and a `.claude-plugin/plugin.json` version bump, then a merge to
  `main` tags and publishes via goreleaser in the same run — see
  [docs/releasing.md](releasing.md).
- `pawl poll --run … --step …` drives a `wait` step; `pawl hook pre|stop` is the `PreToolUse`/`Stop`
  hook entry point (see the enforcement bullet above).
- `pawl graph` (a Mermaid diagram of the parsed workflow) is not built yet.

The workflows under `docs/examples/` beyond `green-tests`, `foreach-fanout` and `foreach-agentic` (all covered by
`e2e/`; the last with canned submits, not a live session) remain authoring exercises rather than verified-runnable artefacts.

## Reading order

1. **[docs/install.md](install.md)** — build and install the binary that actually exists
   today.
2. **[docs/dogfood.md](dogfood.md)** — run `docs/examples/green-tests` end to end, with a real
   captured transcript and the two traps an author will actually hit.
3. **[skills/pawl/SKILL.md](../skills/pawl/SKILL.md)** — the `/agent-pawl:pawl` skill a Claude Code
   session follows to drive a run.
4. **[docs/README.md](README.md)** — the user guide written during design. Its "Not yet" list
   names what isn't built, and its sample output is illustrative rather than captured. Read it for
   the target shape, and this page for what runs today.
5. **[DESIGN.md](../DESIGN.md)** — the engine design: handshake, execution, journal and resume,
   enforcement, testing, distribution, open questions.
6. **[design/format-spec.md](../design/format-spec.md)** — the normative format: nouns, kinds,
   fields, validator, roadmap.

## Implementation

Go, module `github.com/dcferreira/agent-pawl`, single binary built from `cmd/pawl`, stdlib
plus `gopkg.in/yaml.v3` only. Package layout: `internal/spec` (YAML types, loader, validator),
`internal/render` (`${key}` substitution), `internal/emit` (stdout grammar), `internal/journal`
(run directory, event log, replay), `internal/engine` (cursor, outcomes, counters),
`internal/guard` (`guards:` parsing and matching), `internal/hook` (the `PreToolUse`/`Stop`
decisions), `internal/selfupdate` (`pawl update`), `internal/cli` (the `pawl` subcommands).
DESIGN.md §9's fuller distribution story (self-installing pinned release binaries via two static
hooks) is not built. What exists is the plugin described in [install.md](install.md), whose
launcher execs a bundled binary when the plugin carries one, plus `install.sh`/`go install` for the
standalone binary. See [What runs today](#what-runs-today) above.
