# agentic-workflow-fsm

`wf`: a state-machine workflow engine for Claude Code. One YAML file per workflow, pure data,
engine-owned attempt and visit counters, fix-forward semantics, crash-safe resume, and a validator
that refuses dangling edges, undeclared state keys and uncapped cycles before anything runs.
Sequencing lives only in `next:` / `outcomes:` / `catch:`; every step that does something declares
a postcondition the engine — never the agent that did the work — evaluates.

## Status

**It executes.** `wf run` / `wf submit` / `wf status` / `wf abandon` / `wf validate` / `wf list` /
`wf version` are real, working code with unit and end-to-end test coverage (`go test ./...`), and
`examples/green-tests` runs against `testdata/fixture` — see [docs/dogfood.md](docs/dogfood.md)
for a real, captured transcript of that run.

This is a milestone-1 build, and it is deliberately narrower than the design documents below
describe:

- Only two step kinds exist: `deterministic` and `agentic`. `kind: wait`, `kind: human`, and
  `kind: parallel` all parse but are rejected by `wf validate`/`wf run` with a "not implemented in
  this build" (or "reserved for Milestone 3") message — never a silent no-op.
- Top-level `guards:`, `invariants:`, and a step's `retry:` are likewise parsed and rejected, not
  ignored.
- **There is no enforcement layer.** DESIGN.md §5's two static hooks (`PreToolUse`, `Stop`) are
  not implemented. `wf run` prints `enforcement: off (milestone 1)` instead of refusing to start —
  nothing stops a session from walking away from a live run. See
  [docs/dogfood.md](docs/dogfood.md) for what that means in practice.
- There is no plugin, no `install.sh`, and no release binaries. Installation is `make install` /
  `go install ./cmd/wf` — see [docs/install.md](docs/install.md).
- `wf poll` and `wf hook` do not exist, because there is no `wait` step kind and no hooks to
  invoke.

The workflows under `examples/` beyond `green-tests` remain authoring exercises rather than
verified-runnable artefacts.

## Reading order

1. **[docs/install.md](docs/install.md)** — build and install the binary that actually exists
   today.
2. **[docs/dogfood.md](docs/dogfood.md)** — run `examples/green-tests` end to end, with a real
   captured transcript and the two traps an author will actually hit.
3. **[.claude/skills/wf/SKILL.md](.claude/skills/wf/SKILL.md)** — the `/wf` skill a Claude Code
   session follows to drive a run.
4. **`docs/README.md`** — the user guide written during design; describes the finished system
   (all four step kinds, enforcement, distribution) rather than this build — read it for the
   target shape, not for what runs today.
5. **`DESIGN.md`** — the engine design: handshake, execution, journal and resume, enforcement,
   testing, distribution, open questions.
6. **`design/format-spec.md`** — the normative format: nouns, kinds, fields, validator, roadmap.

## Implementation

Go, module `github.com/dcferreira/agentic-workflow-fsm`, single binary built from `cmd/wf`, stdlib
plus `gopkg.in/yaml.v3` only. Package layout: `internal/spec` (YAML types, loader, validator),
`internal/render` (`${key}` substitution), `internal/emit` (stdout grammar), `internal/journal`
(run directory, event log, replay), `internal/engine` (cursor, outcomes, counters), `internal/cli`
(the commands above). `DESIGN.md` §9's plugin/hooks/release-binary distribution story is not
built; see the Status section above.
