package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dcferreira/agent-pawl/internal/selfupdate"
)

// buildTarGz mirrors selfupdate's own test helper (unexported there, so
// duplicated here rather than exported just for a test).
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newUpdateTestServer serves a fake GitHub API + release assets for the
// given latest tag (and, for each of extraTags, its own release too, so a
// rollback test has something else to pin to). Each release's "pawl"
// binary content is "binary-for-<tag>", so a test can assert which one
// ended up installed just by reading the target file back.
func newUpdateTestServer(t *testing.T, latestTag string, extraTags ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+selfupdate.DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name": %q}`, latestTag)
	})
	for _, tag := range append([]string{latestTag}, extraTags...) {
		tag := tag
		asset, err := selfupdate.AssetName(tag, "linux", "amd64")
		if err != nil {
			t.Fatal(err)
		}
		archive := buildTarGz(t, map[string]string{"pawl": "binary-for-" + tag})
		sum := sha256.Sum256(archive)
		sumHex := hex.EncodeToString(sum[:])
		mux.HandleFunc("/"+selfupdate.DefaultRepo+"/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(archive)
		})
		mux.HandleFunc("/"+selfupdate.DefaultRepo+"/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "%s  %s\n", sumHex, asset)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestUpdateConfig(srv *httptest.Server, exePath string) *selfupdate.Config {
	cfg := selfupdate.Config{
		Client:       srv.Client(),
		APIBase:      srv.URL,
		DownloadBase: srv.URL,
		Repo:         selfupdate.DefaultRepo,
		GOOS:         "linux",
		GOARCH:       "amd64",
		ExePath:      exePath,
	}
	return &cfg
}

// newGuardConfig builds a *selfupdate.Config that must never be used: its
// APIBase/DownloadBase point at an httptest.Server that fails the test on
// any request it receives, and ExePath points at a file inside
// t.TempDir() that a test can read back afterwards to confirm nothing was
// ever written there. Every CmdUpdate test that expects a usage error —
// i.e. a refusal that must happen before any network access or filesystem
// write — uses this instead of a nil cfg (which resolves to the real
// GitHub API and the real running binary's own path): with nil, a guard
// regression stays invisible in this sandboxed test run and would only
// surface as `pawl update` actually hitting GitHub, and in the worst case
// overwriting the test binary, the next time someone ran it manually.
func newGuardConfig(t *testing.T) *selfupdate.Config {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected HTTP request reached the network guard: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return newTestUpdateConfig(srv, filepath.Join(t.TempDir(), "pawl"))
}

func TestCmdUpdate_DevBuildRefusesWithoutForce(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("dev-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate(nil, &stdout, &stderr, "dev", cfg)

	if code != 4 {
		t.Errorf("exit = %d, want 4", code)
	}
	if !strings.Contains(stderr.String(), "go install") {
		t.Errorf("stderr should mention go install as an alternative: %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("stderr should mention --force: %q", stderr.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "dev-binary" {
		t.Errorf("dev binary should be untouched, got %q", got)
	}
}

func TestCmdUpdate_DevBuildCheckStillReportsLatest(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--check"}, &stdout, &stderr, "dev", cfg)

	if code != 0 {
		t.Errorf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0.3.0") {
		t.Errorf("stdout should mention latest version: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "dev") {
		t.Errorf("stdout should mention current is dev: %q", stdout.String())
	}
}

func TestCmdUpdate_DevBuildForceUpdates(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("dev-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--force"}, &stdout, &stderr, "dev", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary-for-v0.3.0" {
		t.Errorf("content = %q, want binary-for-v0.3.0", got)
	}
}

func TestCmdUpdate_Check(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--check"}, &stdout, &stderr, "0.2.0", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0.2.0") || !strings.Contains(stdout.String(), "0.3.0") {
		t.Errorf("stdout should mention both versions: %q", stdout.String())
	}
	if _, err := os.Stat(exe); err == nil {
		t.Error("--check must not install anything")
	}
}

func TestCmdUpdate_AlreadyUpToDate(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("current-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate(nil, &stdout, &stderr, "0.3.0", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "up to date") {
		t.Errorf("stdout = %q, want mention of up to date", stdout.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current-binary" {
		t.Error("already-up-to-date should not touch the binary")
	}
}

func TestCmdUpdate_CurrentNewerThanLatest(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("current-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate(nil, &stdout, &stderr, "9.9.9", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "newer") {
		t.Errorf("stdout = %q, want mention of newer", stdout.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current-binary" {
		t.Error("should not downgrade")
	}
}

func TestCmdUpdate_SuccessfulUpdate(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate(nil, &stdout, &stderr, "0.2.0", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "0.2.0 -> 0.3.0") {
		t.Errorf("stdout = %q, want mention of 0.2.0 -> 0.3.0", stdout.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary-for-v0.3.0" {
		t.Errorf("content = %q, want binary-for-v0.3.0", got)
	}
}

func TestCmdUpdate_PinnedVersionRollback(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0", "v0.2.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("old-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--version", "v0.2.0"}, &stdout, &stderr, "0.3.0", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "binary-for-v0.2.0" {
		t.Errorf("content = %q, want binary-for-v0.2.0 (rollback)", got)
	}
}

func TestCmdUpdate_PinnedVersionEqualsCurrentAlreadyOn(t *testing.T) {
	srv := newUpdateTestServer(t, "v0.3.0", "v0.2.0")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("current-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := newTestUpdateConfig(srv, exe)

	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--version", "v0.2.0"}, &stdout, &stderr, "0.2.0", cfg)

	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "already on") {
		t.Errorf("stdout = %q, want mention of already on", stdout.String())
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current-binary" {
		t.Error("should not reinstall when already on the pinned version")
	}
}

func TestCmdUpdate_UnrecognisedFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--bogus"}, &stdout, &stderr, "0.2.0", newGuardConfig(t))

	if code != 2 {
		t.Errorf("exit = %d, want 2", code)
	}
}

func TestCmdUpdate_UsageInMainUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	Run([]string{"pawl"}, &stdout, &stderr)
	if !strings.Contains(stderr.String(), "update") {
		t.Errorf("usage should mention update: %q", stderr.String())
	}
}

// TestCmdUpdate_InvalidPinRejectedBeforeNetwork covers a --version value
// that isn't a clean vX.Y.Z (e.g. path-traversal-shaped input, since it
// ends up spliced into a download URL by selfupdate.Apply): it must be
// refused as a usage error, naming the bad value, without ever reaching
// the network — newGuardConfig fails the test if CmdUpdate makes any HTTP
// request at all, so a pass here proves no network call was attempted,
// not just that the exit code happened to come out right.
func TestCmdUpdate_InvalidPinRejectedBeforeNetwork(t *testing.T) {
	tests := []string{
		"../../x",
		"v1.2.3/../..",
		"not-a-version",
		"1.2.3-x?y=1#frag",
		"+1.+2.+3",
		"01.2.3",
	}
	for _, pin := range tests {
		t.Run(pin, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := CmdUpdate([]string{"--version", pin}, &stdout, &stderr, "0.2.0", newGuardConfig(t))

			if code != 2 {
				t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), pin) {
				t.Errorf("stderr should name the bad value %q: %q", pin, stderr.String())
			}
			if !strings.Contains(stderr.String(), "vX.Y.Z") {
				t.Errorf("stderr should describe the expected form: %q", stderr.String())
			}
		})
	}
}

