# NOTES — what this example exercises

## Current format

`manage-mr` now exercises: guards + invariants together; a `wait` step
parsed with `emits: pairs`; a capped multi-step review cycle via
`max_visits:` on `trigger_coderabbit` (20) plus the workflow-level
`max_steps:` backstop (200); and a `human` step using `options_from:` +
`multi: true` over a runtime reviewer list. Every agentic step's prompt is now an inline
`description:` composed by the model at dispatch time, not a `prompts/*.md` file — there is no
`prompts/` directory in this example any more.

One nuance was lost simplifying from the older format:

- **Per-item fix retries.** The old `fix_issues_gate`/`fix_issues`/
  `fix_issues_push` triad tracked a retry budget *per finding* with a
  hand-rolled counter script, and could commit a partial round
  (`partial_committed`) while blocking only on the findings that ran out of
  budget. The single `fix_issues` agentic step now has one `attempts:` (per
  entry) and one `max_visits:` (total entries) budget for the *whole*
  worklist — a run either fixes everything it can and pushes, or exhausts
  and blocks the whole round. Per-item partial success is a later milestone.

Reviewer choice is no longer lost: `choose_reviewers` offers
`collect_reviewers`' own ranked username list via `options_from:` with
`multi: true`, so a human can approve any subset of it directly, and
`AskUserQuestion`'s always-present free-text "Other" covers naming someone
off the list or typing "skip" to decline for now — one `human` step and one
tiny deterministic router (`route_reviewers`) replace the old three-step
approve/alternate/decline detour.

---

`manage-mr` is the largest of the three example workflows and stresses scale
and fidelity: a real GitLab merge-request lifecycle from a fresh branch to an
assigned, un-drafted MR. It is the format's stress test for the primitives
that only matter once a workflow has real branches, real loops and a real
human in it.

**Guards and invariants, used for their two different jobs.** `guards:`
denies the canonical spelling of "un-draft or assign reviewers" everywhere
except `assign_reviewer` — cheap, pre-hoc, and defeatable by indirection.
`invariants:` re-observes reality after every tool call and catches the
*consequence* even when a guard was bypassed. Neither primitive alone would
be enough; the workflow keeps both.

**The gate / act / commit triad** (`fix_issues_gate` → `fix_issues` →
`fix_issues_push`). A deterministic gate step filters findings down to the
still-actionable subset using the engine's own crash-safe counter store
(`pawl count`/`pawl reset`), an agentic step acts only on that filtered list,
and a deterministic commit step records what actually shipped — including a
`partial_committed` outcome that commits what worked and blocks only on
what didn't, rather than failing the whole round.

**A capped multi-step loop** (`loops: review_cycle`). The CI-fix cycle, the
comment-address cycle and a rejected reviewer choice all re-enter through
`trigger_coderabbit`; a single `wait` step's `timeout:` bounds one sojourn
there, but nothing bounds the number of round trips without `loops:`.

**A `wait` step with a real payload.** `wait_for_mr`'s poller line is
`TOKEN value` (`COMMENTS 3`), parsed with `emits: value` into a typed
`comment_count` — not just a bare routing token.

**A `human` step with a dynamic option list.** `choose_reviewers` uses
`options_from:` over a runtime-computed reviewer list, so the outcome is
the reserved token `chosen` and the actual pick lands in `writes:`,
branched on downstream.

See `design/format-spec.md` for the field reference these steps use.
