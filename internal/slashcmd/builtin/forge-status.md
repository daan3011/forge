---
name: forge-status
description: Update this session's summary shown in forge
argument-hint: "[summary]"
disable-model-invocation: true
allowed-tools: mcp__forge__set_session_summary
---
Update this session's summary in forge.

Call `mcp__forge__set_session_summary` with `summary` set to:

$ARGUMENTS

If that is empty, write a short markdown summary of this session yourself: what
was done, what is left, and anything the user needs to decide. Reply "Summary
updated."
