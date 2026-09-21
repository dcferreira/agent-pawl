package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// DefaultTimeout is the engine-wide wall-clock ceiling applied to every
// exec'd command — a step's run: or a postcondition's command:
// (DESIGN.md §3), overridable (for tests, or a future --timeout flag) via
// Engine.Timeout.
const DefaultTimeout = 10 * time.Minute

// Engine ties together a loaded and validated workflow with the
// working-copy root its runs live under. It holds no per-run state itself:
// every run's state is reconstructed from its journal on every call, so an
// Engine value is safe to reuse across runs and across processes calling
// Start/Resume/Submit independently.
type Engine struct {
	// Workflow is the parsed, spec.Validate-clean workflow definition.
	// pawl run must gate on Validate returning zero errors before ever
	// constructing an Engine: the writes: schema and routing completeness
	// this package relies on are Validate's job, not this package's.
	Workflow *spec.Workflow
	// Root is the working-copy root (never the session's raw cwd —
	// DESIGN.md §5), resolved by the caller via journal.ResolveRoot.
	Root string
	// Timeout is the one engine-wide wall-clock ceiling (DESIGN.md §3).
	Timeout time.Duration
}

// New constructs an Engine for w rooted at root, with the default wall-clock
// ceiling. Callers needing a shorter ceiling (tests) set Timeout directly.
func New(w *spec.Workflow, root string) *Engine {
	return &Engine{Workflow: w, Root: root, Timeout: DefaultTimeout}
}

// Start begins a new run: it creates the run directory, acquires the lock,
// writes plan.json, journals RUN_START, and runs the loop from the
// workflow's start: step (DESIGN.md §2, §4). args are already coerced to
// the type args: declares; Start applies defaults and checks required:
// (design/format-spec.md §B.9).
func (e *Engine) Start(runID string, args map[string]any) (Instruction, error) {
	finalArgs, err := e.resolveArgs(args)
	if err != nil {
		return nil, err
	}
	dir, err := journal.CreateRunDir(e.Root, e.Workflow.Workflow, runID)
	if err != nil {
		return nil, err
	}
	lock, err := journal.AcquireLock(dir, false)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	plan, err := journal.WritePlan(dir, e.Workflow)
	if err != nil {
		return nil, err
	}
	log, err := journal.OpenLog(dir)
	if err != nil {
		return nil, err
	}
	defer log.Close()

	if _, err := log.Append(journal.Event{
		Kind:         journal.KindRunStart,
		RunID:        runID,
		Args:         finalArgs,
		Digest:       plan.Digest,
		HookSelfTest: "off (milestone 1)",
	}); err != nil {
		return nil, err
	}

	return e.runFrom(dir, log, runID, journal.Cursor{Step: e.Workflow.Start, Attempt: 1})
}

