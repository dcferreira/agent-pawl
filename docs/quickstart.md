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
    run: echo hello from ${name} > hello.txt
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
validator will complain that nothing declares it:

<!-- pawl-capture: quickstart/validate-undeclared -->
```
$ pawl validate hello
workflow: /home/you/project/.claude/workflows/hello.yaml (repo-local)
soft: 0/2 steps (0.0%): (none)
/home/you/project/.claude/workflows/hello.yaml: step "greet": rule 4: run uses ${name}, which is not declared in state:/args: and is not an engine pseudo-key; declare "name" under state: or args:, or fix the typo
/home/you/project/.claude/workflows/hello.yaml: terminal "done": rule 4: message uses ${name}, which is not declared in state:/args: and is not an engine pseudo-key; declare "name" under state: or args:, or fix the typo
exit 2
```

Add an argument at the top, under `workflow:`:

```yaml
args:
  name: {type: string, required: true}
```

## 2. Validate it

<!-- pawl-capture: quickstart/validate -->
```
$ pawl validate hello
workflow: /home/you/project/.claude/workflows/hello.yaml (repo-local)
soft: 0/2 steps (0.0%): (none)
```

The first line says which file `pawl` resolved the name to (repo-local before home). The second is
the soft census: how many steps advance on a `soft: true` postcondition (none here). Nothing else
means the workflow is valid; the exit status is 0. Validation is static: it checks the graph, state keys and caps. It does not check that a referenced
script or context file exists — that's not caught until `pawl run` actually tries to gather it, and a
missing one then ends the run `BLOCKED`, not `pawl validate` refusing to start. It runs nothing itself
— the fastest feedback in the tool, so get in the habit.

Break it on purpose to see the shape of an error — change `next: count` to `next: cuont`. Each error is
one line: the file, the step, the rule number, what is wrong and how to fix it. The exit status is 2:

<!-- pawl-capture: quickstart/validate-typo -->
```
$ pawl validate hello
workflow: /home/you/project/.claude/workflows/hello.yaml (repo-local)
soft: 0/2 steps (0.0%): (none)
/home/you/project/.claude/workflows/hello.yaml: step "greet": rule 1: next: "cuont" names a step or terminal that does not exist; declare step "cuont", add it under terminal:, or fix the typo
/home/you/project/.claude/workflows/hello.yaml: step "count": rule 2: is unreachable from start: "greet"; add an edge to it or remove it
exit 2
```

## 3. Run it

Open Claude Code here and type `/pawl run hello name=ada`. That is the whole interface: the model
runs `pawl run hello name=ada` for you, reads what `pawl` prints, and relays it. `/pawl` is a skill the
model interprets, so the arguments don't have to be exact: `/pawl run hello, name is ada` works too —
the model translates it to `pawl run hello name=ada`. Exact `key=value` is what the *engine* needs;
translating is the model's job.

The block below is what `pawl` prints to the session (the plugin's `PreToolUse` hook has already
recorded its heartbeat, so the run starts enforced):

<!-- pawl-capture: quickstart/run -->
```
$ pawl run hello name=ada
workflow: /home/you/project/.claude/workflows/hello.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
soft: 0/2 steps (0.0%): (none)
TERMINAL 7f3a ok
message:
  Wrote hello.txt for ada.
END TERMINAL 7f3a ok
```

That is the whole run. Both steps are `deterministic`, so `pawl run` executed them itself and printed
nothing per step — no subagent, no question, no waiting. Only the banner and the end of the run are
printed.

What each line means:

- **`workflow: …`** — the file the name resolved to, and where it was found (repo before home).
- **`hooks: …`** — the enforcement banner: `PreToolUse ✔ (heartbeat)` means `pawl run` found a fresh
  heartbeat and started enforced (`Stop` is assumed installed too, from the same `hooks.json`);
  without one, `pawl run` refuses to start rather than print this line at all (see
  [install.md#hooks](install.md#hooks)). With `--no-enforcement` this line is `enforcement: off (…)`.
- **`soft: 0/2 steps (0.0%): (none)`** — the soft census: how many steps advance on a `soft: true`
  postcondition, and which. They are counted on every run so a workflow can't quietly stop checking
  itself.
- **`TERMINAL 7f3a ok` … `END TERMINAL 7f3a ok`** — the run is over, and `7f3a` is its run id. The
  status is `ok` or `blocked`; `ok` ends the run, while `blocked` pauses it and prints `resume:` and
  `abandon:` commands.
- **`message:`** — the terminal's `message:`, with `${name}` filled in.

## 4. Watch one fail

Change `greet`'s postcondition to `test -s nothing.txt` and type `/pawl run hello name=ada` again:

<!-- pawl-capture: quickstart/blocked -->
```
$ pawl run hello name=ada
workflow: /home/you/project/.claude/workflows/hello.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
soft: 0/2 steps (0.0%): (none)
TERMINAL 7f3a blocked
message:
  greet: failure
resume:
  pawl run hello --run 7f3a
abandon:
  pawl abandon --run 7f3a --reason '<reason>'
END TERMINAL 7f3a blocked
exit 3
```

The step's body ran; the postcondition failed, there's no `attempts:` budget or `catch:` route, so
the default applies: `failure` goes to `blocked`. The engine never decides a step was probably fine.
The `message:` says which step failed and how (`greet: failure`); the exit status is 3. A blocked run
is paused, not over: it stays live until you fix the cause and run the `resume:` command, which re-runs
`greet` from attempt 1, or until you close it with the `abandon:` command.

## 5. Where to go next

- Both of `hello`'s steps are `deterministic`; next, see the four step types.
  → [step-types.md](step-types.md)
- Add a third answer to a step: print a token from your script and route it.
  → [steps/deterministic.md](steps/deterministic.md)
- Hand a step to a subagent. → [steps/agentic.md](steps/agentic.md)
- Ask yourself a question mid-run. → [steps/human.md](steps/human.md)
- Understand what the engine guarantees and what it does not.
  → [concepts.md](concepts.md)

Run `pawl status` any time to see where a run is, `pawl abandon --run <id>` to end one.
