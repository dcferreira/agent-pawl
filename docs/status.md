# Status

What this build actually runs. This page is the authority on that; every other doc points here
instead of restating it. For what this build does not do, see [Not built](#not-built); plans are in
[roadmap.md](roadmap.md); what 1.x freezes is in [compatibility.md](compatibility.md).

## What runs today

**It executes.** `pawl run` / `pawl submit` / `pawl status` / `pawl abandon` / `pawl validate` / `pawl list` /
`pawl version` / `pawl update` are real, working code with unit and end-to-end test coverage (`go test ./...`), and
`docs/examples/green-tests` runs against `testdata/fixture` — see [docs/dogfood.md](dogfood.md)
for a real, captured transcript of that run.

`pawl` is a pre-1.0 build:

- All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, and `parallel`
  (single-group, all-or-nothing `branches:` join — [design/format-spec.md](../design/format-spec.md)
  §B.15), plus `foreach:` fan-out over a runtime-discovered json list with per-item postconditions
  and a `success`/`partial`/`failure` join, over `deterministic` or `agentic` bodies. An agentic body's items are dispatched together in one
  `DISPATCH_PARALLEL` and answered with `pawl submit --item N`, with a per-item `attempts:` budget that
  re-dispatches only the failed item; deterministic items run one after another.
- Top-level `guards:` is parsed and validated (id required+unique, `match:` required, must
  compile as a Go RE2 regexp, and must not be able to match zero characters; `only_in:` required —
  rule 15 checks every entry names a declared step; `only_in: []` denies everywhere — see
  [internal/guard](../internal/guard)), and is enforced by the `PreToolUse` hook, advisory and
  pattern-matched: `pawl run`'s banner prints `guards: N advisory (pattern-matched)` when the hooks
  are on, or `guards: N declared, NOT enforced (enforcement off)` when enforcement is off, and
  `pawl validate` (which has no run) prints `guards: N declared (enforced only when a run starts
  with the pawl hooks installed)`. A step's `retry:` (deterministic and wait only) is implemented: it retries a
  hard-failed body — a non-zero exit, the wall-clock timeout, or unintelligible stdout — before any
  outcome is resolved, and is distinct from `attempts:`, which re-runs a step on a postcondition
  failure (see [design/format-spec.md](../design/format-spec.md) §B.16).
- **The enforcement layer is built.** `pawl hook pre|stop|subagent-stop` (`internal/hook`, wired by the plugin's
  `hooks/hooks.json` → `bin/pawl-hook`) backs a `PreToolUse`/`Stop`/`SubagentStop` set (`SubagentStop` only records a
  60s single-use marker that excuses one Stop in the background-subagent delivery race). `pawl run` refuses to
  start (exit 4) unless a fresh PreToolUse heartbeat (≤5 minutes old) exists for the session (`CLAUDE_CODE_SESSION_ID`) or, failing that, the working copy, unless
  `--no-enforcement` or `PAWL_ENFORCEMENT=off` is passed; a resume needs no heartbeat and keeps the
  mode bound at run start. The `Stop` hook refuses to end the driving
  session's turn while its run's cursor is at an `agentic`/`parallel` step awaiting `pawl submit`
  (`wait`/`human` cursors and `BLOCKED` runs are exempt); a subagent may not mutate VCS while any run
  is live. See [docs/dogfood.md](dogfood.md) for what that means in practice.
- Top-level `invariants:` IS engine-checked: the engine evaluates every declared invariant, in
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
- `pawl poll --run … --step …` drives a `wait` step; `pawl hook pre|stop|subagent-stop` is the
  `PreToolUse`/`Stop`/`SubagentStop` hook entry point (see the enforcement bullet above).
- **The CLI contract is frozen for 1.0.** Every command parses flags the same way (`--f v` and
  `--f=v`; a repeated, empty-valued or unknown flag is exit 2; `-h`/`--help` prints usage and exits
  0), and the exit codes are one table (0 ok, 1 not found/ambiguous, 2 your input is wrong, 3 run
  blocked, 4 refused, 5 internal/environment error, panics included) with one exception, `pawl hook`,
  which fails open and never exits 2. `pawl status --json` carries `schema_version: 1`, a resolvable
  `workflow` name and a separate `workflow_path`. `pawl run` refuses (exit 4) a binary whose version
  differs from the plugin's, via the launcher's `PAWL__PLUGIN_VERSION` (skipped when unset, and for a
  `dev` build). See [cli.md](cli.md).
A `docs/examples/` workflow is proven to run only if a test in [e2e/](../e2e/) runs it; one without
such a test is an authoring exercise.

## Not built

The docs or design describe these, and this build does not do them. Plans live in
[roadmap.md](roadmap.md), not here.

- `pawl graph`: not built. See [roadmap](roadmap.md#cli).
- `pawl validate --walk`: not built. See [roadmap](roadmap.md#cli).
- `pawl status --history`: not built. See [roadmap](roadmap.md#cli).
- `pawl run <name> --from <step>`: not built. See [roadmap](roadmap.md#cli).
- `foreach:` `concurrency:`: not built. See [roadmap](roadmap.md#workflow-format).
- An `outcome:` enum member of the agentic return schema: not built. See
  [roadmap](roadmap.md#workflow-format).
- Plugin-shipped workflows: not built. See [roadmap](roadmap.md#after-v1).
- A skill-run `install.sh` that pins `~/.claude/pawl/bin/pawl`: not built, and superseded by the
  bundled binary. See [roadmap](roadmap.md#distribution).
- Homebrew (there is no formula or tap): not built. See [roadmap](roadmap.md#after-v1).

## Reading order

1. **[docs/install.md](install.md)** — build and install the binary that actually exists
   today.
2. **[docs/dogfood.md](dogfood.md)** — run `docs/examples/green-tests` end to end, with a real
   captured transcript and the two traps an author will actually hit.
3. **[skills/pawl/SKILL.md](../skills/pawl/SKILL.md)** — the `/agent-pawl:pawl` skill a Claude Code
   session follows to drive a run.
4. **[docs/README.md](README.md)** — the user guide.
5. **[DESIGN.md](../DESIGN.md)** — the engine design: handshake, execution, journal and resume,
   enforcement, testing, distribution, design questions (settled or deferred). §9 marks each
   claim built or not built, row by row.
6. **[design/format-spec.md](../design/format-spec.md)** — the normative format: nouns, kinds,
   fields, validator, roadmap.

## Implementation

Go, module `github.com/dcferreira/agent-pawl`, single binary built from `cmd/pawl`, stdlib
plus `gopkg.in/yaml.v3` only. Package layout: `internal/spec` (YAML types, loader, validator),
`internal/render` (`${key}` substitution), `internal/emit` (stdout grammar), `internal/journal`
(run directory, event log, replay), `internal/engine` (cursor, outcomes, counters),
`internal/guard` (`guards:` parsing and matching), `internal/hook` (the `PreToolUse`/`Stop`
decisions), `internal/selfupdate` (`pawl update`), `internal/cli` (the `pawl` subcommands).
For which of DESIGN.md §9's distribution claims are built, see its table and [Not built](#not-built).
The plugin is described in [install.md](install.md), plus `install.sh`/`go install` for the
standalone binary. See [What runs today](#what-runs-today) above.
