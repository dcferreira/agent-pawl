package journal

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// shortHash mirrors the digest Slug appends, so the test expresses the
// contract ("readable path + short hash of the full root") rather than
// duplicating Slug's own hash-length choice as a second magic number.
func shortHash(root string) string {
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])[:8]
}

// requireVCSTools, when set to a non-empty value, turns a missing/failing
// VCS tool in mustRun into a hard test failure instead of a skip. Its scope
// is git only: git ships on every runner this project's CI uses, so if it
// ever went missing there we want a loud failure, not a silently reduced
// test suite. jj is not installed in CI (see TestResolveRoot_JJWorkspace,
// which uses optionalRun instead and always skips rather than fails when jj
// is absent). requireVCSTools is deliberately its own variable rather than
// the generic CI (which GitHub Actions sets automatically on every runner):
// a git-only contributor could have CI set in their own shell for unrelated
// reasons, and this project must never require git *or* jj for local
// development — only our own CI workflow sets this explicitly.
const requireVCSTools = "PAWL_REQUIRE_VCS_TOOLS"

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		if os.Getenv(requireVCSTools) != "" {
			t.Fatalf("%s not usable in this environment: %v: %s", name, err, out)
		}
		t.Skipf("%s not usable in this environment: %v: %s", name, err, out)
	}
}

// optionalRun runs an external VCS tool that this project's CI does not
// install (currently: jj). Unlike mustRun, a missing/failing tool here
// always skips the test, regardless of requireVCSTools: jj is not
// guaranteed to be on any runner or contributor machine, so there is no
// environment in which its absence should be a hard failure. The skip
// message is intentionally explicit about what is skipped and why, so a
// reader of CI logs sees the reduced coverage rather than having to infer
// it from a silent pass.
func optionalRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("SKIPPING: %s is not installed/usable in this environment (%v: %s) — this test only verifies that a real jj workspace carries a \".jj\" marker; that jj-specific coverage is reduced here, everything else in this package still runs", name, err, out)
	}
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func TestResolveRoot_NonRepoDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, sub); got != want {
		t.Errorf("ResolveRoot(non-repo subdir) = %q, want %q (cwd itself)", got, want)
	}
}

// TestResolveRoot_NonRepoDirectory_ThroughSymlink is I6: the same directory
// reached directly and through a symlink must resolve to the same identity
// in the non-repo fallback branch, exactly as it already must in the VCS
// branches — otherwise a hook and the runtime could derive different roots
// for what is really one working copy.
func TestResolveRoot_NonRepoDirectory_ThroughSymlink(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not usable in this environment: %v", err)
	}

	direct, err := ResolveRoot(filepath.Join(real, "sub"))
	if err != nil {
		t.Fatalf("ResolveRoot(direct): %v", err)
	}
	viaLink, err := ResolveRoot(filepath.Join(link, "sub"))
	if err != nil {
		t.Fatalf("ResolveRoot(via symlink): %v", err)
	}
	if direct != viaLink {
		t.Errorf("ResolveRoot disagrees across a symlink: direct=%q, via symlink=%q", direct, viaLink)
	}
}

// TestResolveRoot_NonExistentCwd is I6's second half: a cwd that does not
// exist must be a deliberate error, not a silently returned identity for a
// directory that isn't there.
func TestResolveRoot_NonExistentCwd(t *testing.T) {
	_, err := ResolveRoot(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("ResolveRoot: want an error for a non-existent cwd")
	}
}

func TestResolveRoot_GitWorktree(t *testing.T) {
	dir := t.TempDir()
	mustRun(t, dir, "git", "init", "-q")
	mustRun(t, dir, "git", "config", "user.email", "test@example.com")
	mustRun(t, dir, "git", "config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, dir, "git", "add", "f")
	mustRun(t, dir, "git", "commit", "-q", "-m", "init")

	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(git subdir) = %q, want repo root %q", got, want)
	}

	// A git worktree is its own root.
	worktreeDir := filepath.Join(t.TempDir(), "wt")
	mustRun(t, dir, "git", "worktree", "add", "-q", "-b", "wt-branch", worktreeDir)
	wtSub := filepath.Join(worktreeDir, "sub2")
	if err := os.MkdirAll(wtSub, 0o755); err != nil {
		t.Fatal(err)
	}
	wtRoot, err := ResolveRoot(wtSub)
	if err != nil {
		t.Fatalf("ResolveRoot(worktree): %v", err)
	}
	if got, want := wtRoot, realpath(t, worktreeDir); got != want {
		t.Errorf("ResolveRoot(worktree subdir) = %q, want worktree root %q (its own root, not %q)", got, want, realpath(t, dir))
	}
}

func TestResolveRoot_JJWorkspace(t *testing.T) {
	dir := t.TempDir()
	optionalRun(t, dir, "jj", "git", "init")

	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(jj subdir) = %q, want workspace root %q", got, want)
	}
}

func TestResolveRoot_ThisRepo(t *testing.T) {
	// The workspace this package is developed in is itself a jj workspace
	// (no .git). Never run jj/git *mutating* commands against it — this is
	// a read-only root query on the real environment, exercising the exact
	// case DESIGN.md calls out.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := ResolveRoot(wd)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if root == "" {
		t.Fatal("ResolveRoot returned empty root for the real dev workspace")
	}
	if root == wd {
		t.Logf("root == cwd (%s); fine if the test binary runs from the workspace root", root)
	}
}

