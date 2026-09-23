package selfupdate

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ResolveTag returns the GitHub release tag to install: pin, normalized to
// a leading "v" (NormalizeTag), if non-empty — without any network call,
// since a pin is exact by definition — otherwise the repo's latest release
// tag, fetched from cfg.APIBase.
func ResolveTag(cfg Config, pin string) (string, error) {
	if pin != "" {
		return NormalizeTag(pin), nil
	}
	url := cfg.APIBase + "/repos/" + cfg.Repo + "/releases/latest"
	body, err := fetch(cfg.Client, url)
	if err != nil {
		return "", fmt.Errorf("selfupdate: fetching latest release: %w", err)
	}
	var payload struct {
		TagName string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("selfupdate: parsing latest-release response: %w", err)
	}
	if payload.TagName == "" {
		return "", fmt.Errorf("selfupdate: latest-release response had no tag_name")
	}
	// Same validation validatePin applies to a user-supplied --version:
	// this tag_name is about to be spliced into a download URL path
	// segment too (Apply), so an API response that isn't a clean vX.Y.Z
	// must not reach that far unchecked, whatever produced it.
	if _, err := ParseVersion(payload.TagName); err != nil {
		return "", fmt.Errorf("selfupdate: latest release tag %q is not a vX.Y.Z version", payload.TagName)
	}
	return payload.TagName, nil
}

// httpStatusError is returned by fetch on any non-200 response. Apply
// checks for it with errors.As to turn a 404 on a pinned release's
// archive into a friendlier "release not found" message while keeping the
// underlying HTTP status visible (Error()) for every other caller.
type httpStatusError struct {
	URL        string
	StatusCode int
	Status     string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("GET %s: unexpected status %s", e.URL, e.Status)
}

// maxFetchBodySize caps how much of any single HTTP response fetch will
// read — the release archive is the largest legitimate body, and
// DefaultMaxBinarySize (200MB) already caps the extracted binary itself,
// so 256MB covers a real archive (compressed, always smaller than the
// binary it contains) with headroom; anything past that from a server
// (malicious, compromised, or just serving something unexpected because
// APIBase/DownloadBase got pointed somewhere it shouldn't have) is refused
// rather than read into memory.
const maxFetchBodySize = 256 * 1024 * 1024

// fetch GETs url and returns the response body, or an *httpStatusError
// naming the status code on anything but 200. It is a thin wrapper around
// fetchLimit with the package's real size cap; tests call fetchLimit
// directly with a small limit instead of downloading megabytes to prove
// the cap is enforced.
func fetch(client *http.Client, url string) ([]byte, error) {
	return fetchLimit(client, url, maxFetchBodySize)
}

func fetchLimit(client *http.Client, url string, limit int64) ([]byte, error) {
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{URL: url, StatusCode: resp.StatusCode, Status: resp.Status}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response from %s exceeds %d bytes; refusing to read further", url, limit)
	}
	return body, nil
}
