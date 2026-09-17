# Troubleshooting

## Hooks not live

```
wf: Stop hook did not respond. Refusing to start.
    Install the plugin (`/plugin install wf@…`) or copy the /wf skill into ~/.claude/skills/wf/.
```

`wf run` pings both hooks at start and refuses if either is missing. Check: the plugin is installed
and enabled; you restarted the session after installing it; `~/.claude/plugins/wf/hooks/hooks.json`
exists. A `brew`- or `go`-installed `wf` alone always fails this check — it ships no hooks.

## Version mismatch

```
wf: binary 0.4.2 does not match plugin pin 0.5.0. Run `/wf` to fetch the pinned build,
    or `brew upgrade wf`.
```

The skill text, hook payloads, and engine change together, so a mismatch is refused. A `PATH` `wf`
shadowing the plugin needs upgrading to the pin, or removing.

## "submit refused: step not current"

```
wf: submit refused: run 7f3a is at wait_for_mr (attempt 1), not fix_issues.
```

A result was reported for a step the engine isn't waiting on — two sessions driving one run, a stale
poller, or a retry of an old tool call. Nothing was applied. Run `wf status --run 7f3a`, look at
`next`, and do that.

## A WAIT that never ends

`wf status` says `waiting` and nothing moves. Run your poller by hand. Three causes: no routed token
on the last line (only the last non-empty line is read); the token isn't in `outcomes:` (check
case — `Success` isn't `SUCCESS`); the poller exits non-zero (that's `failure` via `catch:`, not
"keep waiting" — exit 0, print nothing instead). If the poll process died, `wf run <name>` re-enters
the step and restarts it; the `timeout:` deadline restarts too.

## Lock held

```
wf: run 7f3a is locked by pid 48122 (alive). Wait, or use --force if that process is gone.
```

One process at a time per run. Let a genuinely live holder finish. A dead pid (`ps 48122` shows
nothing) — `--force` steals it. Never `--force` into a run another session is driving.

## BLOCKED — what to do

1. `wf status --run 7f3a` — where it stopped, what state, which check failed.
2. Fix the world by hand — the engine changed nothing and undoes nothing.
3. `wf run <name>` (`--run 7f3a` if more than one resolves) to resume at the step that blocked,
   `attempts:` reset to 1, journalled as an intervention. See
   [running.md#blocked](running.md#blocked).
4. Or `wf abandon --run 7f3a` to end it for good instead.

If the reason is a postcondition you cannot satisfy, the fix is usually in the workflow, not the
world — see [validation.md](validation.md) rule 6. An invariant that "cannot run at all" also counts
as violated.

## The workflow file changed mid-run

```
wf: ship-change.yaml changed since run 7f3a started (steps fix_issues, assign).
    Use --fresh to start over, or restore the file.
```

The run holds a digest of the compiled graph; resuming against an edited file is refused, naming the
changed steps. `git stash`/restore and resume, or `wf run <name> --fresh`. Steps that already ran are
not undone either way, and `wf validate` on the edited file is free.

## The run is fine but the session ended

The `Stop` hook exits 2 while a run is non-terminal, so Claude Code won't end the turn — it prints
`wf abandon --run <id>`. If the session was killed anyway, nothing is lost: `wf run <name>` resumes
from the journal.

## Enforcement looks off

Compare `root` in `wf status` against the tree you think you're in — guards and `Stop` resolve the
working-copy root the same way, and a jj workspace or git worktree is its own root with its own runs.
