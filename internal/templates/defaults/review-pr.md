---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
name: review-pr
description: Review a pull request and report findings
---
Review pull request {pr}: {pr_title}

{pr_body}

The repo is {repo}. Check out the PR's changes and review them for:

- correctness bugs and edge cases
- missing or weak tests
- unclear names, dead code and needless complexity
- anything that contradicts the project docs in {docs}

Don't change code. Report findings ranked by severity, each with file:line and a
suggested fix.
