package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// slugHashLen is the number of hex characters of root's sha256 digest kept
// in Slug's output. Short enough to stay readable, long enough that two
// distinct roots colliding on it is not a practical concern.
const slugHashLen = 8

// ResolveRoot resolves the working-copy root for cwd by asking the VCS —
// `jj workspace root`, else `git rev-parse --show-toplevel`, else cwd
// itself — never by string-manipulating cwd (DESIGN.md §5). A jj workspace
// or a git worktree is its own root. This is the single algorithm both the
// runtime and (in a later build) the enforcement hooks must call, so that
// the two sides can never derive identity differently.
//
// Every branch, including the non-repo fallback, resolves symlinks before
// returning: the same directory reached two ways (once through a symlink,
// once directly) must resolve to the same identity, or a hook and the
// runtime could derive different roots for what is really one working copy —
// exactly the DESIGN.md §5 failure this function exists to prevent.
//
// A cwd that does not exist is an error: there is no sensible identity to
// return for it, and returning one anyway (silently) is the failure mode
// this whole function is designed against.
func ResolveRoot(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("journal: resolving root: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return "", fmt.Errorf("journal: resolving root: %w", err)
	}
	if root, ok := vcsRoot(abs, "jj", "workspace", "root"); ok {
		return root, nil
	}
	if root, ok := vcsRoot(abs, "git", "rev-parse", "--show-toplevel"); ok {
		return root, nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("journal: resolving root: %w", err)
	}
	return real, nil
}

// vcsRoot runs a VCS root-query command with dir as its working directory
// and reports the resolved, symlink-free absolute root, or ok=false if the
// command failed (not that VCS, or not inside a working copy of it).
func vcsRoot(dir, name string, args ...string) (string, bool) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", false
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	abs, err := filepath.Abs(real)
	if err != nil {
		return "", false
	}
	return abs, true
}

// Slug returns the run-directory namespace for a working copy (DESIGN.md
// §4): root with each path separator replaced by "-", followed by a short
// hex digest of the full root, e.g. "-home-user-proj-7d73bf4f". The readable
// prefix alone is not injective — "/home/u/my-proj" and "/home/u/my/proj"
// both naively become "-home-u-my-proj" — so the digest suffix is load-
// bearing, not decorative: it is what keeps distinct roots from sharing a
// run namespace (and, once hooks exist, a guard-lookup namespace). This is
// the single function both the runtime and any future hook must call for
// this identity (DESIGN.md §5) — never reimplement the naive replacement or
// the hash elsewhere.
func Slug(root string) string {
	naive := strings.ReplaceAll(root, string(filepath.Separator), "-")
	sum := sha256.Sum256([]byte(root))
	return naive + "-" + hex.EncodeToString(sum[:])[:slugHashLen]
}
