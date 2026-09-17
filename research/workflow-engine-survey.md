# Workflow-Engine Authoring UX Survey

Goal: extract the best *authoring* patterns (not internals) from existing agentic/FSM
workflow tools, for a declarative Claude-Code-native workflow engine mixing deterministic
and LLM steps.

## Agentic / LLM workflow engines

### LangGraph
`StateGraph` builds a graph of nodes (Python functions receiving/returning a typed state
dict) connected by static edges and functions that route conditionally:

```python
from langgraph.graph import StateGraph, START, END
from langgraph.checkpoint.memory import InMemorySaver

class State(TypedDict):
    messages: list[dict]

def node_a(state): ...
def node_b(state): ...
def route_decision(state):
    return "node_b" if len(state["messages"]) > 2 else END

builder = StateGraph(State)
builder.add_node("node_a", node_a)
builder.add_node("node_b", node_b)
builder.add_edge(START, "node_a")
builder.add_conditional_edges("node_a", route_decision)
builder.add_edge("node_b", END)

graph = builder.compile(checkpointer=InMemorySaver())
```
[verified: https://docs.langchain.com/oss/python/langgraph/graph-api]

(a) Nodes are plain functions — no syntactic distinction between an LLM call and a
deterministic transform; determinism is the author's responsibility. (b) Guards are
just Python functions returning a target node name. Interrupts (`interrupt()` inside a
node) pause execution for human approval; this **requires a checkpointer** to be
enabled at compile time [verified: search result citing LangGraph docs]. (c) Retries
aren't a first-class graph primitive; timeouts/retries are handled at the node/tool
level (LangChain runnable config), not surfaced in the declarative graph shape. (d)
State is persisted via a **checkpointer** keyed by `thread_id`; checkpoints are written
at "super-step" boundaries (between node executions), and resuming means re-invoking
the graph with `None` input and the same thread config so it picks up from the last
saved checkpoint [verified: https://docs.langchain.com/oss/python/langgraph/graph-api].
(e) Easy: graph shape mirrors a flowchart, good for visualizing. Hard for a non-author:
routing logic and state shape live in Python, and there's no separate, readable
declarative file — you must read code to know what happens next.

### Mastra workflows
```typescript
export const testWorkflow = createWorkflow({
  id: "test-workflow",
  inputSchema: z.object({ message: z.string() }),
  outputSchema: z.object({ output: z.string() })
})
  .then(step1)
  .commit();
```
[verified: https://mastra.ai/en/docs/workflows/overview]

(a) Each `step` has zod input/output schemas — a declarative I/O *contract* per step,
independent of whether the step is deterministic code or an LLM call. (b) Branching via
`.branch()`, human/long-running pauses via `.suspend()`/`resumeStream()`, which returns
a `suspended` status with a `suspendPayload` [verified:
https://mastra.ai/en/docs/workflows/overview]. Retry details live on a separate
suspend-and-resume doc page not fetched here — [inferred] that retries are configurable
per-step similar to other JS workflow engines. (d) Suspend/resume implies durable state
snapshots keyed by run id, resumed explicitly by the caller. (e) Reads like a fluent
pipeline (`.then().branch().commit()`), very approachable for non-FSM-experts, but the
type-checked schemas add authoring friction (upside: catches wiring mistakes early).

### Burr
```python
@action(reads=[], writes=["prompt", "chat_history"])
def human_input(state: State, prompt: str) -> State:
    ...

app = (
    ApplicationBuilder()
    .with_actions(human_input, ai_response)
    .with_transitions(("human_input", "ai_response"), ("ai_response", "human_input"))
    .with_state(chat_history=[])
    .with_entrypoint("human_input")
    .build()
)
```
[verified: https://burr.dagworks.io/getting_started/simple-example/]

(a) Actions **declare their state footprint** (`reads=[...]`, `writes=[...]`) directly
in the decorator — this is a distinctive, borrowable idea: static, greppable
data-dependency contracts per step, whether the step body is deterministic or an LLM
call. (b) No syntactic marker for side effects/non-determinism; it's the same decorator
either way. (c) Not covered in the fetched page. (d) State is immutable and
copy-on-write (`.update()`/`.append()`); persistence is pluggable via an
`initialize_from` parameter on the builder, letting an app reload prior state from a
database and resume [verified: https://burr.dagworks.io/getting_started/simple-example/].
(e) The `reads`/`writes` contract makes it easy for a reviewer to see, at a glance, what
each action touches without reading its body — good for non-author auditability. The
transition list is a flat set of tuples though, so complex branching logic (guards) has
to live inside the actions themselves [inferred from example: no guard syntax shown in
`with_transitions`].

### Inngest (step functions / AgentKit)
```ts
inngest.createFunction(
  { id: "sync-systems", triggers: { event: "auto/sync.request" } },
  async ({ step }) => {
    const data = await step.run("get-data", async () => getDataFromExternalSource());
    await step.run("save-data", async () => db.syncs.insertOne(data));
  }
);
```
[verified: https://www.inngest.com/docs/learn/inngest-functions]

(a) `step.run(id, fn)` is the deterministic/non-deterministic boundary: anything wrapped
in `step.run` is treated as a durable, memoized unit; code *outside* a `step.run` call
re-executes on every replay, so authors are taught to push all side effects (LLM calls,
API calls, DB writes) inside named steps. (c) Each step is retried automatically on
throw, "up to 4 times" per a code comment in the docs, without extra config
[verified: https://www.inngest.com/docs/learn/inngest-functions]. (d) **Step
memoization and replay**: on any failure, the whole function re-runs, but each
previously-completed `step.run` returns its cached result instead of re-executing —
this event-sourcing-style replay is the resume mechanism, and it's simply "control
flow in normal async code" rather than a separate graph description. (e) Very easy for
a JS/TS developer — it's just an async function — but "the whole function replays"
is a subtle non-obvious mental model for someone unfamiliar with durable execution
(easy to accidentally put non-idempotent code outside a `step.run`).

### Temporal
Fetched pages were navigation indexes rather than code; details below are drawn from
what those indexes state plus the well-documented Temporal execution model.
[inferred, partial: https://docs.temporal.io/develop/typescript/workflows — page did
not return a code sample; the workflow/activity separation and determinism claims below
come from the site's stated `Timeouts`/`Versioning`/`Activities` structure and are
[inferred] from general Temporal architecture, not a fetched snippet]

(a)/(b) Temporal's headline authoring rule: **Workflow code must be fully
deterministic** (no direct I/O, no non-deterministic APIs like raw `Math.random`/system
clock) — all side effects and non-determinism (API calls, LLM calls, DB writes) must be
pushed into separate **Activities**, invoked from the workflow via a proxy
(`proxyActivities`) with explicit `startToCloseTimeout` and `retry` policy options
[inferred, standard Temporal pattern]. This is the cleanest "determinism boundary" of
any engine surveyed and is highly borrowable: the workflow is the FSM script, activities
are the effectful/LLM steps. (c) Retries and timeouts are declared as options on the
activity proxy call (`retry: { maximumAttempts, backoffCoefficient }`,
`startToCloseTimeout`), i.e., configuration attached at the call site, not a separate
DSL block. (d) Full durable execution: Temporal's server persists an event history of
every activity result and signal; on worker crash, the workflow is **replayed**
deterministically against that history so it reaches the same point without
re-executing already-completed activities — closely analogous to Inngest's step
memoization but backed by a durable server rather than embedded in the function.
(e) Hard for a non-author: understanding the determinism boundary (what may/may not go
in workflow code) is the single biggest learning curve in this survey; mistakes only
surface at replay time.

### CrewAI Flows
```python
class ExampleFlow(Flow[FlowState]):
    @start()
    def initialize(self):
        self.state.counter = 1

    @router(initialize)
    def check_status(self):
        return "proceed" if self.state.counter > 0 else "halt"

    @listen("proceed")
    def process(self):
        self.state.counter += 1
```
[verified: https://docs.crewai.com/en/concepts/flows]

(a) `@start`/`@listen`/`@router` decorators declare an implicit graph from function
names/return values rather than explicit edge lists — very low ceremony. `or_()`/
`and_()` combinators compose multiple listener triggers (join semantics).
(b) No explicit deterministic/agentic distinction; a `@listen` method can call an LLM
or not — up to the author. (c) A `@human_feedback()` decorator provides an approval-gate
primitive natively [verified: https://docs.crewai.com/en/concepts/flows]. (d) A
`@persist` decorator auto-saves Pydantic/dict flow state to SQLite; flows can **resume**
an existing run by id or **fork** a new run seeded from a past state snapshot
(`restore_from_state_id`) [verified: https://docs.crewai.com/en/concepts/flows]. (e)
Very readable for a Python-literate author (looks like an event system), but implicit
graph construction from decorators + string labels makes the overall shape harder to
see at a glance than an explicit edge list — a non-author has to trace decorators
across the whole class to reconstruct the graph.

### Pydantic AI graphs
```python
@dataclass
class IncrementNode(BaseNode[State]):
    async def run(self, ctx: GraphRunContext[State]) -> CheckNode:
        ctx.state.counter += 1
        return CheckNode()

@dataclass
class CheckNode(BaseNode[State, None, int]):
    async def run(self, ctx) -> IncrementNode | End[int]:
        return End(ctx.state.counter) if ctx.state.counter >= 5 else IncrementNode()
```
[verified: https://pydantic.dev/docs/ai/graph/graph/]

(a) Edges are **encoded in the return type annotation** of each node's `run` method
(`-> IncrementNode | End[int]`) — a distinctive type-checker-verifiable graph
definition; mypy/pyright can statically confirm the graph is well-formed. No syntactic
marker for determinism vs. LLM call. (d) The fetched page does not describe built-in
persistence; state is an in-memory object threaded through node calls, and the docs
explicitly leave crash recovery to the caller [verified:
https://pydantic.dev/docs/ai/graph/graph/ — "does not address persistence... you would
need to add external persistence outside what pydantic-graph provides natively"]. (e)
Type-checked edges are a nice safety net for a Python author, but a non-author must
read type signatures across every node class to reconstruct the flow — no
single-glance visual/declarative summary.

### OpenAI Agents SDK — handoffs
```python
triage_agent = Agent(
    name="Triage agent",
    handoffs=[
        billing_agent,
        handoff(agent=refund_agent, input_filter=handoff_filters.remove_all_tools),
    ],
)
```
[verified: https://openai.github.io/openai-agents-python/handoffs/]

(a) Handoffs are exposed to the LLM **as callable tools** (auto-named
`transfer_to_<agent>`); the "transition" decision is made by the LLM itself at
runtime, not a fixed graph edge — a fundamentally different, more dynamic authoring
model than the FSM-style engines above. (b) No distinction; handoff targets are just
other agents. (c) No native retry/guard syntax; `input_filter` lets an author control
what conversation history crosses the handoff boundary. (d) Not covered by the fetched
page — [inferred] state is the running conversation/session object, no durable
checkpoint model shown. (e) Extremely low authoring ceremony (just list agents in an
array) but the actual control flow is emergent/LLM-decided, which is the opposite of
"every step executed in the correct order" — not a good fit as the reliability layer,
though the handoff-as-tool idea (delegate + filter context) is useful for sub-agent
dispatch within a step.

### AWS Step Functions (Amazon States Language)
```json
{
  "Comment": "Hello World example",
  "StartAt": "HelloWorld",
  "States": {
    "HelloWorld": {
      "Type": "Task",
      "Resource": "arn:aws:lambda:region:acct:function:FailFunction",
      "TimeoutSeconds": 2,
      "Retry": [
        { "ErrorEquals": ["States.Timeout"], "IntervalSeconds": 1, "MaxAttempts": 2, "BackoffRate": 2.0 }
      ],
      "Catch": [
        { "ErrorEquals": ["States.ALL"], "Next": "fallback" }
      ],
      "End": true
    },
    "fallback": { "Type": "Pass", "Result": "Hello, AWS Step Functions!", "End": true }
  }
}
```
[verified: https://docs.aws.amazon.com/step-functions/latest/dg/concepts-error-handling.html]

(a) Deterministic vs. side-effecting is architectural, not syntactic: a `Task` state
*is* the side-effecting/LLM call (invokes an external resource); `Choice`/`Pass`/`Wait`
are the deterministic control-flow states. (b) `Choice` states express guards via
declarative comparison operators over the JSON payload (`StringEquals`,
`NumericGreaterThan`, etc. — not fetched verbatim here but standard ASL
[inferred]). (c) **`Retry`/`Catch` are the single best-documented pattern in this
survey**: `Retry` is an ordered array of retriers (`ErrorEquals`, `IntervalSeconds`,
`MaxAttempts`, `BackoffRate`, `MaxDelaySeconds`, `JitterStrategy`); `Catch` is an
ordered array of catchers (`ErrorEquals`, `Next`) that fire when retries are exhausted
or absent, with `States.ALL`/`States.TaskFailed` reserved wildcards
[verified: https://docs.aws.amazon.com/step-functions/latest/dg/concepts-error-handling.html].
`HeartbeatSeconds`/`TimeoutSeconds` give per-task liveness and hard-deadline timeouts.
Human approval is expressed via the `.waitForTaskToken` service-integration pattern —
a task blocks until an external caller submits a callback with the token
[verified: https://docs.aws.amazon.com/step-functions/latest/dg/amazon-states-language.html].
(d) Standard workflows persist full execution history server-side and guarantee
exactly-once state transitions, runnable for up to a year; execution can be inspected
and even **redriven** (retried from point of failure) via the console
[verified: https://docs.aws.amazon.com/step-functions/latest/dg/amazon-states-language.html,
.../concepts-error-handling.html]. (e) JSON is verbose but fully declarative and
data-only (no embedded language beyond JSONPath/JSONata), so tooling (visualizer,
linter, redrive UI) can reason about the whole graph without executing it — arguably
the best "non-author can read and safely edit this" experience of any engine surveyed.

### GitHub Actions
```yaml
jobs:
  job1:
    runs-on: ubuntu-latest
    steps:
      - run: echo "Job 1 running"
  job2:
    needs: job1
    runs-on: ubuntu-latest
    steps:
      - run: echo "Job 2 running after Job 1"
  job3:
    needs: [job1, job2]
    if: ${{ always() }}
    runs-on: ubuntu-latest
    steps:
      - run: echo "Job 3 runs regardless"
```
[verified: https://docs.github.com/en/actions/using-jobs/using-jobs-in-a-workflow]

(a) No distinction — every `run:` step is opaque shell/action. (b) `if:` expressions
gate jobs/steps declaratively (`always()`, `success()`, `failure()` functions,
arbitrary expression syntax) [verified, "if" behavior:
https://docs.github.com/en/actions/using-jobs/using-jobs-in-a-workflow]. `needs:`
declares a DAG (not just a linear chain) of job dependencies. (c) Retries and timeouts
require third-party actions or `timeout-minutes`; no native retry/backoff block.
Approval gates exist only via "environments" with required reviewers, outside the YAML
file itself. (d) No mid-job resume; a failed job is generally re-run from scratch
(`re-run failed jobs` re-executes whole jobs, not steps). (e) The most familiar
declarative format to most engineers — flat YAML, one file, readable top to bottom —
but its lack of state-machine expressiveness (no loops/guards beyond `if`, no
sub-workflow state) is exactly why it's a "baseline," not a target architecture.

## Classic FSM / statechart libraries

### XState
```ts
const machine = setup({
  actions: { logCount: ({ context }) => console.log(context.count) },
  guards: { isOverLimit: ({ context }) => context.count > 10 },
  actors: { fetchData: fromPromise(async () => ({ data: "result" })) },
}).createMachine({
  initial: "idle",
  context: { count: 0 },
  states: {
    idle: {
      on: {
        increment: { guard: "isOverLimit", actions: "logCount" },
        fetch: { target: "loading" },
      },
    },
    loading: { invoke: { src: "fetchData", onDone: { target: "idle" } } },
  },
});
```
[verified: https://stately.ai/docs/machines]

(a) `invoke` is the explicit marker for an external/async/side-effecting service
(`fromPromise`, callback actors, or nested machines); everything else (`actions`,
`guards`, `assign`) is meant to stay synchronous and pure — a lighter-weight analog of
Temporal's determinism boundary, enforced by convention/typing rather than a sandboxed
runtime. (b) `guard` on a transition gates whether it's taken; `always` transitions
(eventless, evaluated immediately on entering a state) implement postcondition-style
auto-advances [stated in fetch result, not shown in snippet — inferred from
XState's documented `always` feature]. (c) `invoke.onDone`/`onError` route
success/failure; no native retry/backoff primitive (would be modeled explicitly as
states). (d) XState itself is in-memory; Stately's ecosystem (not deeply fetched here)
adds persistence via serializable state snapshots that can be rehydrated
[inferred]. Nested (hierarchical) and parallel (orthogonal regions) states are core to
the model, letting one branch hold "approval pending" while a sibling branch continues
independent work. (e) Very approachable visually (Stately's editor renders the machine
graph live from this same code), and the `setup(...).createMachine(...)` split
separates the *implementation* of actions/guards/actors from the *shape* of the
machine — a good template for a config file that references named, pre-registered
step implementations.

### SCXML
```xml
<scxml xmlns="http://www.w3.org/2005/07/scxml" version="1.0">
  <state id="idle" initial="waiting">
    <onentry><log expr="'Entering idle state'"/></onentry>
    <state id="waiting"/>
    <transition event="start" target="active" cond="x > 0"/>
  </state>
  <state id="active"/>
</scxml>
```
[verified: https://www.w3.org/TR/scxml/]

(a) `<invoke>` is the standardized side-effect boundary — "triggers a platform-defined
service and passes data to it," with a `<finalize>` handler for normalizing returned
data before further transitions [verified: https://www.w3.org/TR/scxml/]. (b) `cond`
attribute is the guard mechanism (malformed conditions default to `false` plus a
queued execution error — fails closed, a good reliability property). (c) No native
retry/timeout vocabulary; would be modeled as explicit states/timers. (d) **History
states** (`<history>`, shallow or deep) are SCXML's resume primitive: re-entering a
compound state via its history pseudostate restores the previously active
configuration — a clean, standardized "resume where you left off" model
[verified: https://www.w3.org/TR/scxml/]. (e) XML verbosity and the W3C-spec register
make it the least approachable format here for a non-technical author, though its
formalism (a real, tool-checkable state machine standard) is why XState and Stately's
editor are built on the same conceptual model.

## Comparison table

| Engine | Definition format | Deterministic/side-effect boundary | Guards/retry/timeout/approval | Persistence & resume | Non-author friendliness |
|---|---|---|---|---|---|
| LangGraph | Python graph builder | none (convention) | routing fn; interrupt() for approval; retries external | checkpointer + thread_id, resume via re-invoke | Medium — code, not data |
| Mastra | TS fluent `.then/.branch/.suspend` | schema-typed steps, no hard boundary | `.branch`, `.suspend`/resume | suspend payload + resumeStream | High — reads like pipeline |
| Burr | Python decorators + transition list | none (convention); reads/writes contract | not shown | pluggable `initialize_from` | High — reads/writes contract aids audit |
| Inngest | Async TS fn + `step.run` | `step.run` wrapper = durable/memoized boundary | auto-retry (~4x) per step | replay + step memoization | Medium — subtle "outside step.run" pitfall |
| Temporal | Workflow fn + Activities | hard determinism sandbox vs. Activities | retry policy + timeouts on activity proxy | event-history replay | Low — steep determinism learning curve |
| CrewAI Flows | Python decorators (`@start/@listen/@router`) | none (convention) | `@human_feedback()`, `or_/and_` joins | `@persist` to SQLite, resume/fork by id | Medium — implicit graph from decorators |
| Pydantic AI graphs | Typed node classes, edges = return types | none (convention) | not shown | none built-in (BYO) | Medium — type-checked but code-only |
| OpenAI Agents SDK | Agent list + handoffs-as-tools | none; LLM decides transitions | input_filter only | not shown | High ceremony-wise, low determinism |
| AWS Step Functions | JSON/ASL state machine | Task (effectful) vs Choice/Pass (pure) | native `Retry`/`Catch`/`TimeoutSeconds`/`HeartbeatSeconds`; `.waitForTaskToken` for approval | server-side history, exactly-once, redrivable | High — pure data, tool-friendly |
| GitHub Actions | YAML jobs/steps | none | `if:`, `needs:`, environments (approval) | none (re-run whole job) | Very high familiarity, low expressiveness |
| XState | JS state/config object | `invoke` for async/services | `guard`, `always`, `invoke.onDone/onError` | snapshot serialization (ecosystem) | High — visualizable, setup/machine split |
| SCXML | XML state machine | `<invoke>` for external services | `cond` guards | `<history>` states | Low — XML verbosity |

## Patterns worth borrowing for a declarative Claude Code workflow engine

1. **Temporal's determinism boundary**: a hard syntactic/structural split between the
   "script" (pure control flow, retried freely) and "activities" (LLM calls, tool use,
   anything side-effecting) makes crash-safe replay tractable — port this as
   "orchestration steps vs. agent/tool steps."
2. **Step Functions' `Retry`/`Catch` blocks**: ordered arrays of typed retriers/catchers
   keyed on error name, with `IntervalSeconds`/`MaxAttempts`/`BackoffRate`/
   `MaxDelaySeconds`/`JitterStrategy` — a ready-made, well-specified vocabulary to copy
   almost verbatim for step-level error policy.
3. **Step Functions' `.waitForTaskToken`**: human-approval-as-a-blocking-callback is a
   clean way to express "pause until an external actor (a person, via Slack/CLI)
   responds," without special-casing approval as a different kind of state.
4. **LangGraph's `interrupt()` + checkpointer**: the requirement that human-in-the-loop
   *requires* a checkpointer formalizes "you can't pause safely without durable state"
   as a validation rule at compile/definition time.
5. **Inngest's step memoization**: named, idempotent step boundaries whose results are
   cached and replayed on retry — this gives crash-resume almost for free if the
   engine can persist step outputs keyed by step id + run id.
6. **Burr's `reads`/`writes` per-action contract**: static, declarative data
   dependencies per step, independent of step type — great for validating a workflow
   graph (detect missing writes, unused reads) without executing it, and for a
   non-author skimming what a step touches.
7. **XState's guards + `invoke` + `always`**: guard functions gate transitions;
   `invoke` cleanly marks async/service boundaries; `always` (eventless) transitions
   express automatic postcondition checks — i.e., "when this state's exit condition is
   already true, advance without waiting for an event."
8. **SCXML history states**: a standardized notion of "resume this compound state
   exactly where you left off" (shallow vs. deep) is a cleaner resume model than ad hoc
   thread/run IDs.
9. **Pydantic AI's type-checked edges**: encoding the next-step contract in a
   type signature lets a linter/type-checker catch dangling or unreachable steps
   before runtime — worth mimicking with a schema validator over the declarative file.
10. **CrewAI's `@persist` resume vs. fork**: explicitly distinguishing "continue this
    exact run" from "start a new run seeded from a past state snapshot" is a useful
    pair of resume semantics to expose to authors.
11. **Mastra's per-step I/O schemas**: typed input/output contracts per step (zod)
    catch wiring mistakes at authoring time and make steps independently testable —
    valuable when steps are authored by different people.
12. **GitHub Actions' `needs`/`if` familiarity**: even though underpowered as an FSM,
    its flat job-list-with-dependencies shape is the most instantly readable format
    surveyed — worth keeping the top-level file *shape* similarly flat even if the
    underlying model is a full state machine.
13. **AWS ASL's pure-JSON declarativeness**: keeping the definition language free of
    embedded imperative code (aside from JSONPath/JSONata data references) is what
    makes it tool-friendly (visualizers, static validation, "redrive" from point of
    failure) — a strong argument for keeping the Claude Code workflow file
    data-only, with step *implementations* registered separately (skills/scripts).
14. **OpenAI Agents SDK's handoff-as-tool + input_filter**: useful specifically for
    delegating *within* an agentic step to a sub-agent with a filtered context window,
    even though it's a poor fit for the top-level reliability-critical FSM.

## Anti-patterns / what makes these hard to author

- **Graph shape hidden in code** (LangGraph routing functions, CrewAI decorator webs,
  Pydantic AI type signatures): a non-author must read and mentally execute code to
  reconstruct the flow; no single artifact is "the workflow."
- **Determinism rules that only fail at runtime/replay** (Temporal, Inngest): putting a
  side effect outside the sanctioned boundary doesn't error at definition time, it
  silently breaks replay/resume much later — a declarative engine should validate this
  statically wherever possible.
- **Implicit control flow via LLM decision** (OpenAI Agents SDK handoffs): great for
  flexibility, bad for "every step executed, correct order" guarantees — reliability
  requires the *engine*, not the LLM, to own transitions.
- **No native retry/approval vocabulary** (GitHub Actions, XState, SCXML, Pydantic AI):
  forces authors to hand-roll these as extra states/actions each time, which is
  error-prone and inconsistent across workflows in the same project.
- **Verbose/foreign syntax** (SCXML's XML, ASL's raw JSON without a friendlier
  authoring layer) raises the barrier for a non-author, even though the underlying
  model is otherwise excellent — argues for a thin YAML/TOML layer over a JSON-like
  core, similar to how GitHub Actions wraps a DAG in approachable YAML.

## Sources not successfully fetched (marked inferred where used)
Temporal's actual code-sample pages returned only navigation indexes; XState's nested/
parallel-state examples and Stately persistence were only summarized, not fetched
verbatim. Argo Workflows and Dagger were not fetched at all due to scope/budget and are
omitted rather than guessed.
