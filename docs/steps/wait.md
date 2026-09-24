# `wait` steps

Wait on something outside the run: CI, a review, a build, a person doing a thing elsewhere. See
[step-types.md](../step-types.md) for how the five kinds compare.

## Fields

- **`poll:`** — the command, run every `every:` (default `60s`). Same stdout contract as
  [deterministic](deterministic.md): last non-empty line, optional token, payload
  per `emits:`. An unrouted token, or none, means "not yet" — print nothing useful and exit 0. A
  non-zero exit is a `failure`, routed via `catch:` — unless the step declares `retry:`, in which
  case a non-zero exit is instead a hard failure retried in-place first (see `retry:` below); "not
  yet" is never a failure either way.
- **`timeout:`** — required. On expiry the outcome is `timeout`, and you must route it — an unbounded
  wait stalls forever with nobody to tell. After a resume the deadline restarts from zero. Any
  `retry:` backoff sleeps count against this same clock, so a flaky poller with a long `backoff:`
  eats into the budget you'd otherwise spend actually waiting.
- **`max_visits:`** — bounds the *cycle* that keeps re-entering this step (wait → fix → push → wait),
  separately from `timeout:` which bounds one sojourn. Exceeding it gives the reserved outcome
  `exhausted`; route it somewhere useful (e.g. a `human` gate) or it goes to `blocked`.
- **`postcondition:`** — optional here; the outcome *is* the check (`wait` and `human` are the two
  kinds `pawl validate` exempts).
- **`retry:`** — `{max_attempts, backoff}`. Retries one `poll:` **tick** in place — before any
  outcome is resolved — when that tick *hard*-fails: the poller exits non-zero, hits the
  engine-wide wall-clock ceiling, or a *routed* tick's payload fails to parse. An unrouted ("not
  yet") tick is not a hard failure at all — it's `wait`'s ordinary tick and never touches `retry:`'s
  counter, so it is never retried and never counts toward `max_attempts:`. `max_attempts:` counts
  *consecutive* hard-failure ticks; a "not yet" tick in between resets that streak back to zero
  (any other clean, routed tick ends the wait outright, so there is nothing left to reset). Retry
  *n* sleeps `backoff × n` before the next tick — see `timeout:` above for how that sleep
  interacts with the wait's own deadline. This is a different layer from `catch:`, which decides
  where to go only once `retry:`'s tries (if any) are exhausted and the tick is still a hard
  failure. An exit-0 poller that prints the reserved `failure` token is **not** retried — like a
  non-zero exit without `retry:` declared, it routes straight to `catch:` as a clean (if failing)
  tick, never as a hard failure.

## Example

```yaml
  - id: wait_for_mr
    kind: wait
    poll: scripts/refresh.sh ${project_path} ${mr_iid}
    every: 30s
    timeout: 6h
    emits: pairs
    writes: {comment_count: {type: integer}}
    outcomes:
      CI_FAILED: fix_issues
      COMMENTS:  address_comments
      CLEAN:     collect_reviewers
      API_ERROR: wait_for_mr
      timeout:   blocked
```

The engine prints `WAIT 7f3a wait_for_mr`; the session runs `pawl poll --run 7f3a --step wait_for_mr`
in the background. **`pawl poll` submits its own result** the moment a poll iteration's last line
carries a routed token, or on `timeout:` — the model never runs `pawl submit` for a `wait` step. The
session is free meanwhile (no token cost while polling); if it dies, `pawl run <name>` resumes at the
same step and restarts the poller.

## Outcomes

Named by the poller's printed token, same as [deterministic](deterministic.md). Routing a token back
to the same step (`API_ERROR: wait_for_mr` above) is the idiom for "noise, keep waiting" while still
journalling it — give the step `max_visits:` if you do this, so a permanently broken API doesn't loop
forever.

## Common mistakes

- Treating a non-zero poller exit as "not yet" — it's a hard failure (retried under `retry:` if
  declared, otherwise routed straight to `catch:` as `failure`), never "keep waiting". Exit 0 and
  print nothing instead.
- Printing progress on the last line instead of the token — only the last non-empty line is read.
- No `max_visits:` on a self-routing "keep waiting" token — nothing then stops a broken API looping.
- An expensive poller — it runs every `every:` for hours; keep it to one cheap call.
- Forgetting `${key}`-derived signatures must be computed the same way across scripts, or "the same
  failure" won't mean the same thing at both call sites.

Next: [human steps](human.md).
