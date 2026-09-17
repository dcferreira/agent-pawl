# Decisions made while writing the guide

For the design review, not user documentation. The spec and `DESIGN.md` are silent or ambiguous on
each of these; a user guide cannot be. Format: page · decision · alternative rejected · status.
Reverse any of them and the docs follow.

**Status:** all accepted on first review (2026-09-17). #14 (BLOCKED) and #15 (fall-through) were
**reversed** before review — docs already updated to match — and #27 was a user decision, not
subject to review.

## CLI surface

1. `cli.md` · Exit codes 0/1/2/3(BLOCKED, paused)/4/5, one table for every command · Alt: 0/1 only,
   or one code per refusal kind · accepted.
2. `cli.md` · `wf version` prints binary version + plugin pin + path, exits 4 on mismatch · Alt:
   `wf --version` flag, no pin display · accepted.
3. `cli.md` · `wf validate` accepts a path as well as a name, `--strict` promotes warnings to errors
   · Alt: name only, no strict mode · accepted.
4. `cli.md` · `wf status` flags `--run`/`--all`/`--json`, default scope is live runs for the current
   working copy · Alt: default to all runs machine-wide · accepted.
5. `cli.md`, `running.md` · Seven status words (`running`/`dispatched`/`asking`/`waiting`/`blocked`/
   `ok`/`abandoned`) · Alt: fewer words, JSON only · accepted.
6. `cli.md` · `wf abandon --reason <text>`, journalled and shown in status · Alt: no reason field ·
   accepted.
7. `cli.md` · `wf list` prints name, source (repo/user), path, marks a shadowed user-level workflow ·
   Alt: names only · accepted.
8. `cli.md`, `steps/human.md` · `wf submit` human flags: repeatable `--option`, `--other '<text>'`.
   `DESIGN.md` documents only a single `--option` · Alt: submit all human answers as `--json` ·
   accepted.

## Run output

9. `running.md` · Terminal output is `TERMINAL <run> <status>`, then the terminal `message:`, then
   `N of M advanced on a soft postcondition` · Alt: census only at validate time · accepted.
10. `running.md` · Usage output for a missing required arg lists every arg, type and default ·
    accepted.

## Semantics

11. **REVERSED.** `running.md`, `troubleshooting.md` · Was: a blocked run is terminal, not resumable.
    Now: **BLOCKED is a pause** — `wf run <name>` (or `--run <id>`) resumes it at the step that
    blocked, `attempts:` reset to 1, journalled as an intervention; `Stop` no longer refuses. See
    `format-spec.md` §B.12, `DESIGN.md` §4.
12. **REVERSED.** `writing-workflows.md` · Was: omitting `next:` falls through to the next step in
    the file, and from the last step to `done`. Now: **no fall-through, ever** — every step needs
    `next:` or a complete `outcomes:` map; unrouted is a `wf validate` error. Spec's own hello
    workflow fixed to add an explicit `next: done`.
13. `steps/wait.md` · After a resume the `timeout:` deadline restarts from zero · Alt: an absolute
    deadline from first entry · accepted.
14. `steps/wait.md` · A poll iteration with no token, or an unrouted one, means "not yet"; only a
    non-zero exit is `failure` · Alt: an unrouted token is an error · accepted.
15. `steps/human.md`, `faq.md` · Free text against a static single-select produces `chosen`; if
    unrouted, the run blocks · Alt: re-ask, or reject the answer in the UI · accepted.
16. `steps/deterministic.md` · The engine-wide per-step wall-clock ceiling (10 min) is fixed, not
    configurable · Alt: expose as a flag or workflow field · accepted.
17. `troubleshooting.md` · A lock whose pid is dead is taken automatically; `--force` exists only for
    a recycled pid · Alt: always require `--force` · accepted.

## Installation and paths

18. `install.md` · Paths: plugin `~/.claude/plugins/wf/`, binary `~/.claude/wf/bin/wf`, live-run
    symlinks `~/.claude/wf/live/`, run directories `~/.local/state/wf/` (`DESIGN.md` says only
    `<state_base>`) · Alt: run directories under `~/.claude/wf/runs/` · accepted.
19. `install.md` · Plugin verbs `/plugin install|update|uninstall wf`, plus exact uninstall steps
    (`rm -rf ~/.claude/wf ~/.local/state/wf`) · Alt: an `install.sh`-only flow · accepted.

## Validation

20. `validation.md` · Error message text for all seventeen rules, `file:line:` prefix + named fix,
    success line `ok — N steps, M terminals, K cycles` · Alt: leave messages to implementation ·
    accepted.
21. `validation.md` · The two warnings stay warnings (exit 0) unless `--strict` · Alt: errors always
    · accepted.

## Content choices

22. `faq.md` · Recommended way to test without an LLM: replace agentic steps with deterministic ones
    that `echo` a fixed JSON payload · Alt: ship a `--stub-agentic` mode · accepted.
23. `guards-and-invariants.md` · The exact `PreToolUse` denial text the model sees · Alt: unspecified
    · accepted.
24. `steps/deterministic.md` · The exit-code-to-token wrapper shown as *the* supported pattern, with
    a concrete `case` snippet · Alt: mention only in prose · accepted.
25. `README.md` · A "Not yet" list naming `wf graph`, `--walk`, `--history`, `parallel`, `foreach`,
    plugin-shipped workflows, no milestone numbers · Alt: omit entirely · accepted.
26. `quickstart.md` · The hello workflow writes `hello.txt` in the repo rather than running
    `ruff`/`pytest`, so it works in any repo · Alt: the spec's §F `tidy` workflow · accepted.

## User decision (not pending review)

27. **`goal: prompts/<step>.md` → inline `description:`, composed into the subagent prompt by the
    model at dispatch time.** Replaces the `goal:`/`prompts/` convention across spec, `DESIGN.md`,
    examples and docs. `wf` prints `description`, gathered `context`, the `writes:` schema, `tools:`,
    `model:`, and on retry the previous failure text in `DISPATCH`; the `tools:` allowlist became
    enforced (not advisory) via `PreToolUse`. Status: decided by the user directly.

## Cosmetic (accepted)

Banner line layout (run id/workflow/path on one line, hooks self-test on the next); step-line glyphs
(`✔`/`~`/`↻`/`✗` over words); resume-line wording (`resumed at <step> (attempt n/m)` /
`restored <keys>`, replacing the NOTES.md phrasing describing cut memoization behaviour).
