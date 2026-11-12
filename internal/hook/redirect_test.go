package hook

import "testing"

type classifyCase struct {
	cmd  string
	want bool
}

// Redirects are not arguments (issue #99): `git tag --contains HEAD
// 2>/dev/null` is a listing, so it must classify like the same command
// without the redirect. The lexer drops an unquoted redirect and its target
// at lex time, because it strips quotes and could not tell a quoted '2>x'
// argument from a redirect afterwards.
func TestIsVCSMutation_Redirects(t *testing.T) {
	cases := []classifyCase{
		// The reported false positive, in every redirect form.
		{"git tag --contains HEAD 2>/dev/null | head", false},
		{"git tag --contains HEAD", false},
		{"git tag --contains HEAD >/dev/null", false},
		{"git tag --contains HEAD 2>/dev/null", false},
		{"git tag --contains HEAD 2>&1", false},
		{"git tag --contains HEAD >&2", false},
		{"git tag --contains HEAD &>/dev/null", false},
		{"git tag --contains HEAD &>>log", false},
		{"git tag --contains HEAD >>log", false},
		{"git tag --contains HEAD >| out", false},
		{"git tag --contains HEAD > out.txt", false},
		{"git tag --contains HEAD 2> /dev/null", false},
		{"git tag --contains HEAD 2>> log", false},
		{"git tag --contains HEAD < /dev/null", false},
		{"git tag --contains HEAD <&0", false},
		{"git tag --contains HEAD <>f", false},
		{"git tag --contains HEAD 1>&2 2>/dev/null", false},
		{">/dev/null git tag --contains HEAD", false},
		{"git tag -l 'v*' 2>/dev/null", false},
		{"git tag 2>/dev/null", false},
		// Other read-only commands with redirects stay read-only.
		{"git status 2>/dev/null", false},
		{"git log --oneline >/dev/null", false},
		{"git describe --tags 2>&1", false},
		{"git rev-parse HEAD 2>/dev/null", false},
		{"git diff 2>&1 | head", false},
		{"jj log 2>/dev/null", false},
		{"jj status 2>&1", false},
		// A redirect never hides a mutation.
		{"git commit -m x 2>&1", true},
		{"git commit -m x >out", true},
		{">out git push", true},
		{"git push 2>/dev/null", true},
		{"jj new 2>/dev/null", true},
		{"git tag v1 2>/dev/null", true},
		{"git tag -d v1 2>&1", true},
		{"git tag foo>/dev/null", true},
		{"git tag foo 2>/dev/null", true},
		{"git tag foo &>/dev/null", true},
		{"git tag foo&>/dev/null", true},
		{"git branch new 2>&1", true},
		{"git status > ; git push", true},
		{"git status 2>&1; git push", true},
		// Only digits directly attached to the operator are a file
		// descriptor; a quoted or escaped digit is an argument.
		{"git tag 2 >x", true},
		{"git tag 2 > x", true},
		{"git tag '2>x'", true},
		{`git tag "2>x"`, true},
		{`git tag \2>x`, true},
		{"git tag '2'>x", true},
		{"git tag 2 2>x", true},
		// A here-doc or here-string is not a plain redirect: unsure, so the
		// conservative scan sees the git command.
		{"git commit -F - <<EOF\nmsg\nEOF", true},
		{"git commit -F - <<<msg", true},
		{"git commit -F - <<-EOF\nm\nEOF", true},
		// Process substitution is not a redirect.
		{"git tag foo >(cat)", true},
		{"cat <(git commit -m x)", true},
		{"git tag foo 2> >(cat)", true},
	}
	for _, c := range cases {
		if got := IsVCSMutation(c.cmd); got != c.want {
			t.Errorf("IsVCSMutation(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// A redirect with no target cannot be modelled, so the lexer reports unsure
// rather than silently dropping the operator.
func TestLex_RedirectWithoutTargetIsUnsure(t *testing.T) {
	for _, cmd := range []string{"git tag >", "git tag 2>", "git tag foo >>", "git tag foo &>", "git tag foo 2>&", "git status > ; git push"} {
		if _, _, unsure := lex(cmd); !unsure {
			t.Errorf("lex(%q) unsure = false, want true", cmd)
		}
	}
	for _, cmd := range []string{"git tag >f", "git tag > f", "git tag 2>&1", "git tag &>f", "git tag >&2", "git tag >|f", "git tag <&0"} {
		if _, _, unsure := lex(cmd); unsure {
			t.Errorf("lex(%q) unsure = true, want false", cmd)
		}
	}
	if _, subst, _ := lex("git tag foo >(cat)"); !subst {
		t.Error("lex: >( must still be a substitution, not a redirect")
	}
	if segs, _, _ := lex("git tag --contains HEAD 2>/dev/null | head"); len(segs) != 2 || len(segs[0]) != 4 {
		t.Errorf("redirect not dropped from segments: %q", segs)
	}
}

// A shell variable naming pawl, followed by a pawl subcommand, counts as a
// pawl invocation (issue #96); other variables, substitutions and wrappers
// do not, because a false positive fabricates the heartbeat enforcement
// trusts.
func TestIsPawlCommand_ShellVariable(t *testing.T) {
	cases := []classifyCase{
		{`"$PAWL" run wf`, true},
		{`$PAWL run wf`, true},
		{`${PAWL} submit --run r --step s`, true},
		{`"${PAWL}" status`, true},
		{`"$pawl_bin" status`, true},
		{`"${PAWL_BIN}" poll --run r`, true},
		{`"$MY_PAWL" abandon --run r`, true},
		{`cd /r && "$PAWL" run wf`, true},
		{`PAWL_ENFORCEMENT=off "$PAWL" run wf`, true},
		{`PAWL=/x/pawl; "$PAWL" run wf`, true},
		{`"$PAWL" validate wf.yaml 2>&1`, true},
		{`"$PAWL" run wf x=1 | tee log`, true},
		{`for x in a; do "$PAWL" status; done`, true},
		{`$BIN run wf`, false},
		{`"$P" run wf`, false},
		{`$(which pawl) run wf`, false},
		{`echo "$PAWL"`, false},
		{`echo $PAWL run`, false},
		{`"$PAWL"`, false},
		{`"$PAWL" frobnicate`, false},
		{`"$PAWL" --help`, false},
		{`"${PAWL:-pawl}" run wf`, false},
		{`env "$PAWL" run wf`, false},
		{`sh -c '"$PAWL" run wf'`, false},
		{`"$PAWL_DIR"/bin/tool run wf`, false},
		{`echo "$PAWL run"`, false},
	}
	for _, c := range cases {
		if got := IsPawlCommand(c.cmd); got != c.want {
			t.Errorf("IsPawlCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
	// IsPawlOnlyCommand stays strict: a variable could be anything, so it
	// does not earn the broken-guards exemption.
	for _, cmd := range []string{`"$PAWL" run wf`, `${PAWL} abandon --run r`, `cd /r && "$PAWL" status`} {
		if IsPawlOnlyCommand(cmd) {
			t.Errorf("IsPawlOnlyCommand(%q) = true, want false", cmd)
		}
	}
}
