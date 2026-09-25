# CLI reference

## Who runs what

| Who | Commands |
|---|---|
| **You** (terminal or `/pawl`) | `pawl validate`, `pawl list`, `pawl status`, `pawl abandon`, `pawl update`, `pawl run` (in a session, type `/pawl run`) |
| **The model** (via `/pawl`, you don't type these) | `pawl run`, `pawl submit`, `pawl poll` |
| **The hooks** (nobody types these) | `pawl hook pre`, `pawl hook stop` |

A human typing `pawl run` for an agentic workflow in a plain terminal gets one `DISPATCH` block, then
the process exits with nothing driving it — deterministic-only workflows, or inspection, only.

```
pawl run <name> [key=value …] [--run <id>] [--fresh] [--force] [--no-enforcement]
pawl validate <workflow-name> | --path <file>
pawl status [--run <id>] [--json]
pawl list
pawl abandon --run <id> [--reason <text>]
pawl version
pawl update [--check] [--version <vX.Y.Z>] [--force]
```

## Exit codes

Every command uses the same table (`pawl status` is the one deliberate exception — see below).

| Code | Meaning |
|---|---|
| 0 | fine — incl. stopped at `DISPATCH`/`ASK`/`WAIT`, or ended `ok` |
| 1 | resolution error: unknown workflow, or another non-flag problem hit while resolving it |
| 2 | usage error: bad/unrecognised flag, missing a flag's value, missing required arg, or validation failed |
| 3 | run is `BLOCKED` — paused, resumable, not an error (`pawl run`/`pawl submit`/`pawl poll` only) |
| 4 | refused: `pawl run` with no fresh `PreToolUse` heartbeat and enforcement not opted out, lock held, file changed, submit for a non-current step or kind, a poll of a current step that is not `kind: wait`, or a run that has already finished (the last one is `pawl submit`/`pawl abandon` only — `pawl poll` exits 0 quietly instead for the same case, see below); separately, `pawl update` refusing to overwrite a source/`go install` build without `--force` |
| 5 | engine error — a bug, or a broken/unreadable run directory or journal; for `pawl update`, any failure resolving, downloading, verifying or installing the release |

---

## `pawl run` — human, model

```
pawl run <name> [key=value …] [--run <id>] [--fresh] [--force] [--no-enforcement]
```

Starts a run, or resumes the one for this working copy that is not `done` — `BLOCKED` included (see
[running.md#blocked](running.md#blocked)). First checks enforcement (below); then executes
consecutive `deterministic` steps itself, then prints exactly one of `DISPATCH`/`ASK`/`WAIT`/`TERMINAL`.

| Flag | Effect |
|---|---|
| `key=value` | binds a declared `args:` key; missing required args refuse to start, with usage |
| `--run <id>` | disambiguate which run to resume, when more than one resolves |
| `--fresh` | start a new run, resetting every counter, even if one is live |
| `--force` | steal a lock held by a dead process; refuses a live one regardless |
| `--no-enforcement` | skip the `PreToolUse` heartbeat check and opt this run out of the hooks for its lifetime (same effect as `PAWL_ENFORCEMENT=off`) |

Resuming at an arbitrary step (`--from <step>`) isn't yet available — see
[README.md#not-yet](README.md#not-yet).

**Enforcement check.** Unless `--no-enforcement` or `PAWL_ENFORCEMENT=off` is set, `pawl run` refuses
to start (exit 4) unless `pawl hook pre` has written a heartbeat for this working copy no more than 5 minutes
ago — its way of confirming the `PreToolUse` hook is actually wired up:

```
pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.
Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.
```

On success the banner reads `hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)`, plus
`guards: N advisory (pattern-matched)` when the workflow declares any; with the opt-out, it reads
`enforcement: off (--no-enforcement)` / `enforcement: off (PAWL_ENFORCEMENT=off)`, plus
`guards: N declared, NOT enforced (enforcement off)`. See [install.md#hooks](install.md#hooks).

**On resume, the mode recorded at run start wins.** A run's enforcement mode is bound when it starts,
and the hooks follow that recorded mode for the run's whole life, so a resume does too. Resuming a run
started opted out needs no heartbeat and no flag, and its banner reads
`enforcement: off (bound at run start: --no-enforcement)` (or `…: PAWL_ENFORCEMENT=off)`). Resuming
an enforced run needs no heartbeat either, so a person can resume it from a plain terminal — after a
reboot, or after fixing the world for a `BLOCKED` run. The run stays enforced (the hooks keep applying
to it), deterministic steps run, and it stops at the next `DISPATCH`/`ASK` as usual. Without a fresh
heartbeat the banner reads `enforcement: on (bound at run start; no hook heartbeat for this resume)`
and the run's existing `driver.json` is left as it is; with one (a hooked Claude Code session
resuming), the banner is the usual `hooks: PreToolUse ✔ (heartbeat) …` line and that session becomes
the run's driver. Passing `--no-enforcement` or `PAWL_ENFORCEMENT=off` on a resume of an enforced run
is refused (exit 4), since it could not turn the hooks off for that run:

```
pawl run: run 7f3a was started with enforcement on, and enforcement is bound at run start — --no-enforcement cannot turn it off. To continue the run, resume without it (no hook heartbeat is needed to resume); it stays enforced
```

Other refusals you may see, all exit 4 (real output, not paraphrased):

```
pawl run: journal: run locked by pid 48122 (alive); wait, or use --force if that process is gone
pawl run: workflow file has changed since run 7f3a started (changed: greet); use --fresh to start a new run
```

Submitting or abandoning a run that has already finished is the same refusal, exit 4 (`pawl poll`
against a finished run is not a refusal — it exits 0 quietly instead, since a poll driven by
`Monitor` treats "the run moved on" as expected, not an error; see the `pawl poll` section below):

```
pawl submit: engine: run has already finished: run "7f3a" ended ok
pawl abandon: run 7f3a has already ended (ok); nothing to abandon
```

## `pawl validate` — human

```
pawl validate <workflow-name> | --path <file>
```

Static checks only — nothing runs, no network, no LLM. Takes a workflow name, resolved under
`.claude/workflows/` exactly as `pawl run` resolves one — or, with `--path <file>`, a specific file
instead, skipping name resolution entirely (useful for a workflow mid-edit, before it's placed under
`.claude/workflows/` at all, or checked out under a different name). A name and `--path` are mutually
exclusive; giving both, or neither, is a usage error. A relative `--path` resolves against the current
directory; scripts and context files the workflow references still resolve relative to the workflow
file itself, the same way either way.

```
› pawl validate manage-mr
workflow: .claude/workflows/manage-mr.yaml (repo-local)
soft: 6/23 steps (26.1%): entry, changelog, fix_issues
```

Exit 0 clean, 2 on any error (including a missing/unrecognised flag or giving both/neither of a name
and `--path`); a missing or unreadable `--path` file is exit 1, consistent with an unknown name. Full
check list and messages: [validation.md](validation.md).

`--strict` does not exist in this build, despite an earlier version of this doc claiming it turns the
two `soft:`-adjacent warnings into errors for CI — there is no flag parsing for it in
`internal/cli/validate.go` at all. If you want that behaviour, it needs building; don't pass `--strict`
expecting it to do anything today.

## `pawl status` — human

```
pawl status [--run <id>] [--json]
```

No flags: every live run for this working copy (exactly one live run prints it directly; more than
one is a usage error — disambiguate with `--run <id>`). `--run` finds a run whether it's still live
or already ended (e.g. an abandoned run, to see its `--reason`). `--json` prints the same fields
machine-readably, always exit 0 on a successful lookup (see below; `--all` does not exist — every
run for the machine isn't listable by this command).

```json
{
  "root": "/path/to/working/copy",
  "runs": [
    {
      "run_id": "a98d",
      "workflow": "/path/.claude/workflows/sample.yaml",
      "warning": "",
      "status": "running",
      "step": "greet",
      "attempt": 1,
      "visits": {"greet": 1},
      "state_keys": ["greeting"],
      "reason": "",
      "soft": {"count": 0, "total": 2, "percent": 0, "step_ids": []}
    }
  ]
}
```

`runs` is always an array so the "no live runs" case (`{"root": "...", "runs": []}`) and the
one-run case share a shape; `pawl status` never actually prints more than one run itself (more than
one live run resolving, with no `--run` to disambiguate, is a resolution error, exit 1, `--json`
included — not a multi-element `runs`; the same classing `pawl run`'s own ambiguous-resume refusal
gets, since either way the ambiguity is "which run did you mean", not a refusal by a specific run).
`warning` and `reason` are empty strings, not omitted, when there is nothing to say.

`pawl status` is deliberately exempt from the exit-code table's row 3 (`BLOCKED` → exit 3): it exits 0
on every successful lookup, `BLOCKED`, `abandoned` or otherwise, because it's a reporting command —
the run's status is in its own output (plain text and `--json` alike), not something a non-zero exit
would add to. This is a design choice, not a gap to fill in later.
[running.md#pawl-status](running.md#pawl-status) has more on reading the output.

## `pawl list` — human

```
pawl list
```

Every workflow `pawl run` can resolve, and where it's from. Repo-local shadows user-level; the shadowed
one is still shown, so you know why. No `--json` — machine-readable output isn't implemented for this
command.

```
› pawl list
manage-mr           repo   .claude/workflows/manage-mr.yaml
tidy                user   ~/.claude/workflows/tidy.yaml  (shadowed by repo)
```

## `pawl abandon` — human

```
pawl abandon --run <id> [--reason <text>]
```

Ends a run. Always available, always terminal, never prompts. `--reason` is journalled and shown in
`pawl status`. Undoes nothing a step did; releases `Stop` so the session can end.

## `pawl version` — human

```
› pawl version
pawl dev
```

Prints `Version` (`main.Version` in `cmd/pawl`), always exit 0. Release binaries print the released
version (stamped at build time by goreleaser's `-ldflags -X main.Version`); a binary built from
source (`go build`/`go install`/`make install`) always prints `pawl dev`. There is still no
plugin-pin comparison: the plugin manifest's pin is not checked against `Version`, and there is no
refusal tied to it.

## `pawl update` — human

```
pawl update [--check] [--version <vX.Y.Z>] [--force]
```

Self-updates the *running* binary in place: downloads the release archive for the target GitHub
release, verifies its sha256 against that release's `checksums.txt` before writing anything, and
atomically replaces the binary at its own resolved path (`os.Executable` + symlink resolution). No
flags: resolves the latest release, refuses to downgrade, and no-ops if already current. `--check`
reports current vs. latest and installs nothing. `--version vX.Y.Z` (`v` optional) pins an exact
release, including an older one — a rollback. `--force` skips every "nothing to do" check and
reinstalls regardless.

```
› pawl update --check
current: 0.1.0 latest: 0.2.0 update available: true
› pawl update
pawl updated: 0.1.0 -> 0.2.0 (/home/you/.local/bin/pawl)
```

A binary built from source (`pawl version` prints `pawl dev`, or anything else `MAJOR.MINOR.PATCH`
can't parse) is refused without `--force` — `pawl update` won't silently swap a `go install`/`make
install` build for a release binary out from under you. Verbatim stderr:

```
› pawl update
pawl update: current version "dev" looks like a source/`go install` build, not a tagged release; pawl update won't overwrite it.
To update a source build, either:
  go install github.com/dcferreira/agent-pawl/cmd/pawl@latest
  or rebuild from source (see docs/install.md)
--force replaces it with a release binary anyway (see docs/cli.md).
```

`--check` still works on a dev build (it just reports the latest release and that current is a
source build); `--force` bypasses the refusal like it bypasses every other "nothing to do" check.

`--version` must be a strict `vX.Y.Z` (or `X.Y.Z`) — no leading zeros on any component, and an
optional `-<prerelease>` suffix restricted to `[0-9A-Za-z.-]` — checked with a regexp *before* any
network call, since a validated pin is spliced straight into the release-download URL and its
`checksums.txt` (anything looser would let a value like `1.2.3-/../../other/repo` traverse both
requests, defeating the checksum check along with the download). An empty value (`--version ""` /
`--version=`) and combining `--check` with `--version` are both refused as usage errors too, rather
than silently meaning "latest". The same strict check applies to the `tag_name` the GitHub API
returns for the latest release, so a malformed value there is refused rather than installed. A pinned release GitHub doesn't have (or doesn't publish your
platform's asset for) fails with a "release not found" error naming the release and the expected
asset, not a bare HTTP status.

Only linux/darwin x amd64/arm64 release binaries are published (`.goreleaser.yaml`); any other
platform is a clear error, not a confusing download failure. Exit codes: 0 on success, a no-op
(already up to date, already on the pinned version, or a refused downgrade), or `--check`; 2 for a
usage error (unrecognised flag, missing flag value, empty/malformed `--version`, or `--check`
combined with `--version`); 4 for the dev-build refusal above; 5 for anything that goes wrong
resolving, downloading, verifying or installing the release (network failure, checksum mismatch,
missing archive entry, unwritable target directory).

---

## Internal commands

Called by the model as part of the handshake — documented so you can read a transcript, not to type
by hand; doing so can leave the run and the session disagreeing about who is doing what.

### `pawl submit` — model

```
pawl submit --run <id> --step <step> [--json '<obj>']
                                   [--option <choice> …] [--other '<text>']
```

Delivers an `agentic` or `human` result. The engine runs the postcondition, resolves the outcome,
transitions, re-checks invariants, runs following deterministic steps, and prints the next line.

- `--json` — an agentic step's return; must match `writes:`.
- `--option` — a `human` answer; repeat for a multi-select.
- `--other` — the free-text "Other" answer, always yielding `chosen`.

No `--token` flag: a `wait` result is never submitted by the model — `pawl poll` delivers it
internally. It refuses any `(run, step, attempt)` other than the one the journal is waiting on,
including a run that has already finished (exit 4). The submitted JSON is an input to the
postcondition, never the verdict. A run id that doesn't exist at all is a resolution error (exit 1),
not a refusal.

### `pawl poll` — model

```
pawl poll --run <id> --step <step>
```

Runs a `wait` step's `poll:` command every `every:`. Each iteration reads the last non-empty stdout
line; the first carrying a routed token ends the loop, as does `timeout:` expiring. `pawl poll` then
does internally what `pawl submit` would, printing the next line. See [steps/wait.md](steps/wait.md).

`pawl poll` runs unattended, under Monitor — DESIGN.md §3 treats "the run moved on" as an expected
race, not a failure — so it deliberately exits quietly (0) rather than refusing in two cases: the run
has already ended (another process finished or abandoned it while this `WAIT` line sat in the
session's scrollback), or the run's cursor has simply moved past the polled step. A run id that
doesn't exist at all is still a resolution error (exit 1) — it was never going to resolve, which is
not the same race. Polling a step that is current but isn't `kind: wait` (a caller mistake, not a
race) is a refusal, exit 4, like every other "not what the run is waiting on" case.

### `pawl hook` — hook

```
pawl hook pre
pawl hook stop
```

Bound once at install, called by Claude Code with a JSON payload on stdin. `pre` writes the session's
heartbeat whenever any simple command in it invokes `pawl`, applies the guard table (union across the
cwd working copy's live runs, per guard pattern) on `PreToolUse` for Bash, and denies VCS-mutating Bash from a subagent while any run is live —
its one subagent rule; `subagent_args:` is not enforced. A command made only of `pawl`/`cd` segments
is never denied by a guard (its arguments may mention a guarded pattern, but `pawl` never runs it
directly). Runs started with enforcement opted out are ignored entirely. `stop` blocks only for the
session driving (per `driver.json`, found across every working copy, whatever the payload's cwd)
a run that is at an `agentic`/`parallel` step awaiting `pawl submit` (not `BLOCKED`, not
`wait`/`human`), at most once per turn, printing `pawl abandon --run <id>` — unless the Stop payload
reports background work still in flight (`background_tasks`/`session_crons` non-empty), in which
case it always allows: the session dispatched a background subagent or is polling under Monitor and
will be woken back up. Exit protocol: allow = exit 0 with no output; `pre` deny = exit 0 with
`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"<reason>"}}`
on stdout; `stop` block = exit 0 with `{"decision":"block","reason":"<reason>"}` on stdout;
unparseable input or an I/O error = exit 1 for `pre` (fail open, non-blocking), while `stop` never
fails closed — any error there allows. `pawl hook` never exits 2. `bin/pawl-hook`, the wrapper Claude
Code actually calls, exits in a few ms when no run is live and the payload doesn't mention `pawl`,
and turns any non-zero exit from the binary into exit 1 — so a `pawl` binary older than the plugin
(no `hook` subcommand, usage exit 2) fails open instead of blocking.
