---
# forge session template. The body below the front matter becomes the first
# prompt. {name} placeholders are filled from the session (repo, branch, docs),
# from the linked issue / PR, or asked for in the new-session form. Write \{ for
# a literal brace. Variables you declare here get a description and a default.
# "Fix this" (F on a red run or check) fills every variable below.
name: fix-ci
description: Find and fix why CI fails on this branch
variables:
  - name: ci_workflow
    description: the failing workflow(s)
  - name: ci_job
    description: the failing job(s)
  - name: ci_run
    description: link(s) to the failing run
  - name: commit_message
    description: the message of the commit CI ran on
  - name: failing_log
    description: the failing CI log (or the relevant part of it)
---
CI is failing for {repo} on branch {branch}.

- Workflow: {ci_workflow}
- Job: {ci_job}
- Run: {ci_run}
- Last commit: {commit_message}

The error context from the failing log:

```text
{failing_log}
```

1. Find the root cause; reproduce it locally if you can.
2. Fix the cause, not the symptom. Don't skip or weaken tests to get green.
3. Run the affected tests and linters locally.
4. Explain the cause and the fix in a short summary.

<!-- forge:mcp-tools v1 -->
forge tools (mcp__forge__*): get_context_pack first; create_issue for follow-ups you won't do now (a proposal
the user triages; list_issues first); log_decision for real design choices; write_doc for notes in .forge/docs;
pin_artifact for outputs; report_progress at milestones; set_session_summary before you finish.
