# Authoring format spec

Normative definition of *what the author writes*; `DESIGN.md` defines *what the engine does*, and
`docs/` teaches. A workflow is **one YAML file, pure data**. Step implementations (shell scripts) are
referenced by path; an agentic step's prompt is not one of them (§B.6). Sequencing and branching live
only in `next:` / `outcomes:` / `catch:`. New to `pawl`? See `docs/README.md`.

---

## A. Concept model — eight nouns

| Noun | Description |
|---|---|
| **Workflow** | One YAML file naming its `args:`, its state, a start step, its steps and its terminals. The unit you author, validate and run. |
| **Step** | A named state that does one thing and is either running, satisfied, or failed. |
| **Kind** | Which of **five** executors runs the step: `deterministic`, `agentic`, `wait`, `human`, `parallel` (§C). |
| **Outcome** | A named result of a step that selects the next step. Which kinds produce which outcomes: §C. |
| **State key** | A value the run carries, declared up front, read and written by steps: `string`/`integer`/`number`/`boolean`/`json`. `args:` are read-only state keys; a `default:` counts as a write. |
| **Postcondition** | A command or predicate the **engine** evaluates before it will leave the step; `soft:` labels one judgement-bounded (§B.7). |
| **Guard** | A command pattern that may only run while its designated steps are active — or, with `only_in: []`, nowhere. Pre-hoc, pattern-matched, advisory. |
| **Invariant** | A condition that must hold for the whole run, re-checked after every step; breaking it sends the run to `BLOCKED`. |

Not nouns: hook, state file, lock, session id, poller, attempt counter, run id — the engine owns
them. There is no working-tree noun (the engine never touches files; only steps do — §B.8) and no
read-set noun: the engine derives what a step reads from the `${key}` occurrences in its `run:`,
`description:`, `context:`, `postcondition:` and `question:`.

---

## B. The rules

### 1. One stdout grammar, for `deterministic` and `wait`

The two kinds that run a shell command. The engine reads the **last non-empty line** of stdout;
everything else is logged, never parsed.

```
<line> ::= [TOKEN] [payload]
```

`TOKEN` is present if and only if the step declares author-named `outcomes:` (names other than the
reserved `success` / `failure` / `timeout` / `exhausted`). The `payload` is parsed per `emits:`:

| `emits:` | payload shape |
|---|---|
| `json` (default) | a JSON object whose keys are a subset of `writes:` |
| `pairs` | whitespace-separated `k=v` tokens |

`emits:` is the **maximum** payload shape, not a per-emission obligation: a TOKEN-only line is valid
under both modes, writes nothing, and leaves the declared keys at their previous values or their
`default:`. A step that writes nothing declares no `writes:`.

Values are coerced to the declared `state:` type and rejected loudly if they do not fit. The engine
escapes C0 control characters at this boundary, once, for all workflows.

**Non-zero exit is always `failure`**, whatever was printed; token parsing happens only on exit 0.
Distinct non-zero exit codes do not select distinct outcomes — one signalling mechanism, not two.

### 2. `${key}` substitution — exactly where, and how

**Works in:** `run:`, `poll:`, `check:`, `postcondition:` (the `command:` string and the values of
`equals:`), `description:` (rendered before it is printed in the `DISPATCH` block), every `context:`
entry — including inside a `!cmd` string, resolved **before** the command runs — `question:`,
`attempt_key:` templates, and terminal `message:`.

**Does not work in:** `id:`, `kind:`, `next:`, `outcomes:` keys or targets, `catch[].next`, guard
`match:`, or any path the validator must resolve statically — the graph and the file set must be
knowable without running anything.

**Rendering.** In *shell* contexts (`run:`, `poll:`, `check:`, `postcondition.command`) the value is
substituted as **one shell-quoted token**. In *prose* contexts (`description:`, `question:`,
`message:`) the raw value is substituted, JSON pretty-printed. `$${` renders a literal `${`.

**Never additionally double-quote a `${key}` in a shell context.** The substitution already is a
single shell-quoted token (e.g. `'value'`), so `${key}` should stand alone, or be concatenated only
with literal text outside any quotes (`origin/${branch}` is fine). Writing `"${key}"` embeds the
rendered value's own quoting *inside* a second, literal pair of double quotes; the shell then
re-interprets the result, and command substitution (`$(...)`/backticks) inside the value stays live
even though the value was single-quoted — turning a `${key}` sourced from a `state:` key (e.g. one an
agentic step wrote) into a command-injection vector, and, even when the value is inert, silently
breaking equality checks like `[ "${key}" = true ]` (the comparison sees the literal apostrophes,
never `true`). See `docs/dogfood.md`'s "traps" section for a worked example.

