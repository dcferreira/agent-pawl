package hook

import (
	"errors"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

func table(t *testing.T, gs ...spec.GuardDecl) []Guard {
	t.Helper()
	tb, err := CompileGuards(gs)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

var pushOnlyInCommit = spec.GuardDecl{ID: "push-in-commit", Match: `git push`, OnlyIn: []string{"commit"}}

func bash(cmd string) Payload {
	return Payload{SessionID: "s1", Cwd: "/w", HookEventName: "PreToolUse", ToolName: "Bash", Command: cmd}
}

func TestDecidePre_NoRunsAllows(t *testing.T) {
	if d := DecidePre(nil, bash("git push")); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_GuardDeniesOutsideOnlyIn(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", WorkflowID: "wf", CursorStep: "fix", CursorKind: "agentic",
		ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)}}
	d := DecidePre(runs, bash("git push origin main"))
	if d.Allow || !strings.Contains(d.Reason, "push-in-commit") || !strings.Contains(d.Reason, "ab12") {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_GuardAllowsInOnlyIn(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", CursorStep: "commit", CursorKind: "deterministic",
		ActiveSteps: []string{"commit"}, Guards: table(t, pushOnlyInCommit)}}
	if d := DecidePre(runs, bash("git push")); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_UnionOnePermitsWins(t *testing.T) {
	runs := []LiveRun{
		{RunID: "aaaa", ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)},
		{RunID: "bbbb", ActiveSteps: []string{"commit"}, Guards: table(t, pushOnlyInCommit)},
	}
	if d := DecidePre(runs, bash("git push")); !d.Allow {
		t.Fatalf("union: one run permits, want allow; got %+v", d)
	}
}

func TestDecidePre_UnionNeutralRunDoesNotPermit(t *testing.T) {
	runs := []LiveRun{
		{RunID: "aaaa", ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)},
		{RunID: "bbbb", ActiveSteps: []string{"x"}}, // no guards: neutral
	}
	if d := DecidePre(runs, bash("git push")); d.Allow {
		t.Fatal("a run with no matching guard must not permit")
	}
}

func TestDecidePre_BlockedRunDeniesEverywhere(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", Blocked: true, CursorStep: "commit", Guards: table(t, pushOnlyInCommit)}}
	if d := DecidePre(runs, bash("git push")); d.Allow {
		t.Fatal("BLOCKED run keeps denying guarded commands")
	}
}

func TestDecidePre_FailClosedOnBrokenGuards(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	d := DecidePre(runs, bash("ls"))
	if d.Allow || !strings.Contains(d.Reason, "ab12") || !strings.Contains(d.Reason, "bad regexp") {
		t.Fatalf("got %+v", d)
	}
}

// TestDecidePre_NonBashToolAllowed: the rules are for Bash commands only; a
// non-Bash tool call (Read, Edit, ...) must not be denied by a run whose
// guards are broken, since there is no command to classify.
func TestDecidePre_NonBashToolAllowed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	p := Payload{SessionID: "s1", Cwd: "/w", HookEventName: "PreToolUse", ToolName: "Read"}
	if d := DecidePre(runs, p); !d.Allow {
		t.Fatalf("non-Bash tool denied: %+v", d)
	}
}

func TestDecidePre_PawlCommandsExemptFromFailClosed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	for _, cmd := range []string{"pawl abandon --run ab12", "pawl status"} {
		if d := DecidePre(runs, bash(cmd)); !d.Allow {
			t.Fatalf("%q must stay allowed; got %+v", cmd, d)
		}
	}
}

// TestDecidePre_MixedPawlCommandStillFailsClosed: a command that merely contains a pawl segment among
// others must not be exempted from fail-closed wholesale — only a command
// where *every* segment is pawl (or cd) is exempt.
func TestDecidePre_MixedPawlCommandStillFailsClosed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	d := DecidePre(runs, bash("pawl status; rm x"))
	if d.Allow {
		t.Fatalf("a mixed pawl+other command must stay fail-closed; got %+v", d)
	}
}

