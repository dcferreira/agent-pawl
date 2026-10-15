package hook

import "testing"

// The skill tells the agent to run pawl by the plugin launcher's path (not a
// bare `pawl` that PATH may resolve to a shadowing binary); the hook's
// classification is by the first token's basename, so a quoted path with
// spaces and ".." segments must still count as a pawl command.
func TestLauncherPathInvocationIsPawlCommand(t *testing.T) {
	for _, cmd := range []string{
		`"/home/u/.claude/plugins/cache/agent-pawl/0.9.0/skills/pawl/../../bin/pawl" run wf`,
		`'/home/u/my plugin/bin/pawl' submit --run r1 --step s --json '{"a":1}'`,
		`cd /repo && "/home/u/my plugin/skills/pawl/../../bin/pawl" run wf x=1`,
	} {
		if !IsPawlCommand(cmd) {
			t.Errorf("IsPawlCommand(%q) = false", cmd)
		}
	}
	if !IsPawlOnlyCommand(`cd /repo && "/home/u/my plugin/skills/pawl/../../bin/pawl" run wf x=1`) {
		t.Error("launcher-path invocation should be pawl-only")
	}
}
