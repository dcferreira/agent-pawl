package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
)

// defaultExeMode is what AtomicReplace chmods the replacement to when
// exePath doesn't exist yet (nothing to preserve a mode from).
const defaultExeMode = 0755

// AtomicReplace writes content to exePath by creating a temp file in
// exePath's own directory, fsyncing it, chmod'ing it to match the
// existing target's own permission bits (defaultExeMode if there is no
// existing target), and os.Rename'ing it over exePath — the rename is
// atomic on the same filesystem (and CreateTemp in the same directory as
// exePath guarantees that), so a reader of exePath (including a
// concurrently-running copy of the binary) never observes a
// partially-written file. The temp file is removed on any failure; it
// never happens that this function both leaves a temp file behind and
// returns nil. Only the permission bits are preserved — this never
// attempts to chown; ownership follows whatever the process's own uid
// produces on CreateTemp, matching install.sh's own `install -m 0755` /
// `cp && chmod` fallback.
func AtomicReplace(exePath string, content []byte) error {
	mode := os.FileMode(defaultExeMode)
	if info, err := os.Stat(exePath); err == nil {
		mode = info.Mode().Perm()
	}

	dir := filepath.Dir(exePath)
	tmp, err := os.CreateTemp(dir, ".pawl-update-*")
	if err != nil {
		return fmt.Errorf("selfupdate: %s is not writable (%w); re-run with permission to write there, or reinstall with install.sh and INSTALL_DIR set to a writable directory", dir, err)
	}
	tmpPath := tmp.Name()
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(content); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("selfupdate: writing %s: %w", tmpPath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("selfupdate: syncing %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("selfupdate: closing %s: %w", tmpPath, err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("selfupdate: chmod %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, exePath); err != nil {
		return fmt.Errorf("selfupdate: replacing %s: %w", exePath, err)
	}
	succeeded = true
	return nil
}
