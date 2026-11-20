---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
name: refactor
description: Refactor an area without changing behaviour
variables:
  - name: target
    description: what to refactor (package, file or function)
  - name: goal
    description: what should be better afterwards
---
Refactor {target} in {repo} (branch {branch}).

Goal: {goal}

- Keep behaviour identical; the existing tests must pass unchanged.
- Add characterisation tests first if coverage is thin.
- Work in small steps and run the tests after each one.
- Summarise what moved and why.
