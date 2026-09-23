package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ErrIO marks a Live/FindRun failure that is a genuine I/O problem reading
// the state directory itself (e.g. permission denied listing it) — as
// opposed to "no run by that id/for this working copy", which both
// functions report by simply returning zero matches, not an error at all.
// A caller (cli) that reaches this sentinel is looking at a broken/
// unreadable run directory tree, not an unknown run: docs/cli.md's
// exit-code table calls the former an engine error (5), the latter a
// resolution error (1) — the two exits this package's own callers used to
// conflate by mapping every Live/FindRun error to 1.
var ErrIO = errors.New("journal: reading run directory tree")

// RunRef identifies one run directory found under a working copy's slug,
// together with its replayed state.
type RunRef struct {
	WorkflowID string
	RunID      string
	Dir        string
	State      *RunState
}

// Live enumerates every non-terminal run (BLOCKED included, per DESIGN.md
// §4) for the working copy rooted at root, across every workflow id. It is
// what `pawl run`'s resume-when-exactly-one rule iterates over.
//
// A run directory that exists but has never recorded a single event (e.g.
// created and then abandoned before RUN_START landed) is not counted: it has
// nothing to resume. An unreadable or corrupt run directory, or workflow-id
// directory, is skipped rather than aborting the whole scan — one bad run
// must not hide every other live run for the working copy from `pawl run`'s
// resume rule.
func Live(root string) ([]RunRef, error) {
	all, err := allRuns(root)
	if err != nil {
		return nil, err
	}
	var live []RunRef
	for _, ref := range all {
		if ref.State.Terminal() {
			continue
		}
		live = append(live, ref)
	}
	return live, nil
}

// FindRun locates runID among every run directory for this working copy —
// live or terminal — across every workflow id. Unlike Live, it does not
// filter out terminal runs: `pawl status --run <id>` needs to find a run
// that has already ended (e.g. an abandoned one, to report its recorded end
// reason), not just one still in progress. `pawl submit`, `pawl poll` and
// `pawl abandon` (internal/cli's findRunByID) use it too, for the same
// reason: each needs to tell "no run by this id at all" (a resolution
// error) apart from "that run exists but has already ended" (each
// command's own refusal or no-op to decide), which filtering to live-only
// at the lookup, the way an earlier findLiveRun did, could not do.
//
// Run ids are only 4 hex characters (see journal/rundir.go), so a live run
// in one workflow can collide with an old, terminal run of the same id in a
// different workflow — leaving os.ReadDir's directory-listing order to
// silently decide which one a caller gets. When exactly one candidate is
// still live, FindRun prefers it (overwhelmingly the one a fresh lookup
// means, and the only one an action like abandon could still affect
// anyway); with zero or with more than one live candidate for the same id,
// it refuses rather than guess, naming every candidate's workflow id and
// status.
func FindRun(root, runID string) (*RunRef, error) {
	all, err := allRuns(root)
	if err != nil {
		return nil, err
	}
	var matches []RunRef
	for _, ref := range all {
		if ref.RunID == runID {
			matches = append(matches, ref)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no run %q for this working copy", runID)
	case 1:
		return &matches[0], nil
	}

	var live []RunRef
	for _, m := range matches {
		if !m.State.Terminal() {
			live = append(live, m)
		}
	}
	if len(live) == 1 {
		return &live[0], nil
	}

	candidates := make([]string, len(matches))
	for i, m := range matches {
		candidates[i] = fmt.Sprintf("%s/%s (%s)", m.WorkflowID, m.RunID, m.State.Status())
	}
	sort.Strings(candidates)
	return nil, fmt.Errorf("run id %q is ambiguous across workflows for this working copy: %s", runID, strings.Join(candidates, ", "))
}

// allRuns walks every run directory under root's slug, replaying each one's
// events. A run directory that exists but has never recorded a single event
// (e.g. created and then abandoned before RUN_START landed) is skipped: it
// has no state to report. An unreadable or corrupt run directory, or
// workflow-id directory, is skipped rather than aborting the whole scan —
// one bad run must not hide every other run for the working copy.
func allRuns(root string) ([]RunRef, error) {
	base := filepath.Join(StateBase(), Slug(root))
	workflowDirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: listing runs for %s: %w", ErrIO, root, err)
	}

	var all []RunRef
	for _, wd := range workflowDirs {
		if !wd.IsDir() {
			continue
		}
		workflowID := wd.Name()
		runDirs, err := os.ReadDir(filepath.Join(base, workflowID))
		if err != nil {
			continue
		}
		for _, rd := range runDirs {
			if !rd.IsDir() {
				continue
			}
			runID := rd.Name()
			dir := filepath.Join(base, workflowID, runID)
			events, err := ReadEvents(dir)
			if err != nil || len(events) == 0 {
				continue
			}
			rs, err := Replay(events)
			if err != nil {
				continue
			}
			all = append(all, RunRef{WorkflowID: workflowID, RunID: runID, Dir: dir, State: rs})
		}
	}
	return all, nil
}