// Resume continues a non-terminal run (BLOCKED included), per DESIGN.md §4:
// it refuses a stale lock (unless force), refuses a changed workflow file
// (ErrDigestMismatch), replays the journal, and — per the ruling this
// package owns — appends the RESUME event before replaying again and
// continuing: a blocked resume's RESUME{intervention: true} resets the
// step's attempt counter to 1 in that same append, exactly as DESIGN.md §4
// step 4 describes and Replay (deliberately) does not synthesise itself.
func (e *Engine) Resume(runID string, force bool) (Instruction, error) {
	dir := journal.RunDir(e.Root, e.Workflow.Workflow, runID)
	lock, err := journal.AcquireLock(dir, force)
	if err != nil {
		return nil, err
	}
	defer lock.Release()

	plan, err := journal.ReadPlan(dir)
	if err != nil {
		return nil, err
	}
	digest, err := journal.Digest(e.Workflow)
	if err != nil {
		return nil, err
	}
	if digest != plan.Digest {
		return nil, fmt.Errorf("%w", ErrDigestMismatch)
	}

	log, err := journal.OpenLog(dir)
	if err != nil {
		return nil, err
	}
	defer log.Close()

	rs, err := replayDir(dir)
	if err != nil {
		return nil, err
	}
	if rs.Terminal() {
		return nil, fmt.Errorf("%w: run %q ended %s", ErrAlreadyTerminal, runID, rs.EndStatus)
	}

	resumeEvent := journal.Event{
		Kind:       journal.KindResume,
		RunID:      runID,
		Step:       rs.Cursor.Step,
		Attempt:    rs.Cursor.Attempt,
		AttemptKey: rs.Cursor.AttemptKey,
	}
	if rs.Ended && rs.EndStatus == "blocked" {
		resumeEvent.Attempt = 1
		resumeEvent.Intervention = true
	}
	if _, err := log.Append(resumeEvent); err != nil {
		return nil, err
	}
	rs, err = replayDir(dir)
	if err != nil {
		return nil, err
	}

	step := e.Workflow.StepByID(rs.Cursor.Step)
	if step == nil {
		return nil, fmt.Errorf("engine: internal error: resume cursor at unknown step %q", rs.Cursor.Step)
	}
	switch step.Kind {
	case "agentic":
		return e.dispatchAgentic(dir, log, runID, step, rs.Cursor, true)
	case "deterministic":
		instr, next, err := e.advanceDeterministic(dir, log, runID, rs.Cursor)
		if err != nil {
			return nil, err
		}
		if next != nil {
			return e.runFrom(dir, log, runID, *next)
		}
		return instr, nil
	case "parallel":
		return e.resumeParallel(dir, log, runID, step, rs)
	default:
		return nil, fmt.Errorf("engine: step %q: kind %q is not implemented in this build (milestone 1 MVP covers deterministic and agentic)", step.ID, step.Kind)
	}
}

// resolveArgs applies args: defaults and checks required: (design/format-spec.md
// §B.9), returning the bindings to journal on RUN_START.
func (e *Engine) resolveArgs(args map[string]any) (map[string]any, error) {
	out := map[string]any{}
	for name, decl := range e.Workflow.Args {
		if v, ok := args[name]; ok {
			out[name] = v
			continue
		}
		if decl.Default != nil {
			out[name] = *decl.Default
			continue
		}
		if decl.Required {
			return nil, fmt.Errorf("engine: missing required arg %q (type %s)", name, decl.Type)
		}
		out[name] = zeroForType(decl.Type)
	}
	return out, nil
}

func zeroForType(t string) any {
	switch t {
	case "integer":
		return int64(0)
	case "number":
		return float64(0)
	case "boolean":
		return false
	case "json":
		return nil
	default:
		return ""
	}
}

// replayDir reads and replays dir's full journal.
func replayDir(dir string) (*journal.RunState, error) {
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return nil, err
	}
	return journal.Replay(events)
}

// runFrom is the run loop (DESIGN.md §2): it executes consecutive
// deterministic steps in-process and returns only when the cursor reaches
// an agentic step (a Dispatch) or a terminal.
func (e *Engine) runFrom(dir string, log *journal.Log, runID string, cur journal.Cursor) (Instruction, error) {
	for {
		step := e.Workflow.StepByID(cur.Step)
		if step == nil {
			return nil, fmt.Errorf("engine: internal error: cursor at unknown step %q", cur.Step)
		}
		switch step.Kind {
		case "deterministic":
			instr, next, err := e.advanceDeterministic(dir, log, runID, cur)
			if err != nil {
				return nil, err
			}
			if next != nil {
				cur = *next
				continue
			}
			return instr, nil
		case "agentic":
			return e.dispatchAgentic(dir, log, runID, step, cur, false)
		case "parallel":
			return e.dispatchParallel(dir, log, runID, step, cur, false)
		default:
			return nil, fmt.Errorf("engine: step %q: kind %q is not implemented in this build (milestone 1 MVP covers deterministic and agentic)", step.ID, step.Kind)
		}
	}
}

// checkCaps reports whether entering step would exceed its max_visits: or
// the workflow's max_steps: backstop (design/format-spec.md §B.4).
func (e *Engine) checkCaps(rs *journal.RunState, step *spec.Step) bool {
	if rs.Visits[step.ID] >= step.MaxVisits {
		return true
	}
	total := 0
	for _, v := range rs.Visits {
		total += v
	}
	return total >= e.Workflow.MaxSteps
}

