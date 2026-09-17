# NOTES — authoring `dependency-upgrade` against the UX proposal

## Current format

`dependency-upgrade` now exercises: named deterministic outcomes
(`green`/`red`, `upgraded`/`no_changes`); an agentic fix step with automatic
`attempts:`-based retry (no named classifier script); a capped
wait→fix→commit→wait cycle via `max_visits: 5` on `wait_for_ci` routed to an
`exhausted` outcome; guards restricting `push`/`merge` to their one step
each; and two `human` gates with static `options:`. `fix_tests`'s prompt is now an inline
`description:` composed by the model at dispatch time, not `prompts/fix_tests.md` — there is no
`prompts/` directory in this example any more.

One nuance was lost simplifying from the older format: `attempt_key` no
longer takes a `classify:` script, so `fix_tests`'s retry counter is now
keyed off an automatic hash of the postcondition failure text rather than
the explicit `failing_signature` value computed by `run-tests.sh`/
`poll-ci.sh`. In practice these should usually agree (the same failing
tests produce the same postcondition failure text), but nothing links them
formally the way a named classifier script used to.

---

_Written iteratively against drafts of the spec during the design session; section
references to "v1"/"v2" spec drafts refer to those drafts. The current spec is
`design/format-spec.md`._

_Historical note: this file's discussion of `snapshot:`/restore points (notably in the "v2 port"
section below) describes a mechanism that was later removed from the spec entirely in favour of
fix-forward semantics (§B.10). Left as-is below as a record of the design session; it does not
describe current engine behaviour._

Project shape: Python + uv (`pyproject.toml` + `uv.lock`), GitHub-hosted, `gh`
for PR/CI. Chose uv over pnpm because the spec's own worked example
(`ship-change.yaml`) is already git/GitHub-flavored, and uv's `--check` flag
gave me a clean, free invariant check (see §5).

No fields were invented outside the spec's table. Two spots use `# PROPOSED`
comments to flag places where I resolved an internal inconsistency in the
spec rather than adding new vocabulary — both on the `human` kind's
`timeout:` field (see §6).

## 1. Walkthrough: happy run and crash-then-resume

I don't know the actual state-file schema (the spec deliberately keeps that
out of authoring — "run state location ... never authored"), so these are
inferred snapshots based on what the spec says the engine tracks: current
step, attempt counters, captured `writes:`, and resume prints "Restored: ...
Replayed N deterministic steps from cache."

> **Superseded (resolved inconsistency #15).** "Replayed N deterministic steps
> from cache" describes memoization, which `DESIGN.md` §4 explicitly rejects:
> steps before the cursor are not re-executed *because the cursor has moved
> past them*, not because their results were cached and replayed. The actual
> resume line, per `docs/running.md`, is `run <id>  <workflow>  resumed at
> <step> (attempt n/m)` followed by a `restored <keys>` line — no "replayed"
> language, no cache. Left below for the historical record of the walkthrough.

```
$ wf run dependency-upgrade
run 9c2  dependency-upgrade  resumed at fix_tests (attempt 2/4)
restored  baseline_status, baseline_lockfile_hash, upgrade_diff,
  changed_packages, test_status, failing_signature
```

**Happy run**, abbreviated:

```
step=snapshot_baseline attempt=1        state={}
→ writes baseline_status=green           state={baseline_status: green, baseline_lockfile_hash: "a1b2..."}
step=bump_deps attempt=1
→ writes upgrade_diff, changed_packages  state={..., changed_packages: [{name: requests, from: "2.31.0", to: "2.32.3"}]}
step=run_tests_after_bump attempt=1
→ writes test_status=passed              state={..., test_status: passed}
step=commit_and_pr attempt=1
→ writes branch, pr_number               state={..., branch: "deps/upgrade-2026-09-16", pr_number: "418"}
step=wait_for_ci (polling every 60s)
→ outcome=SUCCESS                        state={..., ci_status: SUCCESS}
step=approve_merge (human, waiting)
→ writes merge_decision=approve          state={..., merge_decision: approve}
step=merge_pr attempt=1
→ terminal=done                          state={..., status: ok}
```

This part is unambiguous. Every transition is either a command exit code, a
poll token, or a human answer validated against `options_from` — there's no
point where I had to guess what "next" means.

**Crash-then-resume**, at `fix_tests` mid-attempt:

```
step=run_tests_after_bump attempt=1
→ writes test_status=failed, failing_signature="e4f1..."
step=fix_tests attempt=1 (attempt_key=e4f1...)
→ subagent runs, writes fix_summary/fix_action, postcondition fails
[CRASH — process killed here]

# resume: (superseded phrasing — see the resolved-inconsistency #15 note above; current
# form is `run <id> <workflow> resumed at <step> (attempt n/m)` / `restored <keys>`)
$ wf run dependency-upgrade
Resuming run 9c2 at fix_tests (attempt 2/4, attempt_key=e4f1...).
Restored: baseline_status, baseline_lockfile_hash, upgrade_diff,
  changed_packages, test_status, failing_signature.
```

This is where the spec is *almost* unambiguous but leaves one real question
unanswered: attempt 1's partial work (the subagent may have already edited
files on disk before the crash) is not "run state" in the `reads:`/`writes:`
sense, so it isn't restored or rolled back by the mechanism the spec
describes — only declared state keys are. The spec says agentic steps are
"not memoized on resume" (§C), which I read as: attempt 2 starts clean from
the engine's point of view (a fresh subagent invocation, previous
attempt's *output* handed back as context per §C item 5), but the working
tree is whatever attempt 1 left on disk when it crashed — possibly a
half-applied edit. Nothing in the spec says the engine snapshots or restores
the working tree around an agentic attempt. For a step that is explicitly
allowed to edit source files, this seems like a gap worth naming rather than
silently assuming "it's fine because attempt 2 will just fix it forward" —
that assumption is doing real work and isn't stated.

