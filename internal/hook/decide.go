package hook

import (
	"fmt"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/guard"
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
	Guards            *guard.Table // nil when the workflow has no guards
	GuardsErr         error        // non-nil → fail closed for this run
	DriverSession     string       // "" when no driver.json
}

// Decision is a hook verdict; Reason is set whenever Allow is false.
type Decision struct {
	Allow  bool
	Reason string
}

var allow = Decision{Allow: true}

// DecidePre applies the enforcement design's PreToolUse rules for a Bash
// call: the fail-closed rule for a run whose guards could not be loaded,
// the guard union across live runs, and the subagent VCS rule. pawl's own
// commands are exempt from fail-closed so `pawl abandon` always works even
// when a run's guards are broken.
func DecidePre(runs []LiveRun, p Payload) Decision {
	if len(runs) == 0 {
		return allow
	}
	cmd := p.Command
	if !IsPawlCommand(cmd) {
		for _, r := range runs {
			if r.GuardsErr != nil {
				return Decision{Reason: fmt.Sprintf(
					"pawl: run %s's guards could not be loaded (%v); every command is denied until it is fixed or abandoned: pawl abandon --run %s",
					r.RunID, r.GuardsErr, r.RunID)}
			}
		}
	}
	if d := decideGuards(runs, cmd); !d.Allow {
		return d
	}
	if p.AgentID != "" && IsVCSMutation(cmd) {
		r := runs[0]
		return Decision{Reason: fmt.Sprintf(
			"pawl: a subagent may not mutate VCS while run %s is live (step %s); leave commits to the workflow's own steps",
			r.RunID, r.CursorStep)}
	}
	return allow
}

// decideGuards applies the guard union rule across all live runs: a command
// is denied only if every run that has guards denies it (a run with no
// guards is neutral, neither permitting nor denying). A run that would deny
// the command with no active step but permits it given its own active
// steps counts as an affirmative permit, which makes the whole call allow
// regardless of what any other run's guards say.
func decideGuards(runs []LiveRun, cmd string) Decision {
	var denied *LiveRun
	var deniedGuard string
	var deniedOnlyIn []string
	for i := range runs {
		r := &runs[i]
		if r.Guards == nil {
			continue
		}
		if g := r.Guards.Denied(r.ActiveSteps, cmd); g != nil {
			if denied == nil {
				denied, deniedGuard, deniedOnlyIn = r, g.ID, g.OnlyIn
			}
			continue
		}
		if r.Guards.Denied(nil, cmd) != nil {
			// The guard's match: hit, but this run's active step is in its
			// only_in:, so this run affirmatively permits the command.
			return allow
		}
	}
	if denied == nil {
		return allow
	}
	where := "no step"
	if len(denied.ActiveSteps) > 0 {
		where = "step " + strings.Join(denied.ActiveSteps, ", ")
	}
	allowed := "never allowed during this run"
	if len(deniedOnlyIn) > 0 {
		allowed = "may only run in step " + strings.Join(deniedOnlyIn, ", ")
	}
	return Decision{Reason: fmt.Sprintf("pawl: denied by guard `%s` (run %s, %s): %s",
		deniedGuard, denied.RunID, where, allowed)}
}

// DecideStop refuses a Stop only for the session driving a run that is
// blocked waiting on its `pawl submit` (cursor at an agentic or parallel
// step), and at most once per turn (Claude Code sets stop_hook_active on
// any retry after a prior Stop refusal, so this package must honor it or
// risk looping the session forever).
func DecideStop(runs []LiveRun, p Payload) Decision {
	if p.StopHookActive {
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
