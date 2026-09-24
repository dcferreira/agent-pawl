package guard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/spec"
)

func TestDenied_NoGuards(t *testing.T) {
	if got := Denied(nil, []string{"a"}, "git push"); got != nil {
		t.Fatalf("Denied(nil guards) = %+v, want nil", got)
	}
}

func TestDenied_OnlyInEmptyDeniesInAnyStep(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "no-push", Match: "git push", OnlyIn: []string{}},
	}
	if got := Denied(guards, []string{"a"}, "git push origin"); got == nil || got.ID != "no-push" {
		t.Fatalf("Denied = %+v, want the no-push guard", got)
	}
	if got := Denied(guards, []string{"anything-else"}, "git push origin"); got == nil {
		t.Fatalf("Denied = nil, want denied everywhere (only_in: [])")
	}
}

func TestDenied_OnlyInPermitsNamedStepDeniesOthers(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "push-only-in-commit", Match: "git push", OnlyIn: []string{"commit_and_pr"}},
	}
	if got := Denied(guards, []string{"commit_and_pr"}, "git push origin"); got != nil {
		t.Fatalf("Denied = %+v, want nil (permitted in commit_and_pr)", got)
	}
	if got := Denied(guards, []string{"other_step"}, "git push origin"); got == nil {
		t.Fatalf("Denied = nil, want denied (not in only_in)")
	}
}

func TestDenied_MultipleActiveStepsOnePermits(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "push-only-in-commit", Match: "git push", OnlyIn: []string{"commit_and_pr"}},
	}
	active := []string{"other_branch", "commit_and_pr"}
	if got := Denied(guards, active, "git push origin"); got != nil {
		t.Fatalf("Denied = %+v, want nil (one active step permits)", got)
	}
}

func TestDenied_FirstMatchOrderWins(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "first", Match: "git push", OnlyIn: []string{}},
		{ID: "second", Match: "push", OnlyIn: []string{}},
	}
	got := Denied(guards, nil, "git push origin")
	if got == nil || got.ID != "first" {
		t.Fatalf("Denied = %+v, want the first declared matching guard", got)
	}
}

func TestDenied_UnanchoredMatch(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "changelog", Match: "CHANGELOG.md", OnlyIn: []string{}},
	}
	if got := Denied(guards, nil, "sed -i s/x/y/ CHANGELOG.md"); got == nil {
		t.Fatalf("Denied = nil, want denied (unanchored match anywhere in command)")
	}
	if got := Denied(guards, nil, "echo unrelated"); got != nil {
		t.Fatalf("Denied = %+v, want nil (no match)", got)
	}
}

// TestDenied_BackslashNewlineContinuationCaught proves the format-spec §10
// normalization: a command split across lines with a trailing `\` (the
// canonical Bash tool wrapping of a long command) matches a guard the same
// as its one-line spelling would, even though `.` does not match a literal
// `\n` under Go's default regexp flags.
func TestDenied_BackslashNewlineContinuationCaught(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "merge-guard", Match: "gh pr merge .*", OnlyIn: []string{}},
	}
	command := "gh pr merge \\\n  41"
	if got := Denied(guards, nil, command); got == nil || got.ID != "merge-guard" {
		t.Fatalf("Denied(%q) = %+v, want denied by merge-guard (backslash-newline continuation normalized)", command, got)
	}
}

// TestDenied_BackslashCarriageReturnNewlineContinuationCaught is the same
// as above but for a `\r\n` line ending.
func TestDenied_BackslashCarriageReturnNewlineContinuationCaught(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "merge-guard", Match: "gh pr merge .*", OnlyIn: []string{}},
	}
	command := "gh pr merge \\\r\n  41"
	if got := Denied(guards, nil, command); got == nil || got.ID != "merge-guard" {
		t.Fatalf("Denied(%q) = %+v, want denied by merge-guard (\\r\\n continuation normalized)", command, got)
	}
}

// TestDenied_PlainNewlineStillMatchedOnItsOwnLine documents the other half
// of the decision: a bare newline with no preceding backslash is not a
// continuation, so it is left alone. An unanchored match: still finds it,
// because substring search does not stop at `\n` — only `.` and the
// anchors do — so this is unchanged from before the normalization was
// added.
func TestDenied_PlainNewlineStillMatchedOnItsOwnLine(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "no-push", Match: "git push", OnlyIn: []string{}},
	}
	command := "echo hi\ngit push origin"
	if got := Denied(guards, nil, command); got == nil || got.ID != "no-push" {
		t.Fatalf("Denied(%q) = %+v, want denied (plain newline-separated second command still matched)", command, got)
	}
}

func TestDenied_EmptyActiveSteps(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "push-only-in-commit", Match: "git push", OnlyIn: []string{"commit_and_pr"}},
	}
	if got := Denied(guards, nil, "git push origin"); got == nil {
		t.Fatalf("Denied = nil, want denied (no active step can permit it)")
	}
}

func TestDenied_NoMatchIsAllowed(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "no-push", Match: "git push", OnlyIn: []string{}},
	}
	if got := Denied(guards, nil, "echo hello"); got != nil {
		t.Fatalf("Denied = %+v, want nil (command does not match)", got)
	}
}

