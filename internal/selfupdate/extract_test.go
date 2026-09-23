package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// buildTarGz builds an in-memory tar.gz containing the given files (name ->
// content, mode 0644 regular files unless noted otherwise).
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("WriteHeader: %v", err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip Close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractPawlBinary(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"pawl": "fake-binary-content"})
	got, err := ExtractPawlBinary(archive, 1<<20)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != "fake-binary-content" {
		t.Errorf("got %q, want %q", got, "fake-binary-content")
	}
}

func TestExtractPawlBinary_Missing(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"README.md": "hi"})
	_, err := ExtractPawlBinary(archive, 1<<20)
	if err == nil {
		t.Fatal("expected error when pawl is absent from the archive")
	}
}

func TestExtractPawlBinary_NotAGzip(t *testing.T) {
	_, err := ExtractPawlBinary([]byte("not a gzip stream"), 1<<20)
	if err == nil {
		t.Fatal("expected error for invalid archive")
	}
}

func TestExtractPawlBinary_TooLarge(t *testing.T) {
	archive := buildTarGz(t, map[string]string{"pawl": "0123456789"})
	_, err := ExtractPawlBinary(archive, 5)
	if err == nil {
		t.Fatal("expected error when the pawl entry exceeds maxSize")
	}
}
