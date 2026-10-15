#!/usr/bin/env bash
# harden.sh - write the sandbox policy and the claude.ai-connector lockdown (see PROTOCOL.md, "Account side").
#
#   bash scripts/nonauthor-test/harden.sh <ROOT>
#
# Idempotent; safe to run on an existing sandbox (e.g. after the human login). Merges into
# $ROOT/claude-config/settings.json with jq, keeping every existing key (enabledPlugins, ...):
#   - permissions.deny += WebFetch, WebSearch (no repo browsing) and SendMessage, ListAgents,
#     RemoteTrigger (built-in tools that reach the logged-in account's other sessions); deduplicated;
#   - disableClaudeAiConnectors = true (documented setting, turns off the account's claude.ai connectors);
#   - syncClaudeAiPlugins = false (documented, https://code.claude.com/docs/en/plugins/loading: stops loading plugins
#     synced from the claude.ai account, org plugins and their MCP servers included);
#   - env.DISABLE_AUTOUPDATER = "1", skipDangerousModePermissionPrompt = true.
# It also seeds projects["$ROOT/home/textstats"].hasTrustDialogAccepted = true in $CFG/.claude.json (the
# documented by-hand trust, https://code.claude.com/docs/en/permissions "What runs before you trust a folder";
# trust for $HOME is session-only and never persisted, so the tester starts in ~/textstats).
# enter.sh adds the other half: ENABLE_CLAUDEAI_MCP_SERVERS=false in the sandbox environment.
set -euo pipefail
[ $# -eq 1 ] || { echo "usage: harden.sh <ROOT>" >&2; exit 2; }
ROOT="$(cd "$1" && pwd -P)" || { echo "harden.sh: no such ROOT: $1" >&2; exit 2; }
command -v jq >/dev/null || { echo "harden.sh: jq is required" >&2; exit 2; }
CFG="$ROOT/claude-config"
[ -d "$CFG" ] || { echo "harden.sh: $CFG missing (not a prep.sh sandbox)" >&2; exit 2; }

sf="$CFG/settings.json"
[ -f "$sf" ] || echo '{}' >"$sf"
jq '.permissions.deny = ((.permissions.deny // [])
        + ["WebFetch", "WebSearch", "SendMessage", "ListAgents", "RemoteTrigger"]
        | unique)
    | .env.DISABLE_AUTOUPDATER = "1"
    | .skipDangerousModePermissionPrompt = true
    | .disableClaudeAiConnectors = true
    | .syncClaudeAiPlugins = false' "$sf" >"$sf.new"
mv "$sf.new" "$sf"
echo "harden: $sf updated"

# Seed workspace trust for ~/textstats (keys other than the one flag are preserved; mode stays 600).
cj="$CFG/.claude.json"
proj="$ROOT/home/textstats"
[ -f "$cj" ] || { (umask 077; echo '{}' >"$cj"); }
tmp="$(umask 077; mktemp "$CFG/.claude.json.XXXXXX")"
jq --arg p "$proj" '.projects[$p].hasTrustDialogAccepted = true' "$cj" >"$tmp"
chmod 600 "$tmp"
mv "$tmp" "$cj"
echo "harden: workspace trust seeded for $proj"
