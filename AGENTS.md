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

[`docs/status.md`](docs/status.md) is the authority on what runs (see
[what runs today](docs/status.md#what-runs-today) and [not built](docs/status.md#not-built)).
[`docs/compatibility.md`](docs/compatibility.md) says what 1.x freezes. Which examples are proven by
`e2e/` and which are labelled authoring exercises is in
[`docs/examples/README.md`](docs/examples/README.md). `docs/roadmap.md` is the to-do list for v1.
`DESIGN.md` §9 marks each distribution claim built, not built or superseded, row by row.

If you're implementing something that DESIGN.md describes but `docs/status.md` doesn't
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
  pre|stop`: no filesystem I/O or clock reads, just `[]LiveRun` + a parsed payload (+ for Stop, the session's
  `SubagentStop` markers and `now`) in, a `Decision` out.
- `internal/cli` — the `pawl` subcommands, including `hook.go` (`pawl hook pre|stop|subagent-stop`) and
  `enforce.go` (`pawl run`'s heartbeat check and refusal).
- `e2e/` — end-to-end tests that run some of `docs/examples/` and the plugin launcher and hook
  wrapper; see `docs/examples/README.md` for which examples.
  `e2e/docs_output_test.go` checks every `pawl` output block in `docs/` that sits under a
  `<!-- pawl-capture: <id> -->` marker against a real run (an unmarked block is not checked, so mark
  any new one); `UPDATE_DOCS=1 go test ./e2e -run
  TestDocsOutput` rewrites them. Never hand-edit a captured block.
- `docs/examples/` — workflow YAML + scripts; each has a `NOTES.md` with the author's design rulings.
  `docs/examples/README.md` lists which are proven by `e2e/` and which are authoring exercises
  (enforced by `internal/cli/examples_status_test.go`).
- `testdata/fixture/` — a tiny Go module (a two-line `Add` that subtracts) used by
  `docs/examples/green-tests` and `e2e/`.
- `design/format-spec.md` — **normative** for what a workflow author writes.
- `DESIGN.md` — the engine design (handshake, execution, resume, enforcement, testing,
  distribution). §9 marks each claim built, not built or superseded; `docs/status.md` is the authority.
- `docs/` — user-facing docs. `docs/install.md` and `docs/dogfood.md` describe this build
  honestly; `docs/status.md` is the authoritative what-runs-today list and `docs/roadmap.md` the to-do list
  for v1 (both moved out of `README.md`, which is now a short newcomer intro with validated
  examples); `docs/README.md` is the user guide for this build; `docs/releasing.md`
  is the release-management flow (see the Conventions section below).
- `.changes/` — changie's fragment store: `unreleased/*.yaml` (one per PR), `vX.Y.Z.md` per
  release, `header.tpl.md`. `CHANGELOG.md` at the repo root is generated from these by `changie
  merge` — never hand-edited. `scripts/release/` — the check and release scripts
  `changelog.yml`/`ci.yml`/`release.yml` run; see `docs/releasing.md`.

### This repo is also a Claude Code plugin

- `.claude-plugin/plugin.json` + `marketplace.json` — plugin manifest, validated in CI by
  `claude plugin validate . --strict`.
- `skills/pawl/SKILL.md` — the canonical, single source of truth for the `/agent-pawl:pawl` skill.
- `.claude/skills/pawl` is a **relative symlink to `skills/pawl`**, so a Claude Code session opened
  in this repo picks up the skill without a plugin install. Do not replace it with a copy — that
  forks the skill into two sources of truth that will drift.
- **The plugin ships the binary.** Each release's workflow uploads a per-release plugin zip
  (`agent-pawl-plugin_<ver>.zip`) whose root is the plugin root: `.claude-plugin/plugin.json`,
  `hooks/`, `skills/pawl/`, `bin/`, and the platform binaries under
  `libexec/<os>_<arch>/pawl` (darwin/linux × amd64/arm64). The repo's own
  `.claude-plugin/marketplace.json` lists the plugin with an `archive` source — the latest release's
  zip URL pinned by sha256 (Claude Code rejects a mismatch), not `"./"` — and `release.yml` commits
  the new URL + sha256 to `main` after each release (`scripts/release/update-repo-marketplace.sh`;
  `check-version-consistency.sh` validates the entry). Users `/plugin marketplace add
  dcferreira/agent-pawl` and enable marketplace auto-update, so plugin and binary move together — see
  `docs/install.md`. A release-URL marketplace is not addable (github.com URLs are treated as git
  repos). Contributors test local plugin changes with `claude --plugin-dir .`; adding the checkout as
  a marketplace installs the release zip, not the working tree. (The older
  claim that "a plugin cannot ship a compiled Go binary" is wrong; don't reintroduce it.)
- `bin/pawl` is a committed **launcher script**, not a build artifact: in an installed plugin it
  execs the bundled `libexec/<os>_<arch>/pawl`; in a source checkout (no `libexec/`, e.g. this repo
  used via `--plugin-dir`) it falls back to a `pawl` off `PATH`. That
  fallback keeps its self-recursion guards (a plugin puts its `bin/` on `PATH`, so the script could
  otherwise find and re-exec itself). `pawl update` refuses on a plugin-managed binary (one whose
  symlink-resolved path is `<root>/libexec/<os>_<arch>/pawl` with `<root>/.claude-plugin/plugin.json`
  present) and says to run `claude plugin update agent-pawl@agent-pawl`.
- `bin/pawl-hook` is the committed POSIX sh entry point `hooks/hooks.json` binds `PreToolUse`
  (matcher `Bash`), `Stop` and `SubagentStop` to (`${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook pre|stop|subagent-stop`): a cheap fast path
  that exits 0 with no live run and no `pawl` mention in the payload (for `subagent-stop`, with no live run
  alone), otherwise pipes stdin to the
  sibling `bin/pawl` launcher's `hook pre|stop|subagent-stop`. A deny/block is exit 0 with Claude Code's JSON
  decision on stdout, never exit 2: `pawl-hook` maps every non-zero binary exit to exit 1 (fail
  open), because a `pawl` binary older than the plugin exits 2 (usage) for the unknown `hook`
  subcommand, which Claude Code would read as "block". Non-plugin users wire `pawl hook pre`/`pawl hook
  stop`/`pawl hook subagent-stop` directly in their own `settings.json` — see `docs/install.md#hooks`.

## Build / test / verify

Makefile targets (all real, all in CI or documented for local use):

- `make build` — `go build -o dist/pawl ./cmd/pawl`. **Output is `dist/pawl`, never `bin/`** —
  `bin/` is the committed plugin launcher above; writing a compiled binary there would clobber it.
- `make install` — `go install ./cmd/pawl` (installs to `$(go env GOPATH)/bin`).
- `make test` / `make test-race` — `go test ./...` / `go test -race ./...`.
- `make fmt` — `go fmt ./...`, **rewrites files**.
- `make fmt-check` — `gofmt -l .`, fails if anything is unformatted, **does not rewrite**. This is
  what CI runs, not `make fmt`.
- `make vet` — `go vet ./...`.
- `make staticcheck` — pinned version (`v0.8.1`) run via `go run`, so no separate install needed.
- `make test-release-checks` — `bash scripts/release/test-checks.sh`, unit tests for
  `scripts/release/*.sh` against throwaway git fixture repos; no network. See `docs/releasing.md`.
- `make check` — `fmt vet test test-install test-release-checks` (uses the rewriting `fmt`, so it's
  a dev convenience, not what CI gates on for formatting).

CI (`.github/workflows/ci.yml`) gates a PR on: `make fmt-check`, `make vet`, `make staticcheck`,
`go mod tidy` producing no diff to `go.mod`/`go.sum`, `claude plugin validate . --strict`,
`scripts/release/check-version-consistency.sh` (plugin.json's version vs. the latest `.changes/`
release), and both `make test` and `make test-race`; the same job also runs
`make test-release-checks` as a separate step. `.github/workflows/changelog.yml` gates a PR separately (so labeling a
PR doesn't rerun the whole suite) on `scripts/release/check-fragment.sh` and
`scripts/release/check-no-version-bump.sh` — see `docs/releasing.md`. Existing tests and `e2e/` set
`PAWL_ENFORCEMENT=off` in their
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

- **PR descriptions fill in `.github/pull_request_template.md`** (the single source of truth for
  the headings), following `.claude/skills/pr-description/SKILL.md` (repo-local skill, not part of
  the plugin): Conventional Commit title, changelog fragment or `skip changelog`, writing rules.
- **Commits are Conventional Commits** (`feat:`, `fix:`, `ci:`, `docs:`, `test:`), imperative
  subject line, a body explaining *why* and any non-obvious trade-off, a `Co-Authored-By:` trailer
  where applicable. Never a `Claude-Session:` trailer or session URL (see the pr-description skill).
- **Every PR adds a `.changes/unreleased/` fragment via `changie new`, or gets the `skip
  changelog` label** — `.github/workflows/changelog.yml` enforces one or the other. Never edit
  `CHANGELOG.md`, a `.changes/v*.md` file, or `.claude-plugin/plugin.json`'s `version` field by
  hand in a normal PR: releases only happen through the Release PR workflow
  (`.github/workflows/release-pr.yml`, manually dispatched), which bumps all three together and
  opens a `release/v*` PR for review. Never push a `v*` tag by hand — `release.yml` tags and
  publishes itself, triggered by a merged release PR's push to `main` (a `CHANGELOG.md` diff), not
  by a tag push. See `docs/releasing.md`.
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
  version (see `docs/status.md`).
- Engine execution detail worth knowing before touching `internal/engine/exec.go`: the wall-clock
  timeout is enforced by an independent `time.AfterFunc` signalling the whole process group
  (`Setpgid` + negative pid), not `exec.CommandContext`/`cmd.Cancel` — `sh -c 'cmd &'` makes `sh`
  exit before the ceiling would fire, so `cmd.Cancel` has nothing left to cancel by then. The
  sequence is `SIGTERM` to the group, a `killGrace` (5s) wait for it to exit, then `SIGKILL` of
  whatever remains; worst case a timed-out command takes timeout + 5s.

## Where format questions are settled

`design/format-spec.md` is normative for what an author writes (nouns, kinds, fields, validator
rules, roadmap). `DESIGN.md` defines what the engine does with it. If the two ever seem to
disagree, `design/format-spec.md` wins for authoring questions and `DESIGN.md` for engine-behavior
questions — but check `docs/status.md` first for what this build actually does.
