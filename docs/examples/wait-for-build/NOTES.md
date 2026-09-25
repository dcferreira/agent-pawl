# NOTES — what this example exercises

`wait-for-build` is a minimal `deterministic → wait → agentic` chain: `kick_off_build` starts a
background "CI run" and returns immediately, `wait_for_build` polls for it to finish, and
`summarize_build` (only reached on a pass) writes a short prose summary. `kind: wait` is implemented
by the engine; this example is not part of `docs/examples/green-tests` and so is not covered by
`e2e/` — it is an authoring exercise, not a verified-runnable artefact (see AGENTS.md's Layout
section).

## The status file

`kick_off_build.sh` and `check_build.sh` share a plain-text status file at
`docs/examples/wait-for-build/.run/status` (gitignored — it's a runtime artifact, not example content).
`kick_off_build` truncates it fresh on every run so a stale `PASSED`/`FAILED` line from a previous
invocation of this example can never be mistaken for the current run's result. `simulate_build.sh`
appends exactly one line to it, ~17s after being launched.

## Forcing the FAILED path

`force_result` is a workflow `args:` key (`pawl run wait-for-build force_result=FAILED`), not an
ad hoc env var, so both outcomes (`PASSED` → `summarize_build`, `FAILED` → `blocked`) can be
exercised from the command line without editing the workflow or the scripts — consistent with how
`args:` is used elsewhere in this repo's examples (e.g. `manage-mr`'s `mr_url_arg`).

## Judgment calls made against format-spec.md / DESIGN.md

1. **What a non-terminal poll iteration prints.** format-spec.md §B.1 says "`TOKEN` is present if
   and only if the step declares author-named `outcomes:`" — read strictly, every iteration's
   line must carry a token once `outcomes:` exists. But DESIGN.md §3 says only "the first
   iteration whose last-non-empty-stdout line carries a *routed* token ends the loop", implying
   iterations that don't end the loop are expected and must be expressible. `check_build.sh`
   reconciles this by always printing a token — `PASSED`, `FAILED`, or `PENDING` — but only
   `PASSED`/`FAILED` are declared in `wait_for_build`'s `outcomes:` map; `PENDING` is a
   well-formed line that simply doesn't match any route, so `pawl poll` (per its own description)
   logs it and re-polls on the next `every:` tick rather than treating a missing/incomplete build
   result as `failure`. The script always exits 0 for the same reason: a still-running build is
   not a script error, and a non-zero exit is unconditionally `failure` (§B.1), which would fail
   the whole wait rather than let it keep polling.
2. **Where the shared status file lives.** Nothing in the spec pins runtime scratch state to a
   location; `docs/examples/wait-for-build/.run/` keeps it colocated with the example (so the two
   scripts' relative-from-repo-root paths in the YAML are self-explanatory) without being
   mistaken for checked-in example content, hence the `.gitignore` entry.
3. **Why the background launch uses `nohup`.** DESIGN.md doesn't specify process-group semantics
   for a backgrounded `run:` command; `kick_off_build.sh`'s own process is short-lived (it exits
   right after printing its JSON line), so without `nohup` a strict shell could send the
   backgrounded `simulate_build.sh` a SIGHUP when the parent exits. `nohup` was the simplest way
   to make "backgrounded, non-blocking" actually survive `kick_off_build`'s own exit, with no
   extra dependency beyond what's already assumed (`sh`, `date`, `sleep`, `tail`).
4. **`summarize_build`'s postcondition.** format-spec.md's field reference lists
   `postcondition:` as a "shell string, `{command}`, `{all_set}`, or `{equals}`"; a shell string
   checking both non-emptiness and a minimum length (`[ -n ${summary} ] && [ ${#summary} -ge 10 ]`)
   was chosen over bare `{all_set: [summary]}` because `all_set` only checks the key was written at
   all (non-empty per the field reference's own description elsewhere in the repo's examples), and
   the task asked for a check that the subagent "wrote something sensible" — a trivial one- or
   two-character string would satisfy `all_set` but not obviously satisfy "sensible".
