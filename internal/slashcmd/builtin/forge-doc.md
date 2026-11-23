---
name: forge-doc
description: Write or update a doc in this project's .forge/docs
argument-hint: "[name] [what to write]"
disable-model-invocation: true
allowed-tools: mcp__forge__list_docs mcp__forge__read_doc mcp__forge__write_doc
---
Write or update a forge doc.

- Doc name: `$0` (a path relative to `.forge/docs`; add `.md` if it has no
  extension).
- What to write: `$ARGUMENTS` without its first word. If nothing is given there,
  write up the work of this conversation that belongs in that doc.

1. Call `mcp__forge__read_doc` for the doc. If it exists, update it in place:
   keep its structure and what is still true, and change only what this
   request is about.
2. If it doesn't exist, call `mcp__forge__list_docs` to match the naming and
   style of the other docs.
3. Save it with `mcp__forge__write_doc` (`create` for a new doc, `overwrite`
   for an update). Write concise markdown with headings.
4. Reply with the path and a one-line summary of the change.
