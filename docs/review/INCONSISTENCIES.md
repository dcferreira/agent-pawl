# Inconsistencies found while writing the guide

**For the design review — not user documentation.** Conflicts between `design/format-spec.md`,
`DESIGN.md` and `examples/`, found by trying to write each behaviour down for a user. Roughly most
consequential first.

1. **Who submits a `wait` result is unspecified.** The `/pawl` skill text in `DESIGN.md` §2 says
   `WAIT` → "run `pawl poll` under Monitor" and stops there. The transcript immediately below it shows
   `pawl submit --run … --step wait_for_mr --token COMMENTS --json '…'`. So either the poller submits
   on its own behalf or the model must, and the skill never says which. The guide assumes the model
   submits what the poller printed.

2. **No submit flags exist for two of the three human answer shapes.** The skill documents
   `pawl submit --option '<choice>'`. A `multi: true` answer is a list and an "Other" answer is free
   text; neither has a documented representation. The guide invents repeatable `--option` plus
   `--other`.

3. **`rejected` is routed in two examples but is not a reserved outcome.** `dependency-upgrade`'s
   `human_fix_decision` and `approve_merge` both route `rejected:`. The spec's reserved set is
   `success`/`failure`/`timeout`/`exhausted`/`chosen`, and `rejected` is not a declared option
   either, so validator rule 1 would reject both steps. Either the examples are stale or the reserved
   list is short one member.

4. **`retry:` has undocumented keys in the examples.** The field table says
   `{max_attempts, backoff}`. `dependency-upgrade` uses `{max_attempts, backoff, multiplier, jitter}`
   and `manage-mr` uses `{max_attempts, backoff, multiplier}`. The guide documents only
   `max_attempts` and `backoff`.

5. **`manage-mr` writes an undeclared state key.** Its `changelog` step has
   `writes: {changelog_updated: {type: boolean}}`, and `changelog_updated` is not in the file's
   `state:` block. Validator rule 11 would reject the shipped example.

6. **The spec's own hello workflow fails validator rule 3.** In §F, `test` is the last step, has no
   `next:`, and is not a terminal. `next:`'s default is given as "fall through", but fall-through is
   never defined anywhere. The guide defines it (next step in the list; from the last step, `done`).

7. **`blocked_reason` is both a pseudo-key and a declared state key.** §B.2 lists it as an
   engine-provided pseudo-key that no step writes; `manage-mr` declares `blocked_reason` in `state:`
   *and* has `resolve_conflict` write it. A step writing a pseudo-key is undefined behaviour — is it
   shadowed, merged, or an error?

8. **Is BLOCKED terminal or paused?** The spec models `blocked` as a terminal status
   (`terminal: {status: ok|blocked}`), while `DESIGN.md` §5 describes the `Stop` hook as "stops
   refusing once the run is `BLOCKED`", which reads like a live-but-halted run. Resumability of a
   blocked run is stated nowhere. The guide says terminal.

9. **First line or last line, for a poller.** `DESIGN.md` §3 says `pawl poll` "prints the **first**
   line whose token is a routed outcome"; §B.1 says the engine reads the **last** non-empty line of
   stdout. Both can be true (first *iteration* whose last line carries a routed token) but the
   wording is a genuine trip hazard for an author.

10. **`timeout` on a `human` step writes nothing, but the blocked terminal reads
    `${blocked_reason}`.** Both examples route `timeout: blocked`, whose message is
    `"Needs a human: ${blocked_reason}"`, and nothing sets `blocked_reason` on a timeout. Either the
    engine populates it for every route to a blocked terminal, or those messages render empty.

11. **`/pawl run` vs `pawl run`.** `manage-mr`'s header comment and `dependency-upgrade`'s NOTES use
    `/pawl run <name>`; the spec's CLI section uses `pawl run <name>`. With the plugin decision now made
    (`/pawl` is the skill, `pawl` is the binary) the examples read as the skill invoking itself.

12. **The examples were written against an older `context:` rule.** `dependency-upgrade`'s NOTES §6.4
    says it deliberately avoided `${key}` in `context:` because substitution was said to apply only
    to `goal:`. §B.2 now says substitution applies to every `context:` entry, including inside a
    `!cmd`. The examples are therefore conservative in a way the spec no longer requires.

13. **"Five commands" is described as four.** §I says "the command surface is four commands plus
    three internal ones" and then lists five (`run`, `validate`, `status`, `abandon`, `list`).

14. **`writes:` form on `human` steps is inconsistent between examples.** `manage-mr` uses the typed
    map (`writes: {reviewers_approved: {type: json}}`), `dependency-upgrade` uses the list form
    (`writes: [fix_decision]`) on static-option steps that do not route `chosen:` — where the spec
    says `writes:` is not required at all. Harmless, but it reads as two different rules.

15. **The NOTES describe a resume behaviour the design cut.** `dependency-upgrade`'s NOTES show
    resume printing "Replayed 3 deterministic steps from cache", while `DESIGN.md` §4 explicitly
    rejects memoization — steps before the cursor are simply not re-executed. The guide follows
    `DESIGN.md`.

## Resolved

Decisions applied across `design/format-spec.md`, `DESIGN.md`, `docs/**` and `examples/**`; see
[DECISIONS-MADE-WHILE-WRITING.md](DECISIONS-MADE-WHILE-WRITING.md) for #8's status (accepted) and
#11/#12's (reversed, renumbered from #14/#15 there).

