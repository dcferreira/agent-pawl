package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

// EnvStateDir overrides the run-directory base, for tests.
const EnvStateDir = "PAWL_STATE_DIR"

// StateBase returns the base directory under which every run directory is
// created: $PAWL_STATE_DIR if set (for tests), else ~/.claude/pawl/runs.
func StateBase() string {
	if d := os.Getenv(EnvStateDir); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".claude", "pawl", "runs")
	}
	return filepath.Join(home, ".claude", "pawl", "runs")
}

// RunDir returns the run directory for (root, workflowID, runID):
// <state_base>/<slug(root)>/<workflowID>/<runID>/ (DESIGN.md §4).
func RunDir(root, workflowID, runID string) string {
	return filepath.Join(StateBase(), Slug(root), workflowID, runID)
}

// WorkflowDir returns the directory holding every run of workflowID for
// root, the level Live enumerates run ids under.
func WorkflowDir(root, workflowID string) string {
	return filepath.Join(StateBase(), Slug(root), workflowID)
}

// CreateRunDir creates the run directory for (root, workflowID, runID) and
// returns its path. It is safe to call on an already-existing directory.
func CreateRunDir(root, workflowID, runID string) (string, error) {
	dir := RunDir(root, workflowID, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("journal: creating run directory %s: %w", dir, err)
	}
	return dir, nil
}

// Plan is the immutable, parsed-graph-plus-digest content of a run's
// plan.json (DESIGN.md §4).
type Plan struct {
	Workflow *spec.Workflow `json:"workflow"`
	Digest   string         `json:"digest"`
}

// digestWorkflow and digestStep are a path-free, defaults-free projection of
// spec.Workflow/spec.Step used only to compute Digest. Two fields are
// deliberately excluded from every step and from the workflow itself:
//
//   - Path: an invocation detail (where the file happened to be loaded
//     from), not authored content. spec.Workflow.Path carries yaml:"-" but
//     no json tag, so a naive json.Marshal(w) includes it verbatim — the
//     same file loaded as "workflow.yaml" from one cwd and
//     "/abs/path/workflow.yaml" from another then digests differently, and
//     step 2 of DESIGN.md §4's resume procedure refuses a live run over
//     nothing but where the user happened to invoke `pawl` from.
//   - MaxSteps/Attempts/MaxVisits (the resolved fields): these are
//     computed from MaxStepsRaw/AttemptsRaw/MaxVisitsRaw by applyDefaults,
//     not authored content in their own right, and duplicate the *Raw
//     fields' information. Digesting them as well as their *Raw witnesses
//     would make the digest sensitive to a defaulting-logic change even
//     when the workflow file itself did not change; digesting only the
//     *Raw fields keeps Digest a function of exactly what was authored.
type digestWorkflow struct {
	Workflow    string
	Description string
	Start       string
	MaxStepsRaw *int
	Args        map[string]spec.ArgDecl
	State       map[string]spec.StateDecl
	Guards      []spec.GuardDecl
	Invariants  []spec.InvariantDecl
	Steps       []digestStep
	Terminal    map[string]spec.Terminal
}

type digestStep struct {
	ID            string
	Kind          string
	Run           string
	Emits         string
	Description   string
	Context       []spec.ContextEntry
	SubagentArgs  any
	Poll          string
	Every         string
	Timeout       string
	Question      string
	Options       []string
	OptionsFrom   string
	Multi         bool
	Writes        spec.Writes
	Postcondition *spec.Postcondition
	AttemptsRaw   *int
	AttemptKey    string
	MaxVisitsRaw  *int
	Retry         any
	Catch         []spec.CatchRule
	Next          string
	Outcomes      map[string]string
	Branches      []string
	UnknownFields []string
}

func newDigestWorkflow(w *spec.Workflow) *digestWorkflow {
	dw := &digestWorkflow{
		Workflow:    w.Workflow,
		Description: w.Description,
		Start:       w.Start,
		MaxStepsRaw: w.MaxStepsRaw,
		Args:        w.Args,
		State:       w.State,
		Guards:      w.Guards,
		Invariants:  w.Invariants,
		Terminal:    w.Terminal,
	}
	for _, s := range w.Steps {
		dw.Steps = append(dw.Steps, digestStep{
			ID:            s.ID,
			Kind:          s.Kind,
			Run:           s.Run,
			Emits:         s.Emits,
			Description:   s.Description,
			Context:       s.Context,
			SubagentArgs:  s.SubagentArgs,
			Poll:          s.Poll,
			Every:         s.Every,
			Timeout:       s.Timeout,
			Question:      s.Question,
			Options:       s.Options,
			OptionsFrom:   s.OptionsFrom,
			Multi:         s.Multi,
			Writes:        s.Writes,
			Postcondition: s.Postcondition,
			AttemptsRaw:   s.AttemptsRaw,
			AttemptKey:    s.AttemptKey,
			MaxVisitsRaw:  s.MaxVisitsRaw,
			Retry:         s.Retry,
			Catch:         s.Catch,
			Next:          s.Next,
			Outcomes:      s.Outcomes,
			Branches:      s.Branches,
			UnknownFields: s.UnknownFields,
		})
	}
	return dw
}

// Digest returns a hex-encoded sha256 digest of w's authored content (see
// digestWorkflow), stable across processes and invocation directories for
// the same authored content: the value plan.json records and that a later
// `pawl run` recompiles and compares to detect a changed workflow file out
// from under a resumed run.
func Digest(w *spec.Workflow) (string, error) {
	data, err := json.Marshal(newDigestWorkflow(w))
	if err != nil {
		return "", fmt.Errorf("journal: digesting workflow: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// WritePlan computes w's digest and writes plan.json into dir.
func WritePlan(dir string, w *spec.Workflow) (*Plan, error) {
	digest, err := Digest(w)
	if err != nil {
		return nil, err
	}
	plan := &Plan{Workflow: w, Digest: digest}
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("journal: marshalling plan.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.json"), data, 0o644); err != nil {
		return nil, fmt.Errorf("journal: writing plan.json: %w", err)
	}
	return plan, nil
}

// ReadPlan reads and parses dir's plan.json.
func ReadPlan(dir string) (*Plan, error) {
	data, err := os.ReadFile(filepath.Join(dir, "plan.json"))
	if err != nil {
		return nil, fmt.Errorf("journal: reading plan.json: %w", err)
	}
	var plan Plan
	if err := json.Unmarshal(data, &plan); err != nil {
		return nil, fmt.Errorf("journal: parsing plan.json: %w", err)
	}
	return &plan, nil
}

// Status is the cosmetic, human-readable projection written to status.json
// after every transition. It is deletable at any time and never read back by
// the engine: RunState (from Replay) is the only state that matters.
type Status struct {
	RunID      string `json:"run_id"`
	WorkflowID string `json:"workflow_id"`
	Root       string `json:"root"`
	Step       string `json:"step"`
	Attempt    int    `json:"attempt"`
	Status     string `json:"status"`
}

// WriteStatus rebuilds status.json from rs. A failure to write it is not
// fatal to the run — it is cosmetic — but is still returned for the caller
// to log.
func WriteStatus(dir string, runID, workflowID, root string, rs *RunState) error {
	st := Status{
		RunID:      runID,
		WorkflowID: workflowID,
		Root:       root,
		Step:       rs.Cursor.Step,
		Attempt:    rs.Cursor.Attempt,
		Status:     rs.Status(),
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("journal: marshalling status.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0o644); err != nil {
		return fmt.Errorf("journal: writing status.json: %w", err)
	}
	return nil
}
