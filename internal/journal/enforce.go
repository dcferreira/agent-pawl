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
// "pawl run / submit / poll changes").
const HeartbeatTTL = 10 * time.Second

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
// a symlink for every live run, none for anything else. Other working
// copies' entries are left alone.
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
		want[prefix+r.WorkflowID+"__"+r.RunID] = r.Dir
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("journal: reading live index: %w", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		if target, ok := want[name]; ok {
			if cur, err := os.Readlink(filepath.Join(dir, name)); err == nil && cur == target {
				delete(want, name)
				continue
			}
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
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
