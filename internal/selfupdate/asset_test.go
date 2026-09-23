package selfupdate

import "testing"

func TestAssetName(t *testing.T) {
	tests := []struct {
		version, goos, goarch string
		wantErr               bool
		want                  string
	}{
		{"0.2.0", "linux", "amd64", false, "pawl_0.2.0_linux_amd64.tar.gz"},
		{"0.2.0", "darwin", "arm64", false, "pawl_0.2.0_darwin_arm64.tar.gz"},
		{"0.2.0", "linux", "arm64", false, "pawl_0.2.0_linux_arm64.tar.gz"},
		{"0.2.0", "darwin", "amd64", false, "pawl_0.2.0_darwin_amd64.tar.gz"},
		{"0.2.0", "windows", "amd64", true, ""},
		{"0.2.0", "linux", "386", true, ""},
	}
	for _, tt := range tests {
		got, err := AssetName(tt.version, tt.goos, tt.goarch)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("AssetName(%q,%q,%q) = %q, want error", tt.version, tt.goos, tt.goarch, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("AssetName(%q,%q,%q) unexpected error: %v", tt.version, tt.goos, tt.goarch, err)
		}
		if got != tt.want {
			t.Errorf("AssetName(%q,%q,%q) = %q, want %q", tt.version, tt.goos, tt.goarch, got, tt.want)
		}
	}
}
