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
// VCS tool in mustRun (and the symlink probe below) into a hard test
// failure instead of a skip. This is deliberately its own variable rather
// than the generic CI (which GitHub Actions sets automatically on every
// runner): a git-only contributor could have CI set in their own shell for
// unrelated reasons, and this project must never require jj for local
// development. Only our own CI workflow sets this explicitly, once it has
// installed jj itself.
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
	mustRun(t, dir, "jj", "git", "init")

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
