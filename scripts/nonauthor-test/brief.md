# Your task

You are evaluating a tool called **pawl** (the "agent-pawl" Claude Code plugin), a workflow engine
for Claude Code. You have never used it. The plugin is already installed in this environment
(its version is <TO>). Its documentation is in `~/pawl-docs/` (README.md is the entry point).
These docs and the tool itself (`--help`, error messages, the installed plugin) are your ONLY
sources of information about pawl.

Rules:
- Do not look for pawl's source code anywhere: not on disk, not on GitHub, not via git/gh/curl.
  If the docs send you somewhere you can't reach, that is a finding; log it and carry on.
- Nobody will answer questions. If you are stuck, log it in the friction log and try another
  approach. If you are truly blocked, log it, write the final report, and stop.
- Do not reinstall or upgrade the plugin or pawl.

Goal: `~/textstats` is a small Go project whose checks fail. Using pawl, author a workflow that
gets it to a state where all three checks pass:
  `test -z "$(gofmt -l .)"`, `go vet ./...` and `go test ./...`.
Requirements for the workflow:
1. A step that runs the checks and decides, from the result, whether any work is needed.
   An already-clean project must finish successfully without involving an agent.
2. A step in which an agent fixes the code, given what the checks reported.
3. The engine itself (not the agent's own say-so) verifies that the checks pass after the fix.
4. A bounded number of fix attempts; when they run out, the workflow ends in a failure state.
Then run it with pawl until it finishes successfully. Do not fix the code by hand; the workflow
must do it.

Deliverables (both in your home directory):
- `~/FRICTION.md`: fill it in AS YOU GO, using the template already in it. Log every place where
  the docs confused you, were wrong, were missing, or contradicted the tool, and every place where
  the tool misbehaved or its messages didn't help. Small things count. Do not tidy it up at the end.
- the final-report section at the bottom of `~/FRICTION.md`: the workflow file's path, the run id,
  the final line pawl printed, and a 3–5 sentence verdict on how learnable pawl is.
When both are written, say "NON-AUTHOR TEST DONE" and stop.
