---
name: forge-ticket
description: File a proposed Plane issue for this session (#project !priority +label tokens work as in forge capture)
argument-hint: "[title #project !priority +label, then details]"
disable-model-invocation: true
allowed-tools: mcp__forge__create_issue mcp__forge__list_issues
---
File a follow-up issue from this forge session.

The user's request, verbatim:

```text
$ARGUMENTS
```

1. Take the first line of the request as the capture text. It is a title with
   optional tokens: `#project`, `!priority` (e.g. `!high`, `!urg`) and `+label`.
   Don't parse the tokens yourself.
2. Call `mcp__forge__list_issues` with a few words of the title to check it isn't
   already filed. If it is, say which issue and stop.
3. Call `mcp__forge__create_issue` with `capture` set to that first line,
   exactly as written. Leave `title`, `priority` and `labels` empty.
   Write a markdown `description` from the rest of the request and what you know
   from this conversation: the problem, why it matters, what done looks like, and
   the files or functions involved.
4. Reply with the issue id forge returned (P-<n>) and its title, nothing more.
