# The five step kinds

Every step in a workflow is one of five kinds. Each pairs a body (who does the work) with the shape
of outcome it can produce. This page is the overview; [steps/*.md](steps/deterministic.md) hold the
full field reference for each.

## `deterministic`

A shell command the engine runs itself — no model involved. Use it whenever a script can say,
reliably, what happened.

```yaml
- id: test
  kind: deterministic
  run: pytest -q
  next: done
```

Exit 0 is success; no `postcondition:` needed here — it would only restate the exit code. Add one
when the exit code alone can't confirm the *effect*:

```yaml
- id: push
  kind: deterministic
  run: git push origin "${branch}"
  postcondition: '[ "$(git rev-parse origin/${branch})" = "$(git rev-parse HEAD)" ]'
  next: open_pr
```

**Checks:** an optional postcondition (a command the engine runs) gates the transition; the stdout
token, if any, resolves a named outcome.

**Use it for:** running tests, pushing a branch, creating a PR, polling a status once.

## `agentic`

A subagent does the work; the engine still checks it. Use it when the task needs judgement over
unstructured input — writing prose, deciding how to fix something — that no script can grade for
correctness, only for shape.

```yaml
- id: changelog
  kind: agentic
  description: |
    Add a changelog entry for this change from the diff and commits below.
  context: ["!git diff main...HEAD"]
  subagent_args: {tools: [Read, Edit], model: sonnet}
  writes: {entry_added: {type: boolean}}
  postcondition: "[ \"${entry_added}\" = true ]"
  next: commit
```

**Checks:** the `writes:` schema (typed, validated) and the postcondition. `subagent_args:` is
passed through verbatim for the subagent launch — the engine doesn't interpret or enforce it. An
agentic step can only produce `success` or `failure` — never its own named outcome.

**Use it for:** fixing failing tests, writing a changelog entry or MR description, triaging review
comments.

## `wait`

A command re-run on an interval until it reports something, or a deadline passes. Use it whenever
you're waiting on the outside world to change state on its own schedule.

```yaml
- id: wait_for_ci
  kind: wait
  poll: scripts/poll-ci.sh
  every: 60s
  timeout: 2h
  outcomes:
    SUCCESS: merge
    FAILURE: fix_tests
    timeout:  blocked
```

**Checks:** each poll iteration's last stdout line for a routed token; a poll with no token, or an
unrouted one, means "not yet" — only a non-zero exit is `failure`.

**Use it for:** CI finishing, a reviewer's reply arriving, a deploy completing.

## `human`

A question mapped onto `AskUserQuestion` — a static list, a runtime list, or free text. Use it
whenever a decision genuinely needs a person, not a script or a model guessing.

```yaml
- id: approve_merge
  kind: human
  question: "CI is green. Approve merge?"
  options: [approve, deny]
  timeout: 24h
  outcomes:
    approve: merge
    deny:    blocked
    chosen:  blocked
    timeout: blocked
```

**Checks:** every static option (and `timeout`) is routed; a free-text "Other" answer against a
static list yields the reserved outcome `chosen`, which must be routed too if reachable.

**Use it for:** approving a merge, picking reviewers, choosing between two live options.

## `parallel`

Fan two or more independent `deterministic`/`agentic` steps out together and join them
all-or-nothing. Use it when the branches genuinely don't affect each other and you want them
running concurrently, not to express a partial-success race.

```yaml
- id: fanout
  kind: parallel
  branches: [branch_a, branch_b]
  next: join
```

**Checks:** the group's outcome is `success` iff every branch's own outcome was `success`,
otherwise `failure` — no partial-success outcome, no author-named token of its own.

**Use it for:** summarizing two unrelated files at once, running two independent scripts before a
join step, anything genuinely parallelizable with no data dependency between the branches.

`steps/deterministic.md`, `steps/agentic.md`, `steps/wait.md`, `steps/human.md` and
`steps/parallel.md` hold every field, its default, and the common mistakes for each kind.

## Start agentic, harden later

Ideally every step that *can* be `deterministic` is — it's cheaper and it can't drift. But you don't
have to get there in one go. A workflow works immediately with most steps `agentic`, even ones a
script could do: the model reads `description:`, does the work, and the postcondition still gates
the transition. Then, as you want more robustness and less cost, switch steps to `deterministic` one
at a time. The postcondition stays the same, so the rest of the workflow doesn't notice which kind is
behind it.
