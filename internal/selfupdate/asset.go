package selfupdate

import "fmt"

// supportedPlatforms mirrors .goreleaser.yaml's builds.goos/goarch matrix
// (darwin/linux x amd64/arm64) and install.sh's pawl_os_from_uname /
// pawl_arch_from_uname allow-lists — the only platforms a release archive
// exists for.
var supportedPlatforms = map[string]map[string]bool{
	"darwin": {"amd64": true, "arm64": true},
	"linux":  {"amd64": true, "arm64": true},
}

// ValidatePlatform reports a clear error for any GOOS/GOARCH combination
// this project does not publish a release archive for, instead of letting
// a 404 from GitHub stand in for "unsupported platform."
func ValidatePlatform(goos, goarch string) error {
	archs, ok := supportedPlatforms[goos]
	if !ok || !archs[goarch] {
		return fmt.Errorf("selfupdate: no release binary for %s/%s; pawl release binaries are only published for darwin/linux x amd64/arm64 — build from source instead (docs/install.md)", goos, goarch)
	}
	return nil
}

// AssetName builds the release archive filename goreleaser produces for a
// given version (with or without a leading "v"), GOOS and GOARCH. Must
// match .goreleaser.yaml's archives.name_template exactly:
// pawl_<version-without-v>_<os>_<arch>.tar.gz (mirrors install.sh's
// pawl_asset_name).
func AssetName(version, goos, goarch string) (string, error) {
	if err := ValidatePlatform(goos, goarch); err != nil {
		return "", err
	}
	return fmt.Sprintf("pawl_%s_%s_%s.tar.gz", VersionNoV(version), goos, goarch), nil
}
