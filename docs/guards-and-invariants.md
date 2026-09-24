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

`match:` is a regexp over the Bash command string — Go's `regexp` package, RE2 syntax, close to
POSIX ERE but with no backreferences — matched unanchored anywhere in the command. `${key}` does not
work here — guards are static, checkable by the validator without running anything. The validator
(rule 15) checks `id:`, `match:` and `only_in:`; the `PreToolUse` denial this section describes is
real in this build (`internal/hook`, wired via the plugin's `hooks/hooks.json`), advisory and
pattern-matched — see [install.md#hooks](install.md#hooks) for wiring the hooks up.

`only_in:` lists the steps where the pattern is allowed — `[commit_and_pr]` allows it there, denies
elsewhere; `[]` (empty) denies it for the whole run: "never do this by hand".

"Active" means the step the run's cursor is on, plus — while a `kind: parallel` step fans out — each
of its branches that hasn't resolved yet. The cursor stays parked on the `parallel` step itself for
the whole fan-out, so the `parallel` step's own id counts as active until the join transitions:
`only_in: [p]`, where `p` is a `parallel` step, permits the pattern in every branch for the entire
fan-out. `only_in: [some_branch]` permits it only while that branch is still outstanding (not yet
resolved).

### Multi-line commands

Before matching, a `\` immediately followed by a newline (`\n` or `\r\n`) — a shell line
continuation — is deleted entirely: nothing is inserted in its place, so a command split mid-word
across lines with a trailing backslash is rejoined into the same word it would run as. `\` + `\n` is
what POSIX sh itself joins when it splices a continued line; `\` + `\r\n` is deleted the same way,
but that half is pawl's own allowance for CRLF-terminated input, not something a real shell does —
the CR in `\` + CR + LF is an ordinary character to POSIX sh, so it does not treat the pair as a
continuation:

```
git pu\
sh origin
```

matches `match: "git push"` exactly like `git push origin` would — even though `git push` does not
appear anywhere in the un-joined text above (it reads "pu", then a newline, then "sh"). This is a
normalisation of the command text `internal/guard.Table.Denied` matches against, not a change to the
regexp's flags — `.` still does not match a literal `\n`, and `^`/`$` still anchor only at the
start/end of the whole string, unless the pattern itself sets `(?s)`/`(?m)` (RE2 inline flags, which
an author's `match:` may use). A bare newline with nothing before it isn't touched: two commands
separated by a plain newline (no `&&`, no trailing `\`) still each get their own chance to match,
since an unanchored `match:` finds a hit on whichever line it lands on — only `.` and the anchors
treat `\n` specially, and this normalisation doesn't change that.

This is a syntactic join, not a real shell parse, and that is a known imprecision: the matcher does
not track quoting, so a backslash-newline inside single quotes, a quoted heredoc body, or after an
escaped backslash is deleted as if it were a continuation, even though a real shell would not join
the line in any of those cases (and, as above, a real shell would not join a backslash-CRLF pair at
all — deleting that pair is pawl's own CRLF-input allowance, not something the matcher is mimicking
from POSIX sh). For the escaped-backslash case, `echo a\\` + newline + `git push` is,
to a real shell, one command ending in a literal backslash followed by a second command on the next
line — but this matcher can't tell an escaped backslash from an unescaped one, and deletes that
newline anyway. The same applies inside single quotes and quoted heredocs, e.g. `echo 'git pu\` +
newline + `sh'` is joined into `git push` even though it's a single-quoted literal. Guards match the
canonical spelling of a command and nothing else; this is one more way a `match:` can be defeated (or
given a surprise hit) by someone constructing the command text specifically to exploit it.

A denied call does not run:

```
PreToolUse: denied by pawl guard `merge-only-in-merge-step` (run 7f3a, step wait_for_ci).
`gh pr merge` may only run in step merge_pr.
```

## What "advisory" actually means

The run banner says so on purpose:

```
  hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
  guards: 2 advisory (pattern-matched)
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

For a `kind: parallel` step, invariants are checked once, when the whole group joins — not after
each branch's own `pawl submit`. Branches in a parallel group are dispatched all at once and their
join is all-or-nothing: checking per branch would mean the first branch to submit could trip an
invariant and block the run while sibling branches were still outstanding and already dispatched —
their own eventual submits would then land against a run the engine had already ended. Waiting for
the join keeps the whole group's side effects accounted for before an invariant gets a say, exactly
like any other step.

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
- **Two live runs in one working copy share one guard table, permissively** — `PreToolUse` lets a
  guard's deny stand only if *no* live run's active step permits a guard with the same `match:`.
- **Guards are scoped to the working copy of the Bash call's cwd** — a command run after `cd`-ing
  elsewhere, or aimed elsewhere (`git -C /repo push`), isn't checked against this working copy's runs.
- **Guards only see Bash** — an Edit-tool write fires no `match:`, and `subagent_args.tools` is
  guidance to the subagent, not an enforced allowlist ([agentic steps](steps/agentic.md)). The one
  subagent rule the `PreToolUse` hook actually keeps is denying VCS-mutating `Bash` from a subagent
  while any run is live, not only during an agentic step.
- **A harness that drops a hook mid-run** is outside the engine's reach — the heartbeat check is at start,
  not for the duration.
- **A script that lies** — printing `CLEAN` without checking is indistinguishable from checking. The
  trust boundary is your script.

Related: [validation.md](validation.md), [troubleshooting.md](troubleshooting.md).
