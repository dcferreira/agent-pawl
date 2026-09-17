#!/usr/bin/env bash
# Write the worker note (frontmatter + empty sections) from WF_* env vars.
set -euo pipefail
mkdir -p ~/Obsidian/vault/workers
printf -- '---\nslug: %s\ntoday_action: %s\nhost: %s\nharness: %s\ntask_type: %s\nvcs: %s\nproject_dir: %s\nshortcut_story: %s\nstatus: primed\n---\n## Brief\n%s\n\n## Gathered context\n\n## Result\n' \
  "$WF_SLUG" "$WF_ACTION_TEXT" "$WF_HOST" "$WF_HARNESS" "$WF_TASK_TYPE" "$WF_VCS" "$WF_PROJECT_DIR" "${WF_SHORTCUT_STORY:-}" "$WF_BRIEF" \
  > ~/Obsidian/vault/workers/"$WF_SLUG".md
