# Troubleshooting

## Hooks not live

```
pawl: Stop hook did not respond. Refusing to start.
    Install the plugin (`/plugin install pawl@…`) or copy the /pawl skill into ~/.claude/skills/pawl/.
```

`pawl run` pings both hooks at start and refuses if either is missing. Check: the plugin is installed
and enabled; you restarted the session after installing it; `~/.claude/plugins/pawl/hooks/hooks.json`
exists. A `brew`- or `go`-installed `pawl` alone always fails this check — it ships no hooks.

## Version mismatch

```
pawl: binary 0.4.2 does not match plugin pin 0.5.0. Run `/pawl` to fetch the pinned build,
    or `brew upgrade pawl`.
```

The skill text, hook payloads, and engine change together, so a mismatch is refused. A `PATH` `pawl`
shadowing the plugin needs upgrading to the pin, or removing.

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
case — `Success` isn't `SUCCESS`); the poller exits non-zero (that's `failure` via `catch:`, not
"keep waiting" — exit 0, print nothing instead). If the poll process died, `pawl run <name>` re-enters
the step and restarts it; the `timeout:` deadline restarts too.

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

## The run is fine but the session ended

The `Stop` hook exits 2 while a run is non-terminal, so Claude Code won't end the turn — it prints
`pawl abandon --run <id>`. If the session was killed anyway, nothing is lost: `pawl run <name>` resumes
from the journal.

## Enforcement looks off

Compare `root` in `pawl status` against the tree you think you're in — guards and `Stop` resolve the
working-copy root the same way, and a git worktree or jj workspace is its own root with its own runs.

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
