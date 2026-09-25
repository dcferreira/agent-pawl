# Troubleshooting

## Hooks not live

`pawl run` refuses to start unless `pawl hook pre` has written a fresh (≤5 minutes old) heartbeat for this
working copy — its way of confirming the `PreToolUse` hook is actually wired up, since it has no other way
to ask Claude Code that directly:

```
pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.
Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.
```

Check: the plugin is installed and enabled; you restarted the session after installing it (hooks bind
at session start); the plugin's `hooks/hooks.json` exists and wires `PreToolUse`/`Stop` to
`bin/pawl-hook`. A `brew`- or `go`-installed `pawl` alone always fails this check — it ships no hooks;
either install the plugin (see [install.md#hooks](install.md#hooks)) or add the equivalent
`PreToolUse`/`Stop` entries to your own `settings.json`. If you don't want the check at all, pass
`--no-enforcement` to `pawl run`, or set `PAWL_ENFORCEMENT=off`.

This check gates only a fresh start. Resuming an existing run needs no heartbeat: an enforced run
resumes from a plain terminal with the banner
`enforcement: on (bound at run start; no hook heartbeat for this resume)` and stays enforced; its mode
was fixed when it started, so `--no-enforcement` on that resume is refused rather than needed.

## "submit refused: step not current"

```
pawl: submit refused: run 7f3a is at wait_for_mr (attempt 1), not fix_issues.
```

A result was reported for a step the engine isn't waiting on — two sessions driving one run, a stale
poller, or a retry of an old tool call. Nothing was applied. Run `pawl status --run 7f3a`, look at
`next`, and do that.

## A WAIT that never ends

`pawl status` says `waiting` and nothing moves. Run your poller by hand. Three causes: no routed token
on the last line (only the last non-empty line is read); the token isn't in `outcomes:` (check
case — `Success` isn't `SUCCESS`); the poller exits non-zero (that's `failure` via `catch:` — unless
the step declares `retry:`, in which case a non-zero exit is instead a hard failure retried in-place
first, per `docs/steps/wait.md`'s `retry:` section — not "keep waiting" either way; exit 0, print
nothing instead). If the poll process died, `pawl run <name>` re-enters the step and restarts it; the
`timeout:` deadline restarts too.

## Lock held

```
pawl: run 7f3a is locked by pid 48122 (alive). Wait, or use --force if that process is gone.
```

One process at a time per run. Let a genuinely live holder finish. A dead pid (`ps 48122` shows
nothing) — `--force` steals it. Never `--force` into a run another session is driving.

## BLOCKED — what to do

1. `pawl status --run 7f3a` — where it stopped, what state, which check failed.
2. Fix the world by hand — the engine changed nothing and undoes nothing.
3. `pawl run <name>` (`--run 7f3a` if more than one resolves) to resume at the step that blocked,
   `attempts:` reset to 1, journalled as an intervention. See
   [running.md#blocked](running.md#blocked).
4. Or `pawl abandon --run 7f3a` to end it for good instead.

If the reason is a postcondition you cannot satisfy, the fix is usually in the workflow, not the
world — see [validation.md](validation.md) rule 6. An invariant that "cannot run at all" also counts
as violated.

## The workflow file changed mid-run

```
pawl: ship-change.yaml changed since run 7f3a started (steps fix_issues, assign).
    Use --fresh to start over, or restore the file.
```

The run holds a digest of the compiled graph; resuming against an edited file is refused, naming the
changed steps. `git stash`/restore and resume, or `pawl run <name> --fresh`. Steps that already ran are
not undone either way, and `pawl validate` on the edited file is free.

## A hook error: "pawl-hook: … exited 2; failing open"

The plugin (`bin/pawl-hook`) is newer than the `pawl` binary on your `PATH`: a binary built before
`pawl hook` existed prints its usage and exits 2 for the unknown subcommand. The wrapper treats that —
and any other non-zero exit — as a non-blocking error, so nothing is denied or blocked, but nothing is
enforced either and `pawl run` refuses to start for lack of a heartbeat. Update the binary (`pawl
update`, or `go install github.com/dcferreira/agent-pawl/cmd/pawl@latest`).

## The run is fine but the session ended

`pawl`'s `Stop` hook only refuses ending the turn for the session **driving** a run — the one
whose `pawl run`/`submit`/`poll` last stamped `driver.json` with a fresh heartbeat — and only while
that run's cursor is at an `agentic`/`parallel` step awaiting `pawl submit` (`wait`/`human` cursors
and `BLOCKED` runs are exempt, since ending the turn there is legitimate — and so is ending the turn
with a `background_tasks` or `session_crons` entry still in the Stop payload, since Claude Code will
wake the session back up: that's the case where you dispatched the agentic step to a background
subagent, or are polling a `wait` step under Monitor, and paused rather than walked away). It prints:

```
pawl run <id> (<workflow>) is at step <step> awaiting `pawl submit`. Finish it, or: pawl abandon --run <id>
```

It refuses at most once per turn (Claude Code force-ends the turn after 8 consecutive blocks
regardless). If enforcement is off (`--no-enforcement`/`PAWL_ENFORCEMENT=off`), or the hooks aren't
wired up at all, nothing stops a session from ending mid-run. If the session was killed anyway,
nothing is lost: `pawl run <name>` resumes from the journal. `pawl abandon --run <id>` always releases
a run's `Stop` block if you're genuinely done with it.

## Enforcement looks off

Compare `root` in `pawl status` against the tree you think you're in — guards, the subagent VCS rule
and `Stop` all resolve the working-copy root the same way (`journal.ResolveRoot`), and a git worktree
or jj workspace is its own root with its own runs. If a guard or the `Stop` block isn't firing when
you expect it to: confirm the banner said `hooks: PreToolUse ✔` at `pawl run` time, not
`enforcement: off (...)`; guards are advisory string matches over the Bash command
(`$()`, a variable, or a renamed binary all evade them — see
[guards-and-invariants.md](guards-and-invariants.md)); and the subagent VCS rule only fires when the
payload carries `agent_id` (a subagent call), not the main session's own commands.

## Every Bash call takes the slow path (or shows an error if `pawl` isn't installed)

A forgotten live run — including a `BLOCKED` one, which is still live — or a dangling `live/` link
left behind by a deleted checkout makes `bin/pawl-hook`'s fast path think a run is live in every
session, so it hands every Bash call's payload to the real `pawl` binary instead of exiting 0
immediately. If `pawl` itself isn't installed (only the plugin's wrapper scripts are), this also
surfaces as a non-blocking "the `pawl` binary is not installed" error on Bash calls that have nothing
to do with `pawl`. Fix it with `pawl abandon --run <id>` in the checkout that started the run, or by
removing the stale entries directly under `~/.claude/pawl/live/`.

