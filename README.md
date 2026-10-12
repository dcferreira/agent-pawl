# agent-pawl

`pawl` ensures your agents follow the steps you want, in the order you want.

Write the steps as a YAML state machine. `pawl` drives the agent through it and won't move on
until a step's check passes.

Built for Claude Code; other harnesses coming soon.

## Example

Run the tests; if they fail, have a subagent fix the code, then run them again:

```yaml
workflow: green-tests
start: run_tests
state:
  failures: {type: string, default: ""}
  fix_summary: {type: string, default: ""}
steps:
  - id: run_tests
    kind: deterministic                 # pawl runs this itself
    run: scripts/run-tests.sh 'go test ./...'   # prints PASS, or FAIL {"failures": "..."}
    writes: [failures]
    max_visits: 4
    outcomes: {PASS: done, FAIL: fix_tests, exhausted: gave_up}
  - id: fix_tests
    kind: agentic                       # handed to a subagent
    description: |
      The tests fail. Fix the code, not the tests:
      ${failures}
    writes: {fix_summary: {type: string}}
    attempts: 3
    postcondition: go build ./...       # pawl checks this, not the agent
    next: run_tests
terminal:
  done:    {status: ok, message: "Tests pass."}
  gave_up: {status: blocked, message: "${blocked_reason}"}
```

The subagent can't declare the job done. `pawl` re-runs the tests itself, and the loop ends only
when they pass. After 4 test runs it stops as `blocked` (the last failures stay in the `failures`
state key). The full version
is [`docs/examples/green-tests`](docs/examples/green-tests/workflow.yaml).

## Install

In Claude Code:

```
/plugin marketplace add https://github.com/dcferreira/agent-pawl/releases/latest/download/marketplace.json
/plugin install agent-pawl@agent-pawl
```

The plugin bundles the `pawl` binary, the skill and the enforcement hooks. Restart Claude Code
afterwards. To use `pawl` in a terminal outside Claude Code, see
[docs/install.md](docs/install.md#using-pawl-in-a-terminal-outside-claude-code).

## Usage

Save the workflow as `.claude/workflows/green-tests.yaml` in your repo and copy
[`docs/examples/green-tests/scripts/run-tests.sh`](docs/examples/green-tests/scripts/run-tests.sh)
to `.claude/workflows/scripts/` (`scripts/` paths resolve next to the workflow file; the script
needs `jq`), then in Claude Code:

```
/agent-pawl:pawl run green-tests
```

## Learn more

- [Writing workflows](docs/writing-workflows.md) and the [step types](docs/step-types.md)
- [A full captured run](docs/dogfood.md) of `green-tests`
- [CLI reference](docs/cli.md)
- [Roadmap to v1](docs/roadmap.md) and [what runs today](docs/status.md)
