package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/dcferreira/agentic-workflow-fsm/internal/journal"
)

// cmdList implements wf list (design/format-spec.md §I): every resolvable
// workflow name and the source it would resolve from — repo-local
// .claude/workflows/ at the working-copy root wins over the user-level
// ~/.claude/workflows/ for the same name, matching resolveWorkflowFile.
//
// Every printed entry goes through a blockWriter (fix round 5): a workflow
// file's name comes from the filesystem, which — on Linux — permits a
// filename containing \n, \r or U+2028; wf list used to print it via a bare
// Fprintf, so such a filename could put an instruction-shaped line at
// column 0 in wf list's own output.
func cmdList(args []string, cwd string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: wf list")
		return 2
	}

	root, err := journal.ResolveRoot(cwd)
	if err != nil {
		printLine(stderr, err.Error())
		return 1
	}

	type entry struct {
		name, source, path string
	}
	seen := map[string]bool{}
	var entries []entry

	repoDir := filepath.Join(root, ".claude", "workflows")
	for _, name := range listYAMLBaseNames(repoDir) {
		seen[name] = true
		entries = append(entries, entry{name, "repo-local", filepath.Join(repoDir, name+".yaml")})
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		userDir := filepath.Join(home, ".claude", "workflows")
		for _, name := range listYAMLBaseNames(userDir) {
			if seen[name] {
				continue
			}
			entries = append(entries, entry{name, "user", filepath.Join(userDir, name+".yaml")})
		}
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	w := &blockWriter{}
	for _, e := range entries {
		w.line(0, fmt.Sprintf("%s\t%s\t%s", e.name, e.source, e.path))
	}
	fmt.Fprint(stdout, w.String())
	return 0
}
