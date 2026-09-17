# FAQ

**My script prints a lot. Does that break anything?**
No — only the **last non-empty line** of stdout is parsed; everything else is journalled. Put the
token and payload last, after any `set -x` trace. See [steps/deterministic.md](steps/deterministic.md).

**Can the agent skip a step?**
No. `wf submit` refuses any `(run, step, attempt)` other than the one the journal is waiting on, and
the postcondition runs in the engine's process afterwards. See
[guards-and-invariants.md](guards-and-invariants.md).

**What happens to my files when a step retries?**
Nothing — attempt N+1 runs on the tree exactly as attempt N left it, with the postcondition's failure
text carried forward. There is no snapshot or restore; fix-forward is the only semantics, which is
why idempotent scripts matter. See [steps/deterministic.md](steps/deterministic.md#common-mistakes).

**How do I test a workflow without an LLM?**
`wf validate` covers the whole graph statically, for free. Then write a version with `agentic` steps
replaced by `deterministic` ones that `echo` a fixed JSON payload, to exercise every transition with
no model and no tokens. See [validation.md](validation.md).

**Does waiting cost tokens?**
No — `wf poll` is a background process running your shell command; nothing is generated while CI
runs. See [steps/wait.md](steps/wait.md).

**What if I answer a `human` step with something that isn't an option?**
`AskUserQuestion` always offers a free-text "Other", with no way to turn it off. Against a static
single-select it produces the reserved outcome `chosen`; route it, or accept it goes to `blocked`
(paused, not ended). See [steps/human.md](steps/human.md).

**Why can't an agentic step choose the next step?**
Because then the actor that did the work would also grade it. Put a three-line `deterministic`
router after it that reads the agent's typed `writes:` and prints a token. See
[steps/agentic.md](steps/agentic.md#outcomes).

**Where does run state live, and can I read or write it?**
`~/.local/state/wf/<root-slug>/<workflow>/<run-id>/` — `events.jsonl` is the append-only truth,
`grep`/`jq`-able; read it to debug, never write it. There is no `wf set`: state enters only through
the stdout contract or a schema-validated agentic return.

**Do I have to rewrite my existing scripts?**
No. `run:` takes the command you have — JSON output gets you `writes:` for free, `k=v` output needs
`emits: pairs`, and inputs can come from `$WF_BRANCH`-style environment variables instead of
arguments. See [writing-workflows.md](writing-workflows.md#state-and-key).

**Can I use `wf` with a harness other than Claude Code?**
The engine is harness-agnostic: an agentic step's `subagent_args:` map is passed through to
`DISPATCH` verbatim, and the engine never interprets or enforces it. In Claude Code these are
typically `model`, `tools`, `effort` — read by the Claude Code plugin as suggestions for the
subagent launch, not enforced by it. A different harness would read `subagent_args:` on its own
terms.

**Why is my guard not firing?**
Most likely the command was spelled differently than your pattern, or wasn't a Bash call at all — the
Edit tool matches no `match:`. The [invariant on the same rule](guards-and-invariants.md) is what
catches the consequence.
