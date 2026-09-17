# Writing workflows

## Where files live

```
.claude/workflows/
    manage-mr.yaml
    scripts/
        preflight.sh
```

`wf run <name>` resolves `<name>` by walking up from your working-copy root for
`.claude/workflows/<name>.yaml`, falling back to `~/.claude/workflows/<name>.yaml`. First match wins;
the run banner prints which one it used.

`scripts/` paths are resolved **relative to the workflow file**, not your shell's cwd — a repo-local
workflow is self-contained. There is no `prompts/` directory: an agentic step's prompt is an inline
`description:` in the YAML, composed into the subagent prompt at dispatch time (see
[agentic steps](steps/agentic.md)).

```
› wf list
manage-mr           repo   .claude/workflows/manage-mr.yaml
dependency-upgrade  repo   .claude/workflows/dependency-upgrade.yaml
brain-dispatch      user   ~/.claude/workflows/brain-dispatch.yaml
```

Steps run with cwd at the working-copy root, never wherever the session happened to be.

## The skeleton

Top-level keys, in the order they're usually written: `workflow` (the name), `description`
(optional), `start`, `max_steps` (backstop, default 200), `args` (command-line inputs, read-only),
`state` (every value the run may carry), `guards`/`invariants` (see
[guards-and-invariants.md](guards-and-invariants.md)), `steps` (the graph), and `terminal:` — optional
(`done`/`blocked` exist implicitly) but a message is worth writing. See the full annotated example
below.

## State and `${key}`

Declare every value that crosses a step boundary:

```yaml
state:
  branch:        {type: string}
  findings:      {type: json,    default: []}
  comment_count: {type: integer, default: 0}
  title:         {type: string,  max_length: 72}
```

Types are `string`, `integer`, `number`, `boolean`, `json`. A value that does not fit its type is
rejected — the engine will not quietly stringify your integer.

Read a key as `${key}`. Substitution works in `run:`, `poll:`, `check:`, `postcondition:`,
`description:`, `context:` (including inside a `!cmd`, resolved before the command runs), `question:`,
`attempt_key:` and terminal `message:`. Not in `id:`, `kind:`, `next:`, `outcomes:` keys/targets,
`catch[].next`, or guard `match:` — the graph must be knowable without running anything. `$${` gives a
literal `${`.

In shell contexts a value is substituted as one shell-quoted token, so a JSON blob with newlines and
quotes survives an argument boundary intact. In prose contexts (`description:`, `question:`,
`message:`) the raw value goes in, JSON pretty-printed.

Six pseudo-keys are always readable and never declared: `run_id`, `step`, `attempt`, `visits`,
`last_error`, `blocked_reason`. Scripts also get state through the environment: every key a step
reads is exported as `WF_<KEY>`, upper-cased — `${branch}` is also `$WF_BRANCH`.

## Arguments

```yaml
args:
  mr_url:  {type: string, required: true}
  dry_run: {type: boolean, default: false}
```

```
wf run ship-change mr_url=https://gitlab/x/y/-/merge_requests/41 dry_run=true
```

Args are ordinary state keys that no step may write — `writes: [mr_url]` is a validator error.

## Transitions

Three fields, in order of precedence:

```yaml
    next: assign                   # one successor
```
```yaml
    outcomes:                      # branch on the outcome
      FRESH:    wait_for_mr
      EXISTING: choose_reviewer
      timeout:  blocked
      exhausted: choose_reviewer
```
```yaml
    catch:                         # ordered fallbacks, on exhaustion
      - on: failure
        next: blocked
```

`next:` and `outcomes:` are mutually exclusive. Where outcomes come from depends on the kind —
[a token your script printed](steps/deterministic.md), [a poll token](steps/wait.md),
[the option a person picked](steps/human.md), or plain `success`/`failure` from
[an agentic step](steps/agentic.md).

`catch:` fires when a step is *finished failing* — body failed, or postcondition failed with the
`attempts:` budget spent. With no `catch:`, default is `failure → blocked`.

