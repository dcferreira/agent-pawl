# Running a workflow

## Starting

In a session, type `/pawl run green-tests …` — the model runs `pawl run`, not you. `/pawl` is a skill the
model interprets, so the wording is loose: `/pawl run green-tests with the default commands` works as
well as the exact form — the model translates it to `pawl run green-tests key=value …`. Exact
`key=value` is what the *engine* needs; translating is the model's job. Typed directly in a terminal,
it's the same with no model reading it: a deterministic-only workflow runs to completion; one with
agentic/human/wait steps prints the first `DISPATCH`/`ASK`/`WAIT` block and exits (inspect only).

The blocks on this page are what `pawl` prints to the session, which the skill reads and acts on.
They come from [`docs/examples/green-tests`](dogfood.md) (a failing test suite and one agentic
step that fixes it), run in a session whose plugin hooks are installed:

<!-- pawl-capture: running/run -->
```
$ pawl run green-tests
workflow: /home/you/project/.claude/workflows/green-tests.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
soft: 0/2 steps (0.0%): (none)
DISPATCH 7f3a fix_tests
attempt: 1 of 3
description:
  The test suite is failing. Fix the code so that it passes.
  Do not edit, weaken or delete tests to make them pass — fix the code under test.
  The failures:
  --- FAIL: TestAdd (0.00s)
      fixture_test.go:7: Add(2, 3) = -1, want 5
  FAIL
  FAIL	fixture	0.00s
  FAIL
context:
  [1] git diff (0 bytes)
    (empty)
return: a JSON object with exactly these keys (key order does not matter)
  fix_summary: string
subagent_args: {"model":"sonnet","tools":["Read","Edit","Bash"]}
submit with: pawl submit --run 7f3a --step fix_tests --json '<the object above>'
END DISPATCH 7f3a fix_tests
```

Arguments are `key=value`, declared in `args:`; a missing required one refuses to start and prints
the usage. The lines before `DISPATCH`, the banner:

