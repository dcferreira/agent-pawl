package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// newFakeGitHub serves a fake releases/latest API endpoint plus fake
// release assets for tag, built from an in-memory tar.gz containing a
// "pawl" file with the given content. It returns the server and the sha256
// of the archive it built (for tests that want to assert against it).
func newFakeGitHub(t *testing.T, tag string, goos, goarch string, pawlContent string) (*httptest.Server, string) {
	t.Helper()
	asset, err := AssetName(tag, goos, goarch)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, map[string]string{"pawl": pawlContent})
	sum := sha256.Sum256(archive)
	sumHex := hex.EncodeToString(sum[:])
	checksums := fmt.Sprintf("%s  %s\n", sumHex, asset)

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"tag_name": %q}`, tag)
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/"+tag+"/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/"+tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, sumHex
}

func testConfig(srv *httptest.Server, exePath string) Config {
	return Config{
		Client:       srv.Client(),
		APIBase:      srv.URL,
		DownloadBase: srv.URL,
		Repo:         DefaultRepo,
		GOOS:         "linux",
		GOARCH:       "amd64",
		ExePath:      exePath,
	}.WithDefaults()
}

func TestResolveTag_Latest(t *testing.T) {
	srv, _ := newFakeGitHub(t, "v0.3.0", "linux", "amd64", "content")
	cfg := testConfig(srv, filepath.Join(t.TempDir(), "pawl"))

	tag, err := ResolveTag(cfg, "")
	if err != nil {
		t.Fatalf("ResolveTag: %v", err)
	}
	if tag != "v0.3.0" {
		t.Errorf("tag = %q, want v0.3.0", tag)
	}
}

func TestResolveTag_PinnedNormalizes(t *testing.T) {
	srv, _ := newFakeGitHub(t, "v0.3.0", "linux", "amd64", "content")
	cfg := testConfig(srv, filepath.Join(t.TempDir(), "pawl"))

	tag, err := ResolveTag(cfg, "0.2.0")
	if err != nil {
		t.Fatalf("ResolveTag: %v", err)
	}
	if tag != "v0.2.0" {
		t.Errorf("tag = %q, want v0.2.0 (pin should not hit the network)", tag)
	}
}

func TestResolveTag_LatestAPIError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := testConfig(srv, filepath.Join(t.TempDir(), "pawl"))

	if _, err := ResolveTag(cfg, ""); err == nil {
		t.Fatal("expected an error for a non-200 API response")
	}
}

func TestResolveTag_MissingTagName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := testConfig(srv, filepath.Join(t.TempDir(), "pawl"))

	if _, err := ResolveTag(cfg, ""); err == nil {
		t.Fatal("expected an error for a response missing tag_name")
	}
}

func TestResolveTag_InvalidTagName(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+DefaultRepo+"/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		// Not a vX.Y.Z tag: a compromised or misconfigured upstream (or a
		// malicious API impersonator hit via a redirected APIBase) could
		// otherwise hand ResolveTag something that later gets spliced
		// straight into a download URL unchecked.
		fmt.Fprint(w, `{"tag_name": "v1.2.3-/../../../other/repo/releases/download/v9.9.9"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	cfg := testConfig(srv, filepath.Join(t.TempDir(), "pawl"))

	_, err := ResolveTag(cfg, "")
	if err == nil {
		t.Fatal("expected an error for a tag_name that isn't a valid vX.Y.Z version")
	}
}

func TestApply_Success(t *testing.T) {
	srv, _ := newFakeGitHub(t, "v0.3.0", "linux", "amd64", "new-pawl-binary")
	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("old-pawl-binary"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(srv, exe)

	if err := Apply(cfg, "v0.3.0"); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new-pawl-binary" {
		t.Errorf("content = %q, want new-pawl-binary", got)
	}
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("mode = %v, want 0755", info.Mode().Perm())
	}
	assertNoTempFiles(t, filepath.Dir(exe))
}

func TestApply_ChecksumMismatchLeavesTargetUntouched(t *testing.T) {
	asset, err := AssetName("v0.3.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, map[string]string{"pawl": "new-content"})
	mux := http.NewServeMux()
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v0.3.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v0.3.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", "0000000000000000000000000000000000000000000000000000000000000000", asset)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("old-content"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(srv, exe)

	err = Apply(cfg, "v0.3.0")
	if err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
	got, rErr := os.ReadFile(exe)
	if rErr != nil {
		t.Fatal(rErr)
	}
	if string(got) != "old-content" {
		t.Errorf("target was modified despite checksum mismatch: %q", got)
	}
	assertNoTempFiles(t, filepath.Dir(exe))
}

func TestApply_MissingChecksumLine(t *testing.T) {
	asset, err := AssetName("v0.3.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, map[string]string{"pawl": "new-content"})
	mux := http.NewServeMux()
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v0.3.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v0.3.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "deadbeef  some_other_file.tar.gz\n")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("old-content"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(srv, exe)

	if err := Apply(cfg, "v0.3.0"); err == nil {
		t.Fatal("expected an error for a missing checksum line")
	}
}

func TestApply_MissingPawlInArchive(t *testing.T) {
	asset, err := AssetName("v0.3.0", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTarGz(t, map[string]string{"README.md": "hi"})
	sum := sha256.Sum256(archive)
	sumHex := hex.EncodeToString(sum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v0.3.0/"+asset, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	mux.HandleFunc("/"+DefaultRepo+"/releases/download/v0.3.0/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", sumHex, asset)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	exe := filepath.Join(t.TempDir(), "pawl")
	if err := os.WriteFile(exe, []byte("old-content"), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(srv, exe)

	if err := Apply(cfg, "v0.3.0"); err == nil {
		t.Fatal("expected an error when the archive has no pawl entry")
	}
	got, rErr := os.ReadFile(exe)
	if rErr != nil {
		t.Fatal(rErr)
	}
	if string(got) != "old-content" {
		t.Errorf("target was modified despite missing pawl entry: %q", got)
	}
}

func TestApply_UnsupportedPlatform(t *testing.T) {
	srv, _ := newFakeGitHub(t, "v0.3.0", "linux", "amd64", "content")
	cfg := testConfig(srv, filepath.Join(t.TempDir(), "pawl"))
	cfg.GOOS = "windows"
	cfg.GOARCH = "amd64"

	if err := Apply(cfg, "v0.3.0"); err == nil {
		t.Fatal("expected an error for an unsupported platform")
	}
}