**Engine-provided pseudo-keys**, readable everywhere, never declared and never written by a step:
`run_id`, `step`, `attempt`, `visits`, `last_error`, `blocked_reason`.

**`blocked_reason`** is set by the engine, and only the engine, when a run enters `BLOCKED`: the
violated invariant's `message:`, or a fixed engine string naming the step and outcome (e.g.
`"wait_for_ci: timeout"`, `"fix_tests: exhausted"`). It is always set by the time a `blocked`
terminal's `message:` is rendered.

Every key the engine can see a step reading is also exported to that step as `PAWL_<KEY>`
(upper-cased).

### 3. Deterministic steps get named outcomes

A `deterministic` step with author-named `outcomes:` takes its outcome from the stdout TOKEN of §B.1 —
the only way a step selects among more than success and failure.

### 4. Attempts, visits, and the two backstops

**`attempts:`** re-runs the *same step* when its postcondition fails, with the failure text appended
to the next attempt's `DISPATCH` block (agentic) or environment (deterministic). Keyed
`(run_id, step_id, attempt_key)`, it is:

- persisted for the life of the run, and crash-safe;
- **not** scoped to the incoming edge: re-entering a step by any edge continues the same countdown,
  provided `attempt_key` resolves the same;
- **not advanced by a crash** — the re-run after an interruption is *the same attempt number*;
- cleared when the postcondition passes and the step leaves by a non-`catch` edge, and reset
  wholesale only by `--fresh`.

**`attempt_key:` is automatic**: a hash of the postcondition's failure text, whitespace normalised, so
the same failure continues a countdown and a different one starts a fresh budget. Where the failure
text is noisy, `attempt_key: "${some_key}"` overrides it with a rendered template.

**`max_visits:`** caps how many times a step may be *entered* in one run, counting every entry from
any edge. Default **10**. It is the cap on a cycle, since every cycle re-enters through at least one
step; two entry points to one cycle means two independent caps.

**`max_steps:`** is a workflow-level backstop on total step entries in a run. Default **200**.

Exceeding either cap produces the reserved outcome **`exhausted`** on the step that would have been
entered. Route it in `outcomes:` or `catch:`; with no route it goes to `blocked`.

### 5. `human` steps map 1:1 onto `AskUserQuestion`

A `human` step is what Claude Code's `AskUserQuestion` tool offers: a list of options, optionally
multi-select, and an always-present free-text "Other" (no field turns "Other" off). All of it is
Milestone 1.

- Exactly one of `options:` (a static inline list) or `options_from:` (a state key holding a list of
  strings, resolved at ask time) is required. `multi:` defaults to `false`.
- **Static `options:` with `multi: false`** — the chosen option is the outcome token, routed via
  `outcomes:` exactly as for `wait`. A free-text "Other" answer instead yields the reserved token
  `chosen` and the typed text goes to `writes:`; `writes:` is therefore required whenever a `chosen:`
  route exists.
- **`options_from:` or `multi: true`** — the outcome is *always* `chosen`. The answer (a single value,
  a list, or free text, including free text mixed into a multi-select list) goes to the one `writes:`
  key, typed `string` for a single free-text/static answer or `json` when the value can be a list.
  Branch on the written value from a following `deterministic` router step (§B.6).
- `timeout:` is **required**, and `timeout` is a reserved outcome. On `timeout` nothing is written.
- **The answer, on the wire.** `pawl submit` (the same command as for `agentic`, no new subcommand)
  accepts a JSON object `{"selected": ["Option Label", …], "other": "free text"}`: `selected` is the
  picked static/dynamic option label(s), verbatim; `other` is present whenever a free-text "Other"
  answer was given, and may sit alongside a non-empty `selected` on a `multi: true` step mixing a
  listed pick with free text. On a plain static single-select with no `chosen:` route (e.g. the
  `choose_reviewer` example in §E: `writes: [reviewer]`, no `chosen:` at all), a normal option pick
  still writes that option's label into `writes:` if one is declared — `writes:` is populated on every
  non-`timeout` path a declared key exists for, not only via the free-text/`chosen:` route.
  `timeout:` itself is enforced at `pawl submit` time (there is no background poller for `human`, unlike
  `wait`): the deadline is the `HUMAN_ASKED` journal event's own recorded time plus the parsed
  `timeout:` duration.

