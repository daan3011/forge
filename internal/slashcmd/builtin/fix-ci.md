---
name: fix-ci
description: Fetch the failing CI run's error summary through forge and fix it
argument-hint: "[run-url]"
disable-model-invocation: true
allowed-tools: mcp__forge__get_ci_failure mcp__forge__report_progress mcp__forge__get_context_pack
---
CI is red for this forge session ({session}, branch {branch}). Fix it.

1. Call `mcp__forge__get_ci_failure` with `run_url` set to `$ARGUMENTS`. If that is
   empty, call it with no `run_url`: forge then uses the latest failure of this
   session's pull request, or of its branch.
2. Read the error lines of every failed job. If a log was unavailable, say so and
   reproduce the failure locally instead.
3. Call `mcp__forge__report_progress` with a one-line diagnosis.
4. Find the root cause and reproduce it locally if you can. Fix the cause, not the
   symptom: don't skip or weaken tests to get green.
5. Run the affected tests and linters locally.
6. Finish with a short summary of the cause and the fix.