// beginAttempt determines the attempt number and attempt key for entering
// step at cursor cur. When step declares no attempt_key: template, the
// engine bootstraps a fresh entry with key "" and only learns the real key
// once a postcondition fails (see advanceDeterministic/Submit); when it
// does, the template is renderable upfront, so a lingering, uncleared
// budget under that same key — from an earlier visit, or across a crash —
// is picked up immediately (design/format-spec.md §B.4: "not scoped to the
// incoming edge ... provided attempt_key resolves the same").
func (e *Engine) beginAttempt(rs *journal.RunState, step *spec.Step, cur journal.Cursor, vals render.Values) (attempt int, key string, err error) {
	attempt = cur.Attempt
	if attempt == 0 {
		attempt = 1
	}
	key = cur.AttemptKey
	if key == "" && step.AttemptKey != "" {
		rendered, rerr := render.RenderProse(step.AttemptKey, vals)
		if rerr != nil {
			return 0, "", fmt.Errorf("engine: step %q: rendering attempt_key: %w", step.ID, rerr)
		}
		key = rendered
		attempt = rs.Attempts[journal.AttemptRef{Step: step.ID, Key: key}] + 1
	}
	return attempt, key, nil
}

// nextTryNumber decides the try number for a re-entry after a postcondition
// failure computed attempt key recordedForKey belongs to (rs.Attempts for
// that key before this journal write). justUsedAttempt is the attempt
// number that just failed, under whatever key it ran under.
//
// This is not simply "justUsedAttempt + 1": when the failure re-keys away
// from the automatic bootstrap key "" (attempt 1's key, since no failure has
// happened yet to hash), recordedForKey is 0 for a hash never seen before in
// this run, even though a real try was just spent — collapsing to
// recordedForKey+1 would wrongly restart the count at 1. Taking the max of
// the two and adding one is correct in every case: a brand-new key
// (recordedForKey 0) continues from justUsedAttempt; a key with a lingering,
// uncleared budget from an earlier visit (design/format-spec.md §B.4, "not
// scoped to the incoming edge") continues from that budget instead, even
// though the current visit's local justUsedAttempt is back at 1; and
// continuing under the same key within one visit has recordedForKey ==
// justUsedAttempt, so either term gives the same answer.
func nextTryNumber(justUsedAttempt, recordedForKey int) int {
	if recordedForKey > justUsedAttempt {
		return recordedForKey + 1
	}
	return justUsedAttempt + 1
}

// combinedRaw returns rs's args and state as one map, for all_set:/equals:
// postcondition evaluation (which needs the actual typed value, not its
// rendered text). A state key never written yet falls back to its
// declared default: (a default: counts as a write per design/format-spec.md
// §A, but the journal only records an actual WRITES event).
func combinedRaw(w *spec.Workflow, rs *journal.RunState) map[string]any {
	raw := make(map[string]any, len(rs.Args)+len(w.State))
	for k, decl := range w.State {
		if v, ok := rs.State[k]; ok {
			raw[k] = v
		} else if decl.Default != nil {
			raw[k] = *decl.Default
		}
	}
	for k, v := range rs.Args {
		raw[k] = v
	}
	return raw
}

// afterTransition resolves what happens once a TRANSITION to target has been
// journaled: either the run continues at another step (a non-nil cursor),
// or target is a terminal, in which case afterTransition journals RUN_END
// and returns the rendered Terminal instruction. fromStep and outcome name
// what produced the transition, for the blocked_reason pseudo-key
// (design/format-spec.md §B.2: "a fixed engine string naming the step and
// outcome").
func (e *Engine) afterTransition(dir string, log *journal.Log, runID, fromStep, outcome, target string) (Instruction, *journal.Cursor, error) {
	if e.Workflow.StepByID(target) != nil {
		return nil, &journal.Cursor{Step: target, Attempt: 1}, nil
	}

	status := "ok"
	msgTmpl := ""
	if t, ok := e.Workflow.Terminal[target]; ok {
		status = t.Status
		msgTmpl = t.Message
	} else if target == "blocked" {
		status = "blocked"
	}
	reason := ""
	if status == "blocked" {
		reason = fmt.Sprintf("%s: %s", fromStep, outcome)
	}
	if _, err := log.Append(journal.Event{
		Kind:   journal.KindRunEnd,
		RunID:  runID,
		Step:   fromStep,
		Status: status,
		Reason: reason,
	}); err != nil {
		return nil, nil, err
	}

	rs, err := replayDir(dir)
	if err != nil {
		return nil, nil, err
	}
	message := rs.BlockedReason
	if msgTmpl != "" {
		vals := buildValues(e.Workflow, rs, runID, target, 0, rs.Visits[fromStep])
		if rendered, rerr := render.RenderProse(msgTmpl, vals); rerr == nil {
			message = rendered
		}
	}
	return Terminal{RunID: runID, Status: status, StepID: target, Outcome: outcome, Message: message}, nil, nil
}

