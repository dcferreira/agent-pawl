# `parallel` steps

Fan out to several branches at once and join them all-or-nothing. Use it when two or more pieces
of work are genuinely independent — nothing one branch does can affect another — and you want them
running concurrently instead of one after another. See [step-types.md](../step-types.md) for how
the five kinds compare.

## Fields

- **`branches:`** — required, at least 2 entries. Each names an already-declared `deterministic` or
  `agentic` step (no nesting: a branch cannot itself be `wait`, `human` or `parallel`). A branch may
  not be the workflow's `start:` step, may not appear twice in the same `branches:`, and may be
  claimed by only one `parallel` step in the whole file.
- A branch step declares **none** of `next:`, `outcomes:`, `catch:`, `attempts:`, `attempt_key:`,
  `max_visits:` or `retry:` — the owning `parallel` step is the sole owner of routing and retry for
  the whole group. A branch may still declare `postcondition:` and `writes:`; those are its own.
- **`postcondition:`** on the `parallel` step itself — optional, same as `deterministic`: the group
  outcome (below) is usually check enough.

## Example

```yaml
  - id: fanout
    kind: parallel
    branches: [branch_a, branch_b, branch_c]
    next: join

  - id: branch_a
    kind: deterministic
    run: echo "branch_a_result=$(wc -l < README.md | tr -d ' ')"
    emits: pairs
    writes: [branch_a_result]
    postcondition: {all_set: [branch_a_result]}

  - id: branch_b
    kind: agentic
    description: |
      Summarize README.md's first paragraph in one sentence into `summary_b`.
    subagent_args: {tools: [Read], model: haiku}
    writes: {summary_b: {type: string}}
    postcondition: {all_set: [summary_b]}

  - id: branch_c
    kind: agentic
    description: |
      Summarize DESIGN.md's Purpose section in one sentence into `summary_c`.
    subagent_args: {tools: [Read], model: haiku}
    writes: {summary_c: {type: string}}
    postcondition: {all_set: [summary_c]}

  - id: join
    kind: deterministic
    run: echo "join ok"
    postcondition: {all_set: [branch_a_result, summary_b, summary_c]}
    next: done
```

`fanout` has no `outcomes:` shown here — with only one target for both `success` and `failure`, a
plain `next:` covers it; if the two outcomes need different targets, route with `outcomes: {success:
…, failure: …}` exactly like `deterministic`.

The engine dispatches every deterministic branch in-process immediately, and renders every agentic
branch into **one** `DISPATCH_PARALLEL` block — `branch_b` and `branch_c` together, not one after
the other — so the driving session fires two genuinely concurrent subagent calls. The group's
outcome resolves only once every branch has transitioned: `success` iff every branch's own outcome
was `success`, otherwise `failure`. There is no partial-success outcome — a still-outstanding
branch always leaves the run parked, never abandoned, and `join` is never entered until all three
branches have landed.

## Outcomes

A `parallel` step produces exactly `success` / `failure`, same shape as `agentic` — never an
author-named token of its own, and never one from a branch's stdout either (a branch has no
`outcomes:` of its own to produce one from). If a decision needs to know *which* branch failed, or
needs what a branch wrote, route from a following `deterministic` step that reads the branches'
`writes:`.

## Common mistakes

- Giving a branch its own `next:`/`outcomes:` — rejected outright; the `parallel` step owns routing
  for the whole group.
- Reaching for `parallel` when the branches aren't actually independent (one reads what another
  writes) — that's a race, not a fan-out; make it two sequential steps instead.
- Expecting a partial-success join — there isn't one in this build. One branch failing fails the
  whole group; `foreach:` with a partial-success join is a later milestone
  ([design/format-spec.md](../../design/format-spec.md) §I).
- Nesting a `wait` or `human` step as a branch — not supported; a branch must be `deterministic` or
  `agentic`.

Back to [writing-workflows.md](../writing-workflows.md).
