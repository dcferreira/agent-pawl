package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/render"
)

func setKillGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := killGrace
	killGrace = d
	t.Cleanup(func() { killGrace = old })
}

func newExecEngine(timeout time.Duration) *Engine {
	return &Engine{Root: os.TempDir(), Timeout: timeout}
}

// TestExecShell_TermHandledGracefully: at the ceiling the group gets SIGTERM
// first; a command that handles it runs its handler and execShell returns
// promptly, without waiting out the full grace period.
func TestExecShell_TermHandledGracefully(t *testing.T) {
	setKillGrace(t, 3*time.Second)
	dir := t.TempDir()
	cmd := `trap 'touch "` + dir + `/done"; exit 0' TERM; while :; do sleep 0.05; done`
	start := time.Now()
	_, _, _, err := newExecEngine(200*time.Millisecond).execShell(cmd, nil, render.Values{})
	elapsed := time.Since(start)
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want errTimeout", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "done")); statErr != nil {
		t.Errorf("TERM handler did not run: %v", statErr)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, want well under the 3s grace", elapsed)
	}
}

// TestExecShell_StubbornGrandchildKilledAfterGrace: sh dies on TERM but a
// backgrounded grandchild ignores it and holds the stdout pipe, so Wait
// returns after waitDelay (2s) -- before TERM+grace if grace is longer. A
// naive implementation that stops the escalation timer once Wait returns
// would never SIGKILL the grandchild. Here grace (3s) > waitDelay (2s) is
// exactly that case.
func TestExecShell_StubbornGrandchildKilledAfterGrace(t *testing.T) {
	setKillGrace(t, 3*time.Second)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "gc.pid")
	cmd := `sh -c 'trap "" TERM; echo $$ > "` + pidFile + `"; while :; do sleep 0.05; done' & wait`
	_, _, _, err := newExecEngine(300*time.Millisecond).execShell(cmd, nil, render.Values{})
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want errTimeout", err)
	}
	b, rerr := os.ReadFile(pidFile)
	if rerr != nil {
		t.Fatalf("reading gc.pid: %v", rerr)
	}
	pid, perr := strconv.Atoi(strings.TrimSpace(string(b)))
	if perr != nil {
		t.Fatalf("parsing pid: %v", perr)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still alive after execShell returned", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestExecShell_ShIgnoresTermStillKilled: even sh itself ignoring TERM
// cannot hang execShell; SIGKILL follows at roughly Timeout + killGrace.
func TestExecShell_ShIgnoresTermStillKilled(t *testing.T) {
	setKillGrace(t, 500*time.Millisecond)
	start := time.Now()
	_, _, _, err := newExecEngine(200*time.Millisecond).execShell(`trap "" TERM; while :; do sleep 0.05; done`, nil, render.Values{})
	elapsed := time.Since(start)
	if !errors.Is(err, errTimeout) {
		t.Fatalf("err = %v, want errTimeout", err)
	}
	if elapsed < 600*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Errorf("took %v, want about timeout+grace (700ms)", elapsed)
	}
}
