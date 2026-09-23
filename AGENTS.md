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
describes the target system (enforcement, full distribution, `foreach:` fan-out), not this build.
`DESIGN.md` is mostly current — it opens with its own up-to-date status line — but its enforcement
(§5) and distribution (§9) sections describe what is not yet built. As of now:

- All five step kinds are implemented: `deterministic`, `agentic`, `wait`, `human`, `parallel`
  (single-group, all-or-nothing `branches:` join — `design/format-spec.md` §B.15).
- Top-level `guards:`, `invariants:`, and a step's `retry:` are still parsed and rejected, not
  ignored.
- **There is no enforcement layer.** DESIGN.md §5's `PreToolUse`/`Stop` hooks don't exist.
  `pawl run` prints `enforcement: off (milestone 1)` and starts anyway — nothing stops a session
  from doing the work itself instead of dispatching, or walking away mid-run. See
  `docs/dogfood.md`.
- `pawl poll --run … --step …` drives a `wait` step; `pawl hook` still doesn't exist (no
  enforcement layer to invoke it).
- No `install.sh`, no release binaries, no `-ldflags` version stamping — `pawl version` always
  prints `pawl dev`.
- Only `examples/green-tests` is a verified-runnable artefact (covered by `e2e/`); the other
  `examples/` are authoring exercises, not proven to run.

If you're implementing something that DESIGN.md describes but the README's Status section doesn't
list as built, that's a real gap to either build properly (with tests) or flag — don't paper over
it by writing the design doc's version of reality into code comments or docs.

## Layout

- `cmd/pawl/` — the single binary entrypoint.
- `internal/spec` — YAML types, loader, validator.
- `internal/render` — `${key}` substitution (shell/prose/raw rendering, shell-quoting).
- `internal/emit` — the stdout grammar `deterministic`/`wait` steps use to report outcomes.
- `internal/journal` — run directory, event log, replay, VCS root resolution, run-directory
  slugging.
- `internal/engine` — cursor, outcome routing, counters, shell execution, postcondition
  evaluation.
- `internal/cli` — the `pawl` subcommands.
- `e2e/` — end-to-end tests: one actually runs `examples/green-tests` (via `testdata/fixture`);
  another validates this repo's own `.claude/workflows/review-pr.yaml` and exercises its scripts
  against temp git/jj repos with a fake `gh`.
- `examples/` — workflow YAML + scripts; each has a `NOTES.md` with the author's design rulings.
  Only `green-tests` is proven-runnable.
- `testdata/fixture/` — a tiny Go module (a two-line `Add` that subtracts) used by
  `examples/green-tests` and `e2e/`.
- `design/format-spec.md` — **normative** for what a workflow author writes.
- `DESIGN.md` — the engine design (handshake, execution, resume, enforcement, testing,
  distribution). Target system, not current build.
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
`make test` and `make test-race`. The test job also installs `jq` and a pinned `jj` binary and sets
`PAWL_REQUIRE_VCS_TOOLS=1` so `internal/journal`'s jj-workspace root-resolution test hard-fails
instead of silently skipping when jj is missing — that env var is set only in this repo's own CI,
never assume or rely on it locally.

## Conventions and traps

- **Commits are Conventional Commits** (`feat:`, `fix:`, `ci:`, `docs:`, `test:`), imperative
  subject line, a body explaining *why* and any non-obvious trade-off, trailers for
  `Co-Authored-By:`/`Claude-Session:` where applicable — verified against this repo's actual `jj
  log`/`git log`, not assumed.
- **git is the assumed VCS for contributors; jj is supported but optional.**
  `internal/journal.ResolveRoot` tries `jj workspace root`, then `git rev-parse --show-toplevel`,
  then falls back to (symlink-resolved) cwd — **in that order, deliberately**. Do not reorder it to
  "prefer git" or otherwise "fix" it; the design docs and code comments describing that precedence
  are accurate on purpose (see the `docs: present git as the assumed VCS` commit).
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
- **`jq` is a hard prerequisite of `examples/green-tests`** (not of `pawl` itself):
  `examples/green-tests/scripts/run-tests.sh` shells out to `jq -Rs .` to JSON-encode a possibly
  multi-line test failure. Missing `jq` doesn't crash the script — it reports a named `FAIL` saying
  so — but you'll never see a real test failure until it's installed. CI installs it explicitly.
- **`pawl version` always prints `pawl dev`** when built from source; that's expected, not a
  broken build (no `-ldflags` version stamping exists yet).
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
