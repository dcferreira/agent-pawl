package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

func enforcedWC(t *testing.T) string {
	t.Helper()
	root := setupWorkingCopy(t)
	t.Setenv("PAWL_ENFORCEMENT", "") // undo setupWorkingCopy's opt-out
	writeWorkflow(t, root, "hookwf", hookWF)
	return root
}

func runPawl(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(append([]string{"pawl"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRun_RefusesWithoutHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	code, out, errs := runPawl("run", "hookwf")
	if code != 4 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if !strings.Contains(errs, "pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.") ||
		!strings.Contains(errs, "Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.") {
		t.Fatalf("err=%q", errs)
	}
	if live, _ := journal.Live(root); len(live) != 0 {
		t.Fatal("a refused run must not create a run directory")
	}
}

func TestRun_RefusesStaleHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now().Add(-6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runPawl("run", "hookwf"); code != 4 {
		t.Fatalf("code=%d", code)
	}
}

func TestRun_FreshHeartbeatStartsAndStampsDriver(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPawl("run", "hookwf")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if !strings.Contains(out, "hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)") ||
		!strings.Contains(out, "guards: 1 advisory (pattern-matched)") {
		t.Fatalf("banner: %q", out)
	}
	live, _ := journal.Live(root)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	d, ok, _ := journal.ReadDriver(live[0].Dir)
	if !ok || d.SessionID != "s1" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}

func TestRun_NoEnforcementFlag(t *testing.T) {
	enforcedWC(t)
	code, out, _ := runPawl("run", "hookwf", "--no-enforcement")
	if code != 0 || !strings.Contains(out, "enforcement: off (--no-enforcement)") ||
		!strings.Contains(out, "guards: 1 declared, NOT enforced") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRun_EnvOptOut(t *testing.T) {
	enforcedWC(t)
	t.Setenv("PAWL_ENFORCEMENT", "off")
	code, out, _ := runPawl("run", "hookwf")
	if code != 0 || !strings.Contains(out, "enforcement: off (PAWL_ENFORCEMENT=off)") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRun_CreatesLiveIndexEntry(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	runPawl("run", "hookwf")
	live, _ := journal.Live(root)
	link := journal.LiveIndexDir() + "/" + journal.Slug(root) + "__hookwf__" + live[0].RunID
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("live index: %v", err)
	}
}

// An opted-out run is invisible to the hooks, so it gets no live/ entry.
func TestRun_OptedOutRunGetsNoLiveIndexEntry(t *testing.T) {
	root := enforcedWC(t)
	runPawl("run", "hookwf", "--no-enforcement")
	live, _ := journal.Live(root)
	link := journal.LiveIndexDir() + "/" + journal.Slug(root) + "__hookwf__" + live[0].RunID
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("opted-out run got a live index entry: err=%v", err)
	}
}

func TestAbandon_RemovesLiveIndexEntry(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	runPawl("run", "hookwf")
	live, _ := journal.Live(root)
	if entries, _ := os.ReadDir(journal.LiveIndexDir()); len(entries) != 1 {
		t.Fatalf("precondition: want one live index entry, got %v", entries)
	}
	if code, _, errs := runPawl("abandon", "--run", live[0].RunID); code != 0 {
		t.Fatalf("abandon: %d %s", code, errs)
	}
	entries, _ := os.ReadDir(journal.LiveIndexDir())
	if len(entries) != 0 {
		t.Fatalf("live index not pruned: %v", entries)
	}
}

func TestSubmit_StampsDriverFromFreshHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	live, _ := journal.Live(root)
	// A different session now drives the run: its submit re-stamps driver.json.
	if err := journal.WriteHeartbeat(root, "s2", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, _, errs := runPawl("submit", "--run", live[0].RunID, "--step", "fix", "--json", `{"done": "true"}`)
	if code != 0 {
		t.Fatalf("submit: %d %s", code, errs)
	}
	// The run is terminal now, so read the driver from the run dir directly.
	d, ok, _ := journal.ReadDriver(live[0].Dir)
	if !ok || d.SessionID != "s2" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}

// TestRun_NoEnforcementIgnoresFreshHeartbeat: with the plugin installed, the
// PreToolUse for `pawl run … --no-enforcement` itself has just written a
// fresh heartbeat. The opt-out must still mean what the banner says — no
// driver.json (so Stop never refuses), guards not enforced — and it stays
// bound to the run: a later submit with a fresh heartbeat stamps nothing.
func TestRun_NoEnforcementIgnoresFreshHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf", "--no-enforcement"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	live, _ := journal.Live(root)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	runID := live[0].RunID
	if _, ok, _ := journal.ReadDriver(live[0].Dir); ok {
		t.Fatal("opt-out run must have no driver.json")
	}
	if code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "git push origin main", "")); code != 0 || out != "" {
		t.Fatalf("guard enforced on an opt-out run: code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "git commit -am x", "agent-1")); code != 0 || out != "" {
		t.Fatalf("subagent VCS rule enforced on an opt-out run: code=%d out=%q err=%q", code, out, errs)
	}
	if code, out, errs := hookCall(t, "stop", stopPayload(root, "s1", false)); code != 0 || out != "" {
		t.Fatalf("Stop refused on an opt-out run: code=%d out=%q err=%q", code, out, errs)
	}
	if code, _, errs := runPawl("submit", "--run", runID, "--step", "fix", "--json", `{"done": "true"}`); code != 0 {
		t.Fatalf("submit: %d %s", code, errs)
	}
	if _, ok, _ := journal.ReadDriver(live[0].Dir); ok {
		t.Fatal("submit must not stamp a driver on an opt-out run")
	}
}

// startRun starts hookwf with args and returns its run id.
func startRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	if code, _, errs := runPawl(append([]string{"run", "hookwf"}, args...)...); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	live, _ := journal.Live(root)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	return live[0].RunID
}

