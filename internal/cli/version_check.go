package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Version is the pawl binary's version, set by cmd/pawl's main from its
// ldflags-stamped Version before any command is dispatched. "dev" is a
// source build.
var Version = "dev"

// envPluginVersion is exported by the plugin's bin/pawl launcher: the
// version in the plugin's own .claude-plugin/plugin.json. The double
// underscore keeps it inside the PAWL__* namespace spec rule 38 reserves, so
// no state:/args: key can export under the same name.
const envPluginVersion = "PAWL__PLUGIN_VERSION"

// checkPluginVersion is pawl run's guard against a binary that does not
// match the plugin whose skill and hooks are driving it. It returns false
// (after printing the refusal to stderr) on a mismatch. Unset or empty
// envPluginVersion means no plugin is involved: no check.
func checkPluginVersion(stderr io.Writer) bool {
	plugin := os.Getenv(envPluginVersion)
	if plugin == "" {
		return true
	}
	if Version == "dev" {
		fmt.Fprintf(stderr, "pawl run: note: source build (pawl dev); skipping the plugin version check (plugin expects %s)\n", plugin)
		return true
	}
	bin := strings.TrimPrefix(Version, "v")
	want := strings.TrimPrefix(plugin, "v")
	if bin == want {
		return true
	}
	fmt.Fprintf(stderr, "pawl run: refusing to start: this pawl binary is %s but the agent-pawl plugin expects %s. "+
		"The plugin's skill and this binary must be the same version. Reinstall the plugin "+
		"(/plugin marketplace update agent-pawl, then reinstall it), or install pawl %s on PATH.\n", bin, want, want)
	return false
}