// dispatchInstruction builds the Dispatch instruction for step at attempt,
// gathering context: and rendering description: (design/format-spec.md §B.6).
// dir is the run directory, consulted only to find the previous attempt's
// failure text scoped to this exact step (finding I5) — never for anything
// that changes execution.
func (e *Engine) dispatchInstruction(dir string, rs *journal.RunState, step *spec.Step, runID string, attempt int, interrupted bool) (Instruction, error) {
	vals := buildValues(e.Workflow, rs, runID, step.ID, attempt, rs.Visits[step.ID])
	desc, err := render.RenderProse(step.Description, vals)
	if err != nil {
		desc = step.Description
	}
	items := make([]ContextItem, 0, len(step.Context))
	for _, c := range step.Context {
		item, err := e.gatherContext(c, vals)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	prev, err := e.previousFailureText(dir, step.ID, rs)
	if err != nil {
		return nil, err
	}
	return Dispatch{
		RunID:           runID,
		Step:            step.ID,
		Attempt:         attempt,
		Description:     desc,
		Context:         items,
		WritesKeys:      step.Writes.Keys,
		WritesTypes:     step.Writes.Types,
		SubagentArgs:    step.SubagentArgs,
		PreviousFailure: prev,
		Interrupted:     interrupted,
	}, nil
}

// gatherContext resolves one context: entry (A1): a "!cmd"-tagged entry has
// ${key} substituted as a shell command line (exactly as run: does,
// including DESIGN.md §9's script-path resolution — resolveScriptPathTemplate)
// and is then executed, its captured stdout becoming Value; a plain entry
// has ${key} substituted as prose and is read as a file, relative to the
// workflow file's own directory (design/format-spec.md §I: "scripts/
// resolve relative to the workflow file").
func (e *Engine) gatherContext(c spec.ContextEntry, vals render.Values) (ContextItem, error) {
	if c.IsCmd {
		cmd, err := resolveScriptPathTemplate(c.Value, vals, filepath.Dir(e.Workflow.Path))
		if err != nil {
			return ContextItem{}, fmt.Errorf("%w: rendering !cmd entry: %v", errContextUnavailable, err)
		}
		stdout, _, _, err := e.execShell(cmd, render.Keys(c.Value), vals)
		if err != nil && !errors.Is(err, errTimeout) {
			return ContextItem{}, fmt.Errorf("%w: running !cmd entry %q: %v", errContextUnavailable, cmd, err)
		}
		return ContextItem{Source: cmd, Value: stdout}, nil
	}
	path, err := render.RenderProse(c.Value, vals)
	if err != nil {
		path = c.Value
	}
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(filepath.Dir(e.Workflow.Path), path)
	}
	data, rerr := os.ReadFile(resolved)
	if rerr != nil {
		return ContextItem{}, fmt.Errorf("%w: reading context file %q: %v", errContextUnavailable, path, rerr)
	}
	return ContextItem{Source: path, Value: string(data)}, nil
}

