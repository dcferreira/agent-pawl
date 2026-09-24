# Enforcement Hooks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build DESIGN.md §5's PreToolUse/Stop enforcement layer: `pawl hook pre|stop`, the
`bin/pawl-hook` fast path, plugin `hooks/hooks.json`, and `pawl run`'s refuse-to-start heartbeat
check.

**Architecture:** Pure decision logic lives in a new `internal/hook` package (no I/O). Small
on-disk records (heartbeat, driver, live index) live in `internal/journal/enforce.go`. The CLI
glues them: `pawl hook` reads the Claude Code payload from stdin, loads live runs via
`journal.Live`, decides, and maps the decision to exit codes. `pawl run` gates on a fresh
heartbeat written by the PreToolUse hook that fired on the `pawl run` command itself.

**Tech Stack:** Go (stdlib only: `encoding/json`, `os`, `path/filepath`, `time`, `strings`),
POSIX sh for `bin/pawl-hook`, Claude Code plugin `hooks/hooks.json`.

**Spec:** `docs/superpowers/specs/2026-09-24-enforcement-hooks-design.md`

**Base:** this work is stacked on change `xowzttwq` (PR #12, `internal/guard`). Do not modify
files that change belongs to except where a task says so (`internal/cli/format.go` banner).

## Global Constraints

- Build output is `dist/pawl`, never `bin/` — `bin/` holds committed wrapper scripts.
- CI gates: `make fmt-check`, `make vet`, `make staticcheck`, `go mod tidy` no diff, `claude plugin validate . --strict`, `make test`, `make test-race`. No new Go module dependencies.
- Heartbeat freshness window: **10 s**.
- Refusal exit code for a missing heartbeat: **4** (docs/cli.md's refusal class).
- Opt-out: `--no-enforcement` flag on `pawl run`, or env `PAWL_ENFORCEMENT=off`.
- Refusal message, verbatim:
  `pawl: refusing to start: pawl's PreToolUse hook has not fired in this session.` /
  `Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.`
- Hook protocol: allow = exit 0, no output; deny/block = exit 2 with a one-line reason on stderr;
  fail-open = exit 1 with a message on stderr (non-blocking in Claude Code).
- Stop reason, verbatim shape:
  ``pawl run <id> (<workflow>) is at step <step> awaiting `pawl submit`. Finish it, or: pawl abandon --run <id>``
- Paths: `pawlHome = filepath.Dir(journal.StateBase())`; `live/` and `heartbeat/` under it;
  `driver.json` in the run directory. Guards and step kinds are read from `plan.json`.
- Every user-visible string that includes workflow-controlled content (step ids, workflow ids,
  guard ids) goes through `printLine`/`blockWriter.line`, never a bare `Fprintf` (see
  `internal/cli/format.go`'s doc comments).
- Conventional Commits; trailers `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`.

## Review Focus

1. Hook payload `cwd` is a **subdirectory** (or symlinked path) of the working copy → must
   resolve to the same run namespace as `pawl run` did (covered: Task 4 test `TestHookPre_SubdirCwd`).
2. `pawl abandon`/`pawl status` must **never** be denied, even when a live run's plan is broken
   (fail-closed) — otherwise the documented escape hatch is itself blocked (Task 3 test
   `TestDecidePre_PawlCommandsExemptFromFailClosed`).
3. Subagent VCS commands in **chained/prefixed** form — `cd x && git commit`, `FOO=1 git push`,
   `git -C d commit`, `jj -R . new` — denied; read-only lists `git status && git log`,
   `git branch`, `git stash list`, `jj log` allowed (Task 2 table).
4. A run started with `--no-enforcement` has no driver → Stop never blocks; a later `pawl submit`
   from a hooked session stamps the driver and Stop then applies (Task 5 test
   `TestSubmit_StampsDriverFromFreshHeartbeat`).
5. A **stale `live/` symlink** (run directory deleted by hand, or run terminal) → hook allows, and
   the next pawl command's `SyncLiveIndex` removes it (Task 1 test `TestSyncLiveIndex_RemovesStale`).

---

### Task 1: Journal records — pawl home, heartbeat, driver, live index

**Files:**
- Create: `internal/journal/enforce.go`
- Test: `internal/journal/enforce_test.go`

**Interfaces:**
- Consumes: `journal.StateBase()`, `journal.Slug(root)`, `journal.Live(root)`, `journal.RunDir`.
- Produces:
  ```go
  const HeartbeatTTL = 10 * time.Second
  func PawlHome() string                           // filepath.Dir(StateBase())
  func LiveIndexDir() string                       // PawlHome()/live
  type Heartbeat struct { SessionID string `json:"session_id"`; Time time.Time `json:"time"` }
  func (h Heartbeat) Fresh(now time.Time) bool     // now.Sub(h.Time) in [0, HeartbeatTTL]
  func WriteHeartbeat(root, sessionID string, now time.Time) error
  func ReadHeartbeat(root string) (Heartbeat, bool, error) // ok=false when absent
  type Driver struct { SessionID string `json:"session_id"`; Updated time.Time `json:"updated"` }
  func WriteDriver(runDir, sessionID string, now time.Time) error
  func ReadDriver(runDir string) (Driver, bool, error)     // ok=false when absent
  func SyncLiveIndex(root string) error
  ```

- [ ] **Step 1: Write the failing tests**

```go
package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withStateDir(t *testing.T) string {
	t.Helper()
	base := filepath.Join(t.TempDir(), "runs")
	t.Setenv(EnvStateDir, base)
	return base
}

func TestPawlHome_IsParentOfStateBase(t *testing.T) {
	base := withStateDir(t)
	if got, want := PawlHome(), filepath.Dir(base); got != want {
		t.Fatalf("PawlHome() = %q, want %q", got, want)
	}
	if got, want := LiveIndexDir(), filepath.Join(filepath.Dir(base), "live"); got != want {
		t.Fatalf("LiveIndexDir() = %q, want %q", got, want)
	}
}

func TestHeartbeat_RoundTripAndFreshness(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if _, ok, err := ReadHeartbeat(root); err != nil || ok {
		t.Fatalf("absent heartbeat: ok=%v err=%v", ok, err)
	}
	if err := WriteHeartbeat(root, "sess-1", now); err != nil {
		t.Fatal(err)
	}
	hb, ok, err := ReadHeartbeat(root)
	if err != nil || !ok || hb.SessionID != "sess-1" || !hb.Time.Equal(now) {
		t.Fatalf("got %+v ok=%v err=%v", hb, ok, err)
	}
	if !hb.Fresh(now.Add(10 * time.Second)) {
		t.Fatal("10s-old heartbeat should be fresh")
	}
	if hb.Fresh(now.Add(10*time.Second + time.Nanosecond)) {
		t.Fatal(">10s-old heartbeat should be stale")
	}
	if hb.Fresh(now.Add(-time.Second)) {
		t.Fatal("heartbeat from the future should not count as fresh")
	}
}

func TestHeartbeat_SeparatePerWorkingCopy(t *testing.T) {
	withStateDir(t)
	a, b := t.TempDir(), t.TempDir()
	now := time.Now()
	if err := WriteHeartbeat(a, "sess-a", now); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ReadHeartbeat(b); ok {
		t.Fatal("heartbeat for a leaked into b")
	}
}

func TestDriver_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := ReadDriver(dir); err != nil || ok {
		t.Fatalf("absent driver: ok=%v err=%v", ok, err)
	}
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if err := WriteDriver(dir, "sess-9", now); err != nil {
		t.Fatal(err)
	}
	d, ok, err := ReadDriver(dir)
	if err != nil || !ok || d.SessionID != "sess-9" || !d.Updated.Equal(now) {
		t.Fatalf("got %+v ok=%v err=%v", d, ok, err)
	}
}

// startRun makes a minimal live run directory for root (one RUN_START event),
// using the package's own Log so the fixture matches real runs.
func startRun(t *testing.T, root, workflowID, runID string) string {
	t.Helper()
	dir, err := CreateRunDir(root, workflowID, runID)
	if err != nil {
		t.Fatal(err)
	}
	lg, err := OpenLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if err := lg.Append(&Event{RunID: runID, Kind: KindRunStart}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSyncLiveIndex_CreatesLinkForLiveRun(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	dir := startRun(t, root, "wf", "ab12")
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(LiveIndexDir(), Slug(root)+"__wf__ab12")
	target, err := os.Readlink(link)
	if err != nil || target != dir {
		t.Fatalf("link %s -> %q (err %v), want %q", link, target, err, dir)
	}
}

func TestSyncLiveIndex_RemovesStale(t *testing.T) {
	withStateDir(t)
	root := t.TempDir()
	dir := startRun(t, root, "wf", "ab12")
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := SyncLiveIndex(root); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(LiveIndexDir())
	if len(entries) != 0 {
		t.Fatalf("stale link survived: %v", entries)
	}
}

func TestSyncLiveIndex_LeavesOtherWorkingCopiesAlone(t *testing.T) {
	withStateDir(t)
	a, b := t.TempDir(), t.TempDir()
	startRun(t, a, "wf", "aaaa")
	if err := SyncLiveIndex(a); err != nil {
		t.Fatal(err)
	}
	if err := SyncLiveIndex(b); err != nil { // b has no runs
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(LiveIndexDir(), Slug(a)+"__wf__aaaa")); err != nil {
		t.Fatalf("syncing b removed a's link: %v", err)
	}
}
```

Before writing `startRun`, check `internal/journal/journal.go` and `log.go` for the exact names of
`KindRunStart`, `Event` fields, and `Log.Close`/`Append`; adjust the fixture to match (e.g. copy
how `live_test.go` builds a run directory, and reuse its helper if one exists). A terminal-run case
is covered by the "RemovesStale" path plus `journal.Live` already filtering terminals; add a
terminal fixture too if `live_test.go` has a helper that appends `RUN_END`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/journal/ -run 'PawlHome|Heartbeat|Driver|SyncLiveIndex' -v`
Expected: FAIL — `undefined: PawlHome` etc.

- [ ] **Step 3: Implement `internal/journal/enforce.go`**

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/journal/ -v -run 'PawlHome|Heartbeat|Driver|SyncLiveIndex' && go test -race ./internal/journal/`
Expected: PASS

- [ ] **Step 5: Commit** — `feat: add heartbeat, driver and live-index records to internal/journal`

---

### Task 2: `internal/hook` — payload parsing and command classifiers

**Files:**
- Create: `internal/hook/payload.go`, `internal/hook/classify.go`
- Test: `internal/hook/payload_test.go`, `internal/hook/classify_test.go`

**Interfaces:**
- Produces:
  ```go
  type Payload struct {
      SessionID      string
      Cwd            string
      HookEventName  string
      ToolName       string
      Command        string // tool_input.command; "" for non-Bash or Stop
      AgentID        string // "" for the main session
      StopHookActive bool
  }
  func ParsePayload(data []byte) (Payload, error)
  func IsPawlCommand(cmd string) bool   // first token of any segment has basename "pawl"
  func IsVCSMutation(cmd string) bool   // any segment is a mutating git/jj command
  ```

- [ ] **Step 1: Write the failing tests**

`payload_test.go`:

```go
package hook

import "testing"

func TestParsePayload_PreToolUseSubagent(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"PreToolUse",
		"tool_name":"Bash","tool_input":{"command":"git push"},"agent_id":"a7","agent_type":"general-purpose"}`))
	if err != nil {
		t.Fatal(err)
	}
	want := Payload{SessionID: "s1", Cwd: "/w", HookEventName: "PreToolUse", ToolName: "Bash", Command: "git push", AgentID: "a7"}
	if p != want {
		t.Fatalf("got %+v, want %+v", p, want)
	}
}

