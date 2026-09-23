package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lockFileName is the pid lockfile's name within a run directory.
const lockFileName = "lock"

// ErrHeld marks an AcquireLock refusal because dir's lock is held by another
// live process (force=false) — docs/cli.md's exit-code table (~line 33)
// calls this a refusal, not a resolution error or an engine bug: the run
// itself is fine, the caller simply lost a race with another session acting
// on it right now. cli wraps this sentinel around the detail below via %w
// so every command that can hit it (run, submit, poll, abandon) can tell it
// apart from a genuinely broken run directory with one errors.Is check,
// without needing to parse the message text. Its own text is deliberately
// short ("journal: run locked") and the detail is appended after it, not
// repeated inside it — an earlier version of this sentinel read "journal:
// run is locked by another process" and, %w-wrapped in front of the detail
// text, rendered "run is locked by pid 123: journal: run is locked by
// another process".
var ErrHeld = errors.New("journal: run locked")

// Lock is a held run lock. Release it when the run's current process is done
// acting on the run.
type Lock struct {
	path string
	pid  int
}

// AcquireLock takes dir's lock: an O_EXCL file holding the caller's pid. A
// lock whose recorded pid is not a live process — including one whose
// content is empty or unparseable, e.g. left by a crash between the O_EXCL
// create and the pid write — is taken automatically; a live holder is
// refused (LockHolder reports the holder's pid) unless force=true, which
// always steals it regardless of liveness.
//
// This still has a narrow TOCTOU window between reading a live holder's pid
// (LockHolder) and this process's own O_EXCL create: two racing callers can
// each observe a dead/absent holder and both proceed to remove+recreate,
// with only one O_EXCL winning — the loser's next iteration will then see
// the winner as a live holder and refuse or steal per its own force flag, so
// the window cannot leave two callers both believing they hold the lock.
func AcquireLock(dir string, force bool) (*Lock, error) {
	path := filepath.Join(dir, lockFileName)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			pid := os.Getpid()
			ok := false
			defer func() {
				if !ok {
					f.Close()
					os.Remove(path)
				}
			}()
			if _, err := fmt.Fprintf(f, "%d\n", pid); err != nil {
				return nil, fmt.Errorf("journal: writing lock: %w", err)
			}
			if err := f.Sync(); err != nil {
				return nil, fmt.Errorf("journal: fsyncing lock: %w", err)
			}
			if err := f.Close(); err != nil {
				return nil, fmt.Errorf("journal: closing lock: %w", err)
			}
			ok = true
			return &Lock{path: path, pid: pid}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("journal: acquiring lock: %w", err)
		}

		_, alive, herr := LockHolder(dir)
		if herr != nil {
			if os.IsNotExist(herr) {
				continue // the lock vanished between the two calls; retry
			}
			return nil, herr
		}
		if alive && !force {
			holderPID, _, _ := LockHolder(dir)
			return nil, fmt.Errorf("%w by pid %d (alive); wait, or use --force if that process is gone", ErrHeld, holderPID)
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("journal: stealing lock: %w", err)
		}
		// Loop back and retry the O_EXCL create.
	}
}

// Release removes the lock file, but only if it still names this Lock's own
// pid: after a --force steal, the process that was stolen from must not be
// able to delete the thief's lock. A lock file that has changed out from
// under this Lock (stolen, or already released) is left alone.
func (l *Lock) Release() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("journal: reading lock before release: %w", err)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil || pid != l.pid {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("journal: releasing lock: %w", err)
	}
	return nil
}

// LockHolder reads dir's lock file and reports the pid it names and whether
// that pid is a live process. A lock file that exists but does not contain a
// parseable pid (e.g. a 0-byte file from a crash between O_EXCL create and
// the pid write) is treated as a dead holder — pid 0, alive false, no error
// — rather than an unrecoverable error, so --force is never the only way
// forward. LockHolder returns an os.IsNotExist error only if dir has no lock
// file at all.
func LockHolder(dir string) (pid int, alive bool, err error) {
	data, err := os.ReadFile(filepath.Join(dir, lockFileName))
	if err != nil {
		return 0, false, err
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil {
		return 0, false, nil
	}
	return pid, pidAlive(pid), nil
}

// pidAlive reports whether pid names a live process, by sending it signal 0
// (which performs no action but fails if the process does not exist or is
// not ours to signal). EPERM means the process exists but is owned by
// another uid (common under sudo or in containers) — that is alive, not
// dead, and must not be treated as a stealable lock.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return errors.Is(err, syscall.EPERM)
}
