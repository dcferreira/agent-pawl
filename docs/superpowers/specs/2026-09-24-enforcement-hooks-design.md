# Enforcement hooks (`pawl hook pre|stop`) — design

> Historical working document; superseded where they differ by DESIGN.md §5, docs/cli.md and the code.

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
- Relays the binary's stdout (a JSON decision, if any) with exit 0 only when the binary exits 0;
  **every non-zero exit becomes exit 1** (fail open), never a bare exit 2. The plugin and the
  binary update separately, and a binary older than `pawl hook` exits 2 (usage) for the unknown
  subcommand — passed through, that would deny every Bash call mentioning `pawl` (including the
  `go install`/`pawl update` that fixes the skew) and refuse every Stop whose payload mentions
  `pawl` (e.g. in `cwd`).
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
  removed on every terminal transition and on `pawl abandon`, and re-derived (best-effort) by
  `cmdHook` itself whenever it loads at least one live run. **A fast-path cache only** in the sense
  that the source of truth for "which runs are live" is always `journal.Live(root)` — but a missing
  entry is not merely cosmetic: it means `bin/pawl-hook` takes the fast-path exit and never invokes
  this binary at all for a payload that doesn't mention `pawl`, so enforcement (guards, the subagent
  VCS rule, `Stop`'s block, the heartbeat) silently lapses for that working copy until the next
  `pawl` command or hook invocation heals it.
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
`driver.json` into `[]LiveRun` (skipping runs started with enforcement opted out — see below), calls
`Decide*`, and maps the `Decision` to the hook protocol: allow = exit 0 with no output; deny/block =
exit 0 with Claude Code's JSON decision on stdout (`{"hookSpecificOutput":{"hookEventName":
"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"…"}}` for pre,
`{"decision":"block","reason":"…"}` for stop). `pawl hook` never exits 2 (see `bin/pawl-hook`).

## Rules

### `pawl hook pre` (PreToolUse, Bash)

1. **Heartbeat.** If the command's first token has basename `pawl`, write
   `heartbeat/<slug>.json`. Never denies.
2. **Guards — union across live runs (§5).** Skipped entirely for a pawl-and-cd-only command
   (`IsPawlOnlyCommand`): a `pawl` invocation never executes a guarded action itself (deterministic
   steps run in-process, unseen by the hook), so a guard's `match:` text inside its arguments (a
   `pawl submit --json` summary, a `pawl run` key=value) must not deny the driver's own submit and
   strand the run. Segmenting is quote-aware: separators inside single/double quotes (or
   backslash-escaped) and fd redirects (`2>&1`, `>&2`) don't start a new segment; any command or
   process substitution (`$(`, backtick, `<(`, `>(`, outside single quotes) makes the command not
   pawl-only. The lexer fails safe: on anything outside its small subset (a `#` comment, `$'…'`,
   an unterminated quote, a heredoc `<<`, a subshell/function `(`, brace expansion, a wrapper like
   `bash -c`/`sudo`/`xargs`) it reports "unsure", and an unsure command is never pawl-only. For each
   live run (BLOCKED included):
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
     `workspace root|list`, `bookmark list`); `--help`/`-h` directly after the subcommand is read-only
     (`git commit -m -h` is not).
   - Matched on the command string (first token basename `git`/`jj`, plus the same check after
     `&&`, `||`, `;`, `|` separators outside quotes, after stripping leading reserved words like
     `do`/`then`/`{`/`!`). Advisory, like guards. If the lexer is unsure or the command has a
     substitution, it falls back to a raw token scan: any `git` token followed later by a mutating
     subcommand word, or any `jj` token followed by a non-read-only subcommand, denies.
4. Deny → the JSON deny decision (exit 0) with a one-line reason: rule, run id, step, what is
   allowed where.

### `pawl hook stop` (Stop)

- `stop_hook_active` true → allow (refuse at most once per turn).
- `background_tasks` non-empty or `session_crons` non-empty → allow: the session is legitimately
  paused waiting to be woken back up (e.g. it dispatched the agentic step's work to a background
  subagent and ended its turn to wait for it), not walking away. Both arrays are counted from the
  Stop payload (`BackgroundTasks`/`SessionCrons` on `hook.Payload`); absent arrays count as empty,
  and entries with shapes pawl doesn't model still parse (decoded as `[]json.RawMessage`).
- Block iff none of the above and some live run for this working copy has: not BLOCKED,
  `DriverSession == payload.session_id`, and `CursorKind` ∈ {`agentic`, `parallel`} (the session
  owes a `pawl submit`). Reason:
  `pawl run <id> (<workflow>) is at step <step> awaiting \`pawl submit\`. Finish it, or: pawl abandon --run <id>`
  — always includes the abandon command.
- Everything else allows: `wait`/`human` cursors (the session may legitimately end its turn to
  ask the person or wait on a poller), BLOCKED runs, other sessions' runs, runs with no driver.

### Allow

Exit 0, no output.

### Opted-out runs

A run started with `--no-enforcement`/`PAWL_ENFORCEMENT=off` records that in `RUN_START`'s
`hook_self_test` (`off (…)`; `journal.RunState.EnforcementOff`). The opt-out is bound for the
run's lifetime: the hooks leave such a run out of the live runs they decide over (no guards, no
subagent VCS rule, no Stop refusal), and nothing ever stamps its `driver.json` — even though, with
the plugin installed, the PreToolUse for the `pawl run … --no-enforcement` call itself has just
written a fresh heartbeat.

## `pawl run` / `submit` / `poll` changes