func TestParsePayload_Stop(t *testing.T) {
	p, err := ParsePayload([]byte(`{"session_id":"s1","cwd":"/w","hook_event_name":"Stop","stop_hook_active":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.StopHookActive || p.AgentID != "" || p.Command != "" {
		t.Fatalf("got %+v", p)
	}
}

func TestParsePayload_Garbage(t *testing.T) {
	if _, err := ParsePayload([]byte("not json")); err == nil {
		t.Fatal("want error")
	}
}
```

`classify_test.go`:

```go
package hook

import "testing"

func TestIsPawlCommand(t *testing.T) {
	cases := map[string]bool{
		"pawl run green-tests":                 true,
		"  pawl submit --run ab12 --step x":    true,
		"./dist/pawl run x":                    true,
		"/home/u/go/bin/pawl status":           true,
		"cd sub && pawl run x":                 true,
		"PAWL_ENFORCEMENT=off pawl run x":      true,
		"echo pawl":                            false,
		"pawlish run":                          false,
		"grep -r pawl .":                       false,
		"":                                     false,
	}
	for cmd, want := range cases {
		if got := IsPawlCommand(cmd); got != want {
			t.Errorf("IsPawlCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestIsVCSMutation(t *testing.T) {
	cases := map[string]bool{
		// git mutating
		"git commit -m x":             true,
		"git push origin main":        true,
		"git -C sub commit -am x":     true,
		"git -c user.name=x commit":   true,
		"FOO=1 git push":              true,
		"cd x && git commit -m y":     true,
		"git status; git add .":       true,
		"git branch -D old":           true,
		"git branch new-branch":       true,
		"git tag v1":                  true,
		"git stash":                   true,
		"git stash pop":               true,
		"git checkout main":           true,
		"/usr/bin/git reset --hard":   true,
		// git read-only
		"git status":                  false,
		"git log --oneline":           false,
		"git diff HEAD":               false,
		"git status && git log":       false,
		"git branch":                  false,
		"git branch -a":               false,
		"git branch --list":           false,
		"git tag":                     false,
		"git tag -l":                  false,
		"git stash list":              false,
		"git stash show":              false,
		"git commit --help":           false,
		"git show HEAD":               false,
		"git rev-parse HEAD":          false,
		// jj mutating (default-deny)
		"jj new":                      true,
		"jj describe -m x":            true,
		"jj -R . squash":              true,
		"jj --repository . abandon":   true,
		"jj git push":                 true,
		"jj git fetch":                true,
		"jj bookmark set main":        true,
		"jj op restore abc":           true,
		"jj workspace add ../x":       true,
		"jj file track x":             true,
		// jj read-only
		"jj":                          false,
		"jj log":                      false,
		"jj st":                       false,
		"jj status":                   false,
		"jj diff -r @-":               false,
		"jj show":                     false,
		"jj evolog":                   false,
		"jj root":                     false,
		"jj file show -r @ x":         false,
		"jj file list":                false,
		"jj op log":                   false,
		"jj workspace root":           false,
		"jj workspace list":           false,
		"jj bookmark list":            false,
		"jj git remote list":          false,
		"jj config get user.name":     false,
		"jj new --help":               false,
		"jj help new":                 false,
		// neither
		"go test ./...":               false,
		"echo git commit":             false,
		"":                            false,
	}
	for cmd, want := range cases {
		if got := IsVCSMutation(cmd); got != want {
			t.Errorf("IsVCSMutation(%q) = %v, want %v", cmd, got, want)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hook/ -v`
Expected: FAIL — package has no non-test files / undefined symbols.

- [ ] **Step 3: Implement**

`payload.go`:

```go
// Package hook decides what pawl's Claude Code hooks (PreToolUse, Stop)
// allow. Its decision functions are pure: the cli layer loads live runs off
// disk and maps a Decision to the hook exit-code protocol.
package hook

import (
	"encoding/json"
	"fmt"
)

// Payload is the subset of a Claude Code hook's stdin JSON pawl reads.
// agent_id is present only when the hook fires inside a subagent.
type Payload struct {
	SessionID      string
	Cwd            string
	HookEventName  string
	ToolName       string
	Command        string
	AgentID        string
	StopHookActive bool
}

type rawPayload struct {
	SessionID      string `json:"session_id"`
	Cwd            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	ToolName       string `json:"tool_name"`
	ToolInput      struct {
		Command string `json:"command"`
	} `json:"tool_input"`
	AgentID        string `json:"agent_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

func ParsePayload(data []byte) (Payload, error) {
	var r rawPayload
	if err := json.Unmarshal(data, &r); err != nil {
		return Payload{}, fmt.Errorf("hook: parsing payload: %w", err)
	}
	return Payload{
		SessionID: r.SessionID, Cwd: r.Cwd, HookEventName: r.HookEventName,
		ToolName: r.ToolName, Command: r.ToolInput.Command, AgentID: r.AgentID,
		StopHookActive: r.StopHookActive,
	}, nil
}
```

`classify.go`:

```go
package hook

import (
	"path/filepath"
	"strings"
)

// segments splits a shell command on the control operators that start a new
// simple command. It does not understand quoting: the classification is
// advisory string matching, like guards (DESIGN.md §5).
func segments(cmd string) [][]string {
	r := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n", "&", "\n")
	var out [][]string
	for _, seg := range strings.Split(r.Replace(cmd), "\n") {
		f := strings.Fields(seg)
		for len(f) > 0 && isAssignment(f[0]) {
			f = f[1:]
		}
		if len(f) > 0 {
			out = append(out, f)
		}
	}
	return out
}

func isAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	return i > 0 && !strings.ContainsAny(tok[:i], "/-.")
}

func IsPawlCommand(cmd string) bool {
	for _, f := range segments(cmd) {
		if filepath.Base(f[0]) == "pawl" {
			return true
		}
	}
	return false
}

func IsVCSMutation(cmd string) bool {
	for _, f := range segments(cmd) {
		switch filepath.Base(f[0]) {
		case "git":
			if gitMutates(f[1:]) {
				return true
			}
		case "jj":
			if jjMutates(f[1:]) {
				return true
			}
		}
	}
	return false
}

func hasHelp(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" {
			return true
		}
	}
	return false
}