### 6. `agentic` steps yield only `success` / `failure`

An `agentic` step has exactly two outcomes: `success` (the return validated, the postcondition
passed) and `failure`. Declaring author-named `outcomes:` on one is a hard validator error whose
message names the fix: **put the routing in a following `deterministic` step** that reads the agentic
step's typed `writes:` and prints a token — otherwise the branch is chosen by the actor that did the
work.

**The step carries a `description:`, not a verbatim prompt.** `description:` is an inline, multi-line
YAML string (`${key}` substituted at dispatch time). The main agent composes the actual subagent
prompt from it, the gathered `context:`, and session knowledge, then dispatches honouring the
`subagent_args:` map exactly as given. No engine guarantee rests on the prompt text: they rest on the
`writes:` return schema (validated by `pawl`), the postcondition (evaluated by `pawl`) and the guards.

The engine passes `subagent_args:` through to the `DISPATCH` block **verbatim and uninterpreted** —
extra arguments for the subagent launch, no engine opinion on harness vocabulary. In Claude Code
these are typically `model`, `tools`, `effort`; other harnesses use whatever they need. Every
agentic step runs as a subagent. The one rule the `PreToolUse` hook keeps on a subagent is the
VCS-mutation deny while an agentic step is live (DESIGN.md §5); nothing in `subagent_args:` is
engine- or hook-enforced.

### 7. `soft: true` — what it costs

A soft postcondition is still *executed* and still gates the transition; failure retries and routes
exactly as a hard one does. `soft:` changes bookkeeping only: `pawl validate` prints a census (count,
percentage, list) and the run's terminal summary prints `N of M steps advanced on a soft
postcondition`. `postcondition: "true"` plus `soft: true` is the floor, and it is counted.

`postcondition:` is **required on `agentic`**, and **optional on `deterministic`, `wait` and
`human`**. On `deterministic`, exit 0 is success unless declared; add one to check the command's
*effect* when exit code alone can't — e.g. after `git push`:
`[ "$(git rev-parse origin/${branch})" = "$(git rev-parse HEAD)" ]`, or after a PR create: the
returned URL resolves. On `wait`/`human` the outcome *is* the check; there is no effect to verify.

### 8. Fix-forward across a crashed or abandoned attempt

**The engine never touches files in the working tree. Only steps do.**

- Attempt N+1 runs on the tree exactly as attempt N left it, and its `DISPATCH` block (agentic) or
  environment (deterministic) carries attempt N's postcondition failure text.
- On crash and resume, the interrupted attempt is re-run on the tree as it currently stands, and the
  `DISPATCH` block/environment says a previous attempt was interrupted and current state should be
  inspected first.
- Fix-forward is the only semantics, for every kind, all the time; there is nothing to opt out of.

The engine guarantees declared state keys, `args:` and the counters, and nothing about the working
tree; nothing rolls back a push, an API call or a posted comment. Idempotent scripts and
postconditions that *re-observe reality* — `glab mr view … | jq -e '.state=="opened"'` rather than a
flag the script set itself — are what make a re-run safe.

### 9. `args:` — workflow arguments

A top-level `args:` block, typed exactly like `state:`, each entry taking `type:` plus either
`default:` or `required: true`. Bound on the command line as `key=value`: `pawl run manage-mr
mr_url=https://…`. They enter run state at run start as read-only state keys; no step may write one.
A missing `required:` arg refuses to start and prints a usage line listing every arg, its type and
its default. Its job is to put "what do I pass in" at the top of the file.

### 10. Guards deny; invariants catch

A guard is a regexp over the command string, matched unanchored anywhere in it (Go's `regexp`
package: RE2 syntax, close to POSIX ERE but with no backreferences). `only_in: [step, …]` allows the
pattern only while one of
those steps is active; `only_in: []` denies it for the entire run, in every step. A guard catches the
canonical spelling of an action and nothing else: variable indirection, `$()`, base64 and a renamed
binary all defeat it. **Invariants are the layer that holds**, because an invariant runs a command
that re-observes real external state.

Invariants are evaluated by the engine after every step completion and after every `pawl submit`.
Exit 0 holds; non-zero violates; a check that *cannot run* — missing script, unparseable output,
network error — counts as violated. A violation blocks the run with the invariant's `message:` as the
reason.

### 11. No fall-through: every step names its own routes

