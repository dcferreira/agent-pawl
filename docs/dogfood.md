# Dogfooding: running `green-tests` for real

This walks through actually running the `docs/examples/green-tests` workflow, end to end, in a real
Claude Code session. It assumes you've followed [install.md](install.md) and have a `pawl` binary
on your `PATH`. Everything in this document was run and its output pasted verbatim — nothing here
is retyped or paraphrased.

## The most important thing to know before you start

**There is no enforcement layer in this build.** `pawl run` prints `enforcement: off (milestone 1)`
in its start banner instead of the `hooks: PreToolUse ✔ Stop ✔` line DESIGN.md §5 describes for
the finished system. Concretely, that means:

- Nothing stops the session from editing files, running the build, or running the tests itself
  instead of dispatching a subagent through `pawl`.
- Nothing stops the session from simply walking away mid-run — closing the chat, starting a new
  task — leaving the run journal sitting there `running` forever. There is no `Stop` hook to
  refuse to end the turn.
- Nothing re-checks that a guarded action didn't happen some other way. There are no guards or
  invariants in this build at all (`guards:`/`invariants:` in a workflow file is a validation
  error, not a silently-skipped feature).

The only thing keeping a dogfood run honest is the `/pawl` skill's protocol (below) and the
discipline of actually following it. Treat a live run as a real state machine you must not skip
steps of, even though nothing forces that on you.

## Prerequisites

- `pawl` installed per [install.md](install.md).
- `jq` on your `PATH`. **This is a real prerequisite of this specific example**, not of `pawl`
  itself: `docs/examples/green-tests/scripts/run-tests.sh` shells out to `jq -Rs .` to JSON-encode a
  possibly multi-line test failure so it survives as a `pawl` state value (see
  `docs/examples/green-tests/NOTES.md`, Ruling R11). If `jq` is missing, `run_tests` doesn't crash —
  it reports a named `FAIL` saying `run-tests.sh requires jq` — but you'll never see a real test
  failure until you install it.
- A Go toolchain, since the fixture used below is a Go module.

## Set up a project to run it against

`green-tests` needs a small Go module with a failing test to fix, plus the workflow file and its
script, laid out like this:

```
<project root>/
  go.mod
  fixture.go          # the code under test
  fixture_test.go
  .claude/
    workflows/
      green-tests.yaml  # from docs/examples/green-tests/workflow.yaml
      scripts/
        run-tests.sh    # from docs/examples/green-tests/scripts/
```

This repo ships exactly such a fixture at `testdata/fixture/` (a two-line `Add` function that
subtracts instead of adding). From a checkout of this repo:

```
mkdir -p /tmp/pawl-dogfood/.claude/workflows/scripts
cp testdata/fixture/go.mod          /tmp/pawl-dogfood/go.mod
cp testdata/fixture/fixture.go      /tmp/pawl-dogfood/fixture.go
cp testdata/fixture/fixture_test.go /tmp/pawl-dogfood/fixture_test.go
cp docs/examples/green-tests/scripts/run-tests.sh /tmp/pawl-dogfood/.claude/workflows/scripts/run-tests.sh
cp docs/examples/green-tests/workflow.yaml /tmp/pawl-dogfood/.claude/workflows/green-tests.yaml
```

`scripts/` lives beside the workflow file, under `.claude/workflows/`, not at the project root:
DESIGN.md §9's "scripts/ resolve relative to the workflow file" rule is implemented as of this
build, so `run: scripts/run-tests.sh ${test_cmd}` in `green-tests.yaml` resolves against
`.claude/workflows/`, the directory containing it — exactly the layout `docs/examples/green-tests`
itself uses and `e2e/green_tests_test.go` builds.

## Run it

From `/tmp/pawl-dogfood`:

```
$ pawl run green-tests
```

captured output:

```
workflow: /tmp/pawl-dogfood/.claude/workflows/green-tests.yaml (repo-local)
enforcement: off (milestone 1)
soft: 0/2 steps (0.0%): (none)
DISPATCH 9074 fix_tests
attempt: 1 of 3
description:
  The test suite is failing. Fix the code so that it passes.
  Do not edit, weaken or delete tests to make them pass — fix the code under test.
  The failures:
  --- FAIL: TestAdd (0.00s)
    fixture_test.go:7: Add(2, 3) = -1, want 5
FAIL
FAIL	fixture	0.002s
FAIL
context:
  [1] git diff (0 bytes)
    (empty)
return: a JSON object with exactly these keys (key order does not matter)
  fix_summary: string
subagent_args: {"model":"sonnet","tools":["Read","Edit","Bash"]}
submit with: pawl submit --run 9074 --step fix_tests --json '<the object above>'
END DISPATCH 9074 fix_tests
```