// stripGlobal drops leading global options, including the value of any
// option in withValue given as a separate token.
func stripGlobal(args []string, withValue map[string]bool) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if withValue[args[0]] && len(args) > 1 {
			args = args[2:]
			continue
		}
		args = args[1:]
	}
	return args
}

var gitValueOpts = map[string]bool{"-C": true, "-c": true, "--git-dir": true, "--work-tree": true, "--namespace": true}

var gitMutating = map[string]bool{
	"commit": true, "push": true, "pull": true, "reset": true, "checkout": true, "switch": true,
	"restore": true, "merge": true, "rebase": true, "cherry-pick": true, "revert": true,
	"tag": true, "branch": true, "stash": true, "am": true, "apply": true, "add": true,
	"rm": true, "mv": true, "clean": true,
}

func gitMutates(args []string) bool {
	if hasHelp(args) {
		return false
	}
	args = stripGlobal(args, gitValueOpts)
	if len(args) == 0 || !gitMutating[args[0]] {
		return false
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "branch", "tag":
		// Listing forms: no positional argument, only list-ish flags.
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") {
				return true
			}
			switch a {
			case "-l", "--list", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "-n":
			default:
				return true
			}
		}
		return false
	case "stash":
		return !(len(rest) > 0 && (rest[0] == "list" || rest[0] == "show"))
	}
	return true
}

var jjValueOpts = map[string]bool{
	"-R": true, "--repository": true, "--at-op": true, "--at-operation": true,
	"--color": true, "--config": true, "--config-toml": true, "--config-file": true,
}

var jjReadOnly = map[string]bool{
	"log": true, "st": true, "status": true, "diff": true, "show": true, "evolog": true,
	"obslog": true, "root": true, "help": true, "interdiff": true, "version": true,
}

var jjReadOnlySub = map[string]map[string]bool{
	"file":      {"show": true, "list": true, "annotate": true, "search": true},
	"op":        {"log": true, "show": true, "diff": true},
	"operation": {"log": true, "show": true, "diff": true},
	"workspace": {"root": true, "list": true},
	"bookmark":  {"list": true, "l": true},
	"b":         {"list": true, "l": true},
	"config":    {"get": true, "list": true, "path": true},
}

func jjMutates(args []string) bool {
	if hasHelp(args) {
		return false
	}
	args = stripGlobal(args, jjValueOpts)
	if len(args) == 0 || jjReadOnly[args[0]] {
		return false
	}
	if subs, ok := jjReadOnlySub[args[0]]; ok {
		return !(len(args) > 1 && subs[args[1]])
	}
	if args[0] == "git" && len(args) > 2 && args[1] == "remote" && args[2] == "list" {
		return false
	}
	return true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/hook/ -v`
Expected: PASS. If a table row fails, fix the classifier, not the table — the table is the spec.

- [ ] **Step 5: Commit** — `feat: add internal/hook payload parsing and VCS/pawl command classifiers`

---

### Task 3: `internal/hook` — `DecidePre` / `DecideStop`

**Files:**
- Create: `internal/hook/decide.go`
- Test: `internal/hook/decide_test.go`

**Interfaces:**
- Consumes: Task 2's `Payload`, `IsPawlCommand`, `IsVCSMutation`; `guard.Compile`, `(*guard.Table).Denied`
  from `internal/guard` (PR #12); `spec.GuardDecl`.
- Produces:
  ```go
  type LiveRun struct {
      RunID, WorkflowID string
      Blocked           bool
      CursorStep        string
      CursorKind        string       // spec step kind of CursorStep: "agentic", "parallel", "wait", ...
      ActiveSteps       []string     // nil when Blocked
      Guards            *guard.Table // nil when the workflow has no guards
      GuardsErr         error        // non-nil → fail closed for this run
      DriverSession     string       // "" when no driver.json
  }
  type Decision struct { Allow bool; Reason string }
  func DecidePre(runs []LiveRun, p Payload) Decision
  func DecideStop(runs []LiveRun, p Payload) Decision
  ```

- [ ] **Step 1: Write the failing tests**

```go
package hook

import (
	"errors"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/guard"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

func table(t *testing.T, gs ...spec.GuardDecl) *guard.Table {
	t.Helper()
	tb, err := guard.Compile(gs)
	if err != nil {
		t.Fatal(err)
	}
	return tb
}

var pushOnlyInCommit = spec.GuardDecl{ID: "push-in-commit", Match: `git push`, OnlyIn: []string{"commit"}}

func bash(cmd string) Payload {
	return Payload{SessionID: "s1", Cwd: "/w", HookEventName: "PreToolUse", ToolName: "Bash", Command: cmd}
}

func TestDecidePre_NoRunsAllows(t *testing.T) {
	if d := DecidePre(nil, bash("git push")); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_GuardDeniesOutsideOnlyIn(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", WorkflowID: "wf", CursorStep: "fix", CursorKind: "agentic",
		ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)}}
	d := DecidePre(runs, bash("git push origin main"))
	if d.Allow || !strings.Contains(d.Reason, "push-in-commit") || !strings.Contains(d.Reason, "ab12") {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_GuardAllowsInOnlyIn(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", CursorStep: "commit", CursorKind: "deterministic",
		ActiveSteps: []string{"commit"}, Guards: table(t, pushOnlyInCommit)}}
	if d := DecidePre(runs, bash("git push")); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_UnionOnePermitsWins(t *testing.T) {
	runs := []LiveRun{
		{RunID: "aaaa", ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)},
		{RunID: "bbbb", ActiveSteps: []string{"commit"}, Guards: table(t, pushOnlyInCommit)},
	}
	if d := DecidePre(runs, bash("git push")); !d.Allow {
		t.Fatalf("union: one run permits, want allow; got %+v", d)
	}
}

func TestDecidePre_UnionNeutralRunDoesNotPermit(t *testing.T) {
	runs := []LiveRun{
		{RunID: "aaaa", ActiveSteps: []string{"fix"}, Guards: table(t, pushOnlyInCommit)},
		{RunID: "bbbb", ActiveSteps: []string{"x"}}, // no guards: neutral
	}
	if d := DecidePre(runs, bash("git push")); d.Allow {
		t.Fatal("a run with no matching guard must not permit")
	}
}

