# NOTES — what this example exercises

`green-tests` is the smallest of the three-plus-one example workflows, and the only one
whose whole run can be driven end to end without an LLM (see the repo's `e2e/` test): a
`deterministic` step (`run_tests`) whose named outcomes route a `FAIL` into an `agentic`
fix step (`fix_tests`), which loops back into `run_tests` until the suite is green, the
fix step's own retry budget is exhausted, or the cycle cap binds.

## The two counters, kept deliberately independent

`fix_tests`'s `postcondition: {command: "sh -c ${build_cmd}"}` is **not** the same check
as `run_tests`'s `${test_cmd}`. The postcondition only asks "did the fix leave the tree
compiling" — a cheap, fast gate that drives `attempts:` (a submission that leaves the
build broken burns an attempt and is re-dispatched with the previous failure text, without
ever re-running the full suite). Whether the fix actually made the tests pass is a
separate question, answered only by re-entering `run_tests`, which drives `max_visits:`.
Collapsing the two checks into one would make the loop degenerate — a submission that
merely compiles would look indistinguishable from one that actually fixes the bug, and
`attempts:` and `max_visits:` would stop being independent budgets. This is the reason
`green-tests` was chosen as the dogfood example over a simpler single-counter loop.

**Why `sh -c ${build_cmd}` and not bare `${build_cmd}`.** `${key}` substitutes into a
shell context as one shell-quoted token (design/format-spec.md §B.2), which is exactly
right when the token is an *argument* inside a larger command line (e.g. `run_tests`'s
own `run: scripts/run-tests.sh ${test_cmd}`, where the script receives it as `$1`). Here
the whole `command:` template *is* the token, so without the `sh -c` wrapper the quoted
multi-word value becomes a single shell word and the shell tries to exec a program
literally named `go build ./...`. Wrapping it in `sh -c` restores a real leading word, so
the outer shell hands `${build_cmd}`'s quoted value to an inner `sh -c` as one argument,
which then runs it as a command line.

**`sh -c ${key}` is legitimate only for an `args:` value, never for a `state:` key that a
step or subagent writes.** A workflow argument comes from whoever invokes `wf run` — the
same person who can already run any command as themselves, so `sh -c ${build_cmd}`
introducing no new capability. A `state:` key can be written by a step's `writes:` (a
deterministic step's own stdout, or an agentic step's typed return, both of which the
engine treats as workflow-internal data, not as instructions) — feeding one of those
into `sh -c` would let a step or subagent smuggle a shell command through a value the
workflow author never intended to execute. `green-tests` never does this: `build_cmd` is
`args:`, and `test_cmd`/`failures`/`fix_summary` never reach a `sh -c` wrapper anywhere in
this workflow.

## `emits: json`, not `pairs` (Ruling R11)

A real test failure is multi-line and whitespace-laden — exactly what `emits: pairs`
(whitespace-separated `k=v` tokens) cannot carry. `scripts/run-tests.sh` prints
`FAIL {"failures": …}` with the captured output JSON-encoded via `jq -Rs .`, so the raw
failure text (including embedded newlines) survives intact into the `failures` state key,
and `fix_tests`'s `description:` can quote it verbatim via `${failures}`. This also means
**`jq` is a prerequisite** for running this example — `scripts/run-tests.sh` checks for it
and reports `FAIL {"failures": "run-tests.sh requires jq"}` if it's missing, so a missing
`jq` still surfaces as an honest, named `FAIL` (and, deterministically, a real test
failure the next time `run_tests` is entered) rather than corrupting the JSON the engine
parses; it does not silently fail the engine, but it does mean the run never actually
observes real test output until `jq` is installed.

## What this exercise could not express

- **A `!cmd` context entry is a convenience, not a dependency.** `fix_tests`'s
  `context: [!cmd "jj diff"]` is there so a real agentic run has the actual diff to look
  at, but per the engine's own contract a failing/missing `!cmd` degrades silently (empty
  context, empty `last_error`) rather than wedging the run. `description:` therefore
  stands on its own via `${failures}` regardless of whether `jj diff` succeeds, is
  available, or the working copy isn't a jj repo at all — the example's correctness never
  depends on that command.
- **No routing on the agentic step itself.** Per format-spec.md §B.6, an `agentic` step
  yields only `success`/`failure`; there is nowhere in this workflow to see the fix
  step's own reasoning branch on anything richer than "did it compile" and "did the
  suite pass on the next `run_tests` visit" — a finer-grained signal (partial progress,
  a fix classified as a real regression vs. a flake) is out of scope for a two-step loop.
- **No human gate.** A real dogfood loop that burns through every `attempts:` and
  `max_visits:` retry lands on `gave_up` — a `blocked` terminal — with no `human` step to
  ask a person how to proceed; `wf run` on the same working copy simply resumes it, per
  design/format-spec.md §B.12.
