# forge

![forge: PRs, CI, Plane and Claude Code agents in one TUI](docs/forge.gif)

forge is a keyboard-driven terminal app that puts your pull requests, CI runs,
Plane board, Claude Code agent sessions, docs and design artifacts behind one
interface. Its home screen answers a single question: *what needs me right
now?* Each agent gets a name, a git worktree, a tmux window, a live status
and links to its issue and PR. Small animations (hammer strikes, sparks,
glowing embers) show work happening; turn them off with `ui.animations: off`.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/daan3011/forge/main/install.sh | sh
```

The script picks the release for your OS (macOS or Linux) and CPU (amd64 or
arm64), verifies its SHA-256 checksum and installs `forge` to
`~/.local/bin`. It then runs `forge doctor`. Options:

| Variable | Default | |
| --- | --- | --- |
| `FORGE_VERSION` | latest release | e.g. `v0.1.0` |
| `FORGE_INSTALL_DIR` | `~/.local/bin` | where the binary goes |

Or build from source (Go 1.26+): `go install github.com/daan3011/forge/cmd/forge@latest`.

**macOS, manual download:** if you download a release archive in a browser
instead of using the script, macOS quarantines the binary. Clear it once:

```sh
xattr -d com.apple.quarantine ~/.local/bin/forge
```

(The curl installer doesn't need this.)

## Requirements

| Tool | Needed for |
| --- | --- |
| [`gh`](https://cli.github.com), logged in (`gh auth login`) | PRs, CI and reviews (forge reuses its token) |
| `git` | worktrees for agent sessions |
| `tmux` 3.2+ (3.3+ recommended) | agent sessions run on forge's own tmux server |
| [`claude`](https://claude.ai/code) (Claude Code) | the agents |
| `delta` *(optional)* | nicer diffs |
| `chafa`, `rsvg-convert` *(optional)* | image and SVG previews in terminals without graphics |
| A [Plane](https://plane.so) API key *(optional)* | the Plane tab |

## First run

```sh
forge          # first launch: writes ~/.config/forge/config.yaml and walks you
               # through setup (tools, GitHub, Plane, projects, tmux keys,
               # animations, and a smoke test of the agent hooks)
forge setup    # run the guided setup again; `forge setup plane` runs one step
forge doctor   # checks everything above and prints a fix for each problem
```

Every setup step can be skipped and run again later. To try forge on a clean
install without touching your real one, point `FORGE_CONFIG_DIR` and
`FORGE_DATA_DIR` at empty folders. Don't use the `XDG_*` variables for that:
they would move claude's files too.

`forge doctor --fix` writes the default config and tmux config, and
re-registers hooks, MCP config and skills for sessions that need them. It
exits non-zero when a check fails, so you can use it in scripts (`--json`
gives machine-readable output). It is also in the palette as **Run doctor**.

Edit the config with `forge config edit`. Add your projects (`projects:`:
name, GitHub repo, local path) and, for Plane, `plane.workspace_slug` plus
your API key in `PLANE_API_KEY`. Every option is commented in the default
file.

## Using it

| Key | Does |
| --- | --- |
| `ctrl+k` | command palette: every action, tab, project and session |
| `1`–`8`, `tab` | switch tabs (Home, PRs, Pipelines, Plane, Sessions, Docs, Artifacts, Context) |
| `?` | every key on the current tab |
| `r` / `R` | refresh this tab / all tabs (also retries a failed load) |
| `ctrl+p` | switch project |
| `q` | quit |

Agents run on forge's own tmux server (`tmux -L forge`), so your own tmux
config is never touched. Inside an agent you need three keys: `Alt+d`
(back to forge), `Alt+[` (scroll) and `Alt+w` (pick another agent). See
[docs/SPEC.md](docs/SPEC.md#tmux-setup-from-a-plain-install) for the full
tmux setup.

Useful commands:

```sh
forge new --name fix-login --project app   # start an agent session
forge capture "fix login #app !high"       # quick-capture a Plane issue
forge adr --title "Use SQLite for local state"   # log a decision
forge tmux config                          # edit forge's tmux config
```

## Files

| What | Where |
| --- | --- |
| Config | `~/.config/forge/config.yaml` (`$FORGE_CONFIG_DIR`, else `$XDG_CONFIG_HOME/forge`) |
| tmux config | `~/.config/forge/tmux.conf` |
| Database, session files | `~/.local/share/forge/` (`$FORGE_DATA_DIR`, else `$XDG_DATA_HOME/forge`) |
| Logs | `~/.local/state/forge/` |

## Development

```sh
make build          # bin/forge
make lint test      # golangci-lint and go test -race
make release-snapshot   # all release archives into dist/ (needs goreleaser)
make gif            # re-record docs/forge.gif from fixture data (needs vhs)
```

Pushing a `v*` tag builds and publishes the release with goreleaser. The
spec is in [docs/SPEC.md](docs/SPEC.md) and progress in
[docs/PROGRESS.md](docs/PROGRESS.md).
