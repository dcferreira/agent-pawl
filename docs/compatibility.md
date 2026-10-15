# Compatibility

What `pawl` 1.x promises, and what it does not. For how a release is cut, see
[releasing.md](releasing.md).

## What 1.x promises

From 1.0.0, every 1.x release keeps the public surfaces below working. A break means 2.0.
0.8.0 is the last pre-1.0 release that may break them.

The principle: be strict where loosening later is additive. Tightening a check after 1.0 is a
break. Loosening one never is. Everything not listed as public is internal and free to change.

- **Bugs.** Behaviour that contradicts this document or format-spec is a bug and may be fixed in a
  1.x release, even if a workflow depended on it. Such fixes are flagged in the changelog. That
  includes the validator: if it accepts something the spec forbids, tightening it to match the spec
  is a fix, not a break.
- **Deprecation.** In 1.x a deprecated feature keeps working and only warns (the warning text is
  not frozen). Removal waits for 2.0.

## Public surfaces

Frozen at 1.0. Only additive changes in 1.x.

### 1. The workflow format

[design/format-spec.md](../design/format-spec.md) is normative. A workflow that passes
`pawl validate` on a 1.x release validates and behaves the same on every later 1.x release.

- The loader and validator stay strict. An unknown field is a validate error, so a workflow that
  uses a field added in a later 1.x fails loudly on an earlier one.
- Workflows carry no `version:` field. Strictness does that job.
- New fields and new kinds are additive.
- Making the validator stricter than the documented spec is a break.

### 2. The step-script contract

Summarised here. The rules are in format-spec [§B.1](../design/format-spec.md) (stdout grammar and
coercion) and [§B.2](../design/format-spec.md) (substitution and environment).

- The engine reads the last non-empty stdout line of a `deterministic` or `wait` command.
- `emits: json` (the default) and `emits: pairs` are both permanent. `pairs` carries
  whitespace-free scalars only; use `json` for anything else.
- Values are coerced to the declared `state:` type by the coercion table in §B.1. A value that
  does not fit is rejected, never quietly converted.
- A non-zero exit is `failure`, or a hard failure retried under `retry:`.
- `PAWL_<KEY>` is exported only for keys the command's own template references. It is not widened
  to every key: one large value could exceed Linux's 128 KiB limit on a single environment string.
- `${key}` renders as exactly one shell-quoted token in shell contexts.
- State ingest escapes C0 control bytes and DEL, except LF and TAB.
- The wall-clock ceiling is 10 minutes. At the ceiling the engine sends SIGTERM to the process
  group, then SIGKILL no sooner than 5s later.
- A `!cmd` context entry that exits non-zero or times out does not stop the dispatch. A status line
  is added to the entry: `context command exited N` or `context command timed out`.
- The `PAWL__` environment namespace (double underscore) is reserved for `pawl`.

### 3. The CLI, as scripts use it

