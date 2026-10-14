package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// launcherPlatform maps Go's GOOS/GOARCH to the launcher's libexec dir name
// (the same uname mapping bin/pawl uses).
func launcherPlatform(t *testing.T) string {
	t.Helper()
	arch := runtime.GOARCH
	if arch != "amd64" && arch != "arm64" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		t.Skipf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	return runtime.GOOS + "_" + arch
}

// newLauncherPlugin copies bin/pawl into <tmp>/bin and writes pluginJSON (if
// non-nil) at <tmp>/.claude-plugin/plugin.json.
func newLauncherPlugin(t *testing.T, pluginJSON *string) string {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile(t, filepath.Join(repoRoot(t), "bin", "pawl"), filepath.Join(dir, "bin", "pawl"), 0o755)
	if pluginJSON != nil {
		if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), []byte(*pluginJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

const echoVersionScript = "#!/bin/sh\necho \"v=[${PAWL__PLUGIN_VERSION-UNSET}]\"\n"

func runLauncher(t *testing.T, plugin, path string, extraEnv ...string) string {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(plugin, "bin", "pawl"))
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + t.TempDir()}, extraEnv...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launcher failed: %v\n%s", err, out)
	}
	return strings.TrimSpace(string(out))
}

var launcherPluginJSONCases = []struct {
	name string
	json *string
	want string
}{
	{"pretty", sp("{\n  \"name\": \"agent-pawl\",\n  \"version\": \"0.7.0\",\n  \"license\": \"MIT\"\n}\n"), "0.7.0"},
	{"compact", sp(`{"name":"agent-pawl","version":"1.2.3-rc.1"}`), "1.2.3-rc.1"},
	{"odd whitespace", sp("{\n\t\"version\"  \t:   \"0.7.0\"\n}\n"), "0.7.0"},
	{"missing file", nil, "UNSET"},
	{"garbled", sp("\x00\x01 not json {{{"), "UNSET"},
	{"no version key", sp(`{"name":"agent-pawl"}`), "UNSET"},
	{"empty version", sp(`{"version":""}`), "UNSET"},
}

func sp(s string) *string { return &s }

func TestPawlLauncher_ExportsPluginVersion_Libexec(t *testing.T) {
	plat := launcherPlatform(t)
	for _, tc := range launcherPluginJSONCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newLauncherPlugin(t, tc.json)
			bundled := filepath.Join(plugin, "libexec", plat)
			if err := os.MkdirAll(bundled, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(bundled, "pawl"), []byte(echoVersionScript), 0o755); err != nil {
				t.Fatal(err)
			}
			got := runLauncher(t, plugin, "/usr/bin:/bin")
			if want := "v=[" + tc.want + "]"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestPawlLauncher_ExportsPluginVersion_PathFallback(t *testing.T) {
	launcherPlatform(t)
	for _, tc := range launcherPluginJSONCases {
		t.Run(tc.name, func(t *testing.T) {
			plugin := newLauncherPlugin(t, tc.json) // no libexec/: PATH branch
			pathDir := t.TempDir()
			if err := os.WriteFile(filepath.Join(pathDir, "pawl"), []byte(echoVersionScript), 0o755); err != nil {
				t.Fatal(err)
			}
			got := runLauncher(t, plugin, filepath.Join(plugin, "bin")+":"+pathDir+":/usr/bin:/bin")
			if want := "v=[" + tc.want + "]"; got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// A value inherited from an outer environment must not survive a plugin
// whose version cannot be read.
func TestPawlLauncher_UnreadableVersionClearsInherited(t *testing.T) {
	plat := launcherPlatform(t)
	plugin := newLauncherPlugin(t, nil)
	bundled := filepath.Join(plugin, "libexec", plat)
	if err := os.MkdirAll(bundled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bundled, "pawl"), []byte(echoVersionScript), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := runLauncher(t, plugin, "/usr/bin:/bin", "PAWL__PLUGIN_VERSION=stale"); got != "v=[UNSET]" {
		t.Errorf("got %q", got)
	}
}
