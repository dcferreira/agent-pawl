package selfupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAtomicReplace_WritesContentAndMode(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pawl")
	if err := os.WriteFile(exe, []byte("old-content"), 0755); err != nil {
		t.Fatal(err)
	}

	if err := AtomicReplace(exe, []byte("new-content")); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}

	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-content" {
		t.Errorf("content = %q, want %q", got, "new-content")
	}

	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}

	assertNoTempFiles(t, dir)
}

func TestAtomicReplace_PreservesExistingTargetMode(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pawl")
	if err := os.WriteFile(exe, []byte("old-content"), 0750); err != nil {
		t.Fatal(err)
	}

	if err := AtomicReplace(exe, []byte("new-content")); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}

	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0750 {
		t.Errorf("mode = %v, want 0750 (preserved from the existing target)", info.Mode().Perm())
	}
}

func TestAtomicReplace_DefaultsTo0755WhenTargetDoesNotExist(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pawl") // does not exist yet

	if err := AtomicReplace(exe, []byte("new-content")); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}

	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("mode = %v, want 0755 (no existing target to preserve a mode from)", info.Mode().Perm())
	}
}

func TestAtomicReplace_LeavesNoTempFileOnSuccess(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "pawl")
	if err := os.WriteFile(exe, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := AtomicReplace(exe, []byte("new")); err != nil {
		t.Fatalf("AtomicReplace: %v", err)
	}
	assertNoTempFiles(t, dir)
}

func TestAtomicReplace_UnwritableDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits behave differently on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "pawl")
	if err := os.WriteFile(exe, []byte("old"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	err := AtomicReplace(exe, []byte("new"))
	if err == nil {
		t.Fatal("expected an error for an unwritable directory")
	}

	// Restore perms before TempDir cleanup and before reading exe back.
	if chErr := os.Chmod(dir, 0700); chErr != nil {
		t.Fatal(chErr)
	}
	got, rErr := os.ReadFile(exe)
	if rErr != nil {
		t.Fatal(rErr)
	}
	if string(got) != "old" {
		t.Errorf("target was modified despite the write failure: %q", got)
	}
}

func assertNoTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) >= len(".pawl-update-") && e.Name()[:len(".pawl-update-")] == ".pawl-update-" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