- `workflow:` — the resolved file and where it was found.
- `hooks:` — the enforcement banner. `pawl` refuses to start a new run unless a fresh `PreToolUse`
  heartbeat exists for this session or, failing that, this working copy (a resume doesn't need one —
  see below); `Stop` is assumed installed from the same `hooks.json`, not independently checked (see
  [troubleshooting](troubleshooting.md#hooks-not-live)). With enforcement off it reads
  `enforcement: off (--no-enforcement)` instead.
- `guards: N advisory (pattern-matched)` and `invariants: N (engine-checked after every step)` —
  printed only when the workflow declares any; [guards are advisory, invariants
  hold](guards-and-invariants.md).
- `soft:` — the soft census.

## What is printed, and when

`pawl run` does not narrate steps. A `deterministic` step runs silently and the engine moves on; the
only output after the banner is the point where the run stops and needs something from the session —
one block per stop:

- `DISPATCH`, `ASK`, `WAIT`: the run is parked and a session must act (below).
- `TERMINAL`: the run is over (`ok`) or paused for review (`blocked`).

A retry shows up inside the next block, not as a line of its own: a re-dispatched agentic step prints
`attempt: 2 of 3` and a `previous attempt failed` section with the postcondition's output; a loop back
through a deterministic step shows as that step's counter in `pawl status` (`visits: …`). A
`parallel` step's branches print `~ branch <id> recorded (parallel <step>: waiting on: …)` lines as
they are submitted, and a `foreach:` agentic body prints `~ item <step>[N] recorded …`. Nothing
prints a `✔`/`✗` per step, and no "N of M advanced on a soft postcondition" summary: the soft census
is the `soft:` line in the banner.

## DISPATCH, ASK and WAIT

At a step it can't run itself, the engine prints one block, closed by an `END` line with the same run
id and step, and exits — the `/pawl` skill's handshake.

`DISPATCH` ([agentic](steps/agentic.md)) is the block in the run above: `pawl` prints `attempt:`,
`description:`, `context:` (each entry with its byte count — `git diff` is empty above because
the example repository has no uncommitted changes; a `!cmd` that fails is flagged with its exit status), `return:` (the keys of `writes:`),
`subagent_args:` as given, and the exact `pawl submit` line. The session composes the prompt, honours
`subagent_args:`, and reports with `pawl submit --json '…'`.

`ASK` ([human](steps/human.md)) asks via `AskUserQuestion` — the listed options plus "Other"
free-text — and the answer goes back through `pawl submit`:

<!-- pawl-capture: running/ask -->
```
$ pawl run review
workflow: /home/you/project/.claude/workflows/review.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
soft: 0/1 steps (0.0%): (none)
ASK 7f3a choose_reviewer
question:
  Who reviews this change?
options:
  [1] alice
  [2] bob
  [3] skip
multi: false
submit with: pawl submit --run 7f3a --step choose_reviewer --json '{"selected": ["<option label>"]}'
or: pawl submit --run 7f3a --step choose_reviewer --json '{"other": "<free text>"}'
END ASK 7f3a choose_reviewer
```

`WAIT` ([wait](steps/wait.md)): the session starts the printed `pawl poll` in the background — it
**submits its own result**, never `pawl submit`. Each poll prints one line, and the final one prints the
next block:

<!-- pawl-capture: running/wait -->
```
$ pawl run ci
workflow: /home/you/project/.claude/workflows/ci.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
soft: 0/1 steps (0.0%): (none)
WAIT 7f3a wait_for_ci
every: 60s
timeout: 2h
poll with: pawl poll --run 7f3a --step wait_for_ci
END WAIT 7f3a wait_for_ci
$ pawl poll --run 7f3a --step wait_for_ci
poll 1 (0s parked): GREEN → routed outcome GREEN
TERMINAL 7f3a ok
message:
  CI is green.
END TERMINAL 7f3a ok
```

Every `pawl submit`/finished `pawl poll` prints the next block, until `TERMINAL <id> ok` and the
terminal's `message:`.

## `pawl status`

The same run, after its `DISPATCH`:

<!-- pawl-capture: running/status -->
```
$ pawl status
root: /home/you/project
workflow: /home/you/project/.claude/workflows/green-tests.yaml
run: 7f3a
status: running
step: fix_tests
attempt: 1
visits: fix_tests=1, run_tests=1
state: failures
soft: 0/2 steps (0.0%): (none)
```

`visits:` counts how many times each step has been entered, `state:` lists the keys written so far
(names, not values), and `soft:` is the same census as the banner. `status:` is `running` for a live run that hasn't ended yet — that covers every step kind, not a
separate word per kind (there's no `dispatched`/`asking`/`waiting`) — and, once the run has ended,
whatever its `RUN_END` recorded: the terminal's own declared `status:` (normatively `ok` or
`blocked` — see [format-spec.md §B.12](../design/format-spec.md)), or `abandoned` for a run `pawl
abandon` ended. A run pawl cannot replay shows `newer-format` (written by a newer pawl) or `unreadable` (corrupt journal) instead; `pawl run`/`submit`/`poll` refuse it, and `pawl abandon` ends it. A `reason:` line appears only when one was recorded (a `blocked` run's diagnostic,
or an abandon's `--reason`). `root` is worth checking if enforcement looks wrong; `--json` (see
[cli.md](cli.md)) prints the same fields, machine-readably.

Runs are keyed by working-copy root, not session: several may be live in one repo, but two on one
tree collide — separate git worktrees/jj workspaces are separate roots.

That keying assumes a `.git` or `.jj` somewhere above cwd. With neither, root falls back to cwd
itself, so every subdirectory of an unversioned tree is its own root: a run started at the top is
invisible to `pawl status`/`pawl run` from a subdirectory. See
[troubleshooting.md](troubleshooting.md#workflow-not-found-or-run-not-visible-outside-a-vcs-tracked-directory).

## Resume after a crash

Close the terminal, reboot — the run is on disk. Running the same `pawl run` again finds the live run
and resumes it, printing a `resume:` line, then the block for the step it stopped at:

<!-- pawl-capture: running/resume -->
```
$ pawl run green-tests
workflow: /home/you/project/.claude/workflows/green-tests.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
soft: 0/2 steps (0.0%): (none)
resume: run 7f3a step fix_tests attempt 1 restored keys: failures
DISPATCH 7f3a fix_tests
attempt: 1 of 3
description:
  The test suite is failing. Fix the code so that it passes.
  Do not edit, weaken or delete tests to make them pass — fix the code under test.
  The failures:
  --- FAIL: TestAdd (0.00s)
      fixture_test.go:7: Add(2, 3) = -1, want 5
  FAIL
  FAIL	fixture	0.00s
  FAIL
context:
  [1] git diff (0 bytes)
    (empty)
return: a JSON object with exactly these keys (key order does not matter)
  fix_summary: string
subagent_args: {"model":"sonnet","tools":["Read","Edit","Bash"]}
interrupted:
  a previous attempt on this step did not finish (a crash, or a person intervened after a block); inspect current state before acting.
submit with: pawl submit --run 7f3a --step fix_tests --json '<the object above>'
END DISPATCH 7f3a fix_tests
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
violated; or a step exhausted `max_visits:`/`max_steps:` unrouted. The block names the reason in
`message:` and prints the two ways out as exact commands, `resume:` and `abandon:`. An invariant
violation (here `check: test ${reviewers_approved} -gt 0` with nothing approved) looks like this, and
the process exits 3:

<!-- pawl-capture: guards/invariant-blocked -->
```
$ pawl run ship
workflow: /home/you/project/.claude/workflows/ship.yaml (repo-local)
hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)
invariants: 1 (engine-checked after every step)
soft: 0/1 steps (0.0%): (none)
TERMINAL 7f3a blocked
message:
  invariant "draft-until-assigned" violated: The MR was un-drafted or given reviewers before any were approved.
resume:
  pawl run ship --run 7f3a
abandon:
  pawl abandon --run 7f3a --reason '<reason>'
END TERMINAL 7f3a blocked
exit 3
```

It releases `Stop` — the session is yours again; guards still deny, the run is still live.

**What to do:** read the reason (`pawl status`), fix the world by hand, then run the printed `resume:`
command (`pawl run <name> --run <id>`; from a plain terminal is fine) to resume at the blocked step
(`attempts:` reset to 1) — or the printed `abandon:` command to end it for good. You can also ask the model to investigate and propose a fix first — resuming is still
your call. Resuming elsewhere (`--from <step>`) isn't yet available — see
[README.md#not-yet](README.md#not-yet).
