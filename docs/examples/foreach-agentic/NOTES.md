# foreach-agentic: author's notes

A `kind: parallel` step with `foreach:` over an **agentic** body. `discover` finds the work list at
runtime (every `inputs/*.txt` next to the workflow); `count_all` dispatches one subagent per item
to count its words; the engine recounts with `wc -w` as each item's postcondition.

## Rulings

- **The postcondition re-observes reality.** `scripts/check-count.sh ${item} ${word_count}` recounts
  the file itself and compares; the subagent's claim is never trusted. `${item}` (the file name) and
  `${word_count}` (the item's own write, integer-typed) are each one shell-quoted token, never
  `sh -c` — both come from workflow state / a subagent, not from whoever invoked `pawl run`
  (`design/format-spec.md` §B.2, rule 28). The script also refuses path-like item names.
- **`attempts: 2` is per item.** A wrong count re-dispatches only that item, at `attempt: 2 of 2`,
  carrying the script's failure text (`bravo.txt: you reported 99 words but wc -w counts 5`), while
  siblings may still be outstanding. A second miss resolves that item `failure` with an
  "exhausted its attempts" error in its `collect:` entry, and the join routes `partial`.
- **Cheap subagents.** `subagent_args: {"model":"haiku","tools":["Read"]}` is all the task needs.
- **Routes.** `success` -> `all_ok`, `partial` -> `some_bad` (both `ok`: the run did its job and
  reports which items failed), `failure` -> `all_bad` (`blocked`). All three render `${results}`.
- **Item path.** The subagent is told `.claude/workflows/inputs/${item}`, relative to the
  repository root: this assumes the example is installed repo-locally as
  `.claude/workflows/foreach-agentic.yaml` with `scripts/` and `inputs/` beside it (as the e2e
  does). Adjust the path if you install it elsewhere.

## What is proven, and what is not

- **Proven by `e2e/foreach_agentic_test.go`** (real `pawl` binary, canned `pawl submit` JSON, no
  LLM): one `DISPATCH_PARALLEL` with a nested block per item and correct `--item N` submit lines;
  out-of-order submits with the `~ item … recorded (… waiting on: …)` interstitial; a per-item
  retry block (`attempt: 2 of 2` plus the previous failure text) for only the wrong item while
  another is still outstanding; the all-correct run ending `all_ok`; and the run where one item
  fails twice ending `some_bad` with the exhaustion error in `${results}`.
- **Needs a live session:** that a real Claude Code session dispatches the item blocks as genuinely
  concurrent subagents, that `haiku` with `Read` counts words and returns the object in the shape
  `return:` asks for, and that the driving session submits each item with its own printed command
  the moment it returns. None of that is exercised by the e2e.
