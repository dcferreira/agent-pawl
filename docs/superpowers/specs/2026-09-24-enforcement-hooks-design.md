# Enforcement hooks (`pawl hook pre|stop`) — design

Status: approved in brainstorming 2026-09-24, pending written-spec review.
Implements DESIGN.md §5 for this build, with the deviations listed at the end.
Stacked on the guards change (PR #12, `internal/guard`), which owns guard validation and matching.

## Goal

A Claude Code session driving a pawl run cannot, without an explicit and visible opt-out:

1. **end its turn while it owes the run a `pawl submit`** (Stop hook);
2. **run a guarded Bash command outside the steps that guard allows** (PreToolUse, guards from
   PR #12 — advisory, pattern-matched);
3. **let a subagent mutate VCS while a run is live** (PreToolUse, `agent_id` rule).

Out of scope (decided): locking the orchestrating session out of Edit/Write while an agentic step
is dispatched — that remains a skill-protocol matter. No `pawl hooks install` command. No skill
frontmatter `hooks:` fallback.

## Scope boundary with the guards worker (agreed)

- Guards worker (PR #12 and follow-ups): `guards:` validation, `internal/guard`
  (`Compile`/`Table.Denied`/`Denied`/`Normalize`), `invariants:`, `retry:`, and their banner lines.
- This spec: live-run index, heartbeat, driver stamping, `pawl hook pre|stop`, `bin/pawl-hook`,
  plugin `hooks/hooks.json`, `pawl run`'s refuse-to-start check, and flipping the
  `enforcement:`/`guards:` banner lines once hooks are detected.

## Components

```
Claude Code ──PreToolUse(Bash)──► bin/pawl-hook pre  ──(maybe)──► pawl hook pre
            ──Stop─────────────► bin/pawl-hook stop ──(maybe)──► pawl hook stop
```

### `bin/pawl-hook` (committed POSIX sh)

- Reads stdin once into a variable.
- Fast path: exits 0 unless the live index directory is non-empty **or** the payload contains the
  substring `pawl` (so a `pawl run …` command still reaches the heartbeat). Target ≤ a few ms.
- Otherwise pipes the payload to `pawl hook <pre|stop>` via the sibling `bin/pawl` wrapper, which
  already finds a real `pawl` on `PATH` without recursing. No real `pawl` found → the wrapper exits
  1 with its install message: a non-blocking hook error the user sees, and `pawl run` could not
  have started anyway.
- Derives the pawl home the same way the binary does (below), honouring `PAWL_STATE_DIR`.

### `hooks/hooks.json` (plugin)

Binds `PreToolUse` (matcher `Bash`) → `${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook pre` and `Stop` →
`${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook stop`. Schema per the verified protocol section. Non-plugin users get a `settings.json` snippet in `docs/install.md#hooks`
that calls `pawl hook pre` / `pawl hook stop` directly (no fast path; `pawl-hook` is not on their
`PATH`).

### Paths

`pawlHome = filepath.Dir(journal.StateBase())` — `~/.claude/pawl`, or the parent of
`$PAWL_STATE_DIR` in tests. Beside `runs/`:

- `live/<slug>__<workflowID>__<runID>` → symlink to the run directory. Created at `RUN_START`,
  removed on every terminal transition and on `pawl abandon`. **A fast-path cache only**: the
  source of truth for "which runs are live" is always `journal.Live(root)`; a stale or missing
  symlink only affects the fast path's decision to invoke the binary.
- `heartbeat/<slug>.json` = `{"session_id": "...", "time": "<RFC3339Nano>"}`.

### Run-directory addition

- `driver.json` = `{"session_id": "...", "updated": "<RFC3339Nano>"}` — the session currently
  driving the run.
- Guards and step kinds are read from the existing `plan.json` (`Plan.Workflow`), not a new file.

### `internal/hook` (new package, pure decisions)

- `ParsePayload([]byte) (Payload, error)`: `SessionID`, `Cwd`, `ToolName`, `Command`
  (`tool_input.command`), `AgentID`, `StopHookActive`.
- `type LiveRun struct { RunID, WorkflowID string; Status string; Blocked bool; ActiveSteps []string; CursorKind spec.Kind; Guards *guard.Table; GuardsErr error; DriverSession string }`
- `DecidePre(runs []LiveRun, p Payload) Decision` and `DecideStop(runs []LiveRun, p Payload) Decision`,
  where `Decision{Allow bool; Reason string}`. No I/O.
- `IsPawlCommand(cmd string) bool`, `IsVCSMutation(cmd string) bool` — pure string classifiers.

### `internal/cli/hook.go` (`pawl hook pre|stop`)

Reads stdin, `ParsePayload`, `journal.ResolveRoot(payload.cwd)`, writes the heartbeat when
`IsPawlCommand` (pre only), loads `journal.Live(root)` plus each run's `plan.json` and
`driver.json` into `[]LiveRun`, calls `Decide*`, and maps the `Decision` to the hook protocol.

## Rules

### `pawl hook pre` (PreToolUse, Bash)

1. **Heartbeat.** If the command's first token has basename `pawl`, write
   `heartbeat/<slug>.json`. Never denies.
2. **Guards — union across live runs (§5).** For each live run (BLOCKED included):
   - `ActiveSteps` = `Cursor.Step` plus every outstanding `PendingBranches` branch id; a BLOCKED
     run has **no** active steps, so all its guards deny ("while BLOCKED, PreToolUse keeps
     denying").
   - run **denies** iff `Table.Denied(ActiveSteps, cmd) != nil`;
   - run **permits** iff some guard matches (`Table.Denied(nil, cmd) != nil`) and it does not deny.
   - Deny the command iff ≥1 run denies and no run permits.
   - A run whose plan's guards fail to `guard.Compile` **denies every command** (fail closed),
     reason naming the run and the error.
3. **Subagent VCS rule.** If `agent_id` is present and any non-terminal run is live for this
   working copy, deny when `IsVCSMutation(cmd)`:
   - git subcommands: `commit push pull reset checkout switch restore merge rebase cherry-pick
     revert tag branch stash am apply add rm mv clean`;
   - jj: every subcommand **except** the read-only set `log st status diff show file evolog op
     root workspace bookmark help` further narrowed where needed (`file show|list`, `op log`,
     `workspace root|list`, `bookmark list`); `--help`/`-h` anywhere is read-only.
   - Matched on the command string (first token basename `git`/`jj`, plus the same check after
     `&&`, `||`, `;`, `|` separators). Advisory, like guards.
4. Deny → blocking hook result with a one-line reason: rule, run id, step, what is allowed where.

### `pawl hook stop` (Stop)

- `stop_hook_active` true → allow (refuse at most once per turn).
- Block iff some live run for this working copy has: not BLOCKED, `DriverSession ==
  payload.session_id`, and `CursorKind` ∈ {`agentic`, `parallel`} (the session owes a
  `pawl submit`). Reason:
  `pawl run <id> (<workflow>) is at step <step> awaiting \`pawl submit\`. Finish it, or: pawl abandon --run <id>`
  — always includes the abandon command.
- Everything else allows: `wait`/`human` cursors (the session may legitimately end its turn to
  ask the person or wait on a poller), BLOCKED runs, other sessions' runs, runs with no driver.

### Allow

Exit 0, no output.

## `pawl run` / `submit` / `poll` changes

- `pawl run` (start and resume):
  - `--no-enforcement` flag or `PAWL_ENFORCEMENT=off` → skip the check; banner
    `enforcement: off (--no-enforcement)` / `enforcement: off (PAWL_ENFORCEMENT=off)`; guards line
    keeps PR #12's `NOT enforced` wording.
  - else read `heartbeat/<slug>.json`; missing or older than **10 s** → refuse: non-zero exit
    (existing precondition-class exit code), no run directory created, message:
    ```
    pawl: refusing to start: pawl's PreToolUse hook has not fired in this session.
    Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.
    ```
  - on success: write `driver.json` from the heartbeat, create the `live/` symlink, banner
    `hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)` and, when guards exist,
    `guards: N advisory (pattern-matched)`.
- `pawl submit` / `pawl poll`: re-stamp `driver.json` when a fresh heartbeat exists; **never
  refuse** for a missing heartbeat (would strand runs; opt-out runs have none by design).
- Terminal transitions and `pawl abandon`: remove the `live/` symlink.

## Error handling

- `pawl hook pre`: unparseable payload or `journal.Live` I/O error → **fail open** (non-blocking
  error exit, message on stderr). Exception: per-run guard compile failure fails closed (above).
- `pawl hook stop`: any error → allow. Never trap a session.
- Writing the heartbeat or `driver.json` fails → hook still decides; `pawl run` will then refuse
  (no heartbeat), which is the visible signal.

## Known residuals (documented, not fixed)

- Heartbeat is per working copy: two sessions launching `pawl` commands in one checkout within
  10 s can misattribute the driver.
- Stop's installation is inferred from PreToolUse's heartbeat (same `hooks.json`), not observed.
- Guards and the VCS rule are string-matched: `$()`, variables, renamed binaries evade them
  (invariants are the backstop, per §5's table).

## Testing (red-green)

- `internal/hook`: table-driven `DecidePre`/`DecideStop`: guard union (deny/permit/neutral across
  2 runs), BLOCKED run, fail-closed table, VCS classifier (git/jj allow and deny lists, chained
  commands, `--help`), `stop_hook_active`, driver match/mismatch, cursor kinds.
- `internal/cli`: `pawl hook pre|stop` with JSON stdin against a temp `PAWL_STATE_DIR` (exit codes,
  stdout/stderr); `pawl run` refusal/opt-out/banner; `driver.json` and `live/` lifecycle
  (start, submit re-stamp, terminal, abandon).
- `bin/pawl-hook`: Go test execs it with a fake `pawl` on `PATH` — fast-path exit, stdin
  passthrough, recursion guard, missing binary.
- Existing tests and `e2e/` set `PAWL_ENFORCEMENT=off` in shared helpers; a new e2e case writes a
  real heartbeat via `pawl hook pre` before `pawl run`.
- Live proof before the PR: a real `claude --plugin-dir .` session driving `green-tests` —
  refusal without hooks, start with hooks, Stop refused mid-dispatch, `pawl abandon` releasing it,
  and a guard deny using a `guards:` workflow.

## Docs

README and AGENTS.md Status; DESIGN.md §5 (driver session, heartbeat detection, wait/human
exemption, `live/` as fast-path cache, plan.json instead of guards.json); `skills/pawl/SKILL.md`;
`docs/install.md#hooks`; `docs/cli.md` (`pawl hook`, `--no-enforcement`); `docs/troubleshooting.md`;
`docs/dogfood.md`; `docs/guards-and-invariants.md` banner example.

## Deviations from DESIGN.md §5 (to be written back into §5)

1. Installation is detected by a PreToolUse heartbeat, not by inspecting settings; Stop is inferred.
2. Stop refuses only the run's driving session, and only at `agentic`/`parallel` cursors.
3. The hook reads guards from `plan.json`; there is no `guards.json`.
4. `live/` is a fast-path cache; identity always comes from `ResolveRoot` + `journal.Live`.
5. `pawl run` has an explicit `--no-enforcement` / `PAWL_ENFORCEMENT=off` opt-out.

## Hook protocol (checked 2026-09-24 against the raw code.claude.com/docs/en/hooks.md and plugins-reference.md)

Verified (line refs into hooks.md as fetched):
- Common input: `session_id`, `transcript_path`, `cwd`, `permission_mode`, `hook_event_name`;
  PreToolUse adds `tool_name`, `tool_input` (Bash: `tool_input.command`). **`agent_id` is "present
  only when the hook fires inside a subagent call"** — absent for the main session.
- Exit 2: PreToolUse "Blocks the tool call"; Stop "Prevents Claude from stopping, continues the
  conversation" (:882, :886). Any other non-zero exit without JSON stdout is a non-blocking error
  and the action proceeds (:864) — this is the "fail open" exit.
- Stop input includes `stop_hook_active`, "true when Claude Code is already continuing as a result
  of a stop hook"; Claude Code force-ends the turn after 8 consecutive blocks (:2554).
- Subagents fire `SubagentStop`, not `Stop` — the Stop rule never sees subagent turns.
- Plugin: `hooks/hooks.json` at the plugin root; schema
  `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"..."}]}],"Stop":[{"hooks":[{"type":"command","command":"..."}]}]}}`;
  `${CLAUDE_PLUGIN_ROOT}` available in hook commands; plugin `bin/` is on the **Bash tool's**
  PATH (plugins-reference) — so hook commands use `${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook`.
- `claude --plugin-dir <path>` loads a local plugin for one session.

Not verified, and not relied on:
- whether a PreToolUse deny is honoured in `bypassPermissions` mode (a residual if not);
- whether `claude plugin validate --strict` schema-checks `hooks.json` (CI runs it regardless);
- whether a subagent's `session_id` equals its parent's (the Stop rule doesn't need it; the VCS rule
  keys on `agent_id` only).
