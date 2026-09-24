# AGENTS.md

Orientation for an AI agent working in this repo. It is deliberately short — read the linked files
for depth, don't infer them.

## What this is

`agent-pawl` (binary: `pawl`) is a state-machine workflow engine for Claude Code, written in Go.
A workflow is one YAML file: pure data, engine-owned attempt/visit counters, fix-forward semantics,
crash-safe resume. The central idea: sequencing lives only in `next:`/`outcomes:`/`catch:`, and
every step that does something declares a **postcondition the engine evaluates itself** — never a
claim the agent that did the work gets to make. The engine owns the run cursor; a session's job is
to do what `pawl run`/`pawl submit` tells it and report back exactly what's asked, nothing more.

## Status: read this before trusting any design doc

**`README.md`'s "Status" section is the authority on what actually runs.** `docs/README.md`
describes the target system (full distribution, `foreach:` fan-out, `invariants:`), not this build.
`DESIGN.md` is mostly current — it opens with its own up-to-date status line — but its distribution
(§9) section describes what is not yet built. As of now:

- All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, `parallel`
  (single-group, all-or-nothing `branches:` join — `design/format-spec.md` §B.15).
- Top-level `guards:` is now parsed and validated (id required+unique, `match:` required, must
  compile as a Go RE2 regexp matched unanchored, and must not be able to match zero characters;
  `only_in:` required; rule 15 checks every `only_in:` entry names a declared step; see
  `internal/guard`), and is enforced by the `PreToolUse` hook, advisory and pattern-matched, not a
  semantic guarantee: `pawl run`'s banner prints `guards: N advisory (pattern-matched)` when hooks
  are on, or `guards: N declared, NOT enforced (enforcement off)` when
  `--no-enforcement`/`PAWL_ENFORCEMENT=off` is in effect; `pawl validate`, which has no run, prints
  `guards: N declared (enforced only when a run starts with the pawl hooks installed)`. Top-level
  `invariants:`, and a step's `retry:`, are still parsed and rejected outright, not ignored.
- **The enforcement layer is built.** `internal/hook` (pure decisions) plus `internal/cli/hook.go`
  (`pawl hook pre|stop`) back a `PreToolUse`/`Stop` pair the plugin wires via `hooks/hooks.json` →
  `bin/pawl-hook`. `pawl run` refuses to start (exit 4) unless a PreToolUse heartbeat for the
  working copy is on disk and no older than 5 minutes, unless `--no-enforcement`/`PAWL_ENFORCEMENT=off` is
  passed. `Stop` refuses to end the driving session's turn while its run's cursor is at an
  `agentic`/`parallel` step awaiting `pawl submit` (`wait`/`human` cursors and `BLOCKED` runs are
  exempt, at most once per turn); a subagent (`agent_id` present) may not mutate VCS while any run
  is live. See `docs/dogfood.md`.
- `pawl poll --run … --step …` drives a `wait` step; `pawl hook pre|stop` is the `PreToolUse`/`Stop`
  hook entry point (see above).
- `install.sh` and goreleaser-built release binaries exist (tagged releases are published on
  GitHub); release builds are version-stamped via `-ldflags -X main.Version`, but a source build
  (`go build`/`go install`/`make install`) still prints `pawl dev`.
- Only `docs/examples/green-tests` is a verified-runnable artefact (covered by `e2e/`); the other
  `docs/examples/` are authoring exercises, not proven to run.

If you're implementing something that DESIGN.md describes but the README's Status section doesn't
list as built, that's a real gap to either build properly (with tests) or flag — don't paper over
it by writing the design doc's version of reality into code comments or docs.

## Layout

- `cmd/pawl/` — the single binary entrypoint.
- `internal/spec` — YAML types, loader, validator.
- `internal/render` — `${key}` substitution (shell/prose/raw rendering, shell-quoting).
- `internal/emit` — the stdout grammar `deterministic`/`wait` steps use to report outcomes.
- `internal/journal` — run directory, event log, replay, VCS root resolution, run-directory
  slugging, heartbeat/driver files, `live/` symlink lifecycle.
- `internal/engine` — cursor, outcome routing, counters, shell execution, postcondition
  evaluation.
- `internal/hook` — pure `PreToolUse`/`Stop` decisions (`DecidePre`/`DecideStop`) for `pawl hook
  pre|stop`: no filesystem I/O, just `[]LiveRun` + a parsed payload in, a `Decision` out.
