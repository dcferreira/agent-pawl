# scripts/e2e-install

Manual install/update verification harness (not run in CI, no tests of its own).
Each scenario installs pawl into a throwaway sandbox and records a transcript.

    bash scripts/e2e-install/run.sh [a b c e f ...]   # default: every scenario-*.sh present

Environment: `FROM` (0.7.0), `TO` (0.8.0), `SOURCE=live|local`, `REPORT_DIR`
(default: a new mktemp dir, printed), `KEEP=1` (keep sandboxes), `E2E_TMP`
(base dir for sandboxes, default `${TMPDIR:-/tmp}`).

`SOURCE=local` is for release candidates, which never reach the live
marketplace: a directory marketplace is built from the TO release's own
`marketplace.json` and plugin zip (sha256 verified).

Output: `$REPORT_DIR/summary.md` (one row per check: PASS/FAIL/SKIP) and
`$REPORT_DIR/<scenario>.transcript.md` (every command, exit code, stdout and
stderr verbatim, plus `claude --version`). `run.sh` exits non-zero if any
scenario had a FAIL.

Scenarios: `a` fresh marketplace install + green-tests through the installed
launcher; `b` install.sh, go install, `pawl update`, plugin-managed refusal;
`c` plugin update FROM->TO via the claude CLI (a sandbox git-repo marketplace
is advanced from the FROM to the TO release `marketplace.json`; records
installed_plugins.json/settings, whether the old cache dir is kept, and what
`marketplace remove` does, all with the claude CLI version); `e` mid-run
upgrade: a run started under FROM blocks on a human step, the plugin is updated,
the FROM plugin dir is deleted, and the harness records verbatim what the stale
launcher/hooks print, then finishes the run through the TO launcher; `f`
plugin/binary version-mismatch UX (a source checkout as plugin dir with a
different-version `pawl` on PATH, both directions, plus synthetic plugin.json
edits to exercise the check TO has).

Releases before the one that added a check cannot enforce it: FROM (0.7.0) has
no PAWL__PLUGIN_VERSION export and no version check, so F records that honestly
instead of expecting a refusal.

Safety: every scenario gets its own `mktemp -d` root with its own HOME,
CLAUDE_CONFIG_DIR, GOPATH and PATH; commands run under `env -i`; the real
`~/.claude` and installed pawl are never touched; cleanup is
`rm -rf "$ROOT"` of that root only. Needs network (github.com,
proxy.golang.org), curl, jq, unzip, tar, git, go and claude. See the header of
`lib.sh` for the helper API used by scenarios.
