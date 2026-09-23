#!/usr/bin/env bash
# Write the worker note (frontmatter + empty sections) from PAWL_* env vars.
set -euo pipefail
mkdir -p ~/Obsidian/vault/workers
printf -- '---\nslug: %s\ntoday_action: %s\nhost: %s\nharness: %s\ntask_type: %s\nvcs: %s\nproject_dir: %s\nshortcut_story: %s\nstatus: primed\n---\n## Brief\n%s\n\n## Gathered context\n\n## Result\n' \
  "$PAWL_SLUG" "$PAWL_ACTION_TEXT" "$PAWL_HOST" "$PAWL_HARNESS" "$PAWL_TASK_TYPE" "$PAWL_VCS" "$PAWL_PROJECT_DIR" "${PAWL_SHORTCUT_STORY:-}" "$PAWL_BRIEF" \
  > ~/Obsidian/vault/workers/"$PAWL_SLUG".md