- `internal/cli` — the `pawl` subcommands, including `hook.go` (`pawl hook pre|stop`) and
  `enforce.go` (`pawl run`'s heartbeat check and refusal).
- `e2e/` — end-to-end test(s) that actually run `docs/examples/green-tests`.
- `docs/examples/` — workflow YAML + scripts; each has a `NOTES.md` with the author's design rulings.
  Only `green-tests` is proven-runnable.
- `testdata/fixture/` — a tiny Go module (a two-line `Add` that subtracts) used by
  `docs/examples/green-tests` and `e2e/`.
- `design/format-spec.md` — **normative** for what a workflow author writes.
- `DESIGN.md` — the engine design (handshake, execution, resume, enforcement, testing,
  distribution). Current through §5 (enforcement); §9 (distribution) is still the target system,
  not this build.
- `docs/` — user-facing docs. `docs/install.md` and `docs/dogfood.md` describe this build
  honestly; `docs/README.md` describes the finished system (says so explicitly).

### This repo is also a Claude Code plugin

- `.claude-plugin/plugin.json` + `marketplace.json` — plugin manifest, validated in CI by
  `claude plugin validate . --strict`.
- `skills/pawl/SKILL.md` — the canonical, single source of truth for the `/agent-pawl:pawl` skill.
- `.claude/skills/pawl` is a **relative symlink to `skills/pawl`**, so a Claude Code session opened
  in this repo picks up the skill without a plugin install. Do not replace it with a copy — that
  forks the skill into two sources of truth that will drift.
- `bin/pawl` is a committed **wrapper script**, not a build artifact: it execs a real `pawl` off
  `PATH`, with self-recursion guards (a plugin puts its `bin/` on `PATH`, so this script could
  otherwise find and re-exec itself). A plugin cannot ship a compiled Go binary, so this is as far
  as the plugin goes — the real binary is still `go install`ed separately.
- `bin/pawl-hook` is the committed POSIX sh entry point `hooks/hooks.json` binds `PreToolUse`
  (matcher `Bash`) and `Stop` to (`${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook pre|stop`): a cheap fast path
  that exits 0 with no live run and no `pawl` mention in the payload, otherwise pipes stdin to the
  sibling `bin/pawl` wrapper's `hook pre|stop`. A deny/block is exit 0 with Claude Code's JSON
  decision on stdout, never exit 2: `pawl-hook` maps every non-zero binary exit to exit 1 (fail
  open), because a `pawl` binary older than the plugin exits 2 (usage) for the unknown `hook`
  subcommand, which Claude Code would read as "block". Non-plugin users wire `pawl hook pre`/`pawl hook
  stop` directly in their own `settings.json` — see `docs/install.md#hooks`.

## Build / test / verify

Makefile targets (all real, all in CI or documented for local use):

- `make build` — `go build -o dist/pawl ./cmd/pawl`. **Output is `dist/pawl`, never `bin/`** —
  `bin/` is the committed plugin wrapper above; writing a compiled binary there would clobber it.
- `make install` — `go install ./cmd/pawl` (installs to `$(go env GOPATH)/bin`).
- `make test` / `make test-race` — `go test ./...` / `go test -race ./...`.
- `make fmt` — `go fmt ./...`, **rewrites files**.
- `make fmt-check` — `gofmt -l .`, fails if anything is unformatted, **does not rewrite**. This is
  what CI runs, not `make fmt`.
- `make vet` — `go vet ./...`.
- `make staticcheck` — pinned version (`v0.8.1`) run via `go run`, so no separate install needed.
- `make check` — `fmt vet test` (uses the rewriting `fmt`, so it's a dev convenience, not what CI
  gates on for formatting).

CI (`.github/workflows/ci.yml`) gates a PR on: `make fmt-check`, `make vet`, `make staticcheck`,
`go mod tidy` producing no diff to `go.mod`/`go.sum`, `claude plugin validate . --strict`, and both
`make test` and `make test-race`. Existing tests and `e2e/` set `PAWL_ENFORCEMENT=off` in their
shared setup (`setupWorkingCopy` and friends) so the enforcement gate added by `pawl run` doesn't
require a real heartbeat in every test; the gate itself is tested directly in
`internal/cli/enforce_test.go`. The test job installs `jq` (the `green-tests` example's
`run-tests.sh` needs it) and sets `PAWL_REQUIRE_VCS_TOOLS=1`, which makes a missing/failing `git` in
`internal/journal`'s VCS tests a hard failure instead of a silent skip — scoped to `git` only. `jj`
is deliberately *not* installed in this job: root resolution (`internal/journal/root.go`) has no
runtime dependency on either VCS binary, so `TestResolveRoot_JJWorkspace` always skips when `jj` is
missing regardless of `PAWL_REQUIRE_VCS_TOOLS`. `pawl` itself never shells out to `git` or `jj` at
runtime either — see the next section.

## Conventions and traps

- **Commits are Conventional Commits** (`feat:`, `fix:`, `ci:`, `docs:`, `test:`), imperative
  subject line, a body explaining *why* and any non-obvious trade-off, trailers for
  `Co-Authored-By:`/`Claude-Session:` where applicable — verified against this repo's actual
  commit history, not assumed.
- **Contributors may use git, jj, or no VCS at all; nothing here assumes one.**
  `internal/journal.ResolveRoot` never shells out to either: it walks up from cwd looking for the
  nearest directory containing a `.git` or `.jj` entry (either can be a plain file, e.g. a git
  worktree/submodule's `.git`) and returns the first one found; with neither anywhere above cwd it
  falls back to (symlink-resolved) cwd itself. The nearest marker wins — a repo nested inside
  another repo stops at the inner one. Don't reintroduce shelling out to `jj`/`git` here (e.g. `jj
  workspace root` or `git rev-parse --show-toplevel`) to "fix" this — the marker walk is deliberate:
  it needs no subprocess and gives the runtime and any future enforcement hook one shared, pure
  algorithm for working-copy identity (see `internal/journal/root.go`'s doc comment).
- **`${key}` shell-quotes as a single token — and that's an injection defence, not an
  inconvenience.** `render.RenderShell` substitutes every `${key}` occurrence as one
  `shellQuote`d token in `run:`/`poll:`/`check:`/`postcondition.command`. The idiom
  `{command: "sh -c ${key}"}` is legitimate **only when `key` is an `args:` value** — it came from
  whoever invoked `pawl run`, who could already run anything. **Never do this for a `state:` key a
  step or an agentic step's `writes:` wrote** — that data is workflow-internal, and piping it
  through `sh -c` turns whatever produced that state into arbitrary command execution. See
  `docs/dogfood.md` ("The two traps you will actually hit", #1) and `design/format-spec.md` §B.2.
  Related: a real command-injection bug was fixed in `internal/engine/scriptpath.go` — the
  first-token-as-path decision for `run:`/`postcondition.command`/`!cmd` must be made on the
  **unrendered template** (`render.FirstTokenTemplate` + `render.RenderRaw`, then exactly one
  `render.ShellQuote`), never by re-parsing already-shell-quoted output; a value like
  `x'/y $(touch PWNED)'` desynchronised an earlier hand-rolled re-quoting scanner. Read the comment
  at the top of `scriptpath.go` before touching that function.
- **`jq` is a hard prerequisite of `docs/examples/green-tests`** (not of `pawl` itself):
  `docs/examples/green-tests/scripts/run-tests.sh` shells out to `jq -Rs .` to JSON-encode a possibly
  multi-line test failure. Missing `jq` doesn't crash the script — it reports a named `FAIL` saying
  so — but you'll never see a real test failure until it's installed. CI installs it explicitly.
- **A source build's `pawl version` prints `pawl dev`** (`go build`/`go install`/`make install`);
  that's expected, not a broken build — only release binaries carry the goreleaser-stamped
  version (see the Status bullet above).
- Engine execution detail worth knowing before touching `internal/engine/exec.go`: the wall-clock
  timeout is enforced by an independent `time.AfterFunc` killing the whole process group
  (`Setpgid` + negative-pid `SIGKILL`), not `exec.CommandContext`/`cmd.Cancel` — `sh -c 'cmd &'`
  makes `sh` exit before the ceiling would fire, so `cmd.Cancel` has nothing left to cancel by
  then.

## Where format questions are settled

`design/format-spec.md` is normative for what an author writes (nouns, kinds, fields, validator
rules, roadmap). `DESIGN.md` defines what the engine does with it. If the two ever seem to
disagree, `design/format-spec.md` wins for authoring questions and `DESIGN.md` for engine-behavior
questions — but check `README.md`'s Status section first, since either doc may be describing the
target system rather than this build.