## 2. What the format could not express

- **A "no changes to make" branch for deterministic steps.** `bump_deps`
  might find nothing to upgrade. A deterministic step's outcome vocabulary is
  fixed to `success`/`failure` (per the field table: "exit 0 = success,
  non-zero = failure") — there's no way for `run:` to signal a third outcome
  the way a `wait` poller's stdout token can. Workaround: I just let
  "nothing to upgrade" be `success` with an empty `changed_packages: []`, and
  let `run_tests_after_bump` run (redundantly, against unchanged code) rather
  than branch around it. It works but wastes a full test run on every
  no-op upgrade. A `kind: deterministic` step that could also emit named
  outcomes via stdout (the way `wait` does) would remove this — but that
  blurs the deterministic/wait boundary the spec is otherwise careful about,
  so I didn't propose it as a field; I just noted the friction.
- **A static, inline option list for `human` steps.** `options_from` in the
  worked example points at a dynamic state key (`${reviewer_candidates}`).
  Both of my human gates have a fixed two-option set known at authoring time
  (`pin_and_continue`/`abandon`, `approve`/`reject`). There's no `options:`
  field for a literal inline list, so I had to declare a `state:` key with a
  `default:` just to hold a constant — a state key that is "written" by
  nobody and exists purely to satisfy `options_from`'s expected shape. This
  passes `wf validate` rule 2 ("key written but never read" — it *is* read)
  but would probably still trip rule 1 ("a step reads a key nobody writes on
  any path reaching it") unless a `default:` counts as an implicit write,
  which the spec doesn't say explicitly. Minor, but a static
  `options: [a, b]` field would be one line vs. a phantom state key.

## 3. Loop-capping and `attempt_key` ergonomics

`attempt_key: "${failing_signature}"` was enough syntactically, but it
pushed real work onto the deterministic steps around it: the *step itself*
just declares a template reference, but something has to actually *compute*
a stable signature from "which tests are failing right now" — that's
`scripts/run-tests.sh` and `scripts/poll-ci.sh` both hashing sorted failing
test IDs into `failing_signature`. The spec's own example computes its
stable key from an already-structured `findings[0].file:category` — i.e. it
assumes some earlier step already produced structured, indexable data.
Ours doesn't have that naturally (pytest output is text), so "was
`attempt_key:` enough?" — yes for the *field*, but only because I pushed a
nontrivial classification job (turning raw test output into a stable id)
into two separate scripts that both have to agree on the same hashing
scheme. That agreement is not checked by `wf validate` anywhere in the
spec's rule list — two failure-key generators drifting out of sync would
silently break the "same failure counts down" guarantee, and nothing would
catch it statically. Open question #2 in the spec ("does attempt_key want a
named classifier script?") is real: I'd have taken `classify:
scripts/failure-key.sh` immediately if it existed, to guarantee both call
sites use the same script instead of two hand-written hashes I have to keep
consistent by hand.

The bigger structural question is the **outer loop**:
`wait_for_ci` → `fix_tests` → `commit_and_pr` → `wait_for_ci` is a 3-step
cycle, not a self-loop on `fix_tests`. Validate rule 9 says "a cycle with no
`attempt_key`/visit cap" is an error, but `attempt_key` is a per-step field
on the agentic node inside the cycle, not a property of the cycle as a
whole. I'm relying on an assumption the spec never states outright: that
`fix_tests`'s `attempts: 4`/`attempt_key` counter persists and keeps
counting down across re-entries into the step via the CI-failure route, not
just the initial route from `run_tests_after_bump`. If the counter instead
resets whenever the *entry edge* changes (say, because attempts are somehow
scoped per incoming transition rather than per `(step id, attempt_key)`),
this loop is uncapped in practice even though `fix_tests` itself declares a
cap. I could not resolve this from the spec text and flagged it as an
assumption inline in `workflow.yaml`.

## 4. How `wait` and `human` felt to author

`wait` was easy and symmetric: `poll:`/`every:`/`timeout:`/`outcomes:` maps
directly onto "run a script, get a token, branch on it" — no surprises, and
the required `timeout:` (spec calls this out explicitly as fixing an unbounded-wait
stall risk) made me actually think about a real CI timeout (2h) instead of
leaving it open-ended.

`human` was harder, for two reasons: (a) the static-options problem in §2,
and (b) the `timeout:`/`on_timeout:` field split described in §6 below,
which I could not resolve confidently from the field reference table alone
and had to infer from prose elsewhere in the doc. Once past those two
questions, `question:`/`writes:`/`accept: in_options` was straightforward,
and the "anything not in the list routes to `on_timeout`, writes nothing"
semantics (matching what a hand-built version of this would do) is a genuinely good
default I didn't have to think hard about.

## 5. `soft: true` postconditions needed

None. Every postcondition in this workflow bottoms out in something
machine-checkable: exit codes, `uv lock --check`, `gh pr view ... | grep`,
or a real test run. This is a property of the domain (dependency upgrades
are inherently "tests pass or they don't"), not evidence that `soft:` is
unnecessary in general — I'd expect to need it the moment a step involves
something like "the fix summary is a good enough description," which this
workflow's `fix_tests` step deliberately avoids needing (the postcondition
is "tests pass," not "the explanation is good"). Worth flagging: `fix_tests`
*writes* a `fix_summary` string whose quality nobody checks at all — it's
not `soft: true` labeled because it isn't part of the postcondition; it's
just unchecked output that happens to ride along. The spec's `soft:` census
(validate rule 7) wouldn't catch this because `soft:` only applies to
postconditions, not to arbitrary unchecked `writes:` fields — a smaller gap
than an unlabeled soft postcondition, but the same shape of problem.

## 6. Ambiguities I had to guess

1. **`human` + `timeout:`.** The field reference table lists `timeout` as
   required only for `wait`, and `on_timeout` for both `human`/`wait`. But
   §H ("On #2") states outright that wait and human "should share one
   vocabulary (`timeout:`, `on_timeout:`)". I added `timeout: 24h` to both
   human steps and marked it `# PROPOSED`, reading §H as authoritative over
   the field table's omission — but a strict reading of the table alone
   would say `human` steps have no `timeout:` field at all, only
   `on_timeout:` (implying an author-invisible/unbounded wait with only a
   defined *escape step*, not a defined *duration*). I could not tell which
   reading validate rule 10 intends when it says a human step needs
   "`timeout`/`on_timeout`" — the slash reads as "either," which would make
   my addition unnecessary. I kept it anyway since an unbounded human wait
   with no duration seems like exactly the kind of stall the `wait` timeout
   requirement was designed to prevent, and "kept `on_timeout` but had no
   `timeout`" seemed like the worse guess if I was wrong.
2. **Whether a step's `catch:` can target another *step* (not just a
   terminal).** The worked example only shows `catch: [{on: failure, next:
   blocked}]`, always to a terminal. I used `catch: [{on: failure, next:
   human_fix_decision}]` on `fix_tests`, i.e. routing exhaustion to a normal
   step rather than straight to `blocked`. Nothing in the field reference
   restricts `catch[].next` to terminals — `next`/`outcomes` elsewhere
   clearly can target steps — so I'm fairly confident this is allowed, but
   it's never demonstrated, and it's the one piece of this workflow's
   control flow I'd most want confirmed against a real engine before
   trusting it.
3. **Whether a `default:` on a `state:` key counts as a "write"** for
   validate rule 1 ("a step reads a key nobody writes on any path reaching
   it"). Relevant to `fix_decision_options`/`merge_decision_options` (§2).
4. **Whether `context:` entries can use `${key}` template substitution.**
   §C says only the `goal` file gets `${key}` substitution; `context` entries
   are "file contents and `!cmd` stdout." I therefore kept all dynamic data
   (`${changed_packages}`, `${failing_signature}`) in `goal:` (which does
   support substitution) and used `context:` only for raw command output and
   static files, rather than risk writing `${...}` into a context entry
   expecting it to resolve.
5. **Whether a postcondition failure inside a `catch`-routed step
   auto-populates `blocked_reason`** used in the `blocked` terminal's
   message. Section E says BLOCKED output includes "the last
   postcondition/invariant failure text" as a runtime behavior, and the
   worked example's terminal message references `${blocked_reason}` without
   it ever being declared in `state:` or written by any step — implying it's
   an engine-injected pseudo-key, not authored. I followed the same pattern
   (referencing `${blocked_reason}` in my `blocked` terminal without
   declaring it) on that assumption.

## 7. Verdict on authoring difficulty

Genuinely easy for the linear, deterministic backbone — steps 1, 2, 3, 5,
7b read almost like a checklist, and `reads:`/`writes:` forced me to think
about data flow in a way that caught one real mistake while drafting (I
initially had `commit_and_pr` reading `upgrade_diff` when it only needed
`changed_packages` and `pinned_package` — the unused-read would have been a
`wf validate` warning). The two `human` gates and the fix-loop's interaction
with the outer CI-retry loop are where the format's few underspecified
corners live (§3, §6.1–6.2), and they're exactly the parts of *this*
workflow that most resemble what makes a hand-built version of this hard: bounded retries with
human escape hatches, and a loop that spans more than one step. None of
these are fatal — every open question has a plausible, narrow answer I could
commit to and document inline — but "someone who has only read the hello
workflow and the field table" (the spec's own goal 7 test) would not,
I think, confidently author the `fix_tests`/`human_fix_decision`/
`wait_for_ci` triangle without either guessing (as I did) or hitting
`wf validate` and iterating. The deterministic 80% of this workflow is a
strong argument for the format as designed; the agentic-loop-plus-human-gate
20% is where the spec should tighten field definitions (`human` timeout
semantics, `catch:` targets, static option lists) before calling itself
done.

---

## v2 port

Ported `workflow.yaml` and `prompts/fix_tests.md` to spec v2
(`design/02-format-spec-v2.md`). Zero `# PROPOSED` comments remain — every
field used is in v2's §D table. One PROPOSED comment from v1 (`timeout:` on
`human`) is now just a required field with a code comment explaining *why*
it's required, not a guess.

### 1. Which friction items v2 resolved, and how

- **§2 "no third outcome for deterministic steps."** Fixed directly by
  §B.4: `snapshot_baseline` now emits `green`/`red` as named outcomes (an
  infra crash is still `failure` on non-zero exit, routing to the default
  `blocked` — the two failure modes are finally distinguishable at the type
  level, not just by convention). `bump_deps` emits `upgraded`/`no_changes`,
  so a no-op upgrade now routes straight to a new `up_to_date` terminal
  instead of paying for a full, pointless test run. This is the single
  biggest quality-of-life change for this workflow.
- **§2 "phantom state key for static human options."** Fixed directly by
  `options:` (§B.1). `human_fix_decision` and `approve_merge` now declare
  `options: [pin_and_continue, abandon]` / `options: [approve, deny]`
  inline; the two dummy `*_options` state keys are gone entirely, and the
  choice itself is the outcome token, so `outcomes:` replaces what used to
  be an implicit "read `fix_decision` and branch in the next step" pattern
  I never actually got to author in v1 (there was no next step to branch —
  I'd have needed one).
- **§3 "the outer wait→fix→commit→wait loop's cap is a per-step field
  masquerading as a cycle property."** Fixed directly by `loops:` (§B.5).
  The `ci_fix` loop entry now owns `max: 5` and `on_exceeded:
  human_fix_decision` as a property of the three-step cycle, completely
  independent of `fix_tests`'s own `attempts: 4` (which still caps *retries
  of the same failure signature*, per step). These are now honestly two
  different, both-real caps instead of one field doing double duty I wasn't
  sure it could do.
- **§3 "two scripts must agree on the failure-key hash."** Substantially,
  not fully, resolved by `attempt_key: {classify: scripts/failure-key.sh}`
  (§B.6). I pointed the classifier at the same script used to populate
  `failing_signature` in `run-tests.sh`/`poll-ci.sh`, so there is now one
  canonical definition of "same failure" instead of two hand-rolled hashers.
  This doesn't fully close the gap — see §3 below and open question 4.
- **§6.1 "is `timeout:` even legal on `human`?"** Settled: required, per
  §B.1, exactly the outcome I'd guessed at and marked `# PROPOSED` for. No
  more guess, no more comment.
- **§6.2 "can `catch.next` target a step?"** Settled explicitly in §B.13/
  rule H.3: yes. My `fix_tests` catch → `human_fix_decision` was correct
  and is no longer an assumption.
- **§6.3 "does a `default:` count as a write?"** Settled explicitly in
  §B.13/rule H.1: yes. Directly obsoletes the phantom-state-key workaround
  above (v2 §B.13 says so by name).
- **§6.4 "does `context:` support `${key}`?"** Settled in §B.3: yes, resolved
  before the `!cmd` runs. I used this in `fix_tests`'s new context entry
  (`"!echo Failing-test signature for this attempt: ${failing_signature}"`),
  which I would not have written under v1 without guessing.
- **§6.5 "`${blocked_reason}` in the terminal message — engine magic or
  undeclared bug?"** Settled: it's a listed engine pseudo-key (§B.3), exactly
  as I'd assumed on faith. No change needed to the workflow, just to my
  confidence in it.
- **§5 "no `soft:` postconditions needed."** Still true for the
  machine-checkable steps, but v2's runtime semantics for `soft:` (§B.14 —
  it still executes and gates, just changes bookkeeping and is visible in
  every run's summary, not only at validate time) gave me a *correct* place
  to put the honest-floor pattern for `fix_tests`'s postcondition (verifying
  tests pass proves the fix works, not that it's the *right* fix — that's
  judgement, now correctly labeled `soft: true`) and for three steps
  discussed in §4 below that v1's schema would have forced me to either
  invent a fake check for or leave undeclared.