Every step must have exactly one of `next:` or a complete `outcomes:` map. "Complete" means every
outcome that kind can actually produce is routed: for `deterministic`, every author-named token; for
`wait`, every named token plus `timeout`; for `human`, every static option plus `timeout` (and
`chosen:` whenever it is producible — §B.5); for `agentic`, `next:` only. `done` and `blocked` are
the only terminal targets.

Two reserved outcomes are exempt, each having an explicit engine-wide default: **`failure`**, handled
by `catch:` (default `failure → blocked`), and **`exhausted`** on hitting `max_visits:`/`max_steps:`
(default `blocked`). An author may still route either explicitly. Every *other* producible outcome
falls under validator rule 3b. There is no implicit fall-through to the next step in the list and no
implicit "the last step goes to `done`".

### 12. `BLOCKED` is paused, not terminal

A run enters status `BLOCKED` on a `blocked` transition (author-routed, e.g. `timeout: blocked`), an
invariant violation, or an unrouted `exhausted`/`failure`. Something unexpected happened; the run is
paused for review before continuing — it is not a dead end.

- `pawl status` shows the reason and the step it stopped at.
- `pawl run <name>` **resumes at the step that produced the `blocked` outcome**, with that step's
  `attempts:` counter reset to 1, journalled as a user intervention (DESIGN.md §4). It resumes when
  exactly one run resolves for this working copy; `--run <id>` is only needed to disambiguate.
- While `BLOCKED`, `PreToolUse` keeps denying guarded commands — the run is still live. The `Stop`
  hook does **not** refuse.
- `pawl abandon --run <id>` ends a blocked run for good, same as any other live run.
- `--from <step>`, to resume at an arbitrary earlier step, is Milestone 2 (§I).

`terminal: {status: ok|blocked, …}` (§D) names the *message shown on reaching that outcome*; `done`
is the one status that is final.

### 13. `pawl poll` submits for itself; the model never does

For a `wait` step the model runs `pawl poll --run <id> --step <step>`, never `pawl submit`. The poller
re-runs `poll:` every `every:`, reads each iteration's last non-empty stdout line per §B.1, and the
first iteration whose line carries a routed token ends the loop; `pawl poll` then submits on its own
behalf (DESIGN.md §3).

### 14. Conditionals and warnings are patterns, not fields

- **A conditional step** is preceded by a `deterministic` router printing `ask` or `skip`, with
  `outcomes: {ask: confirm_story, skip: launch}` — the skip is a visible edge.
- **A non-blocking warning** is a `json` state key with `default: []`, appended to by whichever script
  noticed and rendered in the terminal `message:`; `default:` counts as a write, so it validates.

### 15. `parallel` steps fan out and join all-or-nothing

A `parallel` step names `branches: [step, step, …]` — at least 2, each a **declared, already-existing
`deterministic` or `agentic` step** (no nesting: a branch cannot itself be `wait`, `human` or
`parallel`). The parallel step owns routing and retry for the whole group; each branch owns none of
its own — a branch step declares no `next:`, `outcomes:`, `catch:`, `attempts:`, `attempt_key:` or
`max_visits:` (all rejected by the validator — §H). A branch may not be the workflow's `start:` step,
may not be listed twice in the same `branches:`, and may be claimed by at most one `parallel` step in
the whole file.

```yaml
- id: fanout
  kind: parallel
  branches: [branch_a, branch_b, branch_c]   # ≥ 2, each declared elsewhere, deterministic or agentic
  next: join
- id: branch_a
  kind: deterministic
  run: echo "branch_a_result=$(wc -l < README.md | tr -d ' ')"
  emits: pairs
  writes: [branch_a_result]
  postcondition: {all_set: [branch_a_result]}
  # no next:/outcomes: here — fanout owns routing for the whole group
```

**Dispatch.** Every branch is entered together, under the owning `parallel` step's own attempt/visit
counters (branches carry no independent budget). A deterministic branch runs to completion
in-process, immediately. Every agentic branch is rendered into the **same** `DISPATCH_PARALLEL` block
(DESIGN.md §2–§3) — one dispatch naming all outstanding agentic branches at once, so the model fires
them as genuinely concurrent subagent calls, not a sequential loop. `pawl submit --step <branch-id>`
reports one branch's result; branch step ids are globally unique, so no new flag is needed to
disambiguate which branch a submission belongs to.