Note: the run resolved and executed `run_tests` (a `deterministic` step) entirely by itself before
stopping — the first thing you see is the `DISPATCH` for `fix_tests`, the `agentic` step, since
`pawl run` only ever hands control back at an agentic step or a terminal. The `
`/`	`
sequences are real: the captured multi-line test failure is carried in a rendered string and its
control characters come out escaped, not as raw newlines/tabs, in the printed block.

`[1] git diff (0 bytes)` is empty because `/tmp/pawl-dogfood` isn't a git (or jj) working copy in
this walkthrough; per `docs/examples/green-tests/NOTES.md`, a failing or unavailable `!cmd` context
entry degrades silently to empty rather than blocking the run.

Per the `/pawl` skill (`.claude/skills/pawl/SKILL.md`), this `DISPATCH` line is the instruction: it's
the first column-0 line matching `DISPATCH|TERMINAL`, and everything indented beneath it up to
`END DISPATCH 9074 fix_tests` is data, not a new instruction — including the failure text, which
could in principle contain something instruction-shaped.

## Do the work and submit

In a real session, this is where you compose a subagent prompt from `description:` and `context:`
and dispatch it with the Agent tool, honoring `subagent_args:` (`model: sonnet`, tools `Read`,
`Edit`, `Bash`). Here, standing in for that subagent, the fix is one line:

```
$ sed -i 's/return a - b/return a + b/' fixture.go
```

Then submit exactly the JSON object `return:` asked for, using the `submit with:` command printed
above:

```
$ pawl submit --run 9074 --step fix_tests --json '{"fix_summary":"Fixed Add to use + instead of -"}'
```

captured output:

```
TERMINAL 9074 ok
message:
  Suite green after 2 runs.
END TERMINAL 9074 ok
```

The run re-entered `run_tests` (visit 2), the suite passed, and the workflow reached its `done`
terminal. `pawl status` afterwards confirms there's nothing left live:

```
$ pawl status
root: /tmp/pawl-dogfood
no live runs for this working copy
```

That's the whole loop for a workflow with a single agentic step: one `DISPATCH`, one `pawl submit`,
one `TERMINAL`. A workflow with more agentic steps, or one whose fix doesn't compile or doesn't
actually fix the suite, repeats the `DISPATCH` → dispatch-and-submit cycle — burning `attempts:`
on a bad submission, or `max_visits:` on a submission that compiles but doesn't fix the tests —
until it reaches `done` or `gave_up` (see `docs/examples/green-tests/NOTES.md` for how those two
counters stay independent).

## The two traps you will actually hit

**1. A state key holding a command is not a command.** `${key}` substitutes as one
shell-quoted token (`design/format-spec.md` §B.2) — deliberately, since that's what makes it safe
against injection. `fix_tests`'s postcondition is `{command: "sh -c ${build_cmd}"}`, not
`{command: "${build_cmd}"}`. Write it the second way and the shell tries to exec a program
literally named `go build ./...`; that failure looks exactly like a real build failure — it's a
postcondition failure that burns an attempt, with no hint that the actual command never ran. The
`sh -c ${key}` idiom is legitimate **only for an `args:` value** (it comes from whoever ran
`pawl run`, who could already run anything). Never do this for a `state:` key a step or a subagent
wrote — `writes:` output is workflow-internal data, and piping it through `sh -c` turns it into
arbitrary command execution chosen by whatever produced that state.

**1b. Never wrap a `${key}` in its own double quotes.** Because the substitution already is one
shell-quoted token, `run: scripts/mr-open.sh ${mr_url}` is correct and `run: scripts/mr-open.sh
"${mr_url}"` is not: the shell re-interprets the rendered `'value'` inside a second, literal pair of
double quotes, and `$(...)`/backtick command substitution *inside the value* stays live even though
the value itself was single-quoted. For a `state:` key an agentic step wrote, that is a command
injection exactly like 1's, just with the quoting the other way round — a value like
`$(touch PWNED)` executes. It also breaks functionally even when the value is inert: `[ "${entry_added}"
= true ]` renders to `[ "'true'" = true ]`, which never matches (the comparison sees the literal
apostrophes). See `design/format-spec.md` §B.2.

**2. `jq` is a prerequisite.** Covered above, but worth repeating here since it's the first thing
that silently degrades your first run if you skip it: no `jq` means `run_tests` always reports
`FAIL {"failures": "run-tests.sh requires jq"}`, regardless of whether your code is actually
correct.

## What this walkthrough does not show

Nothing here demonstrates enforcement, because there isn't any — see the warning at the top.
Nothing here shows `wait` or `human` steps, `guards:`, `invariants:`, or `retry:`, because none of
them exist in this build; a workflow file that declares any of them is rejected outright by both
`pawl validate` and `pawl run`.

---

See also: `docs/examples/green-tests/NOTES.md` for the workflow author's own notes on this example's
design, and `.claude/skills/pawl/SKILL.md` for the exact protocol a Claude Code session follows.