// The following TestResolveRoot_Marker* tests construct every marker
// scenario with plain filesystem operations only (mkdir/WriteFile) — no jj
// or git binary involved — so they exercise the marker-walk algorithm
// itself, independent of whether any VCS tool is installed.

// TestResolveRoot_MarkerDirectory covers the plain non-worktree repo shape:
// .git is a directory.
func TestResolveRoot_MarkerDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(sub) = %q, want %q", got, want)
	}
}

// TestResolveRoot_MarkerFile covers the git-worktree/submodule shape: .git
// is a regular FILE (containing a "gitdir: ..." pointer), not a directory.
// This is the case the task calls out as the interesting one to verify
// against the pre-change implementation.
func TestResolveRoot_MarkerFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere/.git/worktrees/x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(sub) = %q, want %q (.git-as-a-file worktree/submodule shape)", got, want)
	}
}

// TestResolveRoot_JJMarkerOnly covers the jj-secondary-workspace shape: a
// .jj marker with NO .git alongside it at all. `git rev-parse` has nothing
// to find here, so this case only ever worked before this change because jj
// itself was installed and used as the first fallback.
func TestResolveRoot_JJMarkerOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(sub) = %q, want %q (.jj-only, no .git)", got, want)
	}
}

// TestResolveRoot_BothMarkers covers a colocated git+jj repo: both .git and
// .jj exist in the same directory; either one identifies the same root, so
// the answer is unambiguous.
func TestResolveRoot_BothMarkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(sub) = %q, want %q (both markers present)", got, want)
	}
}

// TestResolveRoot_NearestMarkerWins covers nested repos: an outer repo
// containing an inner repo. The inner repo's root must win for a cwd inside
// it — the walk must stop at the nearest marker, not the outermost one.
func TestResolveRoot_NearestMarkerWins(t *testing.T) {
	outer := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outer, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(outer, "nested")
	if err := os.MkdirAll(filepath.Join(inner, ".jj"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(inner, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, inner); got != want {
		t.Errorf("ResolveRoot(sub) = %q, want inner repo root %q (nearest marker must win over outer %q)", got, want, realpath(t, outer))
	}
}

// TestResolveRoot_DeepSubdirectory covers a marker several levels above
// cwd, to make sure the walk actually climbs multiple levels rather than
// checking only the immediate parent.
func TestResolveRoot_DeepSubdirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(dir, "a", "b", "c", "d")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(deep)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, dir); got != want {
		t.Errorf("ResolveRoot(deep) = %q, want %q", got, want)
	}
}

// TestResolveRoot_MarkerThroughSymlink covers a marker reached via a
// symlinked path component, mirroring
// TestResolveRoot_NonRepoDirectory_ThroughSymlink but for the marker-found
// branch: both paths must resolve to the same identity.
func TestResolveRoot_MarkerThroughSymlink(t *testing.T) {
	real := t.TempDir()
	if err := os.MkdirAll(filepath.Join(real, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks not usable in this environment: %v", err)
	}

	direct, err := ResolveRoot(filepath.Join(real, "sub"))
	if err != nil {
		t.Fatalf("ResolveRoot(direct): %v", err)
	}
	viaLink, err := ResolveRoot(filepath.Join(link, "sub"))
	if err != nil {
		t.Fatalf("ResolveRoot(via symlink): %v", err)
	}
	if direct != viaLink {
		t.Errorf("ResolveRoot disagrees across a symlink: direct=%q, via symlink=%q", direct, viaLink)
	}
	if direct != realpath(t, real) {
		t.Errorf("ResolveRoot(direct) = %q, want %q", direct, realpath(t, real))
	}
}

// TestResolveRoot_NoMarkerAnywhere covers the case where no ancestor,
// including the filesystem root, has a marker: ResolveRoot must fall back
// to cwd itself rather than climbing forever or erroring.
func TestResolveRoot_NoMarkerAnywhere(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "x", "y")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := ResolveRoot(sub)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, want := root, realpath(t, sub); got != want {
		t.Errorf("ResolveRoot(sub) = %q, want %q (no marker anywhere)", got, want)
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		root string
		want string
	}{
		{"/home/user/proj", "-home-user-proj-" + shortHash("/home/user/proj")},
		{"/a/b/c", "-a-b-c-" + shortHash("/a/b/c")},
	}
	for _, tt := range tests {
		if got := Slug(tt.root); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.root, got, tt.want)
		}
	}
}

// TestSlug_NoCollision guards the DESIGN.md §5 failure mode this change
// exists to prevent: two distinct working-copy roots whose naive "/" -> "-"
// replacement collides (a path component boundary vs. a literal "-" in a
// name) must still produce distinct slugs.
func TestSlug_NoCollision(t *testing.T) {
	a := Slug("/home/u/my-proj")
	b := Slug("/home/u/my/proj")
	if a == b {
		t.Fatalf("Slug collision: both %q and %q produced %q", "/home/u/my-proj", "/home/u/my/proj", a)
	}
}

func TestSlug_Deterministic(t *testing.T) {
	root := "/home/user/proj"
	//lint:ignore SA4000 the point of this test is to call Slug twice on the
	// same input and compare the results, to guard against Slug picking up
	// non-deterministic state (e.g. time, randomness); staticcheck can't
	// distinguish that from a copy-paste mistake, but the repetition here is
	// deliberate.
	if Slug(root) != Slug(root) {
		t.Fatalf("Slug(%q) is not deterministic", root)
	}
}
