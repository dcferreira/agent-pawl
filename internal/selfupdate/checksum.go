package selfupdate

import (
	"bufio"
	"bytes"
	"fmt"
)

// ChecksumForAsset looks up the sha256 for filename in a checksums.txt
// file's contents (goreleaser's default `checksum` block format:
// "<sha256>  <filename>" per line — mirrors install.sh's
// pawl_checksum_for_file). Returns an error if the filename isn't present,
// so a caller never silently proceeds without something to verify against.
func ChecksumForAsset(checksums []byte, filename string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(checksums))
	for sc.Scan() {
		fields := bytes.Fields(sc.Bytes())
		if len(fields) != 2 {
			continue
		}
		if string(fields[1]) == filename {
			return string(fields[0]), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("selfupdate: reading checksums.txt: %w", err)
	}
	return "", fmt.Errorf("selfupdate: no checksum found for %q in checksums.txt", filename)
}
