# `agentic` steps

A subagent does the work; the engine checks it. See [step-types.md](../step-types.md) for how
the five kinds compare.

## Fields

- **`description:`** (required, inline, multi-line, `${key}` substituted) — intent, constraints, and
  definition of done. Not the prompt itself: the session composes the subagent prompt from this plus
  `context:` and session knowledge. Don't restate `writes:`, write the literal subagent prompt, or
  smuggle in routing logic.
- **`context:`** — files/command output `pawl` gathers into `DISPATCH`: `CHANGELOG.md` (verbatim),
  `"!uv run pytest -q | tail -n 150"` (stdout). `${key}` inside `!cmd` resolves first. Keep it small.
- **`subagent_args:`** — extra arguments passed through verbatim for the subagent launch; the engine
  does not interpret or enforce any of it. In Claude Code these are typically `model`, `tools`,
  `effort:`; other harnesses use whatever they need. The one subagent rule the `PreToolUse` hook
  keeps is denying VCS-mutating `Bash` from a subagent while an agentic step is live — that applies
  regardless of what `subagent_args:` says.
- **`writes:`** — required, a typed map: the subagent's return shape. Missing key or wrong type
  fails the step; prose outside the schema is discarded.
- **`postcondition:`** — required; engine-run, the returned JSON is an input, never the verdict. Mark
  `soft: true` where you cannot check the real thing.
- **`attempts:`** — on retry, `DISPATCH` carries the previous failure text. Attempt N+1 runs on the
  tree exactly as N left it. A crash does not consume an attempt. Keys on a hash of the failure text;
  override with `attempt_key:` if noisy.

## Example

```yaml
  - id: describe
    kind: agentic
    description: |
      Write the MR title and description from the diff and commits below. Read-only — never
      run a state-changing VCS command. Title: conventional-commit style, under 72 characters.
      Description: what changed, why, and how it was verified. Write the description to a temp
      file yourself and return its path.
    context:
      - "!git diff \"${target_branch}\"...HEAD"
      - "!git log \"${target_branch}\"..HEAD --format=%B"
    subagent_args: {tools: [Read, "Bash(git diff:*)", "Bash(git log:*)"], model: sonnet}
    writes:
      title:            {type: string, max_length: 72}
      description_file: {type: string}
    postcondition: "[ -n \"${title}\" ] && [ -s \"${description_file}\" ]"
    attempts: 3
    next: push_and_create
```

The engine prints a `DISPATCH` block: rendered `description`, gathered `context`, the `writes:`
schema, `subagent_args:` (printed as given). The session composes the subagent prompt, dispatches
honouring the `subagent_args:` settings its harness understands, and submits the result via
`pawl submit`.

## Outcomes

`success` or `failure`, and that is all — an agentic step cannot name its own outcomes. Declaring
`outcomes: {…}` on one is a hard validator error. When the branch depends on what the agent produced,
route from a following `deterministic` step that reads its `writes:` and prints a token:

```yaml
  - id: ai_review_route
    kind: deterministic
    run: scripts/ai-review-route.sh "${findings}" "${pipeline_failed}"
    postcondition: "true"
    soft: true
    outcomes:
      blocking: fix_issues
      clean:    wait_for_mr
```

## Common mistakes

- Restating `writes:` in prose, or writing the literal subagent prompt, in `description:`.
- Naming outcomes on an agentic step — always `success`/`failure`; route from a deterministic step.
- Giving `subagent_args.tools` more than needed — the hook doesn't enforce it, so it's guidance to
  the subagent, not confinement; keep it tight anyway.
- Forgetting `soft: true` on a postcondition that only proves shape, not correctness.
- `attempts:` above 2–4 — you're paying for a step that should be deterministic instead.

Next: [wait steps](wait.md).
