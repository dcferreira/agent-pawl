# Concepts

Eight words. Learn these and the YAML reads itself.

**Workflow** — one YAML file: arguments, state, first step, steps, terminals. Validated and run.

**Step** — a named state that does one thing: running, satisfied, or failed.

**Kind** — the executor: [`deterministic`](steps/deterministic.md), [`agentic`](steps/agentic.md),
[`wait`](steps/wait.md), [`human`](steps/human.md).

**Outcome** — a named result that picks the next step. `deterministic`/`wait` steps print their own
token; `human` steps use the chosen option; `agentic` steps only ever produce `success`/`failure`.
Reserved everywhere: `success`, `failure`, `timeout`, `exhausted`, `chosen`.

**State key** — a value the run carries, typed `string`/`integer`/`number`/`boolean`/`json`. Steps
write keys; others read them as `${key}`. `args:` are state keys passed on the command line that no
step may write.

**Postcondition** — a check the *engine* runs after the step's body, before the transition. If it
fails, the step did not happen.

**Guard** — a pattern over a shell command, allowed only while certain steps are active. Advisory:
matches spelling, not intent.

**Invariant** — a command re-run after every step; non-zero sends the run to `BLOCKED`.

## What a run looks like

One step of each kind, chained: `preflight` (deterministic) prints `FRESH` → `wait_for_mr` (wait)
prints `WAIT`, polls in the background, resolves `COMMENTS` → `fix_issues` (agentic) prints
`DISPATCH`, a subagent runs, returns `success` → `choose_rev` (human) prints `ASK`, you pick `alice`
→ `done` (terminal, `status: ok`). After every step, invariants are re-checked; any non-zero routes
to `BLOCKED` — paused, not terminal, see [running.md#blocked](running.md#blocked).

`wf run` executes consecutive deterministic steps and stops only where the session must act, printing
`DISPATCH`/`ASK`/`WAIT` and exiting. The session acts, calls `wf submit`, `wf` prints the next line.
See [running.md](running.md).

## Deterministic vs agentic

`deterministic`: same inputs give the same answer, a script can say so — push a branch, run tests,
read an API. The engine runs it directly; no model involved.

`agentic`: needs judgement over unstructured input — write the MR description, fix failing tests.
`description:` (inline, not a file) states intent, constraints, done; the session composes the
subagent prompt from it plus gathered `context:`. Runs as a subagent honouring `subagent_args:` — a
free-form map (in Claude Code typically `model`, `tools`, `effort`) passed through to the harness
verbatim and not enforced by the engine or a hook — returning the typed object in `writes:`.

An agentic step cannot choose its own branch — only `success`/`failure`. When the branch depends on
what it produced, add a `deterministic` router that reads `writes:` and prints a token;
`wf validate` rejects author-named outcomes on an agentic step.

You don't have to pick the final kind up front: a workflow works with most steps `agentic` from the
start, then you switch steps to `deterministic` one at a time as you want less cost and more
robustness, without the postcondition changing.

## Postconditions

`wf` evaluates the postcondition in its own process, after the body, before the transition — the
step's output is an input, never the verdict. `{all_set: […]}` and `{equals: {…}}` run in-process;
`command:` spawns a subprocess. Best postconditions re-observe reality, e.g.
`glab mr view "${mr_iid}" --output json | jq -e '.state=="opened"'` rather than trust a flag the
step set itself.

Where you cannot check the real thing, write the weakest real check and mark it `soft: true`: it
still gates the transition, but is counted separately in `wf validate`'s census and every run's
`N of M advanced on a soft postcondition` line.

Next: [running.md](running.md), then [writing-workflows.md](writing-workflows.md).
