# Running a workflow

## Starting

In a session, type `/wf run manage-mr …` — the model runs `wf run`, not you. `/wf` is a skill the
model interprets, so the wording is loose: `/wf run manage-mr for MR 41` works as well as the exact
form — the model translates it to `wf run manage-mr key=value …`. Exact `key=value` is what the
*engine* needs; translating is the model's job. Typed directly in a terminal, it's the same with no
model reading it: a deterministic-only workflow runs to completion; one with agentic/human/wait steps
prints the first `DISPATCH`/`ASK`/`WAIT` line and exits (inspect only). Either way:

```
› wf run manage-mr mr_url_arg=https://gitlab/x/y/-/merge_requests/41
  run 7f3a  manage-mr  .claude/workflows/manage-mr.yaml
  hooks: PreToolUse ✔  Stop ✔   guards: 4 advisory (pattern-matched)  invariants: 2
```

Arguments are `key=value`, declared in `args:`; a missing required one refuses to start and prints
the usage. Line 1: run id, workflow, resolved file. Line 2: the enforcement self-test — `wf` refuses
to start if either hook doesn't answer (see
[troubleshooting](troubleshooting.md#hooks-not-live)); [guards are advisory, invariants
hold](guards-and-invariants.md).

## Step lines

```
  ✔ preflight → wait_for_mr   FRESH  branch=feat/x
  ↻ fix_tests  attempt 2/4  postcondition failed: 3 tests still failing
```

`✔` postcondition passed — `~` same, `soft: true` — `↻` re-run under `attempts:`, failure text
carried forward — `✗` failed, routed via `catch:` (default `blocked`).

## DISPATCH, ASK and WAIT

At a step it can't run itself, the engine prints one line and exits — the `/wf` skill's handshake.

```
  DISPATCH 7f3a fix_issues
    description: "Fix every issue in ${findings}. …"   context: […]   return: {findings: json}
    subagent_args: {tools: [Read, Edit, Bash(scripts/verify.sh)], model: sonnet}
```

`DISPATCH` ([agentic](steps/agentic.md)): `wf` prints `description:`, `context:`, `writes:`,
`subagent_args:` as given; the session composes the prompt, honours `subagent_args:`, and reports
with `wf submit --json '…'`.

`ASK 7f3a choose_reviewers` ([human](steps/human.md)): asks via `AskUserQuestion` — options plus
"Other" free-text.

`WAIT 7f3a wait_for_mr` ([wait](steps/wait.md)): the session starts `wf poll` in the background — it
**submits its own result**, never `wf submit`.

Every `wf submit`/finished `wf poll` prints the next line, until `TERMINAL 7f3a ok`, the terminal
`message:`, then `N of M advanced on a soft postcondition`.

## `wf status`

```
› wf status
run 7f3a  manage-mr  waiting     step wait_for_mr (wait)  attempt 1/1  visits 2/8
  root     /home/ada/src/app
  next     wf poll --run 7f3a --step wait_for_mr
```

Status word: `running`, `dispatched`, `asking`, `waiting`, `blocked`, `ok`, `abandoned`. `root` is
worth checking if enforcement looks wrong; `--json` prints the same fields.

Runs are keyed by working-copy root, not session: several may be live in one repo, but two on one
tree collide — separate worktrees/jj workspaces are separate roots.

## Resume after a crash

Close the terminal, reboot — the run is on disk.

```
› wf run manage-mr
  run 7f3a  manage-mr  resumed at fix_issues (attempt 2/3)
```

A non-terminal run for this working copy resumes. Steps before the cursor are not re-run; the
interrupted step *is*, noted in `DISPATCH` — a crash never consumes an attempt, and nothing rolls
back.

`--fresh` starts a new run, resetting every counter; `wf run` refuses to resume if the workflow file
changed. `wf abandon --run 7f3a [--reason <text>]` ends a run for good, any time.

## BLOCKED

`BLOCKED` means something unexpected happened; the run is paused for review before continuing — not
dead. Three causes: a postcondition failed with no attempts left and no `catch:`; an invariant
violated; or a step exhausted `max_visits:`/`max_steps:` unrouted.

```
  TERMINAL 7f3a blocked
  Paused for review: invariant `draft-until-assigned` violated — the MR was un-drafted or
  given reviewers before a reviewer-decision step approved any.
```

It releases `Stop` — the session is yours again; guards still deny, the run is still live.

**What to do:** read the reason (`wf status --run 7f3a`), fix the world by hand, then `wf run
manage-mr` to resume at the blocked step (`attempts:` reset to 1) — or `wf abandon --run 7f3a` to end
it for good. You can also ask the model to investigate and propose a fix first — resuming is still
your call. Resuming elsewhere (`--from <step>`) isn't yet available — see
[README.md#not-yet](README.md#not-yet).
