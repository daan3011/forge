---
name: forge-pack
description: Pull a forge context pack into the conversation
argument-hint: "[name]"
disable-model-invocation: true
allowed-tools: mcp__forge__get_context_pack Read
---
Load the forge context pack `$ARGUMENTS`.

1. Call `mcp__forge__get_context_pack` with `name` set to `$ARGUMENTS`. If the
   name is empty, call it with no name and list the available packs instead.
2. Read the pack's preamble and follow it for the rest of this session.
3. Read every file it lists. Mention the missing ones.
4. Reply with the pack name, how many files you read, and one line on what they
   cover.