// previousFailureText returns the most recent postcondition (or hard-
// failure diagnostic) text recorded for stepID specifically, but only when
// that step currently has an uncleared attempt budget: an uncleared entry
// in rs.Attempts is the durable signal that this exact step's last try
// failed and the failure is still "live" (design/format-spec.md §B.4,
// §B.8), which is what lets a bootstrapped re-entry (a fresh visit, whose
// own STEP_ENTER is correctly attempt 1/key "" until it fails again) still
// report the right previous-failure text (finding I5) without touching the
// global last_error pseudo-key's own, already-reviewed semantics
// (journal.RunState.LastError: the most recent POSTCONDITION across the
// whole run, not scoped to any one step).
func (e *Engine) previousFailureText(dir, stepID string, rs *journal.RunState) (string, error) {
	hasLingering := false
	for ref := range rs.Attempts {
		// A bootstrap entry's key is always "": every fresh visit leaves
		// one such artifact behind (rs.Attempts[{step,""}]) that a
		// non-catch, passing exit never clears, because clearing only
		// removes the *last* key actually used, and automatic keying
		// abandons "" the moment a real failure re-keys away from it (see
		// the "Concerns" note in the task report). Treating that harmless
		// artifact as "lingering" would resurface a stale failure's text
		// forever on every later visit, so only a real (non-"") key counts.
		if ref.Step == stepID && ref.Key != "" {
			hasLingering = true
			break
		}
	}
	if !hasLingering {
		return "", nil
	}
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return "", err
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Kind == journal.KindPostcondition && ev.Step == stepID && !ev.OK {
			return ev.Text, nil
		}
	}
	return "", nil
}

// journalFailureDiagnostic journals a POSTCONDITION-kind event carrying the
// text of a hard failure that isn't a postcondition failure at all — a
// non-zero exit, a wall-clock timeout, or unintelligible stdout (findings
// I1, I3) — purely so journal.RunState.LastError gets populated; none of
// these ever run a postcondition, so without this last_error stayed at
// whatever an earlier, unrelated step's POSTCONDITION last set it to (or
// empty), which DESIGN.md §3 requires set on exactly this path. No
// AttemptKey is stamped: a hard failure is never retried by attempts: (only
// a postcondition failure is, per design/format-spec.md §B.4), so it never
// participates in a budget lookup.
func (e *Engine) journalFailureDiagnostic(log *journal.Log, runID, stepID string, attempt int, text string) error {
	_, err := log.Append(journal.Event{
		Kind: journal.KindPostcondition, RunID: runID, Step: stepID,
		Attempt: attempt, OK: false, Text: text,
	})
	return err
}

// safeFilenameComponent replaces every character that isn't a letter,
// digit, underscore or hyphen with "_", so a value that ends up in a
// filename (a step id, in writeStepOutput) can never contain a path
// separator or a "." segment. spec's validator rejects a non-identifier
// step id at author time (finding N1's other half); this is the engine's
// own defence, for a *Workflow that reached it some other way.
func safeFilenameComponent(s string) string {
	return unsafeFilenameChar.ReplaceAllString(s, "_")
}

var unsafeFilenameChar = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// writeStepOutput persists a step attempt's full captured stdout/stderr to
// the run directory (design/format-spec.md §3: "captured in full"; §B.1:
// "everything else is logged, never parsed" — finding I2). It is best-effort
// diagnostic data, not part of the run's durable state, so a write failure
// here does not fail the step.
//
// stepID is sanitised before it ever becomes part of a path (finding N1): an
// unvalidated step id like "../../escaped" must not let a log file land
// outside the run directory. Belt and braces: even after sanitising, the
// resolved path is checked to still be inside logDir before writing.
func (e *Engine) writeStepOutput(dir, stepID string, attempt int, stdout, stderr string) {
	logDir := filepath.Join(dir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return
	}
	prefix := fmt.Sprintf("%s.%d", safeFilenameComponent(stepID), attempt)
	stdoutPath := filepath.Join(logDir, prefix+".stdout")
	stderrPath := filepath.Join(logDir, prefix+".stderr")
	if !isWithinDir(logDir, stdoutPath) || !isWithinDir(logDir, stderrPath) {
		return
	}
	_ = os.WriteFile(stdoutPath, []byte(stdout), 0o644)
	_ = os.WriteFile(stderrPath, []byte(stderr), 0o644)
}

// isWithinDir reports whether path, once cleaned, is dir itself or a
// descendant of it — a defence-in-depth check against a path that somehow
// still escapes dir despite sanitisation.
func isWithinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
