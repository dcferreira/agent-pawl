# NOTES — what this example exercises

`parallel-fanout` demonstrates `kind: parallel` (design/format-spec.md,
`internal/spec/testdata/parallel_ok.yaml`'s minimal shape, dressed up with
real, verifiable work): a deterministic `setup` step enters a three-branch
`fanout` group — one `deterministic` branch (`branch_a`), two independent
`agentic` branches (`branch_b`, `branch_c`) — and a `join` step that is only
reachable once all three branches have completed.

Two agentic branches, not one, is deliberate: a group with only one
outstanding agentic branch can be joined by a driving session that never
actually dispatches more than one subagent at a time, which doesn't
distinguish "parallel" from "sequential fan-out that happens to use a
different step kind". With two independent agentic branches in the same
`DISPATCH_PARALLEL` block, a correct driving session must fire two real
subagents concurrently — as separate Agent-tool calls in one message — to
complete the workflow at all, which is the actual claim `kind: parallel`
makes.

## The group

- **`fanout`** (`kind: parallel`) declares
  `branches: [branch_a, branch_b, branch_c]` and its own `next: join`. Per
  the engine's all-or-nothing join (`internal/engine/parallel.go`), `join`
  is not reachable until *every* branch has completed — there is no
  separate field to express that; it falls straight out of `fanout` owning
  the group's sole `next:`. No branch step declares its own
  `next:`/`outcomes:`/`attempts:`/etc. (`checkParallelBranches` in
  `internal/spec/validate.go` rejects any branch step that does) —
  `fanout` is the sole owner of routing and retry for the whole group.
- **`branch_a`** (deterministic) counts `README.md`'s own line count and
  writes it into `branch_a_result` — small, real, and independently
  checkable (`wc -l < README.md`).
- **`branch_b`** (agentic) asks a real subagent to read this repo's own
  `README.md` and write a one-sentence summary of its first paragraph into
  `summary_b`.
- **`branch_c`** (agentic) asks a real, independent subagent to read this
  repo's `DESIGN.md` §1 and write a one-sentence summary into `summary_c`.
  Independent context, independent `writes:` key, independent
  `postcondition:` — nothing ties it to `branch_b` except both being
  outstanding in the same `fanout` group.
  Per validate rule 6, an agentic step requires its own `postcondition:`
  regardless of `soft:` — both `branch_b` and `branch_c` declare
  `postcondition: {all_set: [...]}`.
- **`join`** re-observes that all three branches' writes actually landed in
  state (`postcondition: {all_set: [branch_a_result, summary_b,
  summary_c]}`) before transitioning to `done`. This is a belt-and-braces
  check, not new enforcement: the engine's own parallel-join semantics
  already guarantee every branch finished before `join` is even dispatched.

## Dry-run behaviour (confirmed against the real CLI)

`pawl run parallel-fanout` runs `setup` and `branch_a` in-process (both
deterministic, so no dispatch is needed) and emits a single
`DISPATCH_PARALLEL` block containing TWO nested `DISPATCH` sub-blocks —
`branch_b` and `branch_c` — the two outstanding agentic branches. `pawl
status` at that point shows `branch_a_result` already in state and the
cursor parked at `fanout`. A driving session dispatches both subagents
(genuinely concurrently — two Agent tool calls in one message) and submits
each as it returns; the group resolves and `join` runs on whichever
`pawl submit` call closes out the second (last-reporting) branch, landing
on `TERMINAL … ok`. Submitting them in either order produces the same
result — the join doesn't care which branch reports last.

## What this exercise could not express

- **Partial-branch failure handling.** All three branches here are designed
  to always succeed (a line count can't fail; each agentic step's only
  postcondition is "wrote a non-empty string"). The format's story for one
  branch failing while its siblings succeed — does the whole group retry,
  does `fanout` itself gain an `attempts:`/`catch:` of its own, does a
  partially-failed group ever partially commit — is real behavior of
  `kind: parallel` that an all-happy-branches example doesn't exercise. See
  `internal/engine/parallel_test.go` for the engine-level coverage of that
  case; this example is deliberately scoped to the "everything succeeds"
  path so a human can drive it by hand in one sitting.
- **Nested `parallel` groups.** `checkParallelBranches` explicitly forbids
  a branch step whose own `kind` is `parallel` ("no nesting") — out of
  scope for the format as it stands, not just for this example.
