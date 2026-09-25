package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

const envEnforcement = "PAWL_ENFORCEMENT"

// enforcementMode is what pawl run found about the enforcement hooks.
type enforcementMode struct {
	On        bool
	OffReason string // "--no-enforcement" or "PAWL_ENFORCEMENT=off" when !On
	SessionID string // the driving session, from the heartbeat, when On; "" for a terminal resume
	// BoundNoHeartbeat is set for a resume of a run bound on at RUN_START
	// with no fresh heartbeat behind this invocation (a person resuming from
	// a plain terminal): the run stays enforced, but no hooked session
	// vouched for this call, so none is stamped as the driver.
	BoundNoHeartbeat bool
}

//lint:ignore ST1005 verbatim two-sentence refusal message (Global Constraint: "Refusal message, verbatim")
var errNoHeartbeat = errors.New("pawl: refusing to start: pawl's PreToolUse hook has not fired for this working copy in the last 5 minutes.\n" +
	"Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.")

// checkEnforcement decides whether pawl run may start: an explicit opt-out,
// or a PreToolUse heartbeat for root no older than journal.HeartbeatTTL.
func checkEnforcement(root string, flagOff bool) (enforcementMode, error) {
	if flagOff {
		return enforcementMode{OffReason: "--no-enforcement"}, nil
	}
	if os.Getenv(envEnforcement) == "off" {
		return enforcementMode{OffReason: envEnforcement + "=off"}, nil
	}
	hb, ok, err := journal.ReadHeartbeat(root)
	if err != nil || !ok || !hb.Fresh(nowFunc()) {
		return enforcementMode{}, errNoHeartbeat
	}
	return enforcementMode{On: true, SessionID: hb.SessionID}, nil
}

// resumeEnforcement is checkEnforcement for a resume of ref. A run's mode is
// bound at RUN_START (journal.RunState.Enforcement) and the hooks follow that
// recorded mode for the run's whole life, so this invocation's opt-out can
// neither turn an enforced run off nor is it needed for an opted-out one:
//
//   - a run bound off resumes off with no heartbeat gate, and the banner
//     says the mode was bound at run start;
//   - a run bound on refuses --no-enforcement / PAWL_ENFORCEMENT=off (it
//     would have no effect, and printing "off" would contradict the hooks),
//     and otherwise resumes with no heartbeat gate either: a person may
//     resume it from a plain terminal (after a reboot, or after fixing the
//     world for a BLOCKED run). Deterministic steps run and the run stops at
//     its next DISPATCH/ASK as usual; the hooks keep applying to it. Only a
//     fresh heartbeat (a hooked session resuming) makes this invocation's
//     session the run's new driver.
func resumeEnforcement(root string, ref *journal.RunRef, flagOff bool) (enforcementMode, error) {
	if ref.State.EnforcementOff() {
		reason := strings.TrimSuffix(strings.TrimPrefix(ref.State.Enforcement, "off ("), ")")
		return enforcementMode{OffReason: "bound at run start: " + reason}, nil
	}
	if flagOff || os.Getenv(envEnforcement) == "off" {
		how := "--no-enforcement"
		if !flagOff {
			how = envEnforcement + "=off"
		}
		return enforcementMode{}, fmt.Errorf("pawl run: run %s was started with enforcement on, and enforcement is bound at run start — %s cannot turn it off. To continue the run, resume without it (no hook heartbeat is needed to resume); it stays enforced", ref.RunID, how)
	}
	hb, ok, err := journal.ReadHeartbeat(root)
	if err != nil || !ok || !hb.Fresh(nowFunc()) {
		return enforcementMode{On: true, BoundNoHeartbeat: true}, nil
	}
	return enforcementMode{On: true, SessionID: hb.SessionID}, nil
}

// enforcementLabel is RUN_START's hook_self_test value:
// what checkEnforcement actually decided, recorded on the Engine before Start
// so the journal reflects reality instead of a hardcoded placeholder.
func enforcementLabel(mode enforcementMode) string {
	if mode.On {
		return "on (PreToolUse heartbeat)"
	}
	return "off (" + mode.OffReason + ")"
}

// stampDriver records the session behind a fresh heartbeat as runID's
// driver — the re-stamp pawl submit and pawl poll do, so a new session that
// picks the run up becomes its driver. Best-effort: a missing or stale
// heartbeat leaves driver.json as it was.
func stampDriver(root, workflowID, runID string) {
	hb, ok, err := journal.ReadHeartbeat(root)
	if err != nil || !ok || !hb.Fresh(nowFunc()) {
		return
	}
	stampDriverAs(root, workflowID, runID, hb.SessionID)
}

// stampDriverAs records sessionID as runID's driver. pawl run passes the
// session checkEnforcement gated on (enforcementMode.SessionID) rather than
// re-reading the heartbeat after Start/Resume: the engine may have run a
// long deterministic prefix by then, so the heartbeat can have gone stale
// (leaving the Stop hook with no driver to guard) or been overwritten by
// another session. Best-effort: a write failure only weakens the Stop hook
// for this run.
//
// A run started with enforcement opted out (RUN_START's hook_self_test,
// journal.RunState.EnforcementOff) never gets a driver, even when a fresh
// heartbeat exists — with the plugin installed, the PreToolUse for the
// `pawl run … --no-enforcement` call itself has just written one. The
// opt-out is bound at run start, so submit/poll/resume honor it too.
func stampDriverAs(root, workflowID, runID, sessionID string) {
	if sessionID == "" {
		return
	}
	dir := journal.RunDir(root, workflowID, runID)
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return
	}
	rs, err := journal.Replay(events)
	if err != nil || rs.EnforcementOff() {
		return
	}
	_ = journal.WriteDriver(dir, sessionID, nowFunc())
}