func TestDecidePre_BlockedRunDeniesEverywhere(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", Blocked: true, CursorStep: "commit", Guards: table(t, pushOnlyInCommit)}}
	if d := DecidePre(runs, bash("git push")); d.Allow {
		t.Fatal("BLOCKED run keeps denying guarded commands")
	}
}

func TestDecidePre_FailClosedOnBrokenGuards(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	d := DecidePre(runs, bash("ls"))
	if d.Allow || !strings.Contains(d.Reason, "ab12") || !strings.Contains(d.Reason, "bad regexp") {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_PawlCommandsExemptFromFailClosed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", GuardsErr: errors.New("bad regexp")}}
	for _, cmd := range []string{"pawl abandon --run ab12", "pawl status"} {
		if d := DecidePre(runs, bash(cmd)); !d.Allow {
			t.Fatalf("%q must stay allowed; got %+v", cmd, d)
		}
	}
}

func TestDecidePre_SubagentVCSDenied(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", WorkflowID: "wf", CursorStep: "fix", CursorKind: "agentic", ActiveSteps: []string{"fix"}}}
	p := bash("git commit -am x")
	p.AgentID = "agent-1"
	d := DecidePre(runs, p)
	if d.Allow || !strings.Contains(d.Reason, "subagent") {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_MainSessionVCSAllowed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", ActiveSteps: []string{"fix"}}}
	if d := DecidePre(runs, bash("git commit -am x")); !d.Allow {
		t.Fatalf("main session is not subject to the subagent VCS rule; got %+v", d)
	}
}

func TestDecidePre_SubagentReadOnlyVCSAllowed(t *testing.T) {
	runs := []LiveRun{{RunID: "ab12", ActiveSteps: []string{"fix"}}}
	p := bash("git status && jj log")
	p.AgentID = "agent-1"
	if d := DecidePre(runs, p); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func TestDecidePre_SubagentVCSAllowedWithNoLiveRun(t *testing.T) {
	p := bash("git commit -am x")
	p.AgentID = "agent-1"
	if d := DecidePre(nil, p); !d.Allow {
		t.Fatalf("got %+v", d)
	}
}

func stop(session string, active bool) Payload {
	return Payload{SessionID: session, Cwd: "/w", HookEventName: "Stop", StopHookActive: active}
}

func TestDecideStop(t *testing.T) {
	agentic := LiveRun{RunID: "ab12", WorkflowID: "green-tests", CursorStep: "fix_code", CursorKind: "agentic", DriverSession: "s1"}
	cases := []struct {
		name  string
		runs  []LiveRun
		p     Payload
		allow bool
	}{
		{"no runs", nil, stop("s1", false), true},
		{"driver at agentic blocks", []LiveRun{agentic}, stop("s1", false), false},
		{"stop_hook_active allows", []LiveRun{agentic}, stop("s1", true), true},
		{"other session allows", []LiveRun{agentic}, stop("s2", false), true},
		{"no driver allows", []LiveRun{func() LiveRun { r := agentic; r.DriverSession = ""; return r }()}, stop("s1", false), true},
		{"blocked allows", []LiveRun{func() LiveRun { r := agentic; r.Blocked = true; return r }()}, stop("s1", false), true},
		{"parallel blocks", []LiveRun{func() LiveRun { r := agentic; r.CursorKind = "parallel"; return r }()}, stop("s1", false), false},
		{"wait allows", []LiveRun{func() LiveRun { r := agentic; r.CursorKind = "wait"; return r }()}, stop("s1", false), true},
		{"human allows", []LiveRun{func() LiveRun { r := agentic; r.CursorKind = "human"; return r }()}, stop("s1", false), true},
	}
	for _, c := range cases {
		d := DecideStop(c.runs, c.p)
		if d.Allow != c.allow {
			t.Errorf("%s: got %+v, want allow=%v", c.name, d, c.allow)
		}
	}
	d := DecideStop([]LiveRun{agentic}, stop("s1", false))
	want := "pawl run ab12 (green-tests) is at step fix_code awaiting `pawl submit`. Finish it, or: pawl abandon --run ab12"
	if d.Reason != want {
		t.Fatalf("reason = %q, want %q", d.Reason, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/hook/ -run 'Decide' -v`
Expected: FAIL — `undefined: LiveRun`, `DecidePre`, `DecideStop`.

- [ ] **Step 3: Implement `internal/hook/decide.go`**

```go
package hook

import (
	"fmt"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/guard"
)

// LiveRun is what the hook decisions need to know about one live run for
// the working copy the hook fired in.
type LiveRun struct {
	RunID, WorkflowID string
	Blocked           bool
	CursorStep        string
	CursorKind        string
	ActiveSteps       []string
	Guards            *guard.Table
	GuardsErr         error
	DriverSession     string
}

// Decision is a hook verdict; Reason is set whenever Allow is false.
type Decision struct {
	Allow  bool
	Reason string
}

var allow = Decision{Allow: true}

// DecidePre applies DESIGN.md §5's PreToolUse rules for a Bash call: the
// fail-closed rule for a run whose guards cannot load, the guard union
// across live runs, and the subagent VCS rule. pawl's own commands are
// exempt from fail-closed so `pawl abandon` always works.
func DecidePre(runs []LiveRun, p Payload) Decision {
	if len(runs) == 0 {
		return allow
	}
	cmd := p.Command
	if !IsPawlCommand(cmd) {
		for _, r := range runs {
			if r.GuardsErr != nil {
				return Decision{Reason: fmt.Sprintf("pawl: run %s's guards could not be loaded (%v); every command is denied until it is fixed or abandoned: pawl abandon --run %s", r.RunID, r.GuardsErr, r.RunID)}
			}
		}
	}
	if d := decideGuards(runs, cmd); !d.Allow {
		return d
	}
	if p.AgentID != "" && IsVCSMutation(cmd) {
		r := runs[0]
		return Decision{Reason: fmt.Sprintf("pawl: a subagent may not mutate VCS while run %s is live (step %s); leave commits to the workflow's own steps", r.RunID, r.CursorStep)}
	}
	return allow
}

func decideGuards(runs []LiveRun, cmd string) Decision {
	var denied *LiveRun
	var deniedID string
	for i := range runs {
		r := &runs[i]
		if r.Guards == nil {
			continue
		}
		if g := r.Guards.Denied(r.ActiveSteps, cmd); g != nil {
			if denied == nil {
				denied, deniedID = r, g.ID
			}
			continue
		}
		if r.Guards.Denied(nil, cmd) != nil {
			return allow // some guard matched and this run's active step permits it
		}
	}
	if denied == nil {
		return allow
	}
	where := "no step"
	if len(denied.ActiveSteps) > 0 {
		where = "step " + strings.Join(denied.ActiveSteps, ", ")
	}
	return Decision{Reason: fmt.Sprintf("pawl: denied by guard `%s` (run %s, %s)", deniedID, denied.RunID, where)}
}

// DecideStop refuses a Stop only for the session driving a run that is
// waiting on its `pawl submit`, and at most once per turn.
func DecideStop(runs []LiveRun, p Payload) Decision {
	if p.StopHookActive {
		return allow
	}
	for _, r := range runs {
		if r.Blocked || r.DriverSession == "" || r.DriverSession != p.SessionID {
			continue
		}
		if r.CursorKind != "agentic" && r.CursorKind != "parallel" {
			continue
		}
		return Decision{Reason: fmt.Sprintf("pawl run %s (%s) is at step %s awaiting `pawl submit`. Finish it, or: pawl abandon --run %s", r.RunID, r.WorkflowID, r.CursorStep, r.RunID)}
	}
	return allow
}
```

Note: the guard-deny reason should name what the guard allows. If PR #12's `spec.GuardDecl` is
returned by `Denied`, extend the message with `only_in` (e.g. `` `git push` may only run in step commit ``
or `never allowed in this run` for an empty `only_in`) — keep the run id and guard id in it (tests
assert both).

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/hook/ -v && go test -race ./internal/hook/`
Expected: PASS

- [ ] **Step 5: Commit** — `feat: add PreToolUse and Stop decision rules in internal/hook`

---

### Task 4: `pawl hook pre|stop` CLI command

**Files:**
- Create: `internal/cli/hook.go`
- Modify: `internal/cli/cli.go` (add `RunIO` with stdin; `hook` case; usage text), `cmd/pawl/main.go` (call `cli.RunIO(args, os.Stdin, …)`)
- Test: `internal/cli/hook_test.go`

**Interfaces:**
- Consumes: Task 1 (`WriteHeartbeat`, `ReadDriver`), Task 2/3 (`ParsePayload`, `IsPawlCommand`,
  `DecidePre`, `DecideStop`, `LiveRun`), `journal.ResolveRoot`, `journal.Live`, `journal.ReadPlan`,
  `guard.Compile`, `(*spec.Workflow).StepByID`.
- Produces:
  ```go
  func RunIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int // Run(args, out, err) == RunIO(args, strings.NewReader(""), out, err)
  var nowFunc = time.Now // package-level clock, overridden in tests
  func loadLiveRuns(root string) ([]hook.LiveRun, error)
  ```

- [ ] **Step 1: Write the failing tests**

Use the existing helpers (`setupWorkingCopy`, `writeWorkflow`) and start a real run via
`Run([]string{"pawl","run",...})` so fixtures match production. `setupWorkingCopy` will set
`PAWL_ENFORCEMENT=off` after Task 5; until then these tests start runs directly (they pre-date the
gate). Use a workflow whose first step is agentic so the run parks at `DISPATCH`:

```go
package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

const hookWF = `workflow: hookwf
steps:
  - id: fix
    kind: agentic
    prompt: "fix it"
    writes: {done: bool}
    postcondition: {all_set: [done]}
    next: done
terminal:
  done: {status: ok, message: "ok"}
guards:
  - id: push-in-commit
    match: "git push"
    only_in: []
`
```

(Before writing, open an existing agentic test workflow in `cli_test.go` and copy its exact
shape — field names above are indicative; `guards:` requires PR #12's validator.)

```go
func startHookRun(t *testing.T) (root, runID string) {
	t.Helper()
	root = setupWorkingCopy(t)
	t.Setenv("PAWL_ENFORCEMENT", "off")
	writeWorkflow(t, root, "hookwf", hookWF)
	var out, errb bytes.Buffer
	if code := Run([]string{"pawl", "run", "hookwf"}, &out, &errb); code != 0 {
		t.Fatalf("run: %d %s", code, errb.String())
	}
	live, err := journal.Live(root)
	if err != nil || len(live) != 1 {
		t.Fatalf("live: %v %v", live, err)
	}
	return root, live[0].RunID
}

func hookCall(t *testing.T, event string, payload string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := RunIO([]string{"pawl", "hook", event}, strings.NewReader(payload), &out, &errb)
	return code, out.String(), errb.String()
}

func prePayload(cwd, session, cmd, agent string) string {
	a := ""
	if agent != "" {
		a = fmt.Sprintf(`,"agent_id":%q`, agent)
	}
	return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":%q}%s}`, session, cwd, cmd, a)
}

func TestHookPre_NoRunAllowsSilently(t *testing.T) {
	root := setupWorkingCopy(t)
	code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "ls", ""))
	if code != 0 || out != "" || errs != "" {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestHookPre_WritesHeartbeatForPawlCommand(t *testing.T) {
	root := setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "pre", prePayload(root, "s1", "pawl run x", "")); code != 0 {
		t.Fatalf("code=%d", code)
	}
	hb, ok, err := journal.ReadHeartbeat(root)
	if err != nil || !ok || hb.SessionID != "s1" {
		t.Fatalf("hb=%+v ok=%v err=%v", hb, ok, err)
	}
}

func TestHookPre_NoHeartbeatForOtherCommands(t *testing.T) {
	root := setupWorkingCopy(t)
	hookCall(t, "pre", prePayload(root, "s1", "ls", ""))
	if _, ok, _ := journal.ReadHeartbeat(root); ok {
		t.Fatal("heartbeat written for a non-pawl command")
	}
}

func TestHookPre_GuardDenyExit2(t *testing.T) {
	root, runID := startHookRun(t)
	code, out, errs := hookCall(t, "pre", prePayload(root, "s1", "git push origin main", ""))
	if code != 2 || out != "" || !strings.Contains(errs, "push-in-commit") || !strings.Contains(errs, runID) {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
}

func TestHookPre_SubdirCwd(t *testing.T) {
	root, _ := startHookRun(t)
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil { // make root a working-copy root
		t.Fatal(err)
	}
	if code, _, _ := hookCall(t, "pre", prePayload(sub, "s1", "git push", "")); code != 2 {
		t.Fatalf("subdirectory cwd must resolve to the run's working copy; code=%d", code)
	}
}

func TestHookPre_SubagentVCSDeny(t *testing.T) {
	root, _ := startHookRun(t)
	if code, _, errs := hookCall(t, "pre", prePayload(root, "s1", "git commit -am x", "agent-1")); code != 2 || !strings.Contains(errs, "subagent") {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func TestHookPre_GarbagePayloadFailsOpen(t *testing.T) {
	setupWorkingCopy(t)
	code, _, errs := hookCall(t, "pre", "not json")
	if code != 1 || errs == "" {
		t.Fatalf("code=%d err=%q", code, errs)
	}
}

func stopPayload(cwd, session string, active bool) string {
	return fmt.Sprintf(`{"session_id":%q,"cwd":%q,"hook_event_name":"Stop","stop_hook_active":%v}`, session, cwd, active)
}

func TestHookStop_BlocksDriverAtAgentic(t *testing.T) {
	root, runID := startHookRun(t)
	dir := journal.RunDir(root, "hookwf", runID)
	if err := journal.WriteDriver(dir, "s1", nowFunc()); err != nil {
		t.Fatal(err)
	}
	code, _, errs := hookCall(t, "stop", stopPayload(root, "s1", false))
	if code != 2 || !strings.Contains(errs, "pawl abandon --run "+runID) {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if code, _, _ := hookCall(t, "stop", stopPayload(root, "s1", true)); code != 0 {
		t.Fatalf("stop_hook_active must allow; code=%d", code)
	}
	if code, _, _ := hookCall(t, "stop", stopPayload(root, "s2", false)); code != 0 {
		t.Fatalf("other session must be allowed; code=%d", code)
	}
}

func TestHookStop_GarbageAllows(t *testing.T) {
	setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "stop", "not json"); code != 0 {
		t.Fatalf("stop must never trap a session; code=%d", code)
	}
}

func TestHook_UnknownEventUsage(t *testing.T) {
	setupWorkingCopy(t)
	if code, _, _ := hookCall(t, "bogus", "{}"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'Hook' -v`
Expected: FAIL — `undefined: RunIO`.

- [ ] **Step 3: Implement**

In `internal/cli/cli.go`: rename the body of `Run` into `RunIO(args []string, stdin io.Reader, stdout, stderr io.Writer) int`,
make `Run` call `RunIO(args, strings.NewReader(""), stdout, stderr)`, and add
`case "hook": return cmdHook(args[2:], stdin, stdout, stderr)`. Add to the usage text:

```
  pawl hook pre|stop
        internal: Claude Code hook entry point (PreToolUse / Stop); reads the
        hook payload on stdin. Wired by the plugin's hooks/hooks.json.
```

In `cmd/pawl/main.go`: `return cli.RunIO(args, os.Stdin, stdout, stderr)` (keep `run`'s signature;
pass `os.Stdin` from inside `run`).

`internal/cli/hook.go`:

```go
package cli

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/dcferreira/agent-pawl/internal/guard"
	"github.com/dcferreira/agent-pawl/internal/hook"
	"github.com/dcferreira/agent-pawl/internal/journal"
)

// nowFunc is the clock the enforcement code reads; tests override it.
var nowFunc = time.Now

// cmdHook implements `pawl hook pre|stop`, Claude Code's PreToolUse/Stop
// entry point (enforcement spec): allow = exit 0 silently, deny/block =
// exit 2 with a one-line reason on stderr, fail open = exit 1 (Claude Code
// treats any other non-zero exit as a non-blocking error). Stop never fails
// closed: an error always allows.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) != 1 || (args[0] != "pre" && args[0] != "stop") {
		fmt.Fprintln(stderr, "usage: pawl hook pre|stop  (reads a Claude Code hook payload on stdin)")
		return 2
	}
	event := args[0]
	failOpen := func(msg string) int {
		printLine(stderr, "pawl hook "+event+":", msg)
		if event == "stop" {
			return 0
		}
		return 1
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return failOpen(err.Error())
	}
	p, err := hook.ParsePayload(data)
	if err != nil {
		return failOpen(err.Error())
	}
	root, err := journal.ResolveRoot(p.Cwd)
	if err != nil {
		return failOpen(err.Error())
	}
	if event == "pre" && hook.IsPawlCommand(p.Command) {
		if err := journal.WriteHeartbeat(root, p.SessionID, nowFunc()); err != nil {
			printLine(stderr, "pawl hook pre: writing heartbeat:", err.Error())
		}
	}
	runs, err := loadLiveRuns(root)
	if err != nil {
		return failOpen(err.Error())
	}
	var d hook.Decision
	if event == "pre" {
		d = hook.DecidePre(runs, p)
	} else {
		d = hook.DecideStop(runs, p)
	}
	if d.Allow {
		return 0
	}
	printLine(stderr, d.Reason)
	return 2
}

// loadLiveRuns builds hook.LiveRun values for every live run in root from
// the run directory alone: replayed state (journal.Live), plan.json (guards
// and step kinds) and driver.json.
func loadLiveRuns(root string) ([]hook.LiveRun, error) {
	live, err := journal.Live(root)
	if err != nil {
		return nil, err
	}
	out := make([]hook.LiveRun, 0, len(live))
	for _, ref := range live {
		lr := hook.LiveRun{
			RunID: ref.RunID, WorkflowID: ref.WorkflowID,
			Blocked:    ref.State.Status() == "blocked",
			CursorStep: ref.State.Cursor.Step,
		}
		if !lr.Blocked {
			lr.ActiveSteps = activeSteps(ref.State)
		}
		plan, err := journal.ReadPlan(ref.Dir)
		if err != nil || plan.Workflow == nil {
			lr.GuardsErr = fmt.Errorf("reading plan.json: %v", err)
		} else {
			if s := plan.Workflow.StepByID(lr.CursorStep); s != nil {
				lr.CursorKind = s.Kind
			}
			if len(plan.Workflow.Guards) > 0 {
				lr.Guards, lr.GuardsErr = guard.Compile(plan.Workflow.Guards)
			}
		}
		if d, ok, err := journal.ReadDriver(ref.Dir); err == nil && ok {
			lr.DriverSession = d.SessionID
		}
		out = append(out, lr)
	}
	return out, nil
}

func activeSteps(rs *journal.RunState) []string {
	steps := []string{rs.Cursor.Step}
	var branches []string
	for _, set := range rs.PendingBranches {
		for id, outstanding := range set {
			if outstanding {
				branches = append(branches, id)
			}
		}
	}
	sort.Strings(branches)
	return append(steps, branches...)
}
```

If `ReadPlan` returns `err == nil` with a nil workflow, the `%v` prints `<nil>` — use a clearer
message (`"plan.json has no workflow"`) in that branch.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/cli/ -run 'Hook' -v && go test ./...`
Expected: PASS

- [ ] **Step 5: Commit** — `feat: add the pawl hook pre|stop subcommand`

---

### Task 5: Heartbeat gate, driver stamping, live index, banner

**Files:**
- Modify: `internal/cli/args.go` (`runFlags.NoEnforcement`, parse `--no-enforcement`)
- Modify: `internal/cli/run.go` (gate before banner; stamp driver; banner arg)
- Modify: `internal/cli/submit.go`, `internal/cli/poll.go` (stamp driver on success)
- Modify: `internal/cli/cli.go` (`RunIO`: after `run|submit|poll|abandon`, best-effort `journal.SyncLiveIndex(root)`)
- Modify: `internal/cli/format.go` (`formatBanner` gains an `enforcement` parameter)
- Create: `internal/cli/enforce.go`
- Modify: `internal/cli/testutil_test.go` (`setupWorkingCopy` sets `PAWL_ENFORCEMENT=off`), `internal/cli/cli_test.go` (3 banner goldens), `e2e/green_tests_test.go` (`PAWL_ENFORCEMENT=off` in `project.run`)
- Test: `internal/cli/enforce_test.go`

**Interfaces:**
- Consumes: Task 1 (`ReadHeartbeat`, `WriteDriver`, `SyncLiveIndex`), `nowFunc` (Task 4).
- Produces:
  ```go
  type enforcementMode struct { On bool; OffReason string; SessionID string } // OffReason: "--no-enforcement" | "PAWL_ENFORCEMENT=off"
  func checkEnforcement(root string, flagOff bool) (enforcementMode, error) // error = refusal
  func stampDriver(root, workflowID, runID string)                        // best-effort, fresh heartbeat only
  func formatBanner(rw *resolvedWorkflow, report *spec.Report, guardCount int, mode enforcementMode) string
  ```

- [ ] **Step 1: Write the failing tests** (`internal/cli/enforce_test.go`)

```go
package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

func enforcedWC(t *testing.T) string {
	t.Helper()
	root := setupWorkingCopy(t)
	t.Setenv("PAWL_ENFORCEMENT", "") // undo setupWorkingCopy's opt-out
	writeWorkflow(t, root, "hookwf", hookWF)
	return root
}

func runPawl(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(append([]string{"pawl"}, args...), &out, &errb)
	return code, out.String(), errb.String()
}

func TestRun_RefusesWithoutHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	code, out, errs := runPawl("run", "hookwf")
	if code != 4 {
		t.Fatalf("code=%d out=%q err=%q", code, out, errs)
	}
	if !strings.Contains(errs, "pawl: refusing to start: pawl's PreToolUse hook has not fired in this session.") ||
		!strings.Contains(errs, "Install the agent-pawl Claude Code plugin (docs/install.md#hooks), or pass --no-enforcement.") {
		t.Fatalf("err=%q", errs)
	}
	if live, _ := journal.Live(root); len(live) != 0 {
		t.Fatal("a refused run must not create a run directory")
	}
}

func TestRun_RefusesStaleHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now().Add(-11*time.Second)); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runPawl("run", "hookwf"); code != 4 {
		t.Fatalf("code=%d", code)
	}
}

func TestRun_FreshHeartbeatStartsAndStampsDriver(t *testing.T) {
	root := enforcedWC(t)
	if err := journal.WriteHeartbeat(root, "s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	code, out, errs := runPawl("run", "hookwf")
	if code != 0 {
		t.Fatalf("code=%d err=%q", code, errs)
	}
	if !strings.Contains(out, "hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)") ||
		!strings.Contains(out, "guards: 1 advisory (pattern-matched)") {
		t.Fatalf("banner: %q", out)
	}
	live, _ := journal.Live(root)
	if len(live) != 1 {
		t.Fatalf("live=%v", live)
	}
	d, ok, _ := journal.ReadDriver(live[0].Dir)
	if !ok || d.SessionID != "s1" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}

func TestRun_NoEnforcementFlag(t *testing.T) {
	enforcedWC(t)
	code, out, _ := runPawl("run", "hookwf", "--no-enforcement")
	if code != 0 || !strings.Contains(out, "enforcement: off (--no-enforcement)") ||
		!strings.Contains(out, "guards: 1 declared, NOT enforced") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRun_EnvOptOut(t *testing.T) {
	enforcedWC(t)
	t.Setenv("PAWL_ENFORCEMENT", "off")
	code, out, _ := runPawl("run", "hookwf")
	if code != 0 || !strings.Contains(out, "enforcement: off (PAWL_ENFORCEMENT=off)") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestRun_CreatesLiveIndexEntry(t *testing.T) {
	root := enforcedWC(t)
	runPawl("run", "hookwf", "--no-enforcement")
	live, _ := journal.Live(root)
	link := journal.LiveIndexDir() + "/" + journal.Slug(root) + "__hookwf__" + live[0].RunID
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("live index: %v", err)
	}
}

func TestAbandon_RemovesLiveIndexEntry(t *testing.T) {
	root := enforcedWC(t)
	runPawl("run", "hookwf", "--no-enforcement")
	live, _ := journal.Live(root)
	if code, _, errs := runPawl("abandon", "--run", live[0].RunID); code != 0 {
		t.Fatalf("abandon: %d %s", code, errs)
	}
	entries, _ := os.ReadDir(journal.LiveIndexDir())
	if len(entries) != 0 {
		t.Fatalf("live index not pruned: %v", entries)
	}
}

func TestSubmit_StampsDriverFromFreshHeartbeat(t *testing.T) {
	root := enforcedWC(t)
	runPawl("run", "hookwf", "--no-enforcement") // no driver
	live, _ := journal.Live(root)
	if _, ok, _ := journal.ReadDriver(live[0].Dir); ok {
		t.Fatal("opt-out run must have no driver")
	}
	if err := journal.WriteHeartbeat(root, "s2", time.Now()); err != nil {
		t.Fatal(err)
	}
	// driver.json is stamped only by a successful submit.
	code, _, errs := runPawl("submit", "--run", live[0].RunID, "--step", "fix", "--json", `{"done": true}`)
	if code != 0 {
		t.Fatalf("submit: %d %s", code, errs)
	}
	// The run is terminal now, so read the driver from the run dir directly.
	d, ok, _ := journal.ReadDriver(live[0].Dir)
	if !ok || d.SessionID != "s2" {
		t.Fatalf("driver=%+v ok=%v", d, ok)
	}
}
```

Add `"os"` to the imports. For the submit test, use a two-step workflow if a one-step run's
terminal transition makes the assertion awkward; the requirement is only "successful submit with a
fresh heartbeat writes `driver.json`".

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestRun_|TestAbandon_Removes|TestSubmit_Stamps' -v`
Expected: FAIL (runs start without a heartbeat; banner lacks the new lines; no driver/live index).

- [ ] **Step 3: Implement**

`internal/cli/enforce.go`:

```go
package cli

import (
	"errors"
	"os"

	"github.com/dcferreira/agent-pawl/internal/journal"
)

const envEnforcement = "PAWL_ENFORCEMENT"

// enforcementMode is what pawl run found about the enforcement hooks.
type enforcementMode struct {
	On        bool
	OffReason string // "--no-enforcement" or "PAWL_ENFORCEMENT=off" when !On
	SessionID string // the driving session, from the heartbeat, when On
}

var errNoHeartbeat = errors.New("pawl: refusing to start: pawl's PreToolUse hook has not fired in this session.\n" +
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

// stampDriver records the session behind a fresh heartbeat as runID's
// driver. Best-effort: a missing or stale heartbeat leaves driver.json as
// it was, and a write failure only weakens the Stop hook for this run.
func stampDriver(root, workflowID, runID string) {
	hb, ok, err := journal.ReadHeartbeat(root)
	if err != nil || !ok || !hb.Fresh(nowFunc()) {
		return
	}
	_ = journal.WriteDriver(journal.RunDir(root, workflowID, runID), hb.SessionID, nowFunc())
}
```

`args.go`: add `NoEnforcement bool` to `runFlags` and `case a == "--no-enforcement": flags.NoEnforcement = true`;
add `[--no-enforcement]` to `pawl run`'s usage lines in `run.go` and `cli.go`.

`run.go`, right after `root` is resolved and **before** `engine.New`/`formatBanner`:

```go
	mode, err := checkEnforcement(root, flags.NoEnforcement)
	if err != nil {
		fmt.Fprintln(stderr, err.Error()) // hardcoded text, no workflow content
		return 4
	}
	e := engine.New(w, root)
	fmt.Fprint(stdout, formatBanner(rw, report, len(w.Guards), mode))
```

After a successful `e.Resume(...)` and after a successful `e.Start(...)`: `stampDriver(root, w.Workflow, ref.RunID)` /
`stampDriver(root, w.Workflow, runID)`.

`submit.go` / `poll.go`: after the engine call succeeds and before printing,
`stampDriver(root, w.Workflow, runID)` (use the variable names each file already has for the
pinned workflow id and run id).

`cli.go` `RunIO`: compute `code` from the switch, then for `run`, `submit`, `poll`, `abandon`:

```go
	if root, err := journal.ResolveRoot(cwd); err == nil {
		_ = journal.SyncLiveIndex(root) // fast-path cache only; failure is harmless
	}
	return code
```

`format.go` `formatBanner(rw, report, guardCount, mode)`:

```go
	if mode.On {
		w.literal("hooks: PreToolUse ✔ (heartbeat)  Stop assumed (same hooks.json)\n")
		if guardCount > 0 {
			w.line(0, fmt.Sprintf("guards: %d advisory (pattern-matched)", guardCount))
		}
	} else {
		w.line(0, "enforcement: off ("+mode.OffReason+")")
		if guardCount > 0 {
			w.line(0, fmt.Sprintf("guards: %d declared, NOT enforced (no PreToolUse hook in this build)", guardCount))
		}
	}
```

Change the off-branch guards wording to `guards: %d declared, NOT enforced (enforcement off)` —
the old "(no PreToolUse hook in this build)" is no longer true; update PR #12's golden for it and
the test above accordingly. Update `formatBanner`'s doc comment (remove "Ruling R5 — there are no
hooks").

Test helpers: in `testutil_test.go` `setupWorkingCopy`, add `t.Setenv("PAWL_ENFORCEMENT", "off")`
with a comment ("the enforcement gate is exercised in enforce_test.go; every other test runs with
it explicitly off"). In `cli_test.go`, replace the 3 occurrences of
`enforcement: off (milestone 1)` with `enforcement: off (PAWL_ENFORCEMENT=off)`. In `e2e/green_tests_test.go`
`project.run`, append `"PAWL_ENFORCEMENT=off"` to `cmd.Env`. Remove Task 4's now-redundant
`t.Setenv("PAWL_ENFORCEMENT","off")` from `startHookRun`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./... && go test -race ./... && make fmt-check vet staticcheck`
Expected: PASS

- [ ] **Step 5: Commit** — `feat: gate pawl run on the PreToolUse heartbeat and stamp the driving session`

---

### Task 6: `bin/pawl-hook` fast path and plugin `hooks/hooks.json`

**Files:**
- Create: `bin/pawl-hook` (mode 0755), `hooks/hooks.json`
- Test: `internal/cli/pawlhook_script_test.go` (Go test that execs the script; lives in `cli` only
  because that package already has test helpers — alternatively a new `e2e/pawl_hook_test.go`; pick
  `e2e/` if it keeps `internal/cli` free of repo-layout knowledge)

**Interfaces:**
- Consumes: the sibling `bin/pawl` wrapper (resolves a real `pawl` on PATH, recursion-guarded).
- Produces: `pawl-hook pre|stop` reading the payload on stdin.

- [ ] **Step 1: Write the failing test** (`e2e/pawl_hook_test.go`)

```go
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

// fakePawl puts a `pawl` on PATH that records its args and stdin.
func fakePawl(t *testing.T) (pathDir, logFile string) {
	t.Helper()
	pathDir = t.TempDir()
	logFile = filepath.Join(t.TempDir(), "log")
	script := "#!/bin/sh\necho \"args:$*\" >> " + logFile + "\ncat >> " + logFile + "\nexit 7\n"
	if err := os.WriteFile(filepath.Join(pathDir, "pawl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return pathDir, logFile
}

func runHookScript(t *testing.T, stateDir, pathDir, event, stdin string) (int, string) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "bin", "pawl-hook"), event)
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = []string{"PATH=" + pathDir + ":/usr/bin:/bin", "HOME=" + t.TempDir(), "PAWL_STATE_DIR=" + stateDir}
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return code, string(out)
}

func TestPawlHook_FastPathSkipsBinary(t *testing.T) {
	pathDir, logFile := fakePawl(t)
	stateDir := filepath.Join(t.TempDir(), "runs")
	code, _ := runHookScript(t, stateDir, pathDir, "pre", `{"cwd":"/x","tool_input":{"command":"ls"}}`)
	if code != 0 {
		t.Fatalf("code=%d", code)
	}
	if _, err := os.Stat(logFile); err == nil {
		t.Fatal("binary invoked on the no-live-run, non-pawl fast path")
	}
}

func TestPawlHook_PawlCommandReachesBinaryWithStdin(t *testing.T) {
	pathDir, logFile := fakePawl(t)
	stateDir := filepath.Join(t.TempDir(), "runs")
	payload := `{"cwd":"/x","tool_input":{"command":"pawl run g"}}`
	code, _ := runHookScript(t, stateDir, pathDir, "pre", payload)
	if code != 7 {
		t.Fatalf("exit code must be the binary's; got %d", code)
	}
	log, _ := os.ReadFile(logFile)
	if !strings.Contains(string(log), "args:hook pre") || !strings.Contains(string(log), payload) {
		t.Fatalf("log=%q", log)
	}
}

func TestPawlHook_LiveRunReachesBinaryForStop(t *testing.T) {
	pathDir, logFile := fakePawl(t)
	base := t.TempDir()
	stateDir := filepath.Join(base, "runs")
	if err := os.MkdirAll(filepath.Join(base, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent", filepath.Join(base, "live", "x__wf__ab12")); err != nil {
		t.Fatal(err)
	}
	runHookScript(t, stateDir, pathDir, "stop", `{"cwd":"/x"}`)
	log, _ := os.ReadFile(logFile)
	if !strings.Contains(string(log), "args:hook stop") {
		t.Fatalf("log=%q", log)
	}
}

func TestPawlHook_BadEventUsage(t *testing.T) {
	pathDir, _ := fakePawl(t)
	if code, _ := runHookScript(t, t.TempDir(), pathDir, "bogus", "{}"); code != 2 {
		t.Fatalf("code=%d", code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./e2e/ -run PawlHook -v`
Expected: FAIL — `bin/pawl-hook` does not exist.

- [ ] **Step 3: Implement**

`bin/pawl-hook`:

```sh
#!/bin/sh
# Claude Code hook entry point shipped by the agent-pawl plugin
# (hooks/hooks.json): PreToolUse(Bash) -> `pawl-hook pre`, Stop -> `pawl-hook stop`.
#
# Fast path: with no live run and no mention of pawl in the payload there is
# nothing to enforce and no heartbeat to write, so exit 0 without starting the
# Go binary. The live/ directory is only a cache for this check — `pawl hook`
# itself always re-derives the live runs from the run directories.
#
# Otherwise hand the payload to the real binary through the sibling bin/pawl
# wrapper, which already finds a real `pawl` on PATH without recursing.
case "${1:-}" in
  pre|stop) ;;
  *) echo "usage: pawl-hook pre|stop" >&2; exit 2 ;;
esac

if [ -n "${PAWL_STATE_DIR:-}" ]; then
  home=$(dirname "$PAWL_STATE_DIR")
else
  home="$HOME/.claude/pawl"
fi

payload=$(cat)

live=no
for f in "$home"/live/*; do
  if [ -e "$f" ] || [ -L "$f" ]; then live=yes; break; fi
done

if [ "$live" = no ]; then
  case "$payload" in
    *pawl*) ;;
    *) exit 0 ;;
  esac
fi

here=$(cd "$(dirname "$0")" && pwd -P)
printf '%s' "$payload" | exec "$here/pawl" hook "$1"
```

Note `exec` on the right side of a pipe runs in a subshell in POSIX sh; the script's exit status is
the pipeline's last command's, which is what the test asserts. `bin/pawl` is bash; it's invoked by
path, so its shebang applies.

`hooks/hooks.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook pre" }]
      }
    ],
    "Stop": [
      {
        "hooks": [{ "type": "command", "command": "${CLAUDE_PLUGIN_ROOT}/bin/pawl-hook stop" }]
      }
    ]
  }
}
```

- [ ] **Step 4: Run tests and plugin validation**

Run: `chmod +x bin/pawl-hook && go test ./e2e/ -run PawlHook -v && claude plugin validate . --strict`
Expected: PASS; validator reports no errors.

- [ ] **Step 5: Commit** — `feat: ship the pawl-hook fast path and plugin hooks.json`

---

### Task 7: Docs — describe the build that now exists

**Files:**
- Modify: `README.md` (Status bullets: enforcement layer exists; `pawl hook pre|stop` exists), `AGENTS.md` (Status: remove "There is no enforcement layer" and "`pawl hook` still doesn't exist"; add `internal/hook` to Layout; add `bin/pawl-hook`/`hooks/hooks.json` to the plugin section; note `PAWL_ENFORCEMENT=off` in tests), `DESIGN.md` §5 (write back the spec's 5 deviations; update the opening status line), `skills/pawl/SKILL.md` (replace the "no enforcement layer" paragraphs: the Stop hook will refuse ending the turn mid-dispatch; the refusal message and `--no-enforcement`; subagents may not mutate VCS), `docs/install.md` (new `#hooks` section: plugin wires them automatically; settings.json snippet for non-plugin users using `pawl hook pre` / `pawl hook stop` directly), `docs/cli.md` (`pawl hook`, `--no-enforcement`, exit 4 refusal), `docs/troubleshooting.md` (the refusal, the Stop block, fail-closed guards), `docs/dogfood.md` (enforcement section), `docs/guards-and-invariants.md` (banner example matches Task 5's exact text), `design/format-spec.md` only where it states hooks are absent.

**Interfaces:** none (docs only).

- [ ] **Step 1:** `grep -rn -i 'enforcement: off\|no enforcement\|pawl hook\|milestone 1' README.md AGENTS.md DESIGN.md docs skills design` — every hit is either updated or confirmed still true.
- [ ] **Step 2:** Write the `docs/install.md#hooks` settings.json snippet:

```json
{
  "hooks": {
    "PreToolUse": [{ "matcher": "Bash", "hooks": [{ "type": "command", "command": "pawl hook pre" }] }],
    "Stop": [{ "hooks": [{ "type": "command", "command": "pawl hook stop" }] }]
  }
}
```

- [ ] **Step 3:** Keep docs VCS-neutral (never claim the repo uses jj; engine jj support mentions are fine).
- [ ] **Step 4:** Run `make test` (golden tests that read docs, if any) and `claude plugin validate . --strict`.
- [ ] **Step 5: Commit** — `docs: document the enforcement hooks`

---

### Task 8: Live proof (orchestrator, not a subagent)

Not a code task; no commit unless it finds a bug (which becomes a new red-green task).

- [ ] `make build`, put `dist/` first on `PATH`.
- [ ] In a scratch copy of `testdata/fixture` with `docs/examples/green-tests` installed as a workflow:
  1. Plain terminal `pawl run green-tests` → exit 4 refusal.
  2. `claude --plugin-dir <workspace>` session driving `green-tests` via the skill → banner shows
     `hooks: PreToolUse ✔`; at `DISPATCH`, ask the session to end its turn → Stop refuses with
     the abandon hint; a second end-turn succeeds (`stop_hook_active`).
  3. Subagent attempts `git commit` → denied.
  4. A workflow with `guards: [{id: no-push, match: "git push", only_in: []}]` → `git push` denied.
  5. `pawl abandon --run <id>` → allowed; Stop no longer refuses; `live/` entry gone.
- [ ] Record the transcript excerpts for the PR body.
