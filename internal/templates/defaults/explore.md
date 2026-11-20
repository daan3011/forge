---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
name: explore
description: Explore and explain part of the codebase, no changes
variables:
  - name: question
    description: what you want to understand
---
Explore {repo} and answer: {question}

Don't change any files. Read the code and the docs in {docs}, then explain how it
works with file:line references, and point out anything surprising or fragile.
