# CLI reference

## Who runs what

| Who | Commands |
|---|---|
| **You** (terminal or `/wf`) | `wf validate`, `wf list`, `wf status`, `wf abandon`, `wf run` (in a session, type `/wf run`) |
| **The model** (via `/wf`, you don't type these) | `wf run`, `wf submit`, `wf poll` |
| **The hooks** (nobody types these) | `wf hook pre`, `wf hook stop` |

A human typing `wf run` for an agentic workflow in a plain terminal gets one `DISPATCH` block, then
the process exits with nothing driving it — deterministic-only workflows, or inspection, only.

```
wf run <name> [key=value …] [--run <id>] [--fresh] [--force]
wf validate <name|path> [--strict]
wf status [--run <id>] [--all] [--json]
wf list [--json]
wf abandon --run <id> [--reason <text>]
wf version
```

## Exit codes

Every command uses the same table.

| Code | Meaning |
|---|---|
| 0 | fine — incl. stopped at `DISPATCH`/`ASK`/`WAIT`, or ended `ok` |
| 1 | usage error: bad flag, unknown workflow, missing required arg |
| 2 | validation failed |
| 3 | run is `BLOCKED` — paused, resumable, not an error |
| 4 | refused: lock held, version mismatch, file changed, submit for a non-current step |
| 5 | engine error — a bug, or a broken run directory |

---

## `wf run` — human, model

```
wf run <name> [key=value …] [--run <id>] [--fresh] [--force]
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
wf: run 7f3a is locked by pid 48122 (alive). Wait, or use --force if that process is gone.
wf: ship-change.yaml changed since run 7f3a started. Use --fresh.
wf: binary 0.4.2 does not match plugin pin 0.5.0. Run `/wf` to fetch the pinned build.
```

## `wf validate` — human

```
wf validate <name|path> [--strict]
```

Static checks only — nothing runs, no network, no LLM. Takes a workflow name or a path. `--strict`
makes the two warnings — a key written and never read, a key read before anything writes it — into
errors, for CI.

```
› wf validate manage-mr
.claude/workflows/manage-mr.yaml: ok — 23 steps, 3 terminals, 2 cycles.
soft postconditions: 6 of 23 (26%): entry, changelog, fix_issues
```

Exit 0 clean, 2 on any error. Full check list and messages: [validation.md](validation.md).

## `wf status` — human

```
wf status [--run <id>] [--all] [--json]
```

No flags: every live run for this working copy. `--run` narrows to one, `--all` covers the machine,
`--json` prints the same fields machine-readably. Exit 0, or 3 if the selected run is `BLOCKED`. See
[running.md#wf-status](running.md#wf-status).

## `wf list` — human

```
wf list [--json]
```

Every workflow `wf run` can resolve, and where it's from. Repo-local shadows user-level; the shadowed
one is still shown, so you know why.

```
› wf list
manage-mr           repo   .claude/workflows/manage-mr.yaml
tidy                user   ~/.claude/workflows/tidy.yaml  (shadowed by repo)
```

## `wf abandon` — human

```
wf abandon --run <id> [--reason <text>]
```

Ends a run. Always available, always terminal, never prompts. `--reason` is journalled and shown in
`wf status`. Undoes nothing a step did; releases `Stop` so the session can end.

## `wf version` — human

```
› wf version
wf 0.4.2 (plugin pin 0.4.2)  binary: ~/.claude/wf/bin/wf
```

Exit 4 if the two versions differ — the same refusal `wf run` gives.

---

## Internal commands

Called by the model as part of the handshake — documented so you can read a transcript, not to type
by hand; doing so can leave the run and the session disagreeing about who is doing what.

### `wf submit` — model

```
wf submit --run <id> --step <step> [--json '<obj>']
                                   [--option <choice> …] [--other '<text>']
```

Delivers an `agentic` or `human` result. The engine runs the postcondition, resolves the outcome,
transitions, re-checks invariants, runs following deterministic steps, and prints the next line.

- `--json` — an agentic step's return; must match `writes:`.
- `--option` — a `human` answer; repeat for a multi-select.
- `--other` — the free-text "Other" answer, always yielding `chosen`.

No `--token` flag: a `wait` result is never submitted by the model — `wf poll` delivers it
internally. It refuses any `(run, step, attempt)` other than the one the journal is waiting on
(exit 4). The submitted JSON is an input to the postcondition, never the verdict.

### `wf poll` — model

```
wf poll --run <id> --step <step>
```

Runs a `wait` step's `poll:` command every `every:`. Each iteration reads the last non-empty stdout
line; the first carrying a routed token ends the loop, as does `timeout:` expiring. `wf poll` then
does internally what `wf submit` would, printing the next line. It exits doing nothing if the run
directory is gone or has moved on. See [steps/wait.md](steps/wait.md).

### `wf hook` — hook

```
wf hook pre
wf hook stop
```

Bound once at install, called by Claude Code with a JSON payload on stdin. `pre` applies the guard
table on `PreToolUse` for Bash, and denies VCS-mutating Bash from a subagent during an agentic step —
its one subagent rule; `subagent_args:` is not enforced. `stop` exits 2 while a live run here is
non-terminal, at most once per turn, printing `wf abandon --run <id>`. A wrapper in front of each
exits in ~2 ms when no run is live.