func TestDenied_CompileErrorFailsClosed(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "bad-regexp", Match: "git push (", OnlyIn: []string{}},
	}
	got := Denied(guards, nil, "anything at all")
	if got == nil || got.ID != "bad-regexp" {
		t.Fatalf("Denied = %+v, want the guard whose match fails to compile (fail closed)", got)
	}
}

func TestDenied_CompileErrorFailsClosed_FirstOffenderInOrder(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "fine", Match: "git push", OnlyIn: []string{}},
		{ID: "first-bad", Match: "git push (", OnlyIn: []string{}},
		{ID: "second-bad", Match: "gh pr merge (", OnlyIn: []string{}},
	}
	got := Denied(guards, nil, "anything")
	if got == nil || got.ID != "first-bad" {
		t.Fatalf("Denied = %+v, want the first guard (in declaration order) that fails to compile", got)
	}
}

// TestFirstCompileFailure_FallsBackToSyntheticNonNilDecl is the fail-closed
// regression test for the should-be-unreachable branch of Denied's compile-
// error handling: if no guard in the slice is actually found to fail
// re-compiling (which Denied only reaches after Compile itself already
// reported an error), the fallback must still be a non-nil, denying guard
// — never nil, which Table.Denied's caller reads as "allowed". Exercised
// directly against firstCompileFailure with an empty slice, since Denied
// itself cannot be driven into this branch without a genuinely
// compile-broken guard already making the first loop in
// firstCompileFailure succeed.
func TestFirstCompileFailure_FallsBackToSyntheticNonNilDecl(t *testing.T) {
	got := firstCompileFailure(nil)
	if got == nil {
		t.Fatalf("firstCompileFailure(nil) = nil, want a non-nil synthetic denying guard (fail closed)")
	}
	if got.ID == "" {
		t.Fatalf("firstCompileFailure(nil) = %+v, want a non-empty ID identifying it as synthetic", got)
	}
}

func TestCompile_ErrorNamesTheGuard(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "bad-regexp", Match: "git push (", OnlyIn: []string{}},
	}
	_, err := Compile(guards)
	if err == nil {
		t.Fatalf("Compile: expected an error, got nil")
	}
	got := err.Error()
	if !strings.Contains(got, "bad-regexp") || !strings.Contains(got, "git push (") {
		t.Fatalf("Compile error = %q, want it to name the guard and its match", got)
	}
}

func TestTable_NilTableDeniedReturnsNil(t *testing.T) {
	var table *Table
	if got := table.Denied([]string{"a"}, "git push"); got != nil {
		t.Fatalf("(*Table)(nil).Denied = %+v, want nil", got)
	}
}

func TestNormalize_NilOnlyInBecomesEmptySlice(t *testing.T) {
	in := []spec.GuardDecl{
		{ID: "a", Match: "x", OnlyIn: nil},
		{ID: "b", Match: "y", OnlyIn: []string{"step1"}},
	}
	out := Normalize(in)
	if out[0].OnlyIn == nil {
		t.Fatalf("Normalize: OnlyIn is still nil, want a non-nil empty slice")
	}
	if len(out[0].OnlyIn) != 0 {
		t.Fatalf("Normalize: OnlyIn = %v, want empty", out[0].OnlyIn)
	}
	if len(out[1].OnlyIn) != 1 || out[1].OnlyIn[0] != "step1" {
		t.Fatalf("Normalize: OnlyIn = %v, want [step1] preserved", out[1].OnlyIn)
	}
	// The input must not be mutated.
	if in[0].OnlyIn != nil {
		t.Fatalf("Normalize mutated its input's OnlyIn")
	}
}

// TestGuardsJSON_RoundTrip proves []spec.GuardDecl marshals with the
// id/match/only_in keys the guards.json contract (DESIGN.md §9's static
// hooks reading guards.json) relies on, and that a nil OnlyIn needs
// Normalize to avoid writing "only_in": null.
func TestGuardsJSON_RoundTrip(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "never-rewrite-changelog", Match: "(sed|awk) .*CHANGELOG.md", OnlyIn: nil},
	}

	rawWithoutNormalize, err := json.Marshal(guards)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var withoutNormalize []map[string]any
	if err := json.Unmarshal(rawWithoutNormalize, &withoutNormalize); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if withoutNormalize[0]["only_in"] != nil {
		t.Fatalf("plain marshal of a nil OnlyIn wrote %v, want null (documenting why Normalize is needed)", withoutNormalize[0]["only_in"])
	}

	raw, err := json.Marshal(Normalize(guards))
	if err != nil {
		t.Fatalf("json.Marshal(Normalize(...)): %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	entry := decoded[0]
	for _, key := range []string{"id", "match", "only_in"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("marshalled guard missing key %q: %v", key, entry)
		}
	}
	if entry["id"] != "never-rewrite-changelog" {
		t.Fatalf("id = %v, want never-rewrite-changelog", entry["id"])
	}
	onlyIn, ok := entry["only_in"].([]any)
	if !ok {
		t.Fatalf("only_in = %v (%T), want an array after Normalize", entry["only_in"], entry["only_in"])
	}
	if len(onlyIn) != 0 {
		t.Fatalf("only_in = %v, want empty array", onlyIn)
	}
}
