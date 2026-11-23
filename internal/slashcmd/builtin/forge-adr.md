---
name: forge-adr
description: Log a decision record for this project
argument-hint: "[decision]"
disable-model-invocation: true
allowed-tools: mcp__forge__log_decision
---
Log a decision record.

The decision: $ARGUMENTS

Call `mcp__forge__log_decision` with:
- `title`: a short name for the decision.
- `decision`: what was decided, in one or two sentences.
- `context`, `alternatives` and `consequences`: from this conversation. Leave
  out what you don't know rather than inventing it.

Reply with the path of the record forge wrote.
