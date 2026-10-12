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
- [ ] **`pawl run` refuses a binary whose version doesn't match the plugin's pin**, naming both
  versions ([format-spec §I](../design/format-spec.md#i-cli-and-roadmap),
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
- [ ] A `when:` predicate. §I lists it as outstanding but §K lists `when:` as rejected, so
  settle that first.

## Docs and examples

- [ ] Replace the illustrative run output in [docs/README.md](README.md) and
  [quickstart.md](quickstart.md) with captured output. Real `pawl` prints `DISPATCH`/`TERMINAL`
  blocks, not the `✔ step → next` lines shown there.
- [ ] Prove the remaining `docs/examples/` run, or label them as sketches. Only `green-tests`,
  `foreach-fanout` and `foreach-agentic` are covered by `e2e/`.

## Design questions to settle

- [ ] Decide on, or explicitly defer, each open question in
  [DESIGN.md §8](../DESIGN.md#8-open-questions). Examples: whether guards are worth keeping, and
  whether one engine-wide wall-clock ceiling is enough.

## After v1

- Plugin-shipped workflows: only "if free", meaning only if Claude Code exposes enabled-plugin
  paths to hooks and skills without dedicated code
  ([DESIGN.md §9](../DESIGN.md#9-implementation-and-distribution)).
