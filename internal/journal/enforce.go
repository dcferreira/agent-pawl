package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HeartbeatTTL is how recently pawl's PreToolUse hook must have fired for
// `pawl run` to treat the enforcement hooks as installed (enforcement spec,
// "pawl run / submit / poll changes"). 5 minutes, not 10s: PreToolUse fires
// before Claude Code's own permission prompt, so a user who takes longer
// than a few seconds to approve the `pawl run` call must not see it refused
// with a message claiming the hook "has not fired" when it plainly has.
const HeartbeatTTL = 5 * time.Minute

// PawlHome is the directory holding runs/, live/ and heartbeat/:
// ~/.claude/pawl, or the parent of $PAWL_STATE_DIR in tests.
func PawlHome() string { return filepath.Dir(StateBase()) }

// LiveIndexDir holds one symlink per live run, named
// <slug>__<workflowID>__<runID>. It is a fast-path cache for bin/pawl-hook
// only: which runs are live is always decided by Live(root).
func LiveIndexDir() string { return filepath.Join(PawlHome(), "live") }

func heartbeatPath(root string) string {
	return filepath.Join(PawlHome(), "heartbeat", Slug(root)+".json")
}

// Heartbeat records that pawl's PreToolUse hook saw a pawl command for a
// working copy, and from which Claude Code session.
type Heartbeat struct {
	SessionID string    `json:"session_id"`
	Time      time.Time `json:"time"`
}

// Fresh reports whether h was written no more than HeartbeatTTL before now
// (and not after it).
func (h Heartbeat) Fresh(now time.Time) bool {
	age := now.Sub(h.Time)
	return age >= 0 && age <= HeartbeatTTL
}

func WriteHeartbeat(root, sessionID string, now time.Time) error {
	return writeJSONAtomic(heartbeatPath(root), Heartbeat{SessionID: sessionID, Time: now})
}

func ReadHeartbeat(root string) (Heartbeat, bool, error) {
	var h Heartbeat
	ok, err := readJSON(heartbeatPath(root), &h)
	return h, ok, err
}

// Driver names the Claude Code session currently driving a run; the Stop
// hook only refuses that session.
type Driver struct {
	SessionID string    `json:"session_id"`
	Updated   time.Time `json:"updated"`
}

func WriteDriver(runDir, sessionID string, now time.Time) error {
	return writeJSONAtomic(filepath.Join(runDir, "driver.json"), Driver{SessionID: sessionID, Updated: now})
}

func ReadDriver(runDir string) (Driver, bool, error) {
	var d Driver
	ok, err := readJSON(filepath.Join(runDir, "driver.json"), &d)
	return d, ok, err
}

// SyncLiveIndex makes LiveIndexDir's entries for root's slug match Live(root):
// a symlink for every live run that enforcement applies to (a run started
// opted out is invisible to the hooks, so it gets none), none for anything
// else. Other working copies' entries are left alone, except that a
// dangling link of any slug — its run directory gone, e.g. with a deleted
// checkout — is pruned: no sync for that slug may ever run again, and a
// dangling link keeps bin/pawl-hook's fast path invoking pawl for nothing.
func SyncLiveIndex(root string) error {
	live, err := Live(root)
	if err != nil {
		return err
	}
	dir := LiveIndexDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("journal: creating live index: %w", err)
	}
	prefix := Slug(root) + "__"
	want := map[string]string{}
	for _, r := range live {
		if r.State.EnforcementOff() {
			continue
		}
		want[prefix+r.WorkflowID+"__"+r.RunID] = r.Dir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("journal: reading live index: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		path := filepath.Join(dir, name)
		if !strings.HasPrefix(name, prefix) {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("journal: pruning live index: %w", err)
				}
			}
			continue
		}
		if target, ok := want[name]; ok {
			if cur, err := os.Readlink(path); err == nil && cur == target {
				delete(want, name)
				continue
			}
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("journal: pruning live index: %w", err)
		}
	}
	for name, target := range want {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("journal: linking live run: %w", err)
		}
	}
	return nil
}

// LiveIndexed returns the non-terminal runs LiveIndexDir points at, across
// every working copy — how `pawl hook stop` finds the run a session is
// driving when the session's cwd has left that run's working copy. The
// index is only a cache, so every entry is verified against the journal:
// its target must be a run directory <StateBase>/<slug>/<workflow>/<run>
// whose components spell the entry's own name, with at least one recorded
// event, and not terminal. Anything else is skipped, not an error.
func LiveIndexed() ([]RunRef, error) {
	dir := LiveIndexDir()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: reading live index: %w", ErrIO, err)
	}
	base := StateBase()
	var out []RunRef
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		rel, err := filepath.Rel(base, filepath.Clean(target))
		if err != nil {
			continue
		}
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) != 3 || parts[0] == ".." || parts[1] == ".." || parts[2] == ".." ||
			e.Name() != parts[0]+"__"+parts[1]+"__"+parts[2] {
			continue
		}
		runDir := filepath.Join(base, parts[0], parts[1], parts[2])
		events, err := ReadEvents(runDir)
		if err != nil || len(events) == 0 {
			continue
		}
		rs, err := Replay(events)
		if err != nil || rs.Terminal() {
			continue
		}
		out = append(out, RunRef{WorkflowID: parts[1], RunID: parts[2], Dir: runDir, State: rs})
	}
	return out, nil
}

func writeJSONAtomic(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("journal: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return fmt.Errorf("journal: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("journal: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return fmt.Errorf("journal: %w", err)
	}
	return nil
}

func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("journal: %w", err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("journal: parsing %s: %w", path, err)
	}
	return true, nil
}
