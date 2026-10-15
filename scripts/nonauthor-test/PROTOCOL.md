# Non-author test: protocol

How to run the non-author test and judge its result. The scripts and their options are in
[README.md](README.md); this file covers the rules around them: what the tester may read, how to
observe, what counts as a pass, and what to do with the friction log.

## Goal and principle

A fresh Claude Code session that has never seen agent-pawl gets what a stranger gets: the released
plugin (which bundles the binary) and the published docs. It authors a realistic workflow, runs it to
`TERMINAL ok` through pawl, and writes a friction log. The log is the main deliverable: it shows
where the docs and the tool's messages fail someone who cannot read the source.

The design rule: **the tester's only channel to pawl knowledge is the shipped artefacts.** Every
other path is either closed off physically (the sandbox) or caught afterwards (the audit).

## What the tester may read

| Allowed | Not allowed |
|---|---|
| `~/pawl-docs/`: `README.md`, all of `docs/` (examples included), `DESIGN.md` and `design/format-spec.md`, laid out as in the repo so relative links resolve | `internal/`, `cmd/`, `e2e/`, `testdata/`, `AGENTS.md`, `.claude/`, `scripts/`, VCS metadata, `.changes/` |
| the installed plugin directory (the skill, hooks and wrapper: that is the shipped product) | browsing the repo on GitHub (`WebFetch`/`WebSearch` are denied in the sandbox settings; `gh` or `curl` against the repo is a violation the audit catches) |
| `pawl --help`, `pawl <cmd> --help` and every error message | the host's real home directory or `~/.claude`, other sessions, this repo on disk |
| general knowledge (Go, shell, YAML) | asking anyone anything (see below) |

The docs snapshot is what a stranger reaches by following links from `README.md` without opening
code. A doc link that points into code or elsewhere outside the snapshot stays dead, and that is
real friction worth logging. `docs/examples/` is included because copying from examples is what
strangers do; it is also why the task is not the `green-tests` example.

## Isolation model

Three layers. Each one alone would leak; together they don't.

1. **Release artefacts only.** `prep.sh` builds a throwaway sandbox root (`ROOT`) with its own
   `home/`, `claude-config/`, `bin/` and Go directories, using `scripts/e2e-install/lib.sh`. It
   installs the plugin through the real `claude plugin` CLI from a directory marketplace built from
   the release's own zip and `marketplace.json`, so no repo clone is present. (The live-marketplace
   install clones the whole repository into the config directory, so `SOURCE=live` is refused.) The
   docs snapshot is extracted into a staging directory outside `ROOT`, only the allowed subset is
   copied in, and the staging directory is deleted.
2. **Filesystem namespace (bubblewrap).** `enter.sh` runs everything the tester does with the host
   filesystem read-only, empty tmpfs over `/home`, `/root`, `/tmp`, `/var/tmp`, `/run`, `/mnt` and
   `/media`, and `ROOT` bound back at the same absolute path (the plugin registry records absolute
   paths). It uses a fresh `/proc` in its own pid namespace, private IPC, `--die-with-parent`, and
   `--clearenv` followed by an explicit environment: no `TMUX`, `SSH_AUTH_SOCK`, tokens, `ANTHROPIC_*`
   or `DBUS` variables. Network stays on, because the API and the Go proxy need it. That is the one
   residual hole, and layer 3 covers it.
3. **Policy and audit.** The sandbox `settings.json` denies `WebFetch` and `WebSearch`, and the brief
   states what may be read. After the run, `audit.sh` searches the tool calls in the tester's session
   transcripts for repo-source access (GitHub blob/tree/raw URLs, `git clone`, `gh repo`), source-only
   paths (`internal/`, `cmd/pawl`, `AGENTS.md`) and any web tool use. A hit is reviewed by a person.
   A real source read invalidates the run: its friction log is still triaged, but a fresh tester
   repeats the test.

