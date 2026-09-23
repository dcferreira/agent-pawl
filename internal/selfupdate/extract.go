package selfupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
)

// DefaultMaxBinarySize caps how much of a "pawl" tar entry ExtractPawlBinary
// will read, guarding against a decompression bomb hiding behind a
// dishonest tar header (a Header.Size a malicious or corrupt archive could
// lie about). 200MB is generously above any real pawl binary (a few tens
// of MB at most).
const DefaultMaxBinarySize = 200 * 1024 * 1024

// ExtractPawlBinary reads a gzip-compressed tar archive (as
// .goreleaser.yaml's archives block produces: a single top-level regular
// file named "pawl") and returns that file's contents. It reads at most
// maxSize bytes of the entry's content — regardless of what the tar header
// claims — so a hostile or corrupt archive can't be used to exhaust memory
// via a decompression bomb.
func ExtractPawlBinary(archive []byte, maxSize int64) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("selfupdate: reading archive: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("selfupdate: reading archive: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Name != "pawl" {
			continue
		}
		limited := io.LimitReader(tr, maxSize+1)
		content, err := io.ReadAll(limited)
		if err != nil {
			return nil, fmt.Errorf("selfupdate: reading %q from archive: %w", hdr.Name, err)
		}
		if int64(len(content)) > maxSize {
			return nil, fmt.Errorf("selfupdate: %q in archive exceeds %d bytes; refusing to extract", hdr.Name, maxSize)
		}
		return content, nil
	}
	return nil, fmt.Errorf("selfupdate: archive has no regular file named %q", "pawl")
}
