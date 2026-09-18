# Quickstart

<!-- TODO: once the plugin exists, open with the actual install steps (plugin install command, what to
expect on first use) before "Write it". Until then, install.md describes the intended flow. -->

Ten minutes, one workflow, no agent. You need [`pawl` installed](install.md) and a repo to stand in.

`/pawl` is a Claude Code skill: invoking it, the model calls the `pawl` engine and follows its
instructions — you don't drive the engine by hand.

## 1. Write it

Workflows live in `.claude/workflows/` at the root of your working copy.

```
mkdir -p .claude/workflows
```

`.claude/workflows/hello.yaml`:

```yaml
workflow: hello
start: greet
steps:
  - id: greet
    kind: deterministic
    run: echo "hello from ${name}" > hello.txt
    postcondition: test -s hello.txt
    next: count
  - id: count
    kind: deterministic
    run: wc -l < hello.txt
    postcondition: test -f hello.txt
    next: done
terminal: {done: {status: ok, message: "Wrote hello.txt for ${name}."}}
```

One thing to notice: `${name}` is not a shell variable. It is a workflow value, and the
validator will complain that nothing declares it. Add an argument at the top, under `workflow:`:

```yaml
args:
  name: {type: string, required: true}
```

## 2. Validate it

```
› pawl validate hello
.claude/workflows/hello.yaml: ok — 2 steps, 1 terminal, 0 cycles.
soft postconditions: 0 of 2 (0%).
```

Validation is static: it checks the graph, state keys, caps, and that every referenced file exists.
It runs nothing — the fastest feedback in the tool, so get in the habit.

Break it on purpose to see the shape of an error — change `next: count` to `next: cuont`:

```
› pawl validate hello
.claude/workflows/hello.yaml:8: step `greet`: next: `cuont` is not a step or terminal.
  Known steps: greet, count. Known terminals: done.
exit 2
```

## 3. Run it

Open Claude Code here and type `/pawl run hello name=ada` — the model runs `pawl run` for you. `/pawl` is a
skill the model interprets, so the arguments don't have to be exact: `/pawl run hello, name is ada`
works too — the model translates it to `pawl run hello name=ada`. Exact `key=value` is what the
*engine* needs; translating is the model's job.

```
› /pawl run hello name=ada
  run 4b17  hello  .claude/workflows/hello.yaml
  hooks: PreToolUse ✔  Stop ✔   guards: 0 advisory (pattern-matched)  invariants: 0
  ✔ greet → count
  ✔ count → done
  TERMINAL 4b17 ok
  Wrote hello.txt for ada.
  0 of 2 advanced on a soft postcondition.
```

That is the whole run. Both steps are `deterministic`, so `pawl run` executed them itself and handed
nothing back — no subagent, no question, no waiting.

What each line means:

- **`run 4b17 …`** — the run id, the workflow, and which file it resolved to. Repo before home.
- **`hooks: …`** — the enforcement self-test. Two ✔ or `pawl` refuses to start.
- **`✔ greet → count`** — step done, postcondition passed, transition taken.
- **`TERMINAL 4b17 ok`** — the run is over. `ok` or `blocked`, nothing else.
- **the last line** — the soft census; `soft: true` postconditions are counted every run so a
  workflow can't quietly stop checking itself.

## 4. Watch one fail

Change `greet`'s postcondition to `test -s nothing.txt` and type `/pawl run hello name=ada` again:

```
› /pawl run hello name=ada
  run 6d02  hello  .claude/workflows/hello.yaml
  hooks: PreToolUse ✔  Stop ✔   guards: 0 advisory (pattern-matched)  invariants: 0
  ✗ greet  postcondition failed: test -s nothing.txt (exit 1)
  TERMINAL 6d02 blocked
  Paused for review: greet: postcondition failed after 1 attempt.
```

The step's body ran; the postcondition failed, there's no `attempts:` budget or `catch:` route, so
the default applies: `failure` goes to `blocked`. The engine never decides a step was probably fine.

## 5. Where to go next

- Both of `hello`'s steps are `deterministic`; next, see the four step types.
  → [step-types.md](step-types.md)
- Add a third answer to a step: print a token from your script and route it.
  → [steps/deterministic.md](steps/deterministic.md)
- Hand a step to a subagent. → [steps/agentic.md](steps/agentic.md)
- Ask yourself a question mid-run. → [steps/human.md](steps/human.md)
- Understand what the engine guarantees and what it does not.
  → [concepts.md](concepts.md)

Run `pawl status` any time to see where a run is, `pawl abandon --run 6d02` to end one.