- `pawl run` (start and resume):
  - `--no-enforcement` flag or `PAWL_ENFORCEMENT=off` → skip the check; banner
    `enforcement: off (--no-enforcement)` / `enforcement: off (PAWL_ENFORCEMENT=off)`; guards line
    keeps PR #12's `NOT enforced` wording.
  - else read `heartbeat/<slug>.json`; missing or older than **5 minutes** → refuse: non-zero exit
    (existing precondition-class exit code), no run directory created, message:
    ```
    pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.
    Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.
    ```
  - on success (enforcement on): write `driver.json` from the heartbeat and create the `live/` symlink
    right after `RUN_START` is journaled (before any deterministic prefix runs), banner
    `hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)` and, when guards exist,
    `guards: N advisory (pattern-matched)`.
  - on resume, the mode recorded in the run's `RUN_START` wins over this invocation's, since the
    hooks follow the recorded one: a run bound off resumes with no heartbeat check and banner
    `enforcement: off (bound at run start: --no-enforcement)` (or `PAWL_ENFORCEMENT=off`); a run
    bound on also resumes with no heartbeat check (a person may resume it from a plain terminal; it
    stays enforced, deterministic steps run, and it stops at the next `DISPATCH`/`ASK`) — a fresh
    heartbeat re-stamps `driver.json` and prints the usual `hooks:` banner, none leaves `driver.json`
    as it is and prints `enforcement: on (bound at run start; no hook heartbeat for this resume)`.
    `--no-enforcement`/`PAWL_ENFORCEMENT=off` on its resume is refused (exit 4, without suggesting an
    opt-out as the way to resume) instead of printing an "off" banner the hooks would contradict.
- `pawl submit` / `pawl poll`: re-stamp `driver.json` when a fresh heartbeat exists and the run was
  not started opted out; **never refuse** for a missing heartbeat (would strand runs; opt-out runs
  need none by design).
- Terminal transitions and `pawl abandon`: remove the `live/` symlink.

## Error handling

- `pawl hook pre`: unparseable payload or `journal.Live` I/O error → **fail open** (exit 1, message
  on stderr). Exception: per-run guard compile failure fails closed (above).
- `pawl hook stop`: any error → allow. Never trap a session.
- Writing the heartbeat or `driver.json` fails → hook still decides; `pawl run` will then refuse
  (no heartbeat), which is the visible signal.

## Known residuals (documented, not fixed)

- Heartbeat is per working copy: two sessions launching `pawl` commands in one checkout within
  5 minutes can misattribute the driver.
- A heartbeat up to 5 minutes old from another session in the same checkout can make a plain-terminal
  `pawl run` start enforced, with that other session recorded as the run's driver: the 5-minute TTL
  (widened from 10s so PreToolUse's heartbeat outlives Claude Code's own permission prompt) trades a
  false refusal for a broader, but strictly less harmful, misattribution window.
- The heartbeat's working copy comes from the hook payload's `cwd` (the Bash tool's actual working
  directory for that call), not the command's own effective directory: a Bash call that leads with
  `cd /other/repo && pawl run x` from a session otherwise sitting elsewhere still stamps the
  heartbeat for the shell's starting `cwd`, not `/other/repo`, so `pawl run` refuses. Run `pawl` from
  inside the working copy directly (see [troubleshooting.md](../../troubleshooting.md)).
- Stop's installation is inferred from PreToolUse's heartbeat (same `hooks.json`), not observed.
- Guards and the subagent VCS rule are scoped to the payload cwd's working copy: a command run after
  `cd`-ing out of it, or aimed at another repo (`git -C /repo push`), is not checked against its runs.
  Only `pawl hook stop` looks across working copies (by `driver.json`'s session id, via `live/`).
- Guards and the VCS rule are string-matched: `$()`, variables, renamed binaries evade them
  (invariants are the backstop, per §5's table).
- The `background_tasks`/`session_crons` Stop exemption (task 9) doesn't check that the reported
  in-flight work is the dispatched step's: a session that owes a `pawl submit` but has *any*
  unrelated background task or scheduled cron in the Stop payload is allowed to end its turn too —
  it can be used to walk away while looking legitimately paused.

## Testing (red-green)

- `internal/hook`: table-driven `DecidePre`/`DecideStop`: guard union (deny/permit/neutral across
  2 runs), BLOCKED run, fail-closed table, VCS classifier (git/jj allow and deny lists, chained
  commands, `--help`), `stop_hook_active`, driver match/mismatch, cursor kinds.
- `internal/cli`: `pawl hook pre|stop` with JSON stdin against a temp `PAWL_STATE_DIR` (exit codes,
  stdout/stderr); `pawl run` refusal/opt-out/banner; `driver.json` and `live/` lifecycle
  (start, submit re-stamp, terminal, abandon).
- `bin/pawl-hook`: Go test execs it with a fake `pawl` on `PATH` — fast-path exit, stdin
  passthrough, recursion guard, missing binary, a too-old binary's usage exit 2 failing open, and a
  JSON decision relayed intact.
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
  and the action proceeds (:864) — this is the "fail open" exit. pawl does not use exit 2 (it can't
  be told apart from a too-old binary's usage exit); it uses the exit-0 JSON form instead:
  PreToolUse `hookSpecificOutput.permissionDecision: "deny"` with `permissionDecisionReason`, and
  Stop `{"decision":"block","reason":…}`.
- Stop input includes `stop_hook_active`, "true when Claude Code is already continuing as a result
  of a stop hook"; Claude Code force-ends the turn after 8 consecutive blocks (:2554).
- Stop input also carries `background_tasks` (in-flight tasks: `{id, type, status, description,
  ...}`, `type` e.g. `shell`, `subagent`, `monitor`, `workflow`) and `session_crons` (scheduled
  wakeups) — both empty when nothing is in flight/scheduled, and may be absent when the task
  registry is unreachable. `DecideStop` allows whenever either is non-empty (task 9).
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
