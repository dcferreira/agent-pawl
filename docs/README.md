# pawl

`pawl` runs a workflow you wrote in YAML, as a state machine, inside a Claude Code session.
Some steps are shell commands the engine runs itself. Some steps are handed to a subagent,
which works until a postcondition — a command `pawl` runs, not the agent — says it is done.
Some steps wait on CI. Some steps ask you a question. Some steps fan out to independent branches
and join them all-or-nothing.
The engine owns the cursor: the model cannot skip a step, fake one, or decide what comes next.

```yaml
workflow: tidy
start: format
steps:
  - id: format
    kind: deterministic
    run: ruff format .
    postcondition: ruff format --check .
    next: test
  - id: test
    kind: deterministic
    run: pytest -q
    postcondition: pytest -q
terminal: {done: {status: ok}}
```

```
› /pawl run tidy
  hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
  ✔ format → test
  ✔ test → done
  TERMINAL a41c ok
```

## Read in this order

1. [install.md](install.md) — get `pawl` onto your machine.
2. [quickstart.md](quickstart.md) — write and run your first workflow in under ten minutes.
3. [concepts.md](concepts.md) — the eight nouns, and why postconditions are the engine's job.
4. [step-types.md](step-types.md) — the five step kinds at a glance, and which one to reach for.
5. [running.md](running.md) — what a run looks like: the banner, the step lines, `pawl status`, resume, BLOCKED.
6. [writing-workflows.md](writing-workflows.md) — the file skeleton, transitions, state, caps, a full example.
7. The five step kinds, in full:
   [deterministic](steps/deterministic.md) ·
   [agentic](steps/agentic.md) ·
   [wait](steps/wait.md) ·
   [human](steps/human.md) ·
   [parallel](steps/parallel.md)
8. [guards-and-invariants.md](guards-and-invariants.md) — the two enforcement layers, and what they do not cover.
9. [validation.md](validation.md) — every `pawl validate` check and its error message.
10. [cli.md](cli.md) — the full command reference.
11. [troubleshooting.md](troubleshooting.md) and [faq.md](faq.md).

## What it is for

Long procedures that an LLM alone gets wrong: shipping a merge request, upgrading dependencies,
dispatching work. The parts a script does well stay scripts. The parts that need judgement go to a
subagent with a tool allowlist and a machine-checked result. The order is data, in one file, in
your repo, reviewable in a diff.

## What it is not

It is not a CI system — there is no scheduler, no server, no daemon. It is not a general job
runner: a run is attached to one working copy and one Claude Code session at a time. It does not
manage your files; steps do that, and nothing is ever rolled back.

## Not yet

These are planned but not available:

- `pawl graph` — a Mermaid diagram of the parsed graph.
- `pawl validate --walk step=TOKEN,…` — print the step sequence a given outcome assignment produces,
  without executing anything.
- `pawl status --history` — the full journal, not just the current position.
- `pawl run <name> --from <step>` — resume a run at an arbitrary step rather than the one it blocked
  at.
- `foreach:` fan-out over a runtime-discovered list, with a partial-success join. `kind: parallel`
  itself — one declared branch group, joined all-or-nothing — already ships; see
  [steps/parallel.md](steps/parallel.md).
- Workflows shipped inside a plugin. Today a workflow comes from your repo or your home directory.

---

Two files under [review/](review/) are for the design review, not for users:
[DECISIONS-MADE-WHILE-WRITING.md](review/DECISIONS-MADE-WHILE-WRITING.md) and
[INCONSISTENCIES.md](review/INCONSISTENCIES.md).