func TestCmdUpdate_ValidPinWithLeadingVAccepted(t *testing.T) {
	// Sanity check that the new validation doesn't reject legitimate pins
	// (with or without a leading "v") before they ever reach ResolveTag.
	for _, pin := range []string{"0.2.0", "v0.2.0"} {
		if err := validatePin(pin); err != nil {
			t.Errorf("validatePin(%q) = %v, want nil", pin, err)
		}
	}
}

// TestCmdUpdate_EmptyVersionIsUsageError covers the previously-skipped ""
// case: `--version ""` / `--version=` silently meant "install latest,"
// which is surprising for a flag that looks like it's pinning something.
func TestCmdUpdate_EmptyVersionIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"--version", ""},
		{"--version="},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := CmdUpdate(args, &stdout, &stderr, "0.2.0", newGuardConfig(t))
			if code != 2 {
				t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

// TestCmdUpdate_CheckAndVersionMutuallyExclusive covers --check --version:
// silently ignoring the pin under --check would be a confusing surprise
// ("did it check the pin or the latest?"); refuse the combination instead.
func TestCmdUpdate_CheckAndVersionMutuallyExclusive(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := CmdUpdate([]string{"--check", "--version", "v0.2.0"}, &stdout, &stderr, "0.2.0", newGuardConfig(t))
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestCmdUpdate_DevRefusalAndCheckDoNotResolveExePath proves the
// dev-build refusal and --check paths never need (and never attempt) to
// resolve the running binary's own path: overriding osExecutable to fail
// must not turn either of them into an unrelated exit-5 "couldn't locate
// the running binary" error, since CmdUpdate only calls resolveExePath
// from the branches that actually write to the binary.
func TestCmdUpdate_DevRefusalAndCheckDoNotResolveExePath(t *testing.T) {
	orig := osExecutable
	osExecutable = func() (string, error) { return "", fmt.Errorf("boom: no /proc/self/exe here") }
	t.Cleanup(func() { osExecutable = orig })

	srv := newUpdateTestServer(t, "v0.3.0")
	cfg := &selfupdate.Config{
		Client:       srv.Client(),
		APIBase:      srv.URL,
		DownloadBase: srv.URL,
		Repo:         selfupdate.DefaultRepo,
		GOOS:         "linux",
		GOARCH:       "amd64",
		// ExePath deliberately left empty, forcing CmdUpdate down the
		// osExecutable path if it takes it at all.
	}

	t.Run("dev refusal", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := CmdUpdate(nil, &stdout, &stderr, "dev", cfg)
		if code != 4 {
			t.Errorf("exit = %d, want 4; stderr=%q", code, stderr.String())
		}
	})

	t.Run("--check", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := CmdUpdate([]string{"--check"}, &stdout, &stderr, "0.2.0", cfg)
		if code != 0 {
			t.Errorf("exit = %d, want 0; stderr=%q", code, stderr.String())
		}
	})
}
