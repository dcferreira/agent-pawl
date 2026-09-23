package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

// Apply downloads the release archive and checksums.txt for tag (a git
// tag such as "v0.3.0"), verifies the archive's sha256 against
// checksums.txt before touching anything on disk, extracts the "pawl"
// binary from it, and atomically replaces cfg.ExePath with it
// (AtomicReplace). cfg should already have WithDefaults applied (the CLI
// layer's job); tag should already be resolved (ResolveTag).
//
// Nothing is written to cfg.ExePath unless every step through checksum
// verification succeeds — a checksum mismatch, a missing checksums.txt
// line, an unsupported platform, or a download failure all leave the
// target binary untouched.
func Apply(cfg Config, tag string) error {
	if err := ValidatePlatform(cfg.GOOS, cfg.GOARCH); err != nil {
		return err
	}
	asset, err := AssetName(tag, cfg.GOOS, cfg.GOARCH)
	if err != nil {
		return err
	}
	base := cfg.DownloadBase + "/" + cfg.Repo + "/releases/download/" + tag

	archive, err := fetch(cfg.Client, base+"/"+asset)
	if err != nil {
		var herr *httpStatusError
		if errors.As(err, &herr) && herr.StatusCode == 404 {
			return fmt.Errorf("selfupdate: release %s not found (or has no %s): %s", tag, asset, herr.Status)
		}
		return fmt.Errorf("selfupdate: downloading %s: %w", asset, err)
	}
	checksums, err := fetch(cfg.Client, base+"/checksums.txt")
	if err != nil {
		return fmt.Errorf("selfupdate: downloading checksums.txt: %w", err)
	}
	expected, err := ChecksumForAsset(checksums, asset)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(archive)
	actual := hex.EncodeToString(sum[:])
	if expected != actual {
		return fmt.Errorf("selfupdate: checksum mismatch for %s: expected %s, got %s", asset, expected, actual)
	}

	content, err := ExtractPawlBinary(archive, DefaultMaxBinarySize)
	if err != nil {
		return err
	}

	if err := AtomicReplace(cfg.ExePath, content); err != nil {
		return err
	}
	return nil
}