**Join, all-or-nothing.** The group's outcome resolves only once every branch has transitioned — a
still-outstanding agentic branch always leaves the run parked, never abandoned. The outcome is
**`success`** iff every branch's own resolved outcome was `success` (a branch's stdout TOKEN plays no
role: with no `outcomes:` of its own, a branch resolves only to `success`/`failure`), otherwise
**`failure`**. There is no partial-success outcome and no per-branch route — the `parallel` step
itself produces exactly `success` / `failure`, routed by its own `next:` or an `outcomes: {success:
…, failure: …}` map exactly like `deterministic`. If a decision needs to see which *branch* failed or
what a branch wrote, route from a following `deterministic` step that reads the branches' `writes:`.

This is the full extent of `kind: parallel` as shipped: one branch group, one join, all-or-nothing.
`foreach:` fan-out over a runtime-discovered list, with per-item postconditions and a
**partial**-success join, remains Milestone 3 (§I) — not this.

---

## C. The five kinds

| Kind | Body | Produces |
|---|---|---|
| `deterministic` | a shell command (`run:`) | named outcomes via the stdout TOKEN, or `success`/`failure` |
| `agentic` | a subagent dispatch with `description:`, `context:`, `subagent_args:`, and `writes:` as its output schema | `success` / `failure` only |
| `wait` | a `poll:` command re-run every `every:` until a routed token or `timeout:` | named outcomes, or `timeout` |
| `human` | a question mapped onto `AskUserQuestion` — options and/or a runtime list, optional multi-select, always free-text "Other" | the chosen option (static single-select), or the reserved `chosen`, or `timeout` |
| `parallel` | `branches:` naming ≥ 2 declared `deterministic`/`agentic` steps, dispatched together and joined all-or-nothing (§B.15) | `success` (every branch succeeded) / `failure` (any branch failed) only |

Reserved outcome tokens, usable anywhere: `success`, `failure`, `timeout`, `exhausted`, `chosen`.

---
## D. Field reference

| Field | Required | Kinds | Type | Meaning | Default |
|---|---|---|---|---|---|
| `workflow` | yes | file | string | Id; the `pawl run` name. | — |
| `description` | no | file | string | One paragraph. | — |
| `start` | yes | file | step id | First step. | — |
| `max_steps` | no | file | integer | Backstop on total step entries in a run. | `200` |
| `args` | no | file | map | Run arguments, typed like `state:`, read-only, bound as `key=value`. | `{}` |
| `state` | no | file | map | Every key the run may carry: `{type, default, max_length}`. | `{}` |
| `guards` | no | file | list | `{id, match, only_in: [steps]}`; `only_in: []` = denied everywhere. | `[]` |
| `invariants` | no | file | list | `{id, check, message}`; breaking one → `BLOCKED`. | `[]` |
| `steps` | yes | file | list | The graph, read top to bottom. | — |
| `terminal` | no | file | map | `{status: ok\|blocked, message}` per terminal id (§B.12). | implicit |
| `id` | yes | all | string | Unique step name; transition target. | — |
| `kind` | yes | all | enum | `deterministic` \| `agentic` \| `wait` \| `human` \| `parallel`. | — |
| `run` | yes | deterministic | string | A command. Exit 0 → token/`success`; non-zero → `failure`. | — |
| `emits` | no | deterministic, wait | enum | Payload grammar: `json` \| `pairs`; the *maximum* payload shape (§B.1). | `json` |
| `description` | **yes** | agentic | string | Inline, multi-line string: intent, constraints, definition of done; `${key}` substituted at dispatch time. Never handed to the subagent verbatim (§B.6). | — |
| `context` | no | agentic | list | Files/`!cmd`-tagged command output gathered by `pawl` and included in the `DISPATCH` block; `${key}` resolved first. A plain scalar names a file; only a scalar carrying the YAML tag `!cmd` (e.g. `!cmd "git diff"`) is run as a command — every plain entry starting with `!` (quoted or not) is rejected by rule 18, not just ones that "look like" an attempt at a command. A real file whose name starts with `!` needs the `./!name` escape hatch instead. | `[]` |
| `subagent_args` | no | agentic | map | Extra arguments passed through verbatim for the subagent launch — in Claude Code typically `model`, `tools`, `effort`; other harnesses use whatever they need. Not interpreted or enforced by the engine (§B.6). | `{}` |
| `poll` | yes | wait | string | Command re-run every `every:`; its last stdout line is read per §B.1. | — |
| `every` | no | wait | duration | Poll interval. | `60s` |
| `timeout` | **yes** | wait, human | duration | Hard deadline; on expiry the outcome is `timeout`. | — |
| `question` | yes | human | string | Prompt text; `${key}` substituted. | — |
| `options` | one of `options`/`options_from` | human | list | Static option list (§B.5). | — |
| `options_from` | one of `options`/`options_from` | human | state key | A `json` state key holding a list of strings, resolved at ask time. Outcome is always `chosen` (§B.5). | — |
| `multi` | no | human | boolean | Multi-select. `true` forces the outcome to `chosen`. | `false` |
| `branches` | **yes** | parallel | list | ≥ 2 declared `deterministic`/`agentic` step ids, dispatched and joined together (§B.15); a listed step may declare no `next:`/`outcomes:`/`catch:`/`attempts:`/`attempt_key:`/`max_visits:` of its own. | — |
| `writes` | no | all | list/map | Keys produced. Typed map required on `agentic` — it is the output schema. On `human`, exactly one key, required whenever `options_from:`, `multi: true` or a `chosen:` route is used. | `[]` |
| `postcondition` | **yes** on agentic | all | string/map | Shell string, `{command}`, `{all_set}`, or `{equals}`. Evaluated by the engine; optional on `deterministic` (exit 0 = success unless declared), `wait`, `human`. | — |
| `soft` | no | all | boolean | Marks the check as judgement-bounded. Counted at validate *and* run time. | `false` |
| `attempts` | no | deterministic, agentic | integer | Re-runs of this step on postcondition failure. ≥ 1. | `1` |
| `attempt_key` | no | deterministic, agentic | string | `"${template}"` overriding the automatic failure-text key (§B.4). | automatic |
| `max_visits` | no | all | integer | Cap on entries to this step in one run; exceeding it → `exhausted`. | `10` |
| `retry` | no | deterministic, wait | map | `{max_attempts, backoff}` — retries the *body* on a hard failure, before any outcome is resolved. `backoff` is one duration (e.g. `30s`); retry *n* waits `backoff` × *n*, linear, no jitter. | none |
| `catch` | no | all | list | Ordered `{on: <outcome>, next: <step or terminal>}`; fires on exhaustion. | `failure → blocked` |
| `next` | one of `next`/`outcomes` | all | step id | Single successor. Mutually exclusive with `outcomes:`. | — (no fall-through — §B.11) |
| `outcomes` | one of `next`/`outcomes` | all | map | Outcome → step/terminal, covering every outcome the step can produce (§B.11). | — |