// TestResume_OptedOutRunNeedsNoHeartbeat: a run started with
// --no-enforcement is bound off for life, so resuming it from a plain
// terminal (no heartbeat, no flag) must not be refused for a missing
// heartbeat, and the banner must say the mode is the one bound at start.
func TestResume_OptedOutRunNeedsNoHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	runID := startRun(t, root, "--no-enforcement")
	code, out, errs := runPawl("run", "hookwf")
	if code != 0 {
		t.Fatalf("resume refused: code=%d err=%q", code, errs)
	}
	if !strings.Contains(out, "enforcement: off (bound at run start: --no-enforcement)") ||
		!strings.Contains(out, "guards: 1 declared, NOT enforced") ||
		!strings.Contains(out, "resume: run "+runID) {
		t.Fatalf("banner: %q", out)
	}
}

// TestResume_OptedOutRunIgnoresFreshHeartbeat: a fresh heartbeat on resume
// must not make the banner claim hooks/guards the hooks will never apply to
// this run (buildLiveRuns skips opted-out runs), and must stamp no driver.
func TestResume_OptedOutRunIgnoresFreshHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	startRun(t, root, "--no-enforcement")
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPawl("run", "hookwf")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if strings.Contains(out, "hooks: PreToolUse") || strings.Contains(out, "advisory") ||
		!strings.Contains(out, "enforcement: off (bound at run start: --no-enforcement)") {
		t.Fatalf("banner: %q", out)
	}
	live, _ := journal.Live(root)
	if _, ok, _ := journal.ReadDriver(live[0].Dir); ok {
		t.Fatal("resume must not stamp a driver on an opt-out run")
	}
}

// TestResume_OptOutOnEnforcedRunRefused: enforcement is bound at run start,
// so --no-enforcement (or PAWL_ENFORCEMENT=off) on a resume of an enforced
// run cannot turn the hooks off; saying "off" would be a lie, so it's
// refused — without steering the user to an opt-out as the way to resume,
// since a plain resume needs no heartbeat.
func TestResume_OptOutOnEnforcedRunRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		env  string
		args []string
	}{
		{"flag", "", []string{"--no-enforcement"}},
		{"env", "off", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := enforcedWC(t)
			if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
				t.Fatal(err)
			}
			runID := startRun(t, root)
			t.Setenv("PAWL_ENFORCEMENT", tc.env)
			code, out, errs := runPawl(append([]string{"run", "hookwf"}, tc.args...)...)
			if code != 4 || out != "" {
				t.Fatalf("code=%d out=%q err=%q", code, out, errs)
			}
			if !strings.Contains(errs, "run "+runID+" was started with enforcement on") ||
				!strings.Contains(errs, "resume without it") ||
				strings.Contains(errs, "--fresh") || strings.Contains(errs, "or pass --no-enforcement") {
				t.Fatalf("err=%q", errs)
			}
		})
	}
}

// TestResume_EnforcedRunFromTerminal: a person may resume an enforced run
// from a plain terminal (no fresh heartbeat). The run stays bound on — the
// banner says so, the guards stay advisory-enforced by the hooks — and the
// existing driver.json is left alone, since no hooked session is resuming.
func TestResume_EnforcedRunFromTerminal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"stale heartbeat", func(t *testing.T, root string) {
			if err := journal.WriteHeartbeat(root, "s9", time.Now().Add(-6*time.Minute)); err != nil {
				t.Fatal(err)
			}
		}},
		{"no heartbeat file", func(t *testing.T, root string) {
			if err := os.RemoveAll(journal.PawlHome() + "/heartbeat"); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := enforcedWC(t)
			if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
				t.Fatal(err)
			}
			runID := startRun(t, root)
			tc.setup(t, root)
			code, out, errs := runPawl("run", "hookwf")
			if code != 0 {
				t.Fatalf("terminal resume refused: code=%d err=%q", code, errs)
			}
			if !strings.Contains(out, "enforcement: on (bound at run start; no hook heartbeat for this resume)") ||
				!strings.Contains(out, "guards: 1 advisory (pattern-matched)") ||
				strings.Contains(out, "hooks: PreToolUse") ||
				!strings.Contains(out, "resume: run "+runID) ||
				!strings.Contains(out, "DISPATCH") {
				t.Fatalf("out=%q", out)
			}
			live, _ := journal.Live(root)
			if len(live) != 1 || live[0].State.EnforcementOff() {
				t.Fatalf("run must stay live and bound on: %+v", live)
			}
			d, ok, _ := journal.ReadDriver(live[0].Dir)
			if !ok || d.SessionID != "s1" {
				t.Fatalf("driver=%+v ok=%v, want the untouched s1", d, ok)
			}
			// The hooks still apply to it: its guard denies.
			if code, hout, _ := hookCall(t, "pre", prePayload(root, "s1", "git push origin main", "")); code != 0 || !strings.Contains(hout, "deny") {
				t.Fatalf("guard not enforced after a terminal resume: code=%d out=%q", code, hout)
			}
		})
	}
}

