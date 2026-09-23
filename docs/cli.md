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
pawl validate <name|path> [--strict]
pawl status [--run <id>] [--json]
pawl list
pawl abandon --run <id> [--reason <text>]
pawl version
```

## Exit codes

Every command uses the same table.

| Code | Meaning |
|---|---|
| 0 | fine — incl. stopped at `DISPATCH`/`ASK`/`WAIT`, or ended `ok` |
| 1 | resolution error: unknown workflow, or another non-flag problem hit while resolving it |
| 2 | usage error: bad/unrecognised flag, missing a flag's value, missing required arg, or validation failed |
| 3 | run is `BLOCKED` — paused, resumable, not an error |
| 4 | refused: lock held, version mismatch, file changed, submit for a non-current step |
| 5 | engine error — a bug, or a broken run directory |

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

Refusals you may see, all exit 4:

```
pawl: run 7f3a is locked by pid 48122 (alive). Wait, or use --force if that process is gone.
pawl: ship-change.yaml changed since run 7f3a started. Use --fresh.
pawl: binary 0.4.2 does not match plugin pin 0.5.0. Run `/pawl` to fetch the pinned build.
```

## `pawl validate` — human

```
pawl validate <name|path> [--strict]
```

Static checks only — nothing runs, no network, no LLM. Takes a workflow name or a path. `--strict`
makes the two warnings — a key written and never read, a key read before anything writes it — into
errors, for CI.

```
› pawl validate manage-mr
.claude/workflows/manage-mr.yaml: ok — 23 steps, 3 terminals, 2 cycles.
soft postconditions: 6 of 23 (26%): entry, changelog, fix_issues
```

Exit 0 clean, 2 on any error. Full check list and messages: [validation.md](validation.md).

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
one-run case share a shape; `pawl status` never actually prints more than one run itself (the
multiple-live-runs case is a plain-text usage refusal on stderr, exit 1, `--json` included — not a
multi-element `runs`). `warning` and `reason` are empty strings, not omitted, when there is nothing
to say. Exit 0 on a successful lookup — including a `BLOCKED` run: despite the general exit-code
table above, this build's `pawl status` has never implemented an exit-3 case for it.
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
pawl 0.4.2 (plugin pin 0.4.2)  binary: ~/.claude/pawl/bin/pawl
```

Exit 4 if the two versions differ — the same refusal `pawl run` gives.

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
internally. It refuses any `(run, step, attempt)` other than the one the journal is waiting on
(exit 4). The submitted JSON is an input to the postcondition, never the verdict.

### `pawl poll` — model

```
pawl poll --run <id> --step <step>
```

Runs a `wait` step's `poll:` command every `every:`. Each iteration reads the last non-empty stdout
line; the first carrying a routed token ends the loop, as does `timeout:` expiring. `pawl poll` then
does internally what `pawl submit` would, printing the next line. It exits doing nothing if the run
directory is gone or has moved on. See [steps/wait.md](steps/wait.md).

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