---

## E. Annotated example

```yaml
workflow: ship-change
start: preflight
max_steps: 60                              # backstop; per-step caps do the real work (§B.4)
args:                                      # pawl run ship-change mr_url=https://…
  mr_url: {type: string, default: ""}      # read-only; no step may write it
state:
  branch:        {type: string}
  title:         {type: string}
  findings:      {type: json,    default: []}
  comment_count: {type: integer, default: 0}   # numeric type, compared numerically
  reviewer:      {type: string,  default: ""}
guards:
  - id: never-rewrite-changelog-by-hand
    match: "(sed|awk) .*CHANGELOG.md"
    only_in: []                            # denied everywhere, all run long (§B.10)
invariants:
  - id: mr-still-open
    check: scripts/mr-open.sh ${mr_url}  # re-observes reality after every step (§B.10)
    message: "The MR was closed out from under the run."

steps:
  - id: preflight
    kind: deterministic
    run: scripts/preflight.sh ${mr_url} && git fetch --quiet
    emits: pairs                           # script prints `FRESH branch=x title=y` (§B.1)
    writes: [branch, title]
    postcondition: {all_set: [branch]}
    outcomes:                              # named outcomes from the stdout TOKEN (§B.3)
      FRESH:    wait_for_mr
      EXISTING: choose_reviewer
  - id: wait_for_mr
    kind: wait
    poll: scripts/refresh.sh ${branch}
    every: 60s
    timeout: 6h
    emits: pairs                           # poller prints e.g. `COMMENTS count=3`
    writes: {comment_count: {type: integer}}
    max_visits: 8                          # caps the wait → fix → wait cycle (§B.4)
    outcomes:
      CI_FAILED: fix_issues
      COMMENTS:  fix_issues
      CLEAN:     choose_reviewer
      timeout:   blocked
      exhausted: choose_reviewer           # stop churning; ask a person instead
  - id: fix_issues
    kind: agentic
    description: |                         # ${findings} substituted at dispatch time (§B.2)
      Fix every issue in ${findings}. Each entry names a file and a problem; make the smallest
      change that resolves it without changing unrelated behaviour. Do not touch CHANGELOG.md
      or push — a later step handles that. When done, re-run scripts/verify.sh yourself and
      report one findings entry per issue you touched, each with a verify_status field.
    subagent_args: {tools: [Read, Edit, "Bash(scripts/verify.sh)"], model: sonnet}   # passed through verbatim (§B.6)
    writes: {findings: {type: json}}       # the typed map is the subagent's output schema
    postcondition: "jq -e 'all(.[]; has(\"verify_status\"))' <<<${findings}"
    soft: true                             # the agent's own claim; counted every run (§B.7)
    attempts: 3                            # same failure text → same countdown (§B.4)
    next: wait_for_mr                      # fix-forward: attempt N+1 runs on the tree as left
  - id: choose_reviewer
    kind: human
    question: "Who reviews ${title}? (${comment_count} comments addressed.)"
    options: [alice, bob, skip]            # the chosen option IS the outcome token (§B.5)
    timeout: 24h
    writes: [reviewer]
    outcomes:
      alice:   assign
      bob:     assign
      skip:    done
      timeout: wait_for_mr                 # ask again next pass rather than blocking
  - id: assign
    kind: deterministic
    run: glab mr update ${mr_url} --assignee ${reviewer} --ready
    postcondition: scripts/mr-assigned.sh ${mr_url} ${reviewer}
    next: done

terminal:
  done:    {status: ok,      message: "MR assigned to ${reviewer}."}
  blocked: {status: blocked, message: "Paused for review: ${blocked_reason}"}
```

