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
args:
  test_cmd:  {type: string, default: "go test ./..."}
  build_cmd: {type: string, default: "go build ./..."}
state:
  failures:    {type: string, default: ""}
  fix_summary: {type: string, default: ""}
steps:
  - id: run_tests
    kind: deterministic
    run: scripts/run-tests.sh ${test_cmd}
    emits: json
    writes: [failures]
    max_visits: 4
    outcomes:
      PASS: done
      FAIL: fix_tests
      exhausted: gave_up
  - id: fix_tests
    kind: agentic
    description: |
      The test suite is failing. Fix the code so that it passes.
      Do not edit, weaken or delete tests to make them pass — fix the code under test.
      The failures:
      ${failures}
    context:
      - !cmd "git diff"
    writes: {fix_summary: {type: string}}
    subagent_args: {model: sonnet, tools: [Read, Edit, Bash]}
    attempts: 3
    postcondition: {command: "sh -c ${build_cmd}"}
    next: run_tests
terminal:
  done:     {status: ok,      message: "Suite green after ${visits} runs."}
  gave_up:  {status: blocked, message: "${blocked_reason}"}
```

This is [`docs/examples/green-tests/workflow.yaml`](docs/examples/green-tests/workflow.yaml), with one comment trimmed. The subagent can't declare the job done. `pawl` re-runs the tests itself, and the loop ends only
when they pass. After 4 test runs it stops as `blocked` (the last failures stay in the `failures`
state key).

## Install

In Claude Code:

```
/plugin marketplace add dcferreira/agent-pawl
/plugin install agent-pawl@agent-pawl
```

The plugin bundles the `pawl` binary, the skill and the enforcement hooks. Restart Claude Code
afterwards. Third-party marketplaces default to auto-update off; see
[docs/install.md](docs/install.md#install-the-plugin-recommended-inside-claude-code) to enable it.
To use `pawl` in a terminal outside Claude Code, see
[docs/install.md](docs/install.md#using-pawl-in-a-terminal-outside-claude-code).

## Usage

Save the workflow as `.claude/workflows/green-tests.yaml` in your repo and copy
[`docs/examples/green-tests/scripts/run-tests.sh`](docs/examples/green-tests/scripts/run-tests.sh)
to `.claude/workflows/scripts/` (`scripts/` paths resolve next to the workflow file; the script
needs `jq`). Make it executable, since `pawl` runs it directly:

```
chmod +x .claude/workflows/scripts/run-tests.sh
```

Then in Claude Code:

```
/agent-pawl:pawl run green-tests
```

## Learn more

- [Writing workflows](docs/writing-workflows.md) and the [step types](docs/step-types.md)
- [A full captured run](docs/dogfood.md) of `green-tests`
- [CLI reference](docs/cli.md)
- [Compatibility](docs/compatibility.md): what 1.x promises not to break
- [Roadmap to v1](docs/roadmap.md) and [what runs today](docs/status.md)
