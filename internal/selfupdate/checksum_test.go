package selfupdate

import "testing"

const sampleChecksums = `d94e9a27b9c84e320a942ceac6ff0b4c74e6be8c8c1a99f6c3b0f5f0a1b2c3d  pawl_0.2.0_linux_amd64.tar.gz
1111111111111111111111111111111111111111111111111111111111111  pawl_0.2.0_darwin_amd64.tar.gz
`

func TestChecksumForAsset(t *testing.T) {
	got, err := ChecksumForAsset([]byte(sampleChecksums), "pawl_0.2.0_linux_amd64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "d94e9a27b9c84e320a942ceac6ff0b4c74e6be8c8c1a99f6c3b0f5f0a1b2c3d"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestChecksumForAsset_Missing(t *testing.T) {
	_, err := ChecksumForAsset([]byte(sampleChecksums), "pawl_0.2.0_linux_arm64.tar.gz")
	if err == nil {
		t.Fatal("expected error for missing checksum line")
	}
}
