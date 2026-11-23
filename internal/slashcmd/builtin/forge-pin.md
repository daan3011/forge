---
name: forge-pin
description: Pin an artifact (default the last HTML or SVG file written) in forge
argument-hint: "[path]"
disable-model-invocation: true
allowed-tools: mcp__forge__pin_artifact mcp__forge__list_artifacts
---
Pin an artifact in forge.

Path: `$ARGUMENTS`

1. If the path is empty, use the last `.html` or `.svg` file you wrote in this
   conversation. If you wrote none, use the most recently modified `.html` or
   `.svg` file in this worktree (outside `.git` and `node_modules`). If there is
   none, say so and stop.
2. Call `mcp__forge__pin_artifact` with the path, a short `title`, and a few
   `tags` that describe it.
3. Reply with what was pinned.
