package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The skill spells the plugin-launcher path out in exactly one place, and that
// place must carry the fallback for a Claude Code that does not substitute
// ${CLAUDE_SKILL_DIR}: an unsubstituted variable must degrade to plain `pawl`,
// never to "/../../bin/pawl".
func TestSkill_PawlCommandDefinitionAndFallback(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "skills", "pawl", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Collapse line wrapping so the assertions survive a reflow of the prose.
	body := strings.Join(strings.Fields(string(data)), " ")
	const path = "${CLAUDE_SKILL_DIR}/../../bin/pawl"
	if n := strings.Count(body, path); n != 1 {
		t.Errorf("the launcher path must be spelled out exactly once (the definition), found %d", n)
	}
	const fallback = "If that path does not start with `/` (Claude Code left the skill-directory placeholder unsubstituted), use plain `pawl` instead."
	def := strings.Index(body, path)
	fb := strings.Index(body, fallback)
	if fb < 0 {
		t.Fatalf("fallback sentence missing: %q", fallback)
	}
	if def < 0 || fb < def || fb-def > 600 {
		t.Errorf("the fallback must follow the definition directly (definition at %d, fallback at %d)", def, fb)
	}
}
