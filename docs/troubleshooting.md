# Troubleshooting

## Hooks not live

`pawl run` refuses to start unless `pawl hook pre` has written a fresh (≤5 minutes old) heartbeat for this
working copy — its way of confirming the `PreToolUse` hook is actually wired up, since it has no other way
to ask Claude Code that directly:

```
pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.
Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.
Diagnosis:
  - CLAUDE_CODE_SESSION_ID is not set, so the session-keyed heartbeat (written by pawl >= 0.4.0) cannot be looked up; only this working copy's heartbeat was checked.
  - no heartbeat for working copy /path/to/repo
```

The `Diagnosis:` lines say which lookups failed. `pawl run` first looks up the heartbeat keyed by the
session id in `CLAUDE_CODE_SESSION_ID`, then the one for this working copy; each line reports one:

- `CLAUDE_CODE_SESSION_ID is not set` — the session heartbeat (written by pawl >= 0.4.0) could not be
  looked up, so only the working-copy heartbeat was checked.
- `the PreToolUse hook has not recorded a pawl command for session <id>` — the variable is set but no
  session heartbeat exists. Either the hook is not installed or enabled for this session (install the
  plugin and restart the session), or it runs a pawl older than 0.4.0, which never writes one: run
  `pawl update` for a standalone binary, or `claude plugin update agent-pawl@agent-pawl` if the binary
  is the plugin's bundled one (`pawl update` refuses there), and restart the session.
- `the heartbeat for session <id> is … old; the limit is 5m0s` (or the same for the working copy) — the
  hook did fire, but not within the last 5 minutes.
- `no heartbeat for working copy <path>` — nothing was ever recorded for this directory.

Check: the plugin is installed and enabled; you restarted the session after installing it (hooks bind
at session start); the plugin's `hooks/hooks.json` exists and wires `PreToolUse`/`Stop`/`SubagentStop` to
`bin/pawl-hook`. A `brew`- or `go`-installed `pawl` alone always fails this check — it ships no hooks;
either install the plugin (see [install.md#hooks](install.md#hooks)) or add the equivalent
`PreToolUse`/`Stop`/`SubagentStop` entries to your own `settings.json`. If you don't want the check at all, pass
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

## A hook error: "pawl-hook: … exited N; failing open"

The wrapper treats any non-zero exit from the binary as a non-blocking error, so nothing is denied or
blocked, but nothing is enforced either and `pawl run` refuses to start for lack of a heartbeat. With the
marketplace-installed plugin the binary is bundled (`libexec/<os>_<arch>/pawl`) and always matches the
plugin. A missing or non-executable bundled binary, or an unsupported platform, shows as `exited 1`
(with an "unsupported platform" or "no usable bundled binary" message above it): reinstall the plugin
(`/plugin marketplace update agent-pawl`, `/plugin uninstall agent-pawl@agent-pawl`, then `/plugin
install agent-pawl@agent-pawl`). `exited 2` is the usage error of an older `pawl` on `PATH`, which
you only get in a checkout, with `--plugin-dir`, or with a v0.6.0 plugin installed before the
marketplace gained its archive source, none of which has a `libexec/`, where the plugin
(`bin/pawl-hook`) can be newer than that binary: one built before `pawl hook` existed prints its usage
and exits 2 for the unknown subcommand. Update that binary (`pawl update`, or `go install
github.com/dcferreira/agent-pawl/cmd/pawl@latest`), or, for the v0.6.0 install, reinstall the
plugin as above, or update it once the next release is out (see "Migrating from an earlier install" in [install.md](install.md)).

## The run is fine but the session ended

`pawl`'s `Stop` hook only refuses ending the turn for the session **driving** a run — the one
whose `pawl run`/`submit`/`poll` last stamped `driver.json` with a fresh heartbeat — and only while
that run's cursor is at an `agentic`/`parallel` step awaiting `pawl submit` (`wait`/`human` cursors
and `BLOCKED` runs are exempt, since ending the turn there is legitimate — and so is ending the turn
with a `background_tasks` or `session_crons` entry still in the Stop payload, since Claude Code will
wake the session back up: that's the case where you dispatched the agentic step to a background
subagent, or are polling a `wait` step under Monitor, and paused rather than walked away — and so is a Stop within 60s of a subagent finishing, once, via the
`SubagentStop` hook's marker). It prints:

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
immediately. If `pawl` itself isn't available, this surfaces as a non-blocking error on Bash calls that
have nothing to do with `pawl`: "the `pawl` binary was not found" (a checkout, `--plugin-dir`, or a v0.6.0
install with no `libexec/` bundled binary and no `pawl` on `PATH`), or "unsupported platform" / "the agent-pawl plugin has no usable bundled
binary" (an installed release plugin; see the "exited N; failing open" section above). Fix it with `pawl abandon --run <id>` in the checkout that started the run, or by
removing the stale entries directly under `~/.claude/pawl/live/`.

## `pawl run` refuses to start from a different directory than the session is "in"

The per-working-copy heartbeat `pawl hook pre` writes is keyed by the hook payload's `cwd`, which in
Claude Code is always the directory the *session started in*, whatever a `cd` in the Bash command does.
So `pawl hook pre` also writes a heartbeat keyed by session id, and `pawl run`/`submit`/`poll` find it
through the `CLAUDE_CODE_SESSION_ID` environment variable that Claude Code exports into Bash-tool
children: a session started in repo A can `cd /ws/B && pawl run …`, and the hooks guard that run too.
You only still see this refusal when `CLAUDE_CODE_SESSION_ID` is not set (a plain terminal, or a
Claude Code too old to export it) — then only the working-copy heartbeat is consulted, and it belongs
to the session's start directory. Start the session in the working copy you run `pawl` in.

The refusal's `Diagnosis:` lines (see [Hooks not live](#hooks-not-live)) tell you which case you are in.
If it says the hook has not recorded a pawl command for your session id, either the hook is not
installed or enabled for this session (install the plugin and restart the session), or it runs a pawl
older than 0.4.0, which never writes the session heartbeat (run `pawl update`).

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
