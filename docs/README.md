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
    next: done
terminal: {done: {status: ok}}
```

<!-- pawl-capture: readme/tidy-run (captured output added in the next PR) -->

## Read in this order

1. [install.md](install.md) — get `pawl` onto your machine.
2. [status.md](status.md) — what this build runs, and what it does not.
3. [quickstart.md](quickstart.md) — write and run your first workflow in under ten minutes.
4. [concepts.md](concepts.md) — the eight nouns, and why postconditions are the engine's job.
5. [step-types.md](step-types.md) — the five step kinds at a glance, and which one to reach for.
6. [running.md](running.md) — what a run looks like: the banner, the step lines, `pawl status`, resume, BLOCKED.
7. [writing-workflows.md](writing-workflows.md) — the file skeleton, transitions, state, caps, a full example.
8. The five step kinds, in full:
   [deterministic](steps/deterministic.md) ·
   [agentic](steps/agentic.md) ·
   [wait](steps/wait.md) ·
   [human](steps/human.md) ·
   [parallel](steps/parallel.md)
9. [guards-and-invariants.md](guards-and-invariants.md) — the two enforcement layers, and what they do not cover.
10. [validation.md](validation.md) — every `pawl validate` check and its error message.
11. [cli.md](cli.md) — the full command reference.
12. [troubleshooting.md](troubleshooting.md) and [faq.md](faq.md).
13. [compatibility.md](compatibility.md) — what 1.x promises not to break, and what is internal.

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

What this build does not do yet is listed in [status.md#not-built](status.md#not-built); plans are in [roadmap.md](roadmap.md).

---

Two files under [review/](review/) are for the design review, not for users:
[DECISIONS-MADE-WHILE-WRITING.md](review/DECISIONS-MADE-WHILE-WRITING.md) and
[INCONSISTENCIES.md](review/INCONSISTENCIES.md).
