package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// slugHashLen is the number of hex characters of root's sha256 digest kept
// in Slug's output. Short enough to stay readable, long enough that two
// distinct roots colliding on it is not a practical concern.
const slugHashLen = 8

// ResolveRoot resolves the working-copy root for cwd by walking up from it
// looking for a directory that contains a ".git" or ".jj" entry — never by
// string-manipulating cwd (DESIGN.md §5), and never by shelling out to jj or
// git: the marker's mere existence is what a working copy *is*, for both a
// git worktree and a jj workspace, so no subprocess is needed to answer this
// question and this package has no runtime dependency on either binary. The
// nearest marker wins: for a repo nested inside another repo, the inner
// marker stops the walk first. If no ancestor (including the filesystem
// root) has a marker, ResolveRoot falls back to cwd itself. This is the
// single algorithm both the runtime and (in a later build) the enforcement
// hooks must call, so that the two sides can never derive identity
// differently.
//
// The marker entry may be a directory (the common case) or a plain file: in
// a git worktree and in a git submodule, ".git" is a file containing a
// "gitdir: ..." pointer, not a directory. ResolveRoot only tests for the
// entry's existence, never for it being a directory, or both shapes would
// silently fall through to the wrong ancestor.
//
// One case is a deliberate divergence from asking git directly: with
// GIT_WORK_TREE/GIT_DIR set in the environment, `git rev-parse
// --show-toplevel` reports the overridden work tree, while this walk always
// reports the physical directory containing the marker. That is fine for
// this package's two uses (namespacing run state and bounding workflow
// discovery) and arguably more correct for them, so it is accepted rather
// than emulated.
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
//
// Practical consequence of the fallback: a directory with no ".git" or
// ".jj" anywhere above it has no stable root at all — every subdirectory
// resolves to itself, not to some shared ancestor. Since workflow discovery
// walks up from this root looking for ".claude/workflows/", and run state is
// namespaced under Slug(root), that means pawl run/list/status stop seeing
// each other's workflows and runs as soon as you're one level below where
// they were invoked. This is a known, accepted limitation of not shelling
// out to a VCS — see docs/troubleshooting.md — not a bug in the walk above.
func ResolveRoot(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("journal: resolving root: %w", err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("journal: resolving root: %w", err)
	}

	dir := real
	for {
		if hasMarker(dir) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return real, nil
}

// hasMarker reports whether dir directly contains a ".git" or ".jj" entry,
// regardless of whether that entry is a directory or a plain file (the
// worktree/submodule shape for ".git").
func hasMarker(dir string) bool {
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if _, err := os.Lstat(filepath.Join(dir, ".jj")); err == nil {
		return true
	}
	return false
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
