# forge

Personal Go TUI for PRs, CI, Plane, Claude Code sessions, docs and artifacts,
themed as a blacksmith's forge (embers, sparks, hammer and anvil).
Full spec: docs/SPEC.md. Progress: docs/PROGRESS.md.

## Stack
Go 1.26+, Charm v2 (charm.land/bubbletea/v2, bubbles/v2, lipgloss/v2), Harmonica, Glamour, Huh, cobra,
modernc.org/sqlite, cli/go-gh/v2, fsnotify, gen2brain/beeep,
modelcontextprotocol/go-sdk (`forge mcp`; stdout is protocol-only, log to mcp.log).

## Rules
- Never block Update(): all I/O goes through tea.Cmd and returns a Msg.
- Each tab is its own model in internal/tabs/<name>, implementing the Tab interface.
- Shared widgets live in internal/ui; colours and styles only in internal/ui/theme.go.
- Animations only via internal/ui/anim components; they must respect the
  `animations` config (full / reduced / off) and only tick while visible.
- Keybindings only in internal/ui/keymap.go (so the help overlay stays accurate).
- Every user action must also be registered as a palette command.
- Schema changes = a new numbered migration in internal/store/migrations.
- External processes (tmux, git, claude, gh, delta) go through small wrapper
  packages with interfaces, so they can be faked in tests.
- Table-driven tests for parsers and clients; teatest for key flows.
- Run `make lint test` before saying a task is done.
- Don't add dependencies beyond the stack without asking.
- Update docs/PROGRESS.md when a feature is done.