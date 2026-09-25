package hook

import (
	"fmt"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/guard"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// LiveRun is what the hook decisions need to know about one live run for
// the working copy the hook fired in. It is built by the cli layer from
// disk (live/, heartbeat/, driver.json, plan.json) — this package never
// touches the filesystem.
type LiveRun struct {
	RunID, WorkflowID string
	Blocked           bool
	CursorStep        string
	CursorKind        string // spec step kind of CursorStep: "deterministic", "agentic", "wait", "human", "parallel"
	ActiveSteps       []string
	Guards            []Guard // empty when the workflow has no guards
	GuardsErr         error   // non-nil → fail closed for this run
	DriverSession     string  // "" when no driver.json
}

// Guard is one compiled guards[] entry. Each is its own single-guard
// guard.Table so the union rule in decideGuards can ask about one guard at a
// time — the Table API only reports the first denying guard of a set.
type Guard struct {
	Decl  spec.GuardDecl
	table *guard.Table
}

// CompileGuards compiles each guard separately, in declaration order. It
// returns the first compile error (naming the guard), as guard.Compile does.
func CompileGuards(decls []spec.GuardDecl) ([]Guard, error) {
	out := make([]Guard, 0, len(decls))
	for _, d := range decls {
		t, err := guard.Compile([]spec.GuardDecl{d})
		if err != nil {
			return nil, err
		}
		out = append(out, Guard{Decl: d, table: t})
	}
	return out, nil
}

// Decision is a hook verdict; Reason is set whenever Allow is false.
type Decision struct {
	Allow  bool
	Reason string
}

var allow = Decision{Allow: true}

// DecidePre applies the enforcement design's PreToolUse rules for a Bash
// call: the fail-closed rule for a run whose guards could not be loaded,
// the guard union across live runs, and the subagent VCS rule. A
// pawl-and-cd-only command (IsPawlOnlyCommand) is exempt from fail-closed so
// `pawl abandon` always works even when a run's guards are broken — but only
// when *every* segment is pawl/cd: `pawl status; rm -rf x` has a pawl
// segment too, but that must not exempt the unrelated, unchecked `rm -rf x`
// from fail-closed, or any stray segment would slip past a broken guard table.
//
// The same pawl-and-cd-only exemption covers the guards: a pawl invocation
// never executes a guarded action itself (deterministic steps run
// in-process, where this hook never sees them), so a guard's match: text
// that only appears in its arguments — a `pawl submit --json` summary, a
// `pawl run` key=value — must not deny the driver's own `pawl submit` and
// strand the run at its agentic cursor.
func DecidePre(runs []LiveRun, p Payload) Decision {
	// Every rule here classifies a Bash command; a non-Bash tool call has
	// none (hooks.json matches only Bash, but a payload is not trusted to
	// have come through that matcher), so it is never denied — not even by
	// the fail-closed rule for a run with broken guards.
	if len(runs) == 0 || p.ToolName != "Bash" {
		return allow
	}
	cmd := p.Command
	if !IsPawlOnlyCommand(cmd) {
		for _, r := range runs {
			if r.GuardsErr != nil {
				return Decision{Reason: fmt.Sprintf(
					"pawl: run %s's guards could not be loaded (%v); every command is denied until it is fixed or abandoned: pawl abandon --run %s",
					r.RunID, r.GuardsErr, r.RunID)}
			}
		}
		if d := decideGuards(runs, cmd); !d.Allow {
			return d
		}
	}
	if p.AgentID != "" && IsVCSMutation(cmd) {
		r := runs[0]
		return Decision{Reason: fmt.Sprintf(
			"pawl: a subagent may not mutate VCS while run %s is live (step %s); leave commits to the workflow's own steps",
			r.RunID, r.CursorStep)}
	}
	return allow
}

// decideGuards applies the guard union rule across all live runs, per
// guard: a guard whose match: hits cmd denies it in its own run unless one
// of that run's active steps is in its only_in:. Such a deny is overridden
// only when some live run's active step permits a guard with the same
// match: pattern — a permit for a different pattern (another part of a
// chained command) does not count. A run with no guards is neutral, and a
// blocked run has no active steps, so it permits nothing.
func decideGuards(runs []LiveRun, cmd string) Decision {
	type denial struct {
		run *LiveRun
		g   spec.GuardDecl
	}
	var denials []denial
	permitted := map[string]bool{}
	for i := range runs {
		r := &runs[i]
		for _, g := range r.Guards {
			if g.table.Denied(nil, cmd) == nil {
				continue // match: does not hit cmd
			}
			if g.table.Denied(r.ActiveSteps, cmd) == nil {
				permitted[g.Decl.Match] = true
			} else {
				denials = append(denials, denial{r, g.Decl})
			}
		}
	}
	for _, dn := range denials {
		if permitted[dn.g.Match] {
			continue
		}
		where := "no step"
		if len(dn.run.ActiveSteps) > 0 {
			where = "step " + strings.Join(dn.run.ActiveSteps, ", ")
		}
		allowed := "never allowed during this run"
		if len(dn.g.OnlyIn) > 0 {
			allowed = "may only run in step " + strings.Join(dn.g.OnlyIn, ", ")
		}
		return Decision{Reason: fmt.Sprintf("pawl: denied by guard `%s` (run %s, %s): %s",
			dn.g.ID, dn.run.RunID, where, allowed)}
	}
	return allow
}

// DecideStop refuses a Stop only for the session driving a run that is
// blocked waiting on its `pawl submit` (cursor at an agentic or parallel
// step), and at most once per turn (Claude Code sets stop_hook_active on
// any retry after a prior Stop refusal, so this package must honor it or
// risk looping the session forever). It also allows whenever the payload
// reports background work still in flight (BackgroundTasks) or a scheduled
// wakeup (SessionCrons): the session is legitimately paused waiting to be
// woken back up, not walking away — e.g. it dispatched the agentic step's
// work to a background subagent and ended its turn to wait for it.
func DecideStop(runs []LiveRun, p Payload) Decision {
	if p.StopHookActive || p.BackgroundTasks > 0 || p.SessionCrons > 0 {
		return allow
	}
	for _, r := range runs {
		if r.Blocked || r.DriverSession == "" || r.DriverSession != p.SessionID {
			continue
		}
		if r.CursorKind != "agentic" && r.CursorKind != "parallel" {
			continue
		}
		return Decision{Reason: fmt.Sprintf(
			"pawl run %s (%s) is at step %s awaiting `pawl submit`. Finish it, or: pawl abandon --run %s",
			r.RunID, r.WorkflowID, r.CursorStep, r.RunID)}
	}
	return allow
}
