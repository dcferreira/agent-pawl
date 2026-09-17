package journal

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

func TestLock_FreshAcquireAndRelease(t *testing.T) {
	dir := t.TempDir()
	l, err := AcquireLock(dir, false)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	pid, alive, err := LockHolder(dir)
	if err != nil {
		t.Fatalf("LockHolder: %v", err)
	}
	if pid != os.Getpid() || !alive {
		t.Errorf("LockHolder = %d/%v, want %d/true", pid, alive, os.Getpid())
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, lockFileName)); !os.IsNotExist(err) {
		t.Errorf("lock file still exists after Release: %v", err)
	}
}

func TestLock_RefusesLiveHolder(t *testing.T) {
	dir := t.TempDir()
	// Fake a live holder: our own pid is definitely alive, and is not us
	// (AcquireLock only ever writes its own pid), so write it directly.
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := AcquireLock(dir, false)
	if err == nil {
		t.Fatal("AcquireLock: want error against a live holder")
	}
}

func TestLock_StealsDeadHolder(t *testing.T) {
	dir := t.TempDir()
	// A pid essentially guaranteed not to be alive: PID 1 belongs to init
	// inside most containers/hosts, so pick something implausibly large
	// instead, which is not alive and not reused this fast.
	deadPID := 1 << 30
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(strconv.Itoa(deadPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := AcquireLock(dir, false)
	if err != nil {
		t.Fatalf("AcquireLock over a dead holder: %v", err)
	}
	defer l.Release()
	pid, _, err := LockHolder(dir)
	if err != nil {
		t.Fatalf("LockHolder: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("lock holder after steal = %d, want our pid %d", pid, os.Getpid())
	}
}

func TestLock_ForceSteals(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A live holder, but --force steals it anyway.
	l, err := AcquireLock(dir, true)
	if err != nil {
		t.Fatalf("AcquireLock(force=true): %v", err)
	}
	defer l.Release()
	pid, alive, err := LockHolder(dir)
	if err != nil {
		t.Fatalf("LockHolder: %v", err)
	}
	if pid != os.Getpid() || !alive {
		t.Errorf("LockHolder after force = %d/%v", pid, alive)
	}
}

func TestLockHolder_NoLockFile(t *testing.T) {
	dir := t.TempDir()
	_, _, err := LockHolder(dir)
	if err == nil || !os.IsNotExist(err) {
		t.Errorf("LockHolder with no lock file: err = %v, want os.IsNotExist", err)
	}
}

// TestLock_ReleaseAfterForceSteal_DoesNotDeleteTheThiefsLock is I1: Release
// must not unlink the lock file by path alone, or verify liveness/identity
// only in Acquire — it must re-check ownership at Release time too. A single
// test process can only ever hold its own pid, so a real second holder is
// simulated by overwriting the lock file's content with a different pid
// directly, the way a genuinely different process's AcquireLock would have
// left it after stealing the lock.
func TestLock_ReleaseAfterForceSteal_DoesNotDeleteTheThiefsLock(t *testing.T) {
	dir := t.TempDir()
	victim, err := AcquireLock(dir, false)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}

	otherPID := os.Getpid() + 1
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(strconv.Itoa(otherPID)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := victim.Release(); err != nil {
		t.Fatalf("victim.Release: %v", err)
	}
	pid, _, err := LockHolder(dir)
	if err != nil {
		t.Fatalf("LockHolder after victim's Release: %v", err)
	}
	if pid != otherPID {
		t.Fatalf("victim's Release altered another holder's lock: pid = %d, want %d", pid, otherPID)
	}
}

// TestLockHolder_CorruptContentIsADeadHolder is I2: a 0-byte or otherwise
// unparseable lock file (e.g. left by a crash between the O_EXCL create and
// the pid write) must not make the run permanently unlockable — it is
// treated as a dead holder, recoverable without --force.
func TestLockHolder_CorruptContentIsADeadHolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	pid, alive, err := LockHolder(dir)
	if err != nil {
		t.Fatalf("LockHolder on corrupt content: %v", err)
	}
	if alive {
		t.Errorf("LockHolder on corrupt content: alive = true, want false (dead holder)")
	}
	if pid != 0 {
		t.Errorf("LockHolder on corrupt content: pid = %d, want 0", pid)
	}

	l, err := AcquireLock(dir, false)
	if err != nil {
		t.Fatalf("AcquireLock(force=false) over a corrupt lock: %v", err)
	}
	defer l.Release()
}

// TestPidAlive_EPERMIsAlive is I3: signal(0) returning EPERM means the
// process exists but is owned by another uid (common under sudo or in
// containers) — that must read as alive, not dead, or AcquireLock would
// steal a lock out from under a live holder it merely lacks permission to
// signal. PID 1 reliably reproduces EPERM for an unprivileged test process
// in this environment.
func TestPidAlive_EPERMIsAlive(t *testing.T) {
	proc, err := os.FindProcess(1)
	if err != nil {
		t.Skipf("os.FindProcess(1): %v", err)
	}
	sigErr := proc.Signal(syscall.Signal(0))
	if !errors.Is(sigErr, syscall.EPERM) {
		t.Skipf("signalling pid 1 did not return EPERM in this environment (got %v) — cannot exercise this case here", sigErr)
	}
	if !pidAlive(1) {
		t.Error("pidAlive(1) = false, want true: EPERM means alive, not dead")
	}
}
