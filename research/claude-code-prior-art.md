# Prior art: workflow/state-machine orchestration on Claude Code and adjacent agent harnesses

Research date: 2026-09-16.

## 1. Claude Code–specific workflow/FSM prior art

**Official primitives (hooks).** Claude Code hooks are "user-defined shell commands... Claude Code runs them at specific points in its lifecycle, which gives you deterministic control: certain actions always happen rather than relying on the LLM to choose to run them" [verified: https://code.claude.com/docs/en/hooks-guide]. The doc explicitly separates deterministic hooks from "prompt-based hooks" and "agent-based hooks" that use a Claude model for judgment-based checks [verified: https://code.claude.com/docs/en/hooks-guide]. Community summaries (not Anthropic primary source, so treated as `[inferred]` unless noted) describe the concrete enforcement mechanics: a `PreToolUse` hook can return a `permissionDecision` of `"deny"` to block a tool call before it runs, or `"allow"` to bypass the prompt; a `Stop` hook fires when the model wants to end its turn and can refuse to let the turn end, forcing the agent to keep working if completion criteria aren't met (exit code 2 blocks/forces-continue) [inferred, aggregated from https://claudefa.st/blog/tools/hooks/hooks-guide, https://dotzlaw.com/insights/claude-hooks/, https://paul-schick.com/posts/claude-code-hooks-pretooluse-posttooluse/]. This is the key primitive for our project: **order and completeness can be enforced outside the model's compliance**, via `PreToolUse` gating (block step N+1 until step N's artifact exists) and `Stop` gating (refuse to finish until a checklist/state file says "done"). Author format: a `hooks` block inside `settings.json`, keyed by event name and a `matcher` (tool name pattern) mapping to a shell `command` [verified: https://code.claude.com/docs/en/hooks-guide].

**Anthropic's own `Workflow` tool / workflow-authoring.** Distinct from hooks, Claude Code (in the "ultracode"/ agent-SDK config this session runs under) exposes a `Workflow` tool and a `workflow-authoring` skill that is itself a primary source (loaded directly in this session). A workflow is a plain-JS script beginning with a pure-literal `export const meta = {name, description, phases}`, then imperative code using `phase(title)` to group progress, `agent(prompt, {schema, model, effort, isolation})` to spawn subagents (with JSON-Schema-validated structured output), `pipeline(items, stage1, stage2, ...)` for per-item multi-stage fan-out without cross-item barriers, and `parallel(thunks)` as an explicit barrier — plus `budget`, `args`, and `workflow()` for one-level nesting [verified: workflow-authoring skill content, loaded in-session]. This is a genuine deterministic-orchestration DSL: control flow (loops, conditionals, fan-out) is plain JS, not model-chosen, and results can be resumed from a `runId` with cached prefix re-use. It is the closest first-party analogue to a "pipeline engine" but it targets **task decomposition/fan-out for one turn**, not long-lived declarative state machines that non-authors configure — there's no persistent state file, no external YAML config, and the script itself must be authored by/with an LLM each time (though it can be saved and reused by name).

**Community plugins/frameworks (verified to exist by GitHub API 200, not deeply vetted for quality):**
- `NamHT4Devlop/claude-code-workflow-kit` — "Spec-driven dev workflow plugin for Claude Code: plan → code → review → test → evidence... 30 skills, 7 sub-agents, git guard hook" [verified: https://github.com/NamHT4Devlop/claude-code-workflow-kit exists via GitHub API 200; description from search snippet].
- `barkain/claude-code-workflow-orchestration` — "multi-step workflow orchestration — automatic task decomposition, parallel agent execution, and specialized agent delegation with native plan mode integration" [verified: repo exists, description from search snippet].
- `repbyrepdev/claude-workflow-core` — portable skills that "drive staged local review pipelines as a state machine for shipping PRs" [verified: repo exists, description from search snippet].
- `EarthmanWeb/serena-workflow-engine` — described as a state-machine workflow engine for Claude Code with Serena memory persistence and a hook-driven event architecture [verified: repo exists via GitHub API; description from awesomeclaudeplugins.com search snippet — not independently read].
- `nirecom/agents` — described as a "self-driving Claude Code framework with hook-enforced workflow state machine covering research → plan → tests → code → security-review → docs" [verified: repo exists via GitHub API; description from search snippet — not independently read].
- `rohitg00/awesome-claude-code-toolkit` — a large curated index (135 agents, 176+ plugins, 20 hooks, etc.) worth using as a discovery surface for more of these [verified: title/description from search result; repo not independently fetched].

Common pattern across these community projects (inferred from descriptions, not verified line-by-line): they encode a workflow as a **fixed pipeline of named phases** (plan → code → review → test → docs), authored as a combination of skill files (Markdown with frontmatter) plus one enforcement hook (commonly a git-guard or a Stop-hook checklist) that blocks phase skipping. None of the descriptions found claim a declarative, non-code (e.g. pure YAML) end-user config format comparable to a general FSM definition language — authoring still means writing/arranging skill files and hook scripts, which is closer to "framework" than "config a non-author fills in."

**Obra's `superpowers`** (github.com/obra/superpowers) is the most mature process-enforcement example. It bills itself as "an agentic skills framework & software development methodology that works" [verified: https://github.com/obra/superpowers repo title/description]. Enforcement is *not* primarily hook-based blocking but **injected bootstrapping**: a session-start hook (or, on Pi, "a small extension that injects the `using-superpowers` bootstrap at session startup and again after compaction") makes the agent check for a relevant skill "before any task," and the README frames these as "Mandatory workflows, not suggestions" [verified: https://raw.githubusercontent.com/obra/superpowers/main/README.md]. Concretely it chains skills like `brainstorming` → (design approval) → `using-git-worktrees` → `test-driven-development` (RED-GREEN-REFACTOR) → `code-review`, each a Markdown SKILL.md the agent is told to consult, with a dedicated `writing-skills` skill documenting the authoring format [verified: same README fetch]. Its limit: this is *soft* enforcement (a persistent system prompt/skill-trigger convention), not a hard state machine — nothing stops the model from skipping a skill if it fails to recognize the trigger, unlike a `PreToolUse` deny.

## 2. oh-my-pi — found, and it is directly on this machine

The user's hunch was right, with a naming correction: **oh-my-pi (binary name `omp`) is a real, actively-installed project, and it is explicitly a fork of Mario Zechner's "Pi"** [verified: `~/.cache/yay/oh-my-pi-bin/PKGBUILD` — `pkgdesc="A coding agent with the IDE wired in (release binary)"`, `provides=("oh-my-pi")`, source pulled from `https://github.com/can1357/oh-my-pi`; confirmed by WebFetch of that URL, which states "a fork of Mario Zechner's Pi project that adds production-grade tooling for real coding workflows"]. It is maintained by GitHub user `can1357` (AUR packaging by Bin Jin), site `https://omp.sh/` [verified: PKGBUILD `url="https://omp.sh/"`]. On this machine it is installed via `yay`/AUR at version 18.2.0 (`~/.cache/yay/oh-my-pi-bin/PKGBUILD`, `.SRCINFO`), with a live config/state tree at `~/.omp/` (`agent/config.yml`, `agent/AGENTS.md`, `agent/mcp.json`, `plugins/`, session DBs) [verified: `find ~/.omp -maxdepth 3`, `cat ~/.omp/agent/config.yml`, `pacman -Qi oh-my-pi-bin` output showing version 18.2.0-1 and description "A coding agent with the IDE wired in (release binary)"]. `omp --version` reports `omp/18.2.0` [verified: command output]. A stray `~/.pi/agent/extensions/pi-automode/config.json` also exists, consistent with the Pi lineage [verified: `find ~/.pi -maxdepth 4`].

**How it defines workflow/orchestration (config format + examples):** oh-my-pi does not use a distinct "Workflow tool" but three "magic prompt" keywords that alter the agent's system contract for that turn [verified: `docs/magic-keywords.md` fetched from `https://raw.githubusercontent.com/can1357/oh-my-pi/main/docs/magic-keywords.md`]:
- `orchestrate` — "Adds the multi-agent orchestration contract: scope the full task, delegate substantial independent work in parallel, verify each phase, and continue until the request is complete." [verified: same fetch, exact quote]
- `workflowz` — "Adds a deterministic multi-subagent workflow contract centered on the persistent `eval` kernel's `agent()`, `completion()`, handle, `wait()`, and `workpool()` helpers." [verified: same fetch, exact quote]

So the "user authors" surface for a deterministic pipeline in oh-my-pi is a **JS/eval-kernel script using `agent()`/`wait()`/`workpool()`/`completion()`**, functionally parallel to Claude Code's own `Workflow` tool script API (both give you agent-spawning primitives plus explicit synchronization, rather than a declarative state-transition table). This strongly suggests the "workflow script as deterministic orchestration DSL" pattern is convergent across at least two independent agent-harness projects (Anthropic's Workflow tool and can1357/oh-my-pi's `workflowz`), not unique to either [inferred from comparing the two verified sources].

Separately, static configuration in oh-my-pi is YAML at `~/.omp/agent/config.yml`: model role mapping, `tools.approvalMode`, `skills.customDirectories` (a list of directories from which Markdown skills are loaded, e.g. this machine's `~/projects/brain/skills`), and an `extensions` list of TypeScript file paths loaded at startup (e.g. `/home/dcferreira/projects/brain/omp/brain-vault.ts`) [verified: `cat ~/.omp/agent/config.yml`]. This is genuine "config, not code" for wiring skills/extensions in, but the workflow/pipeline logic itself still lives in the `workflowz` script, not in that YAML — i.e., even here, a non-author cannot define a new workflow purely by filling in config; they still write (or have an LLM write) an eval-kernel script.

No separate GitHub search turned up a distinct "oh-my-pi state machine example" repo beyond the main `can1357/oh-my-pi` monorepo itself and its `docs/` folder — the workflow capability lives inside that repo, not as an external plugin ecosystem [inferred from search scope; not exhaustively verified against every doc file in the repo].

## 3. Adjacent harness-level prior art

**"Ralph loop."** Named by Geoffrey Huntley in a July 2025 post ("Ralph Wiggum as a 'software engineer'") [inferred, per search-result summary of https://thomas-wiegold.com/blog/ralph-loop-how-recursive-ai-agents-work/ and https://futureagi.com/blog/loop-engineering/ralph-loop/ — origin not independently confirmed against Huntley's original post]. It's the crudest possible "state machine": a shell `while` loop that re-invokes the same coding-agent prompt against the same repo until a stop condition holds, with state surviving between iterations only via the codebase, a TODO/PRD file, and git history — no in-process state machine at all, reliability comes from idempotent re-runs plus external durable state [verified via search-result descriptions: e.g. `github.com/snarktank/ralph` billed as "an autonomous AI agent loop that runs repeatedly until all PRD items are complete"]. Relevant to our project as the "minimum viable reliability" baseline our FSM should clearly beat: no explicit transitions, no per-step verification contract, just repetition + external checklist file.

**Codex/Cursor rules-based workflows.** Not independently searched in this pass (time-boxed); flagging as a gap — Cursor's `.cursor/rules` and Codex's `AGENTS.md`-style instruction files are the other major "declarative-ish" convention for steering agents, but neither exposes hook-level blocking the way Claude Code does `[inferred]`, so they're weaker for *enforcing* order, only for *suggesting* it.

**Hooks-based "phase gate" pattern.** Multiple community projects above (`nirecom/agents`, `EarthmanWeb/serena-workflow-engine`) converge on the same shape: fixed phase list (plan/code/test/review/docs) + one hook that blocks phase-skipping, generally by writing/reading a marker file the hook checks on `PreToolUse`/`Stop` [inferred from descriptions only, not read in detail].

## Implications for our design

- **Use `PreToolUse` + `Stop` hooks as the hard enforcement layer**, not the LLM's judgment — this is the one primitive in the whole survey that gives true "cannot skip a step" guarantees [verified: https://code.claude.com/docs/en/hooks-guide]. Everything else (skills, superpowers-style bootstrapping, magic keywords) is soft/advisory.
- **Persist FSM state externally** (a state file the hook reads/writes), mirroring the Ralph-loop insight that durable state should live outside the model's context, not be trusted to survive compaction or model attention.
- **Borrow the "workflow script" ergonomics** from both Anthropic's `Workflow` tool (`agent()`/`pipeline()`/`parallel()`/schema-validated structured output, resumable by `runId`) and oh-my-pi's `workflowz` (`agent()`/`wait()`/`workpool()`/`completion()`) for the *agentic-step* half of our engine — but note neither is truly "declarative config a non-author fills in"; both require someone to write imperative script/DSL code. Our differentiator (non-authors define workflows via *pure config*, e.g. YAML transitions) appears to be an actual gap none of these projects fill.
- **Gap: no found project separates "deterministic step" from "agentic step" as first-class node types in one state machine.** Every example either orchestrates multiple *agent* subtasks (Workflow tool, `orchestrate`/`workflowz`, superpowers) or is a plain deterministic loop (Ralph) — none model a mixed graph where some nodes are plain code/API calls and others are LLM calls, with the same transition/retry/rollback semantics for both. This is a legitimate design opportunity.
- **Gap: no config-only authoring surface.** Every workflow-definition surface found (Workflow tool scripts, `workflowz` scripts, superpowers SKILL.md chains, community plugin skill/hook bundles) requires writing code or Markdown-with-embedded-instructions, not filling in a schema/config file. If "non-authors can easily define workflows declaratively" is a core goal, that's differentiated territory.
- **Reuse the "schema-validated structured output" pattern** from Workflow tool's `agent(prompt, {schema})` for making agentic steps produce machine-checkable output the state machine can gate transitions on, rather than parsing free text.
- **Consider a phase-gate marker-file convention** (like the community hook-enforced FSM projects) as the simplest interop mechanism with existing Claude Code tooling — a hook that reads/writes a small JSON "current state" file is compatible with our own engine running underneath, and lets us add Claude-Code-native enforcement without reimplementing hooks.
- **superpowers' skill-chaining is worth studying further** (not just its README) for how it structures SKILL.md files and the `writing-skills` skill, since it's the most field-tested "process methodology as skills" example — a deeper follow-up read of `skills/writing-skills/SKILL.md` and the plugin's session-start hook implementation would be worthwhile before finalizing our authoring format.

## oh-my-pi — verbatim extracts

Source repo confirmed public: `can1357/oh-my-pi` [verified: `curl -s https://api.github.com/repos/can1357/oh-my-pi` → `"private": false`, `"description": "⌥ Coding agent with the IDE wired in"`]. All quotes below are `curl`-fetched raw file bytes from `raw.githubusercontent.com/can1357/oh-my-pi/main/...` (not the earlier WebFetch summaries, which paraphrased) unless marked as a local file on this machine.

### (1) The `workflowz` contract — full injected notice text

File: `packages/coding-agent/src/prompts/system/workflow-notice.md` [verified: `https://raw.githubusercontent.com/can1357/oh-my-pi/main/packages/coding-agent/src/prompts/system/workflow-notice.md`, HTTP 200]. This is a Handlebars-style template (`{{#if ...}}`) rendered into the hidden system notice; full text as stored in the repo:

```
<system-notice>
User message contains **workflowz** → deterministic multi-subagent workflow. Default to `workpool()` for 2+ independent items; use individual `agent()` handles only for dependency-coupled or schema-returning calls.

<when>
Use for broad research, reviews, migrations, adversarial coverage, and open-ended work lists. Quick lookup/single edit: direct; no agents. {{#if scoutAvailable}}Scout inline FIRST{{else}}Explore inline FIRST{{/if}} — scope files, call sites, and contracts before creating the pool.

Pool-first phases:
- **Understand**: queue subsystem readers → poll pool job → synthesize
- **Review**: queue one item per lens/file → poll → verify survivors
- **Migrate**: discover sites → queue file-disjoint transforms → verify once
- **Research**: queue modalities/sources → deep-read hits → synthesize
- **Design**: queue independent proposals/judges → choose and integrate
</when>

<helpers>
State persists across `eval` calls. Every call provides:

- `workpool(agent=None, *, name=None, context=None{{#if evalTools}}, tools=None{{/if}})`: pool of keep-alive workers bounded by live `task.maxConcurrency`. `.push(*items)` returns item ids; each item goes to the least context-loaded idle worker, a new worker while capacity remains, or a busy worker's round-robin queue. `eval.workpool.freshAgents=true` instead spawns a new agent per item. `.status()` reports counts/workers; `.peek()` returns a non-consuming batch snapshot; `.close()` drops queued work.
  - The pool name is its background job id and label. Push all items while it is active; its first full drain settles and closes that pool job. New phase/wave after drain → create a new named pool.
  - Results auto-deliver. Need to block? Leave `eval`, then call `hub` with `op:"wait", ids:["<pool-name>"]`; re-issue until settled. NEVER block the kernel with `pool.wait()`.
- `agent(prompt, *, agent=None, label=None, schema=None, isolated=None, apply=None, merge=None{{#if evalTools}}, tools=None{{/if}})`: immediate `AgentHandle`; use for a small fixed dependency graph or when the parent needs validated `schema` data. `.wait()` returns text/data; `.handle` is `agent://<id>`. Unwaited results auto-deliver.
- `completion(prompt, *, model="default", system=None, schema=None)`: immediate `CompletionHandle` for a tool-free one-shot call. Tiers: `"smol"`, `"default"`, `"slow"`.
- `wait(handles, timeout=None, *, raise_errors=True)`: ordered barrier for agent/completion handles only; `raise_errors=False` keeps an error in its slot.
{{#if evalTools}}- `@tool` (Python) / `tool(fn, {…})` (JS): kernel-local tool exposed via `tools=`. Use for shared caches, dedup sets, scoring, or structured accumulation across pool workers; calls execute in YOUR kernel and a raised exception returns to the caller without killing it.
{{/if}}- `log(message)`: progress line. `phase(title)`: status-tree phase.
- `budget`: Python `budget.total` / `budget.spent()` / `budget.remaining()`; JS awaits them. User `+Nk` = advisory; `+Nk!` = hard.
</helpers>

<pool-workflow>
1. Scope the full independent work list before spawning.
2. Create ONE explicitly named pool per phase.
3. Push every known item in one cell; later discoveries MAY be pushed while the pool job is still running.
4. Continue useful local work. Results auto-deliver.
5. Completely blocked? Poll `hub wait` with `ids:[pool-name]`, never `pool.wait()`.
6. Read every batch result; YOU verify and integrate.

**Python:**

phase("Review")
review = workpool({{#if scoutAvailable}}"scout", {{/if}}name="review", context="Return evidence with exact paths; do not edit.")
review.push(*[
    "Review authentication correctness",
    "Review authorization boundaries",
    "Review cancellation and cleanup",
    "Review performance regressions",
])
print(review.name)   # poll outside eval: hub wait, ids:["review"]

**JavaScript:**

phase("Review");
const review = await workpool({{#if scoutAvailable}}"scout", {{/if}}{
    name: "review",
    context: "Return evidence with exact paths; do not edit.",
});
await review.push(
    "Review authentication correctness",
    "Review authorization boundaries",
    "Review cancellation and cleanup",
    "Review performance regressions",
);
console.log(review.name); // poll outside eval: hub wait, ids:["review"]

Need a snapshot without consuming/delivering results? `review.peek()` (JS: `await review.peek()`). Need activity counts? `review.status()`.
</pool-workflow>

<dependencies>
Use handles only when work item B requires A's exact output before B can be written:

spec = agent("Extract the protocol", {{#if scoutAvailable}}agent="scout", {{/if}}schema=SPEC).wait()
impl = agent(f"Implement this protocol: {spec}")
result = impl.wait()

<patterns>
- **Adversarial verify**: pool one REFUTE task per claim/lens; retain only evidence-backed survivors.
- **Perspective-diverse review**: distinct correctness/security/perf/reproduction items; NEVER clone one vague prompt.
- **Judge panel**: pool proposals, then a second named pool scores them after the first pool settles.
- **Loop-until-dry**: push newly discovered items while the pool remains active; dedup against all SEEN.
- **Multi-modal sweep**: queue by-container/by-content/by-entity/by-time items.
- **Completeness critic**: final pool item asks what modality/file/claim remains unchecked.
- **No silent caps**: if sampling/top-N drops work, `log()` what was omitted.
</patterns>

<execution>
- Multi-phase work: capture in `todo`.
- Each pool item: self-contained target, change/read scope, acceptance.
- Same-file mutation? One worker owns it; serialize shared boundaries.
- Pool output is evidence, not truth. Read artifacts, gate findings, run final verification yourself.
- Continue until closed; a drained pool is a phase boundary, not task completion.
</execution>
</system-notice>
```
[verified: exact byte content of the raw file, reformatted here only to strip the outer Handlebars conditionals' redundant repetition already shown once]

Helper JSDoc/comments, from `packages/coding-agent/src/modes/workflow.ts` [verified: same raw-fetch method, HTTP 200]:
```ts
/**
 * "workflowz" keyword support.
 *
 * Typing the standalone word in the input editor paints it with a warm
 * amber→green gradient ({@link highlightWorkflow}); submitting a message that
 * mentions it appends a hidden workflow notice that steers the model to author
 * a deterministic multi-subagent workflow through the active task schema.
 * Matching is prose-delimited and case-sensitive (lowercase only) —
 * "workflowz" triggers, but "workflowzed", "Workflowz", and "workflowz.ts"
 * never do.
 */
```
This confirms the mechanism: `workflowz` is not a config file format at all — it's a **magic keyword detector** (`magicKeywordRegex`, prose-boundary matching) that, when it fires, injects the notice above as a hidden user-attributed message for that turn only, steering the model to *write* an eval-kernel script using `agent()`/`workpool()`/`completion()`/`wait()`. There is no persistent, user-editable "workflow definition file" — the workflow exists only as code generated fresh (or reused ad hoc) inside the `eval` tool's kernel for that turn/session.

### (2) Other magic keywords shipped alongside `workflowz`

Full, verbatim table from `docs/magic-keywords.md` [verified: `https://raw.githubusercontent.com/can1357/oh-my-pi/main/docs/magic-keywords.md`, HTTP 200] — there are exactly **three** magic keywords total, no more:

| Keyword | Effect (verbatim) |
|---|---|
| `ultrathink` | "Adds a careful multi-step reasoning notice. When automatic thinking is active, it also selects the highest reasoning effort supported by the current model for that turn." |
| `orchestrate` | "Adds the multi-agent orchestration contract: scope the full task, delegate substantial independent work in parallel, verify each phase, and continue until the request is complete." |
| `workflowz` | "Adds a deterministic multi-subagent workflow contract centered on the persistent `eval` kernel's `agent()`, `completion()`, handle, `wait()`, and `workpool()` helpers. It is intended for broad research, reviews, migrations, and adversarial coverage. The notice is injected only when both `eval` and `task` are active." |

Matching rule, verbatim: "Use the exact lowercase spelling. `Ultrathink`, `Orchestrate`, and `Workflowz` do not trigger." and "letters, digits, underscores, slashes, backslashes, hyphens, file extensions, symbol references, and call syntax do not match. For example, `orchestrate,` matches; `orchestrated`, `orchestrate.ts`, `foo::orchestrate`, and `orchestrate()` do not." [verified: same fetch]. Configurable per-keyword via `omp config set magicKeywords.<name> false`, default `true` for the global switch and all three [verified: same fetch].

The full `orchestrate` contract (`packages/coding-agent/src/prompts/system/orchestrate-notice.md` [verified: same raw-fetch method, HTTP 200]) is the closest thing to an explicit "phase-gate" rulebook found anywhere in this research — rule 1 states verbatim: "NEVER yield before closure. Phase completion is not a yield point: launch the next phase in the same turn. Stop only when every requested item is verifiably done or concrete `[blocked]` genuinely requires the user," and rule 5: "Verify each phase before the next... Breakage: dispatch fix-up subagents, then re-verify before advancing. NEVER declare a red tree done." This is a *prompted* completeness/order contract, not a hook-enforced one — nothing stops the model from ignoring it other than the model's own compliance.

### (3) `~/.omp/agent/config.yml` — this machine's actual file, verbatim (no secrets present to redact)

[verified: `cat ~/.omp/agent/config.yml`, local file on this machine]:

```yaml
modelRoles:
  default: deepseek/deepseek-flash
  plan: deepseek/deepseek-flash
  slow: deepseek/deepseek-flash
  smol: deepseek/deepseek-flash
  task: deepseek/deepseek-flash
  commit: deepseek/deepseek-flash
  tiny: deepseek/deepseek-flash
tools:
  approvalMode: yolo
providers:
  webSearchOrder:
    []
symbolPreset: unicode
composer:
  shape: band
theme:
  dark: titanium
setupVersion: 2
skills:
  customDirectories:
    - ~/projects/brain/skills
extensions:
  - /home/dcferreira/projects/brain/omp/brain-vault.ts
defaultThinkingLevel: high
hideThinkingBlock: true
steeringMode: one-at-a-time
followUpMode: one-at-a-time
interruptMode: immediate
memory:
  backend: "off"
statusLine:
  sessionAccent: true
  transparent: false
display:
  hideToolActivity: false
edit:
  mode: hashline
vault:
  enabled: true
dev:
  autoqa: false
task:
  enableEffort: false
```

Schema/discovery notes from `docs/config-usage.md` [verified: `https://raw.githubusercontent.com/can1357/oh-my-pi/main/docs/config-usage.md`, HTTP 200]: config is resolved via `ConfigFile<T>` (`packages/coding-agent/src/config/config-file.ts`), a "schema-validated loader for single config files" supporting `.yml`/`.yaml`/`.json`/`.jsonc`, validated against an "omptype schema," with tri-state load results (`ok` / `not-found` / `error`). Config roots are searched in fixed priority order — `.omp` (native) > `.claude` > `.codex` > `.gemini` — at both user level (`~/.omp/agent`) and project level (`<cwd>/.omp`), i.e. **oh-my-pi auto-imports Claude Code / Codex / Gemini config directories without a migration step**, which is directly relevant to interop if our engine wants to sit underneath multiple harnesses.

Notably `config.yml` itself carries **no workflow/pipeline keys** — it only wires up model roles, tool approval mode, skill directories, and one TS extension file. All orchestration logic lives in code (extension `.ts` files or ad hoc `eval` scripts), confirming the pattern already seen in the `workflowz` notice: oh-my-pi's YAML layer is for *wiring*, not for *defining workflow structure*.

### (4) Example multi-step orchestrated flow — real files, quoted

No dedicated "example workflow script" ships in the package's `docs/skills/examples/` (that directory only contains `hello-extension`, `mini-marketplace`, `safety-hook` [verified: GitHub Contents API listing of `docs/skills/examples`]). The canonical multi-step example is the one embedded in the `workflowz` notice itself (the `phase("Review")` / `workpool(...)` / `review.push(...)` snippet quoted in full under (1) above) — that block ships inside the running binary and is shown to the model verbatim whenever `workflowz` fires.

For a real, currently-loaded, non-toy example of a declaratively-triggered multi-step flow *authored by the user of this machine* and wired into this exact oh-my-pi installation via `skills.customDirectories: [~/projects/brain/skills]` in the config.yml above, see `~/projects/brain/skills/brain-dispatch/SKILL.md` [verified: local file, `sed -n '1,40p'`]. Its frontmatter and structure:

```yaml
---
name: brain-dispatch
description: Prepare and launch a primed worker session for a Today action — a single launcher subagent gathers context, classifies code vs non-code, writes a worker note, launches a detached worker session ...
argument-hint: <today action> [base directory] [--host <name>] [--harness omp|claude]
---
```

followed by a numbered `## Step 1` / `## Step 2` / `## Step 3` procedure (identify action → single launcher subagent does gather-context → classify → derive slug → resolve host/harness → preflight → write worker note → launch → verify → main session flips status) [verified: local file content read above]. This is a Markdown-prose "workflow," not a machine-checkable state machine: ordering is enforced by prose instruction only ("the main session only identifies the action... Everything in between is delegated to a single launcher subagent"), the same soft-enforcement pattern as superpowers and the `orchestrate` contract above — nothing in oh-my-pi's skill format itself gives it hook-level blocking.

### (5) How the `eval` kernel persists state, and resume/checkpoint mechanics

Within a single turn/session, the workflow notice states directly: "State persists across `eval` calls" [verified: `workflow-notice.md`, quoted in full under (1)] — i.e. the Python/JS kernel behind `eval`/`agent()`/`workpool()` is a long-lived interpreter whose variables, open `workpool` objects, and `@tool`-registered functions survive between separate `eval` tool invocations within the same agent session, similar in spirit to a Jupyter kernel. Blocking is explicitly discouraged inside the kernel ("NEVER block the kernel with `pool.wait()`"); instead the agent is told to leave `eval` and poll via a separate `hub` tool with `op:"wait", ids:[...]` until pool jobs settle [verified: same source]. This is corroborated by `docs/agent-hub.md` [verified: `https://raw.githubusercontent.com/can1357/oh-my-pi/main/docs/agent-hub.md`, HTTP 200], which describes `hub list`/`hub send` as exposing "the peer roster to the coding agent" and documents `agent://<id>` (resolves a subagent's *saved final output artifact*, not the live transcript) and `history://<id>` (a concise transcript for a live or parked subagent) as the internal-URL handles used to read back results after the kernel/turn boundary.

Beyond a single session, persistence is session-file based, not workflow-state based: `docs/session-operations-export-share-fork-resume.md` [verified: `https://raw.githubusercontent.com/can1357/oh-my-pi/main/docs/session-operations-export-share-fork-resume.md`, HTTP 200] documents `/resume`, `--resume`, `--continue`, and `/fork` as operating on a persisted JSONL session transcript (one file per session, subagent transcripts as `<session>/<AgentId>.jsonl`), where "Agent Hub... discovers parked subagents from the current session's persisted artifacts when a session is resumed," and a `parked` subagent can be `r`evived from the roster. There is **no separate workflow-checkpoint format** distinct from the ordinary session transcript — resuming a session resumes whatever `eval`-kernel state and parked/running subagents existed in that session's persisted JSONL + artifact tree, not a dedicated FSM/workflow-run object. This is a materially different resume model from Anthropic's `Workflow` tool (which resumes by replaying a *script* against a cached per-agent-call journal, `journal.jsonl`, keyed by `runId`) — oh-my-pi resumes the *whole interactive session*, Anthropic's Workflow tool resumes one *script run*.

**Summary of what's genuinely reusable as UX inspiration:** the `workpool()` abstraction (named, keep-alive worker pool; push-anytime; auto-delivering results; explicit non-blocking poll via `hub wait` instead of blocking the kernel) is a cleaner ergonomic than a plain fire-and-forget subagent list, and worth borrowing conceptually for our engine's "agentic step" executor. The magic-keyword mechanism (prose-triggered, prompt-injected contract) is a nice low-friction UX for turning on a stricter mode, but it is pure prompt-engineering — zero hard guarantees — so it should not be mistaken for the enforcement layer our project needs.
