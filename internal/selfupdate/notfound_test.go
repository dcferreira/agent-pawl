package selfupdate

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestApply_PinnedReleaseNotFound covers a pinned --version whose release
// doesn't exist (or doesn't ship this platform's asset): the archive
// download 404s. Apply should return a friendly error naming the release
// and the asset, not just a bare "unexpected status 404 Not Found".
func TestApply_PinnedReleaseNotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := Config{
		Client:       srv.Client(),
		APIBase:      srv.URL,
		DownloadBase: srv.URL,
		Repo:         DefaultRepo,
		GOOS:         "linux",
		GOARCH:       "amd64",
		ExePath:      filepath.Join(t.TempDir(), "pawl"),
	}.WithDefaults()

	err := Apply(cfg, "v9.9.9")
	if err == nil {
		t.Fatal("expected an error for a nonexistent release")
	}
	msg := err.Error()
	if !strings.Contains(msg, "v9.9.9") {
		t.Errorf("error should name the release tag: %q", msg)
	}
	if !strings.Contains(msg, "not found") {
		t.Errorf("error should say not found: %q", msg)
	}
	if !strings.Contains(msg, "404") {
		t.Errorf("error should keep the HTTP status: %q", msg)
	}
	asset, aerr := AssetName("v9.9.9", "linux", "amd64")
	if aerr != nil {
		t.Fatal(aerr)
	}
	if !strings.Contains(msg, asset) {
		t.Errorf("error should name the asset: %q (asset %q)", msg, asset)
	}
}
