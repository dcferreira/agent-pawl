# `human` steps

Ask the person at the keyboard. One step, one question, mapped exactly onto Claude Code's
`AskUserQuestion` tool. See [step-types.md](../step-types.md) for how the four kinds compare.

## Fields

- **`question:`** — the text shown, `${key}` substituted, JSON pretty-printed. Put the facts the
  decision needs into it.
- **Options** — exactly one of `options: [a, b, c]` (static list) or `options_from: state_key` (a
  `json` state key holding a list of strings, resolved at ask time). `multi: true` allows
  multi-select (default `false`).
- **"Other" is always there** — `AskUserQuestion` always offers a free-text box; there is no field to
  turn it off. Never write a workflow that assumes the answer is one of your options.
- **`writes:`** — exactly one key, **required** whenever `options_from:`/`multi: true` is used, or
  `chosen:` is routed. `string` for a single answer, `json` when the value can be a list.
- **`timeout:`** — required, a reserved outcome you must route. Nothing is written on timeout. The
  idiom for "ask again later" is routing `timeout` back to a preceding `wait` step.
- **No `postcondition:` needed** — the answer *is* the check; `human` is exempt from the validator's
  requirement.

## Example

```yaml
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
```

The engine prints `ASK 7f3a choose_reviewer`; the session asks with `AskUserQuestion`. You never
type a `wf` command.

## Outcomes

Two shapes:

- **Static `options:`, `multi: false`** — the chosen option *is* the outcome token
  (`outcomes: {alice: assign}`). Free text yields the reserved outcome `chosen`, with the text in
  `writes:`.
- **`options_from:` or `multi: true`** — the outcome is *always* `chosen`; the answer (single value,
  list, or free text) goes to the one `writes:` key. Branch on it from a deterministic router — that
  router is also where you handle an empty list or an out-of-list value.

## Common mistakes

- Assuming every answer is one of your listed options — route `chosen:`, or accept it goes to
  `blocked` (paused, not ended — see [running.md#blocked](../running.md#blocked)).
- Omitting `writes:` when `options_from:`/`multi:`/`chosen:` is in play — the validator requires it.
- Leaving `timeout:` unrouted, or unbounded — there is no unbounded form by design.
- Naming an option the same as a reserved outcome (`rejected` reads like `failure`) — use `deny`, not
  ambiguous synonyms.
- A dead option with no route and no fall-through — a validator error, and worse in the UI than no
  option at all.

Back to [writing-workflows.md](../writing-workflows.md).