## F. Hello workflow

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
    run: pytest -q                       # exit 0 = success; no postcondition needed (§B.7)
    next: done
terminal: {done: {status: ok}}
```

Every field beyond these appears only when the workflow branches, loops, waits on a person, or hands
work to an agent. See `docs/quickstart.md`.

## H. `pawl validate` — the static checks

1. `next:` / `outcomes:` / `catch[].next` names a step or terminal that does not exist.
2. A step is unreachable from `start:`.
3. A step has no outgoing edge and is not terminal (missing both `next:` and `outcomes:`).
3b. A step has `outcomes:` but not every outcome its kind can produce is routed (§B.11):
   `"step X has no route for outcome Y"`.
4. A `${key}` names a key that is neither declared in `state:`/`args:` nor an engine pseudo-key; or
   uses `${…}` in a field where substitution does not apply (§B.2).
5. A step declares `writes:` on an `args:` key — args are read-only (§B.9).
6. An `agentic` step has no `postcondition:`. `soft: true` is not an exemption from *declaring* one.
   `deterministic`, `wait` and `human` are exempt (§B.7).
7. A `wait` or `human` step with no `timeout:`, or with no route for the `timeout` outcome.
8. A `human` step with neither `options:` nor `options_from:`, or with both; an unrouted static
   option; `multi:` on any kind other than `human`; a `chosen:` route with no `writes:` key, or a
   `writes:` with other than exactly one key; `options_from:`/`multi: true` without `writes:`.
9. A step declares author-named `outcomes:` but its kind cannot produce a token: `agentic` never can
   (hard error, with "route from a following `deterministic` step" as the named fix — §B.6).
9b. `subagent_args:` is present and is not a map.
10. `emits:` is anything other than `json` or `pairs`.
11. `writes:` conflicts with the `state:` type declaration, or writes an undeclared key.
12. `max_visits:` or `max_steps:` is not a positive integer, or a cycle whose every step has
    `max_visits:` raised above `max_steps:` — a cap that can never bind.
13. `attempts:` is less than 1.
14. A `postcondition:` map uses a key other than `command` / `all_set` / `equals`.
15. `guards[].only_in` names a step that does not exist.
16. A referenced file (`context:`, `run:`, `poll:`, `check:`) does not exist or is not executable.
17. `kind: parallel` (§B.15, §H checkParallelBranches): `branches:` has fewer than 2 entries; a
    branch name does not resolve to a declared step; a branch's kind is not `deterministic` or
    `agentic`; a branch is listed more than once in the same `branches:`; a branch is claimed by more
    than one `parallel` step; a branch is the workflow's `start:` step; or a branch declares
    `next:`/`outcomes:`/`catch:`/`attempts:`/`attempt_key:`/`max_visits:` of its own.
18. A `context:` entry starts with `!` but does not carry the YAML tag `!cmd` — either a *quoted*
    string that merely starts with `!` (e.g. `"!git diff main...HEAD"`, read as a FILE PATH, not a
    command), or an *unquoted* entry (e.g. `!git diff main`), which YAML parses as some other
    custom tag (`!git`) applied to the rest of the scalar (`diff main`), not as that literal text.
    The error names the corrected `!cmd "..."` form either way.

Plus two warnings: a key written and never read; a key read on some path before anything writes it.
And one census, printed every time: the `soft:` count, percentage and list.

Every message names the file and line, the step id, the rule, and the *fix* rather than only the
fault, and shows structured forms by example.

## I. CLI and roadmap

**Milestone 1 — five commands plus three internal ones.**

```
pawl run <name> [key=value …] [--fresh] [--force]   start, or resume a non-terminal run — BLOCKED
             [--run <id>]                          included — when exactly one resolves
