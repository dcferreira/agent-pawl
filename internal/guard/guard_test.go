package guard

import (
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
// normalization actually joins the split line back together — not merely
// that the guard's pattern happens to already match the raw, un-joined
// text. `git push` only appears as a substring once "pu" and "sh" (split by
// the backslash-newline) are rejoined with nothing between them, matching
// what a POSIX shell would actually run. A pattern like `git push.*` would
// pass this test even with normalizeContinuations deleted entirely (a `.*`
// swallows the backslash and newline too), which is why this uses a plain
// literal match instead.
func TestDenied_BackslashNewlineContinuationCaught(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "no-push", Match: "git push", OnlyIn: []string{}},
	}
	command := "git pu\\\nsh origin"
	if got := Denied(guards, nil, command); got == nil || got.ID != "no-push" {
		t.Fatalf("Denied(%q) = %+v, want denied by no-push (backslash-newline continuation deleted, joining \"pu\" and \"sh\" into \"push\")", command, got)
	}
}

// TestDenied_BackslashCarriageReturnNewlineContinuationCaught is the same
// idea for a `\r\n` line ending, and also pins the deletion (not
// space-join) semantics: `gh pr merge [0-9]+` requires exactly one space
// between "merge" and the digits. Joining with a space would leave two
// spaces (the one already before the backslash, plus the inserted one) and
// the pattern would not match; deleting the backslash-newline pair
// entirely — matching what a POSIX shell does — leaves exactly one.
func TestDenied_BackslashCarriageReturnNewlineContinuationCaught(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "merge-guard", Match: "gh pr merge [0-9]+", OnlyIn: []string{}},
	}
	command := "gh pr merge \\\r\n41"
	if got := Denied(guards, nil, command); got == nil || got.ID != "merge-guard" {
		t.Fatalf("Denied(%q) = %+v, want denied by merge-guard (\\r\\n continuation deleted, joining \"merge \" and \"41\")", command, got)
	}
}

// TestDenied_BareNewlineNotJoined is the negative case: a newline with no
// preceding backslash is not a line continuation and must not be deleted.
// `merge [0-9]+` requires "merge" immediately followed by a space and
// digits; "gh pr merge\n41" has a newline, not a space, in that position,
// so it must not be denied.
func TestDenied_BareNewlineNotJoined(t *testing.T) {
	guards := []spec.GuardDecl{
		{ID: "merge-guard", Match: "merge [0-9]+", OnlyIn: []string{}},
	}
	command := "gh pr merge\n41"
	if got := Denied(guards, nil, command); got != nil {
		t.Fatalf("Denied(%q) = %+v, want nil (bare newline is not a continuation, must not be joined)", command, got)
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
