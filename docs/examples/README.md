# Examples

Workflow YAML plus any scripts each one needs, one directory per example. Each directory has a
`NOTES.md` with the author's design rulings.

Every example here passes `pawl validate`; CI checks this in
`internal/cli/examples_validate_test.go` (`TestShippedWorkflowsValidate`). Beyond validation, each
example is either proven by an e2e test or explicitly labelled an authoring exercise. A guard test,
`internal/cli/examples_status_test.go`, enforces that: an example must be referenced by a test under
`e2e/` or carry the "Authoring exercise, not run" label in its `NOTES.md` (not both), and must be
listed in the table below.

"Proven" means the engine drives the workflow end to end in a test, with agentic answers stubbed
by canned `pawl submit` calls where a step is agentic. No live LLM is involved. An authoring
exercise validates and ships placeholder scripts only; no test runs it and its external dependencies
are not stubbed.

| Example | What it demonstrates | Status |
| --- | --- | --- |
| `green-tests` | A deterministic test step whose failure routes to an agentic fix step, looping until the suite is green or a cap binds. | Proven: `e2e/green_tests_test.go` |
| `foreach-fanout` | `kind: parallel` with `foreach:` over a runtime-discovered list, one deterministic body per item, join routing success/partial/failure. | Proven: `e2e/foreach_fanout_test.go` |
| `foreach-agentic` | `foreach:` fan-out over an agentic body, with an engine-run postcondition that recounts each item. | Proven: `e2e/foreach_agentic_test.go` (canned agentic submits) |
| `wait-for-build` | A `deterministic`, `wait`, `agentic` chain: start a background build, poll it to completion, summarise a pass. | Proven: `e2e/wait_for_build_test.go` |
| `human-approval` | `kind: human`: an agentic summary, a person approves it, a deterministic step records the decision. | Proven: `e2e/human_approval_test.go` (canned agentic submit) |
| `parallel-fanout` | `kind: parallel` with fixed `branches:` (one deterministic, two agentic) and an all-or-nothing join. | Proven: `e2e/parallel_fanout_test.go` (canned agentic submits) |
| `brain-dispatch` | Dispatching a worker session for a task: two agentic steps and six deterministic ones with real postconditions. | Authoring exercise, not run |
| `manage-mr` | Guards, invariants, a `wait` step with `emits: pairs`, a capped review cycle, and a `human` step using `options_from:`. | Authoring exercise, not run |
| `dependency-upgrade` | Named outcomes, an agentic fix with `attempts:`, a capped wait/fix/commit cycle, guards, and two `human` gates. | Authoring exercise, not run |
