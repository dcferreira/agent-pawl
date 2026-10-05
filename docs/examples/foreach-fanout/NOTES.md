# NOTES — what this example exercises

`foreach-fanout` demonstrates `kind: parallel` with `foreach:` (design/format-spec.md §B.15)
end to end with no LLM: `discover` finds a work list at runtime, `check_all` runs one
deterministic body (`check_one`) per item, and the join routes `success` / `partial` /
`failure` to three terminals. Like `green-tests` it is covered by `e2e/`
(`e2e/foreach_fanout_test.go`), which lays it out as `.claude/workflows/foreach-fanout.yaml`
with `scripts/` and `inputs/` beside it and runs it with `pawl run foreach-fanout`.

## What happens

- **`discover`** runs `scripts/discover.sh`, which lists `inputs/*.txt` and emits
  `{"items": ["alpha.txt", …]}` (`emits: json`, `writes: [items]`). The list is not in the
  YAML: the point is that the engine takes the list from workflow state at run time.
- **`check_all`** (`kind: parallel`, `foreach: {over: items, body: check_one, collect: results}`)
  runs `check_one` for each item in order and joins. `outcomes:` routes all three of
  `success`, `partial` and `failure`; `partial` has no default route, so it must be routed.
- **`check_one`** runs `scripts/check-item.sh ${item} ${forbidden}`. One input
  (`charlie.txt`) contains the default marker `BROKEN`, so the script exits non-zero for that
  item alone: that item is a `failure`, the other three succeed, and the join is `partial`.
  `pawl run foreach-fanout forbidden=NEVER-PRESENT` makes every item pass and ends on `all_clean`.
- **Terminals** render `${results}`, the `collect:` key: a json array in list order of
  `{index, item, outcome, writes, error}`. A successful item carries its per-item `writes`
  (`verdict`), a failed one its diagnostic in `error`.

## Rulings

- **`${item}` is one shell-quoted token handed to a script as `$1`, never `sh -c ${item}`.**
  The item comes from workflow state (written by `discover`), so validator rule 28 rejects
  `-c ${item}`. The script also refuses names containing `/` or starting with `.`, since it
  joins the item onto `inputs/`: defence in depth, because quoting only protects the shell.
- **`verdict` is declared in `state:` but is per-item.** A foreach body's `writes:` are
  captured per item into `collect:`, never into global state, and no other step may read them.
  The `state:` declaration exists only because every written key must be declared.
- **`results` is written only by the join** (validator rule 23). A step after the foreach
  reads it; the terminals here simply print it.
- **A hard failure is an item failure, not a run failure.** The non-zero exit of one body
  makes that item `failure`; the join decides what the run does.
- **`check_one` declares no `next:`/`outcomes:`/`attempts:`** (rule 24): the foreach step owns
  all routing. It could declare `retry:`, which would be per item.

## What this proves, and what it does not

It proves: runtime-discovered list, frozen as a snapshot on the step's `STEP_ENTER`;
per-item deterministic execution with per-item postcondition and per-item writes; the
three-way join; `collect:` written before the terminal renders it; `partial` routing. It
does not prove: `agentic` bodies (supported in this build, but exercised by `docs/examples/foreach-agentic`, not this example), concurrent item execution
(items run sequentially), or crash-resume mid-fan-out (covered by `internal/engine` unit
tests, not by this example's e2e).
