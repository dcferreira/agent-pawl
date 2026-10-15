# `pawl validate`

```
pawl validate <workflow-name> | --path <file>
```

Static only: no commands run, no network, no model. It parses the file, walks the graph, and checks
the rules below. Exit 0 clean, 2 on error. Run before every run, and in CI.

Takes a workflow name (resolved under `.claude/workflows/` exactly as `pawl run` resolves one) or,
with `--path <file>`, a specific file instead — mutually exclusive with a name; scripts and context
files it references still resolve relative to the workflow file itself either way.

Output for a clean file: the resolved file, a `guards:` line if the workflow declares guards (a
validate has no run, so it only says they are declared), and the soft census (below). Nothing else
means the workflow is valid.

<!-- pawl-capture: validate/manage-mr -->
```
$ pawl validate manage-mr
workflow: /home/you/project/.claude/workflows/manage-mr.yaml (repo-local)
guards: 4 declared (enforced only when a run starts with the pawl hooks installed)
soft: 6/23 steps (26.1%): address_comments, address_comments_push, changelog, collect_reviewers, fix_issues, fix_issues_push
```

Every error is one line on stderr naming the file, the step and what is wrong; most also name the rule number and the fix, e.g. `hello.yaml: step "greet": rule 1: next: "cuont" names a step or terminal that does not exist; declare step "cuont", add it under terminal:, or fix the typo` (see [quickstart.md](quickstart.md#2-validate-it)). The summary lines are printed even when there are errors.

## The checks

**1 — dangling edge.** `next:`/`outcomes:` target not a step or terminal.

**2 — unreachable step.** Not reachable from `start:`. Route to it, or delete it.

**3 — dead end.** No outgoing edge and not a terminal. Add `next:` or `outcomes:`.

**3b — incomplete `outcomes:`** (no fall-through, ever). Every outcome the step can produce must be
routed, except `failure` (via `catch:`, default `failure → blocked`) and `exhausted` (unrouted →
`blocked`) — those already have an engine-wide default.

**4 — unknown or misplaced `${key}`.** Not declared in `state:`/`args:` and not a pseudo-key
(`run_id`, `step`, `attempt`, `visits`, `last_error`, `blocked_reason`, or `item`/`item_index` in a
`foreach:` body); or used where substitution
doesn't apply (`next:`, `id:`, `kind:`, etc.).

**5 — writing an argument.** `writes:` names an `args:` key. `args:` are read-only.

**6 — missing postcondition.** Required only on `agentic`. Optional on `deterministic` (exit 0 is
success unless you declare one — add one to check the command's effect, e.g. a push actually
landed) and `wait`; `human` and `parallel` do not accept one (rule 30). If an `agentic` step's real check is out of reach, write the weakest
real check and mark it `soft: true` — `postcondition: "true"` + `soft: true` is the floor.

**7 — missing or unrouted timeout.** `wait` and `human` steps need `timeout:`; its `timeout` outcome
must be routed (the duration itself is checked by rule 36).

**8 — `human` step shape.** One message per fault: needs exactly one of `options:`/`options_from:`;
every option must be routed or have a fall-through; a `chosen:`
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
means denied everywhere). Implemented: `guards:` is now parsed and validated — `id:` required,
unique, and must use step-id syntax (letters, digits, `_` and `-`, starting with a letter or digit);
`match:` required, must compile as a regexp (Go's `regexp` package, RE2 syntax, matched unanchored
against the whole command string), and must not be able to match zero characters (an unanchored
pattern that can, e.g. `a*`, `foo|`, or a bare `\b`, would match — and deny — every command); and
`only_in:` is itself a required key (missing it is an error; write `only_in: []` for "denied
everywhere"). This rule then checks every `only_in:` entry names a declared step. Enforcement is
advisory, and only while the hooks are actually installed and firing: with a fresh `PreToolUse`
heartbeat, `pawl run`'s start banner adds `guards: N advisory (pattern-matched)`; without one
(enforcement off, `--no-enforcement`/`PAWL_ENFORCEMENT=off`), it adds `guards: N declared, NOT
enforced (enforcement off)` instead — whenever a workflow declares any. `pawl validate`, which has
no run to be enforced or not, adds `guards: N declared (enforced only when a run starts with the
pawl hooks installed)`. Full field-by-field rules: `design/format-spec.md` §H rule 15.

**16 — missing or non-executable file.** A literal-path command reference (`run:`, `poll:`,
postcondition `command:`, invariant `check:`, `!cmd` `context:`; first token contains `/` and no
`${`) doesn't exist or isn't `chmod +x`, or a plain `context:` file doesn't exist or isn't readable.
Paths resolve relative to the workflow file's directory. Checked by `pawl validate` and when
`pawl run` starts a new run — never on resume, `pawl status` or hooks, so a script deleted after its
step already ran can't make a live run unresumable. Bare commands (`go test ./...`) and paths
containing `${key}` are skipped. A file that an earlier step creates during the run doesn't exist yet
when the check runs, so reference it through `${key}` (or read it with `!cmd`) instead of a literal path.

Errors use the same one-line shape as the other rules, and keep the resolved path:
`<file>: step "<id>": rule 16: <field>: "<path>" (resolved to <absolute path>) <what is wrong>; <how to fix>`
(an invariant is named `invariant "<id>": rule 16: check: …`). For example:

```
/home/you/project/.claude/workflows/w.yaml: step "fix": rule 16: postcondition.command: "scripts/nope.sh" (resolved to /home/you/project/.claude/workflows/scripts/nope.sh) does not exist; create it there or fix the path (paths resolve relative to the workflow file's directory)
```

A bare-string `postcondition:` (`postcondition: scripts/check.sh`) is the same thing as
`postcondition: {command: scripts/check.sh}`, so it is reported as `postcondition.command`.

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
- A quoted string with `!cmd` *inside* the quotes (e.g. `"!cmd git diff main"`) is the same FILE
  PATH mistake as the first case, just with a value that happens to start with `!cmd `. The tag has
  to sit outside the quotes (`!cmd "git diff main"`) to take effect — the error names the corrected
  form with `!cmd` stripped from the front of the value, not just the leading `!` (which would
  otherwise suggest running a program literally called `cmd`).

`pawl validate` rejects both and names the corrected `!cmd "..."` form. A real file whose name
starts with `!` is not a case this rule can special-case away (there is no way to tell "this author
really means a file" from "this author typo'd a command" from the YAML alone) — reference it as
`./!name` instead, which does not start with `!` and so is unambiguously a file path.

**20 — bad terminal status.** A `terminal:` entry's `status:` must be exactly `ok` or `blocked`.
Anything else — a typo like `done`, or an omitted `status:` — is rejected.

**21 — key name is not an identifier.** Every key in `state:`, `args:` and a step's `writes:` must
match `^[A-Za-z_][A-Za-z0-9_]*$` (letters, digits and `_`, not starting with a digit), because keys
are exported to steps as `PAWL_<KEY>` environment variables. A `writes:` key is also checked
against `state:` by rule 11; rule 21 reports it again against the step that wrote it.

**30 — field not accepted by the step's kind.** Common to every kind: `id kind next outcomes catch
max_visits attempt_key`. Beyond that, `deterministic`: `run emits writes postcondition soft attempts
retry`; `agentic`: `description context subagent_args writes postcondition soft attempts`; `wait`: `poll every timeout emits writes postcondition soft retry`; `human`: `question
options options_from multi timeout writes`; `parallel`: `branches foreach`. The error lists the
offending fields and which kinds take each, and says to remove them. (This replaces the old
`multi:`-off-`human`, `foreach:`-off-`parallel` and `retry:`-kind checks.)

**31 — `soft:` without `postcondition:`.** The flag would be silently dropped; add a postcondition
or remove it.

**32 — missing or unknown `type:`.** Every `state:`/`args:` entry and typed `writes:` entry needs
`type:` of `string`, `integer`, `number`, `boolean` or `json`; a near-miss gets a "did you mean".

**33 — `default:` does not match `type:`.** E.g. `default: 5` on a `string` (quote it), a bool or float
on an `integer`, `default: null` on anything but `json`.

**34 — malformed `catch:` entry.** Only `on:` and `next:`; `on:` is required (a missing `next:` is
rule 1).

**35 — `catch[].on:` the kind cannot produce.** Allowed: `failure`, `exhausted`, plus `timeout` on
`wait`/`human` and `partial` on a `parallel` `foreach:`. Author-named outcomes belong under
`outcomes:`. Also rejects a duplicate `on:` (the later entry can never fire) and an `on:` already
routed in `outcomes:` (which takes precedence).

**36 — bad duration.** `timeout:` (`wait`, `human`) and an authored `every:` (`wait`) must be Go
durations (`5m`, `1h`, `24h`) greater than zero.

**37 — reserved step id.** `done`/`blocked`, or an id equal to a declared `terminal:` key. A declared
`terminal: {done: …}`/`{blocked: …}` is allowed and overrides the implicit terminal.

**38 — reserved key name.** In `state:`/`args:`: an engine pseudo-key (case-insensitive), a key
starting with `_`, or one exporting as `PAWL_ENFORCEMENT`/`PAWL_STATE_DIR`/`PAWL_WRAPPER_ACTIVE`. Environment variables
`pawl` reads in future are named `PAWL__<NAME>`, which a `_`-free key can never collide with.

**39 — key collision.** The same key in `args:` and `state:`, or two keys equal case-insensitively.

## Warnings

`pawl validate` prints no warnings in this build: every finding is an error (exit 2). Two checks were
planned as `warning:` lines — a state key that is written but never read, and a `${key}` read on a path
where nothing writes it first — but neither exists yet (`internal/spec/validate.go` defers them). `--strict`
is likewise not implemented: `pawl validate <name> --strict` is a plain usage error (exit 2,
"unrecognised argument").

## The soft census

Printed on every validate, error or not, as the last summary line: `soft: <count>/<total> steps
(<percent>%): <step ids, sorted>`, or `(none)` when no step is `soft: true`. `pawl run` prints the same line in
its banner (see the captured output above, and [quickstart.md](quickstart.md)).

This is the workflow's trust surface — the same line appears in the banner of every run. No threshold,
no failure; if the percentage climbs, ask which checks could re-observe reality instead.

Related: [troubleshooting.md](troubleshooting.md).
