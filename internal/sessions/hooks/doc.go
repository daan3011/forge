// Package hooks ingests Claude Code hook events (P1.4): it parses the hook
// JSON, maps events to session statuses, records them, and writes the hook
// registrations into a session worktree's .claude/settings.local.json.
package hooks
