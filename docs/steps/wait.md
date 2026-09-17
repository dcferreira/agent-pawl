# `wait` steps

Wait on something outside the run: CI, a review, a build, a person doing a thing elsewhere. See
[step-types.md](../step-types.md) for how the four kinds compare.

## Fields

- **`poll:`** — the command, run every `every:` (default `60s`). Same stdout contract as
  [deterministic](deterministic.md): last non-empty line, optional token, payload
  per `emits:`. An unrouted token, or none, means "not yet" — print nothing useful and exit 0. A
  non-zero exit is a `failure`, routed via `catch:`, not "keep waiting".
- **`timeout:`** — required. On expiry the outcome is `timeout`, and you must route it — an unbounded
  wait stalls forever with nobody to tell. After a resume the deadline restarts from zero.
- **`max_visits:`** — bounds the *cycle* that keeps re-entering this step (wait → fix → push → wait),
  separately from `timeout:` which bounds one sojourn. Exceeding it gives the reserved outcome
  `exhausted`; route it somewhere useful (e.g. a `human` gate) or it goes to `blocked`.
- **`postcondition:`** — optional here; the outcome *is* the check (`wait` and `human` are the two
  kinds `wf validate` exempts).

## Example

```yaml
  - id: wait_for_mr
    kind: wait
    poll: scripts/refresh.sh "${project_path}" "${mr_iid}"
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

The engine prints `WAIT 7f3a wait_for_mr`; the session runs `wf poll --run 7f3a --step wait_for_mr`
in the background. **`wf poll` submits its own result** the moment a poll iteration's last line
carries a routed token, or on `timeout:` — the model never runs `wf submit` for a `wait` step. The
session is free meanwhile (no token cost while polling); if it dies, `wf run <name>` resumes at the
same step and restarts the poller.

## Outcomes

Named by the poller's printed token, same as [deterministic](deterministic.md). Routing a token back
to the same step (`API_ERROR: wait_for_mr` above) is the idiom for "noise, keep waiting" while still
journalling it — give the step `max_visits:` if you do this, so a permanently broken API doesn't loop
forever.

## Common mistakes

- Treating a non-zero poller exit as "not yet" — it's a `failure`. Exit 0 and print nothing instead.
- Printing progress on the last line instead of the token — only the last non-empty line is read.
- No `max_visits:` on a self-routing "keep waiting" token — nothing then stops a broken API looping.
- An expensive poller — it runs every `every:` for hours; keep it to one cheap call.
- Forgetting `${key}`-derived signatures must be computed the same way across scripts, or "the same
  failure" won't mean the same thing at both call sites.

Next: [human steps](human.md).
