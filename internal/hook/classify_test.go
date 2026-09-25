package hook

import "testing"

func TestIsPawlCommand(t *testing.T) {
	cases := map[string]bool{
		"pawl run green-tests":              true,
		"  pawl submit --run ab12 --step x": true,
		"./dist/pawl run x":                 true,
		"/home/u/go/bin/pawl status":        true,
		"cd sub && pawl run x":              true,
		"PAWL_ENFORCEMENT=off pawl run x":   true,
		"echo pawl":                         false,
		"pawlish run":                       false,
		"grep -r pawl .":                    false,
		"":                                  false,
		// Separators inside quotes don't start a new segment.
		`echo "x; pawl run y"`:   false,
		`echo 'a | pawl status'`: false,
		`pawl status 2>&1`:       true,
		// Reserved words before a command are stripped.
		"for x in a; do pawl status; done": true,
	}
	for cmd, want := range cases {
		if got := IsPawlCommand(cmd); got != want {
			t.Errorf("IsPawlCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestIsPawlOnlyCommand(t *testing.T) {
	cases := map[string]bool{
		"pawl abandon --run x":              true,
		"pawl status":                       true,
		"cd sub && pawl status":             true,
		"PAWL_ENFORCEMENT=off pawl run x":   true,
		"pawl status; rm -rf x":             false,
		"ls":                                false,
		"pawl status; pawl abandon --run x": true,
		"":                                  false,
		// Separators inside quotes are part of the argument, not new
		// segments (review round 3).
		`pawl submit --run ab12 --step fix --json '{"summary":"bumped dep; did not git push"}'`: true,
		`pawl submit --run ab12 --step fix --json '{"summary":"a & b"}'`:                        true,
		`pawl submit --run ab12 --step fix --json '{"summary":"a | b"}'`:                        true,
		`pawl submit --run ab12 --step fix --json "{\"summary\":\"a; b && c\"}"`:                true,
		`pawl run wf msg="x || y"`: true,
		`pawl run wf msg=a\;b`:     true,
		// fd redirects stay in the current segment.
		"pawl status 2>&1":             true,
		"pawl status >&2":              true,
		"pawl status 1>&2 2>/dev/null": true,
		"pawl status &>/dev/null":      true,
		"pawl status >| out.txt":       true,
		// Real separators after a closed quote still split.
		`pawl run wf msg="a;b"; rm -rf x`: false,
		`pawl status 2>&1 | tee log`:      false,
		`pawl status & rm -rf x`:          false,
		// Command substitution runs an arbitrary command inside a pawl
		// argument, so it is not pawl-only (single quotes suppress it).
		"pawl run wf msg=$(rm -rf x)":     false,
		"pawl run wf msg=\"$(rm -rf x)\"": false,
		"pawl run wf msg=`rm -rf x`":      false,
		"pawl run wf <(rm -rf x)":         false,
		"pawl run wf >(rm -rf x)":         false,
		"pawl run wf msg='$(not run)'":    true,
		"pawl run wf msg='`not run`'":     true,
		// Fail safe: anything the lexer can't confidently parse is never
		// pawl-only (review round 5).
		"pawl status # it's ok\nrm -rf x": false,
		`pawl status $'a\'b' ; rm -rf x`:  false,
		"pawl status # comment":           false,
		"pawl status 'unterminated":       false,
		"pawl status \"unterminated":      false,
		"pawl status <<EOF\nx\nEOF":       false,
		"pawl status <<< x":               false,
		"(pawl status)":                   false,
		"cd $HOME && pawl status":         true,
		`pawl submit --run c47a --step fix --json '{"summary":"don'\''t push"}'`: true,
		"$P status":                false,
		"env pawl status":          false,
		"pawl status \\\n  --json": true,
		`pawl submit --run c47a --step review --json '{"findings":[{"verify_status":"fixed: a # b; c"}]}'`: true,
	}
	for cmd, want := range cases {
		if got := IsPawlOnlyCommand(cmd); got != want {
			t.Errorf("IsPawlOnlyCommand(%q) = %v, want %v", cmd, got, want)
		}
	}
}

func TestIsVCSMutation(t *testing.T) {
	cases := map[string]bool{
		// git mutating
		"git commit -m x":           true,
		"git push origin main":      true,
		"git -C sub commit -am x":   true,
		"git -c user.name=x commit": true,
		"FOO=1 git push":            true,
		"cd x && git commit -m y":   true,
		"git status; git add .":     true,
		"git branch -D old":         true,
		"git branch new-branch":     true,
		"git tag v1":                true,
		"git stash":                 true,
		"git stash pop":             true,
		"git checkout main":         true,
		"/usr/bin/git reset --hard": true,
		// git read-only
		"git status":            false,
		"git log --oneline":     false,
		"git diff HEAD":         false,
		"git status && git log": false,
		"git branch":            false,
		"git branch -a":         false,
		"git branch --list":     false,
		"git tag":               false,
		"git tag -l":            false,
		// Listing/query flags and patterns after -l/--list only read refs,
		// so they must not false-deny a subagent's branch/tag listing.
		"git branch --list 'feat/*'":       false,
		"git branch -l 'feat/*'":           false,
		"git tag --list 'v*'":              false,
		"git tag -l 'v*'":                  false,
		"git branch --show-current":        false,
		"git branch --contains abc12":      false,
		"git branch --no-contains":         false,
		"git branch --merged":              false,
		"git branch --no-merged":           false,
		"git branch --points-at HEAD":      false,
		"git branch --format=%(refname)":   false,
		"git branch --sort=-committerdate": false,
		"git branch --column":              false,
		"git branch --no-column":           false,
		"git branch --color":               false,
		"git branch --no-color":            false,
		"git tag -n5":                      false,
		"git tag -n":                       false,
		"git stash list":                   false,
		"git stash show":                   false,
		"git commit --help":                false,
		"git show HEAD":                    false,
		"git rev-parse HEAD":               false,
		// git is default-deny like jj: anything off the read-only
		// allowlist mutates.
		"git worktree add ../x":                  true,
		"git worktree remove ../x":               true,
		"git update-ref refs/heads/main abc":     true,
		"git symbolic-ref HEAD refs/heads/x":     true,
		"git fetch origin":                       true,
		"git bisect start":                       true,
		"git config user.name x":                 true,
		"git config --unset user.name":           true,
		"git notes add -m x":                     true,
		"git reflog expire --all":                true,
		"git reflog delete HEAD@{1}":             true,
		"git gc --prune=now":                     true,
		"git submodule update":                   true,
		"git remote add o url":                   true,
		"git remote set-url o url":               true,
		"git commit-tree abc -m x":               true,
		"git some-future-subcommand":             true,
		"git -C sub fetch":                       true,
		"git push --help && git fetch":           true,
		"echo $(git fetch)":                      true,
		"echo $(git status) $(git worktree add)": true,
		"echo $(ls --help) && git push $(true)":  true,
		// git read-only allowlist
		"git":                            false,
		"git --version":                  false,
		"git ls-files":                   false,
		"git ls-tree HEAD":               false,
		"git ls-remote origin":           false,
		"git blame x.go":                 false,
		"git grep foo":                   false,
		"git describe --tags":            false,
		"git cat-file -p HEAD":           false,
		"git for-each-ref":               false,
		"git merge-base a b":             false,
		"git shortlog -sn":               false,
		"git name-rev HEAD":              false,
		"git count-objects -v":           false,
		"git help push":                  false,
		"git version":                    false,
		"git config --get user.name":     false,
		"git config --list":              false,
		"git config -l":                  false,
		"git remote":                     false,
		"git remote -v":                  false,
		"git remote show origin":         false,
		"git remote get-url origin":      false,
		"git remote --verbose":           false,
		"git remote -v show origin":      false,
		"git remote -v get-url origin":   false,
		"git remote -v add x y":          true,
		"git remote --verbose remove x":  true,
		"git remote -v -v add x y":       true,
		"git commit -m -h":               true,
		"git commit -h":                  false,
		"git -h":                         false,
		"git push origin --help":         true,
		"git --help":                     false,
		"git worktree list":              false,
		"git reflog":                     false,
		"git reflog show":                false,
		"git -C sub status":              false,
		"echo \"$(git rev-parse HEAD)\"": false,
		// Quoting: separators inside quotes don't split, quotes are
		// removed from words.
		`echo "done; git push"`:     false,
		`echo 'a && jj new'`:        false,
		`git commit -m "fix; typo"`: true,
		`FOO="a b" git push`:        true,
		`"git" push`:                true,
		`git status 2>&1`:           false,
		`git status 2>&1; git push`: true,
		// jj mutating (default-deny)
		"jj new":                    true,
		"jj describe -m x":          true,
		"jj -R . squash":            true,
		"jj --repository . abandon": true,
		"jj git push":               true,
		"jj git fetch":              true,
		"jj bookmark set main":      true,
		"jj op restore abc":         true,
		"jj workspace add ../x":     true,
		"jj file track x":           true,
		// jj read-only
		"jj":                      false,
		"jj log":                  false,
		"jj st":                   false,
		"jj status":               false,
		"jj diff -r @-":           false,
		"jj show":                 false,
		"jj evolog":               false,
		"jj root":                 false,
		"jj file show -r @ x":     false,
		"jj file list":            false,
		"jj op log":               false,
		"jj workspace root":       false,
		"jj workspace list":       false,
		"jj bookmark list":        false,
		"jj tag list":             false,
		"jj tag l":                false,
		"jj git remote list":      false,
		"jj config get user.name": false,
		"jj new --help":           false,
		"jj --help":               false,
		"jj describe -m -h":       true,
		"jj describe -m --help":   true,
		"jj help new":             false,
		// Fail safe (review round 5): a comment, ANSI-C quote, unterminated
		// quote, heredoc, substitution, subshell or wrapper command falls
		// back to a conservative raw token scan.
		"# don't forget to stage\ngit add . && git commit -m x": true,
		"git status # it's fine\ngit push":                      true,
		"echo 'unterminated && git commit -m x":                 true,
		"echo $'a\\'b' ; git commit -m x":                       true,
		"cat <<'EOF'\ndon't\nEOF\ngit commit -m x":              true,
		"echo \"$(git commit -m x)\"":                           true,
		"echo `jj new`":                                         true,
		"(git commit -m x)":                                     true,
		"bash -c 'git commit -m x'":                             true,
		"sudo git push":                                         true,
		"find . -name x -exec git add {} +":                     true,
		"git \\\ncommit -m x":                                   true,
		"{git,commit} -m x":                                     true,
		"G=git; $G commit -m x":                                 true,
		"f() { git commit -m x; }":                              true,
		"# just looking\ngit status":                            false,
		"git log $'a'":                                          false,
		"x=$(git rev-parse HEAD)":                               false,
		"cat <<EOF\nhello\nEOF":                                 false,
		"echo $(jj log -r @)":                                   false,
		// Reserved words and grouping before a command are stripped.
		`for f in a; do git add "$f"; done`:   true,
		"if true; then git commit -m x; fi":   true,
		"if false; then :; else git push; fi": true,
		"{ git commit -m x; }":                true,
		"! git commit -m x":                   true,
		"time git push":                       true,
		"while true; do git status; done":     false,
		// neither
		"go test ./...":   false,
		"echo git commit": false,
		"":                false,
	}
	for cmd, want := range cases {
		if got := IsVCSMutation(cmd); got != want {
			t.Errorf("IsVCSMutation(%q) = %v, want %v", cmd, got, want)
		}
	}
}
