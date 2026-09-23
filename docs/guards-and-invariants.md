# Guards and invariants

Two enforcement layers, with different strength.

- A **guard** denies a command *before* it runs, by matching its spelling.
- An **invariant** runs a command *after* every step and asks the world whether things are still
  true.

Use both on anything that matters.

## Guards

```yaml
guards:
  - id: publish-only-in-commit-step
    match: "git push .*|gh pr create .*"
    only_in: [commit_and_pr]
```

`match:` is a POSIX extended regular expression over the Bash command string. `${key}` does not work
here — guards are static, checkable by the validator without running anything.

`only_in:` lists the steps where the pattern is allowed — `[commit_and_pr]` allows it there, denies
elsewhere; `[]` (empty) denies it for the whole run: "never do this by hand".

A denied call does not run:

```
PreToolUse: denied by pawl guard `merge-only-in-merge-step` (run 7f3a, step wait_for_ci).
`gh pr merge` may only run in step merge_pr.
```

## What "advisory" actually means

The run banner says so on purpose:

```
  hooks: PreToolUse ✔  Stop ✔   guards: 2 advisory (pattern-matched)  invariants: 1
```

A guard matches a string. Variable indirection (`CMD="gh pr merge"; $CMD 41`), a decoded command, or
a renamed binary all reach the same effect without matching. Guards catch the canonical spelling
cheaply and before the fact; they do not catch a determined workaround.

## Invariants

```yaml
invariants:
  - id: draft-until-assigned
    check: scripts/check-draft-invariant.sh ${project_path} ${mr_iid} ${reviewers_approved}
    message: "The MR was un-drafted or given reviewers before any were approved."
```

The engine evaluates every invariant after every step and after every `pawl submit`, `${key}`
substituted.

Exit 0 holds, non-zero is violated, and cannot run at all (missing script, unparseable output,
network error) also counts as **violated** — write your check so a transient failure is not
catastrophic, or do not make it an invariant.

A violation pauses the run:

```
  TERMINAL 7f3a blocked
  Paused for review: invariant `draft-until-assigned` violated — the MR was un-drafted or
  given reviewers before any were approved.
```

The reason goes into the journal and `${blocked_reason}`, for your `blocked` terminal message.
Something unexpected happened; the run is paused for review before continuing — see
[running.md#blocked](running.md#blocked).

## Which to use

| You want to… | Use |
|---|---|
| stop a risky command in the wrong step | guard |
| notice it happened anyway | invariant |
| enforce "only this step may push" | both |
| check something remote, or free and local | invariant |
| document a rule for readers | guard |

`manage-mr` uses both for one rule: guards on `glab mr update --reviewers`, and an invariant asking
GitLab whether the MR is still a draft — the guard catches the mistake, the invariant the
consequence. Invariants run after every step: keep them to a handful, and fast.

## The residual holes

- **Indirection defeats guards** — the invariant on the same rule is the backstop, for effects that
  leave a trace.
- **No trace, nothing catches it** — e.g. "posted a comment then deleted it".
- **Nothing is rolled back.** An invariant tells you the MR was un-drafted; it cannot re-draft it.
- **Two live runs in one working copy share one guard table, permissively** — `PreToolUse` denies a
  command only if *no* live run's active step permits it.
- **Guards only see Bash** — an Edit-tool write fires no `match:`, and `subagent_args.tools` is
  guidance to the subagent, not an enforced allowlist ([agentic steps](steps/agentic.md)). The one
  subagent rule the `PreToolUse` hook actually keeps is denying VCS-mutating `Bash` while an agentic
  step is live.
- **A harness that drops a hook mid-run** is outside the engine's reach — the self-test is at start,
  not for the duration.
- **A script that lies** — printing `CLEAN` without checking is indistinguishable from checking. The
  trust boundary is your script.

Related: [validation.md](validation.md), [troubleshooting.md](troubleshooting.md).
