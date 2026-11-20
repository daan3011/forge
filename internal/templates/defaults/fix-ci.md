---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
name: fix-ci
description: Find and fix why CI fails on this branch
variables:
  - name: failing_log
    description: the failing CI log (or the relevant part of it)
---
CI is failing for {repo} on branch {branch}. Here is the failing log:

```text
{failing_log}
```

1. Find the root cause; reproduce it locally if you can.
2. Fix the cause, not the symptom. Don't skip or weaken tests to get green.
3. Run the affected tests and linters locally.
4. Explain the cause and the fix in a short summary.
