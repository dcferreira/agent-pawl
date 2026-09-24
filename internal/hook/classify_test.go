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
		"git stash list":        false,
		"git stash show":        false,
		"git commit --help":     false,
		"git show HEAD":         false,
		"git rev-parse HEAD":    false,
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
		"jj git remote list":      false,
		"jj config get user.name": false,
		"jj new --help":           false,
		"jj help new":             false,
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
