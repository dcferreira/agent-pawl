package journal

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func mustRun(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
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
		{"/home/user/proj", "-home-user-proj"},
		{"/a/b/c", "-a-b-c"},
	}
	for _, tt := range tests {
		if got := Slug(tt.root); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.root, got, tt.want)
		}
	}
}