func TestDecidePre_SubagentVCSDenied(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", WorkflowID: "wf", CursorStep: "fix", CursorKind: "agentic", ActiveSteps: []string{"fix"}}}
	p := bash("git commit -am x")
	p.AgentID = "agent-1"
	d := DecidePre(runs, p)
	if d.Allow || !strings.Contains(d.Reason, "subagent") {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_MainSessionVCSAllowed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", ActiveSteps: []string{"fix"}}}
	if d := DecidePre(runs, bash("git commit -am x")); !d.Allow {
		t.Fatalf("main session is not subject to the subagent VCS rule; got %+v", d)
	}
}

func TestDecidePre_SubagentReadOnlyVCSAllowed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", ActiveSteps: []string{"fix"}}}
	p := bash("git status && jj log")
	p.AgentID = "agent-1"
	if d := DecidePre(runs, p); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_SubagentVCSAllowedWithNoLiveRun(t *testing.T) {
	p := bash("git commit -am x")
	p.AgentID = "agent-1"
	if d := DecidePre(nil, p); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func stop(session string, active bool) Payload {
	return Payload{SessionID: session, Cwd: "/w", HookEventName: "Stop", StopHookActive: active}
}

func TestDecideStop(t *testing.T) {
	agentic := LiveRun{RunID: "ab12", WorkflowID: "green-tests", CursorStep: "fix_code", CursorKind: "agentic", DriverSession: "s1"}
	cases := []struct {
		name  string
		runs  []LiveRun
		p     Payload
		allow bool
	}{
		{"no runs", nil, stop("s1", false), true},
		{"driver at agentic blocks", []LiveRun{agentic}, stop("s1", false), false},
		{"stop_hook_active allows", []LiveRun{agentic}, stop("s1", true), true},
		{"other session allows", []LiveRun{agentic}, stop("s2", false), true},
		{"no driver allows", []LiveRun{func() LiveRun { r := agentic; r.DriverSession = ""; return r }()}, stop("s1", false), true},
		{"blocked allows", []LiveRun{func() LiveRun { r := agentic; r.Blocked = true; return r }()}, stop("s1", false), true},
		{"parallel blocks", []LiveRun{func() LiveRun { r := agentic; r.CursorKind = "parallel"; return r }()}, stop("s1", false), false},
		{"wait allows", []LiveRun{func() LiveRun { r := agentic; r.CursorKind = "wait"; return r }()}, stop("s1", false), true},
		{"human allows", []LiveRun{func() LiveRun { r := agentic; r.CursorKind = "human"; return r }()}, stop("s1", false), true},
		{"background task allows", []LiveRun{agentic}, func() Payload { p := stop("s1", false); p.BackgroundTasks = 1; return p }(), true},
		{"session cron allows", []LiveRun{agentic}, func() Payload { p := stop("s1", false); p.SessionCrons = 1; return p }(), true},
	}
	for _, c := range cases {
		d := DecideStop(c.runs, c.p)
		if d.Allow != c.allow {
			t.Errorf("%s: got %+v, want allow=%v", c.name, d, c.allow)
		}
	}
	d := DecideStop([]LiveRun{agentic}, stop("s1", false))
	want := "pawl run ab12 (green-tests) is at step fix_code awaiting `pawl submit`. Finish it, or: pawl abandon --run ab12"
	if d.Reason != want {
		t.Fatalf("reason = %q, want %q", d.Reason, want)
	}
}

// A pawl-only command never executes a guarded action itself (deterministic
// steps run in-process, unseen by the hook), so a guard's match: text that
// merely appears inside its arguments — a submit's JSON summary, a run's
// key=value — must not deny it: denying the driver's own `pawl submit`
// would strand the run at its agentic cursor.
func TestDecidePre_PawlOnlyCommandExemptFromGuards(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", CursorStep: "fix", CursorKind: "agentic",
		ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)}}
	for _, cmd := range []string{
		`pawl submit --run ab12 --step fix --json '{"summary":"did not git push"}'`,
		`cd /w && pawl run wf msg="then git push"`,
		// Separators and fd redirects inside/after a quoted argument don't
		// make a new, non-pawl segment (review round 3).
		`pawl submit --run ab12 --step fix --json '{"summary":"bumped dep; did not git push"}'`,
		`pawl submit --run ab12 --step fix --json '{"summary":"a | git push & b"}' 2>&1`,
	} {
		if d := DecidePre(runs, bash(cmd)); !d.Allow {
			t.Fatalf("%q: got %+v", cmd, d)
		}
	}
	// A non-pawl segment chained after one is still guarded.
	if d := DecidePre(runs, bash(`pawl status; git push`)); d.Allow {
		t.Fatal("a guarded non-pawl segment must still be denied")
	}
}

// The quote-aware pawl-only exemption also covers fail-closed: a driver's
// submit whose summary contains a separator still gets through a run with
// broken guards, but command substitution inside a pawl argument does not.
func TestDecidePre_FailClosedQuotedPawlSubmit(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	if d := DecidePre(runs, bash(`pawl submit --run ab12 --step fix --json '{"summary":"x; y"}' 2>&1`)); !d.Allow {
		t.Fatalf("got %+v", d)
	}
	if d := DecidePre(runs, bash(`pawl run wf msg="$(rm -rf x)"`)); d.Allow {
		t.Fatal("command substitution inside a pawl argument must not be exempt")
	}
}

// The guard union is computed per guard, not per command: run A's permit
// for its `git push` guard says nothing about run B's unrelated `rm -rf`
// guard, so a command chaining both must still be denied by B.
func TestDecidePre_UnionIsPerGuardNotPerCommand(t *testing.T) {
	rmNever := spec.GuardDecl{ID: "no-rm-rf", Match: `rm -rf`, OnlyIn: []string{}}
	runs := []LiveRun{
		{RunID: "aaaa", ActiveSteps: []string{"commit"}, Guards: table(t, pushOnlyInCommit)},
		{RunID: "bbbb", ActiveSteps: []string{"fix"}, Guards: table(t, rmNever)},
	}
	d := DecidePre(runs, bash("git push && rm -rf build"))
	if d.Allow || !strings.Contains(d.Reason, "no-rm-rf") || !strings.Contains(d.Reason, "bbbb") {
		t.Fatalf("A's permit for `git push` must not override B's deny for `rm -rf`; got %+v", d)
	}
	// Same pattern in both runs: A's permit does override B's deny.
	runs[1].Guards = table(t, pushOnlyInCommit)
	runs[1].ActiveSteps = []string{"fix"}
	if d := DecidePre(runs, bash("git push")); !d.Allow {
		t.Fatalf("same match: permitted by A must be allowed; got %+v", d)
	}
}
