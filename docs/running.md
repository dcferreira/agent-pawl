# Running a workflow

## Starting

In a session, type `/pawl run manage-mr …` — the model runs `pawl run`, not you. `/pawl` is a skill the
model interprets, so the wording is loose: `/pawl run manage-mr for MR 41` works as well as the exact
form — the model translates it to `pawl run manage-mr key=value …`. Exact `key=value` is what the
*engine* needs; translating is the model's job. Typed directly in a terminal, it's the same with no
model reading it: a deterministic-only workflow runs to completion; one with agentic/human/wait steps
prints the first `DISPATCH`/`ASK`/`WAIT` line and exits (inspect only). Either way:

```
› pawl run manage-mr mr_url_arg=https://gitlab/x/y/-/merge_requests/41
  run 7f3a  manage-mr  .claude/workflows/manage-mr.yaml
  hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
  guards: 4 advisory (pattern-matched)
```

Arguments are `key=value`, declared in `args:`; a missing required one refuses to start and prints
the usage. Line 1: run id, workflow, resolved file. Line 2: the enforcement banner — `pawl` refuses
to start a new run unless a fresh `PreToolUse` heartbeat exists for this working copy (a resume
doesn't need one — see below); `Stop` is assumed
installed from the same `hooks.json`, not independently checked (see
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

At a step it can't run itself, the engine prints one line and exits — the `/pawl` skill's handshake.

```
  DISPATCH 7f3a fix_issues
    description: "Fix every issue in ${findings}. …"   context: […]   return: {findings: json}
    subagent_args: {tools: [Read, Edit, Bash(scripts/verify.sh)], model: sonnet}
```

`DISPATCH` ([agentic](steps/agentic.md)): `pawl` prints `description:`, `context:`, `writes:`,
`subagent_args:` as given; the session composes the prompt, honours `subagent_args:`, and reports
with `pawl submit --json '…'`.

`ASK 7f3a choose_reviewers` ([human](steps/human.md)): asks via `AskUserQuestion` — options plus
"Other" free-text.

`WAIT 7f3a wait_for_mr` ([wait](steps/wait.md)): the session starts `pawl poll` in the background — it
**submits its own result**, never `pawl submit`.

Every `pawl submit`/finished `pawl poll` prints the next line, until `TERMINAL 7f3a ok`, the terminal
`message:`, then `N of M advanced on a soft postcondition`.

## `pawl status`

```
› pawl status
root: /home/ada/src/app
workflow: /home/ada/src/app/.claude/workflows/manage-mr.yaml
run: 7f3a
status: running
step: fix_issues
attempt: 1
visits: fix_issues=1
state: branch=feat/x
soft: 0/23 steps (0.0%): (none)
```

`status:` is `running` for a live run that hasn't ended yet — that covers every step kind, not a
separate word per kind (there's no `dispatched`/`asking`/`waiting`) — and, once the run has ended,
whatever its `RUN_END` recorded: the terminal's own declared `status:` (normatively `ok` or
`blocked` — see [format-spec.md §B.12](../design/format-spec.md)), or `abandoned` for a run `pawl
abandon` ended. A `reason:` line appears only when one was recorded (a `blocked` run's diagnostic,
or an abandon's `--reason`). `root` is worth checking if enforcement looks wrong; `--json` (see
[cli.md](cli.md)) prints the same fields, machine-readably.

Runs are keyed by working-copy root, not session: several may be live in one repo, but two on one
tree collide — separate git worktrees/jj workspaces are separate roots.

That keying assumes a `.git` or `.jj` somewhere above cwd. With neither, root falls back to cwd
itself, so every subdirectory of an unversioned tree is its own root: a run started at the top is
invisible to `pawl status`/`pawl run` from a subdirectory. See
[troubleshooting.md](troubleshooting.md#workflow-not-found-or-run-not-visible-outside-a-vcs-tracked-directory).

## Resume after a crash

Close the terminal, reboot — the run is on disk.

```
› pawl run manage-mr
  run 7f3a  manage-mr  resumed at fix_issues (attempt 2/3)
```

A non-terminal run for this working copy resumes. Steps before the cursor are not re-run; the
interrupted step *is*, noted in `DISPATCH` — a crash never consumes an attempt, and nothing rolls
back.

You can resume from a plain terminal: a resume needs no hook heartbeat. An enforced run stays
enforced — its mode was fixed at start, the banner reads
`enforcement: on (bound at run start; no hook heartbeat for this resume)`, and the hooks keep
applying to it. Deterministic steps run and the run stops at the next `DISPATCH`/`ASK`; hand that to
a Claude Code session (whose `pawl run`/`submit` makes it the run's driver) to carry on.

`--fresh` starts a new run, resetting every counter; `pawl run` refuses to resume if the workflow file
changed. `pawl abandon --run 7f3a [--reason <text>]` ends a run for good, any time.

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

**What to do:** read the reason (`pawl status --run 7f3a`), fix the world by hand, then `pawl run
manage-mr` (from a plain terminal is fine) to resume at the blocked step (`attempts:` reset to 1) — or `pawl abandon --run 7f3a` to end
it for good. You can also ask the model to investigate and propose a fix first — resuming is still
your call. Resuming elsewhere (`--from <step>`) isn't yet available — see
[README.md#not-yet](README.md#not-yet).
