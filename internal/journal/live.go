package journal

import (
	"fmt"
	"os"
	"path/filepath"
)

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
// what `wf run`'s resume-when-exactly-one rule iterates over.
//
// A run directory that exists but has never recorded a single event (e.g.
// created and then abandoned before RUN_START landed) is not counted: it has
// nothing to resume. An unreadable or corrupt run directory, or workflow-id
// directory, is skipped rather than aborting the whole scan — one bad run
// must not hide every other live run for the working copy from `wf run`'s
// resume rule.
func Live(root string) ([]RunRef, error) {
	base := filepath.Join(StateBase(), Slug(root))
	workflowDirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("journal: listing runs for %s: %w", root, err)
	}

	var live []RunRef
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
			if rs.Terminal() {
				continue
			}
			live = append(live, RunRef{WorkflowID: workflowID, RunID: runID, Dir: dir, State: rs})
		}
	}
	return live, nil
}
