# Roadmap to v1

`pawl` is pre-1.0. This page is the to-do list for v1: everything the design documents still mark
as not built, plus known gaps in the docs and examples. It is the starting point for deciding
v1's scope, not a commitment. When something is deferred past v1, move it to
[After v1](#after-v1) rather than deleting it. For exactly what runs today, see
[status.md](status.md).

Tick an item off in the same PR that ships it.

## Distribution

- [x] **The plugin ships the `pawl` binary.** Since v0.6.0, each release publishes a plugin
  archive that bundles the binary, plus a release `marketplace.json` (see [install.md](install.md)).
- [x] **`pawl run` refuses a binary whose version doesn't match the plugin's pin**, naming both
  versions (exit 4; the plugin launcher exports `PAWL__PLUGIN_VERSION`) ([format-spec §I](../design/format-spec.md#i-cli-and-roadmap),
  [DESIGN.md §9](../DESIGN.md#9-implementation-and-distribution)).
- [ ] **Release pipeline polish:** an App-bot committer, and moving goreleaser-action off Node 20
  (#25).

## CLI

From format-spec §I's Milestone 2:

- [ ] `pawl graph`: a Mermaid diagram of the parsed workflow graph.
- [ ] `pawl validate --walk step=TOKEN,…`: print the step sequence a given outcome assignment
  produces, without executing anything.
- [ ] `pawl status --history`: the full journal, not just the current position.
- [ ] `pawl run <name> --from <step>`: resume a run at any step, not only the one it blocked at.

## Workflow format

The outstanding parts of format-spec §I's Milestone 3:

- [ ] `concurrency:`, so deterministic `foreach:` items can run concurrently. Today they run one
  after another; agentic items are already dispatched together.
- [ ] An `outcome:` member of the agentic return schema, constrained to a declared enum.
- `when:` is not planned. It is rejected (format-spec §K); a conditional is a router step plus
  `outcomes:` (format-spec §B.14).

## Docs and examples

- [ ] Replace the illustrative run output in [docs/README.md](README.md) and
  [quickstart.md](quickstart.md) with captured output. Real `pawl` prints `DISPATCH`/`TERMINAL`
  blocks, not the `✔ step → next` lines shown there.
- [ ] Prove the remaining `docs/examples/` run, or label them as sketches. Only `green-tests`,
  `foreach-fanout` and `foreach-agentic` are covered by `e2e/`.

## Design questions to settle

- [x] The questions in [DESIGN.md §8](../DESIGN.md#8-design-questions) are settled, except three
  deferred items (a shared cap for two-entry cycles, `timeout:` on `deterministic`, `writes:`
  granularity on structured keys) and one pending item (whether a non-author can author a real
  workflow, to be settled by a non-author test before 1.0). The three deferred items are additive and can land in any 1.x. Settled decisions
  are frozen in [compatibility.md](compatibility.md).

## After v1

- Plugin-shipped workflows: only "if free", meaning only if Claude Code exposes enabled-plugin
  paths to hooks and skills without dedicated code
  ([DESIGN.md §9](../DESIGN.md#9-implementation-and-distribution)).