// TestResume_EnforcedRunFromHookedSessionRestamps: a hooked session
// (fresh heartbeat) resuming an enforced run gets the enforced banner and
// becomes the run's driver.
func TestResume_EnforcedRunFromHookedSessionRestamps(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	startRun(t, root)
	if err := journal.WriteHeartbeat(root, "s2", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPawl("run", "hookwf")
	if code != 0 || !strings.Contains(out, "hooks: PreToolUse ✔ (heartbeat)") {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	live, _ := journal.Live(root)
	if d, ok, _ := journal.ReadDriver(live[0].Dir); !ok || d.SessionID != "s2" {
		t.Fatalf("driver=%+v ok=%v, want s2", d, ok)
	}
}

// probeWF's deterministic first step records, while it runs, what the
// live/ index and the run directories hold — i.e. what a pawl run killed
// mid-prefix would leave behind.
const probeWF = `workflow: probe
start: look
state:
  done: {type: string, default: ""}
steps:
  - id: look
    kind: deterministic
    run: ./probe.sh
    next: fix
  - id: fix
    kind: agentic
    description: "fix it"
    writes:
      done: {type: string}
    postcondition: {all_set: [done]}
    next: done
terminal:
  done: {status: ok, message: "ok"}
`

// TestRun_DriverAndLiveLinkBeforePrefix: driver.json and the live/ link are
// written right after RUN_START, before the deterministic prefix runs.
func TestRun_DriverAndLiveLinkBeforePrefix(t *testing.T) {
	root := enforcedWC(t)
	writeWorkflow(t, root, "probe", probeWF)
	writeExecutable(t, root+"/.claude/workflows", "probe.sh", "#!/bin/sh\n"+
		"ls '"+journal.LiveIndexDir()+"' > live.txt 2>&1\n"+
		"find '"+journal.StateBase()+"' -name driver.json -exec cat {} + > driver.txt 2>&1\n")
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, out, errs := runPawl("run", "probe"); code != 0 {
		t.Fatalf("run: %d out=%q err=%q", code, out, errs)
	}
	live, _ := os.ReadFile(root + "/live.txt")
	if !strings.Contains(string(live), journal.Slug(root)+"__probe__") {
		t.Fatalf("no live/ link during the prefix: %q", live)
	}
	driver, _ := os.ReadFile(root + "/driver.txt")
	if !strings.Contains(string(driver), `"s1"`) {
		t.Fatalf("no driver.json during the prefix: %q", driver)
	}
}

// staleAfterGate makes nowFunc return base for its first call (the
// enforcement gate's heartbeat check) and base+HeartbeatTTL+1m for every
// later one: the heartbeat goes stale between the gate and the driver
// stamp, as it does when the permission prompt plus a long deterministic
// prefix outlasts the TTL.
func staleAfterGate(t *testing.T, base time.Time) {
	t.Helper()
	calls := 0
	old := nowFunc
	nowFunc = func() time.Time {
		calls++
		if calls == 1 {
			return base
		}
		return base.Add(journal.HeartbeatTTL + time.Minute)
	}
	t.Cleanup(func() { nowFunc = old })
}

func TestRun_StampsGatedSessionEvenIfHeartbeatGoesStale(t *testing.T) {
	root := enforcedWC(t)
	base := time.Now()
	if err := journal.WriteHeartbeat(root, "s1", base); err != nil {
		t.Fatal(err)
	}
	staleAfterGate(t, base)
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	live, _ := journal.Live(root)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	d, ok, _ := journal.ReadDriver(live[0].Dir)
	if !ok || d.SessionID != "s1" {
		t.Fatalf("driver=%+v ok=%v, want the gated session s1", d, ok)
	}
}

func TestResume_StampsGatedSessionEvenIfHeartbeatGoesStale(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("run: %d %s", code, errs)
	}
	live, _ := journal.Live(root)
	base := time.Now()
	if err := journal.WriteHeartbeat(root, "s2", base); err != nil {
		t.Fatal(err)
	}
	staleAfterGate(t, base)
	if code, _, errs := runPawl("run", "hookwf"); code != 0 {
		t.Fatalf("resume: %d %s", code, errs)
	}
	d, ok, _ := journal.ReadDriver(live[0].Dir)
	if !ok || d.SessionID != "s2" {
		t.Fatalf("driver=%+v ok=%v, want the gated session s2", d, ok)
	}
}