### 2. Crash-then-resume, redone against `snapshot:` and counter semantics

```
step=run_tests_after_bump attempt=1
→ writes failing_signature="e4f1..."      outcome=failure → fix_tests
step=fix_tests attempt=1 (attempt_key=e4f1..., via classify script)
→ snapshot: auto records restore point R1 before this attempt starts
→ subagent edits src/foo.py, runs partial fix, postcondition fails
[CRASH — process killed here, src/foo.py left half-edited on disk]

# resume:
$ wf run dependency-upgrade
Resuming run 9c2 at fix_tests (attempt 1/4, attempt_key=e4f1...).
Restored: baseline_status, baseline_lockfile_hash, upgrade_diff,
  changed_packages, failing_signature.
Working tree restored to restore point R1 (pre-attempt-1 state) — the
  half-edit to src/foo.py from the interrupted attempt is gone.
Re-running attempt 1/4.
```

This is now unambiguous, and it's a direct, named answer to the exact gap I
flagged in v1: §B.10's "On crash and resume, the tree is restored to the
interrupted attempt's start point and that attempt is re-run" is close to a
verbatim fix for "the spec never says what happens to a half-applied
edit." Two things I still had to infer, both explicitly flagged as such by
v2 itself rather than left silent:

- The attempt counter itself does **not** advance for the crashed,
  re-run attempt — §B.10 says the *interrupted* attempt is re-run, and
  §B.5 says the counter "is incremented on each invocation of the step
  body," which I read as meaning the crashed invocation's increment already
  happened and is not undone, but the re-run is invocation-for-the-same-
  attempt-number rather than a new one. This is a reasonable reading but the
  two sentences (§B.5's "incremented on each invocation" vs. §B.10's "that
  attempt is re-run") are not stated together anywhere, so I'm inferring
  they compose rather than seeing it spelled out. My snapshot above assumes
  "attempt 1/4" stays "attempt 1/4" across the crash, not "attempt 2/4."
- What restores on a **git**-backed repo vs. **jj** differs in a way that's
  operationally relevant but not authorable: §B.10 says git's restore point
  is "a tree object written from a temporary index, recorded but never
  checked out" — meaning resume must *apply* that tree back onto the
  worktree, which is a real filesystem operation with its own failure modes
  (dirty worktree, permission issues) that the spec doesn't describe as
  possibly failing. This doesn't affect what I author, only what I'd want to
  test before trusting resume on a git-only project.

Net: yes, meaningfully more unambiguous than v1. The one sentence I
needed — "on crash and resume, the tree is restored to the interrupted
attempt's start point and that attempt is re-run" — now exists verbatim.

### 3. Still inexpressible / still a gap

- **Two `attempt_key.classify` scripts (or one script, two call sites)
  agreeing.** I pointed `fix_tests`'s classifier and my own
  `run-tests.sh`/`poll-ci.sh` payload code at the same file,
  `scripts/failure-key.sh`, specifically to close this gap myself — but
  nothing in `wf validate`'s rule list (§H) checks that a script referenced
  from `attempt_key.classify` is the *same* script (or produces the same
  values as) whatever a different step's `run:`/`poll:` script does with
  `failing_signature`. Open question 4 in the spec asks this exact question
  and leaves it open. My workaround is authoring discipline, not something
  the format verifies.
- **A machine-checkable postcondition for a `human` or `wait` step.** Not
  new to v2 — no version of this spec has a real check for "did the right
  person approve this" — but v2's richer `outcomes:`/reserved-token model
  makes the *absence* of a meaningful postcondition on these steps more
  visible, since I now have to write `postcondition: "true"` / `soft: true`
  explicitly on three steps instead of it being implicitly true. That's
  arguably a feature (the trust surface is honestly reported now), not a
  gap I hit, but it is more visible than it was.
- **Per-loop attempt-key / issue-diversity check.** Open question 2 in the
  spec names this precisely: `ci_fix`'s `max: 5` cannot distinguish
  "5 rounds fixing 5 different regressions" (healthy) from "5 rounds
  fighting the same regression" (should have escalated after 2). I have no
  way to author that distinction — the loop cap and `fix_tests`'s own
  `attempt_key` are genuinely orthogonal counters that happen to both route
  to `human_fix_decision` on exhaustion, and I can't make the loop cap
  itself aware of *which* failure signature is churning.

### 4. Ambiguity / self-contradiction found in v2

**§D's field table and §H rule 7 vs. §E's own annotated example, on
whether `postcondition:` is required for `human`/`wait` steps.**

- §D's field table lists `postcondition` as **required** (bolded `**yes**`)
  for kind `all`, with no kind-specific carve-out.
- §H rule 7 restates this with no hedge: "A step has no `postcondition:` —
  hard error; `soft: true` is not an exemption from *declaring* one."
- But §E's own annotated `ship-change` example — the spec's canonical
  worked reference — omits `postcondition:` entirely on `wait_for_mr` (a
  `wait` step) and on `choose_reviewers` (a `human` step). Both steps have
  `outcomes:`, `writes:`, and (for the human step) `options:`/`timeout:`,
  but no `postcondition:` field of any kind, not even the
  `soft: true`/`"true"` honest-floor pattern §B.14 recommends for exactly
  this situation.

  This matters for authoring, not just pedantry: if the annotated example is
  right and `wait`/`human` are implicitly exempt (their "postcondition" is
  arguably *subsumed* by reaching a named outcome at all — there is no
  separate "did we really transition correctly" question the way there is
  for a deterministic step's side effect), then the honest-floor
  `postcondition: "true"` / `soft: true` pair I added to
  `wait_for_ci`/`human_fix_decision`/`approve_merge` is pure noise: three
  fields that exist only to satisfy a rule the reference example itself
  doesn't follow. If the field table and rule 7 are right and the example is
  simply incomplete/under-specified, then my three additions are correct
  and `wf validate` would flag the reference `ship-change.yaml` in §E as
  broken. I could not resolve this from the text and chose the stricter,
  bolded, "no exceptions" reading — but I flag it because a v3 spec (or an
  actual validator implementation) needs to pick one and fix the other.

### Summary of remaining `# PROPOSED` markers

None. The workflow is fully v2-conformant against the letter of §D/§H as I
read it; the one open item (§4 above) is a spec self-contradiction I
resolved by choosing the stricter reading, not a field I invented.

### v2.1 alignment

Errata 1 resolves item §4 above in favor of the annotated example: the
`postcondition: "true"` + `soft: true` honest-floor pair on `human_fix_decision`,
`wait_for_ci` and `approve_merge` was noise, not conformance, and has been
removed along with the comments explaining the now-resolved ambiguity.
`postcondition:` remains optional (and omitted) on all three `wait`/`human`
steps in this workflow.