**Account side.** The session logs in with a real claude.ai account, and that account's claude.ai
connectors (mail, drive, chat) are reached over the network, so the filesystem sandbox cannot block
them. The two documented switches turn them off
([docs](https://code.claude.com/docs/en/mcp#disable-claude-ai-connectors)): `ENABLE_CLAUDEAI_MCP_SERVERS=false`
in the sandbox environment (`enter.sh`) and `"disableClaudeAiConnectors": true` in the sandbox
`settings.json` (`harden.sh`, called by `prep.sh`). `harden.sh` also denies `SendMessage`, `ListAgents` and
`RemoteTrigger`, built-in tools that reach the logged-in account's other sessions; per the docs, deny
rules hold even in bypassPermissions mode. Plugins synced from the claude.ai account (including organisation
plugins) are turned off with the documented `syncClaudeAiPlugins: false` ([docs](https://code.claude.com/docs/en/plugins/loading)),
and `launch.sh` also passes `--disallowedTools "mcp__*"` so no MCP tool reaches the model. For a sandbox prepped earlier, apply the same settings with
`bash scripts/nonauthor-test/harden.sh "$ROOT"`, then `bash scripts/nonauthor-test/verify-isolation.sh "$ROOT"`.
Sessions start in `~/textstats` because trust accepted in the home directory is never persisted
([docs](https://code.claude.com/docs/en/permissions)), so every home-directory launch would stall on the
trust dialog; `harden.sh` seeds the documented `hasTrustDialogAccepted` flag for that folder.

`verify-isolation.sh` checks from inside the sandbox that the real home and repo are absent, that no
source files are reachable, that the environment is clean, and that the installed plugin and binary
versions match. Any failure aborts. `prep.sh` runs it, and you run it again right before launch.

The tester runs with `--dangerously-skip-permissions` only because the boundary is layer 2 plus the account-side settings above.
Unattended permission prompts would stall the run, and a person answering them would be a coaching
channel. Record this in the report. The tester uses the claude CLI's default model (no `--model`),
as a stranger would; `audit.sh` prints which model it was.

## What is tested

Test a snapshot build of the branch you intend to ship, not a published release, so that fixes the
test provokes can land before the release. `REF=main bash scripts/nonauthor-test/prep.sh` clones
fresh from GitHub and builds a goreleaser snapshot, with the version `<plugin.json
version>-nonauthor.<sha>`. The binary version, the plugin version and the launcher's version must all
agree (`verify-isolation.sh` checks this). Never load the plugin with `--plugin-dir` from a checkout:
that puts source in the sandbox and uses a PATH binary, which is not what a stranger gets.
`SOURCE=local TO=x.y.z` tests a published release instead.

## The task

The workflow must contain at least one deterministic step, one agentic step, an outcome route and an
engine-evaluated postcondition, and run to `TERMINAL ok`. The task is **T2, "lint-fix a small repo
until checks pass"**: `prep.sh` seeds `~/textstats`, a small dependency-free Go module with three
planted problems (an unformatted file, a `go vet` failure, and a logic bug that fails one test). The
workflow runs the checks deterministically and routes on the result (clean goes straight to the ok
terminal; dirty goes to a fix), an agentic step fixes the code given the failing output, the engine
runs the postcondition, attempts are bounded, and a non-ok terminal is reached when they run out.

The other candidate, "red tests to green", is rejected: it is `docs/examples/green-tests` almost
verbatim, so the tester could succeed by copying the example and the test would measure nothing about
authoring. T2 needs two real outcome branches, data passed from a deterministic step into an agentic
step, and a bounded retry loop, and it exercises the plugin's hooks and skill end to end. Expect
30 to 90 minutes.

Wait and human steps are not covered. A human step needs a person to answer, which is a channel into
the tester's session. If wait-step coverage matters, extend T2 with a wait step only.

The exact tester instructions are [brief.md](brief.md) (written to `~/BRIEF.md`) and the log format
is [friction-template.md](friction-template.md) (written to `~/FRICTION.md`). Don't duplicate them
here. The brief describes the task in user terms and never names step kinds, fields or doc sections
beyond "read the pawl docs", because naming them would be coaching.

## Run sequence

1. **Prep.** `REF=main bash scripts/nonauthor-test/prep.sh`. It prints `ROOT=...` and the verify
   result. On any FAIL, stop and fix the kit.
2. **Login, once, interactive.** The person running the test runs
   `bash scripts/nonauthor-test/launch.sh "$ROOT" <slug> --login`, attaches to the tmux session,
   completes onboarding and `/login`, types `/exit`, detaches and kills the session. Credentials are
   written only under `ROOT`. Never copy credentials from a real `~/.claude`.
3. **Smoke launch, same sandbox, neutral prompt.** The tester-launch path must not get its first use
   on the real run. `launch.sh` has no flag for a neutral prompt, so start the session by hand
   (`KIT=scripts/nonauthor-test`, from the repo root):

       command tmux new-session -d -s brain-worker-nonauthor-smoke -x 220 -y 50 "$PWD/$KIT/enter.sh $ROOT"
       command tmux send-keys -t brain-worker-nonauthor-smoke -l 'cd ~/textstats && claude --disallowedTools "mcp__*" --dangerously-skip-permissions "Print the output of the command pawl version and of ls ~, then stop."'
       command tmux send-keys -t brain-worker-nonauthor-smoke Enter

   With `tmux capture-pane -p -t brain-worker-nonauthor-smoke`, confirm that no dialog appears (no
   trust, bypass-permissions or onboarding prompt), that the composer comes up, that `pawl version`
   equals the version `prep.sh` printed, and that `ls ~` shows `BRIEF.md`, `FRICTION.md`, `pawl-docs`
   and `textstats`. Also run `/mcp` in the pane (or `claude mcp list` through `enter.sh`): it must show
   zero servers, and `claude plugin list` must show no "Synced from claude.ai" section; anything else, STOP. Then `/exit` and kill the smoke session. If any dialog or version mismatch shows
   up, stop: that is a kit fix before the real run.
4. **Remove the smoke from the sandbox.** Move the smoke transcript
   (`$ROOT/claude-config/projects/*/<smoke-session-id>.jsonl`, the only `.jsonl` present at that
   point) to `$ROOT.smoke/`, outside `ROOT`. Check that no auto-memory was written
   (`$ROOT/claude-config/projects/*/memory/` absent or empty; move it out too if not). This keeps the
   smoke out of `audit.sh` and stops the tester reading the smoke's transcript or memory.
5. **Verify, then launch.** `bash scripts/nonauthor-test/verify-isolation.sh "$ROOT"`, then
   `bash scripts/nonauthor-test/launch.sh "$ROOT" <slug>`, a fresh session. Record the smoke result.
6. **Observe** (next section).
7. **Audit.** `bash scripts/nonauthor-test/audit.sh "$ROOT"`: exit 0 clean, 1 hits to review, 2 no
   transcripts.
8. **Cross-check from outside.** After the tester stops, run `pawl status --json` through
   `enter.sh`, and re-run the three checks in `~/textstats` (`gofmt -l`, `go vet`, `go test`).
9. **Collect evidence, then tear down.** Copy `FRICTION.md`, the tester's workflow file, the audit
   output, the status JSON and a pane capture of the end of the run out of `ROOT`, and record the
   results. Then `rm -rf "${ROOT:?}" "${ROOT:?}.host.env"`; `ROOT` holds sandbox-only credentials, so
   it isn't kept. `lib.sh` also leaves a `pawl-e2e-report.*` directory under `E2E_TMP` to remove.

## Observation

Observation is read-only: `tmux capture-pane -p -t brain-worker-<slug>` (the session name
`launch.sh` uses), and reading `ROOT/home/FRICTION.md`, the tester's workflow file and the pawl run
directory from outside the sandbox. Never `send-keys` and never attach with input. No coaching: no
hints, no confirmations, no "you're close". Only two interventions are allowed:

- **Environment failure** (broken sandbox, API outage, machine reboot). Restore the environment, for
  example with `launch.sh ... --resume`, and say nothing about pawl. Note the run as
  `env-intervened`.
- **Runaway**: no progress in the friction log or the pane for 45 minutes, or more than 3 hours
  total. Stop it with `/exit` through tmux, a mechanical act and not a message. The friction log as
  it stands is the result, recorded as "did not finish". That is a valid, severe-friction result,
  not a protocol failure.

## Why the tester can't ask questions

- A stranger has nobody to ask. Any answer is coaching, and it would turn the test's most valuable
  signal (where the docs fail) into a private conversation that never reaches the docs.
- Inside the sandbox the tester cannot reach other sessions anyway (no session registry, no tmux
  socket), so the rule matches the mechanism and needs no trust.
- Its questions are exactly the friction items: "I would have asked X" is logged as `doc-missing`,
  with severity blocker if it stopped the tester.
- The cost is that a blocker can end the run early. That is acceptable: an early "did not finish"
  with a precise blocker is a cheap, high-value result, and a second run after the fix confirms it.

## Success criteria

The **run is valid** if:

1. `audit.sh` is clean, or every hit was reviewed and is benign;
2. no environment intervention happened after the tester's first pawl command, or it was purely
   mechanical (a resume);
3. `verify-isolation.sh` passed immediately before launch.

The **product passes** on a valid run if:

1. the tester's workflow reached `TERMINAL ok`, confirmed from outside by `pawl status --json` and
   by re-running the three checks;
2. the workflow meets the floor: `pawl validate` passes, and reading the YAML confirms a
   deterministic step, an agentic step, an outcome route and a postcondition;
3. the code was fixed by the run, not by hand: the journal shows the agentic step's submit and a
   passing postcondition, and the transcript shows no direct edits outside a dispatched agentic step;
4. the friction log exists, with its final report filled in.

Either way the friction log is the deliverable. A pass with many friction items still needs the
triage below before the build is called ready.

## Triage

Classify every friction item:

- **doc-fix**: batch into one docs change.
- **tool-bug / tool-message**: write a short design note, then a small fix.
- **contract**: touches a frozen authoring surface (names, fields, kinds); needs a deliberate
  decision, not a quiet patch.
- **tester-error**: no change, unless a doc could cheaply have prevented it.
- **protocol/env**: fix this kit.

**Re-run rule:** if any blocker or major item got a doc or tool fix, a fresh tester (new sandbox, new
session, never the same transcript) re-runs T2 on the fixed build before release. Otherwise one run
settles it.

## Residual risks

- Network stays on, so a tester could still reach GitHub. Layer 3 (denied web tools, the brief, the
  audit) is the only control there.
- The username is visible inside the sandbox (in the `ROOT` path and via `id -un`).
- Unrelated files on the host (for example a `DESIGN.md` under `/usr/share/doc`) are visible through
  the read-only bind; `verify-isolation.sh` reports them as INFO.
- `launch.sh`'s tester-mode detection (the composer check) is only proven once it has run against a
  real tester launch; treat the first run as also exercising it, and use the smoke launch to catch
  problems early.
