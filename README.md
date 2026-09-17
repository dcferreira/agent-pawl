# agentic-workflow-fsm

A design for `wf`: a state-machine workflow engine for Claude Code. One YAML file per workflow, pure
data, with four step kinds (`deterministic`, `agentic`, `wait`, `human`), engine-owned attempt and
visit counters, fix-forward semantics, crash-safe resume, and a validator that refuses dangling edges,
undeclared state keys and uncapped cycles before anything runs. Sequencing lives only in `next:` /
`outcomes:` / `catch:`; every step that does something declares a postcondition the engine — never the
agent that did the work — evaluates.

## Status

**Design only.** Nothing here executes. The workflows under `examples/` are authoring exercises, not
runnable artefacts.

## Reading order

1. **`docs/README.md`** — the user guide: install, quickstart, concepts, the four kinds, the CLI.
2. **`DESIGN.md`** — the engine: handshake, execution, journal and resume, enforcement, testing,
   distribution, open questions.
3. **`design/format-spec.md`** — the normative format: nouns, kinds, fields, validator, roadmap.

## Implementation

High-level decisions only, in `DESIGN.md` §9: Go single binary, distributed as a Claude Code plugin
(skill + two static hooks + install script), workflow resolution repo → user. Repo layout, build and
release process, and task order are deliberately not decided yet.