1. **Who submits a `wait` result.** `pawl poll` does, internally: it takes the same internal path
   `pawl submit` would (write, postcondition, transition, invariants, deterministic continuation) and
   prints the resulting `DISPATCH`/`ASK`/`WAIT`/`TERMINAL` line itself. The model never runs
   `pawl submit` for a `wait`. Spec §B.13; `DESIGN.md` §2–3; `docs/steps/wait.md`, `docs/cli.md`,
   `docs/running.md`.
2. **No submit flags for two human answer shapes.** Confirmed as the guide's own decision (#8,
   accepted): `--option` repeatable for multi-select, `--other '<text>'` for free text. Already
   consistent in `docs/cli.md` and `docs/steps/human.md`; no further change needed.
3. **`rejected:` is not a reserved outcome.** Removed the `rejected:` routes from
   `dependency-upgrade`'s `human_fix_decision` and `approve_merge`; a free-text "Other" answer against
   a static option list is the reserved outcome `chosen`, now routed instead (both steps already
   declare `writes:` for it). Stale prose mentions of "a rejected reviewer choice" fixed in
   `manage-mr/workflow.yaml`'s comments (that workflow never had a `rejected:` route — `route_reviewers`
   only ever produces `skip`/`assign`).
4. **`retry:` undocumented keys.** Spec is authoritative: `{max_attempts, backoff}` only, `backoff` a
   single duration string, retry *n* waits `backoff` × *n* (linear, no jitter). Removed
   `multiplier:`/`jitter:` from all six occurrences across `dependency-upgrade/workflow.yaml` and
   `manage-mr/workflow.yaml`. Spec field table now states the linear-backoff shape explicitly.
5. **`manage-mr` writes an undeclared state key.** `changelog_updated` is now declared in `state:`
   (typed `boolean`, `default: false`).
6. **Spec's own hello workflow failed validator rule 3.** Added an explicit `next: done` to the
   `test` step in `format-spec.md` §F. The "fall-through" rule is removed everywhere it was
   documented (see #11 above).
7. **`blocked_reason` both pseudo-key and declared key.** `manage-mr` no longer declares
   `blocked_reason` in `state:` or writes it from `resolve_conflict`; that step now writes a new,
   ordinary state key `conflict_detail`, and the `blocked` terminal message reads both
   `${blocked_reason}` (engine-set) and `${conflict_detail}` (author-set). Spec §B.2 now says exactly
   how the engine populates `blocked_reason` (see #10).
8. **BLOCKED terminal or paused.** Reversed toward "paused" — see
   [DECISIONS-MADE-WHILE-WRITING.md](DECISIONS-MADE-WHILE-WRITING.md) #11. Spec §B.12,
   `DESIGN.md` §4/§5, `docs/running.md#blocked`, `docs/troubleshooting.md`, `docs/cli.md` exit code 3,
   `docs/faq.md`, `docs/guards-and-invariants.md`, `docs/concepts.md`.
9. **First line or last line, for a poller.** Fixed the wording everywhere it appears: each poll
   *iteration* yields one last-non-empty-stdout-line (same grammar as deterministic); the first
   *iteration* whose line carries a routed token ends the loop. Spec §B.13, `DESIGN.md` §3,
   `docs/steps/wait.md`, `docs/cli.md`.
10. **`timeout` on a `human` step writes nothing, but the blocked terminal reads `${blocked_reason}`.**
    Resolved: the engine sets `blocked_reason` itself whenever a run enters `BLOCKED` — the
    invariant's `message:` on a violation, or a fixed engine string naming the step and outcome
    otherwise (e.g. `"wait_for_ci: timeout"`). It is never empty by the time a `blocked` terminal
    renders. Spec §B.2.
11. **`/pawl run` vs `pawl run`.** The CLI reference documents the `pawl` binary directly; author-facing
    comments and notes now say `pawl run <name>`, matching `format-spec.md` §I. Fixed in
    `manage-mr/workflow.yaml`'s header comment and `dependency-upgrade/NOTES.md`'s transcripts.
12. **The examples were written against an older `context:` rule.** Already resolved in
    `dependency-upgrade/NOTES.md` §6.4/§7 (a prior pass): substitution applies to every `context:`
    entry, including inside a `!cmd`, resolved before the command runs. No remaining stale caveat.
13. **"Five commands" described as four.** Fixed in `format-spec.md` §I ("four" → "five", matching the
    five commands already listed: `run`, `validate`, `status`, `abandon`, `list`) and in `README.md`.
14. **`writes:` form inconsistency between examples.** Not a bug: both the typed-map form and the
    untyped list form are legal shorthand on `deterministic`/`wait` steps (types come from `state:`
    either way); the typed map is required only on `agentic` and on a `human` step's single
    `writes:` key. Already documented this way in `docs/steps/deterministic.md`; no example change
    needed.
15. **The NOTES describe a cut resume behaviour.** Flagged inline in
    `dependency-upgrade/NOTES.md` as superseded, with the correct `running.md` phrasing
    (`resumed at <step> (attempt n/m)` / `restored <keys>`) shown alongside for contrast. Left the
    original walkthrough text in place as a historical record rather than rewriting it.
