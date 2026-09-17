# `deterministic` steps

The engine runs a shell command itself. No model, no tokens, no handshake. See
[step-types.md](../step-types.md) for how the four kinds compare.

## Fields

- **`run:`** — any command you already have; pipes, `&&`, a script. Runs with cwd at the
  working-copy root, `${key}` substituted as single shell-quoted tokens. Every key a step reads is
  also exported as `WF_<KEY>` (upper-cased).
- **`emits:`** — `json` (default) or `pairs`. The engine reads **the last non-empty line of
  stdout**; everything above is logged, never parsed. `emits: json` expects
  `{"mr_url":"…","mr_iid":"41"}`; `emits: pairs` expects `dir_exists=yes vcs=jj`. Keys must be a
  subset of `writes:`.
- **`writes:`** — `[branch, title]` (types from `state:`) or `{comment_count: {type: integer}}`
  (typed here). Values are coerced and rejected loudly if they don't fit.
- **Named outcomes** — print a token as the first word of the last line and route it in `outcomes:`.
  A bare token with nothing after it writes nothing. **Non-zero exit is always `failure`**, whatever
  was printed — exit codes never select outcomes; wrap a script that uses them:
  `case $? in 0) echo OK ;; 1) echo MISSING_DIR ;; esac`.
- **`postcondition:`** — optional (required only on `agentic`). Exit 0 is success unless you declare
  one. Add it to check the command's *effect* when the exit code alone can't — after `git push`:
  `[ "$(git rev-parse origin/${branch})" = "$(git rev-parse HEAD)" ]`; after a PR create: the
  returned URL resolves. Four forms: a shell string (exit 0 = pass), `{command: "…"}`,
  `{all_set: [key, …]}`, `{equals: {key: "value"}}`. `all_set`/`equals` run in-process; `command:` is
  a subprocess. Best postconditions re-observe reality (e.g.
  `git ls-remote --exit-code origin "${branch}"`) rather than trust a flag the step set itself.

### When to add a postcondition

Most `deterministic` steps need nothing: `pytest -q` exiting 0 already means the tests passed. Add a
postcondition when the exit code confirms the command *ran*, not that it had the effect you wanted —
`git push` can exit 0 into a stale ref, `glab mr create` can exit 0 and hand back a URL that 404s.
`postcondition: '[ "$(git rev-parse origin/${branch})" = "$(git rev-parse HEAD)" ]'` after a push, or
a `curl -fsS "${mr_url}"` after a create, check the effect exit code alone can't.
- **`attempts:`/`retry:`/`catch:`** — different layers. `retry: {max_attempts, backoff}` re-runs the
  *body* on a hard failure (flakiness), before any outcome. `attempts:` re-runs when the
  *postcondition* fails, carrying the failure text into the next attempt; keyed on a hash of that
  text, overridable with `attempt_key:`. `catch:` decides where to go once attempts are spent
  (default `failure → blocked`).

## Example

```yaml
  - id: push_and_create
    kind: deterministic
    run: scripts/push-create.sh "${branch}" "${title}"
    writes: [mr_url, mr_iid, project_path]
    postcondition: {all_set: [mr_iid]}
    next: ai_review
```

Script's last line: `{"mr_url":"https://gitlab/x/y/-/merge_requests/41","mr_iid":"41","project_path":"x/y"}`.

## Outcomes

`next:` for a single successor, or `outcomes:` keyed on the printed token:

```yaml
  - id: bump_deps
    kind: deterministic
    run: scripts/bump-deps.sh
    writes: [changed_packages]
    postcondition: "uv lock --check"
    outcomes:
      upgraded:   run_tests
      no_changes: up_to_date
```

## Common mistakes

- Trusting a flag the script set itself instead of re-observing reality in the postcondition.
- Expecting exit codes to route outcomes — they don't; wrap the script instead.
- A trailing `set -x` trace or stray `echo` landing on the last line — keep the token/payload last.
- Non-idempotent steps: a crash re-runs the step from the top on the tree as it stands, so check
  before you act (`gh pr view … || gh pr create …`) rather than trust "I already did this" state.

Next: [agentic steps](agentic.md), or back to [writing-workflows.md](../writing-workflows.md).
