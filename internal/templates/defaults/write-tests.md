---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
name: write-tests
description: Add tests for an under-tested area
variables:
  - name: target
    description: what to test (package, file or function)
---
Write tests for {target} in {repo} (branch {branch}).

- Read the code and list the behaviours and edge cases worth covering.
- Follow the project's existing test style (table-driven where it fits).
- Don't change production code unless a bug forces it; if so, say so.
- Run the tests and make sure they pass, then summarise the coverage added.
