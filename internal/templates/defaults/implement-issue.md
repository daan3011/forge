---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
name: implement-issue
description: Implement an issue end to end, with tests
variables:
  - name: issue_title
    description: issue title
  - name: issue_body
    description: issue description (paste it if forge can't fetch it)
---
Implement issue {issue}: {issue_title}

{issue_body}

You are working in {repo} on branch {branch}. Project docs are in {docs}.

1. Read the relevant code and docs first, and restate the goal in a few lines.
2. Make the change in small, focused steps.
3. Add or update tests that cover the new behaviour.
4. Run the test suite and linters and fix what they report.
5. Summarise what changed and anything left open.
