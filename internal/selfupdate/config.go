package selfupdate

import (
	"net/http"
	"runtime"
	"time"
)

// DefaultRepo is the GitHub repo pawl release binaries are published under
// (.goreleaser.yaml, .github/workflows/release.yml, install.sh's
// PAWL_REPO).
const DefaultRepo = "dcferreira/agent-pawl"

// DefaultAPIBase and DefaultDownloadBase are GitHub's own API and web
// hosts. Overridable so tests can point them at an httptest.Server instead
// of the real network.
const (
	DefaultAPIBase      = "https://api.github.com"
	DefaultDownloadBase = "https://github.com"
)

// DefaultClientTimeout bounds a whole request (dial + TLS + headers +
// body), not just dialing: http.DefaultClient has no timeout at all, so a
// stalled connection (a hung TCP session, a server that accepts but never
// responds) would otherwise leave `pawl update` hanging indefinitely. Five
// minutes is generous for a release archive on a slow connection while
// still guaranteeing the command eventually gives up.
const DefaultClientTimeout = 5 * time.Minute

// defaultClient is the package-level http.Client WithDefaults falls back
// to when the caller doesn't inject one — built once, not per call, since
// an http.Client is meant to be reused (it pools connections).
var defaultClient = &http.Client{Timeout: DefaultClientTimeout}

// Config holds everything selfupdate needs that isn't a pure function of
// its inputs: where to fetch from, what platform to fetch for, which
// binary to replace, and what HTTP client to use. Every field is
// injectable so tests never touch the real network or a real installed
// pawl binary — see internal/cli/update.go's seam, which passes a *Config
// with APIBase/DownloadBase pointed at an httptest.Server and ExePath
// inside t.TempDir().
type Config struct {
	Client       *http.Client
	APIBase      string
	DownloadBase string
	Repo         string
	GOOS         string
	GOARCH       string
	// ExePath is the binary AtomicReplace writes to. Callers must set this
	// explicitly — WithDefaults does not default it to the running
	// binary's own path, since resolving that (os.Executable +
	// EvalSymlinks) is the CLI layer's job, not this package's (so that a
	// test can inject a path without ever calling os.Executable at all).
	ExePath string
}

// WithDefaults returns a copy of c with every zero-valued field but
// ExePath filled in from the package defaults / runtime.GOOS/GOARCH.
func (c Config) WithDefaults() Config {
	if c.Client == nil {
		c.Client = defaultClient
	}
	if c.APIBase == "" {
		c.APIBase = DefaultAPIBase
	}
	if c.DownloadBase == "" {
		c.DownloadBase = DefaultDownloadBase
	}
	if c.Repo == "" {
		c.Repo = DefaultRepo
	}
	if c.GOOS == "" {
		c.GOOS = runtime.GOOS
	}
	if c.GOARCH == "" {
		c.GOARCH = runtime.GOARCH
	}
	return c
}
