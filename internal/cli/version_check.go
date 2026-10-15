package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dcferreira/agent-pawl/internal/render"
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

// envLauncher is exported by the plugin's bin/pawl launcher: its own fully
// resolved absolute path. Claude Code appends a plugin's bin/ to the END of
// PATH, so a `pawl` earlier on PATH can shadow the launcher; the instruction
// lines pawl prints for the driving agent to run immediately (submit with:,
// or:, poll with:) name this path instead, so they run the plugin's launcher.
const envLauncher = "PAWL__LAUNCHER"

// pawlCommand is the command name for those instruction lines: the shell-quoted
// PAWL__LAUNCHER when it is an absolute path to an existing regular file,
// otherwise plain "pawl". Never used for TERMINAL's resume:/abandon: lines,
// which a person may read later, when an absolute path could have gone stale.
func pawlCommand() string {
	p := os.Getenv(envLauncher)
	if p == "" || !filepath.IsAbs(p) {
		return "pawl"
	}
	if fi, err := os.Stat(p); err != nil || !fi.Mode().IsRegular() {
		return "pawl"
	}
	return render.ShellQuote(p)
}

// checkPluginVersion is pawl run's guard against a binary that does not
// match the plugin whose skill and hooks are driving it. It returns false
// (after printing the refusal to stderr) on a mismatch.
//
// hookVersion is the plugin version recorded in the PreToolUse heartbeat
// that vouched for this invocation ("" when enforcement is off or the
// heartbeat predates the field). The hook always runs through the plugin's
// own launcher, so it is the version of the plugin whose hooks are bound,
// and it is authoritative over the PAWL__PLUGIN_VERSION environment: a
// stale plugin's launcher earlier on PATH exports its own stale version
// there. With no hookVersion the environment is used alone; with neither,
// no plugin is involved and there is no check.
func checkPluginVersion(stderr io.Writer, hookVersion string) bool {
	env := os.Getenv(envPluginVersion)
	plugin := env
	if hookVersion != "" {
		plugin = hookVersion
	}
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
	where := ""
	if path, err := selfExecutablePath(); err == nil && path != "" {
		where = " (" + path + ")"
	}
	// Each case states only the causes and fixes that are true for it.
	var sources, advice string
	updateFix := fmt.Sprintf("`pawl update --version v%s` for an install.sh install, or `go install github.com/dcferreira/agent-pawl/cmd/pawl@v%s`", want, want)
	switch {
	case hookVersion != "" && env != "" && strings.TrimPrefix(env, "v") != want:
		// The hooks' plugin and the launcher that exported the env are
		// different plugin versions: nothing about the binary on PATH fixes it.
		sources = fmt.Sprintf(" (hooks: %s, launcher env: %s)", want, strings.TrimPrefix(env, "v"))
		advice = "The hooks' version wins: this session's hooks reloaded to a different plugin version than its skill and PATH. Restart Claude Code."
	case hookVersion != "" && env == "":
		advice = "The expected version is the one the plugin hooks active in this session report. " +
			"This happens when the `pawl` that ran is not the plugin's own binary, for example a terminal-installed pawl earlier on PATH or a stale plugin directory inherited on PATH. " +
			"Update or remove that binary: " + updateFix + "; " +
			"or, if the plugin is the older side, run `claude plugin update agent-pawl@agent-pawl` and restart Claude Code."
	default:
		advice = "This happens when the plugin has no bundled binary, as in a source checkout or --plugin-dir, " +
			"and its launcher fell back to a `pawl` on PATH of a different version. " +
			"Fix the binary on PATH: " + updateFix + ", or, in a source checkout, `go install ./cmd/pawl`."
	}
	fmt.Fprintf(stderr, "pawl run: refusing to start: this pawl binary%s is %s but the agent-pawl plugin expects %s%s. "+
		"The plugin's skill and this binary must be the same version. %s\n",
		where, bin, want, sources, advice)
	return false
}

// selfExecutablePath is the running binary's own resolved path, or an error
// when it cannot be determined (the refusal then omits the path). A package
// var so tests can pin it.
var selfExecutablePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}
