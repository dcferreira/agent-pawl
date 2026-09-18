package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/journal"
	"github.com/dcferreira/agent-pawl/internal/spec"
)

// resolvedWorkflow is a workflow file found by name, plus the source it was
// found under, for the start banner (design/format-spec.md §I, DESIGN.md
// §9: "pawl run prints which one it used").
type resolvedWorkflow struct {
	Path   string
	Source string // "repo-local" or "user"
}

// resolveWorkflowFile finds <name>.yaml the way DESIGN.md §9 specifies: a
// repo-local .claude/workflows/<name>.yaml, searched from cwd upward through
// the working-copy root inclusive — never above it, since a directory
// outside the working copy has no business supplying a workflow for it —
// then ~/.claude/workflows/<name>.yaml. First match wins.
func resolveWorkflowFile(cwd, name string) (*resolvedWorkflow, error) {
	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		return nil, fmt.Errorf("pawl: resolving working-copy root: %w", err)
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("pawl: resolving cwd: %w", err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}

	dir := abs
	for {
		candidate := filepath.Join(dir, ".claude", "workflows", name+".yaml")
		if fileExists(candidate) {
			return &resolvedWorkflow{Path: candidate, Source: "repo-local"}, nil
		}
		if dir == root {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	if home, err := os.UserHomeDir(); err == nil {
		candidate := filepath.Join(home, ".claude", "workflows", name+".yaml")
		if fileExists(candidate) {
			return &resolvedWorkflow{Path: candidate, Source: "user"}, nil
		}
	}

	return nil, fmt.Errorf("pawl: no workflow named %q found under .claude/workflows/ (searched %s up to working-copy root %s) or ~/.claude/workflows/", name, abs, root)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// loadAndValidate loads path and runs spec.Validate: pawl run and pawl validate
// both gate on Validate returning zero errors before anything constructs an
// Engine (the engine's contract is that a schema always exists).
func loadAndValidate(path string) (*spec.Workflow, *spec.Report, error) {
	w, err := spec.Load(path)
	if err != nil {
		return nil, nil, err
	}
	report, err := spec.Validate(w)
	if err != nil {
		return nil, nil, err
	}
	return w, report, nil
}

// pinnedWorkflow is the workflow definition exactly as pawl run pinned it for
// a run (plan.json's own recorded copy — DESIGN.md §4: "immutable for the
// run"), plus whether the file it was originally loaded from has since
// changed underneath the run, and which steps differ if so.
type pinnedWorkflow struct {
	Workflow *spec.Workflow
	Changed  bool
	Detail   string
}

// loadPinnedWorkflow reads dir's plan.json and returns the workflow
// definition it recorded, never by re-resolving a name.
//
// Finding C1: a workflow whose workflow: id differs from the filename pawl
// run was given must not make a live run unreachable by every command but
// abandon — pawl status and pawl submit used to re-run resolveWorkflowFile
// against the run directory's own name (the workflow: id), which is not
// necessarily a resolvable filename at all. plan.Workflow.Path (present
// because spec.Workflow.Path has no json:"-" tag: journal.WritePlan
// marshals the whole *spec.Workflow, so the exact file pawl run loaded
// travels with the run) is the only correct source for "which file is
// this".
//
// Finding C2: pawl submit is a separate process for every agentic step, so it
// must never silently adopt a mid-run edit of that file. This reloads
// plan.Workflow.Path and compares its digest to plan.Digest, exactly the
// check Engine.Resume already performs for pawl run, and reports the result
// via Changed/Detail rather than ever returning the newer content: the
// pinned copy is always what Workflow holds. A file that has since become
// unreadable or unparsable is not itself treated as a mismatch — the run's
// own pinned copy is unaffected by the state of a file nothing is reading
// off disk for execution — only a successfully reloaded, differently
// hashed file counts.
func loadPinnedWorkflow(dir string) (*pinnedWorkflow, error) {
	plan, err := journal.ReadPlan(dir)
	if err != nil {
		return nil, fmt.Errorf("pawl: reading plan for %s: %w", dir, err)
	}
	if plan.Workflow == nil || plan.Workflow.Path == "" {
		return nil, fmt.Errorf("pawl: plan.json for %s has no recorded workflow path", dir)
	}
	pw := &pinnedWorkflow{Workflow: plan.Workflow}

	current, cerr := spec.Load(plan.Workflow.Path)
	if cerr != nil {
		return pw, nil
	}
	digest, derr := journal.Digest(current)
	if derr != nil {
		return pw, nil
	}
	if digest != plan.Digest {
		pw.Changed = true
		pw.Detail = diffSteps(plan.Workflow, current)
	}
	return pw, nil
}

// diffSteps names which of new's steps differ from old's — best-effort
// detail for a digest-mismatch refusal, since journal.Digest itself reports
// only a single hex string.
func diffSteps(old, new *spec.Workflow) string {
	oldByID := map[string]*spec.Step{}
	for i := range old.Steps {
		oldByID[old.Steps[i].ID] = &old.Steps[i]
	}
	newByID := map[string]*spec.Step{}
	for i := range new.Steps {
		newByID[new.Steps[i].ID] = &new.Steps[i]
	}

	var changed []string
	for id, ns := range newByID {
		olds, ok := oldByID[id]
		if !ok {
			changed = append(changed, id+" (added)")
			continue
		}
		if !reflect.DeepEqual(olds, ns) {
			changed = append(changed, id)
		}
	}
	for id := range oldByID {
		if _, ok := newByID[id]; !ok {
			changed = append(changed, id+" (removed)")
		}
	}
	sort.Strings(changed)
	if len(changed) == 0 {
		return "workflow-level fields only (start:, args:, state:, terminal:, …)"
	}
	return strings.Join(changed, ", ")
}

// blockedDetail returns the most recent failed POSTCONDITION event's text
// for (root, workflowID, runID) — the actual diagnostic (a postcondition's
// output, or a hard-failure text like a context entry that could not be
// gathered) that produced a blocked run, as opposed to the generic
// "<step>: <outcome>" blocked_reason pseudo-key (finding I2). It returns ""
// when the journal is unreadable or carries no such event.
func blockedDetail(root, workflowID, runID string) string {
	dir := journal.RunDir(root, workflowID, runID)
	events, err := journal.ReadEvents(dir)
	if err != nil {
		return ""
	}
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Kind == journal.KindPostcondition && !e.OK {
			return e.Text
		}
	}
	return ""
}

// listYAMLBaseNames lists the base names (without .yaml) of every workflow
// file directly under dir, sorted. A missing or unreadable dir yields no
// names rather than an error: neither a repo without .claude/workflows nor a
// machine without ~/.claude/workflows is a defect.
func listYAMLBaseNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		names = append(names, strings.TrimSuffix(e.Name(), ".yaml"))
	}
	sort.Strings(names)
	return names
}
