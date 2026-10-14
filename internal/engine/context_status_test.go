package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dcferreira/agent-pawl/internal/render"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

const ctxStatusYAML = `
workflow: ctx-status
start: work
state:
  result: {type: string, default: ""}
steps:
  - id: work
    kind: agentic
    description: "look at the context"
    context:
      - !cmd "echo hi; exit 2"
    writes: {result: {type: string}}
    postcondition: "true"
    next: done
terminal: {done: {status: ok}}
`

func gatherCmd(t *testing.T, e *Engine, cmd string) ContextItem {
	t.Helper()
	item, err := e.gatherContext(spec.ContextEntry{Value: cmd, IsCmd: true}, render.Values{})
	if err != nil {
		t.Fatalf("gatherContext(%q): %v", cmd, err)
	}
	return item
}

func TestGatherContext_CmdNonZeroExitSetsStatus(t *testing.T) {
	e := newTestEngine(t, ctxStatusYAML)
	item := gatherCmd(t, e, `printf 'partial\n'; exit 3`)
	if item.Value != "partial\n" {
		t.Errorf("Value = %q, want exactly the captured stdout", item.Value)
	}
	if item.Status != "context command exited 3" {
		t.Errorf("Status = %q, want %q", item.Status, "context command exited 3")
	}
}

func TestGatherContext_CmdZeroExitHasNoStatus(t *testing.T) {
	e := newTestEngine(t, ctxStatusYAML)
	item := gatherCmd(t, e, `echo ok`)
	if item.Status != "" || item.Value != "ok\n" {
		t.Errorf("item = %+v, want Value \"ok\\n\" and empty Status", item)
	}
}

func TestGatherContext_CmdTimeoutSetsStatus(t *testing.T) {
	setKillGrace(t, 200*time.Millisecond)
	e := newTestEngine(t, ctxStatusYAML)
	e.Timeout = 300 * time.Millisecond
	item := gatherCmd(t, e, `echo before; sleep 30`)
	if item.Status != "context command timed out" {
		t.Errorf("Status = %q, want %q", item.Status, "context command timed out")
	}
}

func TestGatherContext_FileEntryHasNoStatus(t *testing.T) {
	e := newTestEngine(t, ctxStatusYAML)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.Workflow.Path), "n.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	item, err := e.gatherContext(spec.ContextEntry{Value: "n.md"}, render.Values{})
	if err != nil {
		t.Fatal(err)
	}
	if item.Status != "" || item.Value != "x" {
		t.Errorf("item = %+v", item)
	}
}

func TestStart_DispatchCarriesContextCmdStatus(t *testing.T) {
	e := newTestEngine(t, ctxStatusYAML)
	instr, err := e.Start("run1", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	d, ok := instr.(Dispatch)
	if !ok {
		t.Fatalf("got %+v, want Dispatch", instr)
	}
	if len(d.Context) != 1 || d.Context[0].Value != "hi\n" || d.Context[0].Status != "context command exited 2" {
		t.Errorf("Context = %+v", d.Context)
	}
}
