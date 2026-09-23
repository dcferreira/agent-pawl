# CLI reference

## Who runs what

| Who | Commands |
|---|---|
| **You** (terminal or `/pawl`) | `pawl validate`, `pawl list`, `pawl status`, `pawl abandon`, `pawl run` (in a session, type `/pawl run`) |
| **The model** (via `/pawl`, you don't type these) | `pawl run`, `pawl submit`, `pawl poll` |
| **The hooks** (nobody types these) | `pawl hook pre`, `pawl hook stop` |

A human typing `pawl run` for an agentic workflow in a plain terminal gets one `DISPATCH` block, then
the process exits with nothing driving it — deterministic-only workflows, or inspection, only.

```
pawl run <name> [key=value …] [--run <id>] [--fresh] [--force]
pawl validate <workflow-name> [--path <file>]
pawl status [--run <id>] [--json]
pawl list
pawl abandon --run <id> [--reason <text>]
pawl version
```

## Exit codes

Every command uses the same table (`pawl status` is the one deliberate exception — see below).

| Code | Meaning |
|---|---|
| 0 | fine — incl. stopped at `DISPATCH`/`ASK`/`WAIT`, or ended `ok` |
| 1 | resolution error: unknown workflow, or another non-flag problem hit while resolving it |
| 2 | usage error: bad/unrecognised flag, missing a flag's value, missing required arg, or validation failed |
| 3 | run is `BLOCKED` — paused, resumable, not an error (`pawl run`/`pawl submit`/`pawl poll` only) |
| 4 | refused: lock held, file changed, submit/poll for a non-current step or kind, or a run that has already finished |
| 5 | engine error — a bug, or a broken/unreadable run directory or journal |

---

## `pawl run` — human, model

```
pawl run <name> [key=value …] [--run <id>] [--fresh] [--force]
```

Starts a run, or resumes the one for this working copy that is not `done` — `BLOCKED` included (see
[running.md#blocked](running.md#blocked)). Executes consecutive `deterministic` steps itself, then
prints exactly one of `DISPATCH`/`ASK`/`WAIT`/`TERMINAL`.

| Flag | Effect |
|---|---|
| `key=value` | binds a declared `args:` key; missing required args refuse to start, with usage |
| `--run <id>` | disambiguate which run to resume, when more than one resolves |
| `--fresh` | start a new run, resetting every counter, even if one is live |
| `--force` | steal a lock held by a dead process; refuses a live one regardless |

Resuming at an arbitrary step (`--from <step>`) isn't yet available — see
[README.md#not-yet](README.md#not-yet).

Refusals you may see, all exit 4 (real output, not paraphrased):

```
pawl run: journal: run locked by pid 48122 (alive); wait, or use --force if that process is gone
pawl run: workflow file has changed since run 7f3a started (changed: greet); use --fresh to start a new run
```

Submitting or polling against a run that has already finished is the same refusal, exit 4:

```
pawl submit: engine: run has already finished: run "7f3a" ended ok
pawl abandon: run 7f3a has already ended (ok); nothing to abandon
```

## `pawl validate` — human

```
pawl validate <workflow-name> [--path <file>]
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

Prints `Version` (`main.Version` in `cmd/pawl`), always exit 0. This build has no `-ldflags`
version stamping and no plugin-pin comparison — a binary built from source always prints `pawl dev`,
whatever the plugin manifest's pin says, and there is no refusal tied to it (see README.md's Status
section).

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

Bound once at install, called by Claude Code with a JSON payload on stdin. `pre` applies the guard
table on `PreToolUse` for Bash, and denies VCS-mutating Bash from a subagent during an agentic step —
its one subagent rule; `subagent_args:` is not enforced. `stop` exits 2 while a live run here is
non-terminal, at most once per turn, printing `pawl abandon --run <id>`. A wrapper in front of each
exits in ~2 ms when no run is live.
