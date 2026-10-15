# human-approval example

This example exists to exercise `kind: human` end to end in agent-pawl.

It runs three steps in sequence: an `agentic` step summarizes this file,
a `human` step asks a person to approve that summary (a static-option
question routed via `outcomes:`, with the chosen answer also recorded to
`state:` via `writes:`), and a `deterministic` step records the approved
decision before the run ends.

Run it with `pawl run human-approval`, then respond to the `ASK` prompt with
`pawl submit --run <id> --step ask_approval --json '<answer>'` once you've
answered the question the session put to you.

This example is driven end to end by `e2e/human_approval_test.go` (approve and revise routes). The agentic `summarize` answer and the human answer there are canned submits: no live subagent or person was involved.
