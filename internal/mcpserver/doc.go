// Package mcpserver is forge's MCP server (P6.5), run by `forge mcp` over
// stdio for the Claude Code agent of one forge session.
//
// The session comes from FORGE_SESSION (else the session whose worktree is
// the working directory). Every write is attributed to it: links from the
// session to what was written, the session's branch and PRs in issue
// footers, and a ping so a running forge TUI refreshes. Issues agents file
// are proposals on Home unless mcp.auto_create_issues is set; docs writes
// stay inside the session worktree's .forge/docs; there are no delete
// tools. Nothing here may write to stdout: it carries the protocol.
package mcpserver