## `pawl run` refuses to start from a different directory than the session is "in"

The heartbeat `pawl hook pre` writes comes from the hook payload's `cwd` — the working directory the
Bash tool actually ran the command in — not from wherever the session's own state says it's sitting.
A session that has `cd`'d elsewhere across several prior tool calls, then runs `pawl` with a leading
`cd` back to the repo in the *same* Bash call, still has the heartbeat land on the wrong working copy:
the hook payload's `cwd` is the shell's starting directory for that call, before the `cd` inside it
takes effect. Run `pawl` from inside the repo directly, without a leading `cd`, when in doubt.

## Workflow not found, or run not visible, outside a VCS-tracked directory

```
pawl: no workflow named "manage-mr" found under .claude/workflows/ (searched /home/ada/src/app/sub
    up to working-copy root /home/ada/src/app/sub) or ~/.claude/workflows/
```

or `pawl list` prints nothing, or `pawl status` can't find a run you know is active — but everything
works fine one directory up.

The working-copy root is found by walking up from cwd looking for a `.git` or `.jj` marker. If a
directory has neither anywhere above it, there's nothing to find, and root falls back to cwd itself.
That means an unversioned directory has no stable root: every subdirectory of it *is* its own root,
not a shared ancestor. `pawl` only looks for `.claude/workflows/` between cwd and root, so from a
subdirectory the walk stops immediately and finds nothing; run state is namespaced under root the same
way, so a run started at the top of such a directory is invisible from a subdirectory too — they're
different roots, not different views of the same one.

Workaround: run `pawl` from the directory that contains `.claude/`, not a subdirectory of it — or put
that directory under version control. `git init` with no commits is enough; only the `.git` marker's
presence is checked.
