package engine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/dcferreira/agent-pawl/internal/emit"
	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// waitDelay bounds how long execShell waits for a command's stdout/stderr
// pipes to close after the process has been signalled to die, so a
// surviving grandchild holding a pipe open cannot block Wait indefinitely
// (finding C2).
const waitDelay = 2 * time.Second

// execShell runs cmdline under "sh -c" with cwd at the working-copy root
// (never the session's raw cwd — DESIGN.md §5) and every key in keys
// exported as PAWL_<KEY> (design/format-spec.md §B.2), subject to the one
// engine-wide wall-clock ceiling. It returns errTimeout, wrapped, if the
// ceiling is hit; that is not a normal execution error and callers handle it
// specially.
//
// The command runs in its own process group (Setpgid). Round 2's C2 finding
// is why this does not use exec.CommandContext/cmd.Cancel to enforce the
// ceiling: cmd.Cancel only fires while "sh" itself is still the running
// process, but `cmd &` makes "sh" exit immediately after backgrounding its
// child, which stays alive in the same process group holding the stdout
// pipe open — by the time the ceiling would fire, there is nothing left for
// Cancel to cancel, and Wait blocks until WaitDelay gives up (which stops
// the *wait*, not the orphan). Instead, an independent timer fires at the
// ceiling regardless of whether "sh" has already exited, and kills the
// negative pgid directly — reaching the grandchild even when the direct
// child is long gone. WaitDelay remains as a second line of defence in case
// the kill itself doesn't reach every descendant.
func (e *Engine) execShell(cmdline string, keys []string, vals render.Values) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.Command("sh", "-c", cmdline)
	cmd.Dir = e.Root
	cmd.Env = append(os.Environ(), render.EnvFor(vals, keys)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = waitDelay

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	if startErr := cmd.Start(); startErr != nil {
		return "", "", -1, fmt.Errorf("engine: starting command: %w", startErr)
	}

	pgid := cmd.Process.Pid
	var timedOut atomic.Bool
	timer := time.AfterFunc(e.Timeout, func() {
		timedOut.Store(true)
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	})
	runErr := cmd.Wait()
	timer.Stop()
	stdout, stderr = outBuf.String(), errBuf.String()

	if timedOut.Load() {
		return stdout, stderr, -1, fmt.Errorf("%w: %q", errTimeout, cmdline)
	}
	if runErr == nil {
		return stdout, stderr, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return stdout, stderr, exitErr.ExitCode(), nil
	}
	return stdout, stderr, -1, fmt.Errorf("engine: running command: %w", runErr)
}

// execDeterministic renders and runs step's run:, then hands the captured
// stdout and exit code to internal/emit. timedOut is true when the
// wall-clock ceiling was hit, in which case the caller treats it as a
// "failure" outcome (design/format-spec.md §3) without inspecting result.
// stdout and stderr are always returned in full (design/format-spec.md §3:
// "captured in full"), even on error, so the caller can persist them for
// diagnosis (finding I2) regardless of how the step ended.
func (e *Engine) execDeterministic(step *spec.Step, vals render.Values) (result emit.Result, timedOut bool, stdout, stderr string, exitCode int, err error) {
	cmd, err := resolveScriptPathTemplate(step.Run, vals, filepath.Dir(e.Workflow.Path))
	if err != nil {
		return emit.Result{}, false, "", "", 0, fmt.Errorf("engine: rendering run: for step %q: %w", step.ID, err)
	}
	stdout, stderr, exitCode, err = e.execShell(cmd, render.Keys(step.Run), vals)
	if err != nil {
		if errors.Is(err, errTimeout) {
			return emit.Result{}, true, stdout, stderr, exitCode, nil
		}
		return emit.Result{}, false, stdout, stderr, exitCode, fmt.Errorf("engine: executing step %q: %w", step.ID, err)
	}
	result, err = emit.Parse(stdout, exitCode, step, e.Workflow.State)
	if err != nil {
		return emit.Result{}, false, stdout, stderr, exitCode, err
	}
	return result, false, stdout, stderr, exitCode, nil
}