pawl validate <name>                                the checks in §H
pawl status [--run <id>]                            where a run is, and its trust surface
pawl abandon --run <id>                             always available, always terminal
pawl list                                           resolvable workflows and their source
```

`--fresh` starts a new run and resets every counter; `pawl run` refuses to resume a run whose workflow
file has changed since it started, and offers `--fresh`. `--force` breaks a stale lock. `pawl run` also
refuses to start if the installed binary's version does not match the plugin's pinned version, naming
both. `--run <id>` is needed only to disambiguate when several runs resolve (§B.12). Internal
commands, which the model calls and an author never writes: `pawl submit --run … --step … --json …`, `pawl poll --run …
--step …` (§B.13), and `pawl hook pre|stop`.

Milestone 1 covers the five kinds (`kind: parallel`'s single-group, all-or-nothing `branches:`
included — §B.15), `state:` and `args:`, the stdout grammar, `${…}` substitution, postconditions with
`soft:`, the attempt and visit caps, `retry:`/`catch:`, guards and invariants, fix-forward,
crash-safe resume, `human` steps in full, and `pawl validate`.

**Milestone 2.** `pawl graph` (Mermaid from the parsed graph); `pawl validate --walk step=TOKEN,…`, printing
the step sequence a given outcome assignment produces without executing anything; `pawl status --history`;
`pawl run <name> --from <step>`; plugin-shipped workflows, "if free".

**Milestone 3.** `foreach:` fan-out over a runtime-discovered list, with per-item postconditions and a
**partial**-success join (`kind: parallel` itself, single-group and all-or-nothing, already shipped in
Milestone 1 — §B.15); an `outcome:` member of the agentic return schema, constrained to a declared
enum; a `when:` predicate.

Installation and distribution are in DESIGN.md §9.

---

## K. Rejected alternatives

- **Markdown-with-frontmatter per phase** (`phases/<state>.md` read by the agent): ordering is only as reliable as the agent's prose-following, and the graph must be reconstructed by reading every file.
- **A `prompts/<step>.md` file handed to the subagent verbatim**: a static file cannot fold in session context, and no guarantee rested on the prompt text (§B.6).
- **A JS/TS DSL** (`agent()`, `workpool()`, `wait()`): more expressive, but not statically validatable, visualisable, resumable at a step boundary, or safely editable by a non-author.
- **Memoization of completed step results**: redundant with a single cursor, and blind to the window it was meant to close — a side effect landing just before the process dies.
- **A `loops:` block** naming a cycle by its step list: a ninth noun for what `max_visits:` on the cycle's entry step already does.
- **`attempt_key: {classify: <script>}`**: a classifier script per step, for something the engine already has — the postcondition's failure text.
- **`kind: tool`**, a fifth executor for zero-judgement privileged edits: it is `agentic` with a
  narrow `subagent_args.tools` list, and "no judgement here" is not machine-checkable.
- **`isolation: main | subagent`**: its meaning was "enforcement off here" — the engine enforces only
  the VCS-mutation deny on a live subagent (DESIGN.md §5).
- **Working-tree snapshot and restore**: doubles the durable state the engine must keep correct, for a guarantee a fresh attempt does not need (§B.8).
- **Dynamically installed hooks**, rewritten per run: Claude Code hooks are static, so the hooks ship once and discover the live run on disk.
- **`pawl count` / `pawl reset` as `PATH` shims**: they make a step script a writer of engine state.
- **Fields**: `on_timeout:`/`on_reject:` (reserved tokens), `exit_map:`/`outcomes_from: {exit:}` (§B.1), `reads:` (§A), `loops:` (§B.4), `deny_always:` (`only_in: []`), `writes_format:` (`emits:`), `emits: value`/`none` (§B.1), `snapshot:` (§B.8), `when:`/`warn:` (§B.14), `soft_writes:` (a second census dilutes the first), `timeout:` on `deterministic` (one engine-wide ceiling), `env:`/`defaults:`/`foreach:` (Milestone 3).
