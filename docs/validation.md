# `pawl validate`

```
pawl validate <name|path> [--strict]
```

Static only: no commands run, no network, no model. It parses the file, walks the graph, and checks
the rules below. Exit 0 clean, 2 on error. Run before every run, and in CI with `--strict`.

```
› pawl validate manage-mr
.claude/workflows/manage-mr.yaml: ok — 23 steps, 3 terminals, 2 cycles.
soft postconditions: 6 of 23 (26%):
  entry, changelog, fix_issues, fix_issues_push, trigger_coderabbit, route_reviewers
```

Every error names the file and line, the step, the rule and the fix.

## The checks

**1 — dangling edge.** `next:`/`outcomes:` target not a step or terminal.

**2 — unreachable step.** Not reachable from `start:`. Route to it, or delete it.

**3 — dead end.** No outgoing edge and not a terminal. Add `next:` or `outcomes:`.

**3b — incomplete `outcomes:`** (no fall-through, ever). Every outcome the step can produce must be
routed, except `failure` (via `catch:`, default `failure → blocked`) and `exhausted` (unrouted →
`blocked`) — those already have an engine-wide default.

**4 — unknown or misplaced `${key}`.** Not declared in `state:`/`args:` and not a pseudo-key
(`run_id`, `step`, `attempt`, `visits`, `last_error`, `blocked_reason`); or used where substitution
doesn't apply (`next:`, `id:`, `kind:`, etc.).

**5 — writing an argument.** `writes:` names an `args:` key. `args:` are read-only.

**6 — missing postcondition.** Required only on `agentic`. Optional on `deterministic` (exit 0 is
success unless you declare one — add one to check the command's effect, e.g. a push actually
landed), `wait` and `human`. If an `agentic` step's real check is out of reach, write the weakest
real check and mark it `soft: true` — `postcondition: "true"` + `soft: true` is the floor.

**7 — missing or unrouted timeout.** `wait` steps need `timeout:`; its `timeout` outcome must be
routed.

**8 — `human` step shape.** One message per fault: needs exactly one of `options:`/`options_from:`;
every option must be routed or have a fall-through; `multi:` only valid on `human`; a `chosen:`
route needs exactly one `writes:` key; `options_from:` requires `writes:`.

**9 — named outcomes on an agentic step.** Produces only `success`/`failure`. Route from a following
`deterministic` step that reads its `writes:` and prints a token.

**9b — bad `subagent_args:` shape.** `subagent_args:` must be a map.

**10 — bad `emits:`.** Must be `json` or `pairs`.

**11 — `writes:` conflicts with `state:`.** Wrong declared type, or writing an undeclared key.

**12 — caps that cannot bind.** `max_visits:` must be a positive integer; if every step's
`max_visits:` in a cycle exceeds `max_steps:`, no per-step cap can ever bind.

**13 — `attempts:` below 1.**

**14 — bad postcondition map.** Use `command`, `all_set` or `equals`, e.g.
`postcondition: {all_set: [branch, title]}`.

**15 — guard names a missing step.** `only_in:` must name a real step (`only_in: []` — empty —
means denied everywhere).

**16 — missing or non-executable file.** A referenced script doesn't exist (relative to
`.claude/workflows/`), or isn't `chmod +x`.

**17 — bad `kind: parallel` branch.** `branches:` needs ≥ 2 entries, each a declared
`deterministic`/`agentic` step (no nesting), listed once, claimed by only one `parallel` step, not
the workflow's `start:` step, and declaring none of `next:`/`outcomes:`/`catch:`/`attempts:`/
`attempt_key:`/`max_visits:` itself — see [steps/parallel.md](steps/parallel.md).

**18 — a `context:` entry starts with `!` but does not carry the YAML tag `!cmd`.** A `context:`
entry is a command only when it carries the YAML tag `!cmd` (e.g. `!cmd "git diff"`). Every plain
entry starting with `!` is rejected, and so is any custom YAML tag other than `!cmd`, in either
shape an author might write it:

- A *quoted* string that merely starts with `!` (e.g. `"!git diff main...HEAD"`) is read as a FILE
  PATH literally named `!git diff main...HEAD`, not executed.
- An *unquoted* entry starting with `!` (e.g. `!git diff main`) isn't even that literal text — YAML
  reads it as a custom tag `!git` applied to the scalar `diff main`. Any custom tag other than
  `!cmd` is rejected the same way.

`pawl validate` rejects both and names the corrected `!cmd "..."` form. A real file whose name
starts with `!` is not a case this rule can special-case away (there is no way to tell "this author
really means a file" from "this author typo'd a command" from the YAML alone) — reference it as
`./!name` instead, which does not start with `!` and so is unambiguously a file path.

## Warnings

Two, printed as `warning:` and exit 0 — unless `--strict`, which makes them errors.

```
warning: `blocked_reason` is written by `resolve_conflict` and never read.
warning: `${round}` is read by `fix_issues_push` on a path where nothing writes it first
  (preflight → changelog → fix_issues_push). Give it a default: in state:.
```

Both are usually real bugs, with exceptions — a key read only in a terminal message, or whose writer
is on a branch the validator can't prove taken.

## The soft census

Printed on every validate, error or not:

```
soft postconditions: 6 of 23 (26%):
  entry, changelog, fix_issues, fix_issues_push, trigger_coderabbit, route_reviewers
```

This is the workflow's trust surface — the same number appears at the end of every run. No threshold,
no failure; if the percentage climbs, ask which checks could re-observe reality instead.

Related: [troubleshooting.md](troubleshooting.md).