The command names, their flags, and the exit-code table in [cli.md](cli.md#exit-codes) are the
contract. The public commands are `run`, `validate`, `status`, `abandon`, `list`, `version`,
`update` and `help`. `pawl status --json` and the tab-separated output of `pawl list` are also public.

Machine-readable output evolves additively:

- `pawl status --json`: new keys may be added in 1.x, so a consumer must ignore keys it does not
  know. Removing or renaming a key, or changing its type, is a break (2.0, with a schema version
  bump).
- The `status` value set is open: today `running`, `ok`, `blocked`, `abandoned`, `newer-format`
  and `unreadable`. 1.x may add values, so a consumer must treat an unknown value as not running.
  Removing or renaming an existing value, or changing what one means, is a break.
- `pawl list`: tab-separated, and columns may only be appended. Parse by position and ignore extra
  columns.
- Exit codes: the table is closed. A new code, or a new meaning for an existing one, is a break.

Human-readable text is not frozen: banners, warnings, error wording and progress lines may change.

What that covers today ([cli.md](cli.md) has the details):

- **Flag parsing** ([cli.md#flags](cli.md#flags)). Every command accepts `--flag value` and
  `--flag=value`. A repeated flag, an unknown flag, an empty value (except `pawl abandon --reason`)
  and a value on a boolean flag are all usage errors (exit 2). `-h`/`--help` prints a command's usage
  to stdout and exits 0.
- **`pawl status --json`** ([cli.md#pawl-status--human](cli.md#pawl-status--human)) carries
  `schema_version: 1`. Every key in the documented shape is always present, with its documented type.
  `workflow` is the name `pawl run` accepts; `workflow_path` is the absolute path the run pinned.
  With several live runs and no `--run`, every run is listed and the exit is 0.
- **`pawl list`** prints `name<TAB>repo-local|user<TAB>absolute-path`, with no header, sorted by
  name.
- **`pawl run` refuses a version mismatch** with the plugin (exit 4). See Internal below.

### 4. Hook entry points, for use without the plugin

`pawl hook pre`, `pawl hook stop` and `pawl hook subagent-stop`, bound to the Claude Code events
`PreToolUse` (matcher `Bash`), `Stop` and `SubagentStop`. See
[install.md](install.md#hooks) and `hooks/hooks.json`.

- Allow is exit 0 with no output. A deny or block is exit 0 with Claude Code's JSON decision on
  stdout. `pawl hook` never exits 2.
- Standing rule: requiring a new hook event in 1.x counts as a break. Any new hook subcommand must
  degrade safely when it is not wired, or when the binary is older. The precedent: without
  `SubagentStop`, `Stop` still works and one race remains.
- Wrap the calls so that any non-zero exit becomes exit 1 (fail open), as the plugin's
  `bin/pawl-hook` does. See [install.md](install.md#hooks).

### 5. Resume across 1.x

A run directory carries a format stamp (`journal.Format`, currently 2). A run with no stamp is
format 1.

- A later binary resumes runs written by every earlier format. Downgrade is not promised.
- Adding an event kind bumps the format.
- A run written by a newer format stays visible: `pawl status` lists it. `pawl run`, `pawl submit`
  and `pawl poll` refuse it (exit 4) and name the format. `pawl abandon --run` still works on it.
- A run whose journal cannot be replayed at a known format also stays visible: `pawl status` lists
  it as an `unreadable` entry with exit 0. Driving it (`pawl run`, `pawl submit`, `pawl poll`) exits
  5; it is never silently skipped. The same goes for a run that fails to load (an unreadable or
  unparsable `plan.json`, or a pinned workflow that fails to load): `pawl status` lists it as
  `unreadable`, with the load error as its `reason`, so it never hides the other runs.
- On resume, the digest of the current workflow and the digest of the workflow pinned in the run
  are both recomputed by the running binary and compared. So `pawl update` in the middle of a run
  is safe within 1.x.

The journal and `plan.json` shapes are internal in format and compatible in practice: this promise
is about resuming, not about reading those files yourself. Any change to how a run is keyed or
recorded, including the attempt-key algorithm, is a run-format change and falls under it.

### 6. Defaults

Frozen. Changing an existing default is a break. A field added in 1.x brings its own default as
part of that addition.

| Name | Default | Applies to |
|---|---|---|
| `attempts` | 1 | `deterministic`, `agentic` steps |
| `max_visits` | 10 | every step |
| `max_steps` | 200 | the workflow |
| `max_items` | 20 | `foreach:` |
| `every` | 60s | `wait` steps |
| `emits` | `json` | `deterministic`, `wait` steps |
| wall-clock ceiling | 10m | every exec'd command: `run:`, each `poll:` tick, a postcondition `command:`, an invariant `check:`, and each `!cmd` context entry |
| kill sequence | SIGTERM to the process group, then SIGKILL no sooner than 5s later | a command that hits the ceiling |

`timeout:` on `human` and `wait` steps has no default; it is required. `retry:` has no default
backoff; `max_attempts:` and `backoff:` are both required.

### 7. Semantics frozen by decision

- **Attempt keys.** The same whitespace-normalised failure text yields the same attempt key, and
  `attempt_key:` overrides it. The key algorithm itself is internal.
- **Stale submit.** `pawl submit` for a step the cursor has moved past is refused (exit 4). There
  is no agentic timeout.
- **Numbers.** A number in state survives a resume exactly, including integers beyond 2^53.
  `equals:` compares two numeric values by value, so `1000000` matches `1e6`, on a fresh run and
  after a resume alike.
- **Human deadline.** A `human` answer submitted after its deadline routes `timeout`, whatever the
  answer was.
- **Wait deadline.** A `wait` deadline is tied to the visit. A crash resume keeps the original
  deadline. A resume of a blocked run starts a new visit and a new deadline.
- **Guards** stay, advisory and pattern-matched. Invariants are the engine-checked guarantee.
- **`when:`** is rejected. A conditional is a router step plus `outcomes:`
  (format-spec [§B.14](../design/format-spec.md)).
- **`all_set:`** `all_set: [k…]` passes iff every k is present in state AND its value is not
  empty. Empty = null, `""`, `false`, numeric zero (any spelling: `0`, `0.0`, `-0`, `0e5`), `[]` or
  `{}`. A key that holds its declared `default:` is present. So a default of `0`, `""`, `false`,
  `[]` or `{}` still fails `all_set`, and any other default passes.

## Internal

Free to change in any 1.x release.

- The `DISPATCH`/`ASK`/`TERMINAL` instruction grammar between the binary and the `/pawl` skill, and
  the flags of `pawl submit` and `pawl poll`. These are driver-internal commands the skill runs. The
  skill and the bundled binary ship together in the plugin. The guard that keeps them in step is a
  binary-to-plugin version match. The plugin's `bin/pawl` launcher exports `PAWL__PLUGIN_VERSION`
  (the plugin's own version), and `pawl run`, on both start and resume, refuses a mismatch with exit 4,
  naming both versions ([cli.md#pawl-version--human](cli.md#pawl-version--human)). The check is
  skipped when the variable is unset (`pawl` not run through the plugin's launcher), and for a `dev`
  source build. Because the grammar is internal, a skill and a binary from different releases are
  never expected to work together.
- The run-directory layout, `live/`, heartbeat and driver files, and the shapes of `plan.json` and
  `events.jsonl` beyond the resume promise above.
- Human-readable output, including the guard banner text.
- Enforcement mechanics a script cannot depend on, with their current values: the enforcement
  heartbeat TTL (5 minutes), the `SubagentStop` marker lifetime (60 seconds), and the 2 second wait
  for a killed command's output pipes.
- The attempt-key hashing algorithm.

## Release candidates and the soak

1.0.0 is preceded by `1.0.0-rc.N` pre-releases; see [releasing.md](releasing.md#pre-releases-release-candidates).

- During the rc soak, fixes ship as the next rc (`1.0.0-rc.N+1`), never as 0.x patch releases.
  `scripts/release/batch.sh` enforces this: while `.changes/rc/` holds fragments, it refuses to cut
  an explicit version other than the rc's target.
- A change to this contract during the soak means a new rc and a restarted soak window.
- A pre-release is never marked "latest" on GitHub, so `install.sh` and `pawl update` skip it. Ask
  for one by version: `PAWL_VERSION=… install.sh` or `pawl update --version vX.Y.Z-rc.N`.

## Reviewing a change against this contract

- Does it touch a public surface above? If so, is it purely additive?
- Does it make the validator reject something it accepted before? That is a break.
- Does it change an existing default? That is a break.
- Does it need a new hook event wired? That is a break unless it degrades safely when unwired or
  when the binary is older.
- Does it add an event kind or change `plan.json`'s shape? Bump `journal.Format`, keep the decode
  path for old formats, and add a fixture.
- Does it change how a run is keyed or recorded? Treat it as a run-format change.