**Every step needs `next:` or a complete `outcomes:` map.** There is no fall-through: omitting
`next:` does not send you to the next step in the file, and the last step does not implicitly go to
`done` — reordering `steps:` never changes what a workflow does. "Complete" routes every outcome the
step's kind can produce, except `failure` (via `catch:`) and `exhausted` (unrouted → `blocked`),
which already have engine-wide defaults. An outcome with no route is a `wf validate` error naming it.

## Caps

```yaml
    max_visits: 8      # entries to this step in one run. default 10
```

```yaml
max_steps: 60          # total step entries in the run. default 200
```

Every cycle re-enters through at least one step; capping that step caps the cycle. Exceeding either
gives the step outcome `exhausted` — route it, usually to a `human` gate
(`exhausted: human_fix_decision`); unrouted, it goes to `blocked`.

## An annotated example

```yaml
workflow: ship-change
start: preflight
max_steps: 60
args:
  mr_url: {type: string, default: ""}
state:
  branch:        {type: string}
  title:         {type: string}
  findings:      {type: json,    default: []}
  comment_count: {type: integer, default: 0}
  reviewer:      {type: string,  default: ""}
guards:
  - id: never-rewrite-changelog-by-hand
    match: "(sed|awk) .*CHANGELOG.md"
    only_in: []
invariants:
  - id: mr-still-open
    check: scripts/mr-open.sh "${mr_url}"
    message: "The MR was closed out from under the run."

steps:
  - id: preflight
    kind: deterministic
    run: scripts/preflight.sh "${mr_url}" && git fetch --quiet
    emits: pairs                           # prints `FRESH branch=x title=y`
    writes: [branch, title]
    postcondition: {all_set: [branch]}
    outcomes:
      FRESH:    wait_for_mr
      EXISTING: choose_reviewer

  - id: wait_for_mr
    kind: wait
    poll: scripts/refresh.sh "${branch}"
    every: 60s
    timeout: 6h
    emits: pairs
    writes: {comment_count: {type: integer}}
    max_visits: 8
    outcomes:
      CI_FAILED: fix_issues
      COMMENTS:  fix_issues
      CLEAN:     choose_reviewer
      timeout:   blocked
      exhausted: choose_reviewer

  - id: fix_issues
    kind: agentic
    description: |
      Fix every issue in ${findings}. Make the smallest change that resolves each one, then run
      scripts/verify.sh yourself and report a verify_status per item. Do not commit or push.
    subagent_args: {tools: [Read, Edit, "Bash(scripts/verify.sh)"]}
    writes: {findings: {type: json}}
    postcondition: "jq -e 'all(.[]; has(\"verify_status\"))' <<<\"${findings}\""
    soft: true
    attempts: 3
    next: wait_for_mr

  - id: choose_reviewer
    kind: human
    question: "Who reviews ${title}? (${comment_count} comments addressed.)"
    options: [alice, bob, skip]
    timeout: 24h
    writes: [reviewer]
    outcomes:
      alice:   assign
      bob:     assign
      skip:    done
      timeout: wait_for_mr

  - id: assign
    kind: deterministic
    run: glab mr update "${mr_url}" --assignee "${reviewer}" --ready
    postcondition: scripts/mr-assigned.sh "${mr_url}" "${reviewer}"
    next: done

terminal:
  done:    {status: ok,      message: "MR assigned to ${reviewer}."}
  blocked: {status: blocked, message: "Paused for review: ${blocked_reason}"}
```

Five steps, one cycle, one agent, one question, every transition visible in the file.

## A conditional step

A router, not a `when:` field — the skip is a visible edge, so reachability and cycle caps still
mean something:

```yaml
  - id: need_story
    kind: deterministic
    run: scripts/need-story.sh
    postcondition: "true"
    soft: true
    outcomes:
      ask:  confirm_story
      skip: launch
```

## Sharing

Commit `.claude/workflows/` and the workflow is the team's: reviewable in a diff, validated in CI with
`wf validate`. Keep personal ones in `~/.claude/workflows/` — a repo-local file of the same name wins.

Next: the four kinds — [deterministic](steps/deterministic.md), [agentic](steps/agentic.md),
[wait](steps/wait.md), [human](steps/human.md).
